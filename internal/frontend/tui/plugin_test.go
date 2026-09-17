package tui

import (
	"context"
	"testing"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/core"
)

func TestPluginPromptCompletesAndCancelsWithoutBlocking(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		s := session{height: 24}
		dialogue := pluginDialogue{ctx: context.Background(), request: v1.InteractionRequest{Context: v1.Context{PluginID: "example"}, Interaction: v1.Interaction{Kind: "prompt", Message: "text"}}, done: make(chan dialogueResult, 1)}
		s.handlePluginDialogue(dialogue)
		if s.inputMode != inputModePrompt {
			t.Fatal("prompt did not open")
		}
		if cancel {
			s.handleModalInput([]byte{3})
		} else {
			s.handleModalInput([]byte("hello\r"))
		}
		select {
		case result := <-dialogue.done:
			if cancel && result.err == nil {
				t.Fatal("cancel reported success")
			}
			if !cancel && (result.err != nil || result.result.Text != "hello") {
				t.Fatal(result)
			}
		default:
			t.Fatal("dialogue did not finish")
		}
		if s.activePluginDialogue != nil || s.inputMode != inputModeNormal {
			t.Fatal("dialogue left modal state")
		}
	}
}
func TestPluginFrameResizeAndClosedViewDiscard(t *testing.T) {
	s := session{}
	c := &externalToolContent{owner: &s, id: "view", generation: 1}
	c.Resize(4, 2)
	generation := c.generation
	frame := &v1.Frame{ViewID: "view", Generation: generation, Width: 4, Height: 2}
	s.applyPluginResult(pluginResult{content: c, generation: generation, render: true, result: v1.ManageResult{Frame: frame}})
	if c.frame != frame {
		t.Fatal("current frame discarded")
	}
	c.Resize(8, 2)
	s.applyPluginResult(pluginResult{content: c, generation: generation, render: true, result: v1.ManageResult{Frame: frame}})
	if c.frame != nil {
		t.Fatal("old resize frame accepted")
	}
	c.closed = true
	s.applyPluginResult(pluginResult{content: c, generation: c.generation, render: true, result: v1.ManageResult{Frame: frame}})
	if c.frame != nil {
		t.Fatal("closed view frame accepted")
	}
}
func TestEditorPreviewRestoresFocusZoomAndPreviousPreview(t *testing.T) {
	s := session{width: 80, height: 24, workspace: 1, window: 1, focus: 3, previewPane: 3, previewPreviousFocus: 1, views: map[core.PaneID]*paneView{}, renderers: defaultPaneRendererRegistry(), snapshot: core.Snapshot{Workspaces: []core.Workspace{{ID: 1, Name: "default", WindowIDs: []core.WindowID{1}}}, Windows: []core.Window{{ID: 1, WorkspaceID: 1, Name: "main", Layout: &core.LayoutNode{Kind: core.LayoutPane, PaneID: 1}}}, Panes: []core.Pane{{ID: 1, WindowID: 1, Kind: core.PaneTool}, {ID: 2, WindowID: 1, Kind: core.PaneTool}, {ID: 3, WindowID: 1, Kind: core.PaneTool}}, StashedPanes: []core.StashedPane{{PaneID: 2, OriginWorkspaceID: 1, OriginWindowID: 1}, {PaneID: 3, OriginWorkspaceID: 1, OriginWindowID: 1}}}}
	s.pluginEditor = &editorPreview{pane: 3, previousPane: 2, previousFocus: 2, previousPreviewFocus: 1, zoom: true, finished: true, dialogue: pluginDialogue{done: make(chan dialogueResult, 1)}}
	done := s.pluginEditor.dialogue.done
	s.applyEditorResult(pluginResult{editor: true, result: v1.ManageResult{Editor: &v1.EditorResult{PaneID: 3, Text: "edited"}}})
	if (<-done).result.Text != "edited" || s.focus != 2 || s.previewPane != 2 || s.previewPreviousFocus != 1 || !s.zoom || s.pluginEditor != nil {
		t.Fatal("editor did not restore view")
	}
}
func TestPluginCommandCompletionAndKeybinding(t *testing.T) {
	if _, err := newInputDecoder(map[string]string{"ctrl-a p": "plugin run example echo hello"}); err != nil {
		t.Fatal(err)
	}
	s := session{promptLead: ":", prompt: "plugin run ex", pluginStatus: v1.ManageResult{Plugins: []v1.Status{{Manifest: v1.Manifest{ID: "example", Commands: []v1.Declaration{{Name: "echo"}}}}}}}
	s.completePrompt()
	if s.prompt != "plugin run example echo " {
		t.Fatal(s.prompt)
	}
	c := &externalToolContent{inputs: make(chan v1.Input, 1), generation: 1}
	if _, err := c.HandlePaste([]byte("x\ny")); err != nil {
		t.Fatal(err)
	}
	input := <-c.inputs
	if !input.Paste || string(input.Data) != "x\ny" {
		t.Fatal("paste lost its explicit event kind")
	}
	c.inputs <- input
	if _, err := c.HandleInput([]byte("z")); err == nil {
		t.Fatal("input overflow silently replaced an event")
	}

}

func TestEditorFailureRestoresPreviewEvenWhenRPCDropsResult(t *testing.T) {
	s := session{width: 80, height: 24, views: map[core.PaneID]*paneView{}, renderers: defaultPaneRendererRegistry()}
	done := make(chan dialogueResult, 1)
	s.pluginEditor = &editorPreview{pane: 3, previousFocus: 1, zoom: true, finished: true, dialogue: pluginDialogue{done: done}}
	if !s.applyEditorResult(pluginResult{editor: true, err: context.Canceled}) {
		t.Fatal("editor error was not consumed")
	}
	if result := <-done; result.err == nil {
		t.Fatal("editor error reported success")
	}
	if s.pluginEditor != nil || s.activePluginDialogue != nil || s.focus != 1 || !s.zoom {
		t.Fatal("editor error retained preview/modal")
	}
}
