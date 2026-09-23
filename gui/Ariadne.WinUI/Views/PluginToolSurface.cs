using System.Text;
using System.Threading.Channels;
using Ariadne.Windows.Protocol;
using Microsoft.UI.Input;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.System;
using Windows.UI.Core;
using Windows.UI.Text;

namespace Ariadne.WinUI.Views;

internal sealed class PluginToolSurface : UserControl, IDisposable
{
    private const long MaxInputBytes = 16L << 20;
    private readonly AriadneClient client;
    private readonly ulong paneId;
    private readonly string provider;
    private readonly string type;
    private readonly string instance;
    private readonly string viewId = $"gui-{Guid.NewGuid():N}";
    private readonly CancellationTokenSource lifetime = new();
    private readonly Channel<PluginInput> inputs = Channel.CreateBounded<PluginInput>(new BoundedChannelOptions(64)
    {
        FullMode = BoundedChannelFullMode.Wait,
        SingleReader = true,
        SingleWriter = false,
    });
    private readonly Canvas canvas = new();
    private readonly Microsoft.UI.Dispatching.DispatcherQueueTimer timer;
    private readonly Task inputLoop;
    private FontFamily fontFamily;
    private double fontSize;
    private double cellWidth;
    private double cellHeight;
    private int columns = 1;
    private int rows = 1;
    private ulong generation = 1;
    private ulong runtimeGeneration;
    private string hostViewId = "";
    private bool opened;
    private bool rendering;
    private bool disposed;
    private bool leftPressed;
    private long inputBytes;
    private PluginFrame? frame;

    public PluginToolSurface(AriadneClient client, PaneModel pane, GuiOptions options)
    {
        this.client = client;
        paneId = pane.Id;
        provider = pane.Tool?.Provider ?? throw new ArgumentException("external Tool has no provider", nameof(pane));
        type = pane.Tool.Type;
        instance = pane.Tool.Instance;
        fontFamily = new FontFamily(options.FontFamily);
        ApplyOptions(options);
        Content = canvas;
        IsTabStop = true;
        Background = Brush(options.Background);
        SizeChanged += OnSizeChanged;
        GotFocus += (_, _) => FocusEntered?.Invoke(this, EventArgs.Empty);
        CharacterReceived += OnCharacterReceived;
        KeyDown += OnKeyDown;
        PointerPressed += OnPointerPressed;
        PointerReleased += OnPointerReleased;
        PointerMoved += OnPointerMoved;
        PointerWheelChanged += OnPointerWheelChanged;
        timer = DispatcherQueue.CreateTimer();
        timer.Interval = TimeSpan.FromMilliseconds(100);
        timer.Tick += OnRenderTick;
        timer.Start();
        inputLoop = Task.Run(SendInputsAsync);
        SetMessage("Waiting for plugin renderer…");
    }

    public event EventHandler? FocusEntered;
    public event EventHandler<Exception>? Failed;
    public bool HasFrame => frame is not null;
    public int? InputCount
    {
        get
        {
            var current = frame;
            if (current is null || current.Height < 1) return null;
            var text = string.Concat(current.Cells.Take(current.Width)
                .Where(cell => cell.Width != 0).Select(cell => cell.Text));
            var marker = text.IndexOf("input:", StringComparison.Ordinal);
            if (marker < 0) return null;
            marker += "input:".Length;
            var end = marker;
            while (end < text.Length && char.IsAsciiDigit(text[end])) end++;
            return int.TryParse(text[marker..end], out var value) ? value : null;
        }
    }

    public void SendSmokeInput() => Enqueue(new PluginInput { Data = Encoding.UTF8.GetBytes("gui-smoke") });

    public async Task PasteClipboardAsync(CancellationToken cancellationToken = default)
    {
        var text = (await client.ReadClipboardAsync(paneId, cancellationToken)).Text;
        if (text.Length != 0)
        {
            Enqueue(new PluginInput { Data = Encoding.UTF8.GetBytes(text), Paste = true });
        }
    }

    public void Update(PaneModel pane)
    {
        if (pane.Tool?.Provider != provider || pane.Tool.Type != type || pane.Tool.Instance != instance)
            SetMessage("Tool descriptor changed; waiting for the view to be rebuilt.");
    }

