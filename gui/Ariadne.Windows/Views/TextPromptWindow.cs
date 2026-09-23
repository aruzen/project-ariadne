using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using System.Windows.Media;

namespace Ariadne.Windows.Views;

internal sealed class TextPromptWindow : Window
{
    private readonly TextBox input;

    private readonly bool allowEmpty;

    public TextPromptWindow(Window owner, string title, string label, string initialValue = "", string acceptLabel = "Run", bool allowEmpty = false)
    {
        this.allowEmpty = allowEmpty;
        Owner = owner;
        Title = title;
        Width = 640;
        SizeToContent = SizeToContent.Height;
        WindowStartupLocation = WindowStartupLocation.CenterOwner;
        ResizeMode = ResizeMode.NoResize;
        Background = new SolidColorBrush(Color.FromRgb(23, 27, 34));
        Foreground = Brushes.White;

        input = new TextBox
        {
            Text = initialValue,
            Margin = new Thickness(0, 8, 0, 12),
            Padding = new Thickness(6),
            Background = new SolidColorBrush(Color.FromRgb(12, 15, 19)),
            Foreground = Brushes.White,
            CaretBrush = Brushes.White,
            SelectionBrush = new SolidColorBrush(Color.FromRgb(52, 92, 140)),
            SelectionTextBrush = Brushes.White,
            BorderBrush = new SolidColorBrush(Color.FromRgb(70, 80, 94)),
        };
        var ok = new Button { Content = acceptLabel, IsDefault = true, MinWidth = 90, Padding = new Thickness(10, 5, 10, 5) };
        var cancel = new Button { Content = "Cancel", IsCancel = true, MinWidth = 90, Padding = new Thickness(10, 5, 10, 5), Margin = new Thickness(8, 0, 0, 0) };
        ok.Click += (_, _) =>
        {
            if (this.allowEmpty || !string.IsNullOrWhiteSpace(input.Text))
            {
                DialogResult = true;
            }
        };
        var buttons = new StackPanel { Orientation = Orientation.Horizontal, HorizontalAlignment = HorizontalAlignment.Right };
        buttons.Children.Add(ok);
        buttons.Children.Add(cancel);
        var content = new StackPanel { Margin = new Thickness(16) };
        content.Children.Add(new TextBlock { Text = label });
        content.Children.Add(input);
        content.Children.Add(buttons);
        Content = content;
        Loaded += (_, _) => Keyboard.Focus(input);
    }

    public string Value => input.Text;
}
