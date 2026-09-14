package clipboard

import (
	"context"
	"errors"
)

var ErrUnavailable = errors.New("clipboard: backend unavailable")

type Backend interface {
	Read(context.Context, int) ([]byte, error)
	Write(context.Context, []byte) error
}
