using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using System.Windows.Media;

namespace Ariadne.Windows.Views;

internal sealed record CommandPaletteItem(string Id, string Label);

internal sealed class CommandPaletteWindow : Window
{
    private readonly IReadOnlyList<CommandPaletteItem> allItems;
    private readonly TextBox filter;
    private readonly ListBox list;

    public CommandPaletteWindow(Window owner, IReadOnlyList<CommandPaletteItem> items)
    {
        Owner = owner;
        Title = "Ariadne commands";
        Width = 620;
        Height = 440;
        WindowStartupLocation = WindowStartupLocation.CenterOwner;
        Background = new SolidColorBrush(Color.FromRgb(23, 27, 34));
        Foreground = Brushes.White;
        allItems = items;

        filter = new TextBox
        {
            Margin = new Thickness(12, 12, 12, 8),
            Padding = new Thickness(8),
            Background = new SolidColorBrush(Color.FromRgb(12, 15, 19)),
            Foreground = Brushes.White,
            CaretBrush = Brushes.White,
            SelectionBrush = new SolidColorBrush(Color.FromRgb(52, 92, 140)),
            SelectionTextBrush = Brushes.White,
            BorderBrush = new SolidColorBrush(Color.FromRgb(70, 80, 94)),
        };
        var itemStyle = new Style(typeof(ListBoxItem));
        itemStyle.Setters.Add(new Setter(Control.ForegroundProperty, new SolidColorBrush(Color.FromRgb(220, 224, 230))));
        itemStyle.Setters.Add(new Setter(Control.BackgroundProperty, Brushes.Transparent));
        itemStyle.Setters.Add(new Setter(Control.PaddingProperty, new Thickness(8, 6, 8, 6)));
        var hover = new Trigger { Property = ListBoxItem.IsMouseOverProperty, Value = true };
        hover.Setters.Add(new Setter(Control.BackgroundProperty, new SolidColorBrush(Color.FromRgb(42, 49, 60))));
        itemStyle.Triggers.Add(hover);
        var selected = new Trigger { Property = ListBoxItem.IsSelectedProperty, Value = true };
        selected.Setters.Add(new Setter(Control.BackgroundProperty, new SolidColorBrush(Color.FromRgb(48, 86, 132))));
        selected.Setters.Add(new Setter(Control.ForegroundProperty, Brushes.White));
        itemStyle.Triggers.Add(selected);
        list = new ListBox
        {
            Margin = new Thickness(12, 0, 12, 12),
            DisplayMemberPath = nameof(CommandPaletteItem.Label),
            Background = new SolidColorBrush(Color.FromRgb(19, 23, 29)),
            Foreground = new SolidColorBrush(Color.FromRgb(220, 224, 230)),
            BorderBrush = new SolidColorBrush(Color.FromRgb(70, 80, 94)),
            ItemContainerStyle = itemStyle,
        };
        filter.TextChanged += (_, _) => RefreshItems();
        filter.PreviewKeyDown += FilterOnPreviewKeyDown;
        list.MouseDoubleClick += (_, _) => Accept();
        list.PreviewKeyDown += (_, e) =>
        {
            if (e.Key == Key.Enter)
            {
                Accept();
                e.Handled = true;
            }
        };
        var layout = new DockPanel();
        DockPanel.SetDock(filter, Dock.Top);
        layout.Children.Add(filter);
        layout.Children.Add(list);
        Content = layout;
        RefreshItems();
        Loaded += (_, _) => Keyboard.Focus(filter);
    }

    public CommandPaletteItem? Selected { get; private set; }

    private void RefreshItems()
    {
        var words = filter.Text.Split(' ', StringSplitOptions.TrimEntries | StringSplitOptions.RemoveEmptyEntries);
        var filtered = allItems.Where(item => words.All(word => item.Label.Contains(word, StringComparison.OrdinalIgnoreCase))).ToList();
        list.ItemsSource = filtered;
        list.SelectedIndex = filtered.Count == 0 ? -1 : 0;
    }

    private void FilterOnPreviewKeyDown(object sender, KeyEventArgs e)
    {
        if (e.Key == Key.Down && list.Items.Count != 0)
        {
            list.SelectedIndex = Math.Min(list.Items.Count - 1, list.SelectedIndex + 1);
            list.ScrollIntoView(list.SelectedItem);
            e.Handled = true;
        }
        else if (e.Key == Key.Up && list.Items.Count != 0)
        {
            list.SelectedIndex = Math.Max(0, list.SelectedIndex - 1);
            list.ScrollIntoView(list.SelectedItem);
            e.Handled = true;
        }
        else if (e.Key == Key.Enter)
        {
            Accept();
            e.Handled = true;
        }
    }

    private void Accept()
    {
        if (list.SelectedItem is CommandPaletteItem item)
        {
            Selected = item;
            DialogResult = true;
        }
    }
}
