package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// Statement nodes.

// ---------- Statements ----------

// ReturnStmt 表示 return 语句节点；Exprs 为返回表达式列表，S 为源码 Span，用于诊断与定位。
//
// 设计者视角：ReturnStmt 永不进 Block.Stmts、只住 Block.Ret，把“return 必须在块末”
// 从校验规则升级为结构不变量——想放错位置都没地方放。Exprs 为空即裸 return；
// 多值展开只看末位（checker 的 checkExprListMulti 同理）。排查现场：若 return 诊断
// 圈到块尾，检查 parser 是否把 Ret.S 独立于 Block.S 存放。
type ReturnStmt struct {
	S     token.Span
	Exprs []Expr
}

func (*ReturnStmt) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：Return 的 S 从 return 关键字到末表达式尾，空返回即关键字本身；
// 调用点元数不匹配圈整段，缺返回值圈关键字。排查现场：若 return 高亮飞到块尾 end，
// 检查 Block.S 与 Ret.S 是否混用。
func (n *ReturnStmt) Span() token.Span { return n.S }

// EmptyStmt 表示空语句节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：空语句是“多余分号与容错坑位”的收容所——`;;` 不报错只产 EmptyStmt，
// 解析失败的同步点也用它占位，保证 Block.Stmts 永不断档。emitter 直接跳过，
// checker 无视。不变量：S 可为单点（分号位置）。排查现场：若同步后语句丢失，
// 检查是否误把 EmptyStmt 当错误节点裁掉。
type EmptyStmt struct{ S token.Span }

func (*EmptyStmt) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：EmptyStmt 的 Span 即分号/空位单点，诊断默认忽略它，避免“空语句错误”
// 刷屏。排查现场：若空语句被圈红，检查 parser 是否误报而非 AST 之过。
func (n *EmptyStmt) Span() token.Span { return n.S }

// ExprStmt 表示表达式语句节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：能独立成句的表达式只有调用（Call/MethodCall，含 await 包裹）——parser 的
// parseExprStmtOrAssign 在 `=` 缺席时强制此门禁，其它表达式一律报“expected assignment
// or function call”。宏体内的裸 Ident 是例外（待替换的语句占位）。E 存原表达式（含 await
// 壳），S 与 E.Span 同范围。排查现场：若 `f(x)` 被报非语句，检查 await 剥壳循环是否先行。
type ExprStmt struct {
	E Expr
	S token.Span
}

func (*ExprStmt) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：ExprStmt 的 S 即内层调用全段，副作用诊断圈整句。排查现场：若调用语句
// 报错只圈函数名，检查是否用了 Func 子 Span 而非本 S。
func (n *ExprStmt) Span() token.Span { return n.S }

// AssignStmt 表示赋值语句节点 `a, b = x, y`；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：多重赋值是 Lua 的“拉链对齐”——Targets 与 Values 按位拉合，多退少补
// （末值多返回展开、缺值补 nil），语义由 checker/emitter 按 Lua 规则消化，AST 只保序
// 搬运。Targets 合法性（IsLValue）由 parser 在 `=` 处一次验明，后续阶段不再复验。
// 不变量：Targets 永非空。排查现场：若左右元数诊断错位，检查逗号收集时 Targets/Values
// 是否等长切片错配，而非类型本身。
type AssignStmt struct {
	Targets []Expr
	Values  []Expr
	S       token.Span
}

func (*AssignStmt) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：Assign 的 S 从首目标到末值尾，左右不匹配圈整句，单个目标非法圈该目标
// 子 Span。排查现场：若赋值报错圈到等号，检查子 Span 是否丢失而回退到整句。
func (n *AssignStmt) Span() token.Span { return n.S }

