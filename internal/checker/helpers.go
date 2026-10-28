package checker

import (
	"strconv"
	"strings"

	"github.com/typelua/typelua/internal/types"
)

// Small type predicates, field/index lookup shared by checking.
//
// 设计者视角：helpers 是判定的“量具箱”——is*Like 全是宽容谓词（any 恒真、泛型透
// 约束、union 全员），lookupField/indexResult 是字段/索引的双入口（声明 vs 计算）。
// 障碍是泛型与 union 的透视：谓词必须递归剥（unwrapGeneric+全员），否则泛型代码
// 误报一片。取舍：谓词永不报错，只答真假，报错留给调用方。不变量：any 输入恒真
// （除 isComparable 的字面特判外）。排查现场：误报先查是否忘透泛型/union。

// ---------- helpers ----------

// isAnyLike reports any/unknown (permissive escape hatch).
//
// 设计者视角：isAnyLike 是全检查的“放行闸”——any/unknown 所到之处X判皆放，避免
// 动态代码刷屏。障碍是 Unknown 与 Any 语义微异（未知 vs 动态）：此处合并，
// 严格性留给显式 unknown 检查。排查现场：漏报先查是否误把 nil/void 当 any。
func isAnyLike(t types.Type) bool {
	return t.Kind() == types.KAny || t.Kind() == types.KUnknown
}

// unwrapGeneric returns the constraint for `T extends C`, or Any for unconstrained params
// (permissive to avoid false positives on generic code).
//
// 设计者视角：unwrapGeneric 是泛型的“透视镜”——有约束看约束，无约束看 Any（放行）。
// 障碍是无约束泛型若判 false 会把所有泛型函数报红；故恒给活路。取舍：谓词内透视，
// 不改原类型。不变量：ok 真即返约束/Any。排查现场：泛型误报先查是否绕过此透视直判。

func unwrapGeneric(t types.Type) (types.Type, bool) {
	if g, ok := t.(*types.GenericParam); ok {
		if g.Constraint != nil {
			return g.Constraint, true
		}
		return types.Any, true
	}
	return t, false
}

// isNumberLike reports numeric types (number/integer/float, literals, unions, enums).
//
// 设计者视角：isNumberLike 是算术的“验钞机”——原语三态、字面 Base、union 全员、
// enum 看 Underlying，泛型透约束。障碍是 enum 数字底（如 integer 枚举）不透即误拦
// 算术。取舍：any 恒真。排查现场：算术误报先查字面 Base 是否对齐。
func isNumberLike(t types.Type) bool {
	if u, ok := unwrapGeneric(t); ok {
		return isNumberLike(u)
	}
	switch x := t.(type) {
	case *types.Primitive:
		return x.K == types.KNumber || x.K == types.KInteger || x.K == types.KFloat
	case *types.Literal:
		return x.Base == types.Number || x.Base == types.Integer || x.Base == types.Float
	case *types.Union:
		for _, m := range x.Types {
			if !isNumberLike(m) {
				return false
			}
		}
		return true
	case *types.Enum:
		return isNumberLike(x.Underlying)
	}
	return isAnyLike(t)
}

// isIntegerLike reports integer types (narrower than number-like, for bitops).
//
// 设计者视角：isIntegerLike 是位运算的“卡尺”——只认 integer（字面亦须 Base integer），
// union 全员，泛型透视。障碍是 float 字面混入：Base 必须精判，否则 `1.0 & 2` 漏网。
// 排查现场：位运算误报先查字面 Base 与 enum 底。
func isIntegerLike(t types.Type) bool {
	if u, ok := unwrapGeneric(t); ok {
		return isIntegerLike(u)
	}
	switch x := t.(type) {
	case *types.Primitive:
		return x.K == types.KInteger
	case *types.Literal:
		return x.Base == types.Integer
	case *types.Union:
		for _, m := range x.Types {
			if !isIntegerLike(m) {
				return false
			}
		}
		return true
	}
	return isAnyLike(t)
}