    public void ApplyOptions(GuiOptions options)
    {
        fontFamily = new FontFamily(options.FontFamily);
        fontSize = options.FontSize;
        cellWidth = Math.Max(1, fontSize * 0.62);
        cellHeight = Math.Max(1, Math.Ceiling(fontSize * 1.35));
        Background = Brush(options.Background);
        RecalculateDimensions();
    }

    private void OnSizeChanged(object sender, SizeChangedEventArgs args) => RecalculateDimensions();

    private void RecalculateDimensions()
    {
        var nextColumns = Math.Max(1, (int)Math.Floor(ActualWidth / cellWidth));
        var nextRows = Math.Max(1, (int)Math.Floor(ActualHeight / cellHeight));
        if (nextColumns == columns && nextRows == rows) return;
        columns = nextColumns;
        rows = nextRows;
        generation++;
        runtimeGeneration = 0;
        frame = null;
        canvas.Children.Clear();
        SetMessage("Resizing plugin view…");
    }

    private async void OnRenderTick(Microsoft.UI.Dispatching.DispatcherQueueTimer sender, object args)
    {
        if (disposed || rendering || ActualWidth <= 0 || ActualHeight <= 0) return;
        rendering = true;
        var selectedGeneration = generation;
        var view = CurrentView(selectedGeneration);
        try
        {
            if (!opened)
            {
                await client.PluginAsync(new PluginManageRequest { Action = "view.open", Id = provider, View = view }, lifetime.Token);
                opened = true;
            }
            var result = await client.PluginAsync(new PluginManageRequest { Action = "render", Id = provider, View = view }, lifetime.Token);
            var frame = result.Frame ?? throw new InvalidDataException("plugin render returned no frame");
            if (!disposed && generation == selectedGeneration && frame.Generation == selectedGeneration &&
                frame.Width == columns && frame.Height == rows && !string.IsNullOrEmpty(frame.ViewId) &&
                (hostViewId.Length == 0 || frame.ViewId == hostViewId))
            {
                hostViewId = frame.ViewId;
                runtimeGeneration = frame.RuntimeGeneration;
                DrawFrame(frame);
            }
        }
        catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
        {
        }
        catch (Exception error)
        {
            SetMessage(error.Message);
            Failed?.Invoke(this, error);
        }
        finally
        {
            rendering = false;
        }
    }

    private PluginView CurrentView(ulong? selectedGeneration = null) => new()
    {
        Id = viewId,
        Generation = selectedGeneration ?? generation,
        RuntimeGeneration = runtimeGeneration,
        PaneId = paneId,
        Type = type,
        Instance = instance,
        Width = columns,
        Height = rows,
    };

    private void DrawFrame(PluginFrame frame)
    {
        if (frame.Cells.Count != frame.Width * frame.Height)
        {
            SetMessage("plugin returned an invalid cell count");
            return;
        }
        this.frame = frame;
        canvas.Children.Clear();
        for (var y = 0; y < frame.Height; y++)
        {
            for (var start = 0; start < frame.Width;)
            {
                var first = frame.Cells[y * frame.Width + start];
                var end = start + 1;
                while (end < frame.Width && SameStyle(first.Style, frame.Cells[y * frame.Width + end].Style)) end++;
                var text = new StringBuilder();
                for (var x = start; x < end; x++)
                {
                    var cell = frame.Cells[y * frame.Width + x];
                    if (cell.Width != 0) text.Append(string.IsNullOrEmpty(cell.Text) ? " " : cell.Text);
                }
                var run = new TextBlock
                {
                    Text = text.ToString(),
                    FontFamily = fontFamily,
                    FontSize = fontSize,
                    FontWeight = first.Style.Bold ? Microsoft.UI.Text.FontWeights.Bold : Microsoft.UI.Text.FontWeights.Normal,
                    FontStyle = first.Style.Italic ? FontStyle.Italic : FontStyle.Normal,
                    Foreground = Brush(first.Style.Foreground, first.Style.Faint ? 0.55 : 1),
                    TextTrimming = TextTrimming.Clip,
                };
                if (first.Style.Underline || first.Style.UnderlineStyle != 0)
                    run.TextDecorations = TextDecorations.Underline;
                if (first.Style.Strikethrough)
                    run.TextDecorations |= TextDecorations.Strikethrough;
                var background = new Border
                {
                    Background = Brush(first.Style.Background),
                    Width = (end - start) * cellWidth,
                    Height = cellHeight,
                    Child = run,
                };
                Canvas.SetLeft(background, start * cellWidth);
                Canvas.SetTop(background, y * cellHeight);
                canvas.Children.Add(background);
                start = end;
            }
        }
        if (frame.Cursor.Visible && frame.Cursor.X >= 0 && frame.Cursor.Y >= 0 &&
            frame.Cursor.X < columns && frame.Cursor.Y < rows)
        {
            var cursor = new Border
            {
                Width = cellWidth,
                Height = cellHeight,
                BorderThickness = new Thickness(1),
                BorderBrush = new SolidColorBrush(Microsoft.UI.Colors.White),
            };
            Canvas.SetLeft(cursor, frame.Cursor.X * cellWidth);
            Canvas.SetTop(cursor, frame.Cursor.Y * cellHeight);
            canvas.Children.Add(cursor);
        }
    }

