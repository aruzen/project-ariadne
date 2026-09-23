using System.Buffers.Binary;
using System.Collections.Concurrent;
using System.Diagnostics;
using System.IO.Pipes;
using System.Security.Principal;
using System.Text;
using System.Text.Json;

namespace Ariadne.Windows.Protocol;

internal sealed class AriadneClient : IAsyncDisposable
{
    private const ushort MessageCommand = 0x1000;
    private const ushort MessageEvent = 0x1001;
    private const ushort MessagePluginInteraction = 0x1002;
    private const ushort MessagePluginInteractionCancel = 0x1003;
    private const ushort MessageFrontendControl = 0x1004;
    private const ushort PtyAttach = 0x1103;
    private const ushort PtyDetach = 0x1104;
    private const ushort PtyInput = 0x1105;
    private const ushort PtyOutput = 0x1106;
    private const ushort PtyResize = 0x1107;
    private const ushort PtyExit = 0x1109;
    private const ushort PtyReplayBegin = 0x110a;
    private const ushort PtyReplayEnd = 0x110b;
    private const ushort PtyError = 0x110c;

    private readonly StreamMuxConnection connection;
    private readonly ConcurrentDictionary<ulong, AriadnePtyAttachment> attachments = new();
    private readonly ConcurrentDictionary<string, CancellationTokenSource> interactions = new();
    private readonly CancellationTokenSource lifetime = new();

    private AriadneClient(StreamMuxConnection connection)
    {
        this.connection = connection;
        Store = new SnapshotStore();
        connection.EventReceived += HandleEventAsync;
        connection.RequestReceived += HandleRequestAsync;
        connection.Failed += error => Failed?.Invoke(error);
    }

    public SnapshotStore Store { get; }
    public GuiOptions Gui { get; private set; } = new();
    public event Func<ulong, Task>? NavigateRequested;
    public event Func<PluginInteractionRequest, CancellationToken, Task<PluginInteractionResult>>? PluginInteractionRequested;
    public event Action<Exception>? Failed;

    public static string DefaultEndpoint
    {
        get
        {
            using var identity = WindowsIdentity.GetCurrent();
            var sid = identity.User?.Value ?? throw new InvalidOperationException("current Windows user has no SID");
            return $@"\\.\pipe\ariadne-{sid}";
        }
    }

    public static async Task<AriadneClient> ConnectAsync(
        string? endpoint = null,
        bool startDaemon = true,
        CancellationToken cancellationToken = default)
    {
        endpoint ??= DefaultEndpoint;
        Exception? firstError = null;
        try
        {
            var initial = await TryConnectAsync(endpoint, TimeSpan.FromMilliseconds(350), cancellationToken).ConfigureAwait(false);
            return await InitializeAsync(initial, cancellationToken).ConfigureAwait(false);
        }
        catch (Exception ex) when (startDaemon && !cancellationToken.IsCancellationRequested &&
                                   ex is IOException or TimeoutException or OperationCanceledException)
        {
            firstError = ex;
        }

        StartDaemon(endpoint);
        var deadline = DateTime.UtcNow + TimeSpan.FromSeconds(10);
        while (DateTime.UtcNow < deadline)
        {
            cancellationToken.ThrowIfCancellationRequested();
            try
            {
                var candidate = await TryConnectAsync(endpoint, TimeSpan.FromMilliseconds(500), cancellationToken).ConfigureAwait(false);
                return await InitializeAsync(candidate, cancellationToken).ConfigureAwait(false);
            }
            catch (Exception ex) when (ex is IOException or TimeoutException or OperationCanceledException)
            {
                await Task.Delay(150, cancellationToken).ConfigureAwait(false);
            }
        }
        throw new IOException("Ariadne daemon did not become ready", firstError);
    }

    private static async Task<StreamMuxConnection> TryConnectAsync(string endpoint, TimeSpan timeout, CancellationToken cancellationToken)
    {
        using var timeoutSource = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeoutSource.CancelAfter(timeout);
        try
        {
            return await StreamMuxConnection.ConnectAsync(endpoint, timeoutSource.Token).ConfigureAwait(false);
        }
        catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
        {
            throw new TimeoutException("timed out connecting to Ariadne daemon");
        }
    }

    private static void StartDaemon(string endpoint)
    {
        var executable = Path.Combine(AppContext.BaseDirectory, "ariadne.exe");
        if (!File.Exists(executable))
        {
            throw new FileNotFoundException("ariadne.exe must be placed beside Ariadne.Windows.exe", executable);
        }
        _ = Process.Start(new ProcessStartInfo
        {
            FileName = executable,
            UseShellExecute = false,
            CreateNoWindow = true,
            Arguments = $"-socket {QuoteArgument(endpoint)} daemon serve",
            WorkingDirectory = AppContext.BaseDirectory,
        }) ?? throw new InvalidOperationException("failed to start Ariadne daemon");
    }

