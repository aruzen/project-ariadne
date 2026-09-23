using System.Globalization;
using Ariadne.Windows.Configuration;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Ariadne.WinUI.Views;

internal sealed record SettingsResult(Dictionary<string, string> Bindings, double FontSize);

internal static class SettingsDialog
{
    public static async Task<SettingsResult?> ShowAsync(
        XamlRoot xamlRoot,
        IReadOnlyDictionary<string, string> current,
        IReadOnlyDictionary<string, string> defaults,
        double currentFontSize,
        string configPath)
    {
        var resolvedPath = string.IsNullOrWhiteSpace(configPath) ? ConfigurationStore.ResolvePath() : configPath;
        var fontSize = new NumberBox
        {
            Header = "Terminal font size",
            Minimum = 6,
            Maximum = 96,
            SmallChange = 1,
            SpinButtonPlacementMode = NumberBoxSpinButtonPlacementMode.Compact,
            Value = currentFontSize,
        };
        var bindings = new TextBox
        {
            Header = "Keyboard shortcuts (one ‘keys = command’ entry per line)",
            AcceptsReturn = true,
            TextWrapping = TextWrapping.NoWrap,
            FontFamily = new Microsoft.UI.Xaml.Media.FontFamily("Cascadia Mono"),
            MinHeight = 300,
            Text = string.Join(Environment.NewLine,
                current.OrderBy(value => value.Key, StringComparer.Ordinal)
                    .Select(value => $"{value.Key} = {value.Value}")),
        };
        var error = new TextBlock
        {
            Foreground = (Microsoft.UI.Xaml.Media.Brush)Application.Current.Resources["SystemFillColorCriticalBrush"],
            TextWrapping = TextWrapping.Wrap,
        };
        var path = new TextBlock { Text = resolvedPath, Opacity = 0.62, TextWrapping = TextWrapping.Wrap };
        var panel = new StackPanel { Spacing = 12, MinWidth = 620 };
        panel.Children.Add(fontSize);
        panel.Children.Add(bindings);
        panel.Children.Add(path);
        panel.Children.Add(error);

        Dictionary<string, string>? parsed = null;
        var dialog = new ContentDialog
        {
            XamlRoot = xamlRoot,
            Title = "Ariadne settings",
            Content = panel,
            PrimaryButtonText = "Save",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
        };
        dialog.PrimaryButtonClick += (_, args) =>
        {
            try
            {
                if (!double.IsFinite(fontSize.Value) || fontSize.Value is < 6 or > 96)
                    throw new InvalidDataException("Font size must be between 6 and 96.");
                parsed = ParseBindings(bindings.Text);
                var overrides = parsed
                    .Where(value => !defaults.TryGetValue(value.Key, out var command) || command != value.Value)
                    .ToDictionary(value => value.Key, value => value.Value, StringComparer.Ordinal);
                ConfigurationStore.Save(resolvedPath, overrides, fontSize.Value);
            }
            catch (Exception exception)
            {
                error.Text = exception.Message;
                args.Cancel = true;
            }
        };

        var result = await dialog.ShowAsync();
        return result == ContentDialogResult.Primary && parsed is not null
            ? new SettingsResult(parsed, fontSize.Value)
            : null;
    }

    private static Dictionary<string, string> ParseBindings(string value)
    {
        var result = new Dictionary<string, string>(StringComparer.Ordinal);
        foreach (var source in value.Replace("\r\n", "\n", StringComparison.Ordinal).Split('\n'))
        {
            var line = source.Trim();
            if (line.Length == 0) continue;
            var separator = line.IndexOf('=');
            if (separator <= 0)
                throw new InvalidDataException($"Expected ‘keys = command’: {line}");
            var key = line[..separator].Trim();
            var command = line[(separator + 1)..].Trim();
            if (key.Length > 128 || command.Length > 4096)
                throw new InvalidDataException("Keys must be at most 128 characters and commands at most 4096 characters.");
            if (!result.TryAdd(key, command))
                throw new InvalidDataException($"Duplicate key sequence: {key}");
        }
        return result;
    }
}
