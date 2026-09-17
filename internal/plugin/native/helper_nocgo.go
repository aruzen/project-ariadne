//go:build !cgo

package native

import (
	"errors"
	"github.com/aruzen/ariadne/internal/plugin/external"
)

func Run(string) error { return errors.New("native plugin helper requires a cgo build") }

func RunConfigured(string, external.Config) error {
	return errors.New("native plugin helper requires a cgo build")
}
