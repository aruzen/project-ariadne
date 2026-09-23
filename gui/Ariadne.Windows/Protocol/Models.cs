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
    public GuiOptions Gui { get; set; } = new();
}

public sealed class GuiOptions
{
    public string ConfigPath { get; set; } = "";
    public string FontFamily { get; set; } = "Cascadia Mono";
    public double FontSize { get; set; } = 14;
    public bool SoftwareRendering { get; set; }
    public string Background { get; set; } = "#0c0f13";
    public string Foreground { get; set; } = "#e8eaed";
    public string Selection { get; set; } = "#264c6b";
    public string Accent { get; set; } = "#5191ff";
    public List<string> ColorTable { get; set; } =
    [
        "#000000", "#cc2222", "#22aa22", "#22aaaa", "#2277cc", "#aa22aa", "#aaaa22", "#cccccc",
        "#666666", "#ff6666", "#66dd66", "#66dddd", "#66aaff", "#dd66dd", "#dddd66", "#ffffff",
    ];
    public List<string> Shell { get; set; } = ["powershell.exe", "-NoLogo"];
    public List<string> Editor { get; set; } = ["notepad.exe"];
    public Dictionary<string, string> Keybindings { get; set; } = new(StringComparer.Ordinal);
    public Dictionary<string, string> DefaultKeybindings { get; set; } = new(StringComparer.Ordinal);
}

public sealed class PluginManageRequest
{
    public string? RequestId { get; set; }
    public string Action { get; set; } = "";
    public string? Id { get; set; }
    public PluginView? View { get; set; }
    public PluginInput? Input { get; set; }
    public PluginEditorRequest? Editor { get; set; }
    public ulong PaneId { get; set; }
}

public sealed class PluginManageResult
{
    public PluginFrame? Frame { get; set; }
    public PluginEditorResult? Editor { get; set; }
}

public sealed class PluginEditorRequest
{
    public List<string> Argv { get; set; } = [];
    public string Cwd { get; set; } = "";
    public List<string> Env { get; set; } = [];
    public string Text { get; set; } = "";
}

public sealed class PluginEditorResult
{
    public ulong PaneId { get; set; }
    public ulong TerminalId { get; set; }
    public string Text { get; set; } = "";
}

public sealed class PluginContext
{
    public string PluginId { get; set; } = "";
    public ulong PaneId { get; set; }
}

public sealed class PluginInteraction
{
    public string Kind { get; set; } = "";
    public string Message { get; set; } = "";
    public string Text { get; set; } = "";
}

public sealed class PluginInteractionRequest
{
    public string Id { get; set; } = "";
    public PluginContext Context { get; set; } = new();
    public PluginInteraction Interaction { get; set; } = new();
}

public sealed class PluginInteractionResult
{
    public string Text { get; set; } = "";
    public bool Confirmed { get; set; }
}

public sealed class PluginView
{
    public ulong RuntimeGeneration { get; set; }
    public string Id { get; set; } = "";
    public ulong Generation { get; set; }
    public ulong PaneId { get; set; }
    public string Type { get; set; } = "";
    public string Instance { get; set; } = "";
    public int Width { get; set; }
    public int Height { get; set; }
}

public sealed class PluginFrame
{
    public ulong RuntimeGeneration { get; set; }
    public string ViewId { get; set; } = "";
    public ulong Generation { get; set; }
    public int Width { get; set; }
    public int Height { get; set; }
    public List<PluginCell> Cells { get; set; } = [];
    public PluginCursor Cursor { get; set; } = new();
}

public sealed class PluginCell
{
    public string Text { get; set; } = "";
    public byte Width { get; set; }
    public PluginStyle Style { get; set; } = new();
}

public sealed class PluginStyle
{
    public PluginColor Foreground { get; set; } = new();
    public PluginColor Background { get; set; } = new();
    public bool Bold { get; set; }
    public bool Italic { get; set; }
    public bool Underline { get; set; }
    public bool Strikethrough { get; set; }
    public bool Faint { get; set; }
    public bool Blink { get; set; }
    public byte UnderlineStyle { get; set; }
    public PluginColor UnderlineColor { get; set; } = new();
    public bool HasUnderlineColor { get; set; }
}

public sealed class PluginColor
{
    public byte R { get; set; }
    public byte G { get; set; }
    public byte B { get; set; }
}

public sealed class PluginCursor
{
    public int X { get; set; }
    public int Y { get; set; }
    public bool Visible { get; set; }
    public byte Shape { get; set; }
}

public sealed class PluginInput
{
    public PluginView View { get; set; } = new();
    public byte[]? Data { get; set; }
    public PluginMouse? Mouse { get; set; }
    public bool Paste { get; set; }
}

public sealed class PluginMouse
{
    public int X { get; set; }
    public int Y { get; set; }
    public int Button { get; set; }
    public int Action { get; set; }
    public int Wheel { get; set; }
    public bool Shift { get; set; }
    public bool Alt { get; set; }
    public bool Ctrl { get; set; }
}

public sealed class SetFocusResult
{
    public FrontendState Focus { get; set; } = new();
}

public sealed class CreateWorkspaceResult
{
    public WorkspaceModel Workspace { get; set; } = new();
}

public sealed class CreateWindowResult
{
    public WindowModel Window { get; set; } = new();
}

public sealed class WorkspaceOperationResult
{
    public WorkspaceModel Workspace { get; set; } = new();
}

public sealed class WindowOperationResult
{
    public WindowModel Window { get; set; } = new();
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
