package plugin

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/aruzen/ariadne/internal/core"
)

type attentionCapture struct{ values []core.Attention }

func (*attentionCapture) Set(context.Context, core.LabelTargetKind, uint64, string, string) error {
	return nil
}
func (*attentionCapture) Remove(context.Context, core.LabelTargetKind, uint64, string) error {
	return nil
}
func (capture *attentionCapture) RaiseAttention(_ context.Context, paneID core.PaneID, key string, class core.AttentionClass, severity core.AttentionSeverity, message string) error {
	capture.values = append(capture.values, core.Attention{PaneID: paneID, Key: key, Class: class, Severity: severity, Message: message})
	return nil
}

func TestAgentDetectorParsesFragmentedMarkerAndCompletion(t *testing.T) {
	detector := NewAgentDetector()
	capture := &attentionCapture{}
	ctx := context.Background()
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"key":"job","class":"waiting","message":"input needed"}`))
	marker := []byte(agentMarkerPrefix + payload + agentMarkerSuffix)
	cut := len(marker) / 2
	if err := detector.HandleTerminalEvent(ctx, TerminalEvent{Kind: TerminalOutput, PaneID: 4, Data: append([]byte("ordinary"), marker[:cut]...)}, capture); err != nil {
		t.Fatal(err)
	}
	if len(capture.values) != 0 {
		t.Fatal("partial marker emitted attention")
	}
	if err := detector.HandleTerminalEvent(ctx, TerminalEvent{Kind: TerminalOutput, PaneID: 4, Data: marker[cut:]}, capture); err != nil {
		t.Fatal(err)
	}
	if len(capture.values) != 1 || capture.values[0].Class != core.AttentionWaiting || capture.values[0].Message != "input needed" {
		t.Fatalf("attention = %+v", capture.values)
	}
	exit := core.TerminalExit{Kind: core.TerminalExitProcess, Code: 0}
	if err := detector.HandleTerminalEvent(ctx, TerminalEvent{Kind: TerminalExited, PaneID: 4, Exit: &exit}, capture); err != nil {
		t.Fatal(err)
	}
	if len(capture.values) != 2 || capture.values[1].Class != core.AttentionCompleted {
		t.Fatalf("completion = %+v", capture.values)
	}
}

func TestAgentDetectorIgnoresMalformedAndOversizedMarkers(t *testing.T) {
	detector := NewAgentDetector()
	detector.MaxMarkerBytes = 8
	capture := &attentionCapture{}
	ctx := context.Background()
	data := []byte(agentMarkerPrefix + "not-base64-and-too-long" + agentMarkerSuffix)
	if err := detector.HandleTerminalEvent(ctx, TerminalEvent{Kind: TerminalOutput, PaneID: 1, Data: data}, capture); err != nil {
		t.Fatal(err)
	}
	if len(capture.values) != 0 {
		t.Fatalf("unexpected attention = %+v", capture.values)
	}
}