    private static string QuoteArgument(string value) => '"' + value.Replace("\"", "\\\"") + '"';

    private static async Task<AriadneClient> InitializeAsync(StreamMuxConnection connection, CancellationToken cancellationToken)
    {
        var client = new AriadneClient(connection);
        try
        {
            var synchronized = await client.CommandAsync<SyncResult>("sync", null, cancellationToken).ConfigureAwait(false);
            client.Store.Initialize(synchronized.Snapshot);
            client.Gui = synchronized.Gui;
            return client;
        }
        catch
        {
            await client.DisposeAsync().ConfigureAwait(false);
            throw;
        }
    }

    public Task<SetFocusResult> SetFocusAsync(ulong paneId, CancellationToken cancellationToken = default) =>
        CommandAsync<SetFocusResult>("set_focus", new { pane_id = paneId }, cancellationToken);

    public Task<SetFocusResult> SelectWindowAsync(ulong windowId, CancellationToken cancellationToken = default) =>
        CommandAsync<SetFocusResult>("select_window", new { window_id = windowId }, cancellationToken);

    public Task<CreateWorkspaceResult> CreateWorkspaceAsync(string name, CancellationToken cancellationToken = default) =>
        CommandAsync<CreateWorkspaceResult>("create_workspace", new { name }, cancellationToken);

    public Task<CreateWindowResult> CreateWindowAsync(ulong workspaceId, string name, CancellationToken cancellationToken = default) =>
        CommandAsync<CreateWindowResult>("create_window", new { workspace_id = workspaceId, name }, cancellationToken);

    public Task<WorkspaceOperationResult> RenameWorkspaceAsync(ulong workspaceId, string name, CancellationToken cancellationToken = default) =>
        CommandAsync<WorkspaceOperationResult>("rename_workspace", new { workspace_id = workspaceId, name }, cancellationToken);

    public Task<WindowOperationResult> RenameWindowAsync(ulong windowId, string name, CancellationToken cancellationToken = default) =>
        CommandAsync<WindowOperationResult>("rename_window", new { window_id = windowId, name }, cancellationToken);

    public Task<JsonElement> StashWindowAsync(ulong windowId, CancellationToken cancellationToken = default) =>
        CommandAsync<JsonElement>("stash_window", new { window_id = windowId }, cancellationToken);

    public Task<TerminalOperationResult> NewTerminalAsync(
        ulong windowId,
        ulong targetPaneId,
        string direction,
        IReadOnlyList<string> argv,
        string cwd,
        IReadOnlyList<string> environment,
        uint columns,
        uint rows,
        CancellationToken cancellationToken = default) =>
        CommandAsync<TerminalOperationResult>("new_terminal", new
        {
            window_id = windowId,
            target_pane_id = targetPaneId,
            direction = targetPaneId == 0 ? null : direction,
            argv,
            cwd,
            env = environment,
            initial_size = new { cols = columns, rows },
        }, cancellationToken);

    public Task<TerminalOperationResult> RestartTerminalAsync(
        ulong paneId,
        IReadOnlyList<string> environment,
        uint columns,
        uint rows,
        CancellationToken cancellationToken = default) =>
        CommandAsync<TerminalOperationResult>("restart_terminal", new
        {
            pane_id = paneId,
            env = environment,
            initial_size = new { cols = columns, rows },
        }, cancellationToken);

    public Task<TerminalOperationResult> RunTerminalAsync(
        ulong paneId,
        IReadOnlyList<string> argv,
        string fallbackWorkingDirectory,
        IReadOnlyList<string> environment,
        uint columns,
        uint rows,
        CancellationToken cancellationToken = default) =>
        CommandAsync<TerminalOperationResult>("run_terminal", new
        {
            pane_id = paneId,
            argv,
            fallback_cwd = fallbackWorkingDirectory,
            env = environment,
            initial_size = new { cols = columns, rows },
        }, cancellationToken);

    public Task<TerminalOperationResult> StopTerminalAsync(ulong paneId, CancellationToken cancellationToken = default) =>
        CommandAsync<TerminalOperationResult>("stop_terminal", new { pane_id = paneId }, cancellationToken);

    public Task<TerminalOperationResult> DeletePaneAsync(ulong paneId, CancellationToken cancellationToken = default) =>
        CommandAsync<TerminalOperationResult>("delete_pane", new { pane_id = paneId }, cancellationToken);

