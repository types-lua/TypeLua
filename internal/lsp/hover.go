package lsp

import (
	"encoding/json"

	"github.com/typelua/typelua/internal/types"
)

// Hover and go-to-definition.
//
// 设计者视角：悬停/定义皆经 useAt 三岔——值符显 `kind name: abbrev`，成员显
// `kind Owner.Name`，类型显 `entryKind TypeName`。定义再按成员/类型/值各取位。
// 障碍是三命名空间并存：Member/Entry/Sym 分流不可串。候选：类型全展 vs abbrev；
// 选 abbrev 防长串。不变量：无命中返 nil（无悬停）。排查现场：悬停空先查 useAt。

// ---------- hover & definition ----------

func (s *Server) hover(raw json.RawMessage) any {
	doc, pos, ok := s.docAt(raw)
	if !ok {
		return nil
	}
	u := doc.useAt(pos)
	if u == nil {
		return nil
	}
	var md string
	if u.Sym != nil && u.Member == nil {
		md = "```tlua\n" + u.Sym.KindString() + " " + u.Sym.Name + ": " + types.Abbrev(u.Sym.Type.String()) + "\n```"
	} else if u.Member != nil {
		kind := u.Member.Kind
		if kind == "" {
			kind = "member"
		}
		md = "```tlua\n" + kind + " " + u.Member.OwnerName + "." + u.Member.Name + "\n```"
	} else if u.Entry != nil {
		md = "```tlua\n" + u.Entry.EntryKind() + " " + u.Entry.TypeName() + "\n```"
	} else {
		return nil
	}
	return map[string]any{"contents": map[string]any{"kind": "markdown", "value": md}}
}

type location struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

func (s *Server) definition(raw json.RawMessage) any {
	// 设计者视角：定义跳转分流——成员查 memberDefSpan，类型取 DeclSpan，值经
	// defSpanOf(Root)。障碍是结构成员无定义位：返 nil。候选：跳声明 vs 跳定义；
	// 选定义。不变量：单档恒单元素数组。排查现场：跳不到先查成员定义记录。
	doc, pos, ok := s.docAt(raw)
	if !ok {
		return nil
	}
	u := doc.useAt(pos)
	if u == nil {
		return nil
	}
	if u.Member != nil {
		if sp, ok := doc.memberDefSpan(u.Member); ok {
			return []location{{URI: doc.uri, Range: doc.toRange(sp)}}
		}
		return nil
	}
	if u.Entry != nil {
		return []location{{URI: doc.uri, Range: doc.toRange(u.Entry.DeclSpan())}}
	}
	if u.Sym != nil {
		return []location{{URI: doc.uri, Range: doc.toRange(doc.defSpanOf(u.Root))}}
	}
	return nil
}
