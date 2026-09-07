//go:build windows

package main

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func missingEndpoint(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf(`\\.\pipe\ariadne-missing-%d-%d`, os.Getpid(), time.Now().UnixNano())
}
