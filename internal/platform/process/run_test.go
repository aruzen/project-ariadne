package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func helper(t *testing.T, mode string, extra ...string) ([]string, []string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argv := append([]string{executable, "-test.run=^TestProcessHelper$", "--", mode}, extra...)
	return argv, append(os.Environ(), "ARIADNE_PROCESS_HELPER=1")
}

func TestRunDirectArgvCWDAndOutputLimit(t *testing.T) {
	dir := t.TempDir()
	argv, env := helper(t, "cwd")
	data, err := Run(context.Background(), argv, dir, env, 4096)
	actualInfo, actualErr := os.Stat(string(data))
	expectedInfo, expectedErr := os.Stat(dir)
	if err != nil || actualErr != nil || expectedErr != nil || !os.SameFile(actualInfo, expectedInfo) {
		t.Fatalf("data=%q err=%v", data, err)
	}
	argv, env = helper(t, "oversize")
	data, err = Run(context.Background(), argv, dir, env, 4096)
	if !errors.Is(err, ErrOutputLimit) || len(data) > 4096 {
		t.Fatalf("size=%d error=%v", len(data), err)
	}
}

func TestTimeoutKillsDescendants(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "must-not-exist")
	argv, env := helper(t, "tree", marker)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Run(ctx, argv, dir, env, 4096)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("elapsed=%v err=%v", time.Since(start), err)
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("descendant survived helper timeout")
	}
}

func TestProcessHelper(t *testing.T) {
	if os.Getenv("ARIADNE_PROCESS_HELPER") != "1" {
		return
	}
	var args []string
	for i, arg := range os.Args {
		if arg == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	if len(args) == 0 {
		os.Exit(2)
	}
	switch args[0] {
	case "cwd":
		dir, _ := os.Getwd()
		fmt.Print(dir)
	case "oversize":
		_, _ = os.Stdout.Write(make([]byte, 8192))
	case "tree":
		command := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$", "--", "marker", args[1])
		command.Env = os.Environ()
		if err := command.Start(); err != nil {
			os.Exit(3)
		}
		time.Sleep(5 * time.Second)
	case "marker":
		time.Sleep(500 * time.Millisecond)
		_ = os.WriteFile(args[1], []byte("escaped"), 0600)
	}
	os.Exit(0)
}