    private void SetMessage(string message)
    {
        canvas.Children.Clear();
        canvas.Children.Add(new TextBlock
        {
            Text = message,
            FontFamily = fontFamily,
            FontSize = fontSize,
            Foreground = new SolidColorBrush(Microsoft.UI.Colors.LightGray),
            Margin = new Thickness(8),
            TextWrapping = TextWrapping.Wrap,
        });
    }

    private static bool SameStyle(PluginStyle left, PluginStyle right) =>
        SameColor(left.Foreground, right.Foreground) && SameColor(left.Background, right.Background) &&
        left.Bold == right.Bold && left.Italic == right.Italic && left.Underline == right.Underline &&
        left.Strikethrough == right.Strikethrough && left.Faint == right.Faint &&
        left.UnderlineStyle == right.UnderlineStyle;

    private static bool SameColor(PluginColor left, PluginColor right) =>
        left.R == right.R && left.G == right.G && left.B == right.B;

    private static SolidColorBrush Brush(string value)
    {
        var rgb = Convert.ToUInt32(value.TrimStart('#'), 16);
        return new SolidColorBrush(global::Windows.UI.Color.FromArgb(255,
            (byte)(rgb >> 16), (byte)(rgb >> 8), (byte)rgb));
    }

    private static SolidColorBrush Brush(PluginColor color, double opacity = 1) =>
        new(global::Windows.UI.Color.FromArgb((byte)Math.Round(255 * opacity), color.R, color.G, color.B));

    private void OnCharacterReceived(UIElement sender, CharacterReceivedRoutedEventArgs args)
    {
        if (args.Character != 0)
        {
            Enqueue(new PluginInput { Data = Encoding.UTF8.GetBytes(char.ConvertFromUtf32((int)args.Character)) });
            args.Handled = true;
        }
    }

    private void OnKeyDown(object sender, KeyRoutedEventArgs args)
    {
        var control = IsKeyDown(VirtualKey.Control);
        var alt = IsKeyDown(VirtualKey.Menu);
        string? sequence = args.Key switch
        {
            VirtualKey.Enter => "\r", VirtualKey.Back => "\x7f", VirtualKey.Tab => "\t", VirtualKey.Escape => "\x1b",
            VirtualKey.Up => "\x1b[A", VirtualKey.Down => "\x1b[B", VirtualKey.Right => "\x1b[C", VirtualKey.Left => "\x1b[D",
            VirtualKey.Home => "\x1b[H", VirtualKey.End => "\x1b[F", VirtualKey.Insert => "\x1b[2~", VirtualKey.Delete => "\x1b[3~",
            VirtualKey.PageUp => "\x1b[5~", VirtualKey.PageDown => "\x1b[6~",
            >= VirtualKey.A and <= VirtualKey.Z when control && !alt =>
                ((char)((int)args.Key - (int)VirtualKey.A + 1)).ToString(),
            _ => null,
        };
        if (sequence is null) return;
        Enqueue(new PluginInput { Data = Encoding.UTF8.GetBytes(sequence) });
        args.Handled = true;
    }

    private static bool IsKeyDown(VirtualKey key) =>
        InputKeyboardSource.GetKeyStateForCurrentThread(key).HasFlag(CoreVirtualKeyStates.Down);

