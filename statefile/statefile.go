// Package statefile persists Ariadne Core domain state as versioned JSON.
// Runtime Terminal IDs, PTY history, environment variables, and frontend
// state are deliberately outside its schema.
package statefile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/aruzen/ariadne/core"
)

const (
	CurrentVersion  = 1
	DefaultDebounce = 100 * time.Millisecond
	DefaultMaxBytes = 16 << 20
)

var (
	ErrInvalidOptions     = errors.New("statefile: invalid options")
	ErrInvalidData        = errors.New("statefile: invalid data")
	ErrUnsupportedVersion = errors.New("statefile: unsupported version")
	ErrTooLarge           = errors.New("statefile: file too large")
	ErrClosed             = errors.New("statefile: closed")
)

type LoadStatus string

const (
	LoadCreated   LoadStatus = "created"
	LoadExisting  LoadStatus = "existing"
	LoadRecovered LoadStatus = "recovered"
)

type LoadResult struct {
	Snapshot        core.Snapshot
	Status          LoadStatus
	QuarantinedPath string
	RecoveryCause   error
}

type Options struct {
	Debounce time.Duration
	MaxBytes int64
	Now      func() time.Time
}

func DefaultOptions() Options {
	return Options{Debounce: DefaultDebounce, MaxBytes: DefaultMaxBytes, Now: time.Now}
}

func (options Options) normalized() (Options, error) {
	if options.Debounce < 0 || options.MaxBytes < 0 {
		return Options{}, ErrInvalidOptions
	}
	if options.Debounce == 0 {
		options.Debounce = DefaultDebounce
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = DefaultMaxBytes
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return options, nil
}

type document struct {
	Version int                `json:"version"`
	State   persistentSnapshot `json:"state"`
}

type persistentSnapshot struct {
	NextWorkspaceID core.WorkspaceID      `json:"next_workspace_id"`
	NextWindowID    core.WindowID         `json:"next_window_id"`
	NextPaneID      core.PaneID           `json:"next_pane_id"`
	Workspaces      []persistentWorkspace `json:"workspaces"`
	Windows         []persistentWindow    `json:"windows"`
	Panes           []persistentPane      `json:"panes"`
}

type persistentWorkspace struct {
	ID        core.WorkspaceID `json:"id"`
	Name      string           `json:"name"`
	WindowIDs []core.WindowID  `json:"window_ids"`
}

type persistentWindow struct {
	ID          core.WindowID     `json:"id"`
	WorkspaceID core.WorkspaceID  `json:"workspace_id"`
	Name        string            `json:"name"`
	Layout      *persistentLayout `json:"layout,omitempty"`
}

type persistentLayout struct {
	Kind      core.LayoutNodeKind `json:"kind"`
	PaneID    core.PaneID         `json:"pane_id,omitempty"`
	Direction core.SplitDirection `json:"direction,omitempty"`
	Children  []persistentLayout  `json:"children,omitempty"`
	Weights   []uint32            `json:"weights,omitempty"`
}

type persistentPane struct {
	ID       core.PaneID         `json:"id"`
	WindowID core.WindowID       `json:"window_id"`
	Kind     core.PaneKind       `json:"kind"`
	Title    string              `json:"title,omitempty"`
	Terminal *persistentTerminal `json:"terminal,omitempty"`
}

type persistentTerminal struct {
	State  core.TerminalState `json:"state"`
	Launch core.LaunchSpec    `json:"launch"`
	Exit   *core.TerminalExit `json:"exit,omitempty"`
}

// Encode validates and serializes a Snapshot, removing all runtime-only data.
func Encode(snapshot core.Snapshot) ([]byte, error) {
	if err := core.ValidateSnapshot(snapshot); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidData, err)
	}
	document := document{Version: CurrentVersion, State: persistentFromSnapshot(snapshot)}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("statefile: encode: %w", err)
	}
	return append(data, '\n'), nil
}

