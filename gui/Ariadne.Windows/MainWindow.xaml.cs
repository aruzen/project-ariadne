using System.Collections;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using Ariadne.Windows.Protocol;
using Ariadne.Windows.Views;

namespace Ariadne.Windows;

public partial class MainWindow : Window
{
    private readonly Dictionary<ulong, TerminalPaneView> terminalViews = new();
    private readonly Dictionary<ulong, ToolPaneView> toolViews = new();
    private readonly ToolPaneRendererRegistry toolRenderers = new();
    private readonly CancellationTokenSource lifetime = new();
    private AriadneClient? client;
    private Snapshot snapshot = new();
    private ulong workspaceId;
    private ulong windowId;
    private ulong paneId;
    private ulong previewPaneId;
    private ulong zoomPaneId;
    private bool prefixArmed;
    private bool updatingPickers;
    private bool closing;

    public MainWindow()
    {
        InitializeComponent();
        Loaded += OnLoaded;
        Closed += OnClosed;
    }

    private async void OnLoaded(object sender, RoutedEventArgs e) => await ConnectAsync();

    private async Task ConnectAsync()
    {
        SetStatus("Connecting…");
        await DisconnectAsync();
        try
        {
            var value = await AriadneClient.ConnectAsync(cancellationToken: lifetime.Token);
            client = value;
            value.Store.Changed += StoreOnChanged;
            value.NavigateRequested += NavigateAsync;
            value.Failed += ConnectionOnFailed;
            snapshot = value.Store.Current;
            SelectInitialLocation();
            Refresh();
            if (paneId != 0)
            {
                var focused = await value.SetFocusAsync(paneId, lifetime.Token);
                ApplyFocus(focused.Focus);
                Refresh();
            }
            SetStatus("Connected");
        }
        catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
        {
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
        }
    }

    private void SelectInitialLocation()
    {
        var stashedWindows = snapshot.StashedWindows.Select(value => value.WindowId).ToHashSet();
        var workspace = snapshot.Workspaces.FirstOrDefault(value =>
            value.WindowIds.Any(id => !stashedWindows.Contains(id))) ?? snapshot.Workspaces.FirstOrDefault();
        workspaceId = workspace?.Id ?? 0;
        windowId = workspace?.WindowIds.FirstOrDefault(id => !stashedWindows.Contains(id)) ?? 0;
        var window = FindWindow(windowId);
        paneId = window?.Layout is null ? 0 : FirstPane(window.Layout);
    }

    private void StoreOnChanged(Snapshot value)
    {
        _ = Dispatcher.BeginInvoke(() =>
        {
            var newAttention = value.Attentions.FirstOrDefault(attention =>
                attention.AcknowledgedAt is null && snapshot.Attentions.All(previous => previous.Id != attention.Id));
            snapshot = value;
            NormalizeLocation();
            Refresh();
            if (newAttention is not null)
            {
                SetStatus($"{newAttention.Severity}: {newAttention.Message}");
            }
        });
    }

    private void NormalizeLocation()
    {
        if (previewPaneId != 0 && snapshot.Panes.All(value => value.Id != previewPaneId))
        {
            previewPaneId = 0;
        }
        var window = FindWindow(windowId);
        if (window is null || snapshot.StashedWindows.Any(value => value.WindowId == windowId))
        {
            SelectInitialLocation();
            return;
        }
        if (paneId != 0 && !ContainsPane(window.Layout, paneId))
        {
            paneId = window.Layout is null ? 0 : FirstPane(window.Layout);
        }
    }

