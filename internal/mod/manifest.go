// Package mod implements the TypeLua package system: a decentralized,
// git-based module manager with a TOML manifest (tlua.toml) and an exact
// lockfile (tlua.lock), in the spirit of go.mod/go.sum.
package mod

import (
	"fmt"
	"regexp"
	"strings"
)

// Manifest file names.
const (
	ManifestName = "tlua.toml"
	LockName     = "tlua.lock"
	// VendorDir holds vendored module sources for builds.
	VendorDir = "tlua_modules"
)

// pathRe validates module paths: host-like first segment plus slashes.
// The first segment must contain a dot (decentralized hosting) unless the
// dependency overrides the fetch URL.
var pathRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~%-]*/[A-Za-z0-9._~%-]+(/[A-Za-z0-9._~%-]+)*$`)

// ValidPath reports whether p is a plausible module path.
func ValidPath(p string) bool {
	if !pathRe.MatchString(p) {
		return false
	}
	if strings.Contains(p, "..") {
		return false
	}
	return true
}

// ValidHostPath additionally requires a dotted first segment.
func ValidHostPath(p string) bool {
	if !ValidPath(p) {
		return false
	}
	first := p
	if i := strings.Index(p, "/"); i >= 0 {
		first = p[:i]
	}
	return strings.Contains(first, ".")
}

// Dep is one manifest dependency: a version query plus an optional
// fetch-URL override (for self-hosted git or local testing).
type Dep struct {
	Query string // "latest", exact "v1.2.3", caret "^v1.2.0", branch, or commit SHA
	URL   string // optional override
}

// Manifest is tlua.toml.
type Manifest struct {
	Name    string
	Version string
	Deps    map[string]*Dep // module path -> requirement
	Replace map[string]string
}

// NewManifest creates an empty manifest.
func NewManifest(name string) *Manifest {
	return &Manifest{Name: name, Deps: map[string]*Dep{}, Replace: map[string]string{}}
}

// Validate checks manifest invariants.
func (m *Manifest) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("package name is empty")
	}
	for p, d := range m.Deps {
		if !ValidPath(p) {
			return fmt.Errorf("invalid module path %q", p)
		}
		if d.URL == "" && !ValidHostPath(p) {
			return fmt.Errorf("module path %q needs a dotted host or an explicit url", p)
		}
		if strings.TrimSpace(d.Query) == "" {
			return fmt.Errorf("module %q needs a version query", p)
		}
		if _, err := ParseQuery(d.Query); err != nil {
			return fmt.Errorf("module %q: %v", p, err)
		}
	}
	for p, r := range m.Replace {
		if !ValidPath(p) {
			return fmt.Errorf("invalid replace path %q", p)
		}
		if strings.TrimSpace(r) == "" {
			return fmt.Errorf("empty replace target for %q", p)
		}
	}
	return nil
}