    private void OnPointerPressed(object sender, PointerRoutedEventArgs args)
    {
        Focus(FocusState.Pointer);
        var point = args.GetCurrentPoint(this);
        var kind = point.Properties.PointerUpdateKind;
        var button = kind switch
        {
            Microsoft.UI.Input.PointerUpdateKind.MiddleButtonPressed => 1,
            Microsoft.UI.Input.PointerUpdateKind.RightButtonPressed => 2,
            _ => 0,
        };
        leftPressed = kind == Microsoft.UI.Input.PointerUpdateKind.LeftButtonPressed;
        if (leftPressed) CapturePointer(args.Pointer);
        Enqueue(new PluginInput { Mouse = Mouse(point.Position, button, 0) });
        args.Handled = true;
    }

    private void OnPointerReleased(object sender, PointerRoutedEventArgs args)
    {
        var point = args.GetCurrentPoint(this);
        var button = point.Properties.PointerUpdateKind switch
        {
            Microsoft.UI.Input.PointerUpdateKind.MiddleButtonReleased => 1,
            Microsoft.UI.Input.PointerUpdateKind.RightButtonReleased => 2,
            _ => 0,
        };
        Enqueue(new PluginInput { Mouse = Mouse(point.Position, button, 1) });
        if (leftPressed)
        {
            leftPressed = false;
            ReleasePointerCapture(args.Pointer);
        }
        args.Handled = true;
    }

    private void OnPointerMoved(object sender, PointerRoutedEventArgs args)
    {
        if (!leftPressed) return;
        Enqueue(new PluginInput { Mouse = Mouse(args.GetCurrentPoint(this).Position, 0, 2) });
        args.Handled = true;
    }

    private void OnPointerWheelChanged(object sender, PointerRoutedEventArgs args)
    {
        var point = args.GetCurrentPoint(this);
        Enqueue(new PluginInput { Mouse = Mouse(point.Position, 0, 2, Math.Sign(point.Properties.MouseWheelDelta)) });
        args.Handled = true;
    }

    private PluginMouse Mouse(global::Windows.Foundation.Point point, int button, int action, int wheel = 0) => new()
    {
        X = Math.Clamp((int)(point.X / cellWidth), 0, columns - 1),
        Y = Math.Clamp((int)(point.Y / cellHeight), 0, rows - 1),
        Button = button,
        Action = action,
        Wheel = wheel,
        Shift = IsKeyDown(VirtualKey.Shift),
        Alt = IsKeyDown(VirtualKey.Menu),
        Ctrl = IsKeyDown(VirtualKey.Control),
    };

    private void Enqueue(PluginInput input)
    {
        if (disposed || runtimeGeneration == 0) return;
        input.View = CurrentView();
        var size = (input.Data?.LongLength ?? 0) + 512;
        if (Interlocked.Add(ref inputBytes, size) > MaxInputBytes || !inputs.Writer.TryWrite(input))
        {
            Interlocked.Add(ref inputBytes, -size);
            var error = new InvalidOperationException("plugin Tool input queue overflow");
            SetMessage(error.Message);
            Failed?.Invoke(this, error);
            _ = FaultAsync();
        }
    }

    private async Task SendInputsAsync()
    {
        try
        {
            await foreach (var input in inputs.Reader.ReadAllAsync(lifetime.Token))
            {
                Interlocked.Add(ref inputBytes, -(input.Data?.LongLength ?? 0) - 512);
                await client.PluginAsync(new PluginManageRequest { Action = "input", Id = provider, Input = input }, lifetime.Token);
            }
        }
        catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
        {
        }
        catch (Exception error)
        {
            DispatcherQueue.TryEnqueue(() =>
            {
                SetMessage(error.Message);
                Failed?.Invoke(this, error);
            });
        }
    }

    private async Task FaultAsync()
    {
        lifetime.Cancel();
        try
        {
            using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(1));
            await client.PluginAsync(new PluginManageRequest { Action = "fault", Id = provider }, timeout.Token);
        }
        catch
        {
        }
    }

    public void Dispose()
    {
        if (disposed) return;
        disposed = true;
        timer.Stop();
        inputs.Writer.TryComplete();
        lifetime.Cancel();
        var view = CurrentView();
        _ = Task.Run(async () =>
        {
            try
            {
                await inputLoop.ConfigureAwait(false);
                if (opened)
                {
                    using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(1));
                    await client.PluginAsync(new PluginManageRequest { Action = "view.close", Id = provider, View = view }, timeout.Token)
                        .ConfigureAwait(false);
                }
            }
            catch
            {
            }
            finally
            {
                lifetime.Dispose();
            }
        });
    }
}
