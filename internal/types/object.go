package types

import (
	"fmt"
	"sort"
	"strings"
)

// Object 是结构化对象/接口类型：命名可选、字段映射与索引签名。
//
// 为什么 Name 与 Fields 共存：命名接口（interface Foo）在打印/缓存时用 Name
// 简化输出，但结构化比较仍看 Fields/Indexers；匿名对象 Name=""。空 Fields
// + 非空 Name 视为名义别名，打印只出 Name。
//
// 物理布局：
//
//	Object
//	├── Name     string          // 接口名（匿名为空）
//	├── Fields   map[string]Field // 命名成员
//	└── Indexers []Indexer       // 索引签名列表
type Object struct {
	Name     string // interface name if named, else ""
	Fields   map[string]Field
	Indexers []Indexer
}

// Kind 返回 Object 的类型分类标签。
//
// 设计者视角：Object 是独立 struct，分类恒定 KObject；结构化类型必须与名义
// Class 区分，assignability 才能分派到 objectAssignable。排查现场：无。
func (t *Object) Kind() Kind { return KObject }

// String 把 Object 渲染为规范文本。
//
// 设计者视角：命名接口（Name 非空且无字段/索引）退化为仅 Name；否则打印
// 字段与索引签名。seen 用于折叠循环对象：Object 入 seen 后递归字段转发
// seen，遇到同一 *Object 返回 Name 或 "{...}"。排查现场：若循环类型打印死循环，
// 检查 seen 的入栈/出栈。
func (t *Object) String() string { return t.stringSeen(map[Type]bool{}) }

// stringSeen 渲染 Object，字段/索引经 typeString 转发 seen；Object 入 seen。
//
// 设计者视角：Object 是唯一可能自引用的类型节点，因此 typeString 对 *Object
// 特殊处理：首次遇到入 seen，再次遇到折叠。这里必须把 seen 传下去并让字段
// 使用 typeString。排查现场：若深层类型未折叠，检查 typeString 的 *Object 分支。
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

// Enum 是枚举类型：名字、底层类型与成员映射。
//
// 为什么 Underlying 可为 union：枚举可基于字符串/数字联合；成员必须可赋值
// 给底层类型。Members 的值是成员字面量类型，支持 `Enum.Member` 索引访问。
//
// 物理布局：
//
//	Enum
//	├── Name       string         // 枚举名
//	├── Underlying Type          // 底层类型（string/number/...）
//	└── Members    map[string]Type // 成员名 -> 成员类型
type Enum struct {
	Name       string
	Underlying Type // string or number (or union)
	Members    map[string]Type
}

// Kind 返回 Enum 的类型分类标签。
//
// 设计者视角：Enum 是独立 struct，分类恒定 KEnum；枚举介于字面量联合与名义
// 类型之间——成员可比、整体有名。独立 Kind 让 assignability 把“成员命中”与
// “整体比名”两条路分开，避免与 Union/Object 混淆。排查现场：无。
func (t *Enum) Kind() Kind { return KEnum }

// String 把 Enum 渲染为规范文本。
//
// 设计者视角：枚举打印仅用 Name（成员通过索引访问），避免冗长；缓存键依赖
// Name 稳定。
//
// 设计者视角续：为什么只打名字不打成员——枚举成员可能是数十个字面量，全打会
// 冲垮诊断与缓存键；Name 即身份（同包同名唯一），成员细节留给索引访问与悬停
// 去展示。候选：全展开 vs 仅名；选仅名。不变量：同 Name 文本相同。排查现场：
// 若诊断需要成员列表，检查此函数是否为截断源头。
func (t *Enum) String() string { return t.Name }
