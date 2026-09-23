using System.Collections;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using System.Windows.Interop;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using Ariadne.Windows.Protocol;
using Ariadne.Windows.Views;

namespace Ariadne.Windows;

public partial class MainWindow : Window
{
    private readonly Dictionary<ulong, TerminalPaneView> terminalViews = new();
    private readonly Dictionary<ulong, ToolPaneView> toolViews = new();
    private ToolPaneRendererRegistry? toolRenderers;
    private readonly CancellationTokenSource lifetime = new();
    private readonly SemaphoreSlim interactionGate = new(1, 1);
    private readonly SemaphoreSlim focusGate = new(1, 1);
    private AriadneClient? client;
    private Snapshot snapshot = new();
    private ulong workspaceId;
    private ulong windowId;
    private ulong paneId;
    private ulong requestedFocusPaneId;
    private ulong previewPaneId;
    private ulong zoomPaneId;
    private bool prefixArmed;
    private bool updatingPickers;
    private bool closing;
    private bool smokePluginMode;
    private Dictionary<string, string> keybindings = LegacyKeybindings();
    private readonly List<string> keySequence = [];

    public MainWindow()
    {
        InitializeComponent();
        Loaded += OnLoaded;
        Closed += OnClosed;
        Deactivated += (_, _) => CancelPrefix();
        PreviewMouseDown += (_, _) => CancelPrefix();
    }

    private void CancelPrefix()
    {
        if (!prefixArmed && keySequence.Count == 0)
        {
            return;
        }
        prefixArmed = false;
        keySequence.Clear();
        SetStatus("Prefix cancelled");
    }

    private async void OnLoaded(object sender, RoutedEventArgs e)
    {
        var arguments = Environment.GetCommandLineArgs();
        smokePluginMode = arguments.Contains("--smoke-test-plugin", StringComparer.OrdinalIgnoreCase);
        var connected = await ConnectAsync();
        var smoke = arguments.Contains("--smoke-test", StringComparer.OrdinalIgnoreCase) ||
                    smokePluginMode;
        if (!smoke)
        {
            return;
        }
        if (connected && smokePluginMode)
        {
            var external = snapshot.Panes.LastOrDefault(value => value.Tool is not null && value.Tool.Provider != "ariadne");
            var externalWindow = external is null ? null : FindWindow(external.WindowId);
            var externalWorkspace = externalWindow is null
                ? null
                : snapshot.Workspaces.FirstOrDefault(value => value.WindowIds.Contains(externalWindow.Id));
            if (external is not null && externalWindow is not null && externalWorkspace is not null)
            {
                workspaceId = externalWorkspace.Id;
                windowId = externalWindow.Id;
                paneId = external.Id;
                zoomPaneId = external.Id;
                Refresh();
            }
        }
        var passed = connected && await WaitForSmokeConditionAsync(smokePluginMode);
        var screenshot = arguments.FirstOrDefault(value => value.StartsWith("--screenshot=", StringComparison.OrdinalIgnoreCase));
        if (screenshot is not null)
        {
            SaveScreenshot(screenshot["--screenshot=".Length..]);
        }
        else if (arguments.Contains("--smoke-test-screenshot", StringComparer.OrdinalIgnoreCase))
        {
            SaveScreenshot(Path.Combine(Path.GetTempPath(), "ariadne-gui-smoke.png"));
        }
        Application.Current.Shutdown(passed ? 0 : 1);
    }

    private void SaveScreenshot(string path)
    {
        if (string.IsNullOrWhiteSpace(path) || ActualWidth < 1 || ActualHeight < 1)
        {
            throw new ArgumentException("invalid screenshot path or window dimensions", nameof(path));
        }
        UpdateLayout();
        var dpi = VisualTreeHelper.GetDpi(this);
        var bitmap = new RenderTargetBitmap(
            Math.Max(1, (int)Math.Ceiling(ActualWidth * dpi.DpiScaleX)),
            Math.Max(1, (int)Math.Ceiling(ActualHeight * dpi.DpiScaleY)),
            dpi.PixelsPerInchX, dpi.PixelsPerInchY, PixelFormats.Pbgra32);
        bitmap.Render(this);
        var directory = Path.GetDirectoryName(path);
        if (!string.IsNullOrEmpty(directory)) Directory.CreateDirectory(directory);
        using var output = File.Create(path);
        var encoder = new PngBitmapEncoder();
        encoder.Frames.Add(BitmapFrame.Create(bitmap));
        encoder.Save(output);
    }