// LocalAssign 表示局部变量声明语句 `local x: T = v`；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：local 是“四轨并行”——Names/Attrs/Types/Values 按位对齐，缺位即 nil/空串
// （无注记、无初值皆合法）。Attrs 只认 "const"/"close"，空串占位保对齐。障碍是
// `local x: T` 与 `local x = v` 的交叉缺省：AST 允许 Types/Values 任一缺席，
// “声明无值、赋值无注记”皆有去处，合法性由 checker 按作用域复核。不变量：Names 永非空，
// 四切片等长或缺省为空。排查现场：若 const/close 语义丢失，检查 Attrs 是否按位错配到
// 邻名。
type LocalAssign struct {
	Names  []*Ident
	Attrs  []string // e.g. "const", "close"; "" if none
	Types  []TypeNode
	Values []Expr
	S      token.Span
}

func (*LocalAssign) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：LocalAssign 的 S 从 local 到末值/末类型尾，未初始化变量的 S 止于名字尾，
// 让“未使用局部变量”仍有坐标。排查现场：若注记类型报错圈到变量名，检查是否误用本 S
// 而非 Types[i].Span()。
func (n *LocalAssign) Span() token.Span { return n.S }

// LocalFuncDef 表示局部函数定义语句 `local function f() ... end`；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：局部函数是“local + FuncLit 的糖衣”——Name 挂名、Func 装肉身（Params/Body/Generics
// 全在 FuncLit 里），Exported 标记是否出模块。parser 不把 `local function` 降为 LocalAssign，
// 保住函数体的独立折叠与跳转。不变量：Func 永非 nil。排查现场：若局部函数递归自引用失败，
// 检查 checker 是否按 Name 先声明后查体，而非 AST 缺名。
type LocalFuncDef struct {
	Name     *Ident
	Generics []*GenericParam
	Func     *FuncLit
	Exported bool
	S        token.Span
}

func (*LocalFuncDef) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：LocalFuncDef 的 S 覆盖 `local function f ... end` 全段，名字错误圈 Name，
// 体内错误圈 Func.Body——大小坐标分工。排查现场：若函数体重构折叠错位，检查 S.End 是否
// 取到 end 而非末语句尾。
func (n *LocalFuncDef) Span() token.Span { return n.S }

// DeferStmt is `defer <call>` / `errdefer <call>` (soft keywords):// cleanup runs LIFO at function exit — deferred calls on every normal
// return, errdeferred calls only when leaving via an explicit `error()`.
// `defer`/`errdefer` stay plain identifiers everywhere else.
//
// 设计者视角：defer 是“函数出口的后事栈”，LIFO 压栈、正常 return 全跑、errdefer 只在
// error() 抛离时跑（emitter/defer.go 见 isErrorCall 配合 CallExpr.ErrorCall 印章）。
// 障碍是软关键字：defer/errdefer 平时仍是变量名，只有语句首后接调用才转义，避免抢名。
// 候选：调用时求值 vs 闭包延迟捕获；选后者（同 spawn，见 SPEC §12），参数在退出时求值，
// 循环 defer 才不串味。不变量：Call 仅为 CallExpr/MethodCallExpr。排查现场：若 defer
// 参数提前求值，检查 emitter 闭包包裹；若 errdefer 在普通 return 也跑，检查 IsErr 分流。
type DeferStmt struct {
	IsErr bool // errdefer
	Call  Expr // CallExpr or MethodCallExpr
	S     token.Span
}

func (*DeferStmt) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：Defer 的 S 从 defer/errdefer 关键字到调用尾，关键字拼错圈首词，调用
// 参数错圈 Call 子段。排查现场：若 defer 被当普通标识符，检查语句首试探是否后接
// Call 起始（`(` / 标识符调用）。
func (n *DeferStmt) Span() token.Span { return n.S }

