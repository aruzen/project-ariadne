#nullable enable
namespace Ariadne.Terminal.WinUI;

public interface ITerminalBackend : IAsyncDisposable
{
    event Action<ReadOnlyMemory<byte>>? OutputReceived;
    event Action? Exited;
    event Action<Exception>? Failed;

    ValueTask StartAsync(CancellationToken cancellationToken);
    ValueTask SendInputAsync(ReadOnlyMemory<byte> data, CancellationToken cancellationToken);
    ValueTask ResizeAsync(uint columns, uint rows, CancellationToken cancellationToken);
}

public sealed record TerminalAppearance(
    uint Background,
    uint Foreground,
    uint SelectionBackground,
    IReadOnlyList<uint> ColorTable,
    string FontFamily,
    double FontSize);
