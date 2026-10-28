package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// 表达式详见实现，排查看 Span。

// ---------- Expressions ----------

// Ident 表示标识符表达式节点；Name 为源码名，S 为源码 Span，用于诊断与定位。
//
// 设计者视角：Ident 是全流水线的“人名牌”，parser 只抄名字不查户口，查表认人是
// checker 的事（作用域/符号表），emitter 原样写回。障碍是关键字与软关键字混用
// （await/spawn/import 平时仍是标识符）：AST 不提前判死刑，留给各阶段按上下文
// 解读。候选：存 token 索引 vs 存 Name+S 值；选后者，让宏展开合成的 Ident 也能
// 造坐标。不变量：Name 永不为空串（空位由 parser 报缺失而非造空节点）。
// 排查现场：若“未定义变量”指错行，先看此处 S 是否为定义处而非使用处。
type Ident struct {
	Name string
	S    token.Span
}

func (*Ident) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：Ident 的 Span 是 rename/引用链的唯一门牌，LSP 跳转全靠它；
// 直接返回 S 不做拼接，保证 O(1)。排查现场：若重命名只改一半，检查宏展开
// 合成的 Ident 是否带了零值 Span。
func (n *Ident) Span() token.Span { return n.S }

// String 返回标识符名称，用于调试打印与诊断上下文.
//
// 设计者视角：String() 直接回 Name 而非加引号/加行号，好比胸牌只印名字——
// 调试打印与错误信息要的是“人话”，坐标另有 Span() 负责。取舍：不实现
// fmt.Stringer 的复杂格式，保持单行无分配。排查现场：若打印出现空串，
// 检查 Name 是否在合成节点时漏填。
func (n *Ident) String() string { return n.Name }

// NumberLit 表示数字字面量表达式节点；Literal 保留原文，S 为源码 Span，用于诊断与定位。
//
// 设计者视角：数字存原文而非 float64，好比收银不把发票揉成一团——`0xFF`/`1e3`/`42`
// 的写法要原样发射回 Lua，精度与进制由 checker 另行解析。障碍是 Lua 整数/浮点双轨：
// AST 不提前定性，只当字符串搬运，让 checker 按上下文判 integer vs number。
// 不变量：Literal 永非空。排查现场：若数字诊断值对不上源码，检查 parser 是否做了
// 归一化（应当原样保留）。
type NumberLit struct {
	Literal string
	S       token.Span
}

func (*NumberLit) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：字面量 Span 即其原文区间，类型错误（如溢出、非法进制）直接圈原文，
// 无需回溯。排查现场：若数字高亮偏一位，检查词法切分时 Start/End 是否含前后空格。
func (n *NumberLit) Span() token.Span { return n.S }

// StringLit 表示字符串字面量表达式节点；Literal 保留原文，S 为源码 Span，用于诊断与定位.
//
// 设计者视角：字符串同样保留含引号原文，长括号/转义原样搬运，emitter 零改写即保真；
// checker 需要值时自行去引号解析。候选：存解码后值 vs 存原文；选原文，避免
// `"\228"` 这类转义在往返中变味。不变量：Literal 含引号。排查现场：若发射后引号
// 风格变了，检查 emitter 是否误用解析值重拼而非原文直写。
type StringLit struct {
	Literal string
	S       token.Span
}

func (*StringLit) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：字符串 Span 含两侧引号，圈选即整串，未闭合字符串的 Span 延至行尾/EOF，
// 供“缺右引号”恢复。排查现场：若多行长字符串定位跳行，检查 Pos 的行列是否按 rune
// 而非 byte 计算。
func (n *StringLit) Span() token.Span { return n.S }

// BoolLit 表示布尔字面量表达式节点；Value 为真值，S 为源码 Span，用于诊断与定位.
//
// 设计者视角：布尔只有 true/false 两张脸，直接存 Value 省去字符串比对，checker 的
// 窄化如 `true`，排查看 Span。
// BoolLit 后，后续阶段不再关心它曾经是哪个 Kind。不变量：Value 与源码拼写一致。
// 排查现场：若条件恒真/恒假误报，检查 parser 是否把非字面量误装成 BoolLit。
type BoolLit struct {
	Value bool
	S     token.Span
}