// ImportStmt is require sugar:
//
//	import "path"                 (side-effect require)
//	import Name from "path"       (local binding, any)
//	import Name: Type from "path" (annotated binding)
//
// It desugars to `local Name = require("path")` (bare require for the
// side-effect form); `import` stays a plain identifier elsewhere.
//
// 设计者视角：import 是 require 的“三档糖衣”——副作用档 Name 为 nil 只跑 require，
// 绑定档 Name 非空落为本地量，注记档 Type 非空再叠类型。障碍同样是软关键字：非句首
// 的 import 仍是标识符，只有语句首出现字符串/from 形态才转义。候选：AST 存糖 vs 解析期
// 直降为 LocalAssign；选存糖，让未解析模块、循环依赖的诊断能圈回 import 原句，降糖留给
// checker/emitter。不变量：Path 永非 nil；Name 为 nil 即副作用式。排查现场：若 import
// 被当变量，检查句首试探；若路径诊断圈错，检查 Path 独立 Span。
type ImportStmt struct {
	Name *Ident   // nil for side-effect imports
	Type TypeNode // optional annotation
	Path *StringLit
	S    token.Span
}

func (*ImportStmt) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：Import 的 S 覆盖整句 import，便于模块未找到时圈整行；Path/Name 各有子 Span
// 做精确定位。排查现场：若缺模块报错飞到文件头，检查 S 是否含 Path 区间。
func (n *ImportStmt) Span() token.Span { return n.S }

// FuncDefStmt 表示全局/表字段函数定义语句；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：`function a.b:c() end` 是“赋值与函数的合体”——FuncName 记落点（Prefix 路径
// + 可选 Method 冒号），Func 装肉身（复用 FuncLit）。parser 不降为 AssignStmt，保住
// 方法 self 隐参与冒号形态，供 checker 校验定义与声明一致。排查现场：若 `a.b:c` 的 self
// 丢失，检查 IsMethod 是否随 Method 非空而立，再看 FuncName.S 是否含整路径。
type FuncDefStmt struct {
	Name     *FuncName
	Generics []*GenericParam
	Func     *FuncLit
	S        token.Span
}

func (*FuncDefStmt) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：FuncDef 的 S 从 function 到 end，落点错误圈 Name，体内错误圈 Func.Body。
// 排查现场：若全局函数污染检查误报，检查 Name.Prefix 首元是否为 _G 隐式。
func (n *FuncDefStmt) Span() token.Span { return n.S }

// FuncName is a.b.c or a.b:c
// FuncName 表示函数定义的目标名（a.b.c 或 a.b:c）；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：FuncName 把“点路径 + 可选冒号方法”拆成 Prefix 切片与 Method 指针：
// Prefix 全为点段，Method 非空即冒号形（隐含 self）。障碍是 `a.b:c.d` 非法形态：
// parser 在冒号后只收一 Ident，多余点直接报错，避免歧义。不变量：Prefix 永非空；
// Method 非 nil 即方法定义。排查现场：若冒号方法丢失 self，检查 Method 是否为 nil
// 而非名字拼写。
type FuncName struct {
	Prefix []*Ident
	Method *Ident // non-nil if colon form
	S      token.Span
}

// Span returns the source span of this node.
//
// 设计者视角：FuncName 的 S 覆盖 `a.b:c` 全路径，逐段 Ident 另有小坐标，未定义字段
// 圈到对应段而非整路径。排查现场：若路径报错圈不全，检查 S.End 是否取到末段/Method 尾。
func (n *FuncName) Span() token.Span { return n.S }

// DoBlock 表示 `do ... end` 语句块节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：do 是“最纯的作用域括号”——无条件无头，只为隔离局部变量而存在。
// Block 独立成子块，checker 进出压栈即得新作用域，emitter 原样套 do/end。
// 候选：复用 Block vs 单独 DoBlock；选单独，让 scope 边界在 AST 可见，
// “变量逃逸到块外”的诊断有明确载体。排查现场：若局部变量泄漏，检查 Block 划分
// 而非符号表。
type DoBlock struct {
	Block *Block
	S     token.Span
}

func (*DoBlock) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：Do 的 S 含 do/end 关键字，折叠整块；内层 Block.S 只圈语句区。
// 排查现场：若空 do 块无坐标，检查 S 是否仍含关键字区间。
func (n *DoBlock) Span() token.Span { return n.S }

