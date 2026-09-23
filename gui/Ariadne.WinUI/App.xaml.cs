using Microsoft.UI.Xaml;

namespace Ariadne.WinUI;

public partial class App : Application
{
    private Window? window;

    public App() => InitializeComponent();

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        var arguments = Environment.GetCommandLineArgs();
        var smokeTest = arguments.Contains("--smoke-test", StringComparer.Ordinal);
        var smokePlugin = arguments.Contains("--smoke-test-plugin", StringComparer.Ordinal);
        window = new MainWindow(smokeTest || smokePlugin, Option(arguments, "--socket"),
            Option(arguments, "--smoke-input"), smokePlugin);
        window.Activate();
    }

    private static string? Option(IReadOnlyList<string> arguments, string name)
    {
        for (var index = 0; index + 1 < arguments.Count; index++)
        {
            if (string.Equals(arguments[index], name, StringComparison.Ordinal)) return arguments[index + 1];
        }
        return null;
    }
}
