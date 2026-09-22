using System.Text.Json;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using Ariadne.Windows.Protocol;

namespace Ariadne.Windows.Views;

internal interface IToolPaneContentRenderer : IDisposable
{
    FrameworkElement Content { get; }
    void Update(PaneModel pane, ToolInstanceModel? tool);
}

internal sealed class ToolPaneRendererRegistry
{
    private readonly Dictionary<(string Provider, string Type), Func<IToolPaneContentRenderer>> factories = new();

    public void Register(string provider, string type, Func<IToolPaneContentRenderer> factory) =>
        factories[(provider, type)] = factory;

    public IToolPaneContentRenderer Create(ToolDescriptor? descriptor)
    {
        if (descriptor is not null && factories.TryGetValue((descriptor.Provider, descriptor.Type), out var factory))
        {
            return factory();
        }
        return new GenericToolPaneRenderer();
    }
}

internal sealed class GenericToolPaneRenderer : IToolPaneContentRenderer
{
    private readonly TextBlock identity = new()
    {
        Foreground = new SolidColorBrush(Color.FromRgb(170, 178, 188)),
        Margin = new Thickness(0, 0, 0, 10),
    };
    private readonly TextBox state = new()
    {
        IsReadOnly = true,
        AcceptsReturn = true,
        TextWrapping = TextWrapping.Wrap,
        VerticalScrollBarVisibility = ScrollBarVisibility.Auto,
        HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
        FontFamily = new FontFamily("Cascadia Mono"),
        Background = new SolidColorBrush(Color.FromRgb(12, 15, 19)),
        Foreground = new SolidColorBrush(Color.FromRgb(210, 215, 222)),
        BorderThickness = new Thickness(0),
        Padding = new Thickness(10),
    };

    public GenericToolPaneRenderer()
    {
        var grid = new Grid();
        grid.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        grid.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        Grid.SetRow(identity, 0);
        Grid.SetRow(state, 1);
        grid.Children.Add(identity);
        grid.Children.Add(state);
        Content = grid;
    }

    public FrameworkElement Content { get; }

    public void Update(PaneModel pane, ToolInstanceModel? tool)
    {
        identity.Text = pane.Tool is null
            ? "No Tool descriptor"
            : $"{pane.Tool.Provider} / {pane.Tool.Type} / {pane.Tool.Instance}" +
              (tool is null ? " — state unavailable" : $" — state v{tool.StateVersion}, generation {tool.Generation}");
        state.Text = tool?.State.ValueKind is null or JsonValueKind.Undefined
            ? "This Tool has no persistent state. A specialized renderer can be registered for its provider/type."
            : JsonSerializer.Serialize(tool.State, new JsonSerializerOptions { WriteIndented = true });
    }

    public void Dispose()
    {
    }
}

internal sealed class TerminalPlaceholderRenderer : IToolPaneContentRenderer
{
    private readonly TextBlock message = new()
    {
        Foreground = new SolidColorBrush(Color.FromRgb(170, 178, 188)),
        VerticalAlignment = VerticalAlignment.Center,
        HorizontalAlignment = HorizontalAlignment.Center,
    };

    public FrameworkElement Content => message;

    public void Update(PaneModel pane, ToolInstanceModel? tool) =>
        message.Text = $"Terminal is {pane.Terminal?.State ?? "unavailable"}. Use Restart or Run command.";

    public void Dispose()
    {
    }
}

internal sealed class ToolPaneView : Border, IDisposable
{
    private readonly ToolPaneRendererRegistry registry;
    private readonly TextBlock title;
    private readonly Grid contentHost;
    private IToolPaneContentRenderer? renderer;
    private string rendererKey = "";

    public ToolPaneView(ToolPaneRendererRegistry registry, PaneModel pane, ToolInstanceModel? tool)
    {
        this.registry = registry;
        PaneId = pane.Id;
        Padding = new Thickness(1);
        Background = new SolidColorBrush(Color.FromRgb(19, 23, 29));
        BorderThickness = new Thickness(1);
        BorderBrush = Brushes.Transparent;
        Cursor = System.Windows.Input.Cursors.Hand;
        title = new TextBlock
        {
            FontSize = 16,
            FontWeight = FontWeights.SemiBold,
            Foreground = Brushes.White,
            Background = new SolidColorBrush(Color.FromRgb(28, 33, 40)),
            Padding = new Thickness(10, 5, 10, 5),
        };
        contentHost = new Grid { Margin = new Thickness(12) };
        var layout = new DockPanel();
        DockPanel.SetDock(title, Dock.Top);
        layout.Children.Add(title);
        layout.Children.Add(contentHost);
        Child = layout;
        MouseLeftButtonDown += (_, _) => FocusRequested?.Invoke(this, EventArgs.Empty);
        Update(pane, tool, false);
    }

    public ulong PaneId { get; }
    public event EventHandler? FocusRequested;
    public event EventHandler<PaneActionEventArgs>? ActionRequested;

    public void Update(PaneModel pane, ToolInstanceModel? tool, bool focused)
    {
        title.Text = string.IsNullOrWhiteSpace(pane.Title) ? $"{pane.Kind} pane {pane.Id}" : pane.Title;
        var key = pane.Kind == "terminal"
            ? "terminal-placeholder"
            : $"{pane.Tool?.Provider}\0{pane.Tool?.Type}";
        if (key != rendererKey)
        {
            renderer?.Dispose();
            renderer = pane.Kind == "terminal" ? new TerminalPlaceholderRenderer() : registry.Create(pane.Tool);
            rendererKey = key;
            contentHost.Children.Clear();
            contentHost.Children.Add(renderer.Content);
        }
        renderer!.Update(pane, tool);
        BorderBrush = focused ? new SolidColorBrush(Color.FromRgb(81, 145, 255)) : Brushes.Transparent;
        ContextMenu = PaneContextMenu.Create(pane, (sender, args) => ActionRequested?.Invoke(this, args));
    }

    public void Dispose() => renderer?.Dispose();
}
