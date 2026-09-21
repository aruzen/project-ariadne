// A small stdio plugin. Build from the repository root:
// go build -o examples/plugins/process/plugin-process ./examples/plugins/process
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *uint64         `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var writerMu sync.Mutex
var pendingMu sync.Mutex
var pending = map[uint64]chan message{}
var next atomic.Uint64
var inputs atomic.Uint64
var completedInteractions atomic.Uint64
var pendingInteractions sync.Map

func send(m message) error {
	m.JSONRPC = "2.0"
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	writerMu.Lock()
	defer writerMu.Unlock()
	_, err = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return err
}
func call(method string, params any) (json.RawMessage, error) {
	id := next.Add(1)
	ch := make(chan message, 1)
	pendingMu.Lock()
	pending[id] = ch
	pendingMu.Unlock()
	defer func() { pendingMu.Lock(); delete(pending, id); pendingMu.Unlock() }()
	data, _ := json.Marshal(params)
	if err := send(message{ID: &id, Method: method, Params: data}); err != nil {
		return nil, err
	}
	m := <-ch
	if m.Error != nil {
		return nil, errors.New(m.Error.Message)
	}
	return m.Result, nil
}
func read(r *bufio.Reader) (message, error) {
	var m message
	length := -1
	for {
		line, err := r.ReadSlice('\n')
		if err != nil {
			return m, err
		}
		if bytes.Equal(line, []byte("\r\n")) {
			break
		}
		key, value, ok := strings.Cut(strings.TrimSpace(string(line)), ":")
		if ok && strings.EqualFold(key, "Content-Length") {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return m, err
			}
		}
	}
	if length < 1 || length > 8<<20 {
		return m, errors.New("invalid Content-Length")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return m, err
	}
	return m, json.Unmarshal(data, &m)
}
func handle(m message) (any, error) {
	switch m.Method {
	case "initialize":
		var p v1.Initialize
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, err
		}
		if p.APIVersion != v1.Version {
			return nil, errors.New("unsupported API version")
		}
		fmt.Fprintf(os.Stderr, "%s: data=%s grants=%v\n", p.ID, p.DataDirectory, p.Grants)
		return v1.InitializeResult{APIVersion: 1}, nil
	case "command":
		var c v1.Command
		if err := json.Unmarshal(m.Params, &c); err != nil {
			return nil, err
		}
		switch c.Name {
		case "echo":
			return v1.CommandResult{Text: strings.Join(c.Args, " ")}, nil
		case "snapshot":
			data, err := call("core.snapshot", v1.APIRequest{Context: c.Context.Token, Params: json.RawMessage(`{}`)})
			return v1.CommandResult{JSON: data}, err
		case "prompt", "editor":
			data, err := call("frontend.interact", v1.APIRequest{Context: c.Context.Token, Params: raw(v1.Interaction{Kind: c.Name, Message: "Enter text", Text: strings.Join(c.Args, " ")})})
			if err != nil {
				return nil, err
			}
			var result v1.InteractionResult
			if err := json.Unmarshal(data, &result); err != nil {
				return nil, err
			}
			return v1.CommandResult{Text: result.Text}, nil
		}
		return nil, errors.New("unknown command")
	case "widget":
		var widget v1.Widget
		if err := json.Unmarshal(m.Params, &widget); err != nil {
			return nil, err
		}
		if widget.Name == "interaction-count" {
			return v1.WidgetResult{Text: fmt.Sprintf("interaction:%d", completedInteractions.Load())}, nil
		}
		return v1.WidgetResult{Text: fmt.Sprintf("input:%d", inputs.Load())}, nil
	case "view.render":
		var v v1.View
		if err := json.Unmarshal(m.Params, &v); err != nil {
			return nil, err
		}
		frame := v1.Frame{ViewID: v.ID, Generation: v.Generation, Width: v.Width, Height: v.Height, Cells: make([]v1.Cell, v.Width*v.Height)}
		style := v1.Style{Foreground: v1.Color{R: 230, G: 230, B: 230}, Background: v1.Color{R: 20, G: 25, B: 30}}
		for i := range frame.Cells {
			frame.Cells[i] = v1.Cell{Text: " ", Width: 1, Style: style}
		}
		text := fmt.Sprintf("Process plugin: %s — input:%d", v.Instance, inputs.Load())
		x := 0
		for _, r := range text {
			if r > 127 {
				r = '-'
			}
			if x >= v.Width {
				break
			}
			frame.Cells[x] = v1.Cell{Text: string(r), Width: 1, Style: style}
			x++
		}
		return frame, nil
	case "view.input":
		inputs.Add(1)
		var input v1.Input
		if err := json.Unmarshal(m.Params, &input); err != nil {
			return nil, err
		}
		if text := string(input.Data); text == "interact" || text == "cancel-view" {
			data, err := call("frontend.interact", v1.APIRequest{Context: input.View.Context.Token, Params: raw(v1.Interaction{Kind: "prompt", Message: text})})
			if err != nil {
				return nil, err
			}
			var started v1.InteractionStarted
			if err := json.Unmarshal(data, &started); err != nil || started.ID == "" {
				return nil, errors.New("invalid asynchronous interaction response")
			}
			pendingInteractions.Store(started.ID, true)
		}
		return nil, nil
	case "interaction.result":
		var completed v1.InteractionCompleted
		if err := json.Unmarshal(m.Params, &completed); err != nil {
			return nil, err
		}
		if _, found := pendingInteractions.LoadAndDelete(completed.ID); found {
			completedInteractions.Add(1)
		}
		return nil, nil
	case "event", "terminal.event", "view.close", "cancel", "shutdown":
		return nil, nil
	}
	return nil, errors.New("unknown method")
}
func raw(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
func main() {
	r := bufio.NewReaderSize(os.Stdin, 8192)
	for {
		m, err := read(r)
		if err != nil {
			if err != io.EOF {
				fmt.Fprintln(os.Stderr, err)
			}
			return
		}
		if m.Method == "" && m.ID != nil {
			pendingMu.Lock()
			ch := pending[*m.ID]
			pendingMu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		go func(m message) {
			result, err := handle(m)
			if m.ID == nil {
				return
			}
			response := message{ID: m.ID}
			if err != nil {
				response.Error = &rpcError{Code: -32000, Message: err.Error()}
			} else {
				response.Result = raw(result)
			}
			_ = send(response)
		}(m)
	}
}
