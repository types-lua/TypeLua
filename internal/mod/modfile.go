package mod

import (
	"fmt"
	"sort"

	"github.com/typelua/typelua/internal/toml"
)

// ParseManifest parses tlua.toml content.
func ParseManifest(src string) (*Manifest, error) {
	d, err := toml.Decode(src)
	if err != nil {
		return nil, err
	}
	m := NewManifest("")
	if pt, ok := d.Tables["package"]; ok {
		if v, ok := pt.Values["name"]; ok {
			if v.Kind != toml.String {
				return nil, fmt.Errorf("package.name must be a string")
			}
			m.Name = v.Str
		}
		if v, ok := pt.Values["version"]; ok {
			if v.Kind != toml.String {
				return nil, fmt.Errorf("package.version must be a string")
			}
			m.Version = v.Str
		}
		for k := range pt.Values {
			if k != "name" && k != "version" {
				return nil, fmt.Errorf("unknown package key %q", k)
			}
		}
	}
	const depPrefix = "dependencies\x00"
	for key, t := range d.Tables {
		if len(key) <= len(depPrefix) || key[:len(depPrefix)] != depPrefix {
			continue
		}
		path := key[len(depPrefix):]
		dep := &Dep{}
		for k, v := range t.Values {
			if v.Kind != toml.String {
				return nil, fmt.Errorf("dependency %q: %q must be a string", path, k)
			}
			switch k {
			case "version":
				dep.Query = v.Str
			case "url":
				dep.URL = v.Str
			default:
				return nil, fmt.Errorf("dependency %q: unknown key %q", path, k)
			}
		}
		m.Deps[path] = dep
	}
	if rt, ok := d.Tables["replace"]; ok {
		for k, v := range rt.Values {
			if v.Kind != toml.String {
				return nil, fmt.Errorf("replace %q must be a string", k)
			}
			m.Replace[k] = v.Str
		}
	}
	// Reject unknown top-level tables (typo guard).
	// Bare [dependencies]/[replace] headers (empty sections) are allowed.
	for key := range d.Tables {
		if key == "" || key == "package" || key == "replace" || key == "dependencies" {
			continue
		}
		if len(key) > len(depPrefix) && key[:len(depPrefix)] == depPrefix {
			continue
		}
		return nil, fmt.Errorf("unknown table %q", key)
	}
	if len(d.Arrays) != 0 {
		return nil, fmt.Errorf("unexpected array table in manifest")
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// EncodeManifest renders tlua.toml deterministically.
func EncodeManifest(m *Manifest) string {
	var b toml.Builder
	b.Comment("tlua package manifest (tlua.toml)")
	b.Table("package")
	b.Str("name", m.Name)
	if m.Version != "" {
		b.Str("version", m.Version)
	}
	for _, p := range toml.SortedKeys(m.Deps) {
		b.Blank()
		b.Table("dependencies", p)
		d := m.Deps[p]
		b.Str("version", d.Query)
		if d.URL != "" {
			b.Str("url", d.URL)
		}
	}
	if len(m.Replace) != 0 {
		b.Blank()
		b.Table("replace")
		for _, p := range toml.SortedKeys(m.Replace) {
			b.Str(p, m.Replace[p])
		}
	}
	return b.String()
}

// ParseLock parses tlua.lock content.
func ParseLock(src string) (*Lock, error) {
	d, err := toml.Decode(src)
	if err != nil {
		return nil, err
	}
	l := NewLock()
	seen := map[string]bool{}
	for _, t := range d.Arrays["module"] {
		e := LockEntry{}
		for k, v := range t.Values {
			switch k {
			case "path":
				if v.Kind != toml.String {
					return nil, fmt.Errorf("module.path must be a string")
				}
				e.Path = v.Str
			case "version":
				if v.Kind != toml.String {
					return nil, fmt.Errorf("module.version must be a string")
				}
				e.Version = v.Str
			case "commit":
				if v.Kind != toml.String {
					return nil, fmt.Errorf("module.commit must be a string")
				}
				e.Commit = v.Str
			case "direct":
				if v.Kind != toml.Boolean {
					return nil, fmt.Errorf("module.direct must be a boolean")
				}
				e.Direct = v.Bool
			default:
				return nil, fmt.Errorf("unknown module key %q", k)
			}
		}
		if e.Path == "" {
			return nil, fmt.Errorf("module entry without path")
		}
		if seen[e.Path] {
			return nil, fmt.Errorf("duplicate lock entry %q", e.Path)
		}
		seen[e.Path] = true
		l.Set(e)
	}
	if len(d.Tables) > 1 || (len(d.Tables) == 1 && !hasOnlyRoot(d)) {
		return nil, fmt.Errorf("unexpected table in lockfile")
	}
	if err := l.Validate(); err != nil {
		return nil, err
	}
	return l, nil
}

func hasOnlyRoot(d *toml.Doc) bool {
	for k := range d.Tables {
		if k != "" {
			return false
		}
	}
	return true
}

// EncodeLock renders tlua.lock deterministically.
func EncodeLock(l *Lock) string {
	var b toml.Builder
	b.Comment("tlua lockfile (tlua.lock) - exact pins; edit via `tlua mod get`")
	entries := append([]LockEntry(nil), l.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	for i, e := range entries {
		if i > 0 {
			b.Blank()
		}
		b.ArrayTable("module")
		b.Str("path", e.Path)
		b.Str("version", e.Version)
		b.Str("commit", e.Commit)
		b.Bool("direct", e.Direct)
	}
	return b.String()
}