// IfStmt 表示 if/elseif/else 语句节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：if 是“一主多备一兜底”——Cond/Then 主分支，Elseifs 切片按序试探，
// Else 兜底（nil 即无）。parser 把 elseif 收为独立 ElseifClause 而非嵌套 IfStmt，
// 让“分支覆盖统计”一次遍历即全。checker 对 Cond 做真值窄化，各分支 Block 独立作用域。
// 排查现场：若 elseif 条件类型污染主分支，检查窄化是否按分支隔离，而非 AST 串块。
type IfStmt struct {
	Cond    Expr
	Then    *Block
	Elseifs []*ElseifClause
	Else    *Block
	S       token.Span
}

func (*IfStmt) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：If 的 S 从 if 到 end，全分支诊断可回溯整句；各分支 Cond/Block 另有子 Span
// 做精准圈选。排查现场：若缺 end 的恢复圈错，检查 S.End 是否取到同步点。
func (n *IfStmt) Span() token.Span { return n.S }

// ElseifClause 表示 if 语句中的 elseif 分支；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：elseif 独立成 clause 而非套娃 IfStmt，避免“右偏深链”把遍历栈拉长；
// Cond/Block 与主分支同形，checker 复用同一窄化路径。不变量：Cond/Block 永非 nil。
// 排查现场：若 elseif 悬空（无 then 块），检查 parser 恢复而非 checker。
type ElseifClause struct {
	Cond  Expr
	Block *Block
	S     token.Span
}

// WhileStmt 表示 while 循环语句节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：while 是“先检票后上车”——Cond 在 Block 之前求值，break 跳整句，
// 条件内声明不进体作用域。AST 把 Cond/Block 并列存放，checker 按序先查 Cond 再进体。
// 排查现场：若 `while true` 恒真告警缺失，检查 Cond 的 BoolLit 折叠而非循环结构。
type WhileStmt struct {
	Cond  Expr
	Block *Block
	S     token.Span
}

func (*WhileStmt) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：While 的 S 从 while 到 end，条件错圈 Cond，体错圈 Block；
// 空体循环仍有 end 兜底坐标。排查现场：若 break 跳错层，检查 checker 的循环栈
// 而非此处 S。
func (n *WhileStmt) Span() token.Span { return n.S }

// RepeatStmt 表示 repeat/until 循环语句节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：repeat 是“先上车后检票”——Block 先跑一遍再看 Cond，且体作用域直通
// Cond（until 条件可见体内局部量），这是与 while 的本质区别。AST 把 Block 放前、
// Cond 放后，原文顺序即求值顺序。不变量：Cond 可见 Block 内 locals。排查现场：
// 若 until 引用体内变量报未定义，检查 checker 作用域是否把 Block 延入 Cond。
type RepeatStmt struct {
	Block *Block
	Cond  Expr
	S     token.Span
}

func (*RepeatStmt) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：Repeat 的 S 从 repeat 到 until 条件尾，条件圈 Cond，体内圈 Block。
// 排查现场：若 until 条件定位飞到块首，检查 S.End 是否取到 Cond.End。
func (n *RepeatStmt) Span() token.Span { return n.S }

// NumericFor 表示数值 for 循环语句节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：数值 for 是“三段式计数器”——Start/End/Step（Step 为 nil 即步长 1），
// Var 为隐式局部计数器（VarTy 可注记），Block 为体。AST 保留 Step nil 而非填 1，
// 让“显式步长 vs 默认步长”的诊断与发射各取所需。不变量：Start/End 永非 nil。
// 排查现场：若步长缺省被当 0，检查 emitter 的 nil 回退而非 parser。
type NumericFor struct {
	Var   *Ident
	VarTy TypeNode
	Start Expr
	End   Expr
	Step  Expr // may be nil
	Block *Block
	S     token.Span
}

func (*NumericFor) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：NumericFor 的 S 从 for 到 end，变量/起止/步长各有子 Span，步长缺省时
// S 仍全包。排查现场：若循环变量重定义误报，检查 Var.S 与外层同名量区分。
func (n *NumericFor) Span() token.Span { return n.S }

