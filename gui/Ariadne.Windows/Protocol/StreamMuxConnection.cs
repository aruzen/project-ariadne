using System.Buffers.Binary;
using System.Collections.Concurrent;
using System.IO.Pipes;

namespace Ariadne.Windows.Protocol;

internal enum FrameFlags : uint
{
    Request = 1,
    Response = 2,
    Event = 4,
}

internal readonly record struct MuxFrame(
    ushort MessageType,
    FrameFlags Flags,
    ulong CorrelationId,
    ulong StreamId,
    byte[] Payload);

internal sealed class StreamMuxConnection : IAsyncDisposable
{
    private const int HeaderSize = 28;
    private const int MaxFrameBytes = 4 * 1024 * 1024;
    private readonly NamedPipeClientStream pipe;
    private readonly CancellationTokenSource lifetime = new();
    private readonly SemaphoreSlim writer = new(1, 1);
    private readonly ConcurrentDictionary<ulong, PendingCall> pending = new();
    private readonly Task reader;
    private long nextCorrelation;

    private sealed record PendingCall(ushort MessageType, ulong StreamId, TaskCompletionSource<MuxFrame> Completion);

    private StreamMuxConnection(NamedPipeClientStream pipe)
    {
        this.pipe = pipe;
        reader = ReadLoopAsync();
    }

    public event Func<MuxFrame, Task>? EventReceived;
    public event Func<MuxFrame, Task>? RequestReceived;
    public event Action<Exception>? Failed;

    public static async Task<StreamMuxConnection> ConnectAsync(string endpoint, CancellationToken cancellationToken)
    {
        const string prefix = @"\\.\pipe\";
        if (!endpoint.StartsWith(prefix, StringComparison.OrdinalIgnoreCase) || endpoint.Length == prefix.Length)
        {
            throw new ArgumentException("invalid Ariadne named pipe endpoint", nameof(endpoint));
        }

        var name = endpoint[prefix.Length..];
        var pipe = new NamedPipeClientStream(".", name, PipeDirection.InOut, PipeOptions.Asynchronous);
        await pipe.ConnectAsync(cancellationToken).ConfigureAwait(false);
        pipe.ReadMode = PipeTransmissionMode.Byte;
        NamedPipePeerVerifier.VerifyCurrentUserServer(pipe.SafePipeHandle);
        return new StreamMuxConnection(pipe);
    }

    public async Task<MuxFrame> CallAsync(ushort messageType, ulong streamId, byte[] payload, CancellationToken cancellationToken)
    {
        var correlation = unchecked((ulong)Interlocked.Increment(ref nextCorrelation));
        if (correlation == 0)
        {
            correlation = unchecked((ulong)Interlocked.Increment(ref nextCorrelation));
        }
        var completion = new TaskCompletionSource<MuxFrame>(TaskCreationOptions.RunContinuationsAsynchronously);
        var call = new PendingCall(messageType, streamId, completion);
        if (!pending.TryAdd(correlation, call))
        {
            throw new InvalidOperationException("duplicate streammux correlation ID");
        }

        try
        {
            await SendAsync(new MuxFrame(messageType, FrameFlags.Request, correlation, streamId, payload), cancellationToken).ConfigureAwait(false);
            return await completion.Task.WaitAsync(cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            pending.TryRemove(correlation, out _);
        }
    }

    public Task EmitAsync(ushort messageType, ulong streamId, byte[] payload, CancellationToken cancellationToken) =>
        SendAsync(new MuxFrame(messageType, FrameFlags.Event, 0, streamId, payload), cancellationToken);

    public Task RespondAsync(MuxFrame request, byte[] payload, CancellationToken cancellationToken) =>
        SendAsync(new MuxFrame(request.MessageType, FrameFlags.Response, request.CorrelationId, request.StreamId, payload), cancellationToken);

    private async Task SendAsync(MuxFrame frame, CancellationToken cancellationToken)
    {
        if (frame.Payload.Length > MaxFrameBytes)
        {
            throw new InvalidDataException("streammux frame exceeds 4 MiB");
        }

        var header = new byte[HeaderSize];
        BinaryPrimitives.WriteUInt32BigEndian(header.AsSpan(0, 4), (uint)frame.Payload.Length);
        BinaryPrimitives.WriteUInt16BigEndian(header.AsSpan(4, 2), 1);
        BinaryPrimitives.WriteUInt16BigEndian(header.AsSpan(6, 2), frame.MessageType);
        BinaryPrimitives.WriteUInt32BigEndian(header.AsSpan(8, 4), (uint)frame.Flags);
        BinaryPrimitives.WriteUInt64BigEndian(header.AsSpan(12, 8), frame.CorrelationId);
        BinaryPrimitives.WriteUInt64BigEndian(header.AsSpan(20, 8), frame.StreamId);

        using var linked = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken, lifetime.Token);
        await writer.WaitAsync(linked.Token).ConfigureAwait(false);
        try
        {
            await pipe.WriteAsync(header, linked.Token).ConfigureAwait(false);
            if (frame.Payload.Length != 0)
            {
                await pipe.WriteAsync(frame.Payload, linked.Token).ConfigureAwait(false);
            }
            await pipe.FlushAsync(linked.Token).ConfigureAwait(false);
        }
        finally
        {
            writer.Release();
        }
    }

