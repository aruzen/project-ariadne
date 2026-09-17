package core

import "time"

// Command is the mutation boundary of Core. Concrete commands are values so
// they can be copied before crossing the executor boundary.
type Command interface {
	isCommand()
}

type PaneSpec struct {
	Kind         PaneKind
	Title        string
	Presentation PanePresentation
	Terminal     *TerminalInstance
	Tool         *ToolInstance
}

type UpdateToolStateCommand struct {
	Descriptor         ToolDescriptor
	ExpectedGeneration uint64
	StateVersion       uint32
	State              []byte
}

type RaiseAttentionCommand struct {
	PaneID     PaneID
	Source     string
	Key        string
	Class      AttentionClass
	Severity   AttentionSeverity
	Message    string
	OccurredAt time.Time
}

type AcknowledgeAttentionCommand struct {
	ID uint64
	At time.Time
}

type RemoveAttentionsBySourceCommand struct{ Source string }

type CreateWorkspaceCommand struct {
	Name string
}

type CreateWindowCommand struct {
	WorkspaceID WorkspaceID
	Name        string
}

type RenameWorkspaceCommand struct {
	WorkspaceID WorkspaceID
	Name        string
}

type DeleteWorkspaceCommand struct {
	WorkspaceID WorkspaceID
}

type RenameWindowCommand struct {
	WindowID WindowID
	Name     string
}

type DeleteWindowCommand struct {
	WindowID WindowID
}

// CreatePaneCommand creates the first Pane in an empty Window. Further Panes
// are created with SplitPaneCommand so their position is unambiguous.
// CreateTransientPaneCommand creates an editor directly in stash without changing layout.
type CreateTransientPaneCommand struct {
	WindowID WindowID
	Launch   LaunchSpec
}

func (CreateTransientPaneCommand) isCommand() {}

type CreatePaneCommand struct {
	WindowID WindowID
	Pane     PaneSpec
}

type SplitPaneCommand struct {
	TargetPaneID PaneID
	Direction    SplitDirection
	Pane         PaneSpec
}

// MovePaneCommand inserts a Pane after TargetPaneID. TargetPaneID may be zero
// only when the destination Window is empty.
type MovePaneCommand struct {
	PaneID        PaneID
	DestinationID WindowID
	TargetPaneID  PaneID
	Direction     SplitDirection
}

type ClosePaneCommand struct {
	PaneID PaneID
}

type ResizeSplitCommand struct {
	SplitID SplitID
	Weights []uint32
}

type StashPaneCommand struct {
	PaneID PaneID
}

// RestorePaneCommand uses the saved placement when DestinationWindowID is
// zero. If it is no longer usable, FrontendID selects the fallback Window.
type RestorePaneCommand struct {
	FrontendID          FrontendID
	PaneID              PaneID
	DestinationWindowID WindowID
	TargetPaneID        PaneID
	Direction           SplitDirection
}

type StashWindowCommand struct {
	WindowID WindowID
}

// RestoreWindowCommand restores to the original Workspace unless WorkspaceID
// explicitly selects another one or the original no longer exists.
type RestoreWindowCommand struct {
	FrontendID  FrontendID
	WindowID    WindowID
	WorkspaceID WorkspaceID
}

// SetFocusCommand changes only per-frontend ephemeral state. It does not
// increment Revision and is never included in a persistent Snapshot.
type SetFocusCommand struct {
	FrontendID FrontendID
	PaneID     PaneID
}

// SelectWindowCommand changes only per-frontend ephemeral state.
type SelectWindowCommand struct {
	FrontendID FrontendID
	WindowID   WindowID
}

// RecordTerminalExitCommand is emitted by the daemon's PTY Manager observer
// for abnormal exits that remain visible in Core.
type RecordTerminalExitCommand struct {
	TerminalID       TerminalID
	State            TerminalState
	Exit             TerminalExit
	HistoryAvailable bool
}

// ForgetTerminalSessionCommand clears a runtime StreamID after its retained
// streammux Session is evicted or removed while preserving Pane diagnostics.
type ForgetTerminalSessionCommand struct {
	TerminalID TerminalID
}

// ActivateTerminalCommand binds the runtime StreamID allocated by the PTY
// manager to a Pane previously reserved in TerminalStarting state.
type ActivateTerminalCommand struct {
	PaneID     PaneID
	TerminalID TerminalID
}

// FailTerminalStartCommand records a PTY startup failure without persisting
// environment variables or backend-specific error values.
type FailTerminalStartCommand struct {
	PaneID  PaneID
	Message string
}