// GenericFor 表示泛型 for 循环语句节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：泛型 for 是“迭代器三件套”——Names（1..n 个接盘变量）、Exprs（1..3 个
// 迭代源：工厂/状态/初值）、Types 可选注记。AST 用双切片保序，元数合法性（ipairs/pairs
// 约定）由 checker 按迭代器协议校验。不变量：Names/Exprs 永非空。排查现场：
// 若迭代变量类型对不上，检查 Types[i] 与 Names[i] 是否按位错配。
type GenericFor struct {
	Names []*Ident
	Types []TypeNode
	Exprs []Expr
	Block *Block
	S     token.Span
}

func (*GenericFor) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：GenericFor 的 S 同样首尾全包，迭代源错误圈 Exprs，变量错误圈 Names。
// 排查现场：若 `in` 关键字丢失仍有坐标，检查 S 是否含 in 区间做恢复锚点。
func (n *GenericFor) Span() token.Span { return n.S }

// BreakStmt 表示 break 语句节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：break 是“无载荷的跳出符”，空 struct 即全部信息，合法性全看上下文
// （必须身处循环，checker 持循环栈判定）。AST 不记跳层数，Lua break 只跳一层。
// 排查现场：若 break 报“不在循环内”误报，检查 checker 循环栈是否把 for/select 漏压。
type BreakStmt struct{ S token.Span }

func (*BreakStmt) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：Break 的 S 即关键字单点，跳出目标由 checker 的循环栈回填诊断附加信息。
// 排查现场：若 break 高亮为空，检查是否为合成节点带零值 Span。
func (n *BreakStmt) Span() token.Span { return n.S }

// GotoStmt 表示 goto 语句节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：goto 是“向前/向后跳的单程票”，Label 记目的地名，合法性（标签存在、
// 不跳进块作用域）由 checker 全程序复核。AST 只记名字符串，不记目标指针，避免
// 前向引用时悬空。不变量：Label 永非 nil。排查现场：若标签存在仍报未定义，检查
// 大小写与 LabelStmt.Name 是否一致，而非跳转逻辑。
type GotoStmt struct {
	Label *Ident
	S     token.Span
}

func (*GotoStmt) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：Goto 的 S 从 goto 到标签名，缺标签名圈 goto 尾。排查现场：若跳转与作用域
// 诊断混淆，检查 Label 子 Span 与本 S 的分工。
func (n *GotoStmt) Span() token.Span { return n.S }

// LabelStmt 表示标签语句节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：Label 是 goto 的“着陆垫”——`::name::` 独占一句，Name 记名。
// checker 收集全函数标签表再回填 goto，避免前向标签无处可跳。排查现场：
// 若标签重复定义漏报，检查收集表是否按函数作用域隔离。
type LabelStmt struct {
	Name *Ident
	S    token.Span
}

func (*LabelStmt) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：Label 的 S 含两侧 `::`，悬空标签（无对应 goto）圈整段提示清理。
// 排查现场：若标签与变量同名混淆，检查命名空间是否独立（标签自成一表）。
func (n *LabelStmt) Span() token.Span { return n.S }

// SpawnStmt is `spawn <call>` (soft keyword): spawns the call as a
// concurrent task on the cooperative scheduler. Arguments are captured
// lazily in a closure (same convention as `defer`: see SPEC §12), i.e.
// evaluated when the task starts, not at the `spawn` site.
// `spawn` stays a plain identifier elsewhere (`local spawn = 1`, `spawn(x)`).
//
// 设计者视角：spawn 是“开协程的打火机”——只接受调用（Call/MethodCall，可带 TypeArgs），
// 参数同样闭包延迟捕获（与 defer 同约 SPEC §12），任务启动时才求值，避免“spawn 时快照
// 与启动时现值”错位。障碍同为软关键字：`local spawn` 与 `spawn(x)` 的函数调用形态
// 仍是普通标识符，只有语句首 `spawn f()` 才转义。不变量：Call 仅为调用形。排查现场：
// 若 spawn 参数提前求值，检查 emitter 闭包包裹；若被当普通调用，检查句首试探。
type SpawnStmt struct {
	Call Expr // CallExpr or MethodCallExpr (may carry explicit type args)
	S    token.Span
}