    private async Task<bool> ConnectAsync()
    {
        SetStatus("Connecting…");
        await DisconnectAsync();
        try
        {
            var value = await AriadneClient.ConnectAsync(cancellationToken: lifetime.Token);
            client = value;
            keybindings = value.Gui.Keybindings.Count == 0
                ? LegacyKeybindings()
                : new Dictionary<string, string>(value.Gui.Keybindings, StringComparer.Ordinal);
            toolRenderers = new ToolPaneRendererRegistry(value, value.Gui);
            RenderOptions.ProcessRenderMode = value.Gui.SoftwareRendering ? RenderMode.SoftwareOnly : RenderMode.Default;
            ApplyGuiTheme(value.Gui);
            value.Store.Changed += StoreOnChanged;
            value.NavigateRequested += NavigateAsync;
            value.PluginInteractionRequested += HandlePluginInteractionAsync;
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
            return true;
        }
        catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
        {
            return false;
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
            return false;
        }
    }

    private async Task<bool> WaitForSmokeConditionAsync(bool requirePlugin)
    {
        var deadline = DateTime.UtcNow + TimeSpan.FromSeconds(15);
        ToolPaneView? pluginView = null;
        int? initialInputCount = null;
        while (DateTime.UtcNow < deadline && !lifetime.IsCancellationRequested)
        {
            await System.Windows.Threading.Dispatcher.Yield(System.Windows.Threading.DispatcherPriority.ApplicationIdle);
            if (PaneHost.Children.Count != 0 && !requirePlugin)
            {
                return true;
            }
            if (requirePlugin)
            {
                pluginView ??= toolViews.Values.FirstOrDefault(value => value.ExternalFrameReady && value.ExternalInputCount is not null);
                if (pluginView is not null && initialInputCount is null)
                {
                    initialInputCount = pluginView.ExternalInputCount;
                    pluginView.SendExternalSmokeInput();
                }
                else if (pluginView is not null && initialInputCount is not null &&
                         pluginView.ExternalInputCount is int currentInputCount && currentInputCount > initialInputCount.Value)
                {
                    return true;
                }
            }
            await Task.Delay(100, lifetime.Token);
        }
        return false;
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
                view = new TerminalPaneView(client!, pane, client!.Gui);
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
            tool = new ToolPaneView(toolRenderers!, pane, FindToolInstance(pane.Tool));
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

    private void RefreshPaneFocus()
    {
        foreach (var (id, view) in terminalViews)
        {
            view.SetFocused(id == paneId || id == previewPaneId);
        }
        foreach (var (id, view) in toolViews)
        {
            view.SetFocused(id == paneId || id == previewPaneId);
        }
    }

    private async Task FocusPaneAsync(ulong id)
    {
        if (client is null || id == 0 || id == paneId)
        {
            return;
        }
        requestedFocusPaneId = id;
        previewPaneId = 0;
        paneId = id;
        RefreshPaneFocus();

        var entered = false;
        try
        {
            await focusGate.WaitAsync(lifetime.Token);
            entered = true;
            if (requestedFocusPaneId != id)
            {
                return;
            }
            var result = await client.SetFocusAsync(id, lifetime.Token);
            if (requestedFocusPaneId != id)
            {
                return;
            }
            ApplyFocus(result.Focus);
            RefreshPaneFocus();
        }
        catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
        {
        }
        catch (Exception ex)
        {
            if (requestedFocusPaneId == id)
            {
                SetStatus(ex.Message);
            }
        }
        finally
        {
            if (entered)
            {
                focusGate.Release();
            }
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

    private async Task<PluginInteractionResult> HandlePluginInteractionAsync(PluginInteractionRequest request, CancellationToken cancellationToken)
    {
        await interactionGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (request.Interaction.Kind == "editor")
            {
                return await HandlePluginEditorAsync(request, cancellationToken).ConfigureAwait(false);
            }
            var completion = new TaskCompletionSource<PluginInteractionResult>(TaskCreationOptions.RunContinuationsAsynchronously);
            await Dispatcher.BeginInvoke(() =>
            {
                try
                {
                    if (request.Interaction.Kind == "confirm")
                    {
                        var confirmed = MessageBox.Show(this, request.Interaction.Message, request.Context.PluginId,
                            MessageBoxButton.YesNo, MessageBoxImage.Question) == MessageBoxResult.Yes;
                        completion.TrySetResult(new PluginInteractionResult { Confirmed = confirmed });
                        return;
                    }
                    if (request.Interaction.Kind != "prompt")
                    {
                        throw new InvalidOperationException($"unsupported plugin interaction {request.Interaction.Kind}");
                    }
                    var prompt = new TextPromptWindow(this, request.Context.PluginId,
                        request.Interaction.Message, request.Interaction.Text, "OK", true);
                    using var registration = cancellationToken.Register(() => Dispatcher.BeginInvoke(prompt.Close));
                    if (prompt.ShowDialog() == true)
                    {
                        completion.TrySetResult(new PluginInteractionResult { Text = prompt.Value });
                    }
                    else
                    {
                        completion.TrySetCanceled(cancellationToken);
                    }
                }
                catch (Exception error)
                {
                    completion.TrySetException(error);
                }
            });
            return await completion.Task.ConfigureAwait(false);
        }
        finally
        {
            interactionGate.Release();
        }
    }

    private async Task<PluginInteractionResult> HandlePluginEditorAsync(PluginInteractionRequest request, CancellationToken cancellationToken)
    {
        var currentClient = client ?? throw new InvalidOperationException("frontend is disconnected");
        var editor = currentClient.Gui.Editor.Count == 0 ? new List<string> { "notepad.exe" } : currentClient.Gui.Editor;
        var environment = Environment.GetEnvironmentVariables().Cast<DictionaryEntry>()
            .Select(value => $"{value.Key}={value.Value}").ToList();
        PluginEditorResult? opened = null;
        var previousPreview = previewPaneId;
        var previousPane = paneId;
        var previousZoom = zoomPaneId;
        try
        {
            var result = await currentClient.PluginAsync(new PluginManageRequest
            {
                Action = "editor.open",
                Editor = new PluginEditorRequest
                {
                    Argv = editor,
                    Cwd = Environment.CurrentDirectory,
                    Env = environment,
                    Text = request.Interaction.Text,
                },
            }, cancellationToken).ConfigureAwait(false);
            opened = result.Editor ?? throw new InvalidDataException("editor.open returned no editor");

            var exited = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
            void Observe(Snapshot value)
            {
                var pane = value.Panes.FirstOrDefault(candidate => candidate.Id == opened.PaneId);
                if (pane?.Terminal?.State is "exited" or "failed")
                {
                    var normal = pane.Terminal.Exit is { Kind: "process", Code: 0 };
                    exited.TrySetResult(normal);
                }
            }
            currentClient.Store.Changed += Observe;
            using var registration = cancellationToken.Register(() => exited.TrySetCanceled(cancellationToken));
            try
            {
                await Dispatcher.InvokeAsync(() =>
                {
                    previewPaneId = opened.PaneId;
                    paneId = opened.PaneId;
                    zoomPaneId = 0;
                    Refresh();
                });
                Observe(currentClient.Store.Current);
                if (!await exited.Task.ConfigureAwait(false))
                {
                    throw new OperationCanceledException("editor exited unsuccessfully", cancellationToken);
                }
            }
            finally
            {
                currentClient.Store.Changed -= Observe;
            }

            var finished = await currentClient.PluginAsync(new PluginManageRequest
            {
                Action = "editor.finish", PaneId = opened.PaneId,
            }, cancellationToken).ConfigureAwait(false);
            return new PluginInteractionResult { Text = finished.Editor?.Text ?? "" };
        }
        catch
        {
            if (opened is not null)
            {
                try
                {
                    using var cleanup = new CancellationTokenSource(TimeSpan.FromSeconds(5));
                    await currentClient.PluginAsync(new PluginManageRequest
                    {
                        Action = "editor.cancel", PaneId = opened.PaneId,
                    }, cleanup.Token).ConfigureAwait(false);
                }
                catch
                {
                }
            }
            throw;
        }
        finally
        {
            await Dispatcher.InvokeAsync(() =>
            {
                previewPaneId = previousPreview;
                paneId = previousPane;
                zoomPaneId = previousZoom;
                Refresh();
            });
        }
    }

    private void ApplyFocus(FrontendState focus)
    {
        workspaceId = focus.WorkspaceId;
        windowId = focus.WindowId;
        paneId = focus.PaneId;
        requestedFocusPaneId = focus.PaneId;
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
        var currentClient = client;
        if (currentClient is null)
        {
            return;
        }
        try
        {
            var result = await currentClient.SelectWindowAsync(id, lifetime.Token);
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
                windowId, targetPaneId, direction, DefaultShell(client.Gui),
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

    private static IReadOnlyList<string> DefaultShell(GuiOptions options) =>
        options.Shell.Count == 0 ? ["powershell.exe", "-NoLogo"] : options.Shell;

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
        var menu = CreateSelectionMenu();
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
        menu.PlacementTarget = sender as UIElement ?? StashButton;
        menu.Placement = ReferenceEquals(sender, StashButton)
            ? System.Windows.Controls.Primitives.PlacementMode.Bottom
            : System.Windows.Controls.Primitives.PlacementMode.MousePoint;
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
        var modifiers = Keyboard.Modifiers;
        if (keySequence.Count == 0 && e.Key == Key.P && modifiers.HasFlag(ModifierKeys.Control) &&
            modifiers.HasFlag(ModifierKeys.Shift))
        {
            e.Handled = true;
            await ShowCommandPaletteAsync();
            return;
        }
        var token = PortableKey(e, modifiers);
        if (token is null)
        {
            return;
        }
        if (e.IsRepeat && keySequence.Count != 0 && keySequence[^1] == token)
        {
            e.Handled = true;
            return;
        }
        var candidate = string.Join(' ', keySequence.Append(token));
        var exact = keybindings.TryGetValue(candidate, out var command);
        var prefix = keybindings.Keys.Any(value => value.StartsWith(candidate + " ", StringComparison.Ordinal));
        if (!exact && !prefix)
        {
            if (keySequence.Count == 0) return;
            e.Handled = true;
            CancelPrefix();
            return;
        }
        e.Handled = true;
        if (exact)
        {
            keySequence.Clear();
            prefixArmed = false;
            await ExecuteKeybindingAsync(command!);
            return;
        }
        keySequence.Add(token);
        prefixArmed = true;
        SetStatus($"keys: {candidate}");
    }

    private static string? PortableKey(KeyEventArgs e, ModifierKeys modifiers)
    {
        var key = e.Key == Key.System ? e.SystemKey : e.Key;
        string? name = key switch
        {
            >= Key.A and <= Key.Z => ((char)('a' + (int)key - (int)Key.A)).ToString(),
            Key.Left => "left", Key.Down => "down", Key.Up => "up", Key.Right => "right",
            Key.Enter => "enter", Key.Escape => "escape", Key.Tab => "tab", Key.Back => "backspace",
            Key.Space => "space", Key.PageUp => "page-up", Key.PageDown => "page-down",
            Key.Home => "home", Key.End => "end", Key.Delete => "delete", Key.Insert => "insert",
            Key.D5 when modifiers.HasFlag(ModifierKeys.Shift) => "%",
            Key.D2 when modifiers.HasFlag(ModifierKeys.Shift) => "\"",
            Key.OemQuotes when modifiers.HasFlag(ModifierKeys.Shift) => "\"",
            Key.D9 when modifiers.HasFlag(ModifierKeys.Shift) => "(",
            Key.D0 when modifiers.HasFlag(ModifierKeys.Shift) => ")",
            Key.D4 when modifiers.HasFlag(ModifierKeys.Shift) => "$",
            Key.OemOpenBrackets => "[", Key.OemCloseBrackets => "]", Key.OemComma => ",",
            Key.OemQuestion when modifiers.HasFlag(ModifierKeys.Shift) => "?",
            Key.OemSemicolon => ":",
            _ => null,
        };
        if (name is null) return null;
        if (modifiers.HasFlag(ModifierKeys.Control)) return "ctrl-" + name.ToLowerInvariant();
        if (modifiers.HasFlag(ModifierKeys.Alt)) return "alt-" + name.ToLowerInvariant();
        if (modifiers.HasFlag(ModifierKeys.Shift) && name.Length == 1 && char.IsLetter(name[0])) return name.ToUpperInvariant();
        return name;
    }

    private async Task ExecuteKeybindingAsync(string commands)
    {
        if (commands.Length == 0)
        {
            SetStatus("Keybinding disabled");
            return;
        }
        foreach (var command in commands.Split(';', StringSplitOptions.TrimEntries | StringSplitOptions.RemoveEmptyEntries))
        {
            switch (command)
            {
                case "focus left": await MoveFocusAsync(-1, 0); break;
                case "focus down": await MoveFocusAsync(0, 1); break;
                case "focus up": await MoveFocusAsync(0, -1); break;
                case "focus right": await MoveFocusAsync(1, 0); break;
                case "resize left": await ResizeFocusedPaneAsync("horizontal", -1); break;
                case "resize right": await ResizeFocusedPaneAsync("horizontal", 1); break;
                case "resize up": await ResizeFocusedPaneAsync("vertical", -1); break;
                case "resize down": await ResizeFocusedPaneAsync("vertical", 1); break;
                case "zoom toggle": MenuZoom_OnClick(this, new RoutedEventArgs()); break;
                case "zoom on" when zoomPaneId == 0: MenuZoom_OnClick(this, new RoutedEventArgs()); break;
                case "zoom off" when zoomPaneId != 0: MenuZoom_OnClick(this, new RoutedEventArgs()); break;
                case "split h": await CreateTerminalAsync(paneId, "horizontal"); break;
                case "split v": await CreateTerminalAsync(paneId, "vertical"); break;
                case "command-prompt": await ShowCommandPaletteAsync(); break;
                case "restart": await ExecutePaneActionAsync(paneId, PaneAction.Restart); break;
                case "run": await ExecutePaneActionAsync(paneId, PaneAction.RunCommand); break;
                case "stop": await ExecutePaneActionAsync(paneId, PaneAction.Stop); break;
                case "stash-pane": await ExecutePaneActionAsync(paneId, PaneAction.Stash); break;
                case "close-confirm": await ExecutePaneActionAsync(paneId, PaneAction.Delete); break;
                case "paste" when terminalViews.TryGetValue(paneId, out var terminal): await PasteClipboardAsync(terminal); break;
                case "send-key ctrl-a" when terminalViews.TryGetValue(paneId, out var target): target.SendInput("\x01"); break;
                case "detach": Close(); return;
                case "help":
                case "commands": await ShowCommandPaletteAsync(); break;
                case "reconnect": await ConnectAsync(); break;
                case "settings": MenuSettings_OnClick(this, new RoutedEventArgs()); break;
                default:
                    SetStatus($"GUI does not support command: {command}");
                    return;
            }
        }
    }

    private async Task ResizeFocusedPaneAsync(string direction, int neighborDelta)
    {
        if (client is null || FindWindow(windowId)?.Layout is not { } layout) return;
        var path = new List<(LayoutNode Node, int ChildIndex)>();
        if (!FindLayoutPath(layout, paneId, path)) return;
        for (var index = path.Count - 1; index >= 0; index--)
        {
            var (node, childIndex) = path[index];
            if (node.Direction != direction) continue;
            var neighbor = childIndex + neighborDelta;
            if (neighbor < 0 || neighbor >= node.Children.Count) continue;
            var weights = Enumerable.Range(0, node.Children.Count)
                .Select(position => position < node.Weights.Count && node.Weights[position] > 0 ? node.Weights[position] : 1u)
                .ToArray();
            if (weights[neighbor] <= 1)
            {
                SetStatus("Adjacent pane is already at its minimum weight");
                return;
            }
            weights[childIndex]++;
            weights[neighbor]--;
            await client.ResizeSplitAsync(node.SplitId, weights, lifetime.Token);
            SetStatus($"Pane {paneId} resized {direction}");
            return;
        }
        SetStatus("No adjacent pane in that direction");
    }

    private static bool FindLayoutPath(LayoutNode node, ulong target, List<(LayoutNode Node, int ChildIndex)> path)
    {
        if (node.Kind == "pane") return node.PaneId == target;
        for (var index = 0; index < node.Children.Count; index++)
        {
            path.Add((node, index));
            if (FindLayoutPath(node.Children[index], target, path)) return true;
            path.RemoveAt(path.Count - 1);
        }
        return false;
    }

    private static Dictionary<string, string> LegacyKeybindings() => new(StringComparer.Ordinal)
    {
        ["ctrl-a h"] = "focus left", ["ctrl-a j"] = "focus down", ["ctrl-a k"] = "focus up", ["ctrl-a l"] = "focus right",
        ["ctrl-a z"] = "zoom toggle", ["ctrl-a ctrl-a"] = "send-key ctrl-a", ["ctrl-a %"] = "split h",
        ["ctrl-a \""] = "split v", ["ctrl-a :"] = "command-prompt",
    };

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
            new("split-right", "split h — split the focused Pane to the right"),
            new("split-below", "split v — split the focused Pane below"),
            new("zoom", "zoom [on|off|toggle] — change the frontend-local zoom state"),
            new("focus-left", "focus left — focus the Pane on the left"),
            new("focus-down", "focus down — focus the Pane below"),
            new("focus-up", "focus up — focus the Pane above"),
            new("focus-right", "focus right — focus the Pane on the right"),
            new("resize-left", "resize left — grow the focused Pane to the left"),
            new("resize-down", "resize down — grow the focused Pane downward"),
            new("resize-up", "resize up — grow the focused Pane upward"),
            new("resize-right", "resize right — grow the focused Pane to the right"),
            new("paste", "paste — paste the shared clipboard into the focused terminal"),
            new("send-prefix", "send-key ctrl-a — send Ctrl+A to the focused terminal"),
            new("settings", "settings — open GUI settings"),
            new("reconnect", "reconnect — reconnect this frontend"),
            new("detach", "detach — close this frontend without stopping the daemon"),
        };
        var pane = snapshot.Panes.FirstOrDefault(value => value.Id == paneId);
        var state = pane?.Terminal?.State;
        if (pane?.Kind == "terminal")
        {
            if (state is "exited" or "failed" or "placeholder")
            {
                items.Add(new("restart", "restart — restart the focused terminal's original command"));
                items.Add(new("run", "run — run another command in the focused Pane…"));
                items.Add(new("delete", "close-confirm — confirm and delete the focused Pane"));
            }
            if (state == "running")
            {
                items.Add(new("stop", "stop — stop the focused terminal"));
            }
        }
        if (pane is not null && !pane.Transient)
        {
            items.Add(new("stash", "stash-pane — stash the focused Pane"));
        }
        foreach (var stashed in snapshot.StashedPanes)
        {
            items.Add(new($"restore-pane:{stashed.PaneId}", $"restore-pane {stashed.PaneId} — restore this stashed Pane"));
        }
        foreach (var stashed in snapshot.StashedWindows)
        {
            items.Add(new($"restore-window:{stashed.WindowId}", $"restore-window {stashed.WindowId} — restore this stashed Window"));
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
            case "resize-left": await ResizeFocusedPaneAsync("horizontal", -1); return;
            case "resize-down": await ResizeFocusedPaneAsync("vertical", 1); return;
            case "resize-up": await ResizeFocusedPaneAsync("vertical", -1); return;
            case "resize-right": await ResizeFocusedPaneAsync("horizontal", 1); return;
            case "restart": await ExecutePaneActionAsync(paneId, PaneAction.Restart); return;
            case "run": await ExecutePaneActionAsync(paneId, PaneAction.RunCommand); return;
            case "stop": await ExecutePaneActionAsync(paneId, PaneAction.Stop); return;
            case "stash": await ExecutePaneActionAsync(paneId, PaneAction.Stash); return;
            case "delete": await ExecutePaneActionAsync(paneId, PaneAction.Delete); return;
            case "paste" when terminalViews.TryGetValue(paneId, out var terminal): await PasteClipboardAsync(terminal); return;
            case "send-prefix" when terminalViews.TryGetValue(paneId, out var target): target.SendInput("\x01"); return;
            case "settings": MenuSettings_OnClick(this, new RoutedEventArgs()); return;
            case "reconnect": await ConnectAsync(); return;
            case "detach": Close(); return;
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
        var menu = CreateSelectionMenu();
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

    private static ContextMenu CreateSelectionMenu() => new()
    {
        Background = new SolidColorBrush(Color.FromRgb(245, 246, 248)),
        Foreground = new SolidColorBrush(Color.FromRgb(32, 36, 42)),
    };

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

    private void Menu_OnSubmenuOpened(object sender, RoutedEventArgs e)
    {
        var pane = snapshot.Panes.FirstOrDefault(value => value.Id == paneId);
        var state = pane?.Terminal?.State;
        var hasTerminalView = terminalViews.ContainsKey(paneId);
        MenuCopy.IsEnabled = hasTerminalView;
        MenuPaste.IsEnabled = hasTerminalView && state == "running";
        MenuZoom.IsEnabled = pane is not null;
        MenuZoom.Header = zoomPaneId == 0 ? "Zoom focused pane" : "Leave zoom";
        MenuRestart.IsEnabled = pane?.Kind == "terminal" && state is "exited" or "failed" or "placeholder";
        MenuRun.IsEnabled = MenuRestart.IsEnabled;
        MenuStop.IsEnabled = pane?.Kind == "terminal" && state == "running";
        MenuStashPane.IsEnabled = pane is { Transient: false };
        MenuDeletePane.IsEnabled = pane?.Kind == "terminal" && state is "exited" or "failed" or "placeholder";
    }

    private void MenuExit_OnClick(object sender, RoutedEventArgs e) => Close();

    private void MenuCopy_OnClick(object sender, RoutedEventArgs e)
    {
        try
        {
            if (!terminalViews.TryGetValue(paneId, out var terminal) || !terminal.CopySelection())
            {
                SetStatus("No terminal selection to copy");
            }
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
        }
    }

    private async void MenuPaste_OnClick(object sender, RoutedEventArgs e)
    {
        if (terminalViews.TryGetValue(paneId, out var terminal))
        {
            await PasteClipboardAsync(terminal);
        }
    }

    private void MenuZoom_OnClick(object sender, RoutedEventArgs e)
    {
        if (paneId == 0)
        {
            return;
        }
        zoomPaneId = zoomPaneId == 0 ? paneId : 0;
        Refresh();
        SetStatus(zoomPaneId == 0 ? "Zoom off" : $"Pane {paneId} zoomed");
    }

    private async void MenuFocusLeft_OnClick(object sender, RoutedEventArgs e) => await MoveFocusAsync(-1, 0);
    private async void MenuFocusDown_OnClick(object sender, RoutedEventArgs e) => await MoveFocusAsync(0, 1);
    private async void MenuFocusUp_OnClick(object sender, RoutedEventArgs e) => await MoveFocusAsync(0, -1);
    private async void MenuFocusRight_OnClick(object sender, RoutedEventArgs e) => await MoveFocusAsync(1, 0);
    private async void MenuRestart_OnClick(object sender, RoutedEventArgs e) => await ExecutePaneActionAsync(paneId, PaneAction.Restart);
    private async void MenuRun_OnClick(object sender, RoutedEventArgs e) => await ExecutePaneActionAsync(paneId, PaneAction.RunCommand);
    private async void MenuStop_OnClick(object sender, RoutedEventArgs e) => await ExecutePaneActionAsync(paneId, PaneAction.Stop);
    private async void MenuStashPane_OnClick(object sender, RoutedEventArgs e) => await ExecutePaneActionAsync(paneId, PaneAction.Stash);
    private async void MenuDeletePane_OnClick(object sender, RoutedEventArgs e) => await ExecutePaneActionAsync(paneId, PaneAction.Delete);

    private async void MenuSettings_OnClick(object sender, RoutedEventArgs e)
    {
        var currentClient = client;
        if (currentClient is null)
        {
            SetStatus("Frontend is disconnected");
            return;
        }
        var defaults = currentClient.Gui.DefaultKeybindings.Count > 0
            ? currentClient.Gui.DefaultKeybindings
            : LegacyKeybindings();
        var settings = new SettingsWindow(this, keybindings, defaults, currentClient.Gui.FontSize, currentClient.Gui.ConfigPath);
        if (settings.ShowDialog() == true && settings.EffectiveBindings is not null)
        {
            keybindings = settings.EffectiveBindings;
            currentClient.Gui.FontSize = settings.GuiFontSize;
            ApplyGuiOptions(currentClient.Gui);
            CancelPrefix();
            try
            {
                var reloaded = await currentClient.ReloadFrontendConfigAsync(lifetime.Token);
                keybindings = reloaded.Keybindings.Count == 0
                    ? LegacyKeybindings()
                    : new Dictionary<string, string>(reloaded.Keybindings, StringComparer.Ordinal);
                ApplyGuiOptions(reloaded);
                SetStatus("Settings saved and applied");
            }
            catch (Exception ex)
            {
                SetStatus($"Settings applied locally; daemon reload failed: {ex.Message}");
            }
        }
    }

    private void MenuAbout_OnClick(object sender, RoutedEventArgs e) => MessageBox.Show(this,
        "Ariadne for Windows\n\nTerminal workspace frontend",
        "About Ariadne", MessageBoxButton.OK, MessageBoxImage.Information);

    private void ConnectionOnFailed(Exception error) => Dispatcher.BeginInvoke(() => SetStatus(error.Message));
    private void SetStatus(string value) => StatusText.Text = value;

    private void ApplyGuiTheme(GuiOptions options)
    {
        var background = (Color)ColorConverter.ConvertFromString(options.Background);
        var foreground = (Color)ColorConverter.ConvertFromString(options.Foreground);
        SetResourceColor("WindowBackground", background);
        SetResourceColor("Accent", (Color)ColorConverter.ConvertFromString(options.Accent));
        Background = new SolidColorBrush(background);
        Foreground = new SolidColorBrush(foreground);
        PaneHost.Background = Background;
    }

    private void ApplyGuiOptions(GuiOptions options)
    {
        ApplyGuiTheme(options);
        toolRenderers?.ApplyOptions(options);
        foreach (var view in terminalViews.Values)
        {
            view.ApplyOptions(options);
        }
        foreach (var view in toolViews.Values)
        {
            view.ApplyOptions(options);
        }
        RefreshPaneFocus();
    }

    private void SetResourceColor(string name, Color color)
    {
        if (FindResource(name) is SolidColorBrush brush && !brush.IsFrozen)
        {
            brush.Color = color;
        }
    }
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
            old.PluginInteractionRequested -= HandlePluginInteractionAsync;
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
        interactionGate.Dispose();
        focusGate.Dispose();
    }
}
