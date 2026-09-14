package plugin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/aruzen/ariadne/internal/core"
)

const (
	agentMarkerPrefix       = "\x1b_Ariadne;1;"
	agentMarkerSuffix       = "\x1b\\"
	DefaultAgentMarkerBytes = 8 << 10
)

type AgentDetector struct {
	MaxMarkerBytes int
	mu             sync.Mutex
	buffers        map[core.PaneID][]byte
	recognized     map[core.PaneID]bool
}

func NewAgentDetector() *AgentDetector {
	return &AgentDetector{MaxMarkerBytes: DefaultAgentMarkerBytes, buffers: make(map[core.PaneID][]byte), recognized: make(map[core.PaneID]bool)}
}

func (*AgentDetector) Name() string                                            { return "agent-marker" }
func (*AgentDetector) Initialize(context.Context, core.Snapshot, Labels) error { return nil }

func (detector *AgentDetector) HandleEvent(_ context.Context, event core.Event, _ Labels) error {
	if event.Kind != core.EventPaneClosed {
		return nil
	}
	payload, ok := event.Payload.(core.PaneClosedEvent)
	if !ok {
		return nil
	}
	detector.mu.Lock()
	delete(detector.buffers, payload.Pane.ID)
	delete(detector.recognized, payload.Pane.ID)
	detector.mu.Unlock()
	return nil
}

func (detector *AgentDetector) HandleTerminalEvent(ctx context.Context, event TerminalEvent, labels Labels) error {
	switch event.Kind {
	case TerminalOutput:
		return detector.consume(ctx, event.PaneID, event.Data, labels)
	case TerminalExited:
		detector.mu.Lock()
		recognized := detector.recognized[event.PaneID]
		delete(detector.buffers, event.PaneID)
		delete(detector.recognized, event.PaneID)
		detector.mu.Unlock()
		if recognized && event.Exit != nil && event.Exit.Kind == core.TerminalExitProcess && event.Exit.Code == 0 {
			return labels.RaiseAttention(ctx, event.PaneID, "process-exit", core.AttentionCompleted, core.SeverityInfo, "agent completed")
		}
	}
	return nil
}

type agentMarker struct {
	Key      string                 `json:"key"`
	Class    core.AttentionClass    `json:"class"`
	Severity core.AttentionSeverity `json:"severity,omitempty"`
	Message  string                 `json:"message,omitempty"`
}

func (detector *AgentDetector) consume(ctx context.Context, paneID core.PaneID, data []byte, labels Labels) error {
	detector.mu.Lock()
	buffer := append(detector.buffers[paneID], data...)
	limit := detector.MaxMarkerBytes
	if limit <= 0 {
		limit = DefaultAgentMarkerBytes
	}
	var payloads [][]byte
	for {
		start := bytes.Index(buffer, []byte(agentMarkerPrefix))
		if start < 0 {
			keep := markerPrefixTail(buffer)
			detector.buffers[paneID] = append(detector.buffers[paneID][:0], keep...)
			break
		}
		candidate := buffer[start:]
		body := candidate[len(agentMarkerPrefix):]
		end := bytes.Index(body, []byte(agentMarkerSuffix))
		if end < 0 {
			if len(body) > limit {
				candidate = markerPrefixTail(candidate)
			}
			detector.buffers[paneID] = append(detector.buffers[paneID][:0], candidate...)
			break
		}
		if end <= limit {
			payloads = append(payloads, append([]byte(nil), body[:end]...))
		}
		buffer = body[end+len(agentMarkerSuffix):]
	}
	detector.mu.Unlock()
	for _, encoded := range payloads {
		marker, err := decodeAgentMarker(encoded)
		if err != nil {
			continue
		}
		detector.mu.Lock()
		detector.recognized[paneID] = true
		detector.mu.Unlock()
		if err := labels.RaiseAttention(ctx, paneID, marker.Key, marker.Class, marker.Severity, marker.Message); err != nil {
			return err
		}
	}
	return nil
}

func markerPrefixTail(data []byte) []byte {
	prefix := []byte(agentMarkerPrefix)
	maximum := min(len(data), len(prefix)-1)
	for count := maximum; count > 0; count-- {
		if bytes.Equal(data[len(data)-count:], prefix[:count]) {
			return data[len(data)-count:]
		}
	}
	return nil
}

func decodeAgentMarker(encoded []byte) (agentMarker, error) {
	data, err := base64.RawURLEncoding.DecodeString(string(encoded))
	if err != nil {
		return agentMarker{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var marker agentMarker
	if err := decoder.Decode(&marker); err != nil {
		return agentMarker{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return agentMarker{}, fmt.Errorf("trailing marker data")
	}
	if marker.Severity == "" {
		switch marker.Class {
		case core.AttentionWarning:
			marker.Severity = core.SeverityWarning
		case core.AttentionError:
			marker.Severity = core.SeverityError
		default:
			marker.Severity = core.SeverityInfo
		}
	}
	probe := core.RaiseAttentionCommand{PaneID: 1, Source: "plugin:agent-marker", Key: marker.Key, Class: marker.Class, Severity: marker.Severity, Message: marker.Message}
	if probe.Key == "" || len(probe.Key) > 128 || len(probe.Message) > core.MaxAttentionMessageBytes {
		return agentMarker{}, fmt.Errorf("invalid marker")
	}
	switch marker.Class {
	case core.AttentionWaiting, core.AttentionCompleted, core.AttentionWarning, core.AttentionError:
	default:
		return agentMarker{}, fmt.Errorf("invalid class")
	}
	switch marker.Severity {
	case core.SeverityInfo, core.SeverityWarning, core.SeverityError, core.SeverityCritical:
	default:
		return agentMarker{}, fmt.Errorf("invalid severity")
	}
	return marker, nil
}
