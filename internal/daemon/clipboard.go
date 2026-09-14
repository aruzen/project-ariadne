package daemon

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	ariadneconfig "github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/core"
	ariadneprotocol "github.com/aruzen/ariadne/internal/protocol"
)

func (server *Server) ClipboardWrite(ctx context.Context, params ariadneprotocol.ClipboardWriteParams) (ariadneprotocol.ClipboardResult, error) {
	if err := server.validateClipboardRequest(ctx, params.PaneID, params.Protocol); err != nil {
		return ariadneprotocol.ClipboardResult{}, err
	}
	if err := requireClipboardPermission(server.config.Clipboard.WritePolicy, params.Approved); err != nil {
		return ariadneprotocol.ClipboardResult{}, err
	}
	data := []byte(params.Text)
	if len(data) > server.config.Clipboard.MaxTextBytes || !utf8.Valid(data) {
		return ariadneprotocol.ClipboardResult{}, fmt.Errorf("%w: invalid clipboard text", core.ErrInvalidArgument)
	}
	server.clipboardMu.Lock()
	server.clipboardText = append(server.clipboardText[:0], data...)
	server.clipboardMu.Unlock()
	backendCtx, cancel := context.WithTimeout(ctx, server.config.Clipboard.Timeout)
	defer cancel()
	_ = server.config.Clipboard.Backend.Write(backendCtx, data)
	return ariadneprotocol.ClipboardResult{}, nil
}

func (server *Server) ClipboardRead(ctx context.Context, params ariadneprotocol.ClipboardReadParams) (ariadneprotocol.ClipboardResult, error) {
	if err := server.validateClipboardRequest(ctx, params.PaneID, params.Protocol); err != nil {
		return ariadneprotocol.ClipboardResult{}, err
	}
	if err := requireClipboardPermission(server.config.Clipboard.ReadPolicy, params.Approved); err != nil {
		return ariadneprotocol.ClipboardResult{}, err
	}
	backendCtx, cancel := context.WithTimeout(ctx, server.config.Clipboard.Timeout)
	data, err := server.config.Clipboard.Backend.Read(backendCtx, server.config.Clipboard.MaxTextBytes)
	cancel()
	if err == nil && len(data) <= server.config.Clipboard.MaxTextBytes && utf8.Valid(data) {
		server.clipboardMu.Lock()
		server.clipboardText = append(server.clipboardText[:0], data...)
		server.clipboardMu.Unlock()
		return ariadneprotocol.ClipboardResult{Text: string(data)}, nil
	}
	server.clipboardMu.Lock()
	shared := append([]byte(nil), server.clipboardText...)
	server.clipboardMu.Unlock()
	return ariadneprotocol.ClipboardResult{Text: string(shared)}, nil
}

func (server *Server) validateClipboardRequest(ctx context.Context, paneID core.PaneID, protocolName string) error {
	if paneID == 0 || strings.TrimSpace(protocolName) == "" || len(protocolName) > 128 || strings.ContainsRune(protocolName, 0) {
		return fmt.Errorf("%w: invalid clipboard request", core.ErrInvalidArgument)
	}
	snapshot, err := server.core.Snapshot(ctx)
	if err != nil {
		return err
	}
	if _, exists := paneByID(snapshot, paneID); !exists {
		return fmt.Errorf("%w: pane %d", core.ErrNotFound, paneID)
	}
	return nil
}

func requireClipboardPermission(policy ariadneconfig.ClipboardPolicy, approved bool) error {
	switch policy {
	case ariadneconfig.ClipboardAllow:
		return nil
	case ariadneconfig.ClipboardAsk:
		if approved {
			return nil
		}
	}
	return ariadneprotocol.ErrPermissionDenied
}
