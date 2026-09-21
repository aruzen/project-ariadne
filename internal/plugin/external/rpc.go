package external

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

var ErrProtocol = errors.New("plugin: invalid JSON-RPC protocol")
var ErrOverflow = errors.New("plugin: queue overflow")

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return e.Message }

type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *uint64         `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// ReadMessage accepts a bounded Content-Length envelope, never newline JSON.
func ReadMessage(r *bufio.Reader, maxBytes int) (Message, error) {
	var zero Message
	length := -1
	headerBytes := 0
	for {
		lineBytes, err := r.ReadSlice('\n')
		line := string(lineBytes)
		if err != nil {
			return zero, err
		}
		headerBytes += len(line)
		if headerBytes > 8192 {
			return zero, ErrProtocol
		}
		if !strings.HasSuffix(line, "\r\n") {
			return zero, ErrProtocol
		}
		line = strings.TrimSuffix(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return zero, ErrProtocol
		}
		switch strings.ToLower(key) {
		case "content-length":
			if length != -1 {
				return zero, ErrProtocol
			}
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 1 || n > maxBytes {
				return zero, ErrProtocol
			}
			length = n
		case "content-type":
		default:
			return zero, ErrProtocol
		}
	}
	if length < 1 {
		return zero, ErrProtocol
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return zero, err
	}
	var m Message
	if err := strict(data, &m); err != nil {
		return zero, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	if m.JSONRPC != "2.0" || (m.ID != nil && *m.ID == 0) {
		return zero, ErrProtocol
	}
	if m.Method != "" {
		if len(m.Result) != 0 || m.Error != nil {
			return zero, ErrProtocol
		}
	} else {
		if m.ID == nil || ((len(m.Result) == 0) == (m.Error == nil)) || len(m.Params) != 0 {
			return zero, ErrProtocol
		}
	}
	return m, nil
}
func WriteMessage(w io.Writer, m Message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	header := []byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data)))
	for _, part := range [][]byte{header, data} {
		for len(part) > 0 {
			n, e := w.Write(part)
			if e != nil {
				return e
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}

type rpcResponse struct {
	result json.RawMessage
	err    error
}
type RPCHandler func(context.Context, string, json.RawMessage) (any, error)
type Peer struct {
	canceled      map[uint64]bool
	incomingIDs   map[uint64]bool
	ctx           context.Context
	cancel        context.CancelFunc
	reader        io.ReadCloser
	writer        io.WriteCloser
	config        Config
	handler       RPCHandler
	onFailure     func(error)
	next          atomic.Uint64
	bytes         atomic.Int64
	incomingBytes atomic.Int64
	incomingCount atomic.Int64
	requests      chan Message
	outgoing      chan []byte
	mu            sync.Mutex
	pending       map[uint64]chan rpcResponse
	err           error
	once          sync.Once
	done          chan struct{}
	wg            sync.WaitGroup
}

func NewPeer(parent context.Context, r io.ReadCloser, w io.WriteCloser, c Config, h RPCHandler, onFailure func(error)) *Peer {
	ctx, cancel := context.WithCancel(parent)
	p := &Peer{ctx: ctx, cancel: cancel, reader: r, writer: w, config: c, handler: h, onFailure: onFailure, requests: make(chan Message, c.ControlQueue), outgoing: make(chan []byte, c.ControlQueue), pending: map[uint64]chan rpcResponse{}, canceled: map[uint64]bool{}, incomingIDs: map[uint64]bool{}, done: make(chan struct{})}

	return p
}
func (p *Peer) Start() { p.wg.Add(3); go p.read(); go p.write(); go p.handle() }
func (p *Peer) Fail(err error) {
	p.once.Do(func() {
		p.mu.Lock()
		p.err = err
		for _, ch := range p.pending {
			ch <- rpcResponse{err: err}
		}
		p.pending = map[uint64]chan rpcResponse{}
		p.mu.Unlock()
		p.cancel()
		_ = p.reader.Close()
		_ = p.writer.Close()
		close(p.done)
		if p.onFailure != nil {
			p.onFailure(err)
		}
	})
}
func (p *Peer) Close()                { p.Fail(context.Canceled) }
func (p *Peer) Done() <-chan struct{} { return p.done }
func (p *Peer) Err() error            { p.mu.Lock(); defer p.mu.Unlock(); return p.err }
func (p *Peer) send(m Message) error {
	m.JSONRPC = "2.0"
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(data) > p.config.MessageBytes {
		p.Fail(ErrOverflow)
		return ErrOverflow
	}
	data = append([]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))), data...)
	if p.bytes.Add(int64(len(data))) > int64(p.config.ControlBytes) {
		p.bytes.Add(-int64(len(data)))
		p.Fail(ErrOverflow)
		return ErrOverflow
	}
	select {
	case <-p.ctx.Done():
		p.bytes.Add(-int64(len(data)))
		return p.Err()
	default:
	}
	select {
	case p.outgoing <- data:
		return nil
	default:
		p.bytes.Add(-int64(len(data)))
		p.Fail(ErrOverflow)
		return ErrOverflow
	}
}
func (p *Peer) Notify(method string, params any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return p.send(Message{Method: method, Params: data})
}
func (p *Peer) Call(ctx context.Context, method string, params any, result any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	id := p.next.Add(1)
	ch := make(chan rpcResponse, 1)
	p.mu.Lock()
	if p.err != nil {
		err = p.err
		p.mu.Unlock()
		return err
	}
	if len(p.pending) >= p.config.ControlQueue {
		p.mu.Unlock()
		p.Fail(ErrOverflow)
		return ErrOverflow
	}
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	if err := p.send(Message{ID: &id, Method: method, Params: data}); err != nil {
		return err
	}
	select {
	case response := <-ch:
		if response.err != nil {
			return response.err
		}
		if result != nil {
			return json.Unmarshal(response.result, result)
		}
		return nil
	case <-ctx.Done():
		p.mu.Lock()
		if _, ok := p.pending[id]; ok {
			delete(p.pending, id)
			p.canceled[id] = true
		}
		overflow := len(p.canceled) > 1024
		p.mu.Unlock()
		if overflow {
			p.Fail(ErrOverflow)
		}
		_ = p.Notify("cancel", map[string]any{"id": id})
		return ctx.Err()
	case <-p.ctx.Done():
		return p.Err()
	}
}
func (p *Peer) read() {
	defer p.wg.Done()
	r := bufio.NewReaderSize(p.reader, 8192)
	for {
		m, err := ReadMessage(r, p.config.MessageBytes)
		if err != nil {
			p.Fail(err)
			return
		}
		if m.Method == "" {
			p.mu.Lock()
			ch := p.pending[*m.ID]
			canceled := p.canceled[*m.ID]
			delete(p.canceled, *m.ID)
			if ch != nil {
				delete(p.pending, *m.ID)
			}
			p.mu.Unlock()
			if ch == nil && !canceled {
				p.Fail(ErrProtocol)
				return
			}
			if ch != nil {
				var e error
				if m.Error != nil {
					e = m.Error
				}
				ch <- rpcResponse{m.Result, e}
			}
			continue
		}
		if m.ID != nil {
			p.mu.Lock()
			duplicate := p.incomingIDs[*m.ID]
			p.incomingIDs[*m.ID] = true
			p.mu.Unlock()
			if duplicate {
				p.Fail(ErrProtocol)
				return
			}
		}
		size := int64(len(m.Params) + len(m.Method) + 128)
		if p.incomingBytes.Add(size) > int64(p.config.ControlBytes) || p.incomingCount.Add(1) > int64(p.config.ControlQueue) {
			p.Fail(ErrOverflow)
			return
		}
		select {
		case p.requests <- m:
		case <-p.ctx.Done():
			return
		default:
			p.Fail(ErrOverflow)
			return
		}
	}
}
func (p *Peer) write() {
	defer p.wg.Done()
	for {
		select {
		case data := <-p.outgoing:
			for len(data) > 0 {
				n, err := p.writer.Write(data)
				if err != nil {
					p.Fail(err)
					return
				}
				if n == 0 {
					p.Fail(io.ErrShortWrite)
					return
				}
				p.bytes.Add(-int64(n))
				data = data[n:]
			}
		case <-p.ctx.Done():
			return
		}
	}
}
func (p *Peer) handle() {
	defer p.wg.Done()
	for {
		select {
		case m := <-p.requests:
			if m.Method == "frontend.interact" {
				// A user's dialogue can take minutes. Keep other frontend APIs
				// available while its bounded, cancellation-aware request waits.
				p.wg.Add(1)
				go func() { defer p.wg.Done(); p.handleMessage(m) }()
			} else {
				p.handleMessage(m)
			}
		case <-p.ctx.Done():
			return
		}
	}
}
func (p *Peer) handleMessage(m Message) {
	if m.ID != nil {
		defer func() { p.mu.Lock(); delete(p.incomingIDs, *m.ID); p.mu.Unlock() }()
	}
	defer p.incomingBytes.Add(-int64(len(m.Params) + len(m.Method) + 128))
	defer p.incomingCount.Add(-1)
	if p.handler == nil {
		if m.ID != nil {
			_ = p.send(Message{ID: m.ID, Error: &RPCError{Code: -32601, Message: "method not found"}})
		}
		return
	}
	result, err := p.handler(p.ctx, m.Method, m.Params)
	if errors.Is(err, ErrOverflow) || errors.Is(err, ErrProtocol) {
		p.Fail(err)
		return
	}
	if m.ID == nil {
		if err != nil {
			p.Fail(err)
		}
		return
	}
	response := Message{ID: m.ID}
	var deferred interface {
		rpcResult() any
		rpcAfterResponse(error)
	}
	if err != nil {
		response.Error = &RPCError{Code: -32000, Message: err.Error()}
	} else {
		if value, ok := result.(interface {
			rpcResult() any
			rpcAfterResponse(error)
		}); ok {
			deferred = value
			result = value.rpcResult()
		}
		response.Result, _ = json.Marshal(result)
		if len(response.Result) == 0 {
			response.Result = json.RawMessage("null")
		}
	}
	sendErr := p.send(response)
	if deferred != nil {
		deferred.rpcAfterResponse(sendErr)
	}
}
