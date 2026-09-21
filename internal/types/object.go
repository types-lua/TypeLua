package types

import (
	"fmt"
	"sort"
	"strings"
)

type Object struct {
	Name     string // interface name if named, else ""
	Fields   map[string]Field
	Indexers []Indexer
}

func (t *Object) Kind() Kind { return KObject }
func (t *Object) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Object) stringSeen(seen map[Type]bool) string {
	if t.Name != "" && len(t.Fields) == 0 && len(t.Indexers) == 0 {
		return t.Name
	}
	var parts []string
	names := make([]string, 0, len(t.Fields))
	for n := range t.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f := t.Fields[n]
		q := ""
		if f.Optional {
			q = "?"
		}
		parts = append(parts, fmt.Sprintf("%s%s: %s", n, q, typeString(f.Type, seen)))
	}
	for _, ix := range t.Indexers {
		parts = append(parts, fmt.Sprintf("[%s]: %s", typeString(ix.Key, seen), typeString(ix.Value, seen)))
	}
	if t.Name != "" {
		return t.Name + "{ " + strings.Join(parts, ", ") + " }"
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

type Enum struct {
	Name       string
	Underlying Type // string or number (or union)
	Members    map[string]Type
}

func (t *Enum) Kind() Kind { return KEnum }
func (t *Enum) String() string { return t.Name }
