using Ariadne.Windows.Protocol;
using Ariadne.Terminal.WinUI;
using Ariadne.WinUI.Terminal;
using Ariadne.WinUI.Views;
using Microsoft.UI;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.Graphics;

namespace Ariadne.WinUI;

public sealed partial class MainWindow : Window
{
    private readonly CancellationTokenSource lifetime = new();
    private readonly Dictionary<ulong, TerminalSurface> terminalSurfaces = [];
    private readonly HashSet<ulong> renderedTerminals = [];
    private readonly Dictionary<ulong, PluginToolSurface> pluginSurfaces = [];
    private readonly HashSet<ulong> renderedTools = [];
    private Dictionary<string, string> keybindings = new(StringComparer.Ordinal);
    private AriadneClient? client;
    private Snapshot snapshot = new();
    private ulong workspaceId;
    private ulong windowId;
    private ulong paneId;
    private ulong zoomPaneId;
    private ulong previewPaneId;
    private bool updatingPickers;
    private readonly bool smokeTest;
    private readonly string? endpoint;
    private readonly string? smokeInput;
    private readonly bool smokePlugin;
    private int shutdownStarted;
    private readonly SemaphoreSlim focusGate = new(1, 1);
    private ulong requestedFocusPaneId;

    public MainWindow(bool smokeTest = false, string? endpoint = null, string? smokeInput = null, bool smokePlugin = false)
    {
        this.smokeTest = smokeTest;
        this.endpoint = endpoint;
        this.smokeInput = smokeInput;
        this.smokePlugin = smokePlugin;
        InitializeComponent();
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(AppTitleBar);
        AppWindow.Resize(new SizeInt32(1180, 760));
        AppWindow.Closing += OnAppWindowClosing;
        Closed += OnClosed;
        _ = ConnectAsync();
    }

    private async Task ConnectAsync()
    {
        Connecting.IsActive = true;
        Connecting.Visibility = Visibility.Visible;
        SetStatus("Connecting to Ariadne daemon…");
        await DisconnectAsync();
        try
        {
            var connected = await AriadneClient.ConnectAsync(endpoint, cancellationToken: lifetime.Token);
            client = connected;
            keybindings = connected.Gui.Keybindings.Count == 0
                ? LegacyKeybindings()
                : new Dictionary<string, string>(connected.Gui.Keybindings, StringComparer.Ordinal);
            connected.Store.Changed += StoreOnChanged;
            connected.Failed += ConnectionOnFailed;
            connected.NavigateRequested += NavigateAsync;
            connected.PluginInteractionRequested += HandlePluginInteractionAsync;
            snapshot = connected.Store.Current;
            ApplyGuiOptions(connected.Gui);
            SelectInitialLocation();
            Refresh();
            await VerifySmokeAsync();
            SetStatus("Connected");
            FinishSmokeTest(0, "connected");
        }
        catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
        {
        }
        catch (Exception error)
        {
            SetStatus(error.Message);
            FinishSmokeTest(1, error.ToString());
        }
        finally
        {
            Connecting.IsActive = false;
            Connecting.Visibility = Visibility.Collapsed;
        }
    }

    private void FinishSmokeTest(int exitCode, string result)
    {
        if (!smokeTest) return;
        File.WriteAllText(Path.Combine(Path.GetTempPath(), "ariadne-winui-smoke.txt"), result);
        Environment.ExitCode = exitCode;
        DispatcherQueue.TryEnqueue(ShutdownSmokeTestAsync);
    }

    private async void ShutdownSmokeTestAsync()
    {
        if (Interlocked.Exchange(ref shutdownStarted, 1) != 0) return;
        DisposeTerminalSurfaces();
        await DisconnectAsync();
        lifetime.Cancel();
        Close();
        lifetime.Dispose();
    }

