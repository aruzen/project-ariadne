#nullable enable
using System.Text;
using Microsoft.Terminal.Wpf;
using Microsoft.UI.Dispatching;

namespace Ariadne.Terminal.WinUI;

internal sealed class TerminalConnectionAdapter : ITerminalConnection, IDisposable
{
    private readonly ITerminalBackend backend;
    private readonly DispatcherQueue dispatcher;
    private readonly CancellationTokenSource lifetime = new();
    private readonly Decoder decoder = Encoding.UTF8.GetDecoder();
    private readonly object outputGate = new();
    private readonly StringBuilder pendingText = new();
    private readonly TaskCompletionSource backendReady = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private readonly TaskCompletionSource firstOutput = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private readonly TaskCompletionSource firstResize = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private bool outputScheduled;
    private int started;
    private int closed;

    public TerminalConnectionAdapter(ITerminalBackend backend, DispatcherQueue dispatcher)
    {
        this.backend = backend;
        this.dispatcher = dispatcher;
        backend.OutputReceived += HandleOutput;
        backend.Exited += HandleExit;
        backend.Failed += HandleFailure;
    }

    public event EventHandler<TerminalOutputEventArgs>? TerminalOutput;
    public event Action<Exception>? Failed;
    public event Action<string>? OutputDecoded;
    public Task BackendReady => backendReady.Task;
    public Task FirstOutput => firstOutput.Task;
    public Task FirstResize => firstResize.Task;

    public void Start()
    {
        if (Interlocked.Exchange(ref started, 1) == 0)
        {
            _ = StartBackendAsync();
        }
    }

    public void WriteInput(string data)
    {
        if (!string.IsNullOrEmpty(data) && Volatile.Read(ref closed) == 0)
        {
            _ = ObserveAsync(backend.SendInputAsync(Encoding.UTF8.GetBytes(data), lifetime.Token));
        }
    }

    public void Resize(uint rows, uint columns)
    {
        if (rows != 0 && columns != 0 && Volatile.Read(ref closed) == 0)
        {
            firstResize.TrySetResult();
            _ = ObserveAsync(backend.ResizeAsync(columns, rows, lifetime.Token));
        }
    }

    public void Close() => Dispose();

    private void HandleOutput(ReadOnlyMemory<byte> data)
    {
        firstOutput.TrySetResult();
        lock (outputGate)
        {
            var chars = new char[Encoding.UTF8.GetMaxCharCount(data.Length)];
            decoder.Convert(data.Span, chars, false, out _, out var used, out _);
            if (used == 0) return;
            pendingText.Append(chars, 0, used);
            if (outputScheduled) return;
            outputScheduled = true;
        }
        dispatcher.TryEnqueue(DrainOutput);
    }

    private void DrainOutput()
    {
        string value;
        lock (outputGate)
        {
            value = pendingText.ToString();
            pendingText.Clear();
            outputScheduled = false;
        }
        OutputDecoded?.Invoke(value);
        TerminalOutput?.Invoke(this, new TerminalOutputEventArgs(value));
    }

    private void HandleExit() => dispatcher.TryEnqueue(() =>
        TerminalOutput?.Invoke(this, new TerminalOutputEventArgs("\r\n[process exited]\r\n")));

    private void HandleFailure(Exception error) => dispatcher.TryEnqueue(() => Failed?.Invoke(error));

    private async Task StartBackendAsync()
    {
        try
        {
            await backend.StartAsync(lifetime.Token).ConfigureAwait(false);
            backendReady.TrySetResult();
        }
        catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
        {
            backendReady.TrySetCanceled(lifetime.Token);
        }
        catch (Exception error)
        {
            backendReady.TrySetException(error);
            HandleFailure(error);
        }
    }

    private async Task ObserveAsync(ValueTask operation)
    {
        try
        {
            await operation.ConfigureAwait(false);
        }
        catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
        {
        }
        catch (Exception error)
        {
            HandleFailure(error);
        }
    }

    public void Dispose()
    {
        if (Interlocked.Exchange(ref closed, 1) != 0) return;
        backend.OutputReceived -= HandleOutput;
        backend.Exited -= HandleExit;
        backend.Failed -= HandleFailure;
        lifetime.Cancel();
        backendReady.TrySetCanceled(lifetime.Token);
        firstOutput.TrySetCanceled(lifetime.Token);
        firstResize.TrySetCanceled(lifetime.Token);
        _ = ObserveAsync(backend.DisposeAsync());
    }
}
