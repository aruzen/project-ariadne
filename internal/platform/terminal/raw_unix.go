//go:build darwin || linux

// Package terminal contains the small OS terminal boundary used by the CLI.
package terminal

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var ErrNotTerminal = errors.New("terminal: file is not a terminal")

type State struct {
	termios unix.Termios
}

type OutputState struct{}

func IsTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	_, err := unix.IoctlGetTermios(int(file.Fd()), ioctlReadTermios)
	return err == nil
}

func MakeRaw(file *os.File) (*State, error) {
	if file == nil {
		return nil, ErrNotTerminal
	}
	fd := int(file.Fd())
	current, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return nil, ErrNotTerminal
	}
	raw := *current
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, &raw); err != nil {
		return nil, err
	}
	return &State{termios: *current}, nil
}

func Restore(file *os.File, state *State) error {
	if file == nil || state == nil {
		return ErrNotTerminal
	}
	return unix.IoctlSetTermios(int(file.Fd()), ioctlWriteTermios, &state.termios)
}

func EnableOutput(file *os.File) (*OutputState, error) {
	if !IsTerminal(file) {
		return nil, ErrNotTerminal
	}
	return &OutputState{}, nil
}

func RestoreOutput(_ *os.File, state *OutputState) error {
	if state == nil {
		return ErrNotTerminal
	}
	return nil
}

func Size(file *os.File) (int, int, error) {
	if file == nil {
		return 0, 0, ErrNotTerminal
	}
	size, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ)
	if err != nil || size.Col == 0 || size.Row == 0 {
		return 0, 0, ErrNotTerminal
	}
	return int(size.Col), int(size.Row), nil
}
