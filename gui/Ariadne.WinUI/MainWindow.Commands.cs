using System.Collections;
using Ariadne.Windows.Protocol;
using Ariadne.Terminal.WinUI;
using Ariadne.WinUI.Views;
using Microsoft.UI.Input;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Windows.System;
using Windows.UI.Core;

namespace Ariadne.WinUI;

public sealed partial class MainWindow
{
    private readonly List<string> keySequence = [];

    private static Dictionary<string, string> LegacyKeybindings() => new(StringComparer.Ordinal)
    {
        ["ctrl-a h"] = "focus left",
        ["ctrl-a j"] = "focus down",
        ["ctrl-a k"] = "focus up",
        ["ctrl-a l"] = "focus right",
        ["ctrl-a z"] = "zoom toggle",
        ["ctrl-a ctrl-a"] = "send-key ctrl-a",
        ["ctrl-a %"] = "split h",
        ["ctrl-a \""] = "split v",
        ["ctrl-a :"] = "command-prompt",
    };

    private async void Root_OnKeyDown(object? sender, KeyRoutedEventArgs args)
    {
        try
        {
            await HandleRootKeyDownAsync(args);
        }
        catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
        {
        }
        catch (Exception error)
        {
            SetStatus(error.Message);
        }
    }

    private async Task HandleRootKeyDownAsync(KeyRoutedEventArgs args)
    {
        var control = IsKeyDown(VirtualKey.Control);
        var shift = IsKeyDown(VirtualKey.Shift);
        var alt = IsKeyDown(VirtualKey.Menu);
        if (keySequence.Count == 0 && ((control && shift && args.Key == VirtualKey.V) ||
                                      (shift && !control && args.Key == VirtualKey.Insert)))
        {
            args.Handled = true;
            await PasteClipboardAsync(paneId);
            return;
        }
        if (keySequence.Count == 0 && control && shift && args.Key == VirtualKey.C)
        {
            args.Handled = true;
            await CopySelectionAsync(paneId);
            return;
        }
        if (keySequence.Count == 0 && control && shift && args.Key == VirtualKey.P)
        {
            args.Handled = true;
            await ShowCommandPaletteAsync();
            return;
        }

        var token = PortableKey(args.Key, control, shift, alt);
        if (token is null) return;
        var candidate = string.Join(' ', keySequence.Append(token));
        var exact = keybindings.TryGetValue(candidate, out var command);
        var prefix = keybindings.Keys.Any(value => value.StartsWith(candidate + " ", StringComparison.Ordinal));
        if (!exact && !prefix)
        {
            if (keySequence.Count == 0) return;
            args.Handled = true;
            keySequence.Clear();
            SetStatus("Key sequence cancelled");
            return;
        }

        args.Handled = true;
        if (exact)
        {
            keySequence.Clear();
            await ExecuteKeybindingAsync(command!);
            return;
        }
        keySequence.Add(token);
        SetStatus($"keys: {candidate}");
    }

    private static bool IsKeyDown(VirtualKey key) =>
        InputKeyboardSource.GetKeyStateForCurrentThread(key).HasFlag(CoreVirtualKeyStates.Down);