    private async Task ReadLoopAsync()
    {
        Exception? failure = null;
        try
        {
            var header = new byte[HeaderSize];
            while (!lifetime.IsCancellationRequested)
            {
                await pipe.ReadExactlyAsync(header, lifetime.Token).ConfigureAwait(false);
                var length = BinaryPrimitives.ReadUInt32BigEndian(header.AsSpan(0, 4));
                var version = BinaryPrimitives.ReadUInt16BigEndian(header.AsSpan(4, 2));
                var type = BinaryPrimitives.ReadUInt16BigEndian(header.AsSpan(6, 2));
                var flags = (FrameFlags)BinaryPrimitives.ReadUInt32BigEndian(header.AsSpan(8, 4));
                var correlation = BinaryPrimitives.ReadUInt64BigEndian(header.AsSpan(12, 8));
                var stream = BinaryPrimitives.ReadUInt64BigEndian(header.AsSpan(20, 8));
                if (version != 1 || type == 0 || length > MaxFrameBytes ||
                    flags is not (FrameFlags.Request or FrameFlags.Response or FrameFlags.Event) ||
                    (flags == FrameFlags.Event ? correlation != 0 : correlation == 0))
                {
                    throw new InvalidDataException("invalid streammux frame header");
                }

                var payload = new byte[(int)length];
                if (payload.Length != 0)
                {
                    await pipe.ReadExactlyAsync(payload, lifetime.Token).ConfigureAwait(false);
                }
                var frame = new MuxFrame(type, flags, correlation, stream, payload);
                if (flags == FrameFlags.Response)
                {
                    if (pending.TryGetValue(correlation, out var call))
                    {
                        if (call.MessageType != type || call.StreamId != stream)
                        {
                            throw new InvalidDataException("streammux response does not match request");
                        }
                        call.Completion.TrySetResult(frame);
                    }
                    continue;
                }
                if (flags == FrameFlags.Request)
                {
                    var handler = RequestReceived;
                    if (handler is null)
                    {
                        throw new InvalidDataException($"unhandled streammux request type {type}");
                    }
                    _ = Task.Run(() => handler(frame), lifetime.Token);
                    continue;
                }
                var eventHandler = EventReceived;
                if (eventHandler is not null)
                {
                    await eventHandler(frame).ConfigureAwait(false);
                }
            }
        }
        catch (OperationCanceledException) when (lifetime.IsCancellationRequested)
        {
        }
        catch (Exception ex)
        {
            failure = ex;
            Failed?.Invoke(ex);
        }
        finally
        {
            lifetime.Cancel();
            var error = failure ?? new EndOfStreamException("Ariadne connection closed");
            foreach (var call in pending.Values)
            {
                call.Completion.TrySetException(error);
            }
        }
    }

    public async ValueTask DisposeAsync()
    {
        lifetime.Cancel();
        await pipe.DisposeAsync().ConfigureAwait(false);
        try
        {
            await reader.ConfigureAwait(false);
        }
        catch (OperationCanceledException)
        {
        }
        writer.Dispose();
        lifetime.Dispose();
    }
}
