package types

import (
	"strings"
)

type FuncParam struct {
	Name     string
	Type     Type
	Optional bool
}

type Func struct {
	Params    []FuncParam
	HasVarArg bool
	VarArg    Type
	Returns   []Type
	// TypeParams records a generic declaration (constraints/defaults
	// intact) so explicit type arguments can bind positionally.
	TypeParams []*GenericParam
	// IsAsync marks `async` functions and async builtins (sleep, channel
	// send/recv, WaitGroup wait, Mutex lock): calling one outside an
	// async task errors at runtime (yield with no scheduler), so the
	// checker reports TL3805. Marker only: ignored by assignability.
	IsAsync bool
}

func (t *Func) Kind() Kind { return KFunc }
func (t *Func) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Func) stringSeen(seen map[Type]bool) string {
	var ps []string
	for _, p := range t.Params {
		if p.Name != "" {
			if p.Optional {
				ps = append(ps, p.Name+"?: "+typeString(p.Type, seen))
			} else {
				ps = append(ps, p.Name+": "+typeString(p.Type, seen))
			}
		} else {
			ps = append(ps, typeString(p.Type, seen))
		}
	}
	if t.HasVarArg {
		if t.VarArg != nil {
			ps = append(ps, "...: "+typeString(t.VarArg, seen))
		} else {
			ps = append(ps, "...")
		}
	}
	ret := "void"
	if len(t.Returns) == 1 {
		ret = typeString(t.Returns[0], seen)
	} else if len(t.Returns) > 1 {
		rs := make([]string, len(t.Returns))
		for i, r := range t.Returns {
			rs[i] = typeString(r, seen)
		}
		ret = "(" + strings.Join(rs, ", ") + ")"
	}
	return "(" + strings.Join(ps, ", ") + ") -> " + ret
}
