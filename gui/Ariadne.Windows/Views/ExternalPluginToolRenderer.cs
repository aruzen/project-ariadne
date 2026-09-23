using System.Globalization;
using System.Text;
using System.Threading.Channels;
using System.Windows;
using System.Windows.Input;
using System.Windows.Media;
using System.Windows.Threading;
using Ariadne.Windows.Protocol;

namespace Ariadne.Windows.Views;

internal sealed class ExternalPluginToolRenderer : IToolPaneContentRenderer
{
    private const long MaxInputBytes = 16L << 20;
    private readonly AriadneClient client;
    private readonly string provider;
    private readonly ulong paneId;
    private readonly string viewId = $"gui-{Guid.NewGuid():N}";
    private readonly CancellationTokenSource lifetime = new();
    private readonly Channel<PluginInput> inputs = Channel.CreateBounded<PluginInput>(new BoundedChannelOptions(64)
    {
        FullMode = BoundedChannelFullMode.Wait,
        SingleReader = true,
        SingleWriter = false,
    });
    private readonly PluginSurface surface;
    private readonly DispatcherTimer timer;
    private readonly Task inputLoop;
    private ulong generation = 1;
    private ulong runtimeGeneration;
    private string hostViewId = "";
    private bool opened;
    private bool rendering;
    private bool disposed;
    private long inputBytes;

    public ExternalPluginToolRenderer(AriadneClient client, PaneModel pane, GuiOptions options)
    {
        this.client = client;
        paneId = pane.Id;
        provider = pane.Tool?.Provider ?? throw new ArgumentException("external Tool pane has no provider", nameof(pane));
        surface = new PluginSurface(options) { Focusable = true };
        surface.DimensionsChanged += OnDimensionsChanged;
        surface.Input += OnInput;
        surface.PasteRequested += OnPasteRequested;
        timer = new DispatcherTimer(DispatcherPriority.Background, surface.Dispatcher)
        {
            Interval = TimeSpan.FromMilliseconds(100),
        };
        timer.Tick += OnRenderTick;
        timer.Start();
        inputLoop = Task.Run(SendInputsAsync);
    }

    public FrameworkElement Content => surface;
    public bool IsReady => surface.HasFrame;
    public int? InputCount => surface.InputCount;

    public void SendSmokeInput() => Enqueue(new PluginInput { Data = Encoding.UTF8.GetBytes("gui-smoke") });

    public void Update(PaneModel pane, ToolInstanceModel? tool)
    {
        // State is captured by the daemon immediately before each render.
        if (pane.Tool?.Provider != provider)
        {
            surface.SetError("Tool provider changed; waiting for the view to be rebuilt.");
        }
    }

    public void ApplyOptions(GuiOptions options) => surface.ApplyOptions(options);

    private void OnDimensionsChanged(object? sender, EventArgs e)
    {
        generation++;
        runtimeGeneration = 0;
        surface.SetFrame(null);
    }