func (*BoolLit) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：BoolLit 的 Span 让“if true then”这类恒定分支能圈出条件词，提示
// 死分支。排查现场：无多余状态可查，Span 对即对。
func (n *BoolLit) Span() token.Span { return n.S }

// NilLit 表示 nil 字面量表达式节点；S 为源码 Span，用于诊断与定位.
//
// 设计者视角：nil 是 Lua 的“空瓶子”，单例语义无需载荷，空 struct 即全部信息；
// checker 见 NilLit 直接给 nil 类型，可选链/非空断言都围着它转。取舍：不与
// false/0 合并，保持与 Lua 语义一对一。排查现场：若 nil 被当缺失参数吞掉，
// 检查调用点是否把 NilLit 与“未传参”混为一谈。
type NilLit struct{ S token.Span }

func (*NilLit) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：nil 的 Span 让“不可赋 nil 给非空”能圈住那个 nil 字，而非整行。
// 排查现场：若圈选为空，检查合成的 NilLit（如缺省补 nil）是否带了零值 Span。
func (n *NilLit) Span() token.Span { return n.S }

// VarArg 表示 vararg 表达式（`...`）节点；S 为源码 Span，用于诊断与定位.
//
// 设计者视角：`...` 是函数的“盲盒口”，裸写时类型未知，T 允许外层函数把形参注记
// (`...: T`) 回填进来，多数时候为 nil 即动态。障碍是作用域：只有变参函数体内
// 的 `...` 合法，AST 先照单全收，合法性由 checker 按函数 HasVarArg 复核，避免
// parser 提前背作用域表。排查现场：若“... 越界”误报/漏报，检查外层 FuncLit/ClassMember
// 的 HasVarArg 是否先立对，再看此处 T 回填是否覆盖了 nil。
type VarArg struct {
	S token.Span
	T TypeNode // optional annotated type from enclosing function; usually nil
}

func (*VarArg) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：VarArg 的 Span 只有三个点，报错圈三点即够；宏展开时 `...` 仅在实参
// 位置展开，Span 指向宏调用处而非定义处。排查现场：若展开后定位飞回定义处，
// 检查宏展开是否复用了旧 S 而未重Stamp。
func (n *VarArg) Span() token.Span { return n.S }

// ParenExpr 表示带括号的表达式节点；S 为源码 Span，用于诊断与定位.
//
// 设计者视角：括号在语义上是空气、在定位上是路标——checker 求值直接穿透到 E，
// 但 Span 保留括号区间，让“多余括号”“换行断行”诊断有处可圈；emitter 按需补括号
// 保序。候选：解析期脱括号 vs 保留节点；选保留，避免“(a)..(b)”这类源码形态丢失，
// LSP 悬停也有括号可点。不变量：E 永非 nil。排查现场：若优先级误判，先看 BinOpExpr
// 的分层，再看此处是否多套/漏套一层。
type ParenExpr struct {
	E Expr
	S token.Span
}

func (*ParenExpr) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：ParenExpr 的 Span 含两侧括号，外层诊断圈括号、内层诊断圈 E，大小
// 坐标分工明确。排查现场：若去括号后行号不变但列漂移，检查 S 是否含括号而 E.S 不含。
func (n *ParenExpr) Span() token.Span { return n.S }

// TableLit 表示 table 字面量表达式节点；S 为源码 Span，用于诊断与定位.
//
// 设计者视角：TableLit 是 Lua 的“万能容器”字面量，`{}` 既是空数组也是空对象，
// checker 按“纯列表→Tuple/含命名→Object”分流（见 checker 注释）。Fields 切片保序，
// 让 `{1, 2, x=3}` 的混合形态原样保留，emitter 逐项直写。排查现场：若空表被判成
// 数组/对象两边不讨好，检查 checker 分流而非此处——AST 只管保序搬运。
type TableLit struct {
	Fields []*TableField
	S      token.Span
}

