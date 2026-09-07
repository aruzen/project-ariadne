//go:build windows

// Package terminal contains the small OS terminal boundary used by the CLI.
package terminal

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

var ErrNotTerminal = errors.New("terminal: file is not a terminal")

type State struct {
	mode uint32
}

type OutputState struct {
	mode uint32
}

func IsTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) == nil
}

func MakeRaw(file *os.File) (*State, error) {
	if file == nil {
		return nil, ErrNotTerminal
	}
	handle := windows.Handle(file.Fd())
	var current uint32
	if err := windows.GetConsoleMode(handle, &current); err != nil {
		return nil, ErrNotTerminal
	}
	raw := current
	raw &^= windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_INPUT |
		windows.ENABLE_QUICK_EDIT_MODE | windows.ENABLE_INSERT_MODE
	raw |= windows.ENABLE_EXTENDED_FLAGS | windows.ENABLE_VIRTUAL_TERMINAL_INPUT
	if err := windows.SetConsoleMode(handle, raw); err != nil {
		return nil, fmt.Errorf("terminal: set raw input mode: %w", err)
	}
	return &State{mode: current}, nil
}

func Restore(file *os.File, state *State) error {
	if file == nil || state == nil {
		return ErrNotTerminal
	}
	return windows.SetConsoleMode(windows.Handle(file.Fd()), state.mode)
}

func EnableOutput(file *os.File) (*OutputState, error) {
	if file == nil {
		return nil, ErrNotTerminal
	}
	handle := windows.Handle(file.Fd())
	var current uint32
	if err := windows.GetConsoleMode(handle, &current); err != nil {
		return nil, ErrNotTerminal
	}
	if err := windows.SetConsoleMode(handle, current|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return nil, fmt.Errorf("terminal: enable virtual terminal output: %w", err)
	}
	return &OutputState{mode: current}, nil
}

func RestoreOutput(file *os.File, state *OutputState) error {
	if file == nil || state == nil {
		return ErrNotTerminal
	}
	return windows.SetConsoleMode(windows.Handle(file.Fd()), state.mode)
}

func Size(file *os.File) (int, int, error) {
	if file == nil {
		return 0, 0, ErrNotTerminal
	}
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(file.Fd()), &info); err != nil {
		if file != os.Stdin {
			return 0, 0, ErrNotTerminal
		}
		output, openErr := os.OpenFile("CONOUT$", os.O_RDWR, 0)
		if openErr != nil {
			return 0, 0, ErrNotTerminal
		}
		defer output.Close()
		if err := windows.GetConsoleScreenBufferInfo(windows.Handle(output.Fd()), &info); err != nil {
			return 0, 0, ErrNotTerminal
		}
	}
	cols := int(info.Window.Right-info.Window.Left) + 1
	rows := int(info.Window.Bottom-info.Window.Top) + 1
	if cols <= 0 || rows <= 0 {
		return 0, 0, ErrNotTerminal
	}
	return cols, rows, nil
}
