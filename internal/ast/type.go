package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// 类型详见实现，排查看 Span。

// ---------- Types ----------

// NamedType 表示命名类型语法节点（可带类型参数）；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：命名类型是“类型世界的变量引用”——Name 认声明（string/自定类/别名），
// Args 装显式实参。AST 只记拼写不决议（`Foo<Bar>` 存两段），决议与实例化全交 checker。
// 候选：存决议指针 vs 存原文；选原文，保持 AST 无符号表依赖，宏展开也无需重决议。
// 排查现场：若未知类型圈不到名，检查 Name 子 Span 是否独立于 S。
type NamedType struct {
	Name string
	Args []TypeNode
	S    token.Span
}

func (*NamedType) typeNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：命名类型 Span 覆盖 `Name<Args>` 全段，缺失实参圈尖括号，名错圈 Name。
// 排查现场：若泛型实参报错圈整类型，检查 Args 子 Span 是否逐个可用。
func (n *NamedType) Span() token.Span { return n.S }

// LiteralType 表示字面量类型语法节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：字面量类型是“值即类型”——Value 存原文（`"red"`/`42`/`true`/`nil` 含引号），
// checker 由此建 Literal 语义值。存原文而非解析值，避免 `"red"` 与 `'red'` 在往返中失真。
// 不变量：Value 永非空。排查现场：若字面量单引号/双引号互串，检查 parser 是否原样保留。
type LiteralType struct {
	// Value holds the literal source, e.g. `"red"`, `42`, `true`, `nil`.
	Value string
	S     token.Span
}

func (*LiteralType) typeNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：字面量类型 Span 即字面量原文区间，拼写错圈原文。排查现场：若引号计入与否
// 不定，检查 S 是否含引号（应当含）。
func (n *LiteralType) Span() token.Span { return n.S }

// ArrayType 表示数组类型语法节点 `T[]`；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：`T[]` 是“同质列表的糖”，Elem 为元素类型，checker 降为 Array/Map 语义。
// parser 把 `T[][]` 套成嵌套 ArrayType 而非多维标记，保持结合性显式。不变量：Elem 永非 nil。
// 排查现场：若多维数组维度反了，检查嵌套顺序（外层为最外 `[]`）。
type ArrayType struct {
	Elem TypeNode
	S    token.Span
}

func (*ArrayType) typeNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：数组 Span 含后缀 `[]`，元素错圈 Elem，括号缺失圈后缀。排查现场：
// 若 `T[]` 被切成 T 与空，检查后缀 Span 是否独立。
func (n *ArrayType) Span() token.Span { return n.S }

// UnionType 表示联合类型语法节点 `A | B`；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：联合是“多选一”，Types 保序存各分支（解析期不展平去重，范式化交 NewUnion）。
// 候选：解析期归一 vs checker 期归一；选后者，AST 保留手写顺序利于诊断回放。
// 不变量：Types 长度≥2（单员不成联合）。排查现场：若联合判定顺序敏感，检查 checker
// “源联合先拆、目标联合后命中”的分流，而非此处顺序。
type UnionType struct {
	Types []TypeNode
	S     token.Span
}

func (*UnionType) typeNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：联合 Span 横跨全 `A | B`，分支错圈对应 Types[i]，分隔符错圈 `|`。
// 排查现场：若分支报错圈整联合，检查是否误用本 S 而非分支子 Span。
func (n *UnionType) Span() token.Span { return n.S }

// IntersectionType 表示交叉类型语法节点 `A & B`；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：交叉是“全都要”，Types 各表一面（结构相加、名义相交），语义由 checker
// 的 object/名义双判定消化。AST 同样不预归一，保留手写交集供诊断。排查现场：
// 若交叉缺一员仍放行，检查 checker 目标交集“全中”分支，而非此处。
type IntersectionType struct {
	Types []TypeNode
	S     token.Span
}

