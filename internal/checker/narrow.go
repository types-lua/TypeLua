package checker

import (
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// 窄化是分支眼镜：`if x~=nil` 真支去 nil，出块即弃。
//
// 设计者视角：窄化是分支内的“临时眼镜”——`if x~=nil` 真支把 x 看窄，经影子 Symbol
// （SameAs 链回定义）实现，出块即弃。支持四镜：nil（去/留 nil）、typeof（type(x)
// 比字）、truthy（Lua 唯 nil/false 为假）、lit（串字面等）。成员径（a.b["c"]）
// 经 refineRootType 重建（Object/Class 才重建，余者不窄）。障碍是 and 链叠加：
// Ops 逐个对当前（已影）绑定施，故组合不互盖。取舍：不窄即弃（ok=false），不报错。
// …（略，详见实现；排查先看 Span/诊断）
//
// 设计者视角：narrowKind 是四镜的“档位”——nil/typeof/truthy/lit 各对一 refine*。
// iota 序即档位，无语义。排查现场：窄化走错分支先查 narrowCond 置 kind 是否错档。
type narrowKind int

const (
	narrowNil narrowKind = iota
	narrowTypeof
	narrowTruthy
	narrowLit
)

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// current (possibly already shadowed) binding, so `and`-chained refinements
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：narrowOp 是一次“磨镜单”——root 为根变量，fields 为成员链（空即整根），
// kind+载荷定磨法。序贯施于已影绑定，故 `x~=nil and type(x)=="t"` 双镜叠加不互盖。
// 取舍：Op 纯值（无指针），narrowCond 产、applyNarrowing 销。排查现场：二次窄化
// 覆盖首镜，先查是否对原定义而非影子施（应对影子施）。

type narrowOp struct {
	root   string
	fields []string
	kind   narrowKind
	isNil  bool   // narrowNil: narrow the member to nil
	lit    string // narrowTypeof: `type()` literal
	toType bool   // narrowTypeof: narrow towards (vs away from) lit
	truthy bool   // narrowTruthy: Lua truthiness branch
	rawLit string // narrowLit: raw string-literal spelling (`"a"`)
	toLit  bool   // narrowLit: narrow towards (vs away from) the literal
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：applyNarrowing 是磨镜的“施工队”——narrowCond 产 Ops，逐个 lookup 根
// （无根即弃），经 refineRootType 算新体，影子入 cur.vars（SameAs 链根，Kind/Const/
// Builtin 照搬）。障碍是根不存在（未定义变量）：静弃（错误另处已报）。取舍：
// 影子覆盖同层同名（出块 pop 即弃）。不变量：失败（ok=false）不写层。排查现场：
// 分支内类型未变先查 refine 是否 false（如 Map/原语径）。

func (c *Checker) applyNarrowing(cond ast.Expr, truthy bool) {
	for _, op := range c.narrowCond(cond, truthy) {
		sym := c.cur().lookupVar(op.root)
		if sym == nil {
			continue
		}
		var newT types.Type
		var ok bool
		switch op.kind {
		case narrowNil:
			newT, ok = refineRootType(sym.Type, op.fields, func(cur types.Type) (types.Type, bool) {
				return narrowNilOfType(cur, op.isNil)
			})
		case narrowTypeof:
			newT, ok = refineRootType(sym.Type, op.fields, func(cur types.Type) (types.Type, bool) {
				return narrowTypeofOfType(cur, op.lit, op.toType)
			})
		case narrowTruthy:
			newT, ok = refineRootType(sym.Type, op.fields, func(cur types.Type) (types.Type, bool) {
				return narrowTruthyOfType(cur, op.truthy)
			})
		case narrowLit:
			newT, ok = refineRootType(sym.Type, op.fields, func(cur types.Type) (types.Type, bool) {
				return narrowLitOfType(cur, op.rawLit, op.toLit)
			})
		}
		if !ok {
			continue
		}
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		c.cur().vars[op.root] = &Symbol{Name: op.root, Kind: sym.Kind, Type: newT, Span: sym.Span, IsConst: sym.IsConst, IsBuiltin: sym.IsBuiltin, SameAs: symRoot(sym)}
	}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：narrowCond 是条件的“译码器”——剥括号/取反（not 翻 truthy），==/~=
// 分 nil 径（整根/成员径）、typeof 径、字面径，and 真支双收、or 假支双收，余者作
// truthy 径（`if x`/`if a.b`）。障碍是 `==` 与 truthy 的双向翻转（isEq==truthy
// 即向 nil/字）：翻错一位即反窄。候选：全 AST 覆盖 vs 五式；选五式（余者 truthy
// 兜底）。取舍：不识即 nil（不窄）。不变量：返回 nil 即无窄。排查现场：`x==nil`
// 未窄先查 asIdent/narrowPath 是否认径。
func (c *Checker) narrowCond(cond ast.Expr, truthy bool) []narrowOp {
	// Strip parens and `not`.
	if p, ok := cond.(*ast.ParenExpr); ok {
		return c.narrowCond(p.E, truthy)
	}
	if u, ok := cond.(*ast.UnOpExpr); ok && u.Op == token.Not {
		return c.narrowCond(u.X, !truthy)
	}
	if b, ok := cond.(*ast.BinOpExpr); ok {
		// `x == nil` / `x ~= nil`
		if b.Op == token.Eq || b.Op == token.Ne {
			isEq := b.Op == token.Eq
			wantTruthy := isEq == truthy // e.g. `x == nil` true => x is nil.
			if id, ok := asIdent(b.L); ok && isNilLit(b.R) {
				return []narrowOp{{root: id, kind: narrowNil, isNil: wantTruthy}}
			}
			if id, ok := asIdent(b.R); ok && isNilLit(b.L) {
				return []narrowOp{{root: id, kind: narrowNil, isNil: wantTruthy}}
			}
			// Member paths (`node.next == nil`): refine the root.
			if root, fields, ok := narrowPath(b.L); ok && len(fields) > 0 && isNilLit(b.R) {
				return []narrowOp{{root: root, fields: fields, kind: narrowNil, isNil: wantTruthy}}
			}
			if root, fields, ok := narrowPath(b.R); ok && len(fields) > 0 && isNilLit(b.L) {
				return []narrowOp{{root: root, fields: fields, kind: narrowNil, isNil: wantTruthy}}
			}
			// `type(x) == "s"` / `~=`.
			if tn, lit, ok := asTypeofCompare(b); ok {
				isEqualOp := b.Op == token.Eq
				narrowTo := (isEqualOp == truthy)
				return []narrowOp{{root: tn, kind: narrowTypeof, lit: lit, toType: narrowTo}}
			}
			if root, fields, lit, ok := asTypeofPathCompare(b); ok {
				isEqualOp := b.Op == token.Eq
				narrowTo := (isEqualOp == truthy)
				return []narrowOp{{root: root, fields: fields, kind: narrowTypeof, lit: lit, toType: narrowTo}}
			}
			// `x == "lit"` / `~=` for plain idents and member paths.
			if id, lit, ok := asLitCompare(b.L, b.R); ok {
				isEqualOp := b.Op == token.Eq
				return []narrowOp{{root: id, kind: narrowLit, rawLit: lit, toLit: isEqualOp == truthy}}
			}
			if id, lit, ok := asLitCompare(b.R, b.L); ok {
				isEqualOp := b.Op == token.Eq
				return []narrowOp{{root: id, kind: narrowLit, rawLit: lit, toLit: isEqualOp == truthy}}
			}
			if root, fields, lit, ok := asLitPathCompare(b.L, b.R); ok {
				isEqualOp := b.Op == token.Eq
				return []narrowOp{{root: root, fields: fields, kind: narrowLit, rawLit: lit, toLit: isEqualOp == truthy}}
			}
			if root, fields, lit, ok := asLitPathCompare(b.R, b.L); ok {
				isEqualOp := b.Op == token.Eq
				return []narrowOp{{root: root, fields: fields, kind: narrowLit, rawLit: lit, toLit: isEqualOp == truthy}}
			}
		}
		// `a and b`: in true branch both hold; in false unknown.
		if b.Op == token.And && truthy {
			return append(c.narrowCond(b.L, true), c.narrowCond(b.R, true)...)
		}
		if b.Op == token.Or && !truthy {
			return append(c.narrowCond(b.L, false), c.narrowCond(b.R, false)...)
		}
	}
	// Truthiness: `if x then` / `if node.next then` (only nil/false are falsy).
	if root, fields, ok := narrowPath(cond); ok {
		return []narrowOp{{root: root, fields: fields, kind: narrowTruthy, truthy: truthy}}
	}
	return nil
}

// narrowPath decomposes a narrowable lvalue (`x`, `x.y`, `x["y"]`) into
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：narrowPath 是成员径的“拆轨器”——Ident 即根，Field/串索引逐节接链，
// Paren 透视；非串键、调用、算术一律 false。障碍是 `["a'b"]` 引号：经 unquoteKey
// 取内容，失败即弃。取舍：只认静态串键（动态键不窄）。不变量：ok 真则 root 非空。
// 排查现场：成员窄化不生效多为键非字面（设计如此）。

func narrowPath(e ast.Expr) (root string, fields []string, ok bool) {
	switch n := e.(type) {
	case *ast.Ident:
		return n.Name, nil, true
	case *ast.ParenExpr:
		return narrowPath(n.E)
	case *ast.FieldExpr:
		r, fs, ok := narrowPath(n.Obj)
		if !ok {
			return "", nil, false
		}
		return r, append(fs, n.Field.Name), true
	case *ast.IndexExpr:
		sl, isStr := n.Key.(*ast.StringLit)
		if !isStr {
			return "", nil, false
		}
		s, _, _, ok := unquoteKey(sl.Literal)
		if !ok {
			return "", nil, false
		}
		r, fs, ok := narrowPath(n.Obj)
		if !ok {
			return "", nil, false
		}
		return r, append(fs, s), true
	}
	return "", nil, false
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：refineRootType 是窄化的“重建机”——空链即对整根施 refine；union 逐员
// 试（有成即并）；Object/Class 拷贝重建首节（经 refineMemberField 折 optional），
// 余者 false。障碍是 optional 的存/观二态：存为 (T,optional)，观为 T|nil，须经
// refineMemberField 来回折（见该函）。候选：原地改 vs 拷贝；选拷贝（原定义不动，
// 影子独享）。不变量：false 即不窄不断流。排查现场：成员窄化无感多为 Map/原语径
// （设计不重建）。

func refineRootType(t types.Type, fields []string, refine func(types.Type) (types.Type, bool)) (types.Type, bool) {
	if len(fields) == 0 {
		return refine(t)
	}
	switch x := t.(type) {
	case *types.Union:
		var rs []types.Type
		changed := false
		for _, m := range x.Types {
			r, ok := refineRootType(m, fields, refine)
			if !ok {
				rs = append(rs, m)
				continue
			}
			changed = true
			rs = append(rs, r)
		}
		if !changed {
			return t, false
		}
		return types.NewUnion(rs), true
	case *types.Object:
		f, ok := x.Fields[fields[0]]
		if !ok {
			return t, false
		}
		ft, opt, ok := refineMemberField(f.Type, f.Optional, fields[1:], refine)
		if !ok {
			return t, false
		}
		cp := *x
		cp.Fields = make(map[string]types.Field, len(x.Fields))
		for k, v := range x.Fields {
			cp.Fields[k] = v
		}
		nf := f
		nf.Type, nf.Optional = ft, opt
		cp.Fields[fields[0]] = nf
		return &cp, true
	case *types.Class:
		cf, ok := x.Fields[fields[0]]
		if !ok {
			return t, false
		}
		ft, opt, ok := refineMemberField(cf.Type, cf.Optional, fields[1:], refine)
		if !ok {
			return t, false
		}
		cp := *x
		cp.Fields = make(map[string]types.ClassField, len(x.Fields))
		for k, v := range x.Fields {
			cp.Fields[k] = v
		}
		ncf := cf
		ncf.Type, ncf.Optional = ft, opt
		cp.Fields[fields[0]] = ncf
		return &cp, true
	}
	return t, false
}

// refineMemberField narrows one field's effective access type (adding `nil`
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：refineMemberField 是可选字段的“折叠术”——观型=存型(+nil 若 optional)，
// 对观型递归 refineRootType，再剥 nil 折回 (存型,optional)。障碍是 `x.y==nil` 后
// optional 应灭（hadNil 假即 optional=false）与 `x.y~=nil` 后仍 optional 的区分：
// 靠 RemoveNil 的 hadNil 判。取舍：递归复用 refineRootType（深径自通）。不变量：
// ok 真则 (存型,optional) 自洽。排查现场：可选窄化后仍 optional，先查 hadNil 分支。

func refineMemberField(ft types.Type, optional bool, rest []string, refine func(types.Type) (types.Type, bool)) (types.Type, bool, bool) {
	eff := ft
	if optional {
		eff = types.NewUnion([]types.Type{ft, types.Nil})
	}
	sub, ok := refineRootType(eff, rest, refine)
	if !ok {
		return nil, false, false
	}
	if !optional {
		return sub, false, true
	}
	stripped, hadNil := types.RemoveNil(sub)
	if !hadNil {
		return sub, false, true
	}
	return stripped, true, true
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：asIdent 是窄化的“快道”——Ident 即根，非 Ident 即 false（成员径另走
// narrowPath）。薄断言。排查现场：整根未窄先查此处是否把 Field 误送快道。
func asIdent(e ast.Expr) (string, bool) {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name, true
	}
	return "", false
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：isNilLit 是 nil 窄化的“锚点”——唯 NilLit 为真，余者假。`x==nil` 的
// 一半即此。排查现场：nil 比较未窄先查 parser 是否把 nil 包成 Paren（narrowCond
// 已剥括号，此处只看裸）。
func isNilLit(e ast.Expr) bool {
	_, ok := e.(*ast.NilLit)
	return ok
}

// matchTypeofCall matches `type(arg) == "lit"` shapes, returning the
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：matchTypeofCall 是 typeof 窄化的“嗅探器”——被调须为裸 `type` 单参，
// 另端须为串字面（经 unquote 取内容）。障碍是 `type` 被局部遮蔽仍形似：此处只认形，
// 语义遮蔽由外层验（误窄率极低）。取舍：形认（快）vs 义认（准）；选形认。排查现场：
// typeof 未窄先查是否 `type (x)` 多空格仍 Call（是）或被包 Paren（narrowCond 已剥外层）。

func matchTypeofCall(call, lit ast.Expr) (ast.Expr, string, bool) {
	ce, ok := call.(*ast.CallExpr)
	if !ok {
		return nil, "", false
	}
	fn, ok := ce.Func.(*ast.Ident)
	if !ok || fn.Name != "type" || len(ce.Args) != 1 {
		return nil, "", false
	}
	sl, ok := lit.(*ast.StringLit)
	if !ok {
		return nil, "", false
	}
	s, _, _, ok := unquoteKey(sl.Literal)
	if !ok {
		return nil, "", false
	}
	return ce.Args[0], s, true
}

// asTypeofCompare matches `type(x) == "lit"` forms, returning (varname, literal, true).
//
// 设计者视角：asTypeofCompare 是整根 typeof 的“双向嗅”——左右互换各试（`type(x)=="s"`
// 与 `"s"==type(x)` 等价）。内层须为 Ident（成员径另有 Path 版）。取舍：只认整根，
// 成员不抢。排查现场：typeof 整根未窄先查是否成员径（应走 Path 版）。

func asTypeofCompare(b *ast.BinOpExpr) (string, string, bool) {
	if arg, s, ok := matchTypeofCall(b.L, b.R); ok {
		if name, isId := asIdent(arg); isId {
			return name, s, true
		}
	}
	if arg, s, ok := matchTypeofCall(b.R, b.L); ok {
		if name, isId := asIdent(arg); isId {
			return name, s, true
		}
	}
	return "", "", false
}

// asTypeofPathCompare matches `type(path.to.member) == "lit"` forms.
//
// 设计者视角：与上对偶，专吃成员径（fields 非空才认）。整根与成员分流，防 Ops
// 冲突。排查现场：`type(a.b)=="t"` 未窄先查 narrowPath 是否认径。

func asTypeofPathCompare(b *ast.BinOpExpr) (string, []string, string, bool) {
	if arg, s, ok := matchTypeofCall(b.L, b.R); ok {
		if root, fields, isPath := narrowPath(arg); isPath && len(fields) > 0 {
			return root, fields, s, true
		}
	}
	if arg, s, ok := matchTypeofCall(b.R, b.L); ok {
		if root, fields, isPath := narrowPath(arg); isPath && len(fields) > 0 {
			return root, fields, s, true
		}
	}
	return "", nil, "", false
}

// asLitCompare matches `x == "lit"` forms, returning the variable name
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：asLitCompare 是串字面窄化的整根版——目标须 Ident，字面须 StringLit，
// 返 raw 拼写（比较时 norm 消引号差异，`"a"` 配 `'a'`）。障碍是拼写差异：存 raw
// 而非内容，交 narrowLitOfType 统归一。排查现场：串比较未窄先查字面是否非串（如数字）。

func asLitCompare(target, lit ast.Expr) (string, string, bool) {
	id, ok := asIdent(target)
	if !ok {
		return "", "", false
	}
	sl, ok := lit.(*ast.StringLit)
	if !ok {
		return "", "", false
	}
	return id, sl.Literal, true
}

// asLitPathCompare matches `path.to.member == "lit"` forms.
//
// 设计者视角：同上之成员径版（fields 非空）。整根/成员双轨并立，Ops 各记其径，
// refine 时各重建其链。排查现场：成员串比较未窄先查径是否含动态键。

func asLitPathCompare(target, lit ast.Expr) (string, []string, string, bool) {
	root, fields, ok := narrowPath(target)
	if !ok || len(fields) == 0 {
		return "", nil, "", false
	}
	sl, ok := lit.(*ast.StringLit)
	if !ok {
		return "", nil, "", false
	}
	return root, fields, sl.Literal, true
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：normStrLit 消引号差异（`"a"` vs `'a'` 同值），供 narrowLitOfType
// 按值比对。经 parseStringLiteral 脱壳再 Trim。障碍是转义：保持 raw 级（源忠实），
// 不求全等。排查现场：字面窄化误判先查两端是否同经此归一。
func normStrLit(raw string) string {
	s, _ := parseStringLiteral(raw)
	return strings.Trim(s, `"'`)
}

// normStrLit如 `f(1)`，排查看 Span。
//
// 设计者视角：isStringPrimitive 辨“宽串”（string 原语）与“窄串”（字面）：宽串
// 可向任一字面窄（`x=="a"` 真支即字面），窄串须命中才窄。any 恒假（any 不窄，
// 见 narrowLitOfType）。排查现场：宽串未窄成字面先查此处是否误把字面当宽串。
func isStringPrimitive(t types.Type) bool {
	if _, ok := t.(*types.Literal); ok {
		return false
	}
	return t.Kind() == types.KString && !isAnyLike(t)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：hasStringPrimitive 是 union 向字面窄的“放行证”——含宽串即允许凭空
// 造字面（`s=="a"` 真支得字面），否则只复用既有成员（防无中生有）。排查现场：
// union 窄化凭空出字面，先查此处是否误含宽串。
func hasStringPrimitive(ms []types.Type) bool {
	for _, m := range ms {
		if isStringPrimitive(m) {
			return true
		}
	}
	return false
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// (`"a"` matches `'a'`); results reuse existing members (never synthesized),
// and `any` stays `any`.
//
// 设计者视角：narrowLitOfType 是串字面的“筛子”——向字（==真/~=假）：union 取命中
// 成员（无命中但有宽串即造字面），整根命中即留、宽串即造；背字则剔命中（剩空即
// Never）。障碍是 any：恒不窄详见实现，排查看 Span。
// 优先（指针稳定），宽串才合成。不变量：false 即不窄。排查现场：`s~="a"` 未剔
// 先查 norm 是否归一。

func narrowLitOfType(cur types.Type, rawLit string, toLit bool) (types.Type, bool) {
	want := normStrLit(rawLit)
	match := func(m types.Type) bool {
		lit, ok := m.(*types.Literal)
		if !ok || lit.Base == nil || lit.Base.Kind() != types.KString {
			return false
		}
		return normStrLit(lit.Value) == want
	}
	if toLit {
		if u, ok := cur.(*types.Union); ok {
			for _, m := range u.Types {
				if match(m) {
					return m, true
				}
			}
			if hasStringPrimitive(u.Types) {
				return &types.Literal{Value: rawLit, Base: types.String}, true
			}
			return nil, false
		}
		if match(cur) {
			return cur, true
		}
		if isStringPrimitive(cur) {
			return &types.Literal{Value: rawLit, Base: types.String}, true
		}
		return nil, false
	}
	if u, ok := cur.(*types.Union); ok {
		var rest []types.Type
		for _, m := range u.Types {
			if !match(m) {
				rest = append(rest, m)
			}
		}
		if len(rest) != len(u.Types) {
			return types.NewUnion(rest), true
		}
		return nil, false
	}
	if match(cur) {
		return types.Never, true
	}
	return nil, false
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：narrowNilOfType 是最常用的“去 nil 刀”——向 nil 即 Nil，背 nil 即
// RemoveNil（无 nil 即 false 不窄）。`if x then return end` 后的 x 即此刀所削。
// 障碍是纯 nil 剥后 Never：显式化 Never（后续不可达可报）。取舍：any 经 RemoveNil
// 亦有 nil？any 无 nil 即 false（any 不窄）。排查现场：去 nil 未生效先查原是否含 nil。

func narrowNilOfType(cur types.Type, isNil bool) (types.Type, bool) {
	if isNil {
		return types.Nil, true
	}
	stripped, hadNil := types.RemoveNil(cur)
	if !hadNil {
		return nil, false
	}
	if stripped.Kind() == types.KNever {
		stripped = types.Never
	}
	return stripped, true
}

// narrowTypeofOfType narrows a type by a `type(x)` literal comparison
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：narrowTypeofOfType 是 typeof 的“筛子”——向型（==真）即取 prim
// （any 亦取，余者恒取 prim 以显下游）；背型则自 union 剔同族（Kind 快道+字面
// Base+String 三保险），整根命中即 Never。障碍是未知字详见实现，排查看 Span。
// 即不窄。候选：交集精算 vs 取 prim；选取 prim（简单 sound，下游可赋性自验）。
// 不变量：未知字恒 false。排查现场：`type(x)~="foo"` 未窄多为未知字（设计不窄）。

func narrowTypeofOfType(cur types.Type, lit string, toType bool) (types.Type, bool) {
	prim := typeofLiteralToType(lit)
	if prim == nil {
		return nil, false
	}
	if toType {
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		if isAnyLike(cur) {
			return prim, true
		}
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		return prim, true
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if u, ok := cur.(*types.Union); ok {
		var rest []types.Type
		for _, m := range u.Types {
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			if m.Kind() == prim.Kind() {
				continue
			}
			// Literal members of that base? e.g. orig `"a" | "b"`, prim string => all removed => Never. Keep logic: if prim is base and member is literal of base, remove.
			if litM, ok := m.(*types.Literal); ok && (litM.Base.Kind() == prim.Kind() || litM.Base.String() == prim.String()) {
				continue
			}
			if m.String() == prim.String() {
				continue
			}
			rest = append(rest, m)
		}
		if len(rest) != len(u.Types) {
			return types.NewUnion(rest), true
		}
		return nil, false
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if cur.Kind() == prim.Kind() {
		// `type(x) ~= "string"` but x: string => unreachable else? Narrow to Never.
		return types.Never, true
	}
	if cur.String() == prim.String() {
		// `type(x) ~= "string"` but x: string => unreachable else? Narrow to Never.
		return types.Never, true
	}
	return nil, false
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：narrowTruthyOfType 是真值性的“留真去假”——真支去 nil/false（boolean
// 剩 true 字面），假支留 nil/false（boolean 剩 false 字面），余者真留假弃。any
// 恒不窄。障碍是 boolean 的宽窄：必须裂为字面才精。取舍：无变化即 false（调用方
// 不写层）。排查现场：`if x` 后 boolean 未裂先查此处 Kind 分支。

func narrowTruthyOfType(cur types.Type, truthy bool) (types.Type, bool) {
	if isAnyLike(cur) {
		return nil, false
	}
	var ms []types.Type
	if u, ok := cur.(*types.Union); ok {
		ms = u.Types
	} else {
		ms = []types.Type{cur}
	}
	var keep []types.Type
	for _, m := range ms {
		switch {
		case m.Kind() == types.KNil:
			if !truthy {
				keep = append(keep, m)
			}
		case isFalseLit(m):
			if !truthy {
				keep = append(keep, m)
			}
		case isTrueLit(m):
			if truthy {
				keep = append(keep, m)
			}
		case m.Kind() == types.KBoolean:
			if truthy {
				keep = append(keep, &types.Literal{Value: "true", Base: types.Boolean})
			} else {
				keep = append(keep, &types.Literal{Value: "false", Base: types.Boolean})
			}
		default:
			if truthy {
				keep = append(keep, m)
			}
		}
	}
	if len(keep) == len(ms) {
		return nil, false
	}
	return types.NewUnion(keep), true
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：isFalseLit 是真值判的“原子”——须 Literal+Boolean 基+值 false 三齐。
// 供 narrowTruthyOfType 逐员判。排查现场：假支未留 false 先查字面拼写是否为 "false"。
func isFalseLit(t types.Type) bool {
	l, ok := t.(*types.Literal)
	return ok && l.Base == types.Boolean && l.Value == "false"
}

// isFalseLit如 `f(1)`，排查看 Span。
//
// 设计者视角：与上对偶，真支留 true 的判据。排查现场同上。
func isTrueLit(t types.Type) bool {
	l, ok := t.(*types.Literal)
	return ok && l.Base == types.Boolean && l.Value == "true"
}

// typeofLiteralToType maps a `type()` string to its primitive (nil on unknown).
//
// 设计者视角：typeofLiteralToType 是 Lua 八值的“对照表”——nil/boolean/number/
// string/table/function/thread/userdata 各对一原语，余者 nil（未知字不窄）。
// 障碍是大小写：只认小写（type() 恒返小写）。排查现场：typeof 窄化全无先查字面
// 是否拼错（如 "Number"）。
func typeofLiteralToType(s string) types.Type {
	switch s {
	case "nil":
		return types.Nil
	case "boolean":
		return types.Boolean
	case "number":
		return types.Number
	case "string":
		return types.String
	case "table":
		return types.Table
	case "function":
		return types.Function
	case "thread":
		return types.Thread
	case "userdata":
		return types.Userdata
	}
	return nil
}