    public Task<JsonElement> StashPaneAsync(ulong paneId, CancellationToken cancellationToken = default) =>
        CommandAsync<JsonElement>("stash_pane", new { pane_id = paneId }, cancellationToken);

    public Task<RestorePaneResult> RestorePaneAsync(ulong paneId, CancellationToken cancellationToken = default) =>
        CommandAsync<RestorePaneResult>("restore_pane", new { pane_id = paneId }, cancellationToken);

    public Task<RestoreWindowResult> RestoreWindowAsync(ulong windowId, CancellationToken cancellationToken = default) =>
        CommandAsync<RestoreWindowResult>("restore_window", new { window_id = windowId }, cancellationToken);

    public Task<JsonElement> ResizeSplitAsync(ulong splitId, IReadOnlyList<uint> weights, CancellationToken cancellationToken = default) =>
        CommandAsync<JsonElement>("resize_split", new { split_id = splitId, weights }, cancellationToken);

    public Task<JsonElement> AcknowledgeAttentionAsync(ulong id, CancellationToken cancellationToken = default) =>
        CommandAsync<JsonElement>("acknowledge_attention", new { id, at = DateTimeOffset.UtcNow }, cancellationToken);

    public async Task<GuiOptions> ReloadFrontendConfigAsync(CancellationToken cancellationToken = default)
    {
        var reloaded = await CommandAsync<GuiOptions>("reload_frontend_config", null, cancellationToken).ConfigureAwait(false);
        CopyGuiOptions(reloaded, Gui);
        return Gui;
    }

    public Task<ClipboardResult> ReadClipboardAsync(ulong paneId, CancellationToken cancellationToken = default) =>
        CommandAsync<ClipboardResult>("clipboard_read", new
        {
            pane_id = paneId,
            protocol = "gui-paste",
            approved = true,
        }, cancellationToken);

    public Task<ClipboardResult> WriteClipboardAsync(ulong paneId, string value, CancellationToken cancellationToken = default) =>
        CommandAsync<ClipboardResult>("clipboard_write", new
        {
            pane_id = paneId,
            protocol = "gui-copy",
            text = value,
            approved = true,
        }, cancellationToken);

    public Task<PluginManageResult> PluginAsync(PluginManageRequest request, CancellationToken cancellationToken = default)
    {
        request.RequestId ??= $"gui-{Guid.NewGuid():N}";
        return CommandAsync<PluginManageResult>("plugin", request, cancellationToken);
    }

    private static void CopyGuiOptions(GuiOptions source, GuiOptions destination)
    {
        destination.ConfigPath = source.ConfigPath;
        destination.FontFamily = source.FontFamily;
        destination.FontSize = source.FontSize;
        destination.SoftwareRendering = source.SoftwareRendering;
        destination.Background = source.Background;
        destination.Foreground = source.Foreground;
        destination.Selection = source.Selection;
        destination.Accent = source.Accent;
        destination.ColorTable = [.. source.ColorTable];
        destination.Shell = [.. source.Shell];
        destination.Editor = [.. source.Editor];
        destination.Keybindings = new Dictionary<string, string>(source.Keybindings, StringComparer.Ordinal);
        destination.DefaultKeybindings = new Dictionary<string, string>(source.DefaultKeybindings, StringComparer.Ordinal);
    }

    private async Task<T> CommandAsync<T>(string operation, object? parameters, CancellationToken cancellationToken)
    {
        var payload = Json.Serialize(new { version = 1, operation, @params = parameters });
        var frame = await connection.CallAsync(MessageCommand, 0, payload, cancellationToken).ConfigureAwait(false);
        var response = Json.Deserialize<ProtocolResponse>(frame.Payload);
        if (response.Version != 1)
        {
            throw new InvalidDataException("unsupported Ariadne protocol version");
        }
        if (response.Error is not null)
        {
            throw new AriadneRemoteException(response.Error.Code, response.Error.Message);
        }
        if (response.Result.ValueKind is JsonValueKind.Undefined or JsonValueKind.Null)
        {
            throw new InvalidDataException("Ariadne response has no result");
        }
        return response.Result.Deserialize<T>(Json.Options) ?? throw new InvalidDataException("empty Ariadne result");
    }

