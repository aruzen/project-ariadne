package core

// Command is the mutation boundary of Core. Concrete commands are values so
// they can be copied before crossing the executor boundary.
type Command interface {
	isCommand()
}

type PaneSpec struct {
	Kind     PaneKind
	Title    string
	Terminal *TerminalInstance
}

type CreateWorkspaceCommand struct {
	Name string
}

type CreateWindowCommand struct {
	WorkspaceID WorkspaceID
	Name        string
}

// CreatePaneCommand creates the first Pane in an empty Window. Further Panes
// are created with SplitPaneCommand so their position is unambiguous.
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

// SetFocusCommand changes only per-frontend ephemeral state. It does not
// increment Revision and is never included in a persistent Snapshot.
type SetFocusCommand struct {
	FrontendID FrontendID
	PaneID     PaneID
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

// BeginTerminalStopCommand makes a user-requested stop visible before the
// daemon waits for the process to exit.
type BeginTerminalStopCommand struct {
	PaneID PaneID
}

func (CreateWorkspaceCommand) isCommand()        {}
func (CreateWindowCommand) isCommand()           {}
func (CreatePaneCommand) isCommand()             {}
func (SplitPaneCommand) isCommand()              {}
func (MovePaneCommand) isCommand()               {}
func (ClosePaneCommand) isCommand()              {}
func (SetFocusCommand) isCommand()               {}
func (RecordTerminalExitCommand) isCommand()     {}
func (ForgetTerminalSessionCommand) isCommand()  {}
func (ActivateTerminalCommand) isCommand()       {}
func (FailTerminalStartCommand) isCommand()      {}
func (PrepareTerminalRestartCommand) isCommand() {}
func (BeginTerminalStopCommand) isCommand()      {}

type CreateWorkspaceResult struct {
	Workspace Workspace `json:"workspace"`
}

type CreateWindowResult struct {
	Window Window `json:"window"`
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

type SetFocusResult struct {
	Focus FrontendState `json:"focus"`
}

type TerminalResult struct {
	Pane Pane `json:"pane"`
}

func cloneCommand(command Command) (Command, error) {
	switch value := command.(type) {
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
	case SetFocusCommand:
		return value, nil
	case *SetFocusCommand:
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
	case BeginTerminalStopCommand:
		return value, nil
	case *BeginTerminalStopCommand:
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
	return spec
}
