package types

import (
	"strings"
)

// FuncParam 是函数参数的描述：名字、类型、是否可选。
//
// 为什么 Name 保留：打印与诊断需要参数名；未命名参数（类型语法允许）用空串。
//
// 物理布局：
//
//	FuncParam
//	├── Name     string  // 参数名，可空
//	├── Type     Type    // 参数类型
//	└── Optional bool    // `?`
type FuncParam struct {
	Name     string
	Type     Type
	Optional bool
}

// Func 是函数类型 `(Params) -> Returns`，含可选 vararg 与泛型声明。
//
// 为什么 Returns 是切片而非单类型：TypeLua/Lua 函数可返回多值，类型必须
// 表达多元返回；单返回只是 len==1 的特例。VarArg 独立于 Params：vararg
// 是“剩余参数”语义，打印与匹配逻辑不同。
//
// IsAsync 是运行时契约标记：async 函数/内建在非 async 任务中调用会因无
// 调度器而 yield 失败，检查器报 TL3805；它不参与可赋值性，避免把异步
// 性质混入子类型判定。
//
// 物理布局：
//
//	Func
//	├── Params     []FuncParam     // 形参列表（含名字/类型/可选）
//	├── HasVarArg  bool           // 是否有 `...`
//	├── VarArg     Type           // vararg 元素类型（HasVarArg 时有效）
//	├── Returns    []Type         // 返回类型列表（0 => void）
//	├── TypeParams []*GenericParam // 泛型声明（约束/默认完整保留）
//	└── IsAsync    bool           // async 标记（不参与 assignability）
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

// Kind 返回 Func 的类型分类标签。
//
// 设计者视角：Kind 是 Func 的固定鉴别符 KFunc，与 Primitive 的"K 字段"模式
// 不同——Func 是独立 struct，分类恒定，无需存储 K。排查现场：无。
func (t *Func) Kind() Kind { return KFunc }

// String 把 Func 渲染为规范文本。
//
// 设计者视角：多返回、可选参数、vararg、泛型声明、async 标记都要可逆地打印，
// 以便诊断与缓存键；String 委托 stringSeen 并以空 seen 起步，stringSeen
// 负责循环对象的折叠。排查现场：若函数类型打印缺 vararg/返回，检查此分支。
func (t *Func) String() string { return t.stringSeen(map[Type]bool{}) }

// stringSeen 用 visited 集合渲染 Func，容器/对象递归时转发 seen，避免循环类型
// 打印死循环；Func 自身不入 seen（函数类型不可递归包含自身作为直接容器）。
//
// 设计者视角：Func 的参数/返回/类型参数都可能含循环对象，必须把 seen 转发给
// typeString；Func 本身不入 seen 是因为函数类型不会被自身直接包含（递归函数
// 通过 Object/Class 间接引用）。排查现场：若深层层类型打印成 "{...}"，检查
// seen 的传递链是否断开。
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
