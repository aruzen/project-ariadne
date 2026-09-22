using System.Text;
using Ariadne.Windows.Protocol;
using Microsoft.Terminal.Wpf;

namespace Ariadne.Windows.Terminal;

internal sealed class AriadneTerminalConnection : ITerminalConnection, IDisposable
{
    private readonly AriadneClient client;
    private readonly ulong terminalId;
    private readonly System.Windows.Threading.Dispatcher dispatcher;
    private readonly CancellationTokenSource lifetime = new();
    private readonly Decoder decoder = Encoding.UTF8.GetDecoder();
    private readonly object decoderGate = new();
    private readonly StringBuilder pendingText = new();
    private bool outputScheduled;
    private AriadnePtyAttachment? attachment;
    private uint pendingRows;
    private uint pendingColumns;
    private int started;
    private int closed;

    public AriadneTerminalConnection(
        AriadneClient client,
        ulong terminalId,
        System.Windows.Threading.Dispatcher dispatcher)
    {
        this.client = client;
        this.terminalId = terminalId;
        this.dispatcher = dispatcher;
    }

    public event EventHandler<TerminalOutputEventArgs>? TerminalOutput;
    public event Action<Exception>? Failed;

    public void Start()
    {
        if (Interlocked.Exchange(ref started, 1) != 0)
        {
            return;
        }
        _ = StartAsync();
    }

    private async Task StartAsync()
    {
        try
        {
            var value = await client.AttachAsync(terminalId, lifetime.Token).ConfigureAwait(false);
            if (Volatile.Read(ref closed) != 0)
            {
                await value.DisposeAsync().ConfigureAwait(false);
                return;
            }
            attachment = value;
            value.SubscribeOutput(HandleOutput);
            value.Exited += HandleExit;
            value.Failed += HandleFailure;
            if (pendingRows != 0 && pendingColumns != 0)
            {
                await value.ResizeAsync(pendingColumns, pendingRows, lifetime.Token).ConfigureAwait(false);
            }
        }
        catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
        {
        }
        catch (Exception ex)
        {
            HandleFailure(ex);
        }
    }

    public void WriteInput(string data)
    {
        if (string.IsNullOrEmpty(data) || attachment is not { } value)
        {
            return;
        }
        _ = ObserveAsync(value.SendInputAsync(Encoding.UTF8.GetBytes(data), lifetime.Token));
    }

    // Windows Terminal's contract uses rows first, columns second.
    public void Resize(uint rows, uint columns)
    {
        pendingRows = rows;
        pendingColumns = columns;
        if (attachment is { } value)
        {
            _ = ObserveAsync(value.ResizeAsync(columns, rows, lifetime.Token));
        }
    }

    public void Close() => Dispose();

    private void HandleOutput(byte[] data)
    {
        lock (decoderGate)
        {
            var chars = new char[Encoding.UTF8.GetMaxCharCount(data.Length)];
            decoder.Convert(data, chars, flush: false, out _, out var used, out _);
            if (used == 0)
            {
                return;
            }
            pendingText.Append(chars, 0, used);
            if (outputScheduled)
            {
                return;
            }
            outputScheduled = true;
        }
        dispatcher.BeginInvoke(DrainOutput);
    }

    private void DrainOutput()
    {
        string text;
        lock (decoderGate)
        {
            text = pendingText.ToString();
            pendingText.Clear();
            outputScheduled = false;
        }
        TerminalOutput?.Invoke(this, new TerminalOutputEventArgs(text));
    }

    private void HandleExit(byte[] payload) =>
        dispatcher.BeginInvoke(() => TerminalOutput?.Invoke(this, new TerminalOutputEventArgs("\r\n[process exited]\r\n")));

    private void HandleFailure(Exception error) => dispatcher.BeginInvoke(() => Failed?.Invoke(error));

    private async Task ObserveAsync(Task task)
    {
        try
        {
            await task.ConfigureAwait(false);
        }
        catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
        {
        }
        catch (Exception ex)
        {
            HandleFailure(ex);
        }
    }

    public void Dispose()
    {
        if (Interlocked.Exchange(ref closed, 1) != 0)
        {
            return;
        }
        lifetime.Cancel();
        if (attachment is { } value)
        {
            _ = ObserveAsync(value.DisposeAsync().AsTask());
        }
    }
}