    public async Task<AriadnePtyAttachment> AttachAsync(ulong terminalId, CancellationToken cancellationToken = default)
    {
        if (terminalId == 0)
        {
            throw new ArgumentOutOfRangeException(nameof(terminalId));
        }
        var attachment = new AriadnePtyAttachment(this, terminalId);
        if (!attachments.TryAdd(terminalId, attachment))
        {
            throw new InvalidOperationException($"terminal {terminalId} is already attached");
        }
        try
        {
            var responseFrame = await connection.CallAsync(
                PtyAttach, terminalId, Json.Serialize(new { version = 1, replay = "history" }), cancellationToken).ConfigureAwait(false);
            EnsurePtyResponse(responseFrame.Payload);
            return attachment;
        }
        catch
        {
            attachments.TryRemove(new KeyValuePair<ulong, AriadnePtyAttachment>(terminalId, attachment));
            throw;
        }
    }

    internal Task SendInputAsync(ulong terminalId, byte[] data, CancellationToken cancellationToken) =>
        connection.EmitAsync(PtyInput, terminalId, data, cancellationToken);

    internal async Task ResizeAsync(ulong terminalId, uint columns, uint rows, CancellationToken cancellationToken)
    {
        if (columns == 0 || rows == 0)
        {
            return;
        }
        var payload = new byte[8];
        BinaryPrimitives.WriteUInt32BigEndian(payload.AsSpan(0, 4), columns);
        BinaryPrimitives.WriteUInt32BigEndian(payload.AsSpan(4, 4), rows);
        var frame = await connection.CallAsync(PtyResize, terminalId, payload, cancellationToken).ConfigureAwait(false);
        EnsurePtyResponse(frame.Payload);
    }

    internal async Task DetachAsync(AriadnePtyAttachment attachment, CancellationToken cancellationToken)
    {
        if (!attachments.TryRemove(new KeyValuePair<ulong, AriadnePtyAttachment>(attachment.TerminalId, attachment)))
        {
            return;
        }
        var frame = await connection.CallAsync(PtyDetach, attachment.TerminalId, [], cancellationToken).ConfigureAwait(false);
        EnsurePtyResponse(frame.Payload);
    }

    private Task HandleEventAsync(MuxFrame frame)
    {
        switch (frame.MessageType)
        {
            case MessageEvent when frame.StreamId == 0:
                var envelope = Json.Deserialize<EventEnvelope>(frame.Payload);
                if (envelope.Version != 1)
                {
                    throw new InvalidDataException("unsupported Ariadne event version");
                }
                Store.Apply(envelope.Event);
                break;
            case PtyOutput:
                if (attachments.TryGetValue(frame.StreamId, out var output))
                {
                    output.DeliverOutput(frame.Payload);
                }
                break;
            case PtyReplayBegin:
            case PtyReplayEnd:
                break;
            case PtyExit:
                if (attachments.TryGetValue(frame.StreamId, out var exited))
                {
                    exited.DeliverExit(frame.Payload);
                }
                break;
            case PtyError:
                if (attachments.TryGetValue(frame.StreamId, out var failed))
                {
                    failed.DeliverError(frame.Payload);
                }
                break;
            case MessagePluginInteractionCancel when frame.StreamId == 0:
                var cancelled = Json.Deserialize<PluginInteractionRequest>(frame.Payload);
                if (interactions.TryGetValue(cancelled.Id, out var interaction))
                {
                    interaction.Cancel();
                }
                break;
        }
        return Task.CompletedTask;
    }

    private async Task HandleRequestAsync(MuxFrame frame)
    {
        if (frame.StreamId != 0)
        {
            throw new InvalidDataException($"unsupported Ariadne request type {frame.MessageType}");
        }
        if (frame.MessageType == MessagePluginInteraction)
        {
            await HandlePluginInteractionAsync(frame).ConfigureAwait(false);
            return;
        }
        if (frame.MessageType != MessageFrontendControl)
        {
            throw new InvalidDataException($"unsupported Ariadne request type {frame.MessageType}");
        }
        object response;
        try
        {
            var request = Json.Deserialize<FrontendControlRequest>(frame.Payload);
            if (request.Version != 1 || request.Action != "navigate" || request.PaneId == 0)
            {
                throw new AriadneRemoteException("invalid_argument", "invalid frontend navigation request");
            }
            await Store.WaitForRevisionAsync(request.MinimumRevision, lifetime.Token).ConfigureAwait(false);
            var handler = NavigateRequested ?? throw new AriadneRemoteException("invalid_state", "frontend is not ready");
            await handler(request.PaneId).ConfigureAwait(false);
            response = new { version = 1 };
        }
        catch (AriadneRemoteException ex)
        {
            response = new { version = 1, error = new { code = ex.Code, message = ex.Message } };
        }
        catch (Exception ex)
        {
            response = new { version = 1, error = new { code = "internal", message = ex.Message } };
        }
        await connection.RespondAsync(frame, Json.Serialize(response), lifetime.Token).ConfigureAwait(false);
    }

