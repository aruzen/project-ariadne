#nullable enable
using Microsoft.Terminal.Wpf;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;

namespace Ariadne.Terminal.WinUI;

public sealed class TerminalSurface : UserControl, IDisposable
{
    private readonly TerminalControl terminal;
    private readonly TerminalConnectionAdapter connection;
    private TerminalAppearance appearance;
    private int disposed;

    public TerminalSurface(ITerminalBackend backend, TerminalAppearance appearance)
    {
        this.appearance = appearance;
        connection = new TerminalConnectionAdapter(backend, DispatcherQueue);
        connection.Failed += error => ConnectionFailed?.Invoke(this, error);
        connection.OutputDecoded += value => OutputReceived?.Invoke(this, value);
        terminal = new TerminalControl
        {
            AutoResize = true,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            VerticalAlignment = VerticalAlignment.Stretch,
        };
        terminal.GotFocus += (_, _) => FocusEntered?.Invoke(this, EventArgs.Empty);
        terminal.AddHandler(UIElement.PreviewKeyDownEvent,
            new KeyEventHandler((_, args) => KeyPressed?.Invoke(this, args)), true);
        terminal.Loaded += (_, _) => ApplyAppearance(this.appearance);
        Content = terminal;
        ApplyAppearance(appearance);
        terminal.Connection = connection;
    }

    public event EventHandler? FocusEntered;
    public event EventHandler<Exception>? ConnectionFailed;
    public event EventHandler<string>? OutputReceived;
    public event EventHandler<KeyRoutedEventArgs>? KeyPressed;
    public Task BackendReady => connection.BackendReady;
    public Task FirstOutput => connection.FirstOutput;
    public Task FirstResize => connection.FirstResize;

    public void ApplyAppearance(TerminalAppearance appearance)
    {
        this.appearance = appearance;
        if (appearance.ColorTable.Count != 16)
        {
            throw new ArgumentException("terminal color table must contain exactly 16 colors", nameof(appearance));
        }
        terminal.SetTheme(new TerminalTheme
        {
            DefaultBackground = appearance.Background,
            DefaultForeground = appearance.Foreground,
            DefaultSelectionBackground = appearance.SelectionBackground,
            ColorTable = appearance.ColorTable.ToArray(),
        }, appearance.FontFamily, (short)Math.Clamp(Math.Round(appearance.FontSize), 6, 96));
    }

    public string GetSelectedText() => terminal.GetSelectedText();
    public void FocusTerminal() => terminal.Focus(FocusState.Programmatic);
    public void SendInput(string value) => connection.WriteInput(value);

    public void Dispose()
    {
        if (Interlocked.Exchange(ref disposed, 1) != 0) return;
        terminal.Dispose();
        connection.Dispose();
    }
}
