package types

import (
	"sort"
)

// ---------- constructors ----------

// NewUnion flattens nested unions, removes duplicates and never, handles any/unknown.
func NewUnion(types []Type) Type {
	var flat []Type
	for _, t := range types {
		if t == nil {
			continue
		}
		if u, ok := t.(*Union); ok {
			flat = append(flat, u.Types...)
			continue
		}
		if t.Kind() == KNever {
			continue
		}
		flat = append(flat, t)
	}
	// any absorbs everything; unknown absorbs (except any).
	for _, t := range flat {
		if t.Kind() == KAny {
			return Any
		}
	}
	for _, t := range flat {
		if t.Kind() == KUnknown {
			return Unknown
		}
	}
	// Dedup by string.
	seen := map[string]bool{}
	var out []Type
	for _, t := range flat {
		s := t.String()
		if !seen[s] {
			seen[s] = true
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return Never
	}
	if len(out) == 1 {
		return out[0]
	}
	// Canonical order for stable messages.
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return &Union{Types: out}
}

// NewIntersection flattens; never absorbs; any is identity.
func NewIntersection(types []Type) Type {
	var flat []Type
	for _, t := range types {
		if t == nil {
			continue
		}
		if u, ok := t.(*Intersection); ok {
			flat = append(flat, u.Types...)
			continue
		}
		if t.Kind() == KAny {
			continue
		}
		if t.Kind() == KNever {
			return Never
		}
		flat = append(flat, t)
	}
	if len(flat) == 0 {
		return Unknown
	}
	seen := map[string]bool{}
	var out []Type
	for _, t := range flat {
		s := t.String()
		if !seen[s] {
			seen[s] = true
			out = append(out, t)
		}
	}
	if len(out) == 1 {
		return out[0]
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return &Intersection{Types: out}
}

// RemoveNil returns T without nil, and whether nil was present.
func RemoveNil(t Type) (Type, bool) {
	u, ok := t.(*Union)
	if !ok {
		if t.Kind() == KNil {
			return Never, true
		}
		return t, false
	}
	var rest []Type
	found := false
	for _, m := range u.Types {
		if m.Kind() == KNil {
			found = true
			continue
		}
		rest = append(rest, m)
	}
	if !found {
		return t, false
	}
	return NewUnion(rest), true
}

// ContainsNil reports whether nil is in the type.
func ContainsNil(t Type) bool {
	if t.Kind() == KNil {
		return true
	}
	if u, ok := t.(*Union); ok {
		for _, m := range u.Types {
			if m.Kind() == KNil {
				return true
			}
		}
	}
	return false
}
