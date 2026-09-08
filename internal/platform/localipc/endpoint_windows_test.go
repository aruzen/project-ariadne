//go:build windows

package localipc

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func testEndpoint(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf(`\\.\pipe\ariadne-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
}