    private void Refresh()
    {
        RevisionText.Text = $"revision {snapshot.Revision}";
        var unacknowledged = snapshot.Attentions.Count(value => value.AcknowledgedAt is null);
        AttentionButton.Content = unacknowledged == 0 ? "Alerts" : $"Alerts ({unacknowledged})";
        AttentionButton.IsEnabled = unacknowledged != 0;
        RefreshPickers();
        CleanupViews();
        PaneHost.Children.Clear();
        if (previewPaneId != 0)
        {
            var preview = snapshot.Panes.FirstOrDefault(value => value.Id == previewPaneId);
            if (preview is not null)
            {
                PaneHost.Children.Add(BuildPane(preview));
                return;
            }
        }
        if (zoomPaneId != 0)
        {
            var zoomed = snapshot.Panes.FirstOrDefault(value => value.Id == zoomPaneId);
            if (zoomed is not null && FindWindow(windowId)?.Layout is { } layout && ContainsPane(layout, zoomPaneId))
            {
                PaneHost.Children.Add(BuildPane(zoomed));
                return;
            }
            zoomPaneId = 0;
        }
        var window = FindWindow(windowId);
        if (window?.Layout is null)
        {
            PaneHost.Children.Add(new TextBlock
            {
                Text = "No panes. Choose New terminal to create one.",
                Foreground = System.Windows.Media.Brushes.Gray,
                HorizontalAlignment = HorizontalAlignment.Center,
                VerticalAlignment = VerticalAlignment.Center,
            });
            return;
        }
        PaneHost.Children.Add(BuildLayout(window.Layout));
    }

    private void RefreshPickers()
    {
        updatingPickers = true;
        try
        {
            var stashedWindows = snapshot.StashedWindows.Select(value => value.WindowId).ToHashSet();
            WorkspacePicker.ItemsSource = snapshot.Workspaces;
            WorkspacePicker.SelectedItem = snapshot.Workspaces.FirstOrDefault(value => value.Id == workspaceId);
            var workspace = snapshot.Workspaces.FirstOrDefault(value => value.Id == workspaceId);
            var windows = workspace?.WindowIds
                .Where(id => !stashedWindows.Contains(id))
                .Select(FindWindow)
                .Where(value => value is not null)
                .Cast<WindowModel>()
                .ToList() ?? [];
            WindowPicker.ItemsSource = windows;
            WindowPicker.SelectedItem = windows.FirstOrDefault(value => value.Id == windowId);
        }
        finally
        {
            updatingPickers = false;
        }
    }

