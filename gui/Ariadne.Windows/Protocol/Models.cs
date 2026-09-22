using System.Text.Json;

namespace Ariadne.Windows.Protocol;

public sealed class Snapshot
{
    public ulong Revision { get; set; }
    public ulong NextWorkspaceId { get; set; }
    public ulong NextWindowId { get; set; }
    public ulong NextPaneId { get; set; }
    public ulong NextSplitId { get; set; }
    public List<WorkspaceModel> Workspaces { get; set; } = [];
    public List<WindowModel> Windows { get; set; } = [];
    public List<PaneModel> Panes { get; set; } = [];
    public List<StashedPaneModel> StashedPanes { get; set; } = [];
    public List<StashedWindowModel> StashedWindows { get; set; } = [];
    public List<AttentionModel> Attentions { get; set; } = [];
    public List<ToolInstanceModel> ToolInstances { get; set; } = [];
}

public sealed class WorkspaceModel
{
    public ulong Id { get; set; }
    public string Name { get; set; } = "";
    public List<ulong> WindowIds { get; set; } = [];
    public override string ToString() => Name;
}

public sealed class WindowModel
{
    public ulong Id { get; set; }
    public ulong WorkspaceId { get; set; }
    public string Name { get; set; } = "";
    public LayoutNode? Layout { get; set; }
    public override string ToString() => Name;
}

public sealed class LayoutNode
{
    public string Kind { get; set; } = "";
    public ulong SplitId { get; set; }
    public ulong PaneId { get; set; }
    public string Direction { get; set; } = "";
    public List<LayoutNode> Children { get; set; } = [];
    public List<uint> Weights { get; set; } = [];
}

public sealed class PaneModel
{
    public bool Transient { get; set; }
    public ulong Id { get; set; }
    public ulong WindowId { get; set; }
    public string Kind { get; set; } = "";
    public string Title { get; set; } = "";
    public PanePresentation Presentation { get; set; } = new();
    public TerminalInstance? Terminal { get; set; }
    public ToolDescriptor? Tool { get; set; }
}

public sealed class PanePresentation
{
    public string Chrome { get; set; } = "";
}

public sealed class TerminalInstance
{
    public ulong? Id { get; set; }
    public string State { get; set; } = "";
    public LaunchSpec Launch { get; set; } = new();
    public TerminalExit? Exit { get; set; }
    public bool HistoryAvailable { get; set; }
}

public sealed class LaunchSpec
{
    public List<string> Argv { get; set; } = [];
    public string Cwd { get; set; } = "";
}

public sealed class TerminalExit
{
    public string Kind { get; set; } = "";
    public int Code { get; set; }
    public string Signal { get; set; } = "";
    public string Message { get; set; } = "";
}

public sealed class ToolDescriptor
{
    public string Provider { get; set; } = "";
    public string Type { get; set; } = "";
    public string Instance { get; set; } = "";
}

public sealed class ToolInstanceModel
{
    public ToolDescriptor Descriptor { get; set; } = new();
    public uint StateVersion { get; set; }
    public ulong Generation { get; set; }
    public JsonElement State { get; set; }
}

public sealed class StashedPaneModel
{
    public ulong PaneId { get; set; }
}

public sealed class StashedWindowModel
{
    public ulong WindowId { get; set; }
}

public sealed class AttentionModel
{
    public ulong Id { get; set; }
    public ulong PaneId { get; set; }
    public string Source { get; set; } = "";
    public string Key { get; set; } = "";
    public string Class { get; set; } = "";
    public string Severity { get; set; } = "";
    public string Message { get; set; } = "";
    public DateTimeOffset OccurredAt { get; set; }
    public DateTimeOffset UpdatedAt { get; set; }
    public DateTimeOffset? AcknowledgedAt { get; set; }
}

public sealed class FrontendState
{
    public ulong Id { get; set; }
    public ulong WorkspaceId { get; set; }
    public ulong WindowId { get; set; }
    public ulong PaneId { get; set; }
}

public sealed class SyncResult
{
    public Snapshot Snapshot { get; set; } = new();
}

public sealed class SetFocusResult
{
    public FrontendState Focus { get; set; } = new();
}

public sealed class TerminalOperationResult
{
    public PaneModel Pane { get; set; } = new();
}

public sealed class ClipboardResult
{
    public string Text { get; set; } = "";
}

public sealed class RestorePaneResult
{
    public PaneModel Pane { get; set; } = new();
    public WindowModel Window { get; set; } = new();
}

public sealed class RestoreWindowResult
{
    public WindowModel Window { get; set; } = new();
    public WorkspaceModel Workspace { get; set; } = new();
}

public sealed class CoreEvent
{
    public ulong Revision { get; set; }
    public string Kind { get; set; } = "";
    public JsonElement Payload { get; set; }
}

public sealed class EventEnvelope
{
    public ushort Version { get; set; }
    public CoreEvent Event { get; set; } = new();
}

public sealed class FrontendControlRequest
{
    public ushort Version { get; set; }
    public string Action { get; set; } = "";
    public ulong PaneId { get; set; }
    public ulong MinimumRevision { get; set; }
}

public sealed class RemoteError
{
    public string Code { get; set; } = "";
    public string Message { get; set; } = "";
}

public sealed class ProtocolResponse
{
    public ushort Version { get; set; }
    public JsonElement Result { get; set; }
    public RemoteError? Error { get; set; }
}

public sealed class PtyProtocolResponse
{
    public ushort Version { get; set; }
    public RemoteError? Error { get; set; }
    public PtyAttachResult? Attach { get; set; }
}

public sealed class PtyAttachResult
{
    public ulong FirstSequence { get; set; }
    public ulong LastSequence { get; set; }
    public ulong NextSequence { get; set; }
    public bool Truncated { get; set; }
}

public sealed class AriadneRemoteException(string code, string message) : Exception($"{code}: {message}")
{
    public string Code { get; } = code;
}
