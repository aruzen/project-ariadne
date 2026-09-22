using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using System.Windows.Media;
using Ariadne.Windows.Protocol;
using Ariadne.Windows.Terminal;
using Microsoft.Terminal.Wpf;

namespace Ariadne.Windows.Views;

internal sealed class TerminalPaneView : Border, IDisposable
{
    private readonly AriadneTerminalConnection connection;
    private readonly TerminalControl terminal;
    private readonly TextBlock title;
    private bool focused;

    public TerminalPaneView(AriadneClient client, PaneModel pane)
    {
        PaneId = pane.Id;
        TerminalId = pane.Terminal?.Id ?? throw new ArgumentException("pane has no terminal ID", nameof(pane));
        Background = new SolidColorBrush(Color.FromRgb(12, 15, 19));
        BorderThickness = new Thickness(1);
        BorderBrush = Brushes.Transparent;
        SnapsToDevicePixels = true;

        title = new TextBlock
        {
            Padding = new Thickness(7, 2, 7, 3),
            Foreground = new SolidColorBrush(Color.FromRgb(190, 198, 208)),
            Background = new SolidColorBrush(Color.FromRgb(28, 33, 40)),
            TextTrimming = TextTrimming.CharacterEllipsis,
        };
        terminal = new TerminalControl
        {
            AutoResize = true,
            Focusable = true,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            VerticalAlignment = VerticalAlignment.Stretch,
        };
        terminal.SetTheme(CreateTheme(), "Cascadia Mono", 14, Colors.Transparent);
        connection = new AriadneTerminalConnection(client, TerminalId, Dispatcher);
        connection.Failed += error => ConnectionFailed?.Invoke(this, error);
        terminal.Connection = connection;

        var content = new DockPanel();
        DockPanel.SetDock(title, Dock.Top);
        content.Children.Add(title);
        content.Children.Add(terminal);
        Child = content;
        Update(pane, false);
        PreviewMouseDown += OnPreviewMouseDown;
        terminal.PreviewKeyDown += TerminalOnPreviewKeyDown;
    }

    public ulong PaneId { get; }
    public ulong TerminalId { get; }
    public event EventHandler? FocusRequested;
    public event EventHandler<Exception>? ConnectionFailed;
    public event EventHandler<PaneActionEventArgs>? ActionRequested;
    public event Action<string>? CopyRequested;
    public event EventHandler? PasteRequested;

    public void Update(PaneModel pane, bool isFocused)
    {
        focused = isFocused;
        var command = pane.Terminal?.Launch.Argv.FirstOrDefault() ?? "terminal";
        title.Text = string.IsNullOrWhiteSpace(pane.Title) ? $"{pane.Id}: {command}" : pane.Title;
        BorderBrush = isFocused
            ? new SolidColorBrush(Color.FromRgb(81, 145, 255))
            : Brushes.Transparent;
        title.Foreground = isFocused ? Brushes.White : new SolidColorBrush(Color.FromRgb(190, 198, 208));
        ContextMenu = PaneContextMenu.Create(pane, (sender, args) => ActionRequested?.Invoke(this, args));
    }

    public void FocusTerminal() => terminal.Focus();

    private void OnPreviewMouseDown(object sender, MouseButtonEventArgs e)
    {
        if (!focused)
        {
            FocusRequested?.Invoke(this, EventArgs.Empty);
        }
        terminal.Focus();
    }

    private void TerminalOnPreviewKeyDown(object sender, KeyEventArgs e)
    {
        var modifiers = Keyboard.Modifiers;
        var copy = e.Key == Key.C && modifiers.HasFlag(ModifierKeys.Control) && modifiers.HasFlag(ModifierKeys.Shift) ||
                   e.Key == Key.Insert && modifiers.HasFlag(ModifierKeys.Control);
        var paste = e.Key == Key.V && modifiers.HasFlag(ModifierKeys.Control) && modifiers.HasFlag(ModifierKeys.Shift) ||
                    e.Key == Key.Insert && modifiers.HasFlag(ModifierKeys.Shift);
        try
        {
            if (copy)
            {
                var selected = terminal.GetSelectedText();
                if (!string.IsNullOrEmpty(selected))
                {
                    Clipboard.SetText(selected, TextDataFormat.UnicodeText);
                    CopyRequested?.Invoke(selected);
                }
                e.Handled = true;
            }
            else if (paste)
            {
                PasteRequested?.Invoke(this, EventArgs.Empty);
                e.Handled = true;
            }
        }
        catch (Exception ex)
        {
            ConnectionFailed?.Invoke(this, ex);
            e.Handled = true;
        }
    }

    public void Paste(string value) => connection.WriteInput(value);
    public void SendInput(string value) => connection.WriteInput(value);

    private static TerminalTheme CreateTheme() => new()
    {
        DefaultBackground = 0x000F0C0C,
        DefaultForeground = 0x00EDEAE8,
        DefaultSelectionBackground = 0x006B4C26,
        ColorTable =
        [
            0x00000000, 0x002222CC, 0x0022AA22, 0x00AAAA22,
            0x00CC7722, 0x00AA22AA, 0x0022AAAA, 0x00CCCCCC,
            0x00666666, 0x006666FF, 0x0066DD66, 0x00DDDD66,
            0x00FFAA66, 0x00DD66DD, 0x0066DDDD, 0x00FFFFFF,
        ],
    };

    public void Dispose()
    {
        PreviewMouseDown -= OnPreviewMouseDown;
        terminal.PreviewKeyDown -= TerminalOnPreviewKeyDown;
        terminal.Connection = null!;
        connection.Dispose();
    }
}
