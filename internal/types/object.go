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
func (t *Object) Kind() Kind { return KObject }

// String 把 Object 渲染为规范文本。
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
func (t *Enum) Kind() Kind { return KEnum }

// String 把 Enum 渲染为规范文本。
func (t *Enum) String() string { return t.Name }