func (*TableLit) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：TableLit 的 S 覆盖 `{...}` 全括号，空表 `{}` 靠它才有坐标可报；
// 单个字段的 Span 另计，外圈内圈各司其职。排查现场：若缺右括号的恢复位置飘了，
// 检查 parser 补的 S.End 是否取到同步点。
func (n *TableLit) Span() token.Span { return n.S }

// TableFieldKind 区分 table 字段的种类（表达式/具名/索引）。
//
// 设计者视角：三种写法三种命运——`{x}` 是 ExprField（缩写/列表元），`{x=1}` 是
// NamedField（Key 为 Ident），`{[k]=1}` 是 IndexedField（KeyX 为任意 Expr）。
// 用 Kind 显式三态而非“看哪个指针非空”，让 switch 必须穷举，漏分支编译期现形。
// 排查现场：若 `{[1]=x}` 被当具名字段，检查 Kind 赋值而非 Key 是否非空。
type TableFieldKind int

const (
	ExprField TableFieldKind = iota
	NamedField
	IndexedField
)

// TableField 表示 table 字面量中的一个字段；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：字段形态速览（Kind 即总开关）：
//
//	ExprField     Val            `{x}`       Key/KeyX 皆空
//	NamedField    Key + Val      `{x = 1}`   Key 为 Ident
//	IndexedField  KeyX + Val     `{[k] = 1}` KeyX 为任意 Expr
//
// 障碍是 Lua 缩写 `{x}` 与列表 `{1,2}` 同形：统一归为 ExprField，语义分流留给
// checker（Ident 进对象？表达式进数组？）。不变量：Val 永非 nil；Kind 与 Key/KeyX
// 的空非严格配对。排查现场：若具名/索引字段互串，检查 parser 的 Kind 标记，而非
// Val 的类型。
type TableField struct {
	Kind TableFieldKind
	Key  *Ident // NamedField
	KeyX Expr   // IndexedField
	Val  Expr
	S    token.Span
}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：TableField 的 S 只圈本字段（含逗号前的全部），让“重复字段”“类型不匹配”
// 能点名到行；TableLit.S 圈全表，两级坐标配合。排查现场：若字段报错圈到整表，检查
// 是否误用了父节点的 Span。
func (n *TableField) Span() token.Span { return n.S }

// FuncLit 表示函数字面量表达式节点；S 为源码 Span，用于诊断与定位.
//
// 设计者视角：FuncLit 是“函数全家桶”，签名与肉身同居一室：Params/HasVarArg/VarArgName/
// VarArgType 管入口，ReturnType 管出口（nil 即未注记，TupleType 即多返回），Body 管
// 实现，Generics/IsAsync/IsMethod 管修饰。障碍有三：一是方法冒号隐含 self，IsMethod
// 标记它以便 checker 自动插首参；二是 async 可 await 但发射后擦为普通函数；三是多返回
// 需 Tuple 承载。候选：语句函数与表达式函数分两种节点 vs 统一 FuncLit；选统一，
// LocalFuncDef/FuncDefStmt 只包一层名字壳，复用此肉身。不变量：Body 永非 nil；
// Step 为 nil 即默认步长 1 的同类思想在此体现为 VarArgType nil 即动态。排查现场：
// 若 self 凭空多/少一个，检查 IsMethod；若 await 报“不在 async 内”，检查 IsAsync。
type FuncLit struct {
	Params     []*Param
	HasVarArg  bool
	VarArgName *Ident
	VarArgType TypeNode
	ReturnType TypeNode // nil if unannotated; TupleType for multi-return
	Body       *Block
	IsMethod   bool // true if defined with colon (implicit self)
	Generics   []*GenericParam
	IsAsync    bool // `async function`: may await/yield; erases to plain function
	S          token.Span
}

func (*FuncLit) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：FuncLit 的 S 从 function/async 关键字横跨到 end/}，是折叠与跳转的
// 大门牌；参数/返回各自有小门牌。花括号体与 end 体在此同形，Span 抹平二者差异。
// 排查现场：若函数报错只圈到名字，检查 parser 拼 S 时是否忘了把 Body.End 纳入。
func (n *FuncLit) Span() token.Span { return n.S }

