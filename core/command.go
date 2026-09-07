package core

// Command is the mutation boundary of Core. Concrete commands are values so
// they can be copied before crossing the executor boundary.
type Command interface {
	isCommand()
}

type PaneSpec struct {
	Kind       PaneKind
	Title      string
	TerminalID *TerminalID
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

func (CreateWorkspaceCommand) isCommand() {}
func (CreateWindowCommand) isCommand()    {}
func (CreatePaneCommand) isCommand()      {}
func (SplitPaneCommand) isCommand()       {}
func (MovePaneCommand) isCommand()        {}
func (ClosePaneCommand) isCommand()       {}
func (SetFocusCommand) isCommand()        {}

type CreateWorkspaceResult struct {
	Workspace Workspace
}

type CreateWindowResult struct {
	Window Window
}

type CreatePaneResult struct {
	Pane   Pane
	Window Window
}

type MovePaneResult struct {
	Pane              Pane
	SourceWindow      Window
	DestinationWindow Window
}

type ClosePaneResult struct {
	Pane   Pane
	Window Window
}

type SetFocusResult struct {
	Focus FrontendState
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
	default:
		return nil, ErrInvalidCommand
	}
}

func clonePaneSpec(spec PaneSpec) PaneSpec {
	if spec.TerminalID != nil {
		terminalID := *spec.TerminalID
		spec.TerminalID = &terminalID
	}
	return spec
}
