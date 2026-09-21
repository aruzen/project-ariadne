package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseArchives(t *testing.T) {
	root := t.TempDir()
	write := func(name, data string, mode os.FileMode) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("ariadne", "binary", 0o755)
	write("README.md", "readme", 0o644)
	write("LICENSE", "license", 0o644)
	write("THIRD_PARTY_NOTICES", "notices", 0o644)
	write("third_party/dependency/LICENSE", "dependency", 0o644)

	tarPath := filepath.Join(root, "release.tar.gz")
	if err := run([]string{"-root", root, "-binary", filepath.Join(root, "ariadne"), "-output", tarPath, "-version", "v1.0.0", "-os", "linux", "-arch", "amd64"}); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	gz, err := gzip.NewReader(file)
	if err != nil {
		file.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gz.Close() })
	reader := tar.NewReader(gz)
	foundExecutable := false
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "ariadne_v1.0.0_linux_amd64/ariadne" {
			foundExecutable = header.FileInfo().Mode()&0o111 != 0
		}
	}
	if !foundExecutable {
		t.Fatal("executable missing or not executable")
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(root, "release.zip")
	if err := run([]string{"-root", root, "-binary", filepath.Join(root, "ariadne"), "-output", zipPath, "-version", "v1.0.0", "-os", "windows", "-arch", "arm64"}); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = archive.Close() })
	foundLicense := false
	for _, item := range archive.File {
		if item.Name == "ariadne_v1.0.0_windows_arm64/third_party/dependency/LICENSE" {
			foundLicense = true
		}
	}
	if !foundLicense {
		t.Fatal("third-party license missing")
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsUnsafeOrUnsupportedArchiveNames(t *testing.T) {
	for _, arguments := range [][]string{
		{"-binary", "ariadne", "-output", "release.tar.gz", "-version", "../../escape", "-os", "linux", "-arch", "amd64"},
		{"-binary", "ariadne", "-output", "release.tar.gz", "-version", "v1.0.0", "-os", "linux", "-arch", "386"},
		{"-binary", "ariadne", "-output", "release.tar.gz", "-version", "v1.0.0", "-os", "plan9", "-arch", "amd64"},
	} {
		if err := run(arguments); err == nil {
			t.Fatalf("unsafe release arguments accepted: %v", arguments)
		}
	}
}