// isConcatable reports string-concatenable types (string/number family).
//
// 设计者视角：isConcatable 管 `..`——串与数字两家可连（Lua 语义数字自动转串），
// 字面/union/enum 透视。障碍是 boolean/table 混入：必须精拦。排查现场：连接误报
// 先查数字家族是否含 integer/float。
func isConcatable(t types.Type) bool {
	if isAnyLike(t) {
		return true
	}
	if u, ok := unwrapGeneric(t); ok {
		return isConcatable(u)
	}
	switch x := t.(type) {
	case *types.Primitive:
		return x.K == types.KString || x.K == types.KNumber || x.K == types.KInteger || x.K == types.KFloat
	case *types.Literal:
		return x.Base == types.String || x.Base == types.Number || x.Base == types.Integer || x.Base == types.Float
	case *types.Union:
		for _, m := range x.Types {
			if !isConcatable(m) {
				return false
			}
		}
		return true
	case *types.Enum:
		return isConcatable(x.Underlying)
	}
	return false
}

// isComparable reports whether < <= > >= are meaningful for (l, r).
//
// 设计者视角：isComparable 是大小比的“配对表”——any/泛型恒配，数字配数字，串配串，
// 同形（指针或 String 等）亦配。障碍是跨族比较（串 vs 数）必须拦，而 `==` 恒放
// （见 checkBinOp）。取舍：宁紧勿松（大小比误放即逻辑错）。排查现场：比较误报先查
// 字面 Base 配对。
func isComparable(l, r types.Type) bool {
	if isAnyLike(l) || isAnyLike(r) {
		return true
	}
	if _, ok := l.(*types.GenericParam); ok {
		return true
	}
	if _, ok := r.(*types.GenericParam); ok {
		return true
	}
	// Numbers compare with numbers, strings with strings.
	if isNumberLike(l) && isNumberLike(r) {
		return true
	}
	if isStringLike(l) && isStringLike(r) {
		return true
	}
	// Literals of same base.
	if l == r {
		return true
	}
	if l.String() == r.String() {
		return true
	}
	return false
}

// isStringLike reports string types (primitive/literal/union/enum, generic-aware).
//
// 设计者视角：isStringLike 与数字镜像——只认 string 家族，供连接与比较复用。泛型
// 透视、union 全员。排查现场：字面串误判先查 Base 是否 String。
func isStringLike(t types.Type) bool {
	if u, ok := unwrapGeneric(t); ok {
		return isStringLike(u)
	}
	switch x := t.(type) {
	case *types.Primitive:
		return x.K == types.KString
	case *types.Literal:
		return x.Base == types.String
	case *types.Union:
		for _, m := range x.Types {
			if !isStringLike(m) {
				return false
			}
		}
		return true
	case *types.Enum:
		return isStringLike(x.Underlying)
	}
	return isAnyLike(t)
}

// isLengthable reports `#`-able types (string/table/array/map/object/tuple).
//
// 设计者视角：isLengthable 管 `#`——串表数组映射对象元组皆可，字面串可，union 全员。
// 障碍是 string 索引 `s[i]` 非法但 `#s` 合法：此处只判长度不判索引（索引另由
// indexResult 拒）。排查现场：长度误报先查 union 是否有非 lengthable 成员。
func isLengthable(t types.Type) bool {
	if isAnyLike(t) {
		return true
	}
	if u, ok := unwrapGeneric(t); ok {
		return isLengthable(u)
	}
	switch x := t.(type) {
	case *types.Primitive:
		return x.K == types.KString || x.K == types.KTable
	case *types.Array, *types.Map, *types.Object, *types.Tuple:
		return true
	case *types.Literal:
		return x.Base == types.String
	case *types.Union:
		for _, m := range x.Types {
			if !isLengthable(m) {
				return false
			}
		}
		return true
	}
	return false
}

