using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;

namespace Ariadne.WinUI.Views;

internal sealed record CommandPaletteItem(string Id, string Label);

internal static class CommandPaletteDialog
{
    public static async Task<CommandPaletteItem?> ShowAsync(
        XamlRoot xamlRoot,
        IReadOnlyList<CommandPaletteItem> items)
    {
        var filter = new TextBox
        {
            PlaceholderText = "Type to filter commands",
            Margin = new Thickness(0, 0, 0, 8),
        };
        var list = new ListView
        {
            DisplayMemberPath = nameof(CommandPaletteItem.Label),
            SelectionMode = ListViewSelectionMode.Single,
            MinHeight = 320,
        };
        var panel = new Grid { MinWidth = 560 };
        panel.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        panel.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        panel.Children.Add(filter);
        Grid.SetRow(list, 1);
        panel.Children.Add(list);

        var dialog = new ContentDialog
        {
            XamlRoot = xamlRoot,
            Title = "Commands",
            Content = panel,
            PrimaryButtonText = "Run",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
        };

        void Refresh()
        {
            var words = filter.Text.Split(' ', StringSplitOptions.TrimEntries | StringSplitOptions.RemoveEmptyEntries);
            var filtered = items.Where(item =>
                words.All(word => item.Label.Contains(word, StringComparison.OrdinalIgnoreCase))).ToList();
            list.ItemsSource = filtered;
            list.SelectedIndex = filtered.Count == 0 ? -1 : 0;
        }

        filter.TextChanged += (_, _) => Refresh();
        filter.KeyDown += (_, args) =>
        {
            if (args.Key == global::Windows.System.VirtualKey.Down && list.Items.Count != 0)
            {
                list.SelectedIndex = Math.Min(list.Items.Count - 1, list.SelectedIndex + 1);
                list.ScrollIntoView(list.SelectedItem);
                args.Handled = true;
            }
            else if (args.Key == global::Windows.System.VirtualKey.Up && list.Items.Count != 0)
            {
                list.SelectedIndex = Math.Max(0, list.SelectedIndex - 1);
                list.ScrollIntoView(list.SelectedItem);
                args.Handled = true;
            }
        };
        dialog.PrimaryButtonClick += (_, args) => args.Cancel = list.SelectedItem is not CommandPaletteItem;
        Refresh();
        dialog.Opened += (_, _) => filter.Focus(FocusState.Programmatic);
        return await dialog.ShowAsync() == ContentDialogResult.Primary
            ? list.SelectedItem as CommandPaletteItem
            : null;
    }
}