    private FrameworkElement BuildLayout(LayoutNode node)
    {
        if (node.Kind == "pane")
        {
            var pane = snapshot.Panes.FirstOrDefault(value => value.Id == node.PaneId);
            return pane is null ? MissingPane(node.PaneId) : BuildPane(pane);
        }
        if (node.Children.Count == 0)
        {
            return MissingPane(0);
        }

        var horizontal = node.Direction == "horizontal";
        var grid = new Grid();
        for (var index = 0; index < node.Children.Count; index++)
        {
            var weight = index < node.Weights.Count && node.Weights[index] > 0 ? node.Weights[index] : 1;
            if (horizontal)
            {
                grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(weight, GridUnitType.Star) });
            }
            else
            {
                grid.RowDefinitions.Add(new RowDefinition { Height = new GridLength(weight, GridUnitType.Star) });
            }
            var child = BuildLayout(node.Children[index]);
            if (horizontal)
            {
                Grid.SetColumn(child, index * 2);
            }
            else
            {
                Grid.SetRow(child, index * 2);
            }
            grid.Children.Add(child);
            if (index == node.Children.Count - 1)
            {
                continue;
            }
            var separator = new GridSplitter
            {
                Background = new System.Windows.Media.SolidColorBrush(System.Windows.Media.Color.FromRgb(55, 62, 72)),
                ResizeBehavior = GridResizeBehavior.PreviousAndNext,
                ResizeDirection = horizontal ? GridResizeDirection.Columns : GridResizeDirection.Rows,
                HorizontalAlignment = HorizontalAlignment.Stretch,
                VerticalAlignment = VerticalAlignment.Stretch,
            };
            if (horizontal)
            {
                grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(3) });
                Grid.SetColumn(separator, index * 2 + 1);
            }
            else
            {
                grid.RowDefinitions.Add(new RowDefinition { Height = new GridLength(3) });
                Grid.SetRow(separator, index * 2 + 1);
            }
            separator.DragCompleted += (_, _) => _ = PersistSplitWeightsAsync(node.SplitId, grid, horizontal, node.Children.Count);
            grid.Children.Add(separator);
        }
        return grid;
    }

    private FrameworkElement BuildPane(PaneModel pane)
    {
        if (pane.Kind == "terminal" && pane.Terminal?.Id is ulong terminalId)
        {
            if (!terminalViews.TryGetValue(pane.Id, out var view) || view.TerminalId != terminalId)
            {
                if (view is not null)
                {
                    view.Dispose();
                }
                view = new TerminalPaneView(client!, pane);
                view.FocusRequested += (_, _) => _ = FocusPaneAsync(pane.Id);
                view.ConnectionFailed += (_, error) => SetStatus(error.Message);
                view.ActionRequested += PaneActionRequested;
                view.CopyRequested += text => _ = ShareClipboardAsync(view.PaneId, text);
                view.PasteRequested += (_, _) => _ = PasteClipboardAsync(view);
                terminalViews[pane.Id] = view;
            }
            Detach(view);
            view.Update(pane, pane.Id == paneId || pane.Id == previewPaneId);
            return view;
        }
        if (!toolViews.TryGetValue(pane.Id, out var tool))
        {
            tool = new ToolPaneView(toolRenderers, pane, FindToolInstance(pane.Tool));
            tool.FocusRequested += (_, _) => _ = FocusPaneAsync(pane.Id);
            tool.ActionRequested += PaneActionRequested;
            toolViews[pane.Id] = tool;
        }
        Detach(tool);
        tool.Update(pane, FindToolInstance(pane.Tool), pane.Id == paneId || pane.Id == previewPaneId);
        return tool;
    }

    private static void Detach(FrameworkElement element)
    {
        if (element.Parent is Panel panel)
        {
            panel.Children.Remove(element);
        }
        else if (element.Parent is Decorator decorator)
        {
            decorator.Child = null;
        }
    }

    private void CleanupViews()
    {
        var panes = snapshot.Panes.ToDictionary(value => value.Id);
        foreach (var id in terminalViews.Keys.Where(id =>
                     !panes.TryGetValue(id, out var pane) || pane.Terminal?.Id != terminalViews[id].TerminalId).ToList())
        {
            terminalViews[id].Dispose();
            terminalViews.Remove(id);
        }
        foreach (var id in toolViews.Keys.Where(id =>
                     !panes.TryGetValue(id, out var pane) || pane.Kind == "terminal" && pane.Terminal?.Id is not null).ToList())
        {
            toolViews[id].Dispose();
            toolViews.Remove(id);
        }
    }

    private async Task FocusPaneAsync(ulong id)
    {
        if (client is null || id == paneId)
        {
            return;
        }
        try
        {
            var result = await client.SetFocusAsync(id, lifetime.Token);
            previewPaneId = 0;
            ApplyFocus(result.Focus);
            Refresh();
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
        }
    }

    private async Task NavigateAsync(ulong id)
    {
        var completion = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        await Dispatcher.BeginInvoke(async () =>
        {
            try
            {
                var pane = snapshot.Panes.FirstOrDefault(value => value.Id == id);
                if (pane is null || pane.Transient)
                {
                    throw new AriadneRemoteException("not_found", "pane not found");
                }
                var stashed = snapshot.StashedPanes.Any(value => value.PaneId == id) ||
                              snapshot.StashedWindows.Any(value => value.WindowId == pane.WindowId);
                if (stashed)
                {
                    previewPaneId = id;
                    Refresh();
                }
                else
                {
                    var result = await client!.SetFocusAsync(id, lifetime.Token);
                    previewPaneId = 0;
                    ApplyFocus(result.Focus);
                    Refresh();
                }
                Activate();
                completion.SetResult();
            }
            catch (Exception ex)
            {
                completion.SetException(ex);
            }
        });
        await completion.Task.ConfigureAwait(false);
    }

    private void ApplyFocus(FrontendState focus)
    {
        workspaceId = focus.WorkspaceId;
        windowId = focus.WindowId;
        paneId = focus.PaneId;
    }

    private async void WorkspacePicker_OnSelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (updatingPickers || WorkspacePicker.SelectedItem is not WorkspaceModel selected || selected.Id == workspaceId)
        {
            return;
        }
        var stashed = snapshot.StashedWindows.Select(value => value.WindowId).ToHashSet();
        var target = selected.WindowIds.FirstOrDefault(id => !stashed.Contains(id));
        if (target != 0)
        {
            await SelectWindowAsync(target);
        }
    }

    private async void WindowPicker_OnSelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (!updatingPickers && WindowPicker.SelectedItem is WindowModel selected && selected.Id != windowId)
        {
            await SelectWindowAsync(selected.Id);
        }
    }

    private async Task SelectWindowAsync(ulong id)
    {
        if (client is null)
        {
            return;
        }
        try
        {
            var result = await client.SelectWindowAsync(id, lifetime.Token);
            previewPaneId = 0;
            ApplyFocus(result.Focus);
            Refresh();
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
            RefreshPickers();
        }
    }

    private async void NewTerminal_OnClick(object sender, RoutedEventArgs e) =>
        await CreateTerminalAsync(paneId, "horizontal");

    private async void SplitBelow_OnClick(object sender, RoutedEventArgs e) =>
        await CreateTerminalAsync(paneId, "vertical");

    private async Task CreateTerminalAsync(ulong targetPaneId, string direction)
    {
        if (client is null || windowId == 0)
        {
            return;
        }
        try
        {
            var result = await client.NewTerminalAsync(
                windowId, targetPaneId, direction, ["powershell.exe", "-NoLogo"],
                Environment.CurrentDirectory, CurrentEnvironment(), 120, 30, lifetime.Token);
            var focused = await client.SetFocusAsync(result.Pane.Id, lifetime.Token);
            ApplyFocus(focused.Focus);
            SetStatus("Terminal created");
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
        }
    }

    private async void PaneActionRequested(object? sender, PaneActionEventArgs e) =>
        await ExecutePaneActionAsync(e.PaneId, e.Action);

    private async Task ExecutePaneActionAsync(ulong targetPaneId, PaneAction action)
    {
        if (client is null)
        {
            return;
        }
        try
        {
            switch (action)
            {
                case PaneAction.SplitRight:
                    await CreateTerminalAsync(targetPaneId, "horizontal");
                    return;
                case PaneAction.SplitBelow:
                    await CreateTerminalAsync(targetPaneId, "vertical");
                    return;
                case PaneAction.Restart:
                    await client.RestartTerminalAsync(targetPaneId, CurrentEnvironment(), 120, 30, lifetime.Token);
                    SetStatus($"Pane {targetPaneId} restarted");
                    break;
                case PaneAction.RunCommand:
                {
                    var prompt = new TextPromptWindow(this, "Run command", "PowerShell command:");
                    if (prompt.ShowDialog() != true)
                    {
                        return;
                    }
                    await client.RunTerminalAsync(targetPaneId,
                        ["powershell.exe", "-NoLogo", "-Command", prompt.Value], Environment.CurrentDirectory,
                        CurrentEnvironment(), 120, 30, lifetime.Token);
                    SetStatus($"Command started in pane {targetPaneId}");
                    break;
                }
                case PaneAction.Stop:
                    await client.StopTerminalAsync(targetPaneId, lifetime.Token);
                    SetStatus($"Pane {targetPaneId} stopped");
                    break;
                case PaneAction.Stash:
                    await client.StashPaneAsync(targetPaneId, lifetime.Token);
                    SetStatus($"Pane {targetPaneId} stashed");
                    break;
                case PaneAction.Delete:
                    if (MessageBox.Show(this, $"Delete pane {targetPaneId}?", "Ariadne",
                            MessageBoxButton.OKCancel, MessageBoxImage.Warning) != MessageBoxResult.OK)
                    {
                        return;
                    }
                    await client.DeletePaneAsync(targetPaneId, lifetime.Token);
                    SetStatus($"Pane {targetPaneId} deleted");
                    break;
            }
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
        }
    }

    private static string[] CurrentEnvironment() => Environment.GetEnvironmentVariables()
        .Cast<DictionaryEntry>()
        .Select(value => $"{value.Key}={value.Value}")
        .ToArray();

    private async Task ShareClipboardAsync(ulong targetPaneId, string text)
    {
        try
        {
            if (client is not null)
            {
                await client.WriteClipboardAsync(targetPaneId, text, lifetime.Token);
            }
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
        }
    }

    private async Task PasteClipboardAsync(TerminalPaneView view)
    {
        try
        {
            string text;
            if (client is not null)
            {
                text = (await client.ReadClipboardAsync(view.PaneId, lifetime.Token)).Text;
            }
            else
            {
                text = Clipboard.ContainsText(TextDataFormat.UnicodeText)
                    ? Clipboard.GetText(TextDataFormat.UnicodeText)
                    : "";
            }
            if (text.Length != 0)
            {
                view.Paste(text);
            }
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
        }
    }

    private void Stash_OnClick(object sender, RoutedEventArgs e)
    {
        var menu = new ContextMenu();
        foreach (var stashed in snapshot.StashedPanes)
        {
            var pane = snapshot.Panes.FirstOrDefault(value => value.Id == stashed.PaneId);
            var title = string.IsNullOrWhiteSpace(pane?.Title) ? pane?.Kind ?? "pane" : pane.Title;
            var item = new MenuItem { Header = $"Restore pane {stashed.PaneId}: {title}" };
            item.Click += async (_, _) => await RestorePaneAsync(stashed.PaneId);
            menu.Items.Add(item);
        }
        if (snapshot.StashedPanes.Count != 0 && snapshot.StashedWindows.Count != 0)
        {
            menu.Items.Add(new Separator());
        }
        foreach (var stashed in snapshot.StashedWindows)
        {
            var window = FindWindow(stashed.WindowId);
            var item = new MenuItem { Header = $"Restore window {stashed.WindowId}: {window?.Name ?? "window"}" };
            item.Click += async (_, _) => await RestoreWindowAsync(stashed.WindowId);
            menu.Items.Add(item);
        }
        if (menu.Items.Count == 0)
        {
            menu.Items.Add(new MenuItem { Header = "Stash is empty", IsEnabled = false });
        }
        menu.PlacementTarget = StashButton;
        menu.Placement = System.Windows.Controls.Primitives.PlacementMode.Bottom;
        menu.IsOpen = true;
    }

    private async Task RestorePaneAsync(ulong id)
    {
        try
        {
            var restored = await client!.RestorePaneAsync(id, lifetime.Token);
            var focused = await client.SetFocusAsync(restored.Pane.Id, lifetime.Token);
            previewPaneId = 0;
            ApplyFocus(focused.Focus);
            SetStatus($"Pane {id} restored");
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
        }
    }

    private async Task RestoreWindowAsync(ulong id)
    {
        try
        {
            var restored = await client!.RestoreWindowAsync(id, lifetime.Token);
            var selected = await client.SelectWindowAsync(restored.Window.Id, lifetime.Token);
            previewPaneId = 0;
            ApplyFocus(selected.Focus);
            SetStatus($"Window {id} restored");
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
        }
    }

    private async Task PersistSplitWeightsAsync(ulong splitId, Grid grid, bool horizontal, int childCount)
    {
        if (client is null || splitId == 0)
        {
            return;
        }
        try
        {
            var weights = new uint[childCount];
            for (var index = 0; index < childCount; index++)
            {
                var length = horizontal
                    ? grid.ColumnDefinitions[index * 2].ActualWidth
                    : grid.RowDefinitions[index * 2].ActualHeight;
                weights[index] = (uint)Math.Max(1, Math.Round(length));
            }
            await client.ResizeSplitAsync(splitId, weights, lifetime.Token);
            SetStatus($"Split {splitId} resized");
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
        }
    }

    private async void MainWindow_OnPreviewKeyDown(object sender, KeyEventArgs e)
    {
        var controlA = e.Key == Key.A && Keyboard.Modifiers.HasFlag(ModifierKeys.Control);
        if (!prefixArmed)
        {
            if (e.Key == Key.P && Keyboard.Modifiers.HasFlag(ModifierKeys.Control) && Keyboard.Modifiers.HasFlag(ModifierKeys.Shift))
            {
                e.Handled = true;
                await ShowCommandPaletteAsync();
                return;
            }
            if (controlA)
            {
                prefixArmed = true;
                e.Handled = true;
                SetStatus("prefix: Ctrl-a");
            }
            return;
        }

        prefixArmed = false;
        e.Handled = true;
        if (controlA)
        {
            if (terminalViews.TryGetValue(paneId, out var terminal))
            {
                terminal.SendInput("\x01");
            }
            SetStatus("Ctrl-a sent");
            return;
        }
        switch (e.Key)
        {
            case Key.H:
                await MoveFocusAsync(-1, 0);
                break;
            case Key.J:
                await MoveFocusAsync(0, 1);
                break;
            case Key.K:
                await MoveFocusAsync(0, -1);
                break;
            case Key.L:
                await MoveFocusAsync(1, 0);
                break;
            case Key.Z:
                zoomPaneId = zoomPaneId == 0 ? paneId : 0;
                Refresh();
                SetStatus(zoomPaneId == 0 ? "Zoom off" : $"Pane {paneId} zoomed");
                break;
            case Key.D5 when Keyboard.Modifiers.HasFlag(ModifierKeys.Shift):
                await CreateTerminalAsync(paneId, "horizontal");
                break;
            case Key.OemQuotes when Keyboard.Modifiers.HasFlag(ModifierKeys.Shift):
                await CreateTerminalAsync(paneId, "vertical");
                break;
            case Key.OemSemicolon when Keyboard.Modifiers.HasFlag(ModifierKeys.Shift):
                await ShowCommandPaletteAsync();
                break;
            default:
                SetStatus("Unknown prefix key");
                break;
        }
    }

    private async void Palette_OnClick(object sender, RoutedEventArgs e) => await ShowCommandPaletteAsync();

    private async Task ShowCommandPaletteAsync()
    {
        var items = BuildCommandPaletteItems();
        var palette = new CommandPaletteWindow(this, items);
        if (palette.ShowDialog() != true || palette.Selected is null)
        {
            return;
        }
        await ExecutePaletteCommandAsync(palette.Selected.Id);
    }

    private List<CommandPaletteItem> BuildCommandPaletteItems()
    {
        var items = new List<CommandPaletteItem>
        {
            new("split-right", "Pane: split right"),
            new("split-below", "Pane: split below"),
            new("zoom", zoomPaneId == 0 ? "Pane: zoom" : "Pane: leave zoom"),
            new("focus-left", "Pane: focus left"),
            new("focus-down", "Pane: focus down"),
            new("focus-up", "Pane: focus up"),
            new("focus-right", "Pane: focus right"),
            new("reconnect", "Frontend: reconnect"),
        };
        var pane = snapshot.Panes.FirstOrDefault(value => value.Id == paneId);
        var state = pane?.Terminal?.State;
        if (pane?.Kind == "terminal")
        {
            if (state is "exited" or "failed" or "placeholder")
            {
                items.Add(new("restart", "Terminal: restart original command"));
                items.Add(new("run", "Terminal: run another command…"));
                items.Add(new("delete", "Pane: delete"));
            }
            if (state == "running")
            {
                items.Add(new("stop", "Terminal: stop"));
            }
        }
        if (pane is not null && !pane.Transient)
        {
            items.Add(new("stash", "Pane: stash"));
        }
        foreach (var stashed in snapshot.StashedPanes)
        {
            items.Add(new($"restore-pane:{stashed.PaneId}", $"Stash: restore pane {stashed.PaneId}"));
        }
        foreach (var stashed in snapshot.StashedWindows)
        {
            items.Add(new($"restore-window:{stashed.WindowId}", $"Stash: restore window {stashed.WindowId}"));
        }
        return items;
    }

    private async Task ExecutePaletteCommandAsync(string command)
    {
        switch (command)
        {
            case "split-right": await CreateTerminalAsync(paneId, "horizontal"); return;
            case "split-below": await CreateTerminalAsync(paneId, "vertical"); return;
            case "zoom":
                zoomPaneId = zoomPaneId == 0 ? paneId : 0;
                Refresh();
                return;
            case "focus-left": await MoveFocusAsync(-1, 0); return;
            case "focus-down": await MoveFocusAsync(0, 1); return;
            case "focus-up": await MoveFocusAsync(0, -1); return;
            case "focus-right": await MoveFocusAsync(1, 0); return;
            case "restart": await ExecutePaneActionAsync(paneId, PaneAction.Restart); return;
            case "run": await ExecutePaneActionAsync(paneId, PaneAction.RunCommand); return;
            case "stop": await ExecutePaneActionAsync(paneId, PaneAction.Stop); return;
            case "stash": await ExecutePaneActionAsync(paneId, PaneAction.Stash); return;
            case "delete": await ExecutePaneActionAsync(paneId, PaneAction.Delete); return;
            case "reconnect": await ConnectAsync(); return;
        }
        if (command.StartsWith("restore-pane:", StringComparison.Ordinal) &&
            ulong.TryParse(command["restore-pane:".Length..], out var restorePane))
        {
            await RestorePaneAsync(restorePane);
        }
        else if (command.StartsWith("restore-window:", StringComparison.Ordinal) &&
                 ulong.TryParse(command["restore-window:".Length..], out var restoreWindow))
        {
            await RestoreWindowAsync(restoreWindow);
        }
    }

    private void Attention_OnClick(object sender, RoutedEventArgs e)
    {
        var menu = new ContextMenu();
        foreach (var attention in snapshot.Attentions
                     .Where(value => value.AcknowledgedAt is null)
                     .OrderByDescending(value => value.UpdatedAt))
        {
            var message = string.IsNullOrWhiteSpace(attention.Message) ? attention.Class : attention.Message;
            var item = new MenuItem { Header = $"[{attention.Severity}] Pane {attention.PaneId}: {message}" };
            item.Click += async (_, _) => await OpenAttentionAsync(attention);
            menu.Items.Add(item);
        }
        if (menu.Items.Count == 0)
        {
            menu.Items.Add(new MenuItem { Header = "No unacknowledged alerts", IsEnabled = false });
        }
        menu.PlacementTarget = AttentionButton;
        menu.Placement = System.Windows.Controls.Primitives.PlacementMode.Top;
        menu.IsOpen = true;
    }

    private async Task OpenAttentionAsync(AttentionModel attention)
    {
        if (client is null)
        {
            return;
        }
        try
        {
            await NavigateAsync(attention.PaneId);
            await client.AcknowledgeAttentionAsync(attention.Id, lifetime.Token);
            SetStatus($"Alert {attention.Id} acknowledged");
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
        }
    }

    private async Task MoveFocusAsync(int horizontal, int vertical)
    {
        if (zoomPaneId != 0 || CurrentPaneView(paneId) is not { } current)
        {
            return;
        }
        var origin = BoundsInHost(current);
        var originX = origin.Left + origin.Width / 2;
        var originY = origin.Top + origin.Height / 2;
        ulong bestId = 0;
        var bestScore = double.MaxValue;
        foreach (var candidatePane in snapshot.Panes)
        {
            if (candidatePane.Id == paneId || CurrentPaneView(candidatePane.Id) is not { } candidate || !candidate.IsVisible)
            {
                continue;
            }
            var bounds = BoundsInHost(candidate);
            var deltaX = bounds.Left + bounds.Width / 2 - originX;
            var deltaY = bounds.Top + bounds.Height / 2 - originY;
            var primary = horizontal != 0 ? deltaX * horizontal : deltaY * vertical;
            if (primary <= 0)
            {
                continue;
            }
            var secondary = horizontal != 0 ? Math.Abs(deltaY) : Math.Abs(deltaX);
            var score = primary * 1000 + secondary;
            if (score < bestScore)
            {
                bestScore = score;
                bestId = candidatePane.Id;
            }
        }
        if (bestId != 0)
        {
            await FocusPaneAsync(bestId);
        }
    }

    private FrameworkElement? CurrentPaneView(ulong id)
    {
        if (terminalViews.TryGetValue(id, out var terminal) && terminal.IsDescendantOf(PaneHost))
        {
            return terminal;
        }
        if (toolViews.TryGetValue(id, out var tool) && tool.IsDescendantOf(PaneHost))
        {
            return tool;
        }
        return null;
    }

    private System.Windows.Rect BoundsInHost(FrameworkElement element)
    {
        var topLeft = element.TransformToAncestor(PaneHost).Transform(new System.Windows.Point(0, 0));
        return new System.Windows.Rect(topLeft, new System.Windows.Size(element.ActualWidth, element.ActualHeight));
    }

    private async void Reconnect_OnClick(object sender, RoutedEventArgs e) => await ConnectAsync();

    private void ConnectionOnFailed(Exception error) => Dispatcher.BeginInvoke(() => SetStatus(error.Message));
    private void SetStatus(string value) => StatusText.Text = value;
    private WindowModel? FindWindow(ulong id) => snapshot.Windows.FirstOrDefault(value => value.Id == id);
    private ToolInstanceModel? FindToolInstance(ToolDescriptor? descriptor) => descriptor is null
        ? null
        : snapshot.ToolInstances.FirstOrDefault(value =>
            value.Descriptor.Provider == descriptor.Provider && value.Descriptor.Type == descriptor.Type &&
            value.Descriptor.Instance == descriptor.Instance);

    private static ulong FirstPane(LayoutNode node) =>
        node.Kind == "pane" ? node.PaneId : node.Children.Select(FirstPane).FirstOrDefault(id => id != 0);

    private static bool ContainsPane(LayoutNode? node, ulong id) =>
        node is not null && (node.Kind == "pane" ? node.PaneId == id : node.Children.Any(child => ContainsPane(child, id)));

    private static TextBlock MissingPane(ulong id) => new()
    {
        Text = id == 0 ? "Empty layout" : $"Pane {id} is unavailable",
        Foreground = System.Windows.Media.Brushes.Gray,
        HorizontalAlignment = HorizontalAlignment.Center,
        VerticalAlignment = VerticalAlignment.Center,
    };

    private async Task DisconnectAsync()
    {
        var old = client;
        client = null;
        foreach (var view in terminalViews.Values)
        {
            view.Dispose();
        }
        terminalViews.Clear();
        foreach (var view in toolViews.Values)
        {
            view.Dispose();
        }
        toolViews.Clear();
        if (old is not null)
        {
            old.Store.Changed -= StoreOnChanged;
            old.NavigateRequested -= NavigateAsync;
            old.Failed -= ConnectionOnFailed;
            await old.DisposeAsync();
        }
    }

    private async void OnClosed(object? sender, EventArgs e)
    {
        if (closing)
        {
            return;
        }
        closing = true;
        lifetime.Cancel();
        await DisconnectAsync();
        lifetime.Dispose();
    }
}
