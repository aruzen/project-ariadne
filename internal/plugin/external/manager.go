package external

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/core"
)

var ErrPermission = errors.New("plugin: permission denied")
var ErrUnavailable = errors.New("plugin: unavailable")

// Operations handles only the daemon's explicit I/O and frontend APIs. It is
// called after capability/resource checks, with a host-captured context.
type Operations interface {
	PluginOperation(context.Context, string, json.RawMessage, v1.Context) (any, error)
}
type Manager struct {
	reserved      map[string]bool
	ctx           context.Context
	cancel        context.CancelFunc
	engine        *core.Core
	operations    Operations
	root          string
	config        Config
	opMu          sync.Mutex
	mu            sync.Mutex
	records       map[string]record
	sessions      map[string]*session
	failures      map[string]string
	generation    uint64
	registryError error
	closed        bool
	started       bool
	commands      map[uint64]bool
	views         map[string]viewRecord
	contexts      map[string]invocation
	interactions  map[string]interactionSession
}
type viewRecord struct {
	hostID        string
	frontend      uint64
	pane          uint64
	generation    uint64
	plugin        string
	width, height int
}
type invocation struct {
	interaction *atomic.Int64
	context     v1.Context
	plugin      string
	generation  uint64
	ctx         context.Context
	cancel      context.CancelFunc
	origin      invocationOrigin
	view        string
}
type invocationOrigin uint8

const (
	invocationOther invocationOrigin = iota
	invocationCommand
	invocationInput
	invocationRender
	invocationWidget
)

type interactionSession struct {
	plugin               string
	id                   string
	generation, frontend uint64
	view                 string
	cancel               context.CancelFunc
}

func NewManager(parent context.Context, engine *core.Core, root string, configuration Config, operations Operations) (*Manager, error) {
	m, err := NewManagerDeferred(parent, engine, root, configuration, operations)
	if err == nil {
		m.StartEnabled()
	}
	return m, err
}

