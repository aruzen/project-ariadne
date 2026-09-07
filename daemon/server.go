// Package daemon owns Ariadne Core, PTY processes, persistence, and frontend
// connections. OS-specific listeners and PTY factories are supplied by the
// platform layer.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/aruzen/ariadne/core"
	ariadneprotocol "github.com/aruzen/ariadne/protocol"
	"github.com/aruzen/ariadne/statefile"
	"github.com/aruzen/streammux"
	"github.com/aruzen/streammux/pty"
)

type listenerCleaner interface {
	Cleanup() error
}

type Server struct {
	config  Config
	core    *core.Core
	manager *pty.Manager
	store   *statefile.Store
	load    statefile.LoadResult

	ctx    context.Context
	cancel context.CancelFunc

	mu                sync.Mutex
	listener          net.Listener
	connections       map[*streammux.Peer]struct{}
	activeConnections int
	stopping          bool
	closing           bool
	closeErr          error
	fatal             chan error
	serveOnce         atomic.Bool
	closeOnce         sync.Once
	commandGate       sync.RWMutex
	terminalMu        sync.Mutex
	stopAfterResponse atomic.Bool
	connectionsWG     sync.WaitGroup
	workersWG         sync.WaitGroup

	stateSubscription   *core.Subscription
	managerSubscription *pty.Subscription
	stateLoopDone       chan struct{}
	managerLoopDone     chan struct{}
}

func Open(parent context.Context, factory pty.ManagedFactory, configuration Config) (*Server, statefile.LoadResult, error) {
	if parent == nil || factory == nil {
		return nil, statefile.LoadResult{}, fmt.Errorf("%w: nil dependency", ErrInvalidConfig)
	}
	configuration, err := configuration.withDefaults()
	if err != nil {
		return nil, statefile.LoadResult{}, err
	}
	loaded, err := statefile.Load(configuration.StatePath, configuration.State)
	if err != nil {
		return nil, statefile.LoadResult{}, err
	}
	engine, err := core.NewFromSnapshot(configuration.Core, loaded.Snapshot)
	if err != nil {
		return nil, loaded, err
	}
	manager, err := pty.NewManager(context.Background(), factory, configuration.Manager)
	if err != nil {
		_ = engine.Close()
		return nil, loaded, err
	}
	store, err := statefile.NewStore(configuration.StatePath, configuration.State)
	if err != nil {
		_ = manager.Close()
		_ = engine.Close()
		return nil, loaded, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := &Server{
		config: configuration, core: engine, manager: manager, store: store, load: loaded,
		ctx: ctx, cancel: cancel, connections: make(map[*streammux.Peer]struct{}), fatal: make(chan error, 1),
		stateLoopDone: make(chan struct{}), managerLoopDone: make(chan struct{}),
	}
	if err := server.startObservers(); err != nil {
		cancel()
		_ = store.Close(context.Background())
		_ = manager.Close()
		_ = engine.Close()
		return nil, loaded, err
	}
	server.workersWG.Add(1)
	go func() {
		defer server.workersWG.Done()
		select {
		case <-parent.Done():
			server.requestStop(nil)
		case <-server.ctx.Done():
		}
	}()
	return server, loaded, nil
}

func (server *Server) Core() *core.Core                 { return server.core }
func (server *Server) Manager() *pty.Manager            { return server.manager }
func (server *Server) LoadResult() statefile.LoadResult { return server.load }

func (server *Server) Serve(listener net.Listener) error {
	if listener == nil {
		return fmt.Errorf("%w: nil listener", ErrInvalidConfig)
	}
	if !server.serveOnce.CompareAndSwap(false, true) {
		return ErrAlreadyServing
	}
	server.mu.Lock()
	if server.closing {
		server.mu.Unlock()
		_ = listener.Close()
		return ErrClosed
	}
	server.listener = listener
	stopping := server.stopping
	server.mu.Unlock()
	if stopping {
		_ = listener.Close()
	}

	var serveErr error
	for {
		connection, err := listener.Accept()
		if err != nil {
			server.mu.Lock()
			closing := server.closing
			stopping := server.stopping
			server.mu.Unlock()
			select {
			case fatalErr := <-server.fatal:
				serveErr = fatalErr
			default:
				if !closing && !stopping {
					serveErr = err
				}
			}
			break
		}
		if !server.acceptConnection(connection) {
			_ = connection.Close()
			continue
		}
		go server.serveConnection(connection)
	}
	closeErr := server.Close(context.Background())
	return errors.Join(serveErr, closeErr)
}

func (server *Server) acceptConnection(connection net.Conn) bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.closing || server.stopping || server.activeConnections >= server.config.MaxConnections {
		return false
	}
	server.activeConnections++
	server.connectionsWG.Add(1)
	return true
}

