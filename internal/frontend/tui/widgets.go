package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/platform/process"
)

type widgetResult struct {
	name       string
	generation uint64
	text       string
	err        error
}
type widgetState struct {
	cwd        string
	pane       core.PaneID
	generation uint64
	running    bool
	cancel     context.CancelFunc
	next       time.Time
	text       string
	err        bool
}
type widgetRunner struct {
	ctx     context.Context
	options map[string]config.WidgetOptions
	states  map[string]*widgetState
	results chan widgetResult
	active  int
	wg      sync.WaitGroup
}

func newWidgetRunner(ctx context.Context, options map[string]config.WidgetOptions) *widgetRunner {
	return &widgetRunner{ctx: ctx, options: options, states: make(map[string]*widgetState), results: make(chan widgetResult, 64)}
}

func (runner *widgetRunner) close() {
	for _, state := range runner.states {
		if state.cancel != nil {
			state.cancel()
		}
	}
	runner.wg.Wait()
}

func (session *session) refreshWidgets(now time.Time) {
	runner := session.widgets
	if runner == nil {
		return
	}
	for name, options := range runner.options {
		if len(options.Command) == 0 && options.Plugin == "" {
			continue
		}
		cwd := session.widgetCWD(options.CWD)
		state := runner.states[name]
		if state == nil {
			state = &widgetState{}
			runner.states[name] = state
		}
		if state.cwd != cwd || state.pane != session.focus {
			if state.cancel != nil {
				state.cancel()
			}
			state.cwd, state.pane = cwd, session.focus
			state.generation++
			state.text = ""
			state.err = false
			state.next = time.Time{}
			session.dirty = true
		}
		if state.running || now.Before(state.next) || runner.active >= 4 {
			continue
		}
		interval, timeout, maxBytes := options.IntervalMS, options.TimeoutMS, options.MaxBytes
		if interval == 0 {
			interval = 5000
		}
		if timeout == 0 {
			timeout = 1000
		}
		if maxBytes == 0 {
			maxBytes = 4096
		}
		ctx, cancel := context.WithTimeout(runner.ctx, time.Duration(timeout)*time.Millisecond)
		state.cancel = cancel
		state.running = true
		state.next = now.Add(time.Duration(interval) * time.Millisecond)
		runner.active++
		generation := state.generation
		statePane := state.pane
		argv := append([]string(nil), options.Command...)
		env := append([]string(nil), session.env...)
		runner.wg.Add(1)
		go func() {
			defer runner.wg.Done()
			defer cancel()
			var data []byte
			var err error
			if options.Plugin != "" {
				id, name, _ := strings.Cut(options.Plugin, "/")
				result, e := session.client.Plugin(ctx, v1.ManageRequest{Action: "widget", ID: id, Widget: name, PaneID: uint64(statePane)})
				err = e
				if result.Widget != nil {
					data = []byte(result.Widget.Text)
				}
				if len(data) > maxBytes {
					err = process.ErrOutputLimit
				}
			} else {
				data, err = process.Run(ctx, argv, cwd, env, maxBytes)
			}
			result := widgetResult{name: name, generation: generation, text: sanitizeWidgetText(string(data)), err: err}
			select {
			case runner.results <- result:
			case <-runner.ctx.Done():
			}
		}()
	}
}

func (session *session) applyWidgetResult(result widgetResult) {
	runner := session.widgets
	if runner == nil {
		return
	}
	runner.active--
	state := runner.states[result.name]
	if state == nil {
		return
	}
	state.running = false
	state.cancel = nil
	if state.generation != result.generation || state.pane != session.focus || state.cwd != session.widgetCWD(runner.options[result.name].CWD) {
		return
	}
	if result.err != nil {
		state.err = true
	} else {
		state.text = result.text
		state.err = false
	}
	session.dirty = true
}

