package external

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
)

const registryVersion = 1
const registryLimit = 8 << 20

type record struct {
	Manifest v1.Manifest `json:"manifest"`
	Package  string      `json:"package,omitempty"`
	Enabled  bool        `json:"enabled"`
	Grants   []v1.Grant  `json:"grants"`
}
type registry struct {
	Version int      `json:"version"`
	Plugins []record `json:"plugins"`
}

func loadRegistry(path string) (map[string]record, error) {
	entries := map[string]record{}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return entries, nil
	}
	if err != nil {
		return entries, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, registryLimit+1))
	if err != nil {
		return entries, err
	}
	if len(data) > registryLimit {
		return entries, errors.New("plugin: registry too large")
	}
	var r registry
	if err = strict(data, &r); err != nil {
		return entries, err
	}
	if r.Version != registryVersion {
		return entries, errors.New("plugin: unsupported registry version")
	}
	for _, e := range r.Plugins {
		if err := ValidateManifest(e.Manifest); err != nil {
			return map[string]record{}, err
		}
		if _, ok := entries[e.Manifest.ID]; ok {
			return map[string]record{}, errors.New("plugin: duplicate registry ID")
		}
		if e.Package != "" && (filepath.Base(e.Package) != e.Package || e.Package == "." || e.Package == "..") {
			return map[string]record{}, errors.New("plugin: invalid package path")
		}
		if e.Package == "" && e.Enabled {
			return map[string]record{}, errors.New("plugin: uninstalled plugin enabled")
		}
		for _, g := range e.Grants {
			if err := ValidateGrant(g); err != nil {
				return map[string]record{}, err
			}
		}
		entries[e.Manifest.ID] = e
	}
	return entries, nil
}
func saveRegistry(path string, entries map[string]record) error {
	r := registry{Version: registryVersion, Plugins: make([]record, 0, len(entries))}
	for _, e := range entries {
		r.Plugins = append(r.Plugins, e)
	}
	sort.Slice(r.Plugins, func(i, j int) bool { return r.Plugins[i].Manifest.ID < r.Plugins[j].Manifest.ID })
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > registryLimit {
		return errors.New("plugin: registry too large")
	}
	return atomicSave(path, append(data, '\n'))
}
func atomicSave(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".registry-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = replaceFile(temp, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}

// Packages are immutable copies. Reject symlinks and special files, including
// nested symlinks; entrypoints cannot escape the installed package.
func copyPackage(source, destination string) error {
	total := int64(0)
	files := 0
	return filepath.WalkDir(source, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("plugin: unsupported package file %s", rel)
		}
		files++
		total += info.Size()
		if files > 10000 || total > 256<<20 {
			return errors.New("plugin: package limit exceeded")
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()&0777)
		if err != nil {
			return err
		}
		_, err = io.Copy(output, io.LimitReader(input, (256<<20)+1))
		if err == nil {
			err = output.Sync()
		}
		return errors.Join(err, output.Close())
	})
}
