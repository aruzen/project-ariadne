package external

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
)

var identifier = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

func strict(data []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func validID(id string) bool {
	return len(id) <= 96 && identifier.MatchString(id) && id != "ariadne" && id != "agent-marker"
}
func ValidateManifest(m v1.Manifest) error {
	if !validID(m.ID) || strings.TrimSpace(m.Version) == "" || len(m.Version) > 128 || m.APIVersion != v1.Version {
		return errors.New("plugin: invalid ID/version or unsupported API version")
	}
	if m.Runtime != "process" && m.Runtime != "native" {
		return errors.New("plugin: runtime must be process or native")
	}
	seen := map[v1.Capability]bool{}
	for _, c := range m.Capabilities {
		if !v1.ValidCapability(c) || seen[c] {
			return errors.New("plugin: invalid/duplicate capability")
		}
		seen[c] = true
	}
	for _, ds := range [][]v1.Declaration{m.Commands, m.Tools, m.Widgets} {
		names := map[string]bool{}
		for _, d := range ds {
			if len(d.Name) > 96 || !identifier.MatchString(d.Name) || names[d.Name] || len(d.Description) > 4096 {
				return errors.New("plugin: invalid/duplicate declaration")
			}
			names[d.Name] = true
		}
	}
	if len(m.Entrypoints) == 0 {
		return errors.New("plugin: entrypoint is required")
	}
	for target, ep := range m.Entrypoints {
		parts := strings.Split(target, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return errors.New("plugin: target must be OS/architecture")
		}
		if ep.Path == "" || filepath.IsAbs(ep.Path) || filepath.Clean(ep.Path) == ".." || strings.HasPrefix(filepath.Clean(ep.Path), ".."+string(filepath.Separator)) || strings.ContainsRune(ep.Path, 0) || strings.Contains(ep.Path, "\\") {
			return errors.New("plugin: entrypoint must be a relative package path")
		}
		if m.Runtime == "native" && len(ep.Args) != 0 {
			return errors.New("plugin: native entrypoint cannot have arguments")
		}
		for _, a := range ep.Args {
			if strings.ContainsRune(a, 0) {
				return errors.New("plugin: invalid argv")
			}
		}
	}
	return nil
}
func ReadManifest(dir string) (v1.Manifest, error) {
	var m v1.Manifest
	f, err := os.Open(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return m, err
	}
	if len(data) > 1<<20 {
		return m, errors.New("plugin: manifest exceeds 1MiB")
	}
	if err = strict(data, &m); err != nil {
		return m, fmt.Errorf("plugin: manifest: %w", err)
	}
	if err = ValidateManifest(m); err != nil {
		return m, err
	}
	ep, ok := m.Entrypoints[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return m, errors.New("plugin: no entrypoint for this OS/architecture")
	}
	info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(ep.Path)))
	if err != nil {
		return m, err
	}
	if !info.Mode().IsRegular() {
		return m, errors.New("plugin: entrypoint must be a regular file")
	}
	if m.Runtime == "process" && runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
		return m, errors.New("plugin: entrypoint is not executable")
	}
	if m.Runtime == "native" {
		if err := validateNative(filepath.Join(dir, filepath.FromSlash(ep.Path))); err != nil {
			return m, err
		}
	}
	return m, nil
}
func ValidateGrant(g v1.Grant) error {
	if !v1.ValidCapability(g.Capability) {
		return errors.New("plugin: unknown capability")
	}
	switch g.Scope.Kind {
	case "all", "context":
		if len(g.Scope.IDs) != 0 {
			return errors.New("plugin: scope cannot contain IDs")
		}
	case "workspace", "pane":
		if len(g.Scope.IDs) == 0 {
			return errors.New("plugin: scope requires IDs")
		}
		seen := map[uint64]bool{}
		for _, id := range g.Scope.IDs {
			if id == 0 || seen[id] {
				return errors.New("plugin: invalid scope IDs")
			}
			seen[id] = true
		}
	default:
		return errors.New("plugin: unknown scope")
	}
	if (g.Capability == v1.ClipboardRead || g.Capability == v1.ClipboardWrite || g.Capability == v1.FrontendInteract || g.Capability == v1.FrontendEditor) && g.Scope.Kind != "all" {
		return errors.New("plugin: global capability requires all scope")
	}
	if g.Capability == v1.FrontendNavigate && g.Scope.Kind == "context" {
		return errors.New("plugin: frontend navigation does not support context scope")
	}
	return nil
}
func declares(ds []v1.Declaration, name string) bool {
	for _, d := range ds {
		if d.Name == name {
			return true
		}
	}
	return false
}

// DecodeParameters performs strict decoding at the daemon broker boundary.
func DecodeParameters(data []byte, destination any) error { return strict(data, destination) }