    private async Task HandlePluginInteractionAsync(MuxFrame frame)
    {
        var request = Json.Deserialize<PluginInteractionRequest>(frame.Payload);
        using var cancellation = CancellationTokenSource.CreateLinkedTokenSource(lifetime.Token);
        if (string.IsNullOrWhiteSpace(request.Id) || !interactions.TryAdd(request.Id, cancellation))
        {
            await connection.RespondAsync(frame, Json.Serialize(new { error = "plugin: invalid or duplicate interaction" }), lifetime.Token).ConfigureAwait(false);
            return;
        }
        object response;
        try
        {
            var handler = PluginInteractionRequested ?? throw new InvalidOperationException("plugin: frontend is not ready for interaction");
            var result = await handler(request, cancellation.Token).ConfigureAwait(false);
            response = new { result };
        }
        catch (OperationCanceledException)
        {
            response = new { error = "plugin: interaction cancelled" };
        }
        catch (Exception error)
        {
            response = new { error = error.Message };
        }
        finally
        {
            interactions.TryRemove(new KeyValuePair<string, CancellationTokenSource>(request.Id, cancellation));
        }
        await connection.RespondAsync(frame, Json.Serialize(response), lifetime.Token).ConfigureAwait(false);
    }

    private static void EnsurePtyResponse(byte[] payload)
    {
        var response = Json.Deserialize<PtyProtocolResponse>(payload);
        if (response.Version != 1)
        {
            throw new InvalidDataException("unsupported PTY protocol version");
        }
        if (response.Error is not null)
        {
            throw new AriadneRemoteException(response.Error.Code, response.Error.Message);
        }
    }

    public async ValueTask DisposeAsync()
    {
        lifetime.Cancel();
        foreach (var interaction in interactions.Values)
        {
            interaction.Cancel();
        }
        interactions.Clear();
        foreach (var attachment in attachments.Values)
        {
            attachment.CloseLocally();
        }
        attachments.Clear();
        await connection.DisposeAsync().ConfigureAwait(false);
        lifetime.Dispose();
    }
}

internal sealed class AriadnePtyAttachment : IAsyncDisposable
{
    private readonly AriadneClient client;
    private readonly object outputGate = new();
    private readonly Queue<byte[]> pendingOutput = new();
    private Action<byte[]>? outputHandler;
    private int closed;

    internal AriadnePtyAttachment(AriadneClient client, ulong terminalId)
    {
        this.client = client;
        TerminalId = terminalId;
    }

    public ulong TerminalId { get; }
    public event Action<byte[]>? Exited;
    public event Action<Exception>? Failed;

    public Task SendInputAsync(byte[] data, CancellationToken cancellationToken = default) =>
        Volatile.Read(ref closed) == 0
            ? client.SendInputAsync(TerminalId, data, cancellationToken)
            : Task.CompletedTask;

    public Task ResizeAsync(uint columns, uint rows, CancellationToken cancellationToken = default) =>
        Volatile.Read(ref closed) == 0
            ? client.ResizeAsync(TerminalId, columns, rows, cancellationToken)
            : Task.CompletedTask;

    public void SubscribeOutput(Action<byte[]> handler)
    {
        ArgumentNullException.ThrowIfNull(handler);
        lock (outputGate)
        {
            if (outputHandler is not null)
            {
                throw new InvalidOperationException("PTY output already has a subscriber");
            }
            outputHandler = handler;
            while (pendingOutput.TryDequeue(out var data))
            {
                handler(data);
            }
        }
    }

    internal void DeliverOutput(byte[] data)
    {
        lock (outputGate)
        {
            if (outputHandler is null)
            {
                pendingOutput.Enqueue(data);
            }
            else
            {
                outputHandler(data);
            }
        }
    }
    internal void DeliverExit(byte[] payload) => Exited?.Invoke(payload);
    internal void DeliverError(byte[] payload) => Failed?.Invoke(new InvalidDataException(Encoding.UTF8.GetString(payload)));
    internal void CloseLocally() => Interlocked.Exchange(ref closed, 1);

    public async ValueTask DisposeAsync()
    {
        if (Interlocked.Exchange(ref closed, 1) != 0)
        {
            return;
        }
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(2));
        try
        {
            await client.DetachAsync(this, timeout.Token).ConfigureAwait(false);
        }
        catch (Exception ex) when (ex is IOException or OperationCanceledException)
        {
        }
    }
}