func (server *Server) serveConnection(connection net.Conn) {
	defer func() {
		server.mu.Lock()
		server.activeConnections--
		server.mu.Unlock()
		server.connectionsWG.Done()
	}()
	muxConnection, err := streammux.Open(server.ctx, connection, server.config.Stream)
	if err != nil {
		_ = connection.Close()
		return
	}
	peer, err := streammux.NewPeer(muxConnection, server.config.Peer)
	if err != nil {
		_ = muxConnection.Close()
		return
	}
	server.mu.Lock()
	if server.closing {
		server.mu.Unlock()
		_ = peer.Close()
		return
	}
	server.connections[peer] = struct{}{}
	server.mu.Unlock()
	defer func() {
		server.mu.Lock()
		delete(server.connections, peer)
		server.mu.Unlock()
		_ = peer.Close()
	}()

	ariadneConfig := server.config.AriadneProtocol
	ariadneConfig.CommandGuard = server.guardCommand
	ariadneConfig.Terminal = server
	ariadneProtocol, err := ariadneprotocol.Register(peer, server.core, ariadneConfig)
	if err != nil {
		return
	}
	defer ariadneProtocol.Close()
	ptyConfig := server.config.PTYProtocol
	ptyConfig.Authorize = server.authorizePTY
	ptyProtocol, err := pty.RegisterProtocol(peer, server.manager, ptyConfig)
	if err != nil {
		return
	}
	defer ptyProtocol.Close()
	_ = peer.Serve(server.ctx)
}

func (server *Server) authorizePTY(ctx context.Context, request pty.AuthorizationRequest) error {
	switch request.Operation {
	case pty.OperationOpen, pty.OperationList, pty.OperationInspect, pty.OperationKill:
		return ErrPTYOperationDenied
	case pty.OperationAttach, pty.OperationDetach, pty.OperationInput, pty.OperationResize:
		snapshot, err := server.core.Snapshot(ctx)
		if err != nil {
			return err
		}
		if _, exists := snapshot.PaneByTerminalID(request.SessionID); !exists {
			return ErrTerminalNotOwned
		}
		return nil
	default:
		return ErrPTYOperationDenied
	}
}

func (server *Server) startObservers() error {
	_, stateSubscription, err := server.core.Subscribe(server.ctx)
	if err != nil {
		return err
	}
	managerSubscription, err := server.manager.Subscribe()
	if err != nil {
		_ = stateSubscription.Close()
		return err
	}
	server.stateSubscription = stateSubscription
	server.managerSubscription = managerSubscription
	server.workersWG.Add(3)
	go server.persistLoop(stateSubscription)
	go server.managerLoop(managerSubscription)
	go server.storeErrorLoop()
	return nil
}

func (server *Server) persistLoop(subscription *core.Subscription) {
	defer server.workersWG.Done()
	defer close(server.stateLoopDone)
	for range subscription.Events() {
		snapshot, err := server.core.Snapshot(server.ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, core.ErrClosed) {
				server.requestStop(err)
			}
			return
		}
		if err := server.store.Schedule(snapshot); err != nil {
			if !errors.Is(err, statefile.ErrClosed) {
				server.requestStop(err)
			}
			return
		}
	}
	if err := subscription.Err(); err != nil && !errors.Is(err, context.Canceled) {
		server.requestStop(err)
	}
}

func (server *Server) managerLoop(subscription *pty.Subscription) {
	defer server.workersWG.Done()
	defer close(server.managerLoopDone)
	pendingRemoval := make(map[streammux.StreamID]struct{})
	for event := range subscription.Events() {
		server.terminalMu.Lock()
		err := server.handleManagerEvent(event, pendingRemoval)
		server.terminalMu.Unlock()
		if err != nil {
			server.requestStop(err)
			return
		}
	}
	if err := subscription.Err(); err != nil && !errors.Is(err, pty.ErrManagerClosed) {
		server.requestStop(err)
	}
}

