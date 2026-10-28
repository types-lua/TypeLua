package types

import (
	"strings"
)

// GenericParam 是泛型类型参数 `T extends C = D` 的声明载体。
//
// 为什么 Constraint/Default 都是 Type：约束与默认值在语义层已解析为类型，
// 便于实例化时直接代入；声明处保存完整约束/默认，供显式类型实参校验。
//
// 物理布局：
//
//	GenericParam
//	├── Name       string  // 参数名
//	├── Constraint Type    // 上界（nil 表示无约束）
//	└── Default    Type    // 默认类型（nil 表示无默认）
type GenericParam struct {
	Name       string
	Constraint Type
	Default    Type
}

// Kind 返回 GenericParam 的类型分类标签。
func (t *GenericParam) Kind() Kind { return KGenericParam }

// String 把 GenericParam 渲染为规范文本。
func (t *GenericParam) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *GenericParam) stringSeen(seen map[Type]bool) string {
	if t.Constraint != nil {
		return t.Name + " extends " + typeString(t.Constraint, seen)
	}
	return t.Name
}

// Ref 是未解析的命名类型并携带可选类型实参（如 `Foo<T, U>`）。
//
// 为什么“未解析”：Ref 是 parser/checker 之间的中间态，允许名字在作用域中
// 尚未绑定；checker 解析后替换为 Class/Enum/Object 或具体实例。保留 Args
// 让泛型实例化在解析后仍可定位。
//
// 物理布局：
//
//	Ref
//	├── Name string   // 类型名
//	└── Args []Type   // 类型实参（可空）
type Ref struct {
	Name string
	Args []Type
}

// Kind 返回 Ref 的类型分类标签。
func (t *Ref) Kind() Kind { return KRef }

// String 把 Ref 渲染为规范文本。
func (t *Ref) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Ref) stringSeen(seen map[Type]bool) string {
	if len(t.Args) == 0 {
		return t.Name
	}
	ps := make([]string, len(t.Args))
	for i, a := range t.Args {
		ps[i] = typeString(a, seen)
	}
	return t.Name + "<" + strings.Join(ps, ", ") + ">"
}

// InferParam 是条件类型 `extends` 子句中的 `infer U` 占位符。
//
// 为什么只活在 extends：infer 的语义是“在匹配模式时捕获类型”，离开该上下文
// 就失去绑定来源；检查器在别处见到它会报错，求值器对它保持宽容（permissive）。
//
// 物理布局：
//
//	InferParam
//	└── Name string   // 推断出的类型参数名
type InferParam struct {
	Name string
}

// Kind 返回 InferParam 的类型分类标签。
func (t *InferParam) Kind() Kind { return KInferParam }

// String 把 InferParam 渲染为规范文本。
func (t *InferParam) String() string { return "infer " + t.Name }