// Decode accepts exactly one current-version JSON document.
func Decode(data []byte, maxBytes int64) (core.Snapshot, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if int64(len(data)) > maxBytes {
		return core.Snapshot{}, fmt.Errorf("%w: %d bytes", ErrTooLarge, len(data))
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document document
	if err := decoder.Decode(&document); err != nil {
		return core.Snapshot{}, fmt.Errorf("%w: %w", ErrInvalidData, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return core.Snapshot{}, err
	}
	if document.Version != CurrentVersion {
		return core.Snapshot{}, fmt.Errorf("%w: got %d, want %d", ErrUnsupportedVersion, document.Version, CurrentVersion)
	}
	snapshot := snapshotFromPersistent(document.State)
	if err := core.ValidateSnapshot(snapshot); err != nil {
		return core.Snapshot{}, fmt.Errorf("%w: %w", ErrInvalidData, err)
	}
	return snapshot, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: multiple JSON values", ErrInvalidData)
		}
		return fmt.Errorf("%w: %w", ErrInvalidData, err)
	}
	return nil
}

// Load reads path. Missing files are initialized. Invalid or unknown-version
// files are timestamped and quarantined before a default state is written.
func Load(path string, options Options) (LoadResult, error) {
	options, err := options.normalized()
	if err != nil {
		return LoadResult{}, err
	}
	if path == "" {
		return LoadResult{}, fmt.Errorf("%w: empty path", ErrInvalidOptions)
	}
	data, err := readBounded(path, options.MaxBytes)
	if errors.Is(err, fs.ErrNotExist) {
		snapshot := core.DefaultSnapshot()
		if err := writeSnapshotAtomic(path, snapshot, options.MaxBytes); err != nil {
			return LoadResult{}, err
		}
		return LoadResult{Snapshot: snapshot, Status: LoadCreated}, nil
	}
	if err != nil && !errors.Is(err, ErrTooLarge) {
		return LoadResult{}, err
	}
	decodeErr := err
	var snapshot core.Snapshot
	if decodeErr == nil {
		snapshot, decodeErr = Decode(data, options.MaxBytes)
	}
	if decodeErr == nil {
		return LoadResult{Snapshot: snapshot, Status: LoadExisting}, nil
	}
	quarantinedPath, err := quarantine(path, options.Now())
	if err != nil {
		return LoadResult{}, fmt.Errorf("statefile: quarantine: %w", err)
	}
	snapshot = core.DefaultSnapshot()
	if err := writeSnapshotAtomic(path, snapshot, options.MaxBytes); err != nil {
		return LoadResult{Snapshot: snapshot, Status: LoadRecovered, QuarantinedPath: quarantinedPath, RecoveryCause: decodeErr}, err
	}
	return LoadResult{
		Snapshot: snapshot, Status: LoadRecovered, QuarantinedPath: quarantinedPath, RecoveryCause: decodeErr,
	}, nil
}

func readBounded(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return data, fmt.Errorf("%w: exceeds %d bytes", ErrTooLarge, maxBytes)
	}
	return data, nil
}

func persistentFromSnapshot(snapshot core.Snapshot) persistentSnapshot {
	persistent := persistentSnapshot{
		NextWorkspaceID: snapshot.NextWorkspaceID,
		NextWindowID:    snapshot.NextWindowID,
		NextPaneID:      snapshot.NextPaneID,
		Workspaces:      make([]persistentWorkspace, len(snapshot.Workspaces)),
		Windows:         make([]persistentWindow, len(snapshot.Windows)),
		Panes:           make([]persistentPane, len(snapshot.Panes)),
	}
	for index, workspace := range snapshot.Workspaces {
		persistent.Workspaces[index] = persistentWorkspace{
			ID: workspace.ID, Name: workspace.Name, WindowIDs: append([]core.WindowID(nil), workspace.WindowIDs...),
		}
	}
	for index, window := range snapshot.Windows {
		persistent.Windows[index] = persistentWindow{
			ID: window.ID, WorkspaceID: window.WorkspaceID, Name: window.Name, Layout: persistentLayoutFromCore(window.Layout),
		}
	}
	for index, pane := range snapshot.Panes {
		persistentPane := persistentPane{ID: pane.ID, WindowID: pane.WindowID, Kind: pane.Kind, Title: pane.Title}
		if pane.Terminal != nil {
			terminal := persistentTerminal{State: pane.Terminal.State, Launch: cloneLaunch(pane.Terminal.Launch)}
			if pane.Terminal.Exit != nil {
				exit := *pane.Terminal.Exit
				terminal.Exit = &exit
			}
			switch pane.Terminal.State {
			case core.TerminalStarting, core.TerminalRunning, core.TerminalStopping:
				terminal.State = core.TerminalPlaceholder
				terminal.Exit = nil
			}
			persistentPane.Terminal = &terminal
		}
		persistent.Panes[index] = persistentPane
	}
	return persistent
}