    private static string? PortableKey(VirtualKey key, bool control, bool shift, bool alt)
    {
        var number = (int)key;
        string? name = key switch
        {
            >= VirtualKey.A and <= VirtualKey.Z => ((char)('a' + number - (int)VirtualKey.A)).ToString(),
            VirtualKey.Left => "left", VirtualKey.Down => "down", VirtualKey.Up => "up", VirtualKey.Right => "right",
            VirtualKey.Enter => "enter", VirtualKey.Escape => "escape", VirtualKey.Tab => "tab",
            VirtualKey.Back => "backspace", VirtualKey.Space => "space",
            VirtualKey.PageUp => "page-up", VirtualKey.PageDown => "page-down",
            VirtualKey.Home => "home", VirtualKey.End => "end", VirtualKey.Delete => "delete",
            VirtualKey.Insert => "insert",
            VirtualKey.Number5 when shift => "%",
            VirtualKey.Number2 when shift => "\"",
            VirtualKey.Number9 when shift => "(",
            VirtualKey.Number0 when shift => ")",
            VirtualKey.Number4 when shift => "$",
            _ when number == 186 && shift => ":",
            _ when number == 222 && shift => "\"",
            _ when number == 219 => "[",
            _ when number == 221 => "]",
            _ when number == 188 => ",",
            _ when number == 191 && shift => "?",
            _ => null,
        };
        if (name is null) return null;
        if (control) return "ctrl-" + name.ToLowerInvariant();
        if (alt) return "alt-" + name.ToLowerInvariant();
        if (shift && name.Length == 1 && char.IsLetter(name[0])) return name.ToUpperInvariant();
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
                case "zoom toggle": ToggleZoom(); break;
                case "zoom on" when zoomPaneId == 0: ToggleZoom(); break;
                case "zoom off" when zoomPaneId != 0: ToggleZoom(); break;
                case "split h": await CreateTerminalAsync(paneId, "horizontal"); break;
                case "split v": await CreateTerminalAsync(paneId, "vertical"); break;
                case "new-window": await CreateWindowAsync(); break;
                case "next-window": await SelectRelativeWindowAsync(1); break;
                case "previous-window": await SelectRelativeWindowAsync(-1); break;
                case "next-workspace": await SelectRelativeWorkspaceAsync(1); break;
                case "previous-workspace": await SelectRelativeWorkspaceAsync(-1); break;
                case "rename-window": await RenameWindowAsync(); break;
                case "rename-workspace": await RenameWorkspaceAsync(); break;
                case "command-prompt":
                case "commands":
                case "help": await ShowCommandPaletteAsync(); break;
                case "restart": await ExecutePaneActionAsync(paneId, PaneAction.Restart); break;
                case "run": await ExecutePaneActionAsync(paneId, PaneAction.RunCommand); break;
                case "stop": await ExecutePaneActionAsync(paneId, PaneAction.Stop); break;
                case "stash-pane": await ExecutePaneActionAsync(paneId, PaneAction.Stash); break;
                case "stash-window": await StashWindowAsync(); break;
                case "stash-list": await ShowCommandPaletteAsync(); break;
                case "close-confirm": await ExecutePaneActionAsync(paneId, PaneAction.Delete); break;
                case "attention next": await MoveAttentionAsync(1); break;
                case "attention prev": await MoveAttentionAsync(-1); break;
                case "attention ack": await AcknowledgeCurrentAttentionAsync(); break;
                case "copy-mode": SetStatus("Select terminal text with mouse or Shift+Arrow, then run copy"); break;
                case "copy": await CopySelectionAsync(paneId); break;
                case "paste": await PasteClipboardAsync(paneId); break;
                case "send-key ctrl-a" when TerminalForPane(paneId) is { } terminal: terminal.SendInput("\x01"); break;
                case "settings": await ShowSettingsAsync(); break;
                case "reconnect": await ConnectAsync(); break;
                case "detach": Close(); return;
                default:
                    SetStatus($"GUI does not support command: {command}");
                    return;
            }
        }
    }

    private async void NewTerminal_OnClick(object sender, RoutedEventArgs args) =>
        await CreateTerminalAsync(paneId, "horizontal");

    private async void SplitBelow_OnClick(object sender, RoutedEventArgs args) =>
        await CreateTerminalAsync(paneId, "vertical");

    private async void NewWindow_OnClick(object sender, RoutedEventArgs args) => await CreateWindowAsync();

    private async void NewWorkspace_OnClick(object sender, RoutedEventArgs args) => await CreateWorkspaceAsync();

    private async void StashWindow_OnClick(object sender, RoutedEventArgs args) => await StashWindowAsync();

    private void Zoom_OnClick(object sender, RoutedEventArgs args) => ToggleZoom();

    private void Exit_OnClick(object sender, RoutedEventArgs args) => Close();

    private async void Commands_OnClick(object sender, RoutedEventArgs args) => await ShowCommandPaletteAsync();

    private async Task CreateTerminalAsync(ulong targetPaneId, string direction)
    {
        if (client is null || windowId == 0) return;
        try
        {
            var result = await client.NewTerminalAsync(
                windowId, targetPaneId, direction, DefaultShell(client.Gui),
                Environment.CurrentDirectory, CurrentEnvironment(), 120, 30, lifetime.Token);
            await FocusPaneAsync(result.Pane.Id);
            SetStatus("Terminal created");
        }
        catch (Exception error)
        {
            SetStatus(error.Message);
        }
    }

    private async Task CreateWindowAsync()
    {
        if (client is null || workspaceId == 0) return;
        try
        {
            var created = await client.CreateWindowAsync(workspaceId, $"window-{snapshot.NextWindowId}", lifetime.Token);
            var terminal = await client.NewTerminalAsync(created.Window.Id, 0, "horizontal",
                DefaultShell(client.Gui), Environment.CurrentDirectory, CurrentEnvironment(), 120, 30, lifetime.Token);
            windowId = created.Window.Id;
            await FocusPaneAsync(terminal.Pane.Id);
        }
        catch (Exception error)
        {
            SetStatus(error.Message);
        }
    }

    private async Task SelectRelativeWindowAsync(int delta)
    {
        var workspace = snapshot.Workspaces.FirstOrDefault(value => value.Id == workspaceId);
        if (workspace is null) return;
        var stashed = snapshot.StashedWindows.Select(value => value.WindowId).ToHashSet();
        var ids = workspace.WindowIds.Where(id => !stashed.Contains(id)).ToList();
        if (ids.Count == 0) return;
        var index = Math.Max(0, ids.IndexOf(windowId));
        await SelectWindowAsync(ids[(index + delta + ids.Count) % ids.Count]);
    }

    private async Task SelectRelativeWorkspaceAsync(int delta)
    {
        if (snapshot.Workspaces.Count == 0) return;
        var index = snapshot.Workspaces.FindIndex(value => value.Id == workspaceId);
        if (index < 0) index = 0;
        var target = snapshot.Workspaces[(index + delta + snapshot.Workspaces.Count) % snapshot.Workspaces.Count];
        var stashed = snapshot.StashedWindows.Select(value => value.WindowId).ToHashSet();
        var targetWindow = target.WindowIds.FirstOrDefault(id => !stashed.Contains(id));
        if (targetWindow != 0) await SelectWindowAsync(targetWindow);
    }

    private async Task RenameWindowAsync()
    {
        if (client is null || windowId == 0) return;
        var current = snapshot.Windows.FirstOrDefault(value => value.Id == windowId)?.Name ?? "";
        var name = await PromptAsync("Rename Window", "Name", current);
        if (!string.IsNullOrWhiteSpace(name)) await client.RenameWindowAsync(windowId, name, lifetime.Token);
    }

    private async Task RenameWorkspaceAsync()
    {
        if (client is null || workspaceId == 0) return;
        var current = snapshot.Workspaces.FirstOrDefault(value => value.Id == workspaceId)?.Name ?? "";
        var name = await PromptAsync("Rename Workspace", "Name", current);
        if (!string.IsNullOrWhiteSpace(name)) await client.RenameWorkspaceAsync(workspaceId, name, lifetime.Token);
    }

    private async Task StashWindowAsync()
    {
        if (client is null || windowId == 0) return;
        await client.StashWindowAsync(windowId, lifetime.Token);
        SetStatus($"Window {windowId} stashed");
    }

    private async Task MoveAttentionAsync(int delta)
    {
        var alerts = snapshot.Attentions.Where(value => value.AcknowledgedAt is null)
            .OrderBy(value => value.UpdatedAt).ToList();
        if (alerts.Count == 0)
        {
            SetStatus("No unacknowledged alerts");
            return;
        }
        var index = alerts.FindIndex(value => value.PaneId == paneId);
        if (index < 0) index = delta > 0 ? -1 : 0;
        await NavigateAsync(alerts[(index + delta + alerts.Count) % alerts.Count].PaneId);
    }

    private async Task AcknowledgeCurrentAttentionAsync()
    {
        if (client is null) return;
        var attention = snapshot.Attentions.Where(value => value.AcknowledgedAt is null && value.PaneId == paneId)
            .OrderByDescending(value => value.UpdatedAt).FirstOrDefault();
        if (attention is null)
        {
            SetStatus("Focused Pane has no unacknowledged alert");
            return;
        }
        await client.AcknowledgeAttentionAsync(attention.Id, lifetime.Token);
    }

    private MenuFlyout CreatePaneFlyout(PaneModel pane)
    {
        var menu = new MenuFlyout();
        AddPaneMenuItem(menu, "Split right", pane.Id, PaneAction.SplitRight, true);
        AddPaneMenuItem(menu, "Split below", pane.Id, PaneAction.SplitBelow, true);
        menu.Items.Add(new MenuFlyoutSeparator());
        if (pane.Kind == "terminal")
        {
            var state = pane.Terminal?.State;
            var dormant = state is "exited" or "failed" or "placeholder";
            AddPaneMenuItem(menu, "Restart", pane.Id, PaneAction.Restart, dormant);
            AddPaneMenuItem(menu, "Run command…", pane.Id, PaneAction.RunCommand, dormant);
            AddPaneMenuItem(menu, "Stop", pane.Id, PaneAction.Stop, state == "running");
            AddPaneMenuItem(menu, "Copy", pane.Id, PaneAction.Copy, TerminalForPane(pane.Id) is not null);
            AddPaneMenuItem(menu, "Paste", pane.Id, PaneAction.Paste, state == "running");
        }
        menu.Items.Add(new MenuFlyoutSeparator());
        AddPaneMenuItem(menu, "Stash", pane.Id, PaneAction.Stash, !pane.Transient);
        AddPaneMenuItem(menu, "Delete", pane.Id, PaneAction.Delete,
            pane.Kind != "terminal" || pane.Terminal?.State is "exited" or "failed" or "placeholder");
        return menu;
    }

    private void AddPaneMenuItem(MenuFlyout menu, string text, ulong target, PaneAction action, bool enabled)
    {
        var item = new MenuFlyoutItem { Text = text, IsEnabled = enabled };
        item.Click += async (_, _) => await ExecutePaneActionAsync(target, action);
        menu.Items.Add(item);
    }

    private async Task ExecutePaneActionAsync(ulong target, PaneAction action)
    {
        if (client is null || target == 0) return;
        try
        {
            switch (action)
            {
                case PaneAction.SplitRight: await CreateTerminalAsync(target, "horizontal"); return;
                case PaneAction.SplitBelow: await CreateTerminalAsync(target, "vertical"); return;
                case PaneAction.Restart:
                    await client.RestartTerminalAsync(target, CurrentEnvironment(), 120, 30, lifetime.Token);
                    SetStatus($"Pane {target} restarted");
                    break;
                case PaneAction.RunCommand:
                    var command = await PromptAsync("Run command", "PowerShell command");
                    if (command is null) return;
                    await client.RunTerminalAsync(target,
                        ["powershell.exe", "-NoLogo", "-Command", command], Environment.CurrentDirectory,
                        CurrentEnvironment(), 120, 30, lifetime.Token);
                    SetStatus($"Command started in Pane {target}");
                    break;
                case PaneAction.Stop:
                    await client.StopTerminalAsync(target, lifetime.Token);
                    SetStatus($"Pane {target} stopped");
                    break;
                case PaneAction.Stash:
                    await client.StashPaneAsync(target, lifetime.Token);
                    SetStatus($"Pane {target} stashed");
                    break;
                case PaneAction.Delete:
                    if (!await ConfirmAsync("Delete Pane?", $"Delete Pane {target}?")) return;
                    await client.DeletePaneAsync(target, lifetime.Token);
                    SetStatus($"Pane {target} deleted");
                    break;
                case PaneAction.Copy: await CopySelectionAsync(target); break;
                case PaneAction.Paste: await PasteClipboardAsync(target); break;
            }
        }
        catch (Exception error)
        {
            SetStatus(error.Message);
        }
    }

    private async Task<string?> PromptAsync(string title, string label, string initial = "")
    {
        var input = new TextBox { Header = label, Text = initial, MinWidth = 480 };
        var dialog = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = title,
            Content = input,
            PrimaryButtonText = "Run",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
        };
        return await dialog.ShowAsync() == ContentDialogResult.Primary ? input.Text : null;
    }

    private async Task<bool> ConfirmAsync(string title, string message)
    {
        var dialog = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = title,
            Content = message,
            PrimaryButtonText = "Delete",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Close,
        };
        return await dialog.ShowAsync() == ContentDialogResult.Primary;
    }

    private TerminalSurface? TerminalForPane(ulong id)
    {
        var terminalId = snapshot.Panes.FirstOrDefault(value => value.Id == id)?.Terminal?.Id;
        return terminalId is ulong value && terminalSurfaces.TryGetValue(value, out var terminal) ? terminal : null;
    }

    private async Task CopySelectionAsync(ulong target)
    {
        var text = TerminalForPane(target)?.GetSelectedText() ?? "";
        if (text.Length == 0)
        {
            SetStatus("No terminal selection to copy");
            return;
        }
        if (client is not null) await client.WriteClipboardAsync(target, text, lifetime.Token);
        SetStatus("Selection copied to shared clipboard");
    }

    private async Task PasteClipboardAsync(ulong target)
    {
        if (client is null) return;
        if (TerminalForPane(target) is { } terminal)
        {
            var text = (await client.ReadClipboardAsync(target, lifetime.Token)).Text;
            if (text.Length != 0) terminal.SendInput(text);
            return;
        }
        if (pluginSurfaces.TryGetValue(target, out var plugin))
        {
            await plugin.PasteClipboardAsync(lifetime.Token);
            return;
        }
        SetStatus("Focused Pane does not accept pasted text");
    }

    private static string[] CurrentEnvironment() => Environment.GetEnvironmentVariables()
        .Cast<DictionaryEntry>()
        .Select(value => $"{value.Key}={value.Value}")
        .ToArray();

    private static IReadOnlyList<string> DefaultShell(GuiOptions options) =>
        options.Shell.Count == 0 ? ["powershell.exe", "-NoLogo"] : options.Shell;

    private void ToggleZoom()
    {
        if (paneId == 0) return;
        zoomPaneId = zoomPaneId == 0 ? paneId : 0;
        Refresh();
        SetStatus(zoomPaneId == 0 ? "Zoom off" : $"Pane {paneId} zoomed");
    }

    private async Task FocusPaneAsync(ulong target)
    {
        if (client is null || target == 0) return;
        requestedFocusPaneId = target;
        previewPaneId = 0;
        paneId = target;
        Refresh();
        var entered = false;
        try
        {
            await focusGate.WaitAsync(lifetime.Token);
            entered = true;
            if (requestedFocusPaneId != target || client is null) return;
            var result = await client.SetFocusAsync(target, lifetime.Token);
            if (requestedFocusPaneId != target) return;
            workspaceId = result.Focus.WorkspaceId;
            windowId = result.Focus.WindowId;
            paneId = result.Focus.PaneId;
            Refresh();
        }
        catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
        {
        }
        catch (Exception error)
        {
            if (requestedFocusPaneId == target) SetStatus(error.Message);
        }
        finally
        {
            if (entered) focusGate.Release();
        }
    }

    private readonly record struct PaneRect(double X, double Y, double Width, double Height);

    private async Task MoveFocusAsync(int horizontal, int vertical)
    {
        if (zoomPaneId != 0) return;
        var window = snapshot.Windows.FirstOrDefault(value => value.Id == windowId);
        if (window?.Layout is null) return;
        var rectangles = new Dictionary<ulong, PaneRect>();
        CollectPaneRects(window.Layout, new PaneRect(0, 0, 1, 1), rectangles);
        if (!rectangles.TryGetValue(paneId, out var origin)) return;
        var originX = origin.X + origin.Width / 2;
        var originY = origin.Y + origin.Height / 2;
        ulong best = 0;
        var bestScore = double.MaxValue;
        foreach (var (id, rectangle) in rectangles)
        {
            if (id == paneId) continue;
            var deltaX = rectangle.X + rectangle.Width / 2 - originX;
            var deltaY = rectangle.Y + rectangle.Height / 2 - originY;
            var primary = horizontal != 0 ? deltaX * horizontal : deltaY * vertical;
            if (primary <= 0) continue;
            var secondary = horizontal != 0 ? Math.Abs(deltaY) : Math.Abs(deltaX);
            var score = primary * 1000 + secondary;
            if (score < bestScore) (best, bestScore) = (id, score);
        }
        if (best != 0) await FocusPaneAsync(best);
    }

    private static void CollectPaneRects(LayoutNode node, PaneRect bounds, IDictionary<ulong, PaneRect> result)
    {
        if (node.Kind == "pane")
        {
            result[node.PaneId] = bounds;
            return;
        }
        var horizontal = node.Direction == "horizontal";
        var weights = Enumerable.Range(0, node.Children.Count)
            .Select(index => index < node.Weights.Count && node.Weights[index] > 0 ? node.Weights[index] : 1u)
            .ToArray();
        var total = Math.Max(1d, weights.Sum(value => (double)value));
        var offset = 0d;
        for (var index = 0; index < node.Children.Count; index++)
        {
            var fraction = weights[index] / total;
            var child = horizontal
                ? new PaneRect(bounds.X + bounds.Width * offset, bounds.Y, bounds.Width * fraction, bounds.Height)
                : new PaneRect(bounds.X, bounds.Y + bounds.Height * offset, bounds.Width, bounds.Height * fraction);
            CollectPaneRects(node.Children[index], child, result);
            offset += fraction;
        }
    }

    private async Task ResizeFocusedPaneAsync(string direction, int neighborDelta)
    {
        if (client is null || snapshot.Windows.FirstOrDefault(value => value.Id == windowId)?.Layout is not { } layout) return;
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
                SetStatus("Adjacent Pane is already at its minimum weight");
                return;
            }
            weights[childIndex]++;
            weights[neighbor]--;
            await client.ResizeSplitAsync(node.SplitId, weights, lifetime.Token);
            return;
        }
        SetStatus("No adjacent Pane in that direction");
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

    private async Task ShowCommandPaletteAsync()
    {
        var selected = await CommandPaletteDialog.ShowAsync(Content.XamlRoot, BuildCommandPaletteItems());
        if (selected is not null) await ExecutePaletteCommandAsync(selected.Id);
    }

    private Task NavigateAsync(ulong target)
    {
        var completion = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        DispatcherQueue.TryEnqueue(async () =>
        {
            try
            {
                var pane = snapshot.Panes.FirstOrDefault(value => value.Id == target);
                if (pane is null || pane.Transient)
                    throw new InvalidOperationException("Pane not found");
                var stashed = snapshot.StashedPanes.Any(value => value.PaneId == target) ||
                              snapshot.StashedWindows.Any(value => value.WindowId == pane.WindowId);
                if (stashed)
                {
                    previewPaneId = target;
                    paneId = target;
                    zoomPaneId = 0;
                    Refresh();
                }
                else
                {
                    await FocusPaneAsync(target);
                }
                Activate();
                completion.TrySetResult();
            }
            catch (Exception error)
            {
                completion.TrySetException(error);
            }
        });
        return completion.Task;
    }

    private void Attention_OnClick(object sender, RoutedEventArgs args)
    {
        var menu = new MenuFlyout();
        foreach (var attention in snapshot.Attentions
                     .Where(value => value.AcknowledgedAt is null)
                     .OrderByDescending(value => value.UpdatedAt))
        {
            var message = string.IsNullOrWhiteSpace(attention.Message) ? attention.Class : attention.Message;
            var item = new MenuFlyoutItem { Text = $"{attention.Severity}: {message}" };
            item.Click += async (_, _) => await OpenAttentionAsync(attention);
            menu.Items.Add(item);
        }
        if (menu.Items.Count == 0)
            menu.Items.Add(new MenuFlyoutItem { Text = "No unacknowledged alerts", IsEnabled = false });
        menu.ShowAt(AttentionButton);
    }

    private async Task OpenAttentionAsync(AttentionModel attention)
    {
        if (client is null) return;
        try
        {
            await NavigateAsync(attention.PaneId);
            await client.AcknowledgeAttentionAsync(attention.Id, lifetime.Token);
            SetStatus($"Alert {attention.Id} acknowledged");
        }
        catch (Exception error)
        {
            SetStatus(error.Message);
        }
    }

    private List<CommandPaletteItem> BuildCommandPaletteItems()
    {
        var items = new List<CommandPaletteItem>
        {
            new("split-right", "split h — split focused Pane to the right"),
            new("split-below", "split v — split focused Pane below"),
            new("new-window", "new-window — create a Window and terminal"),
            new("new-workspace", "new-workspace — create a Workspace, Window, and terminal"),
            new("next-window", "next-window — select next Window"),
            new("previous-window", "previous-window — select previous Window"),
            new("next-workspace", "next-workspace — select next Workspace"),
            new("previous-workspace", "previous-workspace — select previous Workspace"),
            new("rename-window", "rename-window — rename selected Window"),
            new("rename-workspace", "rename-workspace — rename selected Workspace"),
            new("stash-window", "stash-window — stash selected Window"),
            new("zoom", "zoom toggle — toggle focused Pane zoom"),
            new("focus-left", "focus left"), new("focus-down", "focus down"),
            new("focus-up", "focus up"), new("focus-right", "focus right"),
            new("resize-left", "resize left"), new("resize-down", "resize down"),
            new("resize-up", "resize up"), new("resize-right", "resize right"),
            new("copy", "copy — copy terminal selection to shared clipboard"),
            new("paste", "paste — paste shared clipboard"),
            new("send-prefix", "send-key ctrl-a"),
            new("settings", "settings — edit shared GUI/TUI configuration"),
            new("reconnect", "reconnect frontend"), new("detach", "detach frontend"),
        };
        var pane = snapshot.Panes.FirstOrDefault(value => value.Id == paneId);
        var state = pane?.Terminal?.State;
        if (pane?.Kind == "terminal" && state is "exited" or "failed" or "placeholder")
        {
            items.Add(new("restart", "restart focused terminal"));
            items.Add(new("run", "run command in focused Pane"));
            items.Add(new("delete", "delete focused Pane"));
        }
        if (pane?.Kind == "terminal" && state == "running") items.Add(new("stop", "stop focused terminal"));
        if (pane is { Transient: false }) items.Add(new("stash", "stash focused Pane"));
        items.AddRange(snapshot.StashedPanes.Select(value =>
            new CommandPaletteItem($"restore-pane:{value.PaneId}", $"restore Pane {value.PaneId}")));
        items.AddRange(snapshot.StashedWindows.Select(value =>
            new CommandPaletteItem($"restore-window:{value.WindowId}", $"restore Window {value.WindowId}")));
        return items;
    }

    private async Task ExecutePaletteCommandAsync(string command)
    {
        switch (command)
        {
            case "split-right": await CreateTerminalAsync(paneId, "horizontal"); return;
            case "split-below": await CreateTerminalAsync(paneId, "vertical"); return;
            case "new-window": await CreateWindowAsync(); return;
            case "new-workspace": await CreateWorkspaceAsync(); return;
            case "next-window": await SelectRelativeWindowAsync(1); return;
            case "previous-window": await SelectRelativeWindowAsync(-1); return;
            case "next-workspace": await SelectRelativeWorkspaceAsync(1); return;
            case "previous-workspace": await SelectRelativeWorkspaceAsync(-1); return;
            case "rename-window": await RenameWindowAsync(); return;
            case "rename-workspace": await RenameWorkspaceAsync(); return;
            case "stash-window": await StashWindowAsync(); return;
            case "zoom": ToggleZoom(); return;
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
            case "copy": await CopySelectionAsync(paneId); return;
            case "paste": await PasteClipboardAsync(paneId); return;
            case "send-prefix" when TerminalForPane(paneId) is { } terminal: terminal.SendInput("\x01"); return;
            case "settings": await ShowSettingsAsync(); return;
            case "reconnect": await ConnectAsync(); return;
            case "detach": Close(); return;
        }
        if (command.StartsWith("restore-pane:", StringComparison.Ordinal) &&
            ulong.TryParse(command["restore-pane:".Length..], out var restorePane) && client is not null)
        {
            var result = await client.RestorePaneAsync(restorePane, lifetime.Token);
            await FocusPaneAsync(result.Pane.Id);
        }
        else if (command.StartsWith("restore-window:", StringComparison.Ordinal) &&
                 ulong.TryParse(command["restore-window:".Length..], out var restoreWindow) && client is not null)
        {
            var result = await client.RestoreWindowAsync(restoreWindow, lifetime.Token);
            await SelectWindowAsync(result.Window.Id);
        }
    }

    private async Task CreateWorkspaceAsync()
    {
        if (client is null) return;
        var name = await PromptAsync("New Workspace", "Name", $"workspace-{snapshot.NextWorkspaceId}");
        if (string.IsNullOrWhiteSpace(name)) return;
        try
        {
            var workspace = await client.CreateWorkspaceAsync(name, lifetime.Token);
            workspaceId = workspace.Workspace.Id;
            await CreateWindowAsync();
        }
        catch (Exception error)
        {
            SetStatus(error.Message);
        }
    }

    private async void Settings_OnClick(object sender, RoutedEventArgs args) => await ShowSettingsAsync();

    private async Task ShowSettingsAsync()
    {
        if (client is null)
        {
            SetStatus("Frontend is disconnected");
            return;
        }
        var defaults = client.Gui.DefaultKeybindings.Count == 0
            ? LegacyKeybindings()
            : client.Gui.DefaultKeybindings;
        var result = await SettingsDialog.ShowAsync(Content.XamlRoot, keybindings, defaults,
            client.Gui.FontSize, client.Gui.ConfigPath);
        if (result is null) return;

        keybindings = result.Bindings;
        client.Gui.FontSize = result.FontSize;
        ApplyGuiOptions(client.Gui);
        try
        {
            var reloaded = await client.ReloadFrontendConfigAsync(lifetime.Token);
            keybindings = reloaded.Keybindings.Count == 0
                ? LegacyKeybindings()
                : new Dictionary<string, string>(reloaded.Keybindings, StringComparer.Ordinal);
            ApplyGuiOptions(reloaded);
            SetStatus("Settings saved and applied");
        }
        catch (Exception error)
        {
            SetStatus($"Settings applied locally; daemon reload failed: {error.Message}");
        }
    }

    private void ApplyGuiOptions(GuiOptions options)
    {
        var appearance = CreateTerminalAppearance(options);
        foreach (var terminal in terminalSurfaces.Values) terminal.ApplyAppearance(appearance);
        foreach (var surface in pluginSurfaces.Values) surface.ApplyOptions(options);
        var background = UiColor(options.Background);
        if (Application.Current.Resources["TerminalSurfaceBrush"] is Microsoft.UI.Xaml.Media.SolidColorBrush terminalBrush)
            terminalBrush.Color = background;
        if (Application.Current.Resources["PaneSurfaceBrush"] is Microsoft.UI.Xaml.Media.SolidColorBrush paneBrush)
            paneBrush.Color = global::Windows.UI.Color.FromArgb(225, background.R, background.G, background.B);
        if (Application.Current.Resources["PaneHeaderBrush"] is Microsoft.UI.Xaml.Media.SolidColorBrush headerBrush)
            headerBrush.Color = global::Windows.UI.Color.FromArgb(245,
                (byte)Math.Min(255, background.R + 18),
                (byte)Math.Min(255, background.G + 18),
                (byte)Math.Min(255, background.B + 18));
        Root.Background = new Microsoft.UI.Xaml.Media.SolidColorBrush(background);
        Root.RequestedTheme = background.R + background.G + background.B < 384
            ? ElementTheme.Dark
            : ElementTheme.Light;
        Refresh();
    }
}

internal enum PaneAction
{
    SplitRight,
    SplitBelow,
    Restart,
    RunCommand,
    Stop,
    Delete,
    Stash,
    Copy,
    Paste,
}
