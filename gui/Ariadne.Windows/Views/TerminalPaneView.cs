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
    private readonly Brush accent;
    private bool focused;

    public TerminalPaneView(AriadneClient client, PaneModel pane, GuiOptions options)
    {
        PaneId = pane.Id;
        TerminalId = pane.Terminal?.Id ?? throw new ArgumentException("pane has no terminal ID", nameof(pane));
        Background = new SolidColorBrush(Color.FromRgb(12, 15, 19));
        accent = new SolidColorBrush((Color)ColorConverter.ConvertFromString(options.Accent));
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
        terminal.SetTheme(CreateTheme(options), options.FontFamily, (short)Math.Clamp(Math.Round(options.FontSize), 6, 96), Colors.Transparent);
        connection = new AriadneTerminalConnection(client, TerminalId, Dispatcher);
        connection.Failed += error => ConnectionFailed?.Invoke(this, error);
        terminal.Connection = connection;

        var content = new DockPanel();
        DockPanel.SetDock(title, Dock.Top);
        content.Children.Add(title);
        content.Children.Add(terminal);
        Child = content;
        Update(pane, false);
        // TerminalControl handles mouse input internally. Listen on the control
        // itself and include handled events so pane selection is not limited to
        // the title bar or placeholder panes.
        terminal.AddHandler(Mouse.PreviewMouseDownEvent, new MouseButtonEventHandler(OnPreviewMouseDown), true);
        terminal.AddHandler(Keyboard.GotKeyboardFocusEvent, new KeyboardFocusChangedEventHandler(OnTerminalGotKeyboardFocus), true);
        terminal.AddHandler(Keyboard.PreviewKeyDownEvent, new KeyEventHandler(TerminalOnPreviewKeyDown), true);
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
        var command = pane.Terminal?.Launch.Argv.FirstOrDefault() ?? "terminal";
        title.Text = string.IsNullOrWhiteSpace(pane.Title) ? $"{pane.Id}: {command}" : pane.Title;
        SetFocused(isFocused);
        ContextMenu = PaneContextMenu.Create(pane, (sender, args) => ActionRequested?.Invoke(this, args));
    }

    public void SetFocused(bool isFocused)
    {
        focused = isFocused;
        BorderBrush = isFocused ? accent : Brushes.Transparent;
        title.Foreground = isFocused ? Brushes.White : new SolidColorBrush(Color.FromRgb(190, 198, 208));
    }

    public void ApplyOptions(GuiOptions options) =>
        terminal.SetTheme(CreateTheme(options), options.FontFamily,
            (short)Math.Clamp(Math.Round(options.FontSize), 6, 96), Colors.Transparent);

    public void FocusTerminal() => terminal.Focus();

    private void OnPreviewMouseDown(object sender, MouseButtonEventArgs e)
    {
        if (e.ChangedButton != MouseButton.Left)
        {
            return;
        }
        if (!focused)
        {
            FocusRequested?.Invoke(this, EventArgs.Empty);
        }
        terminal.Focus();
    }

    private void OnTerminalGotKeyboardFocus(object sender, KeyboardFocusChangedEventArgs e)
    {
        if (!focused)
        {
            FocusRequested?.Invoke(this, EventArgs.Empty);
        }
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
                CopySelection();
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

    public bool CopySelection()
    {
        var selected = terminal.GetSelectedText();
        if (string.IsNullOrEmpty(selected))
        {
            return false;
        }
        Clipboard.SetText(selected, TextDataFormat.UnicodeText);
        CopyRequested?.Invoke(selected);
        return true;
    }

    public void Paste(string value) => connection.WriteInput(value);
    public void SendInput(string value) => connection.WriteInput(value);

    private static TerminalTheme CreateTheme(GuiOptions options) => new()
    {
        DefaultBackground = TerminalColor(options.Background),
        DefaultForeground = TerminalColor(options.Foreground),
        DefaultSelectionBackground = TerminalColor(options.Selection),
        ColorTable = options.ColorTable.Select(TerminalColor).ToArray(),
    };

    private static uint TerminalColor(string value)
    {
        var color = (Color)ColorConverter.ConvertFromString(value);
        return (uint)(color.B << 16 | color.G << 8 | color.R);
    }

    public void Dispose()
    {
        terminal.RemoveHandler(Mouse.PreviewMouseDownEvent, new MouseButtonEventHandler(OnPreviewMouseDown));
        terminal.RemoveHandler(Keyboard.GotKeyboardFocusEvent, new KeyboardFocusChangedEventHandler(OnTerminalGotKeyboardFocus));
        terminal.RemoveHandler(Keyboard.PreviewKeyDownEvent, new KeyEventHandler(TerminalOnPreviewKeyDown));
        terminal.Connection = null!;
        connection.Dispose();
    }
}