func (*IntersectionType) typeNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：交叉 Span 同联合，全包 `A & B`，员错误圈员。排查现场：同联合，先分清
// 源/目标交集分流再看 Span。
func (n *IntersectionType) Span() token.Span { return n.S }

// OptionalType 表示可选类型语法节点 `T?`；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：`T?` 是 `T | nil` 的糖，Elem 为本体。parser 产糖而非直接展联合，
// 让“显式可选 vs 手写联合”的诊断可区分（缺值提示不同）。checker 视二者等价。
// 不变量：Elem 永非 nil。排查现场：若 `T??` 嵌套，检查 parser 是否允许多层糖。
type OptionalType struct {
	Elem TypeNode
	S    token.Span
}

func (*OptionalType) typeNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：可选 Span 含尾部 `?`，`?` 漏写圈本体尾。排查现场：若 `!` 与 `?` 混淆，
// 检查后缀链的 Question/Bang 分支。
func (n *OptionalType) Span() token.Span { return n.S }

// ParenType 表示带括号的类型语法节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：类型括号同表达式括号——语义透明、定位有痕。checker 穿透到 Elem，
// Span 保留括号供“多余括号”提示。候选：解析期脱括号 vs 保留；选保留，对称于 ParenExpr。
// 排查现场：若优先级误判（如 `A | B[]`），先看 Array/Union 绑定，再看括号是否套对。
type ParenType struct {
	Elem TypeNode
	S    token.Span
}

func (*ParenType) typeNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：括号类型 Span 含括号，内层 Elem 另计。排查现场：若去括号后行号漂移，
// 检查内外 Span 是否混用。
func (n *ParenType) Span() token.Span { return n.S }

// TupleType 表示元组类型语法节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：元组是“定长定型的数组”，Elems 保序（含空元组 `[]`），多返回与变参展开
// 都靠它定形。空元组由 checker 视为空 Never 联合之源（见 unionOf），AST 允许空切片。
// 排查现场：若元组与数组互串，检查 checker 分流（定长 vs 同质）而非此处。
type TupleType struct {
	Elems []TypeNode
	S     token.Span
}

func (*TupleType) typeNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：元组 Span 覆盖 `[...]` 全段，员错圈员。排查现场：若空元组无坐标，
// 检查 S 是否仍有括号区间。
func (n *TupleType) Span() token.Span { return n.S }

// TableType 表示 table 类型语法节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：table 类型是“对象字面量的类型镜像”——Fields 保序含三态（普通/索引器/方法，
// 见 TableTypeField），checker 展为 Object（含 Indexers）。与 InterfaceDecl 分工：
// 前者嵌在表达式位，后者立在声明位。排查现场：若索引器与具名字段冲突，检查
// keyAcceptsLiteral 的兼容判定，而非字段顺序。
type TableType struct {
	Fields []*TableTypeField
	S      token.Span
}

func (*TableType) typeNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：table 类型 Span 罩住 `{...}` 全体，字段错圈字段。排查现场：若空表类型
// 无坐标，检查 S 括号区间。
func (n *TableType) Span() token.Span { return n.S }

// TableTypeField 表示 table 类型中的一个字段/索引器/方法；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：一个 struct 扛三活，好比万能插座——普通字段详见实现，排查看 Span。
// 索引器（IsIndexer 时看 IndexKey/IndexVal）、方法（IsMethod 时看 MethodSig）。
// Name 存 string 而非 *Ident（省一次解引用），NameSpan 另存坐标供 rename 跳转。
// 障碍是三态互斥：parser 按 `[`/`:`+`(` 外形一次分流，IsIndexer/IsMethod 双旗互斥。
// 不变量：三态恰一有效。排查现场：若方法签名丢失，检查 IsMethod 与 MethodSig 是否配对。
type TableTypeField struct {
	Name     string
	NameSpan token.Span
	Type     TypeNode
	Optional bool
	Readonly bool
	S        token.Span
	// For indexer `[K]: V`
	IsIndexer bool
	IndexKey  TypeNode
	IndexVal  TypeNode
	IsMethod  bool
	MethodSig *FuncType
}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：字段 Span 覆盖本条全段（含 `readonly`/`?` 修饰），索引器含 `[K]: V`，
// 方法含签名。NameSpan 只圈名，hover 圈全段、rename 圈名。排查现场：若修饰符报错圈不准，
// 检查 S 与 NameSpan 的分工。
func (n *TableTypeField) Span() token.Span { return n.S }