// lookupField resolves a field on any host (union/object/enum/map/class/value/chan/table).
//
// 设计者视角：lookupField 是字段的“万能查号台”，顺序即语义：union 须全员有（TS
// 同款，缺一即无）并合并（类型并、optional/readonly 升格、Access 取严）；Object
// 先字段后串索引器；Enum 取成员字面；Map 须 Key 容串；Class/ClassValue 分实例/
// 静态（含合成 new）；Chan 委托 chanMethodType；table 恒 Any。障碍是 union 部分
// 缺字段：放行会漏错，严判才 sound。候选：首个命中 vs 全员；选全员。图：
//
//	union(全员)→Object/Map→Enum→Class(实例)→ClassValue(静态/new)→Chan→table
//
// 不变量：纯读，无诊断。排查现场：字段找不到先查 union 是否有缺员，或索引器 Key
// 是否非串。
func lookupField(ot types.Type, name string) (types.Field, bool) {
	switch x := ot.(type) {
	case *types.Union:
		// Distribute: every member must declare the field (nil and
		// field-less members fail the whole access, like TS).
		var ts []types.Type
		opt, ro := false, false
		acc := types.Public
		var owner *types.ClassDef
		for _, m := range x.Types {
			f, ok := lookupField(m, name)
			if !ok {
				return types.Field{}, false
			}
			ts = append(ts, f.Type)
			if f.Optional {
				opt = true
			}
			if f.Readonly {
				ro = true
			}
			if f.Access == types.Private || (f.Access == types.Protected && acc == types.Public) {
				acc = f.Access
				owner = f.Owner
			} else if owner == nil {
				owner = f.Owner
			}
		}
		return types.Field{Type: types.NewUnion(ts), Optional: opt, Readonly: ro, Access: acc, Owner: owner}, true
	case *types.Object:
		if f, ok := x.Fields[name]; ok {
			return f, true
		}
		// Indexer with string key?
		for _, ix := range x.Indexers {
			if ix.Key.Kind() == types.KString || ix.Key.Kind() == types.KAny {
				return types.Field{Type: ix.Value}, true
			}
			if lit, ok := ix.Key.(*types.Literal); ok && lit.Base == types.String {
				return types.Field{Type: ix.Value}, true
			}
		}
		// Enum runtime tables are Objects; handled above.
		return types.Field{}, false
	case *types.Enum:
		// Enum value access returns member literal.
		if m, ok := x.Members[name]; ok {
			return types.Field{Type: m}, true
		}
		return types.Field{}, false
	case *types.Map:
		// `m.field` where m: map<K,V>: allowed if K accepts strings.
		if types.IsAssignable(types.String, x.Key, types.Options{StrictNilChecks: true}) || isAnyLike(x.Key) {
			return types.Field{Type: x.Value}, true
		}
		return types.Field{}, false
	case *types.Class:
		// Instance members (flattened to include inherited, minus privates).
		if f, ok := x.Fields[name]; ok {
			return types.Field{Type: f.Type, Optional: f.Optional, Readonly: f.Readonly, Access: f.Access, Owner: f.Owner}, true
		}
		return types.Field{}, false
	case *types.ClassValue:
		if f, ok := x.Statics[name]; ok {
			return types.Field{Type: f.Type, Optional: f.Optional, Readonly: f.Readonly, Access: f.Access, Owner: f.Owner}, true
		}
		if name == "new" {
			if x.Constructor == nil {
				return types.Field{Type: types.Any, Access: x.CtorAccess, Owner: classDefOf(x)}, true
			}
			return types.Field{Type: &types.Func{Params: x.Constructor.Params, HasVarArg: x.Constructor.HasVarArg, VarArg: x.Constructor.VarArg, Returns: []types.Type{x.Class}, TypeParams: x.Class.TypeParams}, Access: x.CtorAccess, Owner: classDefOf(x)}, true
		}
		return types.Field{}, false
	case *types.Chan:
		if mt, ok := chanMethodType(x.Elem, name); ok {
			return types.Field{Type: mt}, true
		}
		return types.Field{}, false
	case *types.Primitive:
		if x.K == types.KTable {
			return types.Field{Type: types.Any}, true
		}
	}
	return types.Field{}, false
}