// NewManagerDeferred lets the daemon publish the manager and register its Core/
// PTY observers before restored plugins may call host APIs during initialize.
func NewManagerDeferred(parent context.Context, engine *core.Core, root string, configuration Config, operations Operations) (*Manager, error) {
	if parent == nil || engine == nil || root == "" {
		return nil, errors.New("plugin: nil manager dependency")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	c, err := configuration.Normalize()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	m := &Manager{ctx: ctx, cancel: cancel, engine: engine, operations: operations, root: root, config: c, sessions: map[string]*session{}, failures: map[string]string{}, commands: map[uint64]bool{}, views: map[string]viewRecord{}, contexts: map[string]invocation{}, interactions: map[string]interactionSession{}}
	m.reserved = map[string]bool{}
	if provider, ok := operations.(interface{ ReservedPluginIDs() []string }); ok {
		for _, id := range provider.ReservedPluginIDs() {
			m.reserved[id] = true
		}
	}
	m.records, m.registryError = loadRegistry(filepath.Join(root, "registry.json"))
	if m.registryError != nil {
		return m, nil
	} // Preserve the corrupt file and stop all automatic starts.
	return m, nil
}
func (m *Manager) StartEnabled() {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.closed || m.registryError != nil || m.started {
		return
	}
	m.started = true
	for id, e := range m.records {
		m.mu.Lock()
		existing := m.sessions[id] != nil
		m.mu.Unlock()
		if e.Enabled && !existing {
			m.start(id)
		}
	}
}
func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (m *Manager) save(entries map[string]record) error {
	if m.registryError != nil {
		return fmt.Errorf("plugin: registry preserved; automatic startup and mutations blocked: %w", m.registryError)
	}
	return saveRegistry(filepath.Join(m.root, "registry.json"), entries)
}
func (m *Manager) cloneRecords() map[string]record {
	result := make(map[string]record, len(m.records))
	for id, e := range m.records {
		e.Grants = append([]v1.Grant(nil), e.Grants...)
		result[id] = e
	}
	return result
}
func (m *Manager) List() v1.ManageResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := v1.ManageResult{}
	if m.registryError != nil {
		result.RegistryError = m.registryError.Error()
	}
	for id, e := range m.records {
		status := v1.Status{Manifest: e.Manifest, Installed: e.Package != "", Enabled: e.Enabled, Grants: append([]v1.Grant(nil), e.Grants...), Error: m.failures[id]}
		if s := m.sessions[id]; s != nil {
			status.Running = s.active.Load()
			status.Generation = s.generation
		}
		result.Plugins = append(result.Plugins, status)
	}
	sort.Slice(result.Plugins, func(i, j int) bool { return result.Plugins[i].Manifest.ID < result.Plugins[j].Manifest.ID })
	return result
}
func (m *Manager) Manage(ctx context.Context, frontend uint64, request v1.ManageRequest) (v1.ManageResult, error) {
	switch request.Action {
	case "fault":
		s, err := m.get(request.ID)
		if err != nil {
			return v1.ManageResult{}, err
		}
		s.peer.Fail(ErrOverflow)
		return v1.ManageResult{}, nil
	case "run":
		return m.runCommand(ctx, frontend, request)
	case "view.open", "render", "input", "view.close":
		return m.view(ctx, frontend, request)
	case "widget":
		return m.widget(ctx, frontend, request)
	case "list", "status":
		r := m.List()
		if request.ID != "" {
			for _, s := range r.Plugins {
				if s.Manifest.ID == request.ID {
					r.Plugins = []v1.Status{s}
					return r, nil
				}
			}
			return r, ErrUnavailable
		}
		return r, nil
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return v1.ManageResult{}, ErrUnavailable
	}
	entries := m.cloneRecords()
	m.mu.Unlock()
	if m.registryError != nil {
		return v1.ManageResult{}, fmt.Errorf("plugin: registry is invalid: %w", m.registryError)
	}
	id := request.ID
	e, exists := entries[id]
	var oldPackage, newPackage string
	switch request.Action {
	case "install", "update":
		dir, err := filepath.Abs(request.Directory)
		if err != nil {
			return v1.ManageResult{}, err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return v1.ManageResult{}, err
		}
		if !info.IsDir() {
			return v1.ManageResult{}, errors.New("plugin: local directory package required")
		}
		manifest, err := ReadManifest(dir)
		if err != nil {
			return v1.ManageResult{}, err
		}
		if id != "" && id != manifest.ID {
			return v1.ManageResult{}, errors.New("plugin: package ID mismatch")
		}
		id = manifest.ID
		if m.reserved[id] {
			return v1.ManageResult{}, errors.New("plugin: ID belongs to a built-in provider")
		}
		e, exists = entries[id]
		if request.Action == "install" && exists && e.Package != "" {
			return v1.ManageResult{}, errors.New("plugin: already installed")
		}
		if request.Action == "update" && (!exists || e.Package == "") {
			return v1.ManageResult{}, ErrUnavailable
		}
		newPackage = id + "-" + randomID()
		target := filepath.Join(m.root, "packages", newPackage)
		if err := copyPackage(dir, target); err != nil {
			_ = os.RemoveAll(target)
			return v1.ManageResult{}, err
		}
		copied, err := ReadManifest(target)
		if err != nil || !reflect.DeepEqual(copied, manifest) {
			_ = os.RemoveAll(target)
			if err == nil {
				err = errors.New("plugin: package changed during install")
			}
			return v1.ManageResult{}, err
		}
		oldPackage = e.Package
		if request.Action == "install" {
			e.Grants = nil
		}
		e.Manifest = manifest
		e.Package = newPackage
		e.Enabled = false
	case "uninstall":
		if !exists && (!request.Purge || !validID(id) || m.reserved[id]) {
			return v1.ManageResult{}, ErrUnavailable
		}
		oldPackage = e.Package
		e.Package = ""
		e.Enabled = false
		delete(entries, id)
	case "enable", "disable":
		if !exists || e.Package == "" {
			return v1.ManageResult{}, ErrUnavailable
		}
		e.Enabled = request.Action == "enable"
	case "restart":
		if !exists || !e.Enabled || e.Package == "" {
			return v1.ManageResult{}, errors.New("plugin: restart requires enabled installed plugin")
		}
	case "grant", "revoke":
		if !exists || e.Package == "" || request.Grant == nil {
			return v1.ManageResult{}, ErrUnavailable
		}
		g := *request.Grant
		if err := ValidateGrant(g); err != nil {
			return v1.ManageResult{}, err
		}
		if request.Action == "grant" {
			requested := false
			for _, c := range e.Manifest.Capabilities {
				if c == g.Capability {
					requested = true
				}
			}
			if !requested {
				return v1.ManageResult{}, errors.New("plugin: capability not requested by manifest")
			}
			found := false
			for _, old := range e.Grants {
				if reflect.DeepEqual(old, g) {
					found = true
				}
			}
			if !found {
				e.Grants = append(e.Grants, g)
			}
		} else {
			grants := make([]v1.Grant, 0, len(e.Grants))
			for _, old := range e.Grants {
				if !reflect.DeepEqual(old, g) {
					grants = append(grants, old)
				}
			}
			e.Grants = grants
		}
	default:
		return v1.ManageResult{}, errors.New("plugin: unknown management action")
	}
	if request.Action != "uninstall" {
		entries[id] = e
	}
	if err := m.save(entries); err != nil {
		if newPackage != "" {
			_ = os.RemoveAll(filepath.Join(m.root, "packages", newPackage))
		}
		return v1.ManageResult{}, err
	}
	// Invalidate the previous generation before publishing changed grants.
	m.stop(id, context.Canceled)
	m.mu.Lock()
	m.records = entries
	delete(m.failures, id)
	m.mu.Unlock()
	if oldPackage != "" {
		if err := os.RemoveAll(filepath.Join(m.root, "packages", oldPackage)); err != nil {
			return m.List(), err
		}
	}
	if request.Purge && request.Action == "uninstall" {
		if err := m.purge(ctx, id); err != nil {
			return m.List(), err
		}
	}
	if e.Enabled {
		m.start(id)
	}
	return m.List(), nil
}
func (m *Manager) Close(ctx context.Context) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	m.closed = true
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.stop(id, context.Canceled)
	}
	m.cancel()
	return ctx.Err()
}
func (m *Manager) stop(id string, cause error) {
	m.mu.Lock()
	s := m.sessions[id]
	if s != nil {
		delete(m.sessions, id)
	}
	for token, c := range m.contexts {
		if c.plugin == id {
			c.cancel()
			delete(m.contexts, token)
		}
	}
	m.cancelInteractionsLocked(func(interaction interactionSession) bool { return interaction.plugin == id })
	// Views belong to frontends and survive a runtime restart. Closing a view
	// or detaching its frontend releases it independently of plugin processes.
	m.mu.Unlock()
	if s != nil {
		s.shutdown(cause)
	}
}
func (m *Manager) failed(s *session, err error) {
	m.mu.Lock()
	s.active.Store(false)
	s.apiCancel()
	s.cancel()
	if m.sessions[s.id] == s {
		m.failures[s.id] = err.Error()
		for token, c := range m.contexts {
			if c.plugin == s.id {
				c.cancel()
				delete(m.contexts, token)
			}
		}
		m.cancelInteractionsLocked(func(interaction interactionSession) bool {
			return interaction.plugin == s.id && interaction.generation == s.generation
		})
	}
	m.mu.Unlock()
	s.cleanupSources()
}
func (m *Manager) capture(ctx context.Context, s *session, frontend, pane uint64) (v1.Context, error) {
	return m.captureInvocation(ctx, s, frontend, pane, invocationOther, "")
}

