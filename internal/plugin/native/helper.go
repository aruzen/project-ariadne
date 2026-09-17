//go:build cgo

// Package native loads trusted native code only in the disposable helper role.
package native

/*
#cgo linux LDFLAGS: -ldl
#include <stdlib.h>
#include "bridge.h"
*/
import "C"

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/aruzen/ariadne/internal/plugin/external"
)

var limits external.Config
var outgoing chan []byte
var failure chan error
var queued atomic.Int64
var incomingBytes atomic.Int64

//export goNativeSend
func goNativeSend(data unsafe.Pointer, length C.size_t) C.int {
	if data == nil || uint64(length) == 0 {
		signalFailure(external.ErrProtocol)
		return -1
	}
	if uint64(length) > uint64(limits.MessageBytes) {
		signalFailure(external.ErrOverflow)
		return -1
	}
	copy := C.GoBytes(data, C.int(length))
	if queued.Add(int64(len(copy))) > int64(limits.ControlBytes) {
		queued.Add(-int64(len(copy)))
		signalFailure(external.ErrOverflow)
		return -1
	}
	select {
	case outgoing <- copy:
		return 0
	default:
		queued.Add(-int64(len(copy)))
		signalFailure(external.ErrOverflow)
		return -1
	}
}

//export goNativeLog
func goNativeLog(data unsafe.Pointer, length C.size_t) {
	if uint64(length) > 4096 {
		length = 4096
	}
	if data != nil {
		_, _ = os.Stderr.Write(C.GoBytes(data, C.int(length)))
	}
}
func signalFailure(err error) {
	select {
	case failure <- err:
	default:
	}
}
func Run(path string) error { return RunConfigured(path, external.DefaultConfig()) }
func RunConfigured(path string, configuration external.Config) error {
	var err error
	limits, err = configuration.Normalize()
	if err != nil {
		return err
	}
	outgoing = make(chan []byte, limits.ControlQueue)
	failure = make(chan error, 1)
	incoming := make(chan []byte, limits.ControlQueue)
	fd := C.ariadne_redirect_stdout()
	if uintptr(fd) == ^uintptr(0) {
		return errors.New("plugin helper: cannot separate stdout")
	}
	protocol := os.NewFile(uintptr(fd), "plugin-protocol")
	defer protocol.Close()
	filename := C.CString(path)
	defer C.free(unsafe.Pointer(filename))
	if code := C.ariadne_native_open(filename); code != 0 {
		return fmt.Errorf("plugin helper: native ABI initialization failed (%d)", int(code))
	}

	go func() {
		reader := bufio.NewReaderSize(os.Stdin, 8192)
		for {
			message, err := external.ReadMessage(reader, limits.MessageBytes)
			if err != nil {
				signalFailure(err)
				return
			}
			data, _ := json.Marshal(message)
			if incomingBytes.Add(int64(len(data))) > int64(limits.ControlBytes) {
				signalFailure(external.ErrOverflow)
				return
			}
			select {
			case incoming <- data:
			default:
				signalFailure(external.ErrOverflow)
				return
			}
		}
	}()
	// Protocol I/O remains independent of native callbacks; a hung callback is
	// terminated by the daemon deadline, never by unloading a live library.
	stopCallbacks := make(chan struct{})
	callbacksDone := make(chan struct{})
	go func() {
		closed := false
		defer close(callbacksDone)
		defer func() {
			if closed {
				return
			}
			timer := time.AfterFunc(time.Duration(limits.ShutdownMS)*time.Millisecond, func() { os.Exit(70) })
			C.ariadne_native_close()
			timer.Stop()
		}()
		for {
			var data []byte
			select {
			case data = <-incoming:
			case <-stopCallbacks:
				return
			}
			incomingBytes.Add(-int64(len(data)))
			var request external.Message
			_ = json.Unmarshal(data, &request)
			if request.Method == "shutdown" {
				timer := time.AfterFunc(time.Duration(limits.ShutdownMS)*time.Millisecond, func() { os.Exit(70) })
				C.ariadne_native_close()
				closed = true
				timer.Stop()
				if request.ID != nil {
					response, _ := json.Marshal(external.Message{JSONRPC: "2.0", ID: request.ID, Result: json.RawMessage("null")})
					buffer := C.CBytes(response)
					_ = goNativeSend(buffer, C.size_t(len(response)))
					C.free(buffer)
				}
				return
			}
			buffer := C.CBytes(data)
			watchdog := time.AfterFunc(time.Duration(limits.APIMS)*time.Millisecond, func() { fmt.Fprintln(os.Stderr, "native callback deadline exceeded"); os.Exit(70) })
			code := C.ariadne_native_message((*C.uchar)(buffer), C.size_t(len(data)))
			watchdog.Stop()
			C.free(buffer)
			if code != 0 {
				var request external.Message
				_ = json.Unmarshal(data, &request)
				if request.ID != nil {
					response, _ := json.Marshal(external.Message{JSONRPC: "2.0", ID: request.ID, Error: &external.RPCError{Code: -32000, Message: fmt.Sprintf("native callback failed (%d)", int(code))}})
					buffer := C.CBytes(response)
					_ = goNativeSend(buffer, C.size_t(len(response)))
					C.free(buffer)
				}
			}
		}
	}()
	for {
		select {
		case data := <-outgoing:
			queued.Add(-int64(len(data)))
			var message external.Message
			if err := json.Unmarshal(data, &message); err != nil {
				return external.ErrProtocol
			}
			if err := external.WriteMessage(protocol, message); err != nil {
				return err
			}
		case err := <-failure:
			close(stopCallbacks)
			<-callbacksDone
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
