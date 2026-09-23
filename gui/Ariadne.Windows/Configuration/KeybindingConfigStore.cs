using System.Text;
using System.Globalization;

namespace Ariadne.Windows.Configuration;

internal static class ConfigurationStore
{
    public static string ResolvePath()
    {
        var explicitDirectory = Environment.GetEnvironmentVariable("ARIADNE_CONFIG_PATH");
        if (!string.IsNullOrEmpty(explicitDirectory))
        {
            return InDirectory(explicitDirectory, "ARIADNE_CONFIG_PATH");
        }
        var xdg = Environment.GetEnvironmentVariable("XDG_CONFIG_HOME");
        if (!string.IsNullOrEmpty(xdg))
        {
            return Path.Combine(FullDirectory(xdg, "XDG_CONFIG_HOME"), "ariadne", "config.toml");
        }
        var home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
        if (string.IsNullOrEmpty(home))
        {
            throw new InvalidOperationException("cannot resolve the Ariadne configuration directory");
        }
        return Path.Combine(home, ".config", "ariadne", "config.toml");
    }

    public static void Save(string path, IReadOnlyDictionary<string, string> bindings, double guiFontSize)
    {
        var original = File.Exists(path) ? File.ReadAllText(path, Encoding.UTF8) : "# Ariadne configuration\n";
        var newline = original.Contains("\r\n", StringComparison.Ordinal) ? "\r\n" : "\n";
        var lines = original.Replace("\r\n", "\n", StringComparison.Ordinal).Split('\n').ToList();
        var section = new List<string> { "[keybindings.normal]" };
        section.AddRange(bindings.OrderBy(value => value.Key, StringComparer.Ordinal)
            .Select(value => $"{Quote(value.Key)} = {Quote(value.Value)}"));
        section.Add("");
        ReplaceSection(lines, "keybindings.normal", section);
        UpsertValue(lines, "gui", "font_size", guiFontSize.ToString("0.##", CultureInfo.InvariantCulture));

        var directory = Path.GetDirectoryName(path) ?? throw new InvalidOperationException("configuration path has no directory");
        Directory.CreateDirectory(directory);
        var temporary = Path.Combine(directory, $".{Path.GetFileName(path)}.{Guid.NewGuid():N}.tmp");
        try
        {
            File.WriteAllText(temporary, string.Join(newline, lines), new UTF8Encoding(false));
            File.Move(temporary, path, true);
        }
        finally
        {
            if (File.Exists(temporary)) File.Delete(temporary);
        }
    }

    private static void ReplaceSection(List<string> lines, string name, IReadOnlyCollection<string> replacement)
    {
        var start = FindSection(lines, name);
        if (start < 0)
        {
            AppendSection(lines, replacement);
            return;
        }
        var end = FindSectionEnd(lines, start);
        lines.RemoveRange(start, end - start);
        lines.InsertRange(start, replacement);
    }

    private static void UpsertValue(List<string> lines, string section, string key, string value)
    {
        var start = FindSection(lines, section);
        if (start < 0)
        {
            AppendSection(lines, [$"[{section}]", $"{key} = {value}", ""]);
            return;
        }
        var end = FindSectionEnd(lines, start);
        for (var index = start + 1; index < end; index++)
        {
            var candidate = lines[index].TrimStart();
            if (candidate.StartsWith('#')) continue;
            var equals = candidate.IndexOf('=');
            if (equals >= 0 && candidate[..equals].Trim().Equals(key, StringComparison.Ordinal))
            {
                lines[index] = $"{key} = {value}";
                return;
            }
        }
        lines.Insert(end, $"{key} = {value}");
    }

    private static int FindSection(List<string> lines, string name) =>
        lines.FindIndex(line => line.Trim().Equals($"[{name}]", StringComparison.Ordinal));

    private static int FindSectionEnd(List<string> lines, int start)
    {
        var end = lines.FindIndex(start + 1, line =>
        {
            var value = line.Trim();
            return value.StartsWith("[", StringComparison.Ordinal) && value.EndsWith("]", StringComparison.Ordinal);
        });
        return end < 0 ? lines.Count : end;
    }

    private static void AppendSection(List<string> lines, IReadOnlyCollection<string> section)
    {
        if (lines.Count != 0 && lines[^1].Length != 0) lines.Add("");
        lines.AddRange(section);
    }

    private static string InDirectory(string value, string variable) =>
        Path.Combine(FullDirectory(value, variable), "config.toml");

    private static string FullDirectory(string value, string variable)
    {
        if (!Path.IsPathFullyQualified(value))
        {
            throw new InvalidOperationException($"{variable} must be an absolute path");
        }
        return value;
    }

    private static string Quote(string value) => '"' + value
        .Replace("\\", "\\\\", StringComparison.Ordinal)
        .Replace("\"", "\\\"", StringComparison.Ordinal)
        .Replace("\r", "\\r", StringComparison.Ordinal)
        .Replace("\n", "\\n", StringComparison.Ordinal)
        .Replace("\t", "\\t", StringComparison.Ordinal) + '"';
}