    private async void OnRenderTick(object? sender, EventArgs e)
    {
        if (disposed || rendering || surface.Columns < 1 || surface.Rows < 1)
        {
            return;
        }
        rendering = true;
        var currentGeneration = generation;
        var view = CurrentView(currentGeneration);
        try
        {
            if (!opened)
            {
                await client.PluginAsync(new PluginManageRequest { Action = "view.open", Id = provider, View = view }, lifetime.Token);
                opened = true;
            }
            var result = await client.PluginAsync(new PluginManageRequest { Action = "render", Id = provider, View = view }, lifetime.Token);
            var frame = result.Frame ?? throw new InvalidDataException("plugin render returned no frame");
            if (!disposed && currentGeneration == generation && frame.Generation == currentGeneration &&
                !string.IsNullOrEmpty(frame.ViewId) && (hostViewId.Length == 0 || frame.ViewId == hostViewId) &&
                frame.Width == surface.Columns && frame.Height == surface.Rows)
            {
                hostViewId = frame.ViewId;
                runtimeGeneration = frame.RuntimeGeneration;
                surface.SetFrame(frame);
            }
        }
        catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
        {
        }
        catch (Exception error)
        {
            surface.SetError(error.Message);
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
        Width = surface.Columns,
        Height = surface.Rows,
    };

    private void OnInput(object? sender, PluginInputEventArgs e) => Enqueue(e.Input);

    private async void OnPasteRequested(object? sender, EventArgs e)
    {
        try
        {
            var text = (await client.ReadClipboardAsync(paneId, lifetime.Token)).Text;
            if (!string.IsNullOrEmpty(text))
            {
                Enqueue(new PluginInput { Data = Encoding.UTF8.GetBytes(text), Paste = true });
            }
        }
        catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
        {
        }
        catch (Exception error)
        {
            surface.SetError(error.Message);
        }
    }

    private void Enqueue(PluginInput input)
    {
        if (disposed || runtimeGeneration == 0)
        {
            return;
        }
        input.View = CurrentView();
        var size = (input.Data?.LongLength ?? 0) + 512;
        if (Interlocked.Add(ref inputBytes, size) > MaxInputBytes || !inputs.Writer.TryWrite(input))
        {
            Interlocked.Add(ref inputBytes, -size);
            surface.SetError("plugin Tool input queue overflow");
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
            await surface.Dispatcher.InvokeAsync(() => surface.SetError(error.Message));
        }
    }

    private async Task FaultAsync()
    {
        if (disposed)
        {
            return;
        }
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
        if (disposed)
        {
            return;
        }
        disposed = true;
        timer.Stop();
        timer.Tick -= OnRenderTick;
        surface.DimensionsChanged -= OnDimensionsChanged;
        surface.Input -= OnInput;
        surface.PasteRequested -= OnPasteRequested;
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
                    await client.PluginAsync(new PluginManageRequest { Action = "view.close", Id = provider, View = view }, timeout.Token).ConfigureAwait(false);
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

internal sealed class PluginInputEventArgs(PluginInput input) : EventArgs
{
    public PluginInput Input { get; } = input;
}

internal sealed class PluginSurface : FrameworkElement
{
    private Typeface normalTypeface = null!;
    private Typeface boldTypeface = null!;
    private Typeface italicTypeface = null!;
    private double fontSize;
    private Brush fallbackForeground = Brushes.White;
    private Brush fallbackBackground = Brushes.Black;
    private readonly Dictionary<uint, Brush> brushes = new();
    private PluginFrame? frame;
    private string error = "Waiting for plugin renderer…";
    private double cellWidth = 8;
    private double cellHeight = 16;
    private bool leftPressed;
    private int columns;
    private int rows;

    public PluginSurface(GuiOptions options)
    {
        ApplyOptions(options);
        Cursor = Cursors.IBeam;
        SnapsToDevicePixels = true;
        UseLayoutRounding = true;
        Loaded += (_, _) => RecalculateMetrics();
        SizeChanged += (_, _) => RecalculateDimensions();
        TextInput += OnTextInput;
        PreviewKeyDown += OnPreviewKeyDown;
        MouseDown += OnMouseDown;
        MouseUp += OnMouseUp;
        MouseMove += OnMouseMove;
        MouseWheel += OnMouseWheel;
    }

    public Brush Background { get; private set; } = Brushes.Black;
    public int Columns => columns;
    public int Rows => rows;
    public bool HasFrame => frame is not null;
    public int? InputCount
    {
        get
        {
            var current = frame;
            if (current is null || current.Height < 1)
            {
                return null;
            }
            var text = string.Concat(current.Cells.Take(current.Width).Where(cell => cell.Width != 0).Select(cell => cell.Text));
            var marker = text.IndexOf("input:", StringComparison.Ordinal);
            if (marker < 0)
            {
                return null;
            }
            marker += "input:".Length;
            var end = marker;
            while (end < text.Length && char.IsAsciiDigit(text[end])) end++;
            return int.TryParse(text[marker..end], out var value) ? value : null;
        }
    }
    public event EventHandler? DimensionsChanged;
    public event EventHandler<PluginInputEventArgs>? Input;
    public event EventHandler? PasteRequested;

    public void ApplyOptions(GuiOptions options)
    {
        brushes.Clear();
        fontSize = options.FontSize;
        var family = new FontFamily(options.FontFamily);
        normalTypeface = new Typeface(family, FontStyles.Normal, FontWeights.Normal, FontStretches.Normal);
        boldTypeface = new Typeface(family, FontStyles.Normal, FontWeights.Bold, FontStretches.Normal);
        italicTypeface = new Typeface(family, FontStyles.Italic, FontWeights.Normal, FontStretches.Normal);
        fallbackForeground = Brush(options.Foreground);
        fallbackBackground = Brush(options.Background);
        Background = fallbackBackground;
        RecalculateMetrics();
        InvalidateVisual();
    }

    public void SetFrame(PluginFrame? value)
    {
        frame = value;
        if (value is not null)
        {
            error = "";
        }
        InvalidateVisual();
    }

    public void SetError(string value)
    {
        error = value;
        InvalidateVisual();
    }

    protected override void OnRender(DrawingContext drawingContext)
    {
        base.OnRender(drawingContext);
        drawingContext.DrawRectangle(Background, null, new Rect(RenderSize));
        var current = frame;
        if (current is null || current.Cells.Count != current.Width * current.Height)
        {
            DrawText(drawingContext, error, 0, 0, fallbackForeground, normalTypeface);
            return;
        }
        var maxRows = Math.Min(rows, current.Height);
        var maxColumns = Math.Min(columns, current.Width);
        for (var y = 0; y < maxRows; y++)
        {
            for (var start = 0; start < maxColumns;)
            {
                var background = current.Cells[y * current.Width + start].Style.Background;
                var end = start + 1;
                while (end < maxColumns && SameColor(background, current.Cells[y * current.Width + end].Style.Background)) end++;
                drawingContext.DrawRectangle(Brush(background), null,
                    new Rect(start * cellWidth, y * cellHeight, (end - start) * cellWidth, cellHeight));
                start = end;
            }
            for (var x = 0; x < maxColumns; x++)
            {
                var cell = current.Cells[y * current.Width + x];
                if (cell.Width == 0)
                {
                    continue;
                }
                var typeface = cell.Style.Bold ? boldTypeface : cell.Style.Italic ? italicTypeface : normalTypeface;
                var foreground = Brush(cell.Style.Foreground, cell.Style.Faint ? 0.55 : 1);
                if (!string.IsNullOrWhiteSpace(cell.Text))
                {
                    DrawText(drawingContext, cell.Text, x, y, foreground, typeface);
                }
                if (cell.Style.Underline || cell.Style.UnderlineStyle != 0)
                {
                    var underline = cell.Style.HasUnderlineColor ? Brush(cell.Style.UnderlineColor) : foreground;
                    drawingContext.DrawLine(new Pen(underline, 1), new Point(x * cellWidth, (y + 1) * cellHeight - 1), new Point((x + Math.Max(1, (int)cell.Width)) * cellWidth, (y + 1) * cellHeight - 1));
                }
                if (cell.Style.Strikethrough)
                {
                    drawingContext.DrawLine(new Pen(foreground, 1), new Point(x * cellWidth, (y + .55) * cellHeight), new Point((x + Math.Max(1, (int)cell.Width)) * cellWidth, (y + .55) * cellHeight));
                }
            }
        }
        if (IsKeyboardFocusWithin && current.Cursor.Visible && current.Cursor.X >= 0 && current.Cursor.Y >= 0 &&
            current.Cursor.X < columns && current.Cursor.Y < rows)
        {
            drawingContext.DrawRectangle(null, new Pen(fallbackForeground, 1),
                new Rect(current.Cursor.X * cellWidth, current.Cursor.Y * cellHeight, cellWidth, cellHeight));
        }
    }

    private void DrawText(DrawingContext context, string text, int x, int y, Brush brush, Typeface typeface)
    {
        if (string.IsNullOrEmpty(text))
        {
            return;
        }
        var formatted = new FormattedText(text, CultureInfo.CurrentUICulture, FlowDirection.LeftToRight, typeface,
            fontSize, brush, VisualTreeHelper.GetDpi(this).PixelsPerDip);
        context.DrawText(formatted, new Point(x * cellWidth, y * cellHeight));
    }

    private void RecalculateMetrics()
    {
        var formatted = new FormattedText("M", CultureInfo.InvariantCulture, FlowDirection.LeftToRight, normalTypeface,
            fontSize, fallbackForeground, VisualTreeHelper.GetDpi(this).PixelsPerDip);
        cellWidth = Math.Max(1, formatted.WidthIncludingTrailingWhitespace);
        cellHeight = Math.Max(1, Math.Ceiling(formatted.Height));
        RecalculateDimensions();
    }

    private void RecalculateDimensions()
    {
        var nextColumns = Math.Max(1, (int)Math.Floor(ActualWidth / cellWidth));
        var nextRows = Math.Max(1, (int)Math.Floor(ActualHeight / cellHeight));
        if (nextColumns == columns && nextRows == rows)
        {
            return;
        }
        columns = nextColumns;
        rows = nextRows;
        DimensionsChanged?.Invoke(this, EventArgs.Empty);
    }

    private Brush Brush(string hex) => Brush(ParseColor(hex));
    private Brush Brush(PluginColor color, double opacity = 1) => Brush(Color.FromArgb((byte)(255 * opacity), color.R, color.G, color.B));
    private Brush Brush(Color color)
    {
        var key = ((uint)color.A << 24) | ((uint)color.R << 16) | ((uint)color.G << 8) | color.B;
        if (!brushes.TryGetValue(key, out var value))
        {
            value = new SolidColorBrush(color);
            value.Freeze();
            brushes[key] = value;
        }
        return value;
    }

    private static Color ParseColor(string value) => (Color)ColorConverter.ConvertFromString(value);
    private static bool SameColor(PluginColor left, PluginColor right) =>
        left.R == right.R && left.G == right.G && left.B == right.B;

    private void OnTextInput(object sender, TextCompositionEventArgs e)
    {
        if (!string.IsNullOrEmpty(e.Text))
        {
            Input?.Invoke(this, new PluginInputEventArgs(new PluginInput { Data = Encoding.UTF8.GetBytes(e.Text) }));
            e.Handled = true;
        }
    }

    private void OnPreviewKeyDown(object sender, KeyEventArgs e)
    {
        var modifiers = Keyboard.Modifiers;
        var paste = e.Key == Key.V && modifiers.HasFlag(ModifierKeys.Control) && modifiers.HasFlag(ModifierKeys.Shift) ||
                    e.Key == Key.Insert && modifiers.HasFlag(ModifierKeys.Shift);
        if (paste)
        {
            PasteRequested?.Invoke(this, EventArgs.Empty);
            e.Handled = true;
            return;
        }
        var data = KeyBytes(e.Key, modifiers);
        if (data is not null)
        {
            Input?.Invoke(this, new PluginInputEventArgs(new PluginInput { Data = data }));
            e.Handled = true;
        }
    }

    private static byte[]? KeyBytes(Key key, ModifierKeys modifiers)
    {
        var sequence = key switch
        {
            Key.Enter => "\r", Key.Back => "\x7f", Key.Tab => "\t", Key.Escape => "\x1b",
            Key.Up => "\x1b[A", Key.Down => "\x1b[B", Key.Right => "\x1b[C", Key.Left => "\x1b[D",
            Key.Home => "\x1b[H", Key.End => "\x1b[F", Key.Insert => "\x1b[2~", Key.Delete => "\x1b[3~",
            Key.PageUp => "\x1b[5~", Key.PageDown => "\x1b[6~",
            _ => null,
        };
        if (sequence is not null)
        {
            return Encoding.UTF8.GetBytes(sequence);
        }
        // Ctrl+Alt is commonly AltGr. Do not turn printable AltGr input into a
        // control byte; TextInput will deliver the layout-specific character.
        if (modifiers.HasFlag(ModifierKeys.Control) && !modifiers.HasFlag(ModifierKeys.Alt) &&
            key >= Key.A && key <= Key.Z)
        {
            return [(byte)((int)key - (int)Key.A + 1)];
        }
        return null;
    }

    private (int X, int Y) Cell(Point point) =>
        (Math.Clamp((int)(point.X / cellWidth), 0, Math.Max(0, columns - 1)),
         Math.Clamp((int)(point.Y / cellHeight), 0, Math.Max(0, rows - 1)));

    private PluginMouse Mouse(Point point, int button, int action, int wheel = 0)
    {
        var (x, y) = Cell(point);
        var modifiers = Keyboard.Modifiers;
        return new PluginMouse { X = x, Y = y, Button = button, Action = action, Wheel = wheel,
            Shift = modifiers.HasFlag(ModifierKeys.Shift), Alt = modifiers.HasFlag(ModifierKeys.Alt), Ctrl = modifiers.HasFlag(ModifierKeys.Control) };
    }

    private void OnMouseDown(object sender, MouseButtonEventArgs e)
    {
        Focus();
        var button = e.ChangedButton switch { MouseButton.Middle => 1, MouseButton.Right => 2, _ => 0 };
        leftPressed = e.ChangedButton == MouseButton.Left;
        if (leftPressed) CaptureMouse();
        Input?.Invoke(this, new PluginInputEventArgs(new PluginInput { Mouse = Mouse(e.GetPosition(this), button, 0) }));
        e.Handled = true;
    }

    private void OnMouseUp(object sender, MouseButtonEventArgs e)
    {
        var button = e.ChangedButton switch { MouseButton.Middle => 1, MouseButton.Right => 2, _ => 0 };
        Input?.Invoke(this, new PluginInputEventArgs(new PluginInput { Mouse = Mouse(e.GetPosition(this), button, 1) }));
        if (e.ChangedButton == MouseButton.Left)
        {
            leftPressed = false;
            ReleaseMouseCapture();
        }
        e.Handled = true;
    }

    private void OnMouseMove(object sender, MouseEventArgs e)
    {
        if (leftPressed)
        {
            Input?.Invoke(this, new PluginInputEventArgs(new PluginInput { Mouse = Mouse(e.GetPosition(this), 0, 2) }));
            e.Handled = true;
        }
    }

    private void OnMouseWheel(object sender, MouseWheelEventArgs e)
    {
        Input?.Invoke(this, new PluginInputEventArgs(new PluginInput { Mouse = Mouse(e.GetPosition(this), 0, 2, Math.Sign(e.Delta)) }));
        e.Handled = true;
    }
}