func (m *Manager) captureInvocation(ctx context.Context, s *session, frontend, pane uint64, origin invocationOrigin, view string) (v1.Context, error) {
	f, err := m.engine.FrontendState(ctx, core.FrontendID(frontend))
	if err != nil {
		return v1.Context{}, err
	}
	c := v1.Context{PluginID: s.id, Token: randomID(), FrontendID: frontend, WorkspaceID: uint64(f.WorkspaceID), WindowID: uint64(f.WindowID), PaneID: uint64(f.PaneID)}
	if pane != 0 {
		c.PaneID = pane
	}
	snapshot, err := m.engine.Snapshot(ctx)
	if err != nil {
		return c, err
	}
	if c.PaneID != 0 {
		found := false
		for _, p := range snapshot.Panes {
			if uint64(p.ID) == c.PaneID {
				found = true
				c.WindowID = uint64(p.WindowID)
				c.WorkspaceID = 0
				for _, w := range snapshot.Windows {
					if w.ID == p.WindowID {
						c.WorkspaceID = uint64(w.WorkspaceID)
					}
				}
				for _, hidden := range snapshot.StashedPanes {
					if hidden.PaneID == p.ID {
						c.WindowID = 0
						c.WorkspaceID = 0
					}
				}
				for _, hidden := range snapshot.StashedWindows {
					if hidden.WindowID == p.WindowID {
						c.WorkspaceID = 0
					}
				}
				if p.Terminal != nil && p.Terminal.ID != nil {
					c.TerminalID = uint64(*p.Terminal.ID)
				}
				break
			}
		}
		if !found {
			return c, ErrUnavailable
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !s.active.Load() {
		return c, ErrUnavailable
	}
	if len(m.contexts) >= 4096 {
		return c, ErrOverflow
	}
	invocationCtx, invocationCancel := context.WithCancel(ctx)
	m.contexts[c.Token] = invocation{interaction: &atomic.Int64{}, context: c, plugin: s.id, generation: s.generation, ctx: invocationCtx, cancel: invocationCancel, origin: origin, view: view}
	s.observeFrontend(frontend)
	return c, nil
}

func (s *session) observeFrontend(frontend uint64) {
	if frontend == 0 {
		return
	}
	s.observedMu.Lock()
	if s.observed == nil {
		s.observed = map[uint64]struct{}{}
	}
	s.observed[frontend] = struct{}{}
	s.observedMu.Unlock()
}

func (s *session) observedFrontend(frontend uint64) bool {
	s.observedMu.Lock()
	defer s.observedMu.Unlock()
	_, ok := s.observed[frontend]
	return ok
}

func (s *session) detachFrontend(frontend uint64) {
	s.observedMu.Lock()
	_, observed := s.observed[frontend]
	delete(s.observed, frontend)
	s.observedMu.Unlock()
	if !observed || !s.hasCapability(v1.FrontendNavigate) || s.notify == nil {
		return
	}
	_ = s.notify("frontend.event", v1.FrontendEvent{Kind: v1.FrontendDetached, FrontendID: frontend})
}

func (m *Manager) invocationSource(s *session, token string) (invocationOrigin, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	invocation, ok := m.contexts[token]
	if !ok || invocation.plugin != s.id || invocation.generation != s.generation {
		return invocationOther, ""
	}
	return invocation.origin, invocation.view
}
func (m *Manager) releaseContext(token string) {
	m.mu.Lock()
	c, ok := m.contexts[token]
	var runtime *session
	if ok {
		c.cancel()
		runtime = m.sessions[c.plugin]
	}
	delete(m.contexts, token)
	m.mu.Unlock()
	if runtime != nil {
		runtime.unsubscribe(token)
	}
}
func (m *Manager) contextFor(s *session, token string) (v1.Context, error) {
	if token == "" {
		return v1.Context{}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.contexts[token]
	if !ok || c.plugin != s.id || c.generation != s.generation || !s.active.Load() || c.ctx.Err() != nil {
		return v1.Context{}, ErrPermission
	}
	return c.context, nil
}
func (m *Manager) get(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if m.closed || s == nil || !s.active.Load() {
		return nil, ErrUnavailable
	}
	return s, nil
}
func (m *Manager) Detach(frontend uint64) {
	type subscription struct {
		runtime *session
		token   string
	}
	var subscriptions []subscription
	var sessions []*session
	m.mu.Lock()
	for _, runtime := range m.sessions {
		sessions = append(sessions, runtime)
	}
	for token, c := range m.contexts {
		if c.context.FrontendID == frontend {
			if runtime := m.sessions[c.plugin]; runtime != nil {
				subscriptions = append(subscriptions, subscription{runtime, token})
			}
			c.cancel()
			delete(m.contexts, token)
		}
	}
	for key, v := range m.views {
		if v.frontend == frontend {
			delete(m.views, key)
		}
	}
	m.cancelInteractionsLocked(func(interaction interactionSession) bool { return interaction.frontend == frontend })
	m.mu.Unlock()
	for _, runtime := range sessions {
		runtime.detachFrontend(frontend)
	}
	for _, sub := range subscriptions {
		sub.runtime.unsubscribe(sub.token)
	}
}
func (m *Manager) purge(ctx context.Context, id string) error {
	snapshot, err := m.engine.Snapshot(ctx)
	if err != nil {
		return err
	}
	for _, p := range snapshot.Panes {
		if p.Tool != nil && p.Tool.Provider == id {
			if _, err := m.engine.Execute(ctx, core.ClosePaneCommand{PaneID: p.ID}); err != nil {
				return err
			}
		}
	}
	return os.RemoveAll(filepath.Join(m.root, "data", id))
}

func (m *Manager) invocationContext(s *session, token string) (context.Context, error) {
	if token == "" {
		return s.apiCtx, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.contexts[token]
	if !ok || c.plugin != s.id || c.generation != s.generation {
		return nil, ErrPermission
	}
	return c.ctx, nil
}

func (m *Manager) interactionCounter(token string) *atomic.Int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.contexts[token].interaction
}