    private async Task VerifySmokeAsync()
    {
        if (!smokeTest) return;
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(15));
        if (terminalSurfaces.Count != 0)
        {
            var surfaces = terminalSurfaces.Values.ToArray();
            await Task.WhenAll(surfaces.Select(surface => surface.BackendReady)).WaitAsync(timeout.Token);
            await Task.WhenAll(surfaces.Select(surface => surface.FirstResize)).WaitAsync(timeout.Token);
            await Task.WhenAny(surfaces.Select(surface => surface.FirstOutput)).WaitAsync(timeout.Token);
            if (!string.IsNullOrEmpty(smokeInput))
            {
                var observed = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
                void OnOutput(object? sender, string value)
                {
                    if (value.Contains(smokeInput, StringComparison.Ordinal)) observed.TrySetResult();
                }
                foreach (var surface in surfaces) surface.OutputReceived += OnOutput;
                try
                {
                    surfaces[0].SendInput($"echo {smokeInput}\r");
                    await observed.Task.WaitAsync(timeout.Token);
                }
                finally
                {
                    foreach (var surface in surfaces) surface.OutputReceived -= OnOutput;
                }
            }
        }
        if (!smokePlugin) return;
        PluginToolSurface? plugin = null;
        while (!timeout.IsCancellationRequested)
        {
            plugin ??= pluginSurfaces.Values.FirstOrDefault(value => value.HasFrame && value.InputCount is not null);
            if (plugin is not null)
            {
                var before = plugin.InputCount;
                plugin.SendSmokeInput();
                while (!timeout.IsCancellationRequested)
                {
                    if (plugin.InputCount is int current && before is int initial && current > initial) return;
                    await Task.Delay(100, timeout.Token);
                }
            }
            await Task.Delay(100, timeout.Token);
        }
        timeout.Token.ThrowIfCancellationRequested();
    }

    private void StoreOnChanged(Snapshot value) => DispatcherQueue.TryEnqueue(() =>
    {
        snapshot = value;
        NormalizeLocation();
        Refresh();
    });

    private void ConnectionOnFailed(Exception error) =>
        DispatcherQueue.TryEnqueue(() => SetStatus(error.Message));

    private void SelectInitialLocation()
    {
        var workspace = snapshot.Workspaces.FirstOrDefault();
        workspaceId = workspace?.Id ?? 0;
        var window = workspace is null
            ? null
            : snapshot.Windows.FirstOrDefault(candidate => workspace.WindowIds.Contains(candidate.Id));
        windowId = window?.Id ?? 0;
        paneId = window?.Layout is null ? 0 : FirstPane(window.Layout);
    }

    private void NormalizeLocation()
    {
        var workspace = snapshot.Workspaces.FirstOrDefault(candidate => candidate.Id == workspaceId)
                        ?? snapshot.Workspaces.FirstOrDefault();
        workspaceId = workspace?.Id ?? 0;
        var window = snapshot.Windows.FirstOrDefault(candidate => candidate.Id == windowId && candidate.WorkspaceId == workspaceId)
                     ?? (workspace is null ? null : snapshot.Windows.FirstOrDefault(candidate => workspace.WindowIds.Contains(candidate.Id)));
        windowId = window?.Id ?? 0;
        if (window?.Layout is null || !ContainsPane(window.Layout, paneId))
        {
            paneId = window?.Layout is null ? 0 : FirstPane(window.Layout);
        }
        if (previewPaneId != 0)
        {
            var preview = snapshot.Panes.FirstOrDefault(value => value.Id == previewPaneId);
            if (preview is null) previewPaneId = 0;
            else paneId = previewPaneId;
        }
    }

    private void Refresh()
    {
        updatingPickers = true;
        WorkspacePicker.ItemsSource = snapshot.Workspaces;
        WorkspacePicker.DisplayMemberPath = nameof(WorkspaceModel.Name);
        WorkspacePicker.SelectedItem = snapshot.Workspaces.FirstOrDefault(value => value.Id == workspaceId);
        var windows = snapshot.Windows.Where(value => value.WorkspaceId == workspaceId).ToList();
        WindowPicker.ItemsSource = windows;
        WindowPicker.DisplayMemberPath = nameof(WindowModel.Name);
        WindowPicker.SelectedItem = windows.FirstOrDefault(value => value.Id == windowId);
        updatingPickers = false;

        PaneHost.Children.Clear();
        renderedTerminals.Clear();
        renderedTools.Clear();
        var preview = previewPaneId == 0 ? null : snapshot.Panes.FirstOrDefault(value => value.Id == previewPaneId);
        var window = snapshot.Windows.FirstOrDefault(value => value.Id == windowId);
        if (preview is not null)
        {
            PaneHost.Children.Add(BuildPane(preview));
        }
        else if (window?.Layout is not null)
        {
            if (zoomPaneId != 0 && ContainsPane(window.Layout, zoomPaneId))
            {
                var zoomed = snapshot.Panes.FirstOrDefault(value => value.Id == zoomPaneId);
                if (zoomed is not null) PaneHost.Children.Add(BuildPane(zoomed));
            }
            else
            {
                zoomPaneId = 0;
                PaneHost.Children.Add(BuildLayout(window.Layout));
            }
        }
        else
        {
            PaneHost.Children.Add(new TextBlock
            {
                Text = "No Pane in this Window",
                HorizontalAlignment = HorizontalAlignment.Center,
                VerticalAlignment = VerticalAlignment.Center,
                Opacity = 0.68,
            });
        }
        PruneTerminalSurfaces();
        PrunePluginSurfaces();
        RevisionText.Text = $"revision {snapshot.Revision}";
        var alerts = snapshot.Attentions.Count(value => value.AcknowledgedAt is null);
        AttentionButton.Content = alerts == 0 ? "Alerts" : $"Alerts ({alerts})";
    }

    private FrameworkElement BuildLayout(LayoutNode node)
    {
        if (node.Kind == "pane")
        {
            var pane = snapshot.Panes.FirstOrDefault(value => value.Id == node.PaneId);
            return pane is null ? MissingPane(node.PaneId) : BuildPane(pane);
        }
        var horizontal = node.Direction == "horizontal";
        var grid = new Grid();
        for (var index = 0; index < node.Children.Count; index++)
        {
            var weight = index < node.Weights.Count && node.Weights[index] > 0 ? node.Weights[index] : 1;
            if (horizontal) grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(weight, GridUnitType.Star) });
            else grid.RowDefinitions.Add(new RowDefinition { Height = new GridLength(weight, GridUnitType.Star) });
            var child = BuildLayout(node.Children[index]);
            child.Margin = new Thickness(index == 0 ? 0 : horizontal ? 3 : 0, index == 0 ? 0 : horizontal ? 0 : 3, 0, 0);
            if (horizontal) Grid.SetColumn(child, index);
            else Grid.SetRow(child, index);
            grid.Children.Add(child);
        }
        for (var index = 1; index < node.Children.Count; index++)
        {
            AddSplitHandle(grid, node, horizontal, index);
        }
        return grid;
    }

    private void AddSplitHandle(Grid grid, LayoutNode node, bool horizontal, int index)
    {
        var handle = new Border
        {
            Background = new SolidColorBrush(ColorHelper.FromArgb(1, 255, 255, 255)),
            Width = horizontal ? 8 : double.NaN,
            Height = horizontal ? double.NaN : 8,
            HorizontalAlignment = horizontal ? HorizontalAlignment.Left : HorizontalAlignment.Stretch,
            VerticalAlignment = horizontal ? VerticalAlignment.Stretch : VerticalAlignment.Top,
            Margin = horizontal ? new Thickness(-4, 0, 0, 0) : new Thickness(0, -4, 0, 0),
        };
        if (horizontal) Grid.SetColumn(handle, index);
        else Grid.SetRow(handle, index);
        Canvas.SetZIndex(handle, 20);

        global::Windows.Foundation.Point start = default;
        double previous = 0;
        double current = 0;
        var dragging = false;
        handle.PointerEntered += (_, _) => handle.Background = new SolidColorBrush(ColorHelper.FromArgb(90, 81, 145, 255));
        handle.PointerExited += (_, _) =>
        {
            if (!dragging) handle.Background = new SolidColorBrush(ColorHelper.FromArgb(1, 255, 255, 255));
        };
        handle.PointerPressed += (_, args) =>
        {
            dragging = true;
            start = args.GetCurrentPoint(grid).Position;
            previous = horizontal ? grid.ColumnDefinitions[index - 1].ActualWidth : grid.RowDefinitions[index - 1].ActualHeight;
            current = horizontal ? grid.ColumnDefinitions[index].ActualWidth : grid.RowDefinitions[index].ActualHeight;
            handle.CapturePointer(args.Pointer);
            args.Handled = true;
        };
        handle.PointerMoved += (_, args) =>
        {
            if (!dragging) return;
            var point = args.GetCurrentPoint(grid).Position;
            var delta = horizontal ? point.X - start.X : point.Y - start.Y;
            var before = Math.Max(24, previous + delta);
            var after = Math.Max(24, current - delta);
            if (before + after > previous + current)
            {
                if (previous + delta < 24) after = previous + current - before;
                else before = previous + current - after;
            }
            if (horizontal)
            {
                grid.ColumnDefinitions[index - 1].Width = new GridLength(before);
                grid.ColumnDefinitions[index].Width = new GridLength(after);
            }
            else
            {
                grid.RowDefinitions[index - 1].Height = new GridLength(before);
                grid.RowDefinitions[index].Height = new GridLength(after);
            }
            args.Handled = true;
        };
        handle.PointerReleased += async (_, args) =>
        {
            if (!dragging) return;
            dragging = false;
            handle.ReleasePointerCapture(args.Pointer);
            handle.Background = new SolidColorBrush(ColorHelper.FromArgb(1, 255, 255, 255));
            args.Handled = true;
            if (client is null) return;
            try
            {
                var weights = horizontal
                    ? grid.ColumnDefinitions.Select(value => (uint)Math.Max(1, Math.Round(value.ActualWidth))).ToArray()
                    : grid.RowDefinitions.Select(value => (uint)Math.Max(1, Math.Round(value.ActualHeight))).ToArray();
                await client.ResizeSplitAsync(node.SplitId, weights, lifetime.Token);
                SetStatus($"Split {node.SplitId} resized");
            }
            catch (Exception error)
            {
                SetStatus(error.Message);
                Refresh();
            }
        };
        grid.Children.Add(handle);
    }

    private FrameworkElement BuildPane(PaneModel pane)
    {
        var selected = pane.Id == paneId;
        var border = new Border
        {
            Tag = pane.Id,
            Background = (Brush)Application.Current.Resources["PaneSurfaceBrush"],
            BorderBrush = selected
                ? new SolidColorBrush(client is null ? Colors.DeepSkyBlue : UiColor(client.Gui.Accent))
                : new SolidColorBrush(ColorHelper.FromArgb(72, 118, 126, 140)),
            BorderThickness = new Thickness(selected ? 2 : 1),
            CornerRadius = new CornerRadius(6),
        };
        border.PointerPressed += Pane_OnPointerPressed;
        border.ContextFlyout = CreatePaneFlyout(pane);

        var content = new Grid();
        content.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        content.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        var title = new Grid
        {
            Padding = new Thickness(10, 7, 10, 7),
            Background = (Brush)Application.Current.Resources["PaneHeaderBrush"],
        };
        title.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        title.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        title.Children.Add(new TextBlock
        {
            Text = string.IsNullOrWhiteSpace(pane.Title) ? $"Pane {pane.Id}" : pane.Title,
            FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
            TextTrimming = TextTrimming.CharacterEllipsis,
        });
        var state = new TextBlock { Text = PaneState(pane), Opacity = 0.6 };
        Grid.SetColumn(state, 1);
        title.Children.Add(state);
        content.Children.Add(title);

        if (pane.Kind == "terminal" && pane.Terminal?.Id is ulong terminalId && client is not null)
        {
            var terminal = GetTerminalSurface(pane.Id, terminalId);
            Grid.SetRow(terminal, 1);
            content.Children.Add(terminal);
        }
        else if (pane.Kind == "tool" && pane.Tool is not null)
        {
            FrameworkElement tool = pane.Tool.Provider == "ariadne" || client is null
                ? BuildGenericTool(pane)
                : GetPluginSurface(pane);
            Grid.SetRow(tool, 1);
            content.Children.Add(tool);
        }
        else
        {
            var empty = new StackPanel
            {
                HorizontalAlignment = HorizontalAlignment.Center,
                VerticalAlignment = VerticalAlignment.Center,
                Spacing = 7,
            };
            empty.Children.Add(new FontIcon { Glyph = pane.Kind == "terminal" ? "\uE756" : "\uECAA", FontSize = 28, Opacity = 0.72 });
            empty.Children.Add(new TextBlock
            {
                Text = pane.Kind == "terminal"
                    ? "Terminal is not running"
                    : $"{pane.Tool?.Provider ?? "tool"} / {pane.Tool?.Type ?? pane.Kind}",
                Opacity = 0.7,
            });
            Grid.SetRow(empty, 1);
            content.Children.Add(empty);
        }
        border.Child = content;
        return border;
    }

    private TerminalSurface GetTerminalSurface(ulong ownerPaneId, ulong terminalId)
    {
        renderedTerminals.Add(terminalId);
        if (terminalSurfaces.TryGetValue(terminalId, out var existing)) return existing;
        var terminal = new TerminalSurface(
            new AriadneTerminalBackend(client!, terminalId),
            CreateTerminalAppearance(client!.Gui));
        terminal.ConnectionFailed += (_, error) => SetStatus(error.Message);
        terminal.FocusEntered += (_, _) => SelectPane(ownerPaneId);
        terminal.KeyPressed += Root_OnKeyDown;
        terminalSurfaces.Add(terminalId, terminal);
        return terminal;
    }

    private void PruneTerminalSurfaces()
    {
        foreach (var terminalId in terminalSurfaces.Keys.Where(id => !renderedTerminals.Contains(id)).ToArray())
        {
            terminalSurfaces.Remove(terminalId, out var terminal);
            terminal?.Dispose();
        }
    }

    private FrameworkElement GetPluginSurface(PaneModel pane)
    {
        renderedTools.Add(pane.Id);
        if (pluginSurfaces.TryGetValue(pane.Id, out var existing))
        {
            existing.Update(pane);
            return existing;
        }
        var surface = new PluginToolSurface(client!, pane, client!.Gui);
        surface.FocusEntered += (_, _) => SelectPane(pane.Id);
        surface.Failed += (_, error) => SetStatus(error.Message);
        pluginSurfaces.Add(pane.Id, surface);
        return surface;
    }

    private FrameworkElement BuildGenericTool(PaneModel pane)
    {
        var descriptor = pane.Tool!;
        var instance = snapshot.ToolInstances.FirstOrDefault(value =>
            value.Descriptor.Provider == descriptor.Provider && value.Descriptor.Type == descriptor.Type &&
            value.Descriptor.Instance == descriptor.Instance);
        return new TextBox
        {
            IsReadOnly = true,
            AcceptsReturn = true,
            TextWrapping = TextWrapping.Wrap,
            FontFamily = new Microsoft.UI.Xaml.Media.FontFamily(client?.Gui.FontFamily ?? "Cascadia Mono"),
            FontSize = client?.Gui.FontSize ?? 14,
            BorderThickness = new Thickness(0),
            Padding = new Thickness(12),
            Text = instance is null
                ? $"{descriptor.Provider} / {descriptor.Type} / {descriptor.Instance}\n\nState unavailable"
                : $"{descriptor.Provider} / {descriptor.Type} / {descriptor.Instance}\n" +
                  $"state v{instance.StateVersion}, generation {instance.Generation}\n\n{instance.State}",
        };
    }

    private void PrunePluginSurfaces()
    {
        foreach (var pane in pluginSurfaces.Keys.Where(id => !renderedTools.Contains(id)).ToArray())
        {
            pluginSurfaces.Remove(pane, out var surface);
            surface?.Dispose();
        }
    }

    private void SelectPane(ulong id)
    {
        if (client is null || id == paneId) return;
        _ = FocusPaneAsync(id);
    }

    private static TerminalAppearance CreateTerminalAppearance(GuiOptions options) => new(
        TerminalColor(options.Background),
        TerminalColor(options.Foreground),
        TerminalColor(options.Selection),
        options.ColorTable.Select(TerminalColor).ToArray(),
        options.FontFamily,
        options.FontSize);

    private static uint TerminalColor(string value)
    {
        var rgb = Convert.ToUInt32(value.TrimStart('#'), 16);
        return (rgb & 0xff) << 16 | rgb & 0xff00 | rgb >> 16 & 0xff;
    }

    private static global::Windows.UI.Color UiColor(string value)
    {
        var rgb = Convert.ToUInt32(value.TrimStart('#'), 16);
        return global::Windows.UI.Color.FromArgb(255, (byte)(rgb >> 16), (byte)(rgb >> 8), (byte)rgb);
    }

    private static string PaneState(PaneModel pane) =>
        pane.Kind == "terminal" ? pane.Terminal?.State ?? "unavailable" : pane.Kind;

    private static FrameworkElement MissingPane(ulong id) => new TextBlock
    {
        Text = $"Pane {id} is unavailable",
        HorizontalAlignment = HorizontalAlignment.Center,
        VerticalAlignment = VerticalAlignment.Center,
    };

    private void Pane_OnPointerPressed(object sender, PointerRoutedEventArgs args)
    {
        if (client is null || sender is not Border { Tag: ulong id } || id == paneId)
        {
            return;
        }
        _ = FocusPaneAsync(id);
    }

    private async void WorkspacePicker_OnSelectionChanged(object sender, SelectionChangedEventArgs args)
    {
        if (updatingPickers || WorkspacePicker.SelectedItem is not WorkspaceModel selected || selected.Id == workspaceId) return;
        workspaceId = selected.Id;
        var window = snapshot.Windows.FirstOrDefault(value => selected.WindowIds.Contains(value.Id));
        if (window is not null) await SelectWindowAsync(window.Id);
    }

    private async void WindowPicker_OnSelectionChanged(object sender, SelectionChangedEventArgs args)
    {
        if (!updatingPickers && WindowPicker.SelectedItem is WindowModel selected && selected.Id != windowId)
        {
            await SelectWindowAsync(selected.Id);
        }
    }

    private async Task SelectWindowAsync(ulong id)
    {
        if (client is null) return;
        try
        {
            var result = await client.SelectWindowAsync(id, lifetime.Token);
            previewPaneId = 0;
            workspaceId = result.Focus.WorkspaceId;
            windowId = result.Focus.WindowId;
            paneId = result.Focus.PaneId;
            Refresh();
        }
        catch (Exception error)
        {
            SetStatus(error.Message);
        }
    }

    private async void Reconnect_OnClick(object sender, RoutedEventArgs args) => await ConnectAsync();

    private async void About_OnClick(object sender, RoutedEventArgs args)
    {
        var dialog = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = "About Ariadne",
            Content = "Protocol connection and Pane composition are active. Terminal rendering is intentionally isolated as the next compatibility test.",
            CloseButtonText = "Close",
        };
        await dialog.ShowAsync();
    }

    private void SetStatus(string value) => StatusText.Text = value;

    private static ulong FirstPane(LayoutNode node) =>
        node.Kind == "pane" ? node.PaneId : node.Children.Select(FirstPane).FirstOrDefault(value => value != 0);

    private static bool ContainsPane(LayoutNode node, ulong id) =>
        node.Kind == "pane" ? node.PaneId == id : node.Children.Any(child => ContainsPane(child, id));

    private async Task DisconnectAsync()
    {
        DisposeTerminalSurfaces();
        var old = client;
        client = null;
        if (old is null) return;
        old.Store.Changed -= StoreOnChanged;
        old.Failed -= ConnectionOnFailed;
        old.NavigateRequested -= NavigateAsync;
        old.PluginInteractionRequested -= HandlePluginInteractionAsync;
        await old.DisposeAsync();
    }

    private void OnClosed(object sender, WindowEventArgs args)
    {
        if (Interlocked.Exchange(ref shutdownStarted, 1) != 0) return;
        lifetime.Cancel();
        DisposeTerminalSurfaces();
        _ = FinishCloseAsync();
    }

    private async Task FinishCloseAsync()
    {
        await DisconnectAsync();
        lifetime.Dispose();
    }

    private void OnAppWindowClosing(AppWindow sender, AppWindowClosingEventArgs args) => DisposeTerminalSurfaces();

    private void DisposeTerminalSurfaces()
    {
        PaneHost.Children.Clear();
        foreach (var terminal in terminalSurfaces.Values) terminal.Dispose();
        terminalSurfaces.Clear();
        foreach (var surface in pluginSurfaces.Values) surface.Dispose();
        pluginSurfaces.Clear();
    }
}