// FuncType 表示函数类型语法节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：函数类型是“调用契约的纸面版”——Params 逐参定形（含可选/vararg 标记），
// HasVarArg/VarArgType 管尾部兜底，Returns 为返回表（0 即 void-ish，1+ 即多返回），
// Generics 管参数化。形状对照 FuncLit：前者是类型，后者是实现。不变量：Returns 可空。
// 排查现场：若参数逆变/返回协变误判，检查 funcAssignable 的元数四象限，而非此处。
type FuncType struct {
	Params     []*FuncTypeParam
	HasVarArg  bool
	VarArgType TypeNode
	Returns    []TypeNode // 0 => void-ish (unannotated?), 1+ return types
	Generics   []*GenericParam
	S          token.Span
}

func (*FuncType) typeNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：函数类型 Span 从首参/左括号到返回尾，参数错圈 Param，返回错圈 Returns。
// 排查现场：若泛型函数高亮丢 Generics，检查 S 是否含 `<T>` 区间。
func (n *FuncType) Span() token.Span { return n.S }

// FuncTypeParam 表示函数类型的一个参数；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：类型侧参数比值侧多两旗——Name 可空（匿名 `(string, number)` 合法），
// Optional（`x?`）与 IsVarArg（`...`）显式标记。定义侧 Param 无此二旗（调用元数宽容消化），
// 类型侧必须明示以便逆变检查。不变量：Type 永非 nil（缺省即 any 由 checker 补）。
// 排查现场：若可选参数仍被要求传参，检查 Optional 是否置位。
type FuncTypeParam struct {
	Name     *Ident // may be nil for unnamed
	Type     TypeNode
	IsVarArg bool
	Optional bool
	S        token.Span
}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：参数 Span 覆盖 `name?: T` / `...` 全段，匿名参数 S 即类型区间。
// 排查现场：若匿名参数无坐标，检查 S 是否回退到 Type.Span()。
func (n *FuncTypeParam) Span() token.Span { return n.S }

// TypeofType 表示 typeof 类型语法节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：typeof 是“从值偷类型”——Expr 为任意表达式，checker 对其求值取类型
// （变量/字段/调用结果皆可）。AST 存表达式而非结果，保持“语法快照”，求值推迟到检查。
// 不变量：Expr 永非 nil。排查现场：若 typeof 循环引用，检查 checker 是否用延迟求值
// 而非 AST 展开。
type TypeofType struct {
	Expr Expr
	S    token.Span
}

func (*TypeofType) typeNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：typeof Span 从 typeof 到表达式尾，表达式错圈 Expr。排查现场：
// 若 `typeof` 被当值，检查类型位与表达式位的上下文分流。
func (n *TypeofType) Span() token.Span { return n.S }

// KeyofType 表示 keyof 类型语法节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：keyof 是“取键集”——Elem 为目标类型，checker 求其键的字面量联合
// （`keyof {a, b}` 得 `"a"|"b"`）。与 typeof 对仗：一个取值的型、一个取型的键。
// 排查现场：若 keyof 得空，检查 Elem 是否为无字段的空表/any。
type KeyofType struct {
	Elem TypeNode
	S    token.Span
}

func (*KeyofType) typeNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：keyof Span 全包 `keyof T`，T 错圈 Elem。排查现场：若 keyof 套 keyof
// 嵌套错，检查右结合解析。
func (n *KeyofType) Span() token.Span { return n.S }