// Param 表示函数参数声明；S 为源码 Span，用于诊断与定位.
//
// 设计者视角：Param 是“最小的具名类型对”——Name 认人、Type 报价（可 nil 即动态），
// 比 FuncTypeParam 少了可选/vararg 标记，因为定义侧的可选由调用侧元数宽容消化，
// 不在语法层设卡。排查现场：若参数类型诊断无位置，检查 Type 子节点的 Span 而非
// 此处 S（此处只圈名字段）。
type Param struct {
	Name *Ident
	Type TypeNode
	S    token.Span
}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：Param 的 S 以 Name 为主、含类型注记区间，hover 时圈整段 `x: T`，
// rename 时只用 Name.S。排查现场：若重命名圈到类型，检查 LSP 是否误用了 Param.S
// 而非 Param.Name.S。
func (n *Param) Span() token.Span { return n.S }

// IndexExpr 表示索引访问表达式 `a[b]` 节点；S 为源码 Span，用于诊断与定位.
//
// 设计者视角：`a[b]` 是动态钥匙开动态锁——Key 为任意 Expr，checker 统一走 lookup
// 求值，支持联合分发与索引签名。障碍是与 FieldExpr 的分工：`a.b` 是 `a["b"]` 的
// 快捷糖，静态名可直接查表，故另立 FieldExpr 做快路径，此处专啃动态键。不变量：
// Obj/Key 永非 nil。排查现场：若 `a.b` 被解析成此处，检查 parser 后缀链的 Dot 分支。
type IndexExpr struct {
	Obj Expr
	Key Expr
	S   token.Span
}

func (*IndexExpr) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：IndexExpr 的 S 从 Obj.Start 到 `]`，让“索引越界/键不存在”圈出整段
// `a[b]` 而非仅 b。排查现场：若报错只圈括号内，检查 S.End 是否取到 RBrack 而非 Key.End。
func (n *IndexExpr) Span() token.Span { return n.S }

// FieldExpr 表示字段访问表达式 `a.b` 节点；S 为源码 Span，用于诊断与定位.
//
// 设计者视角：`a.b` 是“写死的钥匙”，Field 为 Ident，可直接按名查表/补全，无需求值
// Key；性能与诊断都比 IndexExpr 更直。checker 的字段存在性、可见性检查都先看此处。
// 取舍：parser 遇到 `.` 即产此节点，不归一为 IndexExpr+StringLit，保住“静态可分析”
// 的信息。排查现场：若补全在 `a.` 后失灵，检查 Field 是否为 nil（容错恢复的半成品）。
type FieldExpr struct {
	Obj   Expr
	Field *Ident
	S     token.Span
}

func (*FieldExpr) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：FieldExpr 的 S 同样首尾全包，`a.b.c` 的链式访问逐层嵌套，外层 S 含整链，
// 内层 S 含前缀，悬停时由小到大逐级放大。排查现场：若链式报错圈不全，检查嵌套时外层
// S.Start 是否取了 Obj.Start。
func (n *FieldExpr) Span() token.Span { return n.S }

// CallExpr 表示函数调用表达式节点；S 为源码 Span，用于诊断与定位.
//
// 设计者视角：CallExpr 是“多返回的十字路口”——Args 按序求值，末位可展开多返回；
// TypeArgs 承载显式泛型实参 `f<T>(x)`，无则由 checker 推断。ErrorCall 是 checker 回填的
// 印章：未被遮蔽的 `error(...)` 才盖章，emitter 见章才在周围跑 errdefer（见 emitter/defer.go）。
// 障碍是 AST 默认不可变 vs 此处可变回填：ErrorCall 是唯一的“检查期铅笔批注”，
// 发射前只写一次，不破坏“形状稳定”的大原则。排查现场：若 errdefer 未触发，检查
// checker 是否因局部遮蔽详见实现，排查看 Span。
type CallExpr struct {
	Func     Expr
	TypeArgs []TypeNode // explicit generic arguments: f<T>(x)
	Args     []Expr
	// ErrorCall marks a call to the `error` builtin (set by the checker
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	ErrorCall bool
	S         token.Span
}

