package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type entry struct {
	name string
	data []byte
	mode fs.FileMode
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "package-release:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("package-release", flag.ContinueOnError)
	root := flags.String("root", ".", "repository root")
	binary := flags.String("binary", "", "built Ariadne executable")
	output := flags.String("output", "", "archive path")
	version := flags.String("version", "", "release version including v prefix")
	goos := flags.String("os", "", "target operating system")
	goarch := flags.String("arch", "", "target architecture")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *binary == "" || *output == "" || *version == "" || *goos == "" || *goarch == "" || flags.NArg() != 0 {
		return errors.New("root, binary, output, version, os and arch are required")
	}
	if !validComponent(*version) || !strings.HasPrefix(*version, "v") {
		return errors.New("version must start with v and contain only letters, digits, '.', '_' or '-'")
	}
	if !supportedTarget(*goos, *goarch) {
		return fmt.Errorf("unsupported release target %s/%s", *goos, *goarch)
	}
	base := fmt.Sprintf("ariadne_%s_%s_%s", *version, *goos, *goarch)
	binaryName := "ariadne"
	if *goos == "windows" {
		binaryName += ".exe"
	}
	entries, err := releaseEntries(*root, *binary, binaryName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		return err
	}
	if *goos == "windows" {
		if !strings.HasSuffix(*output, ".zip") {
			return errors.New("Windows output must end in .zip")
		}
		return writeZip(*output, base, entries)
	}
	if !strings.HasSuffix(*output, ".tar.gz") {
		return errors.New("Unix output must end in .tar.gz")
	}
	return writeTarGzip(*output, base, entries)
}

func validComponent(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("._-", character) {
			continue
		}
		return false
	}
	return true
}

func supportedTarget(goos, goarch string) bool {
	if goarch != "amd64" && goarch != "arm64" {
		return false
	}
	return goos == "darwin" || goos == "linux" || goos == "windows"
}

func releaseEntries(root, binary, binaryName string) ([]entry, error) {
	var entries []entry
	add := func(path, name string, mode fs.FileMode) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entries = append(entries, entry{name: filepath.ToSlash(name), data: data, mode: mode})
		return nil
	}
	if err := add(binary, binaryName, 0o755); err != nil {
		return nil, fmt.Errorf("read binary: %w", err)
	}
	for _, name := range []string{"README.md", "LICENSE", "THIRD_PARTY_NOTICES"} {
		if err := add(filepath.Join(root, name), name, 0o644); err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
	}
	licenses := filepath.Join(root, "third_party")
	err := filepath.WalkDir(licenses, func(path string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if item.IsDir() || item.Name() != "LICENSE" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return add(path, relative, 0o644)
	})
	if err != nil {
		return nil, fmt.Errorf("collect licenses: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	return entries, nil
}

func writeTarGzip(path, base string, entries []entry) (returnErr error) {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); returnErr == nil {
			returnErr = err
		}
	}()
	gzipWriter, err := gzip.NewWriterLevel(file, gzip.BestCompression)
	if err != nil {
		return err
	}
	gzipWriter.Header.ModTime = time.Unix(0, 0)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, item := range entries {
		header := &tar.Header{Name: base + "/" + item.name, Mode: int64(item.mode.Perm()), Size: int64(len(item.data)), ModTime: time.Unix(0, 0)}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if _, err := tarWriter.Write(item.data); err != nil {
			return err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return err
	}
	return gzipWriter.Close()
}

func writeZip(path, base string, entries []entry) (returnErr error) {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); returnErr == nil {
			returnErr = err
		}
	}()
	writer := zip.NewWriter(file)
	for _, item := range entries {
		header := &zip.FileHeader{Name: base + "/" + item.name, Method: zip.Deflate}
		header.SetMode(item.mode)
		header.SetModTime(time.Unix(0, 0))
		destination, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		if _, err := io.Copy(destination, bytes.NewReader(item.data)); err != nil {
			return err
		}
	}
	return writer.Close()
}