// classDefOf extracts the ClassDef behind a ClassValue, if any.
//
// 设计者视角：classDefOf 是静态侧 Owner 的“溯源”——ClassValue 的 Owner 取自其
// Class.Def，供门禁比对。空/无类即 nil（调用方恒放行）。排查现场：静态越权未报
// 先查此处是否返 nil。
func classDefOf(v *types.ClassValue) *types.ClassDef {
	if v == nil || v.Class == nil {
		return nil
	}
	return v.Class.Def
}

// indexResult computes `obj[key]` element type (nil when not indexable).
//
// 设计者视角：indexResult 是方括号的“演算尺”——any 恒 Any（具体对 any 尽力：
// 数组/映射/对象首索引器/元组并/串拒/table Any）；union 全员演算再并；数组要整数
// 键；元组要字面下标界内；Object 字面键先字段（含 optional 升 nil|T）后串索引器，
// 非字面取首索引器；串/他恒 nil。障碍是 `t[k]` 动态键：只能取首索引器 best-effort。
// 取舍：nil 表“不可索引”（调用方再报），非报错。图：
//
//	any→union(全员)→Array/Map/Tuple→Object(字面/索引器)→table
//
// 排查现场：索引取空先查键是否为字面（动态键只给首索引器）。
func indexResult(ot, kt types.Type) types.Type {
	if isAnyLike(ot) || isAnyLike(kt) {
		if isAnyLike(ot) {
			return types.Any
		}
		// ot concrete, kt any => best effort.
		switch x := ot.(type) {
		case *types.Array:
			return x.Elem
		case *types.Map:
			return x.Value
		case *types.Object:
			if len(x.Indexers) > 0 {
				return x.Indexers[0].Value
			}
			return types.Any
		case *types.Tuple:
			return types.NewUnion(x.Elems)
		case *types.Primitive:
			if x.K == types.KString {
				// string[i]? Lua 5.4 no, but allow any? Return string? Actually string indexing not valid; return nil.
				return nil
			}
			if x.K == types.KTable {
				return types.Any
			}
		}
		return types.Any
	}
	switch x := ot.(type) {
	case *types.Union:
		var rs []types.Type
		for _, m := range x.Types {
			r := indexResult(m, kt)
			if r == nil {
				return nil
			}
			rs = append(rs, r)
		}
		return types.NewUnion(rs)
	case *types.Array:
		if isIntegerLike(kt) || isNumberLike(kt) {
			return x.Elem
		}
		return nil
	case *types.Map:
		return x.Value
	case *types.Tuple:
		if lit, ok := kt.(*types.Literal); ok {
			if idx, err := strconv.Atoi(lit.Value); err == nil {
				if idx >= 1 && idx <= len(x.Elems) {
					return x.Elems[idx-1]
				}
			}
		}
		return nil
	case *types.Object:
		// Literal string/number keys map to fields.
		if lit, ok := kt.(*types.Literal); ok {
			key := strings.Trim(lit.Value, `"'`)
			if s, _, _, ok := unquoteKey(lit.Value); ok {
				key = s
			}
			// Number literal for array-like indexers.
			if lit.Base == types.Number || lit.Base == types.Integer || lit.Base == types.Float {
				for _, ix := range x.Indexers {
					if ix.Key.Kind() == types.KInteger || ix.Key.Kind() == types.KNumber {
						return ix.Value
					}
				}
				if f, ok := x.Fields[key]; ok {
					return f.Type
				}
				return nil
			}
			if f, ok := x.Fields[key]; ok {
				if f.Optional {
					return types.NewUnion([]types.Type{f.Type, types.Nil})
				}
				return f.Type
			}
			for _, ix := range x.Indexers {
				if ix.Key.Kind() == types.KString || ix.Key.Kind() == types.KAny {
					return ix.Value
				}
			}
			return nil
		}
		for _, ix := range x.Indexers {
			return ix.Value
		}
		return nil
	case *types.Primitive:
		if x.K == types.KTable {
			return types.Any
		}
		if x.K == types.KString {
			return nil
		}
	}
	return nil
}
