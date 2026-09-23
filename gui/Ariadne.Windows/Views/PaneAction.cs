using Ariadne.Windows.Protocol;
using System.Windows.Media;

namespace Ariadne.Windows.Views;

internal enum PaneAction
{
    SplitRight,
    SplitBelow,
    Restart,
    RunCommand,
    Stop,
    Delete,
    Stash,
}

internal sealed class PaneActionEventArgs(ulong paneId, PaneAction action) : EventArgs
{
    public ulong PaneId { get; } = paneId;
    public PaneAction Action { get; } = action;
}

internal static class PaneContextMenu
{
    public static System.Windows.Controls.ContextMenu Create(PaneModel pane, EventHandler<PaneActionEventArgs> handler)
    {
        var menu = new System.Windows.Controls.ContextMenu
        {
            Background = new SolidColorBrush(Color.FromRgb(245, 246, 248)),
            Foreground = new SolidColorBrush(Color.FromRgb(32, 36, 42)),
        };
        Add(menu, "Split right", PaneAction.SplitRight, pane.Id, true, handler);
        Add(menu, "Split below", PaneAction.SplitBelow, pane.Id, true, handler);
        menu.Items.Add(new System.Windows.Controls.Separator());
        if (pane.Kind == "terminal")
        {
            var state = pane.Terminal?.State;
            Add(menu, "Restart", PaneAction.Restart, pane.Id, state is "exited" or "failed" or "placeholder", handler);
            Add(menu, "Run command…", PaneAction.RunCommand, pane.Id, state is "exited" or "failed" or "placeholder", handler);
            Add(menu, "Stop", PaneAction.Stop, pane.Id, state is "running", handler);
        }
        Add(menu, "Stash", PaneAction.Stash, pane.Id, !pane.Transient, handler);
        Add(menu, "Delete", PaneAction.Delete, pane.Id,
            pane.Kind != "terminal" || pane.Terminal?.State is "exited" or "failed" or "placeholder", handler);
        return menu;
    }

    private static void Add(
        System.Windows.Controls.ContextMenu menu,
        string title,
        PaneAction action,
        ulong paneId,
        bool enabled,
        EventHandler<PaneActionEventArgs> handler)
    {
        var item = new System.Windows.Controls.MenuItem { Header = title, IsEnabled = enabled };
        item.Click += (sender, args) => handler(sender, new PaneActionEventArgs(paneId, action));
        menu.Items.Add(item);
    }
}