func (*SpawnStmt) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：Spawn 的 S 从 spawn 到调用尾，调度器挂起点诊断圈整句，参数错误圈 Call。
// 排查现场：若 spawn 在非 async 内误放行，检查 checker 的 async 上下文判定。
func (n *SpawnStmt) Span() token.Span { return n.S }

// SelectStmt is Go-style `select { case ... default ... }` over channels:
// each case is one send / receive / timeout guard plus a body. It suspends
// the current async task until one guard is ready (or `default` fires
// immediately). Only valid inside `async` (checker-enforced).
//
// Source form (braces, no `end`, mirroring class/enum bodies):
//
//	select {
//	  case v = ch:recv() then
//	    print(v)
//	  case ch:send(x) then
//	    print("sent")
//	  case timeout(1.0) then
//	    print("timed out")
//	  default then
//	    print("busy")
//	}
//
// 设计者视角：select 是“多路 channel 的等红灯”——Cases 逐个挂 guard，Default（nil 即无）
// 立即放行。形态学：花括号无 end，与 class/enum 体同构，parser 复用 brace 体恢复；
// 每个 case 的 guard 仅三选一（Recv/Send/Timeout），由 SelectCase 独占字段保证。
// 障碍是 async 约束：挂起只能在 async 内，checker 强制拦截。DS 记 default 关键字位置，
// 便于“重复 default”精准报错。不变量：Cases 可空但 Default 单一。排查现场：若 select
// 在同步函数放行，检查 async 标记；若 guard 种类混淆，检查 SelectCase 独占字段。
type SelectStmt struct {
	Cases   []*SelectCase
	Default *Block
	DS      token.Span // span of `default` (zero if absent)
	S       token.Span
}

func (*SelectStmt) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：Select 的 S 覆盖 `select {...}` 全括号体，guard 错误圈 SelectCase.S，
// default 重复圈 DS。排查现场：若 case 体报错圈到整 select，检查是否误用父 Span。
func (n *SelectStmt) Span() token.Span { return n.S }

// SelectCase is one `case <guard> then <block>` in a select.
//
// 设计者视角：单个 case 是“guard + body”的二段式——guard 三选一独占：Recv（`v[,ok] =
// ch:recv()`，RecvVars 1-2 个，RecvLocal 标记 `case local`）、Send（`ch:send(v)` 方法名
// 必须为 send）、Timeout（`sleep/timeout(sec)` 单数值参）。障碍是三种 guard 外形迥异但
// 共处一槽：用“恰一非空”不变量 + parser 按方法名/函数名分流，误配在解析期即报错。
// Block 为命中后执行体。不变量：Recv/Send/Timeout 恰一有效；RecvVars 长度 1-2。
// 排查现场：若 recv 变量泄漏到 case 外，检查 RecvLocal 与 Block 作用域隔离。
type SelectCase struct {
	// Guard kinds (exactly one non-nil, or Timeout):
	//   Recv: `v = ch:recv()` / `v, ok = ch:recv()` (RecvVars 1-2 idents)
	//   Send: `ch:send(v)` (MethodCallExpr, method "send")
	//   Timeout: `sleep(sec)` / `timeout(sec)` (CallExpr, 1 numeric arg)
	Recv      *MethodCallExpr // method must be "recv"
	RecvVars  []*Ident        // 1-2 locals bound in the case body
	RecvLocal bool            // `case local v = ...`
	Send      *MethodCallExpr // method must be "send"
	Timeout   Expr            // seconds expression
	Block     *Block
	S         token.Span
}

// Span returns the source span of this node.
//
// 设计者视角：SelectCase 的 S 从 case 到 block 尾，guard 写错圈 guard 子段，体错圈 Block。
// 排查现场：若 timeout 参数非法圈不到，检查 Timeout.Expr 子 Span。
func (n *SelectCase) Span() token.Span { return n.S }
