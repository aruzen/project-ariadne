package external

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/platform/process"
	"github.com/aruzen/ariadne/internal/plugin"
)

type session struct {
	log           *cappedLog
	observedMu    sync.Mutex
	observed      map[uint64]struct{}
	subMu         sync.Mutex
	subscriptions map[string]map[v1.Capability]bool
	events        chan observedEvent
	eventBytes    atomic.Int64
	observation   atomic.Pointer[observationSet]
	apiCtx        context.Context
	apiCancel     context.CancelFunc
	manager       *Manager
	id            string
	manifest      v1.Manifest
	grants        []v1.Grant
	generation    uint64
	ctx           context.Context
	cancel        context.CancelFunc
	peer          *Peer
	command       *exec.Cmd
	done          chan struct{}
	active        atomic.Bool
	gate          sync.RWMutex
	terminal      chan v1.TerminalEvent
	terminalBytes atomic.Int64
	subscription  *core.Subscription
	handlerErrors atomic.Int64
	stopOnce      sync.Once
}
type cappedLog struct {
	mu        sync.Mutex
	file      *os.File
	remaining int64
}

func (l *cappedLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(data)
	if l.remaining > 0 {
		part := data
		if int64(len(part)) > l.remaining {
			part = part[:l.remaining]
		}
		_, _ = l.file.Write(part)
		l.remaining -= int64(len(part))
	}
	return n, nil
}
func (m *Manager) start(id string) {
	m.mu.Lock()
	e := m.records[id]
	m.generation++
	generation := m.generation
	m.mu.Unlock()
	if m.reserved[id] {
		m.mu.Lock()
		m.failures[id] = "plugin: ID belongs to a built-in provider"
		m.mu.Unlock()
		return
	}
	dir := filepath.Join(m.root, "packages", e.Package)
	manifest, err := ReadManifest(dir)
	if err == nil && !reflect.DeepEqual(manifest, e.Manifest) {
		err = errors.New("plugin: installed manifest changed")
	}
	fail := func(err error) { m.mu.Lock(); m.failures[id] = err.Error(); m.mu.Unlock() }
	if err != nil {
		fail(err)
		return
	}
	dataDirectory := filepath.Join(m.root, "data", id)
	if err := os.MkdirAll(dataDirectory, 0700); err != nil {
		fail(err)
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	s := &session{manager: m, id: id, manifest: manifest, grants: append([]v1.Grant(nil), e.Grants...), generation: generation, ctx: ctx, cancel: cancel, done: make(chan struct{}), terminal: make(chan v1.TerminalEvent, m.config.ControlQueue), observed: map[uint64]struct{}{}}
	ep := manifest.Entrypoints[runtime.GOOS+"/"+runtime.GOARCH]
	argv := append([]string{filepath.Join(dir, filepath.FromSlash(ep.Path))}, ep.Args...)
	if manifest.Runtime == "native" {
		executable, err := os.Executable()
		if err != nil {
			cancel()
			fail(err)
			return
		}
		argv = []string{executable, "plugin-helper", argv[0], string(asJSON(m.config))}
	}
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Dir = dir
	command.Env = os.Environ()
	command.WaitDelay = milliseconds(m.config.ShutdownMS)
	logFile, err := os.OpenFile(filepath.Join(dataDirectory, "stderr.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		cancel()
		fail(err)
		return
	}
	command.Stderr = &cappedLog{file: logFile, remaining: 1 << 20}
	s.log = command.Stderr.(*cappedLog)
	stdin, err := command.StdinPipe()
	if err != nil {
		_ = logFile.Close()
		cancel()
		fail(err)
		return
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		_ = logFile.Close()
		cancel()
		fail(err)
		return
	}
	install, cleanup, err := process.PrepareTree(command)
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = logFile.Close()
		cancel()
		fail(err)
		return
	}
	if err = command.Start(); err != nil {
		cleanup()
		_ = logFile.Close()
		cancel()
		fail(err)
		return
	}
	if err = install(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		cleanup()
		_ = logFile.Close()
		cancel()
		fail(err)
		return
	}
	s.events = make(chan observedEvent, m.config.ControlQueue)
	s.apiCtx, s.apiCancel = context.WithCancel(ctx)
	s.command = command
	s.active.Store(true)
	initial, subscription, err := m.engine.Subscribe(ctx)
	if err != nil {
		cancel()
		_ = command.Wait()
		cleanup()
		_ = logFile.Close()
		fail(err)
		return
	}
	s.subscription = subscription
	s.updateObservation(initial)
	s.peer = NewPeer(ctx, stdout, stdin, m.config, s.handleAPI, func(err error) { m.failed(s, err) })
	m.mu.Lock()
	m.sessions[id] = s
	delete(m.failures, id)
	m.mu.Unlock()
	s.peer.Start()
	go func() {
		err := command.Wait()
		cleanup()
		_ = logFile.Close()
		if err == nil {
			err = io.EOF
		}
		s.peer.Fail(fmt.Errorf("plugin: process exited: %w", err))
		_ = subscription.Close()
		close(s.done)
	}()
	initCtx, initCancel := context.WithTimeout(ctx, milliseconds(m.config.InitializeMS))
	var initialized v1.InitializeResult
	effective := make([]v1.Grant, 0, len(s.grants))
	for _, g := range s.grants {
		for _, requested := range s.manifest.Capabilities {
			if g.Capability == requested {
				effective = append(effective, g)
				break
			}
		}
	}
	err = s.peer.Call(initCtx, "initialize", v1.Initialize{APIVersion: v1.Version, ID: id, Generation: generation, Grants: effective, DataDirectory: dataDirectory}, &initialized)
	initCancel()
	if err == nil && initialized.APIVersion != v1.Version {
		err = errors.New("plugin: incompatible initialization API version")
	}
	if err != nil {
		s.peer.Fail(err)
		return
	}
	go s.relayEvents(initial)
	go s.observe()
}
func (s *session) cleanupSources() {
	s.gate.Lock()
	defer s.gate.Unlock()
	s.manager.mu.Lock()
	current := s.manager.sessions[s.id]
	s.manager.mu.Unlock()
	if current != nil && current != s {
		return
	}
	_, _ = s.manager.engine.Execute(context.Background(), core.RemoveLabelsBySourceCommand{Source: plugin.LabelSource(s.id)})
	_, _ = s.manager.engine.Execute(context.Background(), core.RemoveAttentionsBySourceCommand{Source: plugin.LabelSource(s.id)})
}
func (s *session) shutdown(cause error) {
	s.stopOnce.Do(func() {
		s.active.Store(false)
		s.apiCancel()
		s.gate.Lock()
		s.gate.Unlock()
		if s.peer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), milliseconds(s.manager.config.ShutdownMS))
			_ = s.peer.Call(ctx, "shutdown", nil, nil)
			cancel()
			s.peer.Fail(cause)
		}
		s.cancel()
		select {
		case <-s.done:
		case <-time.After(milliseconds(s.manager.config.ShutdownMS)):
			if s.command.Cancel != nil {
				_ = s.command.Cancel()
			}
			<-s.done
		}
		s.cleanupSources()
	})
}
func (s *session) call(ctx context.Context, method string, params, result any, timeout int) error {
	ctx, cancel := context.WithTimeout(ctx, milliseconds(timeout))
	defer cancel()
	err := s.peer.Call(ctx, method, params, result)
	if err != nil {
		var handlerError *RPCError
		if errors.As(err, &handlerError) {
			if s.handlerErrors.Add(1) >= 3 {
				s.peer.Fail(err)
			}
		} else if !errors.Is(err, context.Canceled) {
			s.peer.Fail(err)
		}
	} else {
		s.handlerErrors.Store(0)
	}
	return err
}
func (s *session) observe() {
	for {
		select {
		case event := <-s.events:
			s.eventBytes.Add(-event.bytes)
			_ = s.call(s.ctx, "event", event.event, nil, s.manager.config.APIMS)
		case event := <-s.terminal:
			s.terminalBytes.Add(-int64(len(event.Data) + 128))
			snapshot, err := s.manager.engine.Snapshot(s.ctx)
			if err != nil {
				return
			}
			resource := v1.Resource{Kind: "pane", ID: event.PaneID}
			if s.allowed(snapshot, v1.PTYObserve, resource, v1.Context{}) {
				_ = s.call(s.ctx, "terminal.event", event, nil, s.manager.config.APIMS)
			}
			for _, c := range s.subscriptionContexts(v1.PTYObserve) {
				if s.allowed(snapshot, v1.PTYObserve, resource, c) {
					event.Context = c.Token
					_ = s.call(s.ctx, "terminal.event", event, nil, s.manager.config.APIMS)
				}
			}
		case <-s.ctx.Done():
			return
		}
	}
}
func (m *Manager) PublishTerminalEvent(event plugin.TerminalEvent) {
	m.mu.Lock()
	sessions := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()
	for _, s := range sessions {
		if !s.active.Load() || !s.hasCapability(v1.PTYObserve) {
			continue
		}
		set := s.observation.Load()
		if set == nil || !set.panes[uint64(event.PaneID)] {
			continue
		}
		size := int64(len(event.Data) + 128)
		if s.terminalBytes.Add(size) > int64(m.config.PTYQueueBytes) {
			s.terminalBytes.Add(-size)
			go s.peer.Fail(ErrOverflow)
			continue
		}
		data := append([]byte(nil), event.Data...)
		exit, _ := json.Marshal(PublicExit(event.Exit))
		e := v1.TerminalEvent{Kind: string(event.Kind), PaneID: uint64(event.PaneID), TerminalID: uint64(event.TerminalID), Sequence: event.Sequence, Data: data, Exit: exit}
		select {
		case s.terminal <- e:
		default:
			s.terminalBytes.Add(-size)
			go s.peer.Fail(ErrOverflow)
		}
	}
}
func (m *Manager) runCommand(ctx context.Context, frontend uint64, request v1.ManageRequest) (v1.ManageResult, error) {
	s, err := m.get(request.ID)
	if err != nil {
		return v1.ManageResult{}, err
	}
	if !declares(s.manifest.Commands, request.Command) {
		return v1.ManageResult{}, errors.New("plugin: undeclared command")
	}
	m.mu.Lock()
	if m.commands[frontend] {
		m.mu.Unlock()
		return v1.ManageResult{}, errors.New("plugin: one command per frontend")
	}
	m.commands[frontend] = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.commands, frontend); m.mu.Unlock() }()
	c, err := m.captureInvocation(ctx, s, frontend, request.PaneID, invocationCommand, "")
	if err != nil {
		return v1.ManageResult{}, err
	}
	defer m.releaseContext(c.Token)
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var result v1.CommandResult
	done := make(chan error, 1)
	go func() {
		done <- s.peer.Call(callCtx, "command", v1.Command{Name: request.Command, Args: append([]string{}, request.Args...), Context: c}, &result)
	}()
	interaction := m.interactionCounter(c.Token)
	if interaction == nil {
		return v1.ManageResult{}, ErrUnavailable
	}
	remaining := milliseconds(m.config.CommandMS)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	last := time.Now()
	for {
		select {
		case err := <-done:
			if err != nil {
				var remote *RPCError
				if errors.As(err, &remote) {
					if s.handlerErrors.Add(1) >= 3 {
						s.peer.Fail(err)
					}
				} else if !errors.Is(err, context.Canceled) {
					s.peer.Fail(err)
				}
				return v1.ManageResult{}, err
			}
			s.handlerErrors.Store(0)
			return v1.ManageResult{Command: &result}, nil
		case now := <-tick.C:
			if interaction.Load() == 0 {
				remaining -= now.Sub(last)
			}
			last = now
			if remaining <= 0 {
				s.peer.Fail(context.DeadlineExceeded)
				return v1.ManageResult{}, context.DeadlineExceeded
			}
		case <-ctx.Done():
			cancel()
			return v1.ManageResult{}, ctx.Err()
		case <-s.ctx.Done():
			return v1.ManageResult{}, ErrUnavailable
		}
	}
}
func (m *Manager) widget(ctx context.Context, frontend uint64, request v1.ManageRequest) (v1.ManageResult, error) {
	s, err := m.get(request.ID)
	if err != nil {
		return v1.ManageResult{}, err
	}
	if !declares(s.manifest.Widgets, request.Widget) {
		return v1.ManageResult{}, errors.New("plugin: undeclared widget")
	}
	c, err := m.captureInvocation(ctx, s, frontend, request.PaneID, invocationWidget, "")
	if err != nil {
		return v1.ManageResult{}, err
	}
	defer m.releaseContext(c.Token)
	var result v1.WidgetResult
	if err = s.call(ctx, "widget", v1.Widget{Name: request.Widget, Context: c}, &result, m.config.RenderMS); err != nil {
		return v1.ManageResult{}, err
	}
	if len(result.Text) > 4096 {
		s.peer.Fail(ErrOverflow)
		return v1.ManageResult{}, ErrOverflow
	}
	return v1.ManageResult{Widget: &result}, nil
}