// PrepareTerminalRestartCommand transitions a placeholder or exited Terminal
// back to its I/O-free reservation state before the daemon starts a process.
type PrepareTerminalRestartCommand struct {
	PaneID PaneID
}

// PrepareTerminalRunCommand assigns a new launch specification to an inactive
// Terminal Pane and reserves it before daemon I/O begins.
type PrepareTerminalRunCommand struct {
	PaneID PaneID
	Launch LaunchSpec
}

// BeginTerminalStopCommand makes a user-requested stop visible before the
// daemon waits for the process to exit.
type BeginTerminalStopCommand struct {
	PaneID PaneID
}

type SetLabelCommand struct {
	Label Label
}

type RemoveLabelCommand struct {
	TargetKind LabelTargetKind
	TargetID   uint64
	Source     string
	Name       string
}

type RemoveLabelsBySourceCommand struct {
	Source string
}

func (CreateWorkspaceCommand) isCommand()          {}
func (CreateWindowCommand) isCommand()             {}
func (RenameWorkspaceCommand) isCommand()          {}
func (DeleteWorkspaceCommand) isCommand()          {}
func (RenameWindowCommand) isCommand()             {}
func (DeleteWindowCommand) isCommand()             {}
func (CreatePaneCommand) isCommand()               {}
func (SplitPaneCommand) isCommand()                {}
func (MovePaneCommand) isCommand()                 {}
func (ClosePaneCommand) isCommand()                {}
func (ResizeSplitCommand) isCommand()              {}
func (StashPaneCommand) isCommand()                {}
func (RestorePaneCommand) isCommand()              {}
func (StashWindowCommand) isCommand()              {}
func (RestoreWindowCommand) isCommand()            {}
func (SetFocusCommand) isCommand()                 {}
func (SelectWindowCommand) isCommand()             {}
func (RecordTerminalExitCommand) isCommand()       {}
func (ForgetTerminalSessionCommand) isCommand()    {}
func (ActivateTerminalCommand) isCommand()         {}
func (FailTerminalStartCommand) isCommand()        {}
func (PrepareTerminalRestartCommand) isCommand()   {}
func (PrepareTerminalRunCommand) isCommand()       {}
func (BeginTerminalStopCommand) isCommand()        {}
func (SetLabelCommand) isCommand()                 {}
func (RemoveLabelCommand) isCommand()              {}
func (RemoveLabelsBySourceCommand) isCommand()     {}
func (UpdateToolStateCommand) isCommand()          {}
func (RaiseAttentionCommand) isCommand()           {}
func (AcknowledgeAttentionCommand) isCommand()     {}
func (RemoveAttentionsBySourceCommand) isCommand() {}

type CreateWorkspaceResult struct {
	Workspace Workspace `json:"workspace"`
}

type CreateWindowResult struct {
	Window Window `json:"window"`
}

type WorkspaceResult struct {
	Workspace Workspace `json:"workspace"`
}

type DeleteWorkspaceResult struct {
	Workspace     Workspace `json:"workspace"`
	RemovedLabels []Label   `json:"removed_labels,omitempty"`
}

type WindowResult struct {
	Window Window `json:"window"`
}

type DeleteWindowResult struct {
	Window        Window    `json:"window"`
	Workspace     Workspace `json:"workspace"`
	RemovedLabels []Label   `json:"removed_labels,omitempty"`
}

type CreatePaneResult struct {
	Pane   Pane   `json:"pane"`
	Window Window `json:"window"`
}

type MovePaneResult struct {
	Pane              Pane   `json:"pane"`
	SourceWindow      Window `json:"source_window"`
	DestinationWindow Window `json:"destination_window"`
}

type ClosePaneResult struct {
	Pane   Pane   `json:"pane"`
	Window Window `json:"window"`
}

type ResizeSplitResult struct {
	Window Window `json:"window"`
}

type StashPaneResult struct {
	Pane    Pane        `json:"pane"`
	Window  Window      `json:"window"`
	Stashed StashedPane `json:"stashed"`
}

type RestorePaneResult struct {
	Pane    Pane        `json:"pane"`
	Window  Window      `json:"window"`
	Stashed StashedPane `json:"stashed"`
}

type StashWindowResult struct {
	Window    Window        `json:"window"`
	Workspace Workspace     `json:"workspace"`
	Stashed   StashedWindow `json:"stashed"`
}

type RestoreWindowResult struct {
	Window    Window        `json:"window"`
	Workspace Workspace     `json:"workspace"`
	Stashed   StashedWindow `json:"stashed"`
}

type SetFocusResult struct {
	Focus FrontendState `json:"focus"`
}

type TerminalResult struct {
	Pane Pane `json:"pane"`
}