func (*CallExpr) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：CallExpr 的 S 从被调函数起始到右括号，参数错误圈整调用，函数不存在圈
// Func 子段——大小坐标配合。TypeArgs 的尖括号区间含在 S 内但另有子 Span。排查现场：
// 若泛型调用报错圈不到 `<T>`，检查 parser 是否为 TypeArgs 另存了 Span。
func (n *CallExpr) Span() token.Span { return n.S }

// MethodCallExpr 表示方法调用表达式 `a:m(...)`；S 为源码 Span，用于诊断与定位.
//
// 设计者视角：冒号调用是“自带 self 的糖”——Obj 自动成为首实参，checker 按方法表查
// Method，emitter 翻译为 `obj.m(obj, ...)`。障碍是点调用与冒号调用一字之差语义迥异：
// parser 必须分两种节点，否则 self 传不传全靠猜。TypeArgs 同理承载 `obj:m<T>(x)`。
// 不变量：Method 永非 nil（缺名由 parser 报恢复）。排查现场：若方法调用丢失 self，
// 检查是否误用了 CallExpr+FieldExpr 组合而非本节点。
type MethodCallExpr struct {
	Obj      Expr
	Method   *Ident
	TypeArgs []TypeNode // obj:m<T>(x)
	Args     []Expr
	S        token.Span
}

func (*MethodCallExpr) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：MethodCallExpr 的 S 同样全包 `obj:m(args)`，方法名缺失的诊断圈冒号处。
// 排查现场：若冒号与点调用混淆，先看 parser 后缀链的 Colon 分支是否产出本节点。
func (n *MethodCallExpr) Span() token.Span { return n.S }

// BinOpExpr 表示二元运算表达式节点；S 为源码 Span，用于诊断与定位.
//
// 设计者视角：二元运算是 Pratt 爬坡的果实——Op 存 token.Kind（如 Plus/Eq/DotDot），
// L/R 为左右子树，优先级已在解析期固化成树形，后续阶段无需再查表。障碍是比较链与
// 连接/算术的优先级交错：parser 用 binPrec 表一次排定，AST 只记结果。候选：Op 存
// 字符串 vs 存 Kind；选 Kind，checker 按 Kind switch 穷举，拼写错误编译期现形。
// 排查现场：若 `a + b * c` 结合错，先查 parser 优先级表，再看此处树形是否左右颠倒。
type BinOpExpr struct {
	Op token.Kind
	L  Expr
	R  Expr
	S  token.Span
}

func (*BinOpExpr) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：BinOpExpr 的 S 横跨 `L op R`，操作数类型不匹配时圈整式，操作符拼写问题
// 圈 Op 区间（由 checker 切子 Span）。排查现场：若报错只圈一半，检查 S 是否取了
// L.Start 到 R.End，而非某一侧。
func (n *BinOpExpr) Span() token.Span { return n.S }

// UnOpExpr 表示一元运算表达式节点；S 为源码 Span，用于诊断与定位.
//
// 设计者视角：一元运算只有四个前缀（`not - # ~`）加 await 软关键字，Op 同样存 Kind，
// X 为唯一操作数。parser 的 parseUnary 先吞前缀再套后缀，保证 `-a.b` 解析为
// `-(a.b)` 而非 `(-a).b`。排查现场：若 `-#a` 嵌套顺序反了，检查前缀递归而非此处。
type UnOpExpr struct {
	Op token.Kind
	X  Expr
	S  token.Span
}

func (*UnOpExpr) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：UnOpExpr 的 S 从操作符到操作数尾，长度操作符（如 `#t`）报错圈整段。
// 排查现场：若 `not` 被当标识符，检查 parser 是否在表达式开头优先尝试前缀。
func (n *UnOpExpr) Span() token.Span { return n.S }

// CastExpr 表示类型断言/转换表达式 `x as T` 节点；S 为源码 Span，用于诊断与定位.
//
// 设计者视角：`as` 是“编译期橡皮图章”——checker 验证兼容性（必要时窄化），emitter
// 直接擦除为 E，运行时零开销。障碍是断言与转换的界限：TypeLua 的 as 只做可验证的
// 断言，不做 JS 式强制转换，避免运行时偷换语义。不变量：E/T 永非 nil。排查现场：
// 若 as 后运行时行为变了，检查 emitter 是否误发射了转换代码（应当只发 E）。
type CastExpr struct {
	E Expr
	T TypeNode
	S token.Span
}