// MappedType is `{ [K in Keys]?: V }` with TS-style modifier operators:
// an optional `+`/`-` prefix on `readonly` (`+readonly`/`-readonly`) and on
// `?` (`+?`/`-?`). `+` is the explicit form of the bare marker; `-` removes
// the modifier (meaningful for homomorphic mappings over `keyof T`).
//
// 设计者视角：映射类型是“类型的 for 循环”——Param（K）遍历 Constraint（Keys 域），
// Value 为模板。`?`/`readonly` 修饰符各有三态：裸写、显式 `+`、移除 `-`（同态映射
// `keyof T` 时继承/去掉原修饰）。障碍是域未具体时不可展开：AST 存形状，
// checker 的 evalMapped 只在域具体详见实现，排查看 Span。
// 不变量：Param/Constraint/Value 永非 nil；OptMinus/RoMinus 为真则对应正旗亦真。
// 排查现场：若映射展开丢修饰符，检查 Optional/OptMinus/Readonly/RoMinus 四旗。
type MappedType struct {
	Param      *Ident
	Constraint TypeNode
	Optional   bool // `?` or `+?`
	OptMinus   bool // `-?`
	Readonly   bool // `readonly` or `+readonly`
	RoMinus    bool // `-readonly`
	Value      TypeNode
	S          token.Span
}

func (*MappedType) typeNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：映射 Span 罩住 `{...}` 全体，域错圈 Constraint，值错圈 Value。
// 排查现场：若 `in` 缺失仍有 Span，检查恢复锚点是否含括号。
func (n *MappedType) Span() token.Span { return n.S }

// IndexedAccessType is `T[K]`.
//
// 设计者视角：`T[K]` 是类型层的下标——Obj 为被索引类型，Index 为键类型，checker 的
// evalIndexedAccess 按“联合分发 + 对象字段/索引器 + 元组序号”求值。障碍是泛型未实例化
// 时算不出：返回 ok=false 延迟而非报错（defer vs error 双通道）。AST 只记形状。
// 排查现场：若索引过早报错，检查 IsConcreteType 守门；若 defer 丢失，检查 ok=false 分支。
type IndexedAccessType struct {
	Obj   TypeNode
	Index TypeNode
	S     token.Span
}

func (*IndexedAccessType) typeNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：索引访问 Span 覆盖 `T[K]` 全段，键错圈 Index。排查现场：若括号缺失，
// 检查 S.End 是否取到同步点。
func (n *IndexedAccessType) Span() token.Span { return n.S }

// ConditionalType is `Check extends Extends ? True : False`.
//
// 设计者视角：条件类型是“类型的 if”——Check 是否 extends Extends 决定取 True/False。
// 与 infer 联动（`T extends infer U ? U : never` 即萃取）。障碍同样是延迟：Check 含泛型
// 参时不可判定，保留形状待实例化。不变量：四段永非 nil。排查现场：若条件提前求值误判，
// 检查守门是否为 IsConcreteType。
type ConditionalType struct {
	Check   TypeNode
	Extends TypeNode
	True    TypeNode
	False   TypeNode
	S       token.Span
}

func (*ConditionalType) typeNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：条件 Span 横跨 `Check extends Extends ? True : False`，分支错圈对应段。
// 排查现场：若 `? :` 缺失，检查恢复后 S 是否含同步点。
func (n *ConditionalType) Span() token.Span { return n.S }

// InferType is `infer U` (only meaningful in an extends clause).
//
// 设计者视角：infer 是“条件分支里的占位符”——仅在 ConditionalType 的 Extends 位有意义，
// 匹配成功即把 Check 的对应部分绑给 U。游离在外的 infer 无义，checker 报“游离 infer”。
// 不变量：Name 永非 nil。排查现场：若 infer 未绑定，检查是否身处 extends 子句。
type InferType struct {
	Name *Ident
	S    token.Span
}

func (*InferType) typeNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：infer Span 即 `infer U` 全段，游离 infer 圈整段提示挪入 extends。
// 排查现场：若 U 名冲突，检查 Name.S 与外层泛型名。
func (n *InferType) Span() token.Span { return n.S }