type LabelResult struct {
	Label   Label `json:"label"`
	Changed bool  `json:"changed"`
}

type RemoveLabelsResult struct {
	Labels []Label `json:"labels"`
}

type ToolStateResult struct {
	Tool ToolInstance `json:"tool"`
}

type AttentionResult struct {
	Attention Attention `json:"attention"`
	Changed   bool      `json:"changed"`
}

type RemoveAttentionsResult struct {
	Attentions []Attention `json:"attentions"`
}

func cloneCommand(command Command) (Command, error) {
	switch value := command.(type) {
	case CreateTransientPaneCommand:
		value.Launch = cloneLaunch(value.Launch)
		return value, nil
	case CreateWorkspaceCommand:
		return value, nil
	case *CreateWorkspaceCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case CreateWindowCommand:
		return value, nil
	case *CreateWindowCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case RenameWorkspaceCommand:
		return value, nil
	case *RenameWorkspaceCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case DeleteWorkspaceCommand:
		return value, nil
	case *DeleteWorkspaceCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case RenameWindowCommand:
		return value, nil
	case *RenameWindowCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case DeleteWindowCommand:
		return value, nil
	case *DeleteWindowCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case CreatePaneCommand:
		value.Pane = clonePaneSpec(value.Pane)
		return value, nil
	case *CreatePaneCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		copy := *value
		copy.Pane = clonePaneSpec(copy.Pane)
		return copy, nil
	case SplitPaneCommand:
		value.Pane = clonePaneSpec(value.Pane)
		return value, nil
	case *SplitPaneCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		copy := *value
		copy.Pane = clonePaneSpec(copy.Pane)
		return copy, nil
	case MovePaneCommand:
		return value, nil
	case *MovePaneCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case ClosePaneCommand:
		return value, nil
	case *ClosePaneCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case ResizeSplitCommand:
		value.Weights = append([]uint32(nil), value.Weights...)
		return value, nil
	case *ResizeSplitCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		copy := *value
		copy.Weights = append([]uint32(nil), value.Weights...)
		return copy, nil
	case StashPaneCommand:
		return value, nil
	case *StashPaneCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case RestorePaneCommand:
		return value, nil
	case *RestorePaneCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case StashWindowCommand:
		return value, nil
	case *StashWindowCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case RestoreWindowCommand:
		return value, nil
	case *RestoreWindowCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case SetFocusCommand:
		return value, nil
	case *SetFocusCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case SelectWindowCommand:
		return value, nil
	case *SelectWindowCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case RecordTerminalExitCommand:
		return value, nil
	case *RecordTerminalExitCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case ForgetTerminalSessionCommand:
		return value, nil
	case *ForgetTerminalSessionCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case ActivateTerminalCommand:
		return value, nil
	case *ActivateTerminalCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case FailTerminalStartCommand:
		return value, nil
	case *FailTerminalStartCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case PrepareTerminalRestartCommand:
		return value, nil
	case *PrepareTerminalRestartCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case PrepareTerminalRunCommand:
		value.Launch = cloneLaunch(value.Launch)
		return value, nil
	case *PrepareTerminalRunCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		copy := *value
		copy.Launch = cloneLaunch(copy.Launch)
		return copy, nil
	case BeginTerminalStopCommand:
		return value, nil
	case *BeginTerminalStopCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case SetLabelCommand:
		return value, nil
	case *SetLabelCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case RemoveLabelCommand:
		return value, nil
	case *RemoveLabelCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case RemoveLabelsBySourceCommand:
		return value, nil
	case *RemoveLabelsBySourceCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case UpdateToolStateCommand:
		value.State = append([]byte(nil), value.State...)
		return value, nil
	case *UpdateToolStateCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		copy := *value
		copy.State = append([]byte(nil), value.State...)
		return copy, nil
	case RaiseAttentionCommand:
		return value, nil
	case *RaiseAttentionCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case AcknowledgeAttentionCommand:
		return value, nil
	case *AcknowledgeAttentionCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	case RemoveAttentionsBySourceCommand:
		return value, nil
	case *RemoveAttentionsBySourceCommand:
		if value == nil {
			return nil, ErrInvalidCommand
		}
		return *value, nil
	default:
		return nil, ErrInvalidCommand
	}
}

func clonePaneSpec(spec PaneSpec) PaneSpec {
	if spec.Terminal != nil {
		terminal := cloneTerminal(*spec.Terminal)
		spec.Terminal = &terminal
	}
	if spec.Tool != nil {
		tool := cloneToolInstance(*spec.Tool)
		spec.Tool = &tool
	}
	return spec
}