func (*CastExpr) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：CastExpr 的 S 覆盖 `E as T` 全段，断言失败圈整段，T 写错圈 T 子段。
// 排查现场：若 as 诊断只圈 E，检查 T 的 Span 是否独立可用。
func (n *CastExpr) Span() token.Span { return n.S }

// NonNullExpr 表示非空断言表达式 `x!` 节点；S 为源码 Span，用于诊断与定位.
//
// 设计者视角：`!` 是程序员给 checker 立的军令状——“此值必非空”，checker 据此从
// `T?` 窄化为 `T`，emitter 同样擦除为 E。障碍是与阶乘/取反 `!` 的词法冲突：parser
// 只在表达式后缀位置认此用法，其余位置仍是 UnOpExpr 的 not。候选：复用 CastExpr
// vs 独立节点；选独立，让“断言未验证就大胆用”的告警能单独统计。排查现场：
// 若 `x!` 被解析成双重否定，检查后缀链的 Bang 分支优先级。
type NonNullExpr struct {
	E Expr
	S token.Span
}

func (*NonNullExpr) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：NonNullExpr 的 S 含尾部 `!`，漏写 `!` 与误写 `!` 的诊断都能定位到
// 感叹号本身。排查现场：若 `!!` 嵌套解析异常，检查后缀循环是否消费了两个 Bang。
func (n *NonNullExpr) Span() token.Span { return n.S }

// AwaitExpr is `await <expr>` (soft keyword): marks a suspension point
// inside an `async` function (or the top-level chunk). It erases to the
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：await 是“显式让出标记”而非运行时指令，好比马拉松打卡点——真正让出由
// 阻塞调用自己 yield，await 只让 checker 検証“是否身处 async 上下文”。障碍是软关键字：
// `await` 平时仍是普通标识符，只有前缀位置才转义为本节点，避免占用用户命名。
// 候选：await 产新运行时调用 vs 擦除为内层；选擦除，保持发射零开销，挂起能力下沉到
// 调度器。不变量：X 永非 nil；顶层 chunk 视作隐式 async。排查现场：若“await 不在
// async 内”误报，检查外层 FuncLit.IsAsync 与 chunk 顶层特例，再看 parser 是否把
// 非前缀的 await 误收编。
type AwaitExpr struct {
	X Expr
	S token.Span
}

func (*AwaitExpr) exprNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：AwaitExpr 的 S 从 await 起到内层表达式尾，让“await 套 await”逐层可圈；
// checker 报“不在 async 内”圈 await 关键字，类型错误圈 X。排查现场：若 await 高亮
// 吞掉整行，检查 S 是否把换行后的 X 也纳了进来。
func (n *AwaitExpr) Span() token.Span { return n.S }

// IsLValue reports whether e can appear on the left of `=`.
//
// 设计者视角：可赋值目标只有三张面孔——变量名、索引、字段（`a`/`a[b]`/`a.b`），好比
// 收件地址必须具体到户，`f()`/`a+b`/`(x)` 这类临时值没有门牌收不了件。障碍是括号：
// `(a) = 1` 在 Lua 非法，AST 保留 ParenExpr 即天然拒收，无需额外判括号。候选：在 parser
// 报错 vs 暴露 IsLValue 给各方复用；选后者，让 parser 的赋值校验与后续 checker/emitter
// 共用同一口径。形状速览：
//
//	LValue ::= Ident | IndexExpr | FieldExpr
//
// 不变量：nil 输入返回 false（不 panic）。排查现场：若合法赋值被拒，先看目标是否被
// ParenExpr 包了一层（去括号即合法），再看是否为 CallExpr 误用。
func IsLValue(e Expr) bool {
	switch e.(type) {
	case *Ident, *IndexExpr, *FieldExpr:
		return true
	}
	return false
}