func (server *Server) handleManagerEvent(event pty.Event, pendingRemoval map[streammux.StreamID]struct{}) error {
	switch event.Kind {
	case pty.EventExited:
		if event.Session.Exit == nil {
			return nil
		}
		if terminalExitRemovesPane(*event.Session.Exit) {
			snapshot, err := server.core.Snapshot(server.ctx)
			if err == nil {
				if pane, exists := snapshot.PaneByTerminalID(event.Session.ID); exists {
					_, err = server.core.Execute(server.ctx, core.ClosePaneCommand{PaneID: pane.ID})
				}
			}
			if err == nil || errors.Is(err, core.ErrNotFound) {
				pendingRemoval[event.Session.ID] = struct{}{}
				return nil
			}
			if !errors.Is(err, context.Canceled) {
				return err
			}
			return nil
		}
		state, exit := terminalExit(*event.Session.Exit)
		_, err := server.core.Execute(server.ctx, core.RecordTerminalExitCommand{
			TerminalID: event.Session.ID, State: state, Exit: exit,
			HistoryAvailable: event.Session.HistoryAvailable,
		})
		if err != nil && !errors.Is(err, core.ErrNotFound) && !errors.Is(err, context.Canceled) {
			return err
		}
	case pty.EventRetained:
		if _, remove := pendingRemoval[event.Session.ID]; remove {
			delete(pendingRemoval, event.Session.ID)
			if err := server.manager.Remove(event.Session.ID); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
				return err
			}
		}
	case pty.EventEvicted, pty.EventRemoved:
		delete(pendingRemoval, event.Session.ID)
		_, err := server.core.Execute(server.ctx, core.ForgetTerminalSessionCommand{TerminalID: event.Session.ID})
		if err != nil && !errors.Is(err, core.ErrNotFound) && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return nil
}

func (server *Server) guardCommand() (func(), error) {
	server.commandGate.RLock()
	server.mu.Lock()
	stopping := server.stopping || server.closing
	server.mu.Unlock()
	if stopping {
		server.commandGate.RUnlock()
		return nil, fmt.Errorf("%w: daemon is stopping", core.ErrInvalidState)
	}
	return server.commandGate.RUnlock, nil
}

func terminalExitRemovesPane(status pty.ExitStatus) bool {
	return status.Reason == pty.ExitReasonKilled || (status.Reason == pty.ExitReasonExited && status.Code == 0)
}

func terminalExit(status pty.ExitStatus) (core.TerminalState, core.TerminalExit) {
	switch status.Reason {
	case pty.ExitReasonExited:
		if status.Code >= 0 {
			return core.TerminalExited, core.TerminalExit{Kind: core.TerminalExitProcess, Code: status.Code}
		}
	case pty.ExitReasonSignaled:
		signal := status.Signal
		if signal == "" {
			signal = "unknown"
		}
		return core.TerminalExited, core.TerminalExit{Kind: core.TerminalExitSignal, Signal: signal}
	}
	message := status.Error
	if message == "" {
		message = string(status.Reason)
	}
	return core.TerminalFailed, core.TerminalExit{Kind: core.TerminalExitPTYError, Message: message}
}

func (server *Server) storeErrorLoop() {
	defer server.workersWG.Done()
	select {
	case err, ok := <-server.store.Errors():
		if ok && err != nil {
			server.requestStop(err)
		}
	case <-server.ctx.Done():
	}
}

func (server *Server) requestStop(err error) {
	if err != nil {
		select {
		case server.fatal <- err:
		default:
		}
	}
	server.mu.Lock()
	server.stopping = true
	listener := server.listener
	server.mu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
}

func (server *Server) Close(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidConfig)
	}
	server.closeOnce.Do(func() {
		server.mu.Lock()
		server.closing = true
		listener := server.listener
		server.mu.Unlock()
		if listener != nil {
			_ = listener.Close()
		}

		var shutdownErrors []error
		if server.managerSubscription != nil {
			_ = server.managerSubscription.Close()
			<-server.managerLoopDone
		}
		if server.stateSubscription != nil {
			_ = server.stateSubscription.Close()
			<-server.stateLoopDone
		}
		server.commandGate.Lock()
		snapshot, err := server.core.Snapshot(ctx)
		if err != nil {
			shutdownErrors = append(shutdownErrors, err)
		} else {
			if err := server.store.Schedule(snapshot); err != nil {
				shutdownErrors = append(shutdownErrors, err)
			}
			if err := server.store.Flush(ctx); err != nil {
				shutdownErrors = append(shutdownErrors, err)
			}
		}
		if err := server.store.Close(ctx); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
		server.commandGate.Unlock()

		server.cancel()
		server.mu.Lock()
		peers := make([]*streammux.Peer, 0, len(server.connections))
		for peer := range server.connections {
			peers = append(peers, peer)
		}
		server.mu.Unlock()
		for _, peer := range peers {
			if err := peer.Close(); err != nil && !errors.Is(err, io.EOF) {
				shutdownErrors = append(shutdownErrors, err)
			}
		}
		server.connectionsWG.Wait()
		if err := server.manager.Close(); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
		server.workersWG.Wait()
		if err := server.core.Close(); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
		if cleaner, ok := listener.(listenerCleaner); ok {
			if err := cleaner.Cleanup(); err != nil {
				shutdownErrors = append(shutdownErrors, err)
			}
		}
		server.mu.Lock()
		server.closeErr = errors.Join(shutdownErrors...)
		server.mu.Unlock()
	})
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.closeErr
}