func snapshotFromPersistent(persistent persistentSnapshot) core.Snapshot {
	snapshot := core.Snapshot{
		NextWorkspaceID: persistent.NextWorkspaceID,
		NextWindowID:    persistent.NextWindowID,
		NextPaneID:      persistent.NextPaneID,
		Workspaces:      make([]core.Workspace, len(persistent.Workspaces)),
		Windows:         make([]core.Window, len(persistent.Windows)),
		Panes:           make([]core.Pane, len(persistent.Panes)),
	}
	for index, workspace := range persistent.Workspaces {
		snapshot.Workspaces[index] = core.Workspace{
			ID: workspace.ID, Name: workspace.Name, WindowIDs: append([]core.WindowID(nil), workspace.WindowIDs...),
		}
	}
	for index, window := range persistent.Windows {
		snapshot.Windows[index] = core.Window{
			ID: window.ID, WorkspaceID: window.WorkspaceID, Name: window.Name, Layout: coreLayoutFromPersistent(window.Layout),
		}
	}
	for index, pane := range persistent.Panes {
		corePane := core.Pane{ID: pane.ID, WindowID: pane.WindowID, Kind: pane.Kind, Title: pane.Title}
		if pane.Terminal != nil {
			terminal := core.TerminalInstance{State: pane.Terminal.State, Launch: cloneLaunch(pane.Terminal.Launch)}
			if pane.Terminal.Exit != nil {
				exit := *pane.Terminal.Exit
				terminal.Exit = &exit
			}
			corePane.Terminal = &terminal
		}
		snapshot.Panes[index] = corePane
	}
	return snapshot
}

func cloneLaunch(launch core.LaunchSpec) core.LaunchSpec {
	launch.Argv = append([]string(nil), launch.Argv...)
	return launch
}

func persistentLayoutFromCore(layout *core.LayoutNode) *persistentLayout {
	if layout == nil {
		return nil
	}
	persistent := &persistentLayout{
		Kind: layout.Kind, PaneID: layout.PaneID, Direction: layout.Direction,
		Children: make([]persistentLayout, len(layout.Children)), Weights: append([]uint32(nil), layout.Weights...),
	}
	for index := range layout.Children {
		child := persistentLayoutFromCore(&layout.Children[index])
		persistent.Children[index] = *child
	}
	return persistent
}

func coreLayoutFromPersistent(layout *persistentLayout) *core.LayoutNode {
	if layout == nil {
		return nil
	}
	coreLayout := &core.LayoutNode{
		Kind: layout.Kind, PaneID: layout.PaneID, Direction: layout.Direction,
		Children: make([]core.LayoutNode, len(layout.Children)), Weights: append([]uint32(nil), layout.Weights...),
	}
	for index := range layout.Children {
		child := coreLayoutFromPersistent(&layout.Children[index])
		coreLayout.Children[index] = *child
	}
	return coreLayout
}

func writeSnapshotAtomic(path string, snapshot core.Snapshot, maxBytes int64) error {
	data, err := Encode(snapshot)
	if err != nil {
		return err
	}
	if int64(len(data)) > maxBytes {
		return fmt.Errorf("%w: encoded state is %d bytes", ErrTooLarge, len(data))
	}
	return writeAtomic(path, data)
}

func writeAtomic(path string, data []byte) (resultErr error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("statefile: create directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".ariadne-state-*")
	if err != nil {
		return fmt.Errorf("statefile: create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if resultErr != nil {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("statefile: chmod temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("statefile: write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("statefile: sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("statefile: close temporary file: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return fmt.Errorf("statefile: replace state: %w", err)
	}
	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("statefile: sync directory: %w", err)
	}
	return nil
}

func quarantine(path string, now time.Time) (string, error) {
	timestamp := now.UTC().Format("20060102T150405.000000000Z")
	base := path + ".invalid-" + timestamp
	quarantinedPath := base
	for suffix := 1; ; suffix++ {
		_, err := os.Lstat(quarantinedPath)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			return "", err
		}
		quarantinedPath = fmt.Sprintf("%s-%d", base, suffix)
	}
	if err := os.Rename(path, quarantinedPath); err != nil {
		return "", err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return "", err
	}
	return quarantinedPath, nil
}
