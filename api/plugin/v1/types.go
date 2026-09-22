// Package v1 defines Ariadne's external plugin wire contract. It has no dependency
// on Ariadne's internal Core or frontend types.
package v1

import "encoding/json"

const Version = 1

type Capability string

const (
	CoreRead          Capability = "core.read"
	CoreEvents        Capability = "core.events"
	LayoutWrite       Capability = "layout.write"
	TerminalLifecycle Capability = "terminal.lifecycle"
	PTYObserve        Capability = "pty.observe"
	PTYInput          Capability = "pty.input"
	ClipboardRead     Capability = "clipboard.read"
	ClipboardWrite    Capability = "clipboard.write"
	FrontendInteract  Capability = "frontend.interact"
	FrontendEditor    Capability = "frontend.editor"
	FrontendNavigate  Capability = "frontend.navigate"
	ProcessInspect    Capability = "process.inspect"
	LabelWrite        Capability = "label.write"
	AttentionWrite    Capability = "attention.write"
	ToolStateWrite    Capability = "tool.state.write"
)

func ValidCapability(c Capability) bool {
	switch c {
	case CoreRead, CoreEvents, LayoutWrite, TerminalLifecycle, PTYObserve, PTYInput, ClipboardRead, ClipboardWrite, FrontendInteract, FrontendEditor, FrontendNavigate, ProcessInspect, LabelWrite, AttentionWrite, ToolStateWrite:
		return true
	}
	return false
}

type Scope struct {
	Kind string   `json:"kind"`
	IDs  []uint64 `json:"ids,omitempty"`
}
type Grant struct {
	Capability Capability `json:"capability"`
	Scope      Scope      `json:"scope"`
}
type Entrypoint struct {
	Path string   `json:"path"`
	Args []string `json:"args,omitempty"`
}
type Declaration struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}
type Manifest struct {
	ID           string                `json:"id"`
	Version      string                `json:"version"`
	APIVersion   int                   `json:"api_version"`
	Runtime      string                `json:"runtime"`
	Entrypoints  map[string]Entrypoint `json:"entrypoints"`
	Capabilities []Capability          `json:"capabilities,omitempty"`
	Commands     []Declaration         `json:"commands,omitempty"`
	Tools        []Declaration         `json:"tools,omitempty"`
	Widgets      []Declaration         `json:"widgets,omitempty"`
}

