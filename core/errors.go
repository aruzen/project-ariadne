package core

import "errors"

var (
	ErrClosed             = errors.New("core: closed")
	ErrInvalidConfig      = errors.New("core: invalid configuration")
	ErrInvalidCommand     = errors.New("core: invalid command")
	ErrInvalidArgument    = errors.New("core: invalid argument")
	ErrNotFound           = errors.New("core: not found")
	ErrAlreadyExists      = errors.New("core: already exists")
	ErrInvalidState       = errors.New("core: invalid state")
	ErrEventQueueOverflow = errors.New("core: event queue overflow")
)
