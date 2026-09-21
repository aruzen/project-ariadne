package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	ghosttyRepository = "https://github.com/ghostty-org/ghostty.git"
	requiredZig       = "0.16.0"
)

type target struct {
	name    string
	zig     string
	archive string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "build-libghostty-vt: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	defaultTarget := runtime.GOOS + "-" + runtime.GOARCH
	targetName := flag.String("target", defaultTarget, "Go target as GOOS-GOARCH")
	sourceFlag := flag.String("source", "", "existing Ghostty checkout")
	flag.Parse()

	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return errors.New("run from the Ariadne repository root")
	}
	revision, err := readRevision(root)
	if err != nil {
		return err
	}
	selected, err := resolveTarget(*targetName)
	if err != nil {
		return err
	}
	if err := checkZig(); err != nil {
		return err
	}

	source := *sourceFlag
	if source == "" {
		source = filepath.Join(root, "build", "ghostty-source")
		if err := prepareSource(source, revision); err != nil {
			return err
		}
	} else {
		source, err = filepath.Abs(source)
		if err != nil {
			return err
		}
	}
	if err := checkRevision(source, revision); err != nil {
		return err
	}

	prefix := filepath.Join(root, "build", "libghostty-vt", selected.name)
	arguments := []string{
		"build",
		"-Demit-lib-vt",
		"-Demit-xcframework=false",
		"-Dtarget=" + selected.zig,
		"-Doptimize=ReleaseFast",
		"-p", prefix,
	}
	command := exec.Command("zig", arguments...)
	command.Dir = source
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("zig build: %w", err)
	}
	archive := filepath.Join(prefix, "lib", selected.archive)
	if info, err := os.Stat(archive); err != nil || info.IsDir() {
		return fmt.Errorf("static archive was not produced at %s", archive)
	}
	fmt.Printf("libghostty-vt %s -> %s\n", revision, archive)
	return nil
}

func readRevision(root string) (string, error) {
	path := filepath.Join(root, "third_party", "ghostty", "REVISION")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read Ghostty revision: %w", err)
	}
	revision := strings.TrimSpace(string(data))
	decoded, err := hex.DecodeString(revision)
	if err != nil || len(decoded) != 20 {
		return "", fmt.Errorf("invalid Ghostty revision %q in %s", revision, path)
	}
	return revision, nil
}

func resolveTarget(name string) (target, error) {
	switch name {
	case "darwin-arm64":
		return target{name: name, zig: "aarch64-macos", archive: "libghostty-vt.a"}, nil
	case "darwin-amd64":
		return target{name: name, zig: "x86_64-macos", archive: "libghostty-vt.a"}, nil
	case "linux-amd64":
		return target{name: name, zig: "x86_64-linux-gnu", archive: "libghostty-vt.a"}, nil
	case "linux-arm64":
		return target{name: name, zig: "aarch64-linux-gnu", archive: "libghostty-vt.a"}, nil
	case "windows-amd64":
		return target{name: name, zig: "x86_64-windows-gnu", archive: "ghostty-vt-static.lib"}, nil
	case "windows-arm64":
		return target{name: name, zig: "aarch64-windows-gnu", archive: "ghostty-vt-static.lib"}, nil
	default:
		return target{}, fmt.Errorf("unsupported target %q", name)
	}
}

func checkZig() error {
	output, err := exec.Command("zig", "version").Output()
	if err != nil {
		return fmt.Errorf("run zig: %w", err)
	}
	version := strings.TrimSpace(string(output))
	if version != requiredZig {
		return fmt.Errorf("Zig %s is required, found %s", requiredZig, version)
	}
	return nil
}

func prepareSource(source, revision string) error {
	if err := os.MkdirAll(source, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(source, ".git")); errors.Is(err, os.ErrNotExist) {
		if err := runCommand(source, "git", "init"); err != nil {
			return err
		}
		if err := runCommand(source, "git", "remote", "add", "origin", ghosttyRepository); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := runCommand(source, "git", "remote", "set-url", "origin", ghosttyRepository); err != nil {
		return err
	}
	if err := runCommand(source, "git", "fetch", "--depth=1", "origin", revision); err != nil {
		return err
	}
	return runCommand(source, "git", "checkout", "--detach", "FETCH_HEAD")
}

func checkRevision(source, expected string) error {
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = source
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("read Ghostty revision: %w", err)
	}
	revision := strings.TrimSpace(string(output))
	if revision != expected {
		return fmt.Errorf("Ghostty checkout is %s, expected %s", revision, expected)
	}
	return nil
}

func runCommand(directory, name string, arguments ...string) error {
	command := exec.Command(name, arguments...)
	command.Dir = directory
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(arguments, " "), err)
	}
	return nil
}