func sanitizeWidgetText(value string) string {
	var out strings.Builder
	state := byte(0)
	for _, r := range strings.ToValidUTF8(value, "�") {
		switch state {
		case 1:
			if r == '[' {
				state = 2
			} else if r == ']' || r == 'P' || r == '_' || r == '^' {
				state = 3
			} else {
				state = 0
			}
			continue
		case 2:
			if r >= 0x40 && r <= 0x7e {
				state = 0
			}
			continue
		case 3:
			if r == 7 || r == 0x9c {
				state = 0
			} else if r == 0x1b {
				state = 4
			}
			continue
		case 4:
			if r == '\\' {
				state = 0
			} else {
				state = 3
			}
			continue
		}
		if r == 0x1b {
			state = 1
			continue
		}
		if r == 0x9b {
			state = 2
			continue
		}
		if r == 0x9d {
			state = 3
			continue
		}
		if r == '\n' || r == '\r' || r == '\t' {
			out.WriteByte(' ')
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		out.WriteRune(r)
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

func (session *session) configuredStatusBar() StatusBar {
	bar := StatusBar{Background: session.theme.styles["status"]}
	makeWidget := func(name string) StatusWidget {
		options := session.presentation.Status.Widgets[name]
		if options.Plugin == "" && strings.Contains(name, "/") {
			options.Plugin = name
		}
		return StatusWidgetFunc(func(c StatusContext) []Segment {
			styleName := options.Style
			if styleName == "" {
				styleName = "muted"
				if name == "brand" {
					styleName = "accent"
				}
				if name == "workspace" || name == "window" || name == "clock" {
					styleName = "status"
				}
			}
			style := session.theme.styles[styleName]
			value := ""
			format := options.Format
			if len(options.Command) != 0 || options.Plugin != "" {
				if state := session.widgets.states[name]; state != nil {
					value = state.text
					if state.err {
						value += " !"
					}
				}
				if format == "" {
					format = " {output} "
				}
			} else {
				switch name {
				case "brand":
					value = "ariadne"
				case "workspace":
					value = c.Workspace + "/" + c.Window
				case "window":
					value = c.Window
				case "pane":
					if format == "" {
						return []Segment{{Text: fmt.Sprintf(" pane:%d", c.PaneID), Style: style}, {Text: " " + c.PaneTitle + " ", Style: style, Tag: "pane-title"}}
					}
					value = fmt.Sprintf("pane:%d %s", c.PaneID, c.PaneTitle)
				case "attention":
					if c.UnreadAttention == 0 {
						return nil
					}
					value = fmt.Sprintf("!%d", c.UnreadAttention)
					if options.Style == "" {
						if c.AttentionSeverity == core.SeverityWarning {
							style = session.theme.styles["warning"]
						} else if severityRank(c.AttentionSeverity) >= severityRank(core.SeverityError) {
							style = session.theme.styles["error"]
						}
					}
				case "state":
					value = c.Message
					if value == "" {
						value = string(c.State)
					}
					if value == "" {
						value = "ready"
					}
				case "clock":
					value = c.Now.Format("15:04")
				case "cwd":
					value = c.CWD
				}
				if format == "" {
					format = " {value} "
				}
			}
			text := strings.NewReplacer("{value}", value, "{output}", value, "{workspace}", c.Workspace, "{window}", c.Window, "{pane}", fmt.Sprint(c.PaneID), "{title}", c.PaneTitle, "{pane_title}", c.ManualPaneTitle, "{terminal_title}", c.TerminalTitle, "{cwd}", c.CWD, "{state}", string(c.State)).Replace(format)
			return []Segment{{Text: cleanText(text), Style: style, Tag: name}}
		})
	}
	for _, name := range session.presentation.Status.Left {
		bar.Left = append(bar.Left, makeWidget(name))
	}
	for _, name := range session.presentation.Status.Right {
		bar.Right = append(bar.Right, makeWidget(name))
	}
	return bar
}
