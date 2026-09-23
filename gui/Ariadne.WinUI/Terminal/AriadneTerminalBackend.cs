using Ariadne.Terminal.WinUI;
using Ariadne.Windows.Protocol;

namespace Ariadne.WinUI.Terminal;

internal sealed class AriadneTerminalBackend : ITerminalBackend
{
    private readonly AriadneClient client;
    private readonly ulong terminalId;
    private readonly CancellationTokenSource lifetime = new();
    private readonly object gate = new();
    private AriadnePtyAttachment? attachment;
    private uint pendingColumns;
    private uint pendingRows;
    private int started;
    private int disposed;

    public AriadneTerminalBackend(AriadneClient client, ulong terminalId)
    {
        this.client = client;
        this.terminalId = terminalId;
    }

    public event Action<ReadOnlyMemory<byte>>? OutputReceived;
    public event Action? Exited;
    public event Action<Exception>? Failed;

    public async ValueTask StartAsync(CancellationToken cancellationToken)
    {
        if (Interlocked.Exchange(ref started, 1) != 0) return;
        using var linked = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken, lifetime.Token);
        try
        {
            var value = await client.AttachAsync(terminalId, linked.Token).ConfigureAwait(false);
            if (Volatile.Read(ref disposed) != 0)
            {
                await value.DisposeAsync().ConfigureAwait(false);
                return;
            }
            lock (gate)
            {
                attachment = value;
            }
            value.SubscribeOutput(data => OutputReceived?.Invoke(data));
            value.Exited += _ => Exited?.Invoke();
            value.Failed += error => Failed?.Invoke(error);
            uint columns;
            uint rows;
            lock (gate)
            {
                columns = pendingColumns;
                rows = pendingRows;
            }
            if (columns != 0 && rows != 0)
            {
                await value.ResizeAsync(columns, rows, linked.Token).ConfigureAwait(false);
            }
        }
        catch (OperationCanceledException) when (linked.IsCancellationRequested)
        {
        }
    }

    public async ValueTask SendInputAsync(ReadOnlyMemory<byte> data, CancellationToken cancellationToken)
    {
        AriadnePtyAttachment? value;
        lock (gate) value = attachment;
        if (value is not null)
        {
            await value.SendInputAsync(data.ToArray(), cancellationToken).ConfigureAwait(false);
        }
    }

    public async ValueTask ResizeAsync(uint columns, uint rows, CancellationToken cancellationToken)
    {
        AriadnePtyAttachment? value;
        lock (gate)
        {
            pendingColumns = columns;
            pendingRows = rows;
            value = attachment;
        }
        if (value is not null)
        {
            await value.ResizeAsync(columns, rows, cancellationToken).ConfigureAwait(false);
        }
    }

    public async ValueTask DisposeAsync()
    {
        if (Interlocked.Exchange(ref disposed, 1) != 0) return;
        lifetime.Cancel();
        AriadnePtyAttachment? value;
        lock (gate)
        {
            value = attachment;
            attachment = null;
        }
        if (value is not null)
        {
            await value.DisposeAsync().ConfigureAwait(false);
        }
        lifetime.Dispose();
    }
}
