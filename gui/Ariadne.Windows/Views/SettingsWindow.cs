using System.Collections.ObjectModel;
using System.Globalization;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Data;
using System.Windows.Media;
using Ariadne.Windows.Configuration;

namespace Ariadne.Windows.Views;

internal sealed class SettingsWindow : Window
{
    private sealed class BindingRow
    {
        public string Key { get; set; } = "";
        public string Command { get; set; } = "";
    }

    private readonly Dictionary<string, string> defaults;
    private readonly ObservableCollection<BindingRow> rows;
    private readonly DataGrid grid;
    private readonly TextBox fontSize;
    private readonly string configPath;

    public SettingsWindow(Window owner, IReadOnlyDictionary<string, string> current,
        IReadOnlyDictionary<string, string> defaultBindings, double currentFontSize, string configPath)
    {
        Owner = owner;
        Title = "Ariadne settings";
        Width = 760;
        Height = 600;
        MinWidth = 560;
        MinHeight = 400;
        WindowStartupLocation = WindowStartupLocation.CenterOwner;
        Background = new SolidColorBrush(Color.FromRgb(23, 27, 34));
        Foreground = new SolidColorBrush(Color.FromRgb(220, 224, 230));
        defaults = new Dictionary<string, string>(defaultBindings, StringComparer.Ordinal);
        this.configPath = string.IsNullOrWhiteSpace(configPath) ? ConfigurationStore.ResolvePath() : configPath;
        rows = new ObservableCollection<BindingRow>(current.OrderBy(value => value.Key, StringComparer.Ordinal)
            .Select(value => new BindingRow { Key = value.Key, Command = value.Value }));
        fontSize = new TextBox
        {
            Text = currentFontSize.ToString("0.##", CultureInfo.InvariantCulture),
            Width = 100,
            Padding = new Thickness(6, 4, 6, 4),
            HorizontalAlignment = HorizontalAlignment.Left,
            Background = new SolidColorBrush(Color.FromRgb(245, 246, 248)),
            Foreground = new SolidColorBrush(Color.FromRgb(32, 36, 42)),
        };

        grid = new DataGrid
        {
            ItemsSource = rows,
            AutoGenerateColumns = false,
            CanUserAddRows = false,
            HeadersVisibility = DataGridHeadersVisibility.Column,
            GridLinesVisibility = DataGridGridLinesVisibility.Horizontal,
            Background = new SolidColorBrush(Color.FromRgb(19, 23, 29)),
            Foreground = new SolidColorBrush(Color.FromRgb(32, 36, 42)),
            RowBackground = new SolidColorBrush(Color.FromRgb(245, 246, 248)),
            AlternatingRowBackground = new SolidColorBrush(Color.FromRgb(231, 234, 238)),
            HorizontalGridLinesBrush = new SolidColorBrush(Color.FromRgb(190, 196, 204)),
            Margin = new Thickness(12, 8, 12, 8),
        };
        grid.Columns.Add(new DataGridTextColumn
        {
            Header = "Keys", Width = new DataGridLength(220),
            Binding = new Binding(nameof(BindingRow.Key)) { UpdateSourceTrigger = UpdateSourceTrigger.LostFocus },
        });
        grid.Columns.Add(new DataGridTextColumn
        {
            Header = "Command prompt", Width = new DataGridLength(1, DataGridLengthUnitType.Star),
            Binding = new Binding(nameof(BindingRow.Command)) { UpdateSourceTrigger = UpdateSourceTrigger.LostFocus },
        });

        var add = Button("Add", (_, _) => rows.Add(new BindingRow()));
        var remove = Button("Disable / remove", (_, _) => RemoveSelected());
        var reset = Button("Reset selected", (_, _) => ResetSelected());
        var save = Button("Save", (_, _) => Save(), true);
        var cancel = Button("Cancel", (_, _) => DialogResult = false);
        cancel.IsCancel = true;
        var actions = new StackPanel { Orientation = Orientation.Horizontal, Margin = new Thickness(12, 0, 12, 12) };
        actions.Children.Add(add);
        actions.Children.Add(remove);
        actions.Children.Add(reset);
        actions.Children.Add(new Border { Width = 20 });
        actions.Children.Add(save);
        actions.Children.Add(cancel);

        var keyboard = new DockPanel();
        var heading = new StackPanel { Margin = new Thickness(12, 12, 12, 0) };
        heading.Children.Add(new TextBlock { Text = "Keyboard shortcuts shared with the TUI", FontSize = 16, FontWeight = FontWeights.SemiBold });
        heading.Children.Add(new TextBlock { Text = this.configPath, Foreground = new SolidColorBrush(Color.FromRgb(165, 173, 184)), Margin = new Thickness(0, 4, 0, 0) });
        DockPanel.SetDock(heading, Dock.Top);
        keyboard.Children.Add(heading);
        keyboard.Children.Add(grid);

        var appearance = new StackPanel { Margin = new Thickness(16) };
        appearance.Children.Add(new TextBlock { Text = "Terminal appearance", FontSize = 16, FontWeight = FontWeights.SemiBold });
        appearance.Children.Add(new TextBlock
        {
            Text = "Font size (6–96)",
            Margin = new Thickness(0, 18, 0, 6),
        });
        appearance.Children.Add(fontSize);
        appearance.Children.Add(new TextBlock
        {
            Text = "Applies to terminal and plugin panes without restarting the daemon.",
            Foreground = new SolidColorBrush(Color.FromRgb(165, 173, 184)),
            Margin = new Thickness(0, 8, 0, 0),
        });

        var tabs = new TabControl { Margin = new Thickness(12, 8, 12, 8) };
        tabs.Items.Add(new TabItem { Header = "Appearance", Content = appearance });
        tabs.Items.Add(new TabItem { Header = "Keyboard shortcuts", Content = keyboard });

        var root = new DockPanel();
        DockPanel.SetDock(actions, Dock.Bottom);
        root.Children.Add(actions);
        root.Children.Add(tabs);
        Content = root;
    }

