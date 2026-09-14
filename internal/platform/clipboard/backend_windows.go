//go:build windows

package clipboard

import (
	"context"
	"fmt"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procGetClipboardData = user32.NewProc("GetClipboardData")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procGlobalSize       = kernel32.NewProc("GlobalSize")
)

type systemBackend struct{}

func NewSystemBackend() Backend { return systemBackend{} }

func (systemBackend) Read(ctx context.Context, maxBytes int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := openClipboard(); err != nil {
		return nil, err
	}
	defer procCloseClipboard.Call()
	handle, _, callErr := procGetClipboardData.Call(cfUnicodeText)
	if handle == 0 {
		return nil, fmt.Errorf("clipboard: GetClipboardData: %w", callErr)
	}
	size, _, _ := procGlobalSize.Call(handle)
	if size == 0 || size > uintptr(maxBytes+2)*2 {
		return nil, fmt.Errorf("clipboard: invalid or oversized Unicode text")
	}
	pointer, _, callErr := procGlobalLock.Call(handle)
	if pointer == 0 {
		return nil, fmt.Errorf("clipboard: GlobalLock: %w", callErr)
	}
	defer procGlobalUnlock.Call(handle)
	units := unsafe.Slice((*uint16)(unsafe.Pointer(pointer)), int(size/2))
	end := 0
	for end < len(units) && units[end] != 0 {
		end++
	}
	data := []byte(string(utf16.Decode(units[:end])))
	if len(data) > maxBytes {
		return nil, fmt.Errorf("clipboard: content exceeds %d bytes", maxBytes)
	}
	return data, nil
}

func (systemBackend) Write(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	units, err := windows.UTF16FromString(string(data))
	if err != nil {
		return err
	}
	if err := openClipboard(); err != nil {
		return err
	}
	defer procCloseClipboard.Call()
	if result, _, callErr := procEmptyClipboard.Call(); result == 0 {
		return fmt.Errorf("clipboard: EmptyClipboard: %w", callErr)
	}
	size := uintptr(len(units) * 2)
	handle, _, callErr := procGlobalAlloc.Call(gmemMoveable, size)
	if handle == 0 {
		return fmt.Errorf("clipboard: GlobalAlloc: %w", callErr)
	}
	owned := true
	defer func() {
		if owned {
			procGlobalFree.Call(handle)
		}
	}()
	pointer, _, callErr := procGlobalLock.Call(handle)
	if pointer == 0 {
		return fmt.Errorf("clipboard: GlobalLock: %w", callErr)
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(pointer)), len(units)), units)
	procGlobalUnlock.Call(handle)
	if result, _, callErr := procSetClipboardData.Call(cfUnicodeText, handle); result == 0 {
		return fmt.Errorf("clipboard: SetClipboardData: %w", callErr)
	}
	owned = false
	return nil
}

func openClipboard() error {
	if result, _, callErr := procOpenClipboard.Call(0); result == 0 {
		return fmt.Errorf("clipboard: OpenClipboard: %w", callErr)
	}
	return nil
}