// Context is issued by the host; only its opaque token may be used on host APIs.
// IDs describe the captured invocation, never a plugin-selected focus.
type Context struct {
	PluginID    string `json:"plugin_id"`
	Token       string `json:"token"`
	FrontendID  uint64 `json:"frontend_id"`
	WorkspaceID uint64 `json:"workspace_id"`
	WindowID    uint64 `json:"window_id"`
	PaneID      uint64 `json:"pane_id"`
	TerminalID  uint64 `json:"terminal_id,omitempty"`
}
type Initialize struct {
	APIVersion    int     `json:"api_version"`
	ID            string  `json:"id"`
	Generation    uint64  `json:"generation"`
	Grants        []Grant `json:"grants"`
	DataDirectory string  `json:"data_directory"`
}
type InitializeResult struct {
	APIVersion int `json:"api_version"`
}
type Command struct {
	Name    string   `json:"name"`
	Args    []string `json:"args"`
	Context Context  `json:"context"`
}
type CommandResult struct {
	Text string          `json:"text,omitempty"`
	JSON json.RawMessage `json:"json,omitempty"`
}
type APIRequest struct {
	Context string          `json:"context,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}
type Resource struct {
	Kind string `json:"kind"`
	ID   uint64 `json:"id"`
}
type Color struct {
	R uint8 `json:"r"`
	G uint8 `json:"g"`
	B uint8 `json:"b"`
}
type Style struct {
	Foreground        Color `json:"foreground"`
	Background        Color `json:"background"`
	Bold              bool  `json:"bold,omitempty"`
	Italic            bool  `json:"italic,omitempty"`
	Underline         bool  `json:"underline,omitempty"`
	Strikethrough     bool  `json:"strikethrough,omitempty"`
	Faint             bool  `json:"faint,omitempty"`
	Blink             bool  `json:"blink,omitempty"`
	UnderlineStyle    uint8 `json:"underline_style,omitempty"`
	UnderlineColor    Color `json:"underline_color,omitempty"`
	HasUnderlineColor bool  `json:"has_underline_color,omitempty"`
}
type Cell struct {
	Text  string `json:"text"`
	Width uint8  `json:"width"`
	Style Style  `json:"style"`
}
type Cursor struct {
	X       int   `json:"x"`
	Y       int   `json:"y"`
	Visible bool  `json:"visible"`
	Shape   uint8 `json:"shape,omitempty"`
}
type View struct {
	RuntimeGeneration uint64          `json:"runtime_generation,omitempty"`
	ID                string          `json:"id"`
	Generation        uint64          `json:"generation"`
	PaneID            uint64          `json:"pane_id"`
	Type              string          `json:"type"`
	Instance          string          `json:"instance"`
	Width             int             `json:"width"`
	Height            int             `json:"height"`
	Context           Context         `json:"context"`
	StateVersion      uint32          `json:"state_version"`
	StateGeneration   uint64          `json:"state_generation"`
	State             json.RawMessage `json:"state"`
}
type Frame struct {
	RuntimeGeneration uint64 `json:"runtime_generation,omitempty"`
	ViewID            string `json:"view_id"`
	Generation        uint64 `json:"generation"`
	Width             int    `json:"width"`
	Height            int    `json:"height"`
	Cells             []Cell `json:"cells"`
	Cursor            Cursor `json:"cursor"`
}
type Input struct {
	View  View   `json:"view"`
	Data  []byte `json:"data,omitempty"`
	Mouse *Mouse `json:"mouse,omitempty"`
	Paste bool   `json:"paste,omitempty"`
}
type Mouse struct {
	X      int  `json:"x"`
	Y      int  `json:"y"`
	Button int  `json:"button"`
	Action int  `json:"action"`
	Wheel  int  `json:"wheel"`
	Shift  bool `json:"shift"`
	Alt    bool `json:"alt"`
	Ctrl   bool `json:"ctrl"`
}
type Widget struct {
	Name    string  `json:"name"`
	Context Context `json:"context"`
}
type WidgetResult struct {
	Text string `json:"text"`
}
type Interaction struct {
	Kind    string `json:"kind"`
	Message string `json:"message,omitempty"`
	Text    string `json:"text,omitempty"`
}
type InteractionResult struct {
	Text      string `json:"text,omitempty"`
	Confirmed bool   `json:"confirmed,omitempty"`
}

type FrontendNavigateParams struct {
	FrontendID uint64 `json:"frontend_id"`
	PaneID     uint64 `json:"pane_id"`
}

type TerminalProcessParams struct {
	PaneID uint64 `json:"pane_id"`
}

type TerminalProcessResult struct {
	TerminalID uint64 `json:"terminal_id"`
	PID        uint64 `json:"pid"`
}
type InteractionStarted struct {
	ID string `json:"id"`
}
type InteractionCompleted struct {
	ID     string             `json:"id"`
	Result *InteractionResult `json:"result,omitempty"`
	Error  string             `json:"error,omitempty"`
}

// Read data is deliberately opaque JSON with a versioned schema documented in
// docs/plugin-protocol-v1.md, rather than exported internal Go domain structs.
type Snapshot struct {
	Revision      uint64            `json:"revision"`
	Workspaces    []json.RawMessage `json:"workspaces"`
	Windows       []json.RawMessage `json:"windows"`
	Panes         []json.RawMessage `json:"panes"`
	Labels        []json.RawMessage `json:"labels"`
	Attentions    []json.RawMessage `json:"attentions"`
	ToolInstances []json.RawMessage `json:"tool_instances"`
}
type Event struct {
	Context  string   `json:"context,omitempty"`
	Kind     string   `json:"kind"`
	Snapshot Snapshot `json:"snapshot"`
}
type TerminalEvent struct {
	Context    string          `json:"context,omitempty"`
	Kind       string          `json:"kind"`
	PaneID     uint64          `json:"pane_id"`
	TerminalID uint64          `json:"terminal_id"`
	Sequence   uint64          `json:"sequence"`
	Data       []byte          `json:"data,omitempty"`
	Exit       json.RawMessage `json:"exit,omitempty"`
}

// Management is transported over the existing daemon/frontend streammux link.
type ManageRequest struct {
	RequestID string         `json:"request_id,omitempty"`
	Editor    *EditorRequest `json:"editor,omitempty"`
	Action    string         `json:"action"`
	ID        string         `json:"id,omitempty"`
	Directory string         `json:"directory,omitempty"`
	Purge     bool           `json:"purge,omitempty"`
	Grant     *Grant         `json:"grant,omitempty"`
	Command   string         `json:"command,omitempty"`
	Args      []string       `json:"args,omitempty"`
	PaneID    uint64         `json:"pane_id,omitempty"`
	View      *View          `json:"view,omitempty"`
	Input     *Input         `json:"input,omitempty"`
	Widget    string         `json:"widget,omitempty"`
}
type Status struct {
	Manifest   Manifest `json:"manifest"`
	Enabled    bool     `json:"enabled"`
	Installed  bool     `json:"installed"`
	Grants     []Grant  `json:"grants"`
	Running    bool     `json:"running"`
	Generation uint64   `json:"generation"`
	Error      string   `json:"error,omitempty"`
}
type ManageResult struct {
	Editor        *EditorResult  `json:"editor,omitempty"`
	Plugins       []Status       `json:"plugins,omitempty"`
	RegistryError string         `json:"registry_error,omitempty"`
	Command       *CommandResult `json:"command,omitempty"`
	Frame         *Frame         `json:"frame,omitempty"`
	Widget        *WidgetResult  `json:"widget,omitempty"`
}

type InteractionRequest struct {
	ID          string      `json:"id"`
	Context     Context     `json:"context"`
	Interaction Interaction `json:"interaction"`
}
type InteractionResponse struct {
	Result InteractionResult `json:"result"`
	Error  string            `json:"error,omitempty"`
}
type EditorRequest struct {
	Argv []string `json:"argv"`
	CWD  string   `json:"cwd"`
	Env  []string `json:"env"`
	Text string   `json:"text"`
}
type EditorResult struct {
	PaneID     uint64 `json:"pane_id"`
	TerminalID uint64 `json:"terminal_id"`
	Text       string `json:"text,omitempty"`
}