    public Dictionary<string, string>? EffectiveBindings { get; private set; }
    public double GuiFontSize { get; private set; }

    private static Button Button(string text, RoutedEventHandler handler, bool primary = false)
    {
        var button = new Button { Content = text, MinWidth = 90, Padding = new Thickness(10, 5, 10, 5), Margin = new Thickness(0, 0, 8, 0), IsDefault = primary };
        button.Click += handler;
        return button;
    }

    private void RemoveSelected()
    {
        if (grid.SelectedItem is not BindingRow row) return;
        if (defaults.ContainsKey(row.Key)) row.Command = "";
        else rows.Remove(row);
        grid.Items.Refresh();
    }

    private void ResetSelected()
    {
        if (grid.SelectedItem is not BindingRow row) return;
        if (defaults.TryGetValue(row.Key, out var command)) row.Command = command;
        else rows.Remove(row);
        grid.Items.Refresh();
    }

    private void Save()
    {
        if ((!double.TryParse(fontSize.Text, NumberStyles.Float, CultureInfo.CurrentCulture, out var parsedFontSize) &&
             !double.TryParse(fontSize.Text, NumberStyles.Float, CultureInfo.InvariantCulture, out parsedFontSize)) ||
            !double.IsFinite(parsedFontSize) || parsedFontSize < 6 || parsedFontSize > 96)
        {
            MessageBox.Show(this, "Font size must be a number between 6 and 96.", "Ariadne settings", MessageBoxButton.OK, MessageBoxImage.Warning);
            fontSize.Focus();
            fontSize.SelectAll();
            return;
        }
        grid.CommitEdit(DataGridEditingUnit.Cell, true);
        grid.CommitEdit(DataGridEditingUnit.Row, true);
        var effective = new Dictionary<string, string>(StringComparer.Ordinal);
        foreach (var row in rows)
        {
            var key = row.Key.Trim();
            if (key.Length == 0 || key.Length > 128 || row.Command.Length > 4096)
            {
                MessageBox.Show(this, "Keys must be 1–128 characters and commands at most 4096 characters.", "Ariadne settings", MessageBoxButton.OK, MessageBoxImage.Warning);
                return;
            }
            if (!effective.TryAdd(key, row.Command))
            {
                MessageBox.Show(this, $"Duplicate key sequence: {key}", "Ariadne settings", MessageBoxButton.OK, MessageBoxImage.Warning);
                return;
            }
        }
        var overrides = effective.Where(value => !defaults.TryGetValue(value.Key, out var command) || command != value.Value)
            .ToDictionary(value => value.Key, value => value.Value, StringComparer.Ordinal);
        try
        {
            ConfigurationStore.Save(configPath, overrides, parsedFontSize);
            EffectiveBindings = effective;
            GuiFontSize = parsedFontSize;
            DialogResult = true;
        }
        catch (Exception ex)
        {
            MessageBox.Show(this, ex.Message, "Could not save Ariadne settings", MessageBoxButton.OK, MessageBoxImage.Error);
        }
    }
}
