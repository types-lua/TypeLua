package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/expand"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// 物理布局（单文件单遍历状态机，一次 CheckFile 走完）：
//   scopes 栈 + retStack 返回契约 + label/goto 跳转表 + tparam/vararg 泛型栈
//   + classStack/asyncStack 类与异步 + Index（LSP 只读副本）。
// 数据流：ast.File → ExpandFile 宏展开 → newScope(Ambient 或 fresh) → checkBlock
//   → collectTypes 登记 → checkStmt 逐句 → checkReturn → moduleType 取顶层 return M。
// 例：`local P: Person = {name="阿强"}` 在 checkBlock 里登记 P、逐字段校验，错填 any 续查。
// 类型解析：TypeNode → 原语特判 → 泛型表 → typeEntry（enum 一次 buildEnum 缓存；
// class 用 shell+回填撑住递归；泛型 alias/interface 按 args 拼 key 缓存，inflight 壳防环）。
// 协作：只读 AST、不产码，类型全进符号表，供 compiler/lsp 消费。
// 排查：nil 误报先看 Strict 组合（见 toTypes），递归成员悬空先查壳回填。

// Checker 是单文件单遍历的状态机：配项、符号、作用域栈与 CheckFile 驱动。

// Options 是严格度开关（对标 tsconfig strict）：管 nil 与隐式 any。
//
// 设计者视角：Options 是检查器的“总闸刀”，好比施工图的抗震等级章——Strict
// 一开即 StrictNilChecks 强制为真（见 toTypes），NoImplicitAny 则管住无标注
// 参数。障碍是 Lua 动态习惯与严格类型的拉扯：默认放行隐式 any，只收紧 nil，
// 避免把存量脚本一夜全报红。候选：多档精细开关 vs 三开关；选三开关，映射
// tsconfig 心智。Target 负责版本门禁（如 coroutine.close），RequireTypes 与
// Ambient 由 compiler 驱动注入（见 ambient.go）。不变量：Options 只读，检查
// 中途不改。排查现场：nil 误报先看 Strict/StrictNilChecks 组合，版本 API 误报
// 先看 Target 是否透传。
type Options struct {
	Strict          bool
	StrictNilChecks bool
	NoImplicitAny   bool
	// Target selects version-gated builtins ("5.4" default, "5.1" hides
	// 5.4-only APIs like coroutine.close). Empty means latest.
	Target string
	// RequireTypes maps dotted require paths ("a.b.c") to module types
	// resolved by the driver (nil = every require is any, classic mode).
	RequireTypes map[string]types.Type
	// Ambient is an optional parent scope holding builtins + global .d.tl
	// declarations (like tsconfig types/typeRoots). When nil, the checker
	// defines builtins in a fresh top scope (classic single-file mode).
	// When set, the file is checked in a child scope so locals shadow
	// ambient without DuplicateType errors and rename stays detached.
	Ambient *Scope
}

// DefaultOptions returns the checker options used by the driver when no
// explicit tsconfig is supplied: strict + strict-nil-checks, implicit any
// allowed (Lua keeps untyped locals permissive).
//
// 设计者视角：DefaultOptions 是“无配置时的出厂刻度”——严格但不苛刻：nil 要管，
// 隐式 any 先放行。障碍是新用户第一印象：若默认把未标注参数全报 ImplicitAny，
// 入门示例都飘红；故 NoImplicitAny 默认为 false。取舍：安全（nil）优先于完备
// （any）。不变量：返回值独立，每次调用新造 Options。排查现场：若无标注参数
// 被误报，先查调用方是否误开了 NoImplicitAny 而非默认。
func DefaultOptions() Options {
	return Options{Strict: true, StrictNilChecks: true, NoImplicitAny: false}
}

func (o Options) toTypes() types.Options {
	strictNil := o.StrictNilChecks
	if o.Strict {
		strictNil = true
	}
	return types.Options{StrictNilChecks: strictNil}
}

// symKind 区分值绑定的三种出身：变量/函数/参数。
type symKind int

const (
	symVar symKind = iota
	symFunc
	symParam
)

// Symbol 是值名的一次落户口：`local x = 1` 登记 x 的语义类型，窄化影子经 SameAs 链回原定义。
//
// 设计者视角：Symbol 是值命名空间的一张“户口卡”，好比监理台账上的一行——
// Type 记语义类型，SameAs 把窄化影子（见 narrow.go）链回原定义，保证引用/
// 重命名跨作用域仍归一。障碍是窄化会在分支作用域复制同名 Symbol，若不链回，
// LSP 会把分支内的使用算成新定义。候选：Scope 外挂窄化表 vs 影子 Symbol；选
// 影子 Symbol，查找逻辑零改动（lookupVar 照常），代价是 bindVar 要维护链。
// 不变量：SameAs 终点恒为定义点（见 symRoot）；IsBuiltin 为真则永不被链。
// 排查现场：重命名漏分支引用，先查影子的 SameAs 是否指向定义。
type Symbol struct {
	Name      string
	Kind      symKind
	Type      types.Type
	Span      token.Span
	IsConst   bool
	IsBuiltin bool
	// SameAs links narrowing shadows (and similar ephemeral copies) back
	// to the definition symbol for references/rename.
	SameAs *Symbol
	// For functions with generics.
	GenericNames []string
	FuncDecl     *ast.FuncLit
}

type typeEntry struct {
	name         string
	span         token.Span // declaration name span (for goto-definition)
	params       []*ast.GenericParam
	alias        *ast.TypeAliasDecl
	iface        *ast.InterfaceDecl
	enumDecl     *ast.EnumDecl
	enumType     *types.Enum
	classDecl    *ast.ClassDecl
	classDef     *types.ClassDef
	classBuild   *classBuild
	classShell   *types.Class
	classPatches []func()
	resolving    bool
	cached       types.Type // for non-generic
	cacheInst    map[string]types.Type
	// inflight tracks in-progress builds per instantiation key for
	// recursive declarations: a cycle re-entry shares a shell object
	// (nil = building, non-nil = shell handed out), filled on completion.
	inflight map[string]*types.Object
}

// TypeEntry 是 typeEntry 的导出别名：LSP 比指针认身份（如两个同名 Dog 只认声明点）。
//
// 设计者视角：TypeEntry 是类型声明的“身份证指针”——LSP 比较身份只比指针，
// 不比名字（重名遮蔽下名字不可靠）。障碍是 typeEntry 未导出而 LSP 要用：
// 若另造 ID 映射会增加同步成本。用类型别名（非新类型）让内部与外部看到同一
// 指针，零转换。取舍：暴露实现细节换导航零成本。不变量：同一声明恒同一指针
// （见 collectTypes 只登记一次）。排查现场：成员归属错乱先查 ownerEntry 回查
// 的指针是否为声明点。
type TypeEntry = typeEntry

// classBuild 是类的一遍签名快照：`class Box<T>` 的 T 原样保留，供每次引用再 Substitute。
//
// 设计者视角：classBuild 是类的一遍产物——“签名快照”，泛型占位符原样保留，
// 好比模具而非成品：每次 Point<T> 引用时再 Substitute 出实例。障碍是类体内
// 自引用（next: Box<T>）：若一遍就求具体类型会鸡生蛋；保留占位符 + shell
// （见 ensureClassDef）让递归先占位后回填。候选：一遍全算 vs 签名/体两遍；
// 选两遍，签名先行供继承/override 检查。不变量：inst/val 的泛型维度与声明一致。
// 排查现场：泛型类实例成员丢失，先查此处占位符是否被提前实例化。
type classBuild struct {
	inst *types.Class
	val  *types.ClassValue
	sigs map[*ast.ClassMember]*classMemberSig
}

// classCtx 记住“正在查哪个类的方法体”，管 self/super 与 static 合法性。
//
// 设计者视角：classCtx 是方法体检查时的“施工围挡”——记当前类 Def、定义态
// inst、基类 base/baseVal，以及 static/ctor 标记。障碍是 self/super 的合法性
// 与返回栈深度耦合：闭包里的 super 必须失效，故用 retDepth 快照方法入口时的
// retStack 深度（见 checkSuperCall）。候选：全局变量记当前类 vs 栈；选栈，
// 嵌套类/闭包自然隔离。不变量：进方法压栈、出方法弹栈，长度恒配对。排查现场：
// super 误放行先查 retDepth 与 len(retStack) 是否相等。
type classCtx struct {
	def           *types.ClassDef
	name          string
	inst          *types.Class // definition-form instance (placeholders)
	base          *types.Class
	baseVal       *types.ClassValue
	inStatic      bool
	inConstructor bool
	retDepth      int // len(retStack) at method entry; super only applies at this depth
}

// Scope 是词法层：parent 上链 + vars/types 两本册（值名与类型名分家，`local Foo` 与 `type Foo` 可共存）。
//
// 设计者视角：Scope 是词法作用域的“一室两册”——vars 管值名、types 管类型名，
// 互不串味（local Foo 与 type Foo 可共存），parent 链向上查。障碍是类型与值
// 同名污染：若合表，类名（双栖）会互相覆盖；分表让类同时占两册（见 checkClass）。
// 图：
//
//	Ambient → File顶层 → block → 函数/方法（push/pop 成对）
//
// 候选：扁平符号表 + 层号 vs 父子链；选父子链，遮蔽天然表达。不变量：只写
// 当前层，只读向上走。排查现场：名字莫名找不到，先查是否在子层 bind 却到父层
// lookup，或 Ambient 隔离导致 DuplicateType 误报。
type Scope struct {
	parent *Scope
	vars   map[string]*Symbol
	types  map[string]*typeEntry
}

func newScope(parent *Scope) *Scope {
	return &Scope{parent: parent, vars: map[string]*Symbol{}, types: map[string]*typeEntry{}}
}

func (s *Scope) lookupVar(name string) *Symbol {
	for sc := s; sc != nil; sc = sc.parent {
		if v, ok := sc.vars[name]; ok {
			return v
		}
	}
	return nil
}

func (s *Scope) lookupType(name string) *typeEntry {
	for sc := s; sc != nil; sc = sc.parent {
		if e, ok := sc.types[name]; ok {
			return e
		}
	}
	return nil
}

// Checker 是一次一文件的遍历状态机：管作用域/返回/跳转/泛型/类/异步栈，只读 AST，结论全进 diags 与符号表。
//
// 设计者视角：Checker 是单文件单遍历的“状态机驾驶舱”，好比监理的随身夹——
// scopes 记词法层、retStack 记函数返回契约、labelStack/gotoList 记跳转、
// tparamStack/varargStack 记泛型与变参、classStack/asyncStack 记类与异步。
// 障碍是 Lua 作用域与类型作用域交织：函数既开值层又开类型参数层，必须成对
// push/pop，否则嵌套函数泄漏泛型。取舍：全部状态放 Checker 而非 AST，还 AST
// 以不变（发射器无感）。不变量：AST 只读；遇错填 any 继续（见 checkExprOne）。
// 排查现场：状态泄漏（如下个函数看到上个泛型）先查 push/pop 是否配对。
type Checker struct {
	opt    Options
	diags  []diag.Diagnostic
	scopes []*Scope
	file   string
	// Narrowing overrides per scope depth? We implement via shadow symbols: narrowedScopes stack.
	// Return checking.
	retStack [][]types.Type // expected returns per function (nil = infer/any)
	// Labels per function.
	labelStack []map[string]token.Span
	gotoList   [][]gotoRef
	// Generic params in scope (type-level).
	tparamStack []map[string]*types.GenericParam
	// Vararg type in current function.
	varargStack []types.Type
	// Inferred returns for unannotated functions (collect).
	inferStack [][]types.Type
	loopDepth  int
	// Instantiation depth for generic alias/interface builds. Self-feeding
	// expansions (`L<L<T>>`, `L<Wrap<T>>`) grow the key forever, so cap it.
	// (Non-generic builds terminate via inflight shells.)
	typeDepth int
	// depthReported suppresses repeat overflow errors per entry.
	depthReported map[*typeEntry]bool
	// Enclosing classes for self/super/visibility.
	classStack []*classCtx
	// asyncStack tracks `async` function nesting (FuncLit/ClassMember
	// IsAsync): `await`/`select` are valid when the stack is empty (the
	// top-level chunk = main task) or its top is true.
	asyncStack []bool
	// spawnDepth counts enclosing `spawn` calls: spawning an async
	// function from sync code is fine (the body becomes a new task), so
	// the TL3805 async-call check is suspended inside.
	spawnDepth int
	// >0 while resolving an extends clause (where `infer` may appear).
	extendsDepth int
	// Index collects position information for LSP when non-nil.
	Index *SymIndex
}

// SymUse 记一次标识符露面：IsDef 为真即定义，否则为使用（如 `x = x + 1` 记两笔）。
//
// 设计者视角：SymUse 是 LSP 的“考勤打卡条”——每个标识符出现记一笔，IsDef
// 区分定义与使用，Entry/Member 区分类型名与成员。障碍是值/类型双命名空间：
// 类名同时占两册，打卡必须一次记两边（见 recordDefEntry），否则重命名漏一半。
// 取舍：记录 best-effort，永不影响诊断（Index 为 nil 即跳过）。不变量：Span
// 为源坐标，Root 经 symRoot 归一。排查现场：跳转缺失先查记录点是否因 Index
// 为 nil 被跳过。
type SymUse struct {
	Span  token.Span
	Sym   *Symbol
	Root  *Symbol
	IsDef bool
	// Entry is set for type-name references (aliases, interfaces...).
	Entry *TypeEntry
	// Member is set for member references (class/interface/enum members).
	Member *MemberRef
}

// MemberRef 记一次成员露面：名义成员认 Owner 指针（如 `d.name`），匿名表字面量认 StructOwner 根变量；两者恰其一。
//
// 设计者视角：MemberRef 是成员的“房产证”——名义成员（类/接口/枚举）认 Owner
// 指针（Entry 身份），匿名表字面量认 StructOwner（根变量）。障碍是索引器解出的
// 字段无定义点：若硬记会把 `m["x"]` 全并到一起；故只有 lookupNamedField 命中的
// 声明成员才记（见 recordMemberUse）。候选：名/结构统一 vs 双轨；选双轨，名义
// 走声明、结构走变量直流（别名不追踪防误合并）。不变量：Owner 与 StructOwner
// 恰其一。排查现场：成员引用漏记先查是否走索引器分支，误合并先查别名是否被追踪。
type MemberRef struct {
	Owner       *TypeEntry
	OwnerName   string // e.g. "Dog", or the variable name for structural
	Name        string // member name
	Kind        string // "method", "field", or "member" (kind is "" at use sites)
	StructOwner *Symbol
}

// ScopeSpan 把一层 Scope 钉到拥有它的块 Span 上，供补全按偏移找最深层。
//
// 设计者视角：ScopeSpan 是作用域的“户型图钉”——Sc 记符号表，Span 记其语块
// 范围，供 ScopeAt 按字节偏移找最深层（补全/悬停先定位层）。障碍是块与层
// 生命周期错位：checkBlock 内 push 的层必须与块 Span 同记，否则查询漂移。
// 取舍：记录时拷贝指针（Scope 不再动结构只增符号亦可接受，见 LSP 查询只读）。
// 不变量：Span 含左右括号的块全域。排查现场：补全变量缺失先查此处 Span 是否
// 把函数体包小了。
type ScopeSpan struct {
	Sc   *Scope
	Span token.Span
}

// SymIndex 是给 LSP 的只读副本库：best-effort 记录，永不影响诊断（如开补全不改报错）。
//
// 设计者视角：SymIndex 是给 LSP 的“只读副本库”——Uses 记每次打卡，Scopes 记
// 每层范围。障碍是检查与语言服务抢状态：若记录影响诊断，一次补全会改下次报错。
// 故铁律：所有 record* 先判 Index==nil 即返，从不 errorf。取舍：内存换体验，
// 常驻全量 Uses（重命名/引用一次遍历即得）。不变量：记录失败永不报错。排查现场：
// 若开 LSP 后诊断变了，必是有人把记录分支写成了检查分支。
type SymIndex struct {
	Uses   []SymUse
	Scopes []ScopeSpan
}

// symRoot 顺 SameAs 链一路找到定义点（如三次窄化影子回同一 `local x`）。
//
// 设计者视角：symRoot 是影子链的“寻根绳”——窄化/重定义产生的影子 Symbol 经
// SameAs 一路回溯到定义点，引用与重命名才不散。障碍是链可能多跳（多次窄化
// 叠加）且含 nil：必须循环判空，不能只跳一层。候选：递归 vs 循环；选循环，
// 防超长链栈溢出。不变量：返回 nil 或 SameAs==nil 的端点。排查现场：引用聚类
// 分裂先查是否有影子忘记设 SameAs（直接入 vars 而未链）。
func symRoot(s *Symbol) *Symbol {
	for s != nil && s.SameAs != nil {
		s = s.SameAs
	}
	return s
}

type gotoRef struct {
	name string
	span token.Span
}

// New 只装 Options 发白纸：栈全空，创后须走 CheckFile* 驱动（声明文件走 BuildAmbient）。
//
// 设计者视角：New 只做“发白纸”——装 Options，其余栈全空，scopes/diags 由各
// 驱动入口自建（checkFileModule/BuildAmbient）。障碍是 Ambient 复用：若在此
// 建顶层层会与传入 Ambient 打架；故一律延迟到驱动处按有无 Ambient 分流。
// 取舍：构造极简，分支留给驱动。不变量：New 后 scopes 为空，不可直接 checkBlock。
// 排查现场：空指针 panic 于 cur()，多为跳过驱动直接用 New 后未设 scopes。
func New(opt Options) *Checker {
	return &Checker{opt: opt}
}

// Diags 按源序交出整改单（含宏展开前置诊断在前）。
//
// 设计者视角：Diags 是“整改单出窗口”——检查中 errorf 按遍历序追加，此处原样
// 交出。障碍是宏展开前置也会产诊断（见 expand.ExpandFile），必须先于检查诊断
// 入列以保源序。取舍：返回内部切片（不拷贝），调用方只读。不变量：源序即报序。
// 排查现场：诊断乱序先查是否有人排序或并发追加（Program.Check 为顺序复用）。
func (c *Checker) Diags() []diag.Diagnostic { return c.diags }

func (c *Checker) errorf(code diag.Code, sp token.Span, format string, args ...any) {
	c.diags = append(c.diags, diag.Errorf(code, sp, format, args...))
}

func (c *Checker) cur() *Scope { return c.scopes[len(c.scopes)-1] }
func (c *Checker) push()       { c.scopes = append(c.scopes, newScope(c.cur())) }
func (c *Checker) pop()        { c.scopes = c.scopes[:len(c.scopes)-1] }

// bindVar 落户口：同层重定义经 SameAs 链起旧户（如 `local print = print` 的内置旧户不断链防污染全局引用）。
//
// 设计者视角：bindVar 是值册的“落户口”——同层重定义经 SameAs 链起旧户，新户
// 仍覆盖 vars[name] 保证查找见新。障碍是内置函数本地化（local print=...）：
// 若链起会把全文件的 print 引用并到局部；故 IsBuiltin 旧户不断链、保持脱钩。
// 候选：报错阻断 vs 链起容忍；选链起（重定义另报 Redefinition，引用不断）。
// 不变量：当前层写入，父层不动。排查现场：重命名把内置也改了，先查旧户
// IsBuiltin 是否丢失。
func (c *Checker) bindVar(name string, sym *Symbol) *Symbol {
	if old, ok := c.cur().vars[name]; ok && !old.IsBuiltin {
		sym.SameAs = old
	}
	c.cur().vars[name] = sym
	return sym
}

// CheckFile 薄封装：只要诊断不要索引（如 CI 门禁），内部转调 CheckFileIndexed 再丢索引。
//
// 设计者视角：CheckFile 是最薄的“便民窗口”——只要诊断不要索引，内部委托
// CheckFileIndexed 并丢掉索引。障碍是调用方误以为无索引更快：实则索引恒
// 构造（best-effort 开销极小），省不掉。取舍：保留此薄封装，兼容单文件驱动。
// 不变量：语义等价于 CheckFileIndexed 的首返回值。排查现场：要跳转却调此处，
// 应换 CheckFileIndexed/Module。
func CheckFile(f *ast.File, opt Options) []diag.Diagnostic {
	diags, _ := CheckFileIndexed(f, opt)
	return diags
}

// CheckFileIndexed 是 LSP 主入口：诊断与索引一次遍历双收，索引恒非空。
//
// 设计者视角：CheckFileIndexed 是“LSP 主入口”——诊断与索引一次遍历双收，索引
// 恒有值（Index 在 checkFileModule 内即分配）。障碍是有人想关索引省内存：
// 记录分支全是追加，无开关反而少分叉。取舍：恒开 best-effort 记录。不变量：
// 返回的 idx 非 nil。排查现场：索引为空先查文件 Block 是否为 nil（空文件无层）。
func CheckFileIndexed(f *ast.File, opt Options) ([]diag.Diagnostic, *SymIndex) {
	diags, idx, _ := checkFileModule(f, opt)
	return diags, idx
}

// CheckFileModule 多交一块：顶层 `return M` 里 M 的类型（非 Ident 如 `return {}` 一律 Any）。
//
// 设计者视角：CheckFileModule 是跨文件的“交接单”——除诊索引外，再报模块出口
// 类型（顶层 return M 查顶层残留层）。障碍是 return 可为任意表达式：只认
// Ident 直流，其余一律 Any，避免把复杂表达式类型误作模块契约。候选：全表达式
// 求值 vs 仅标识符；选后者，契约简单可依赖。不变量：无顶层 return 即 Any。
// 排查现场：require 取到 any，先查模块末尾是否 `return M` 且 M 为局部表。
func CheckFileModule(f *ast.File, opt Options) ([]diag.Diagnostic, *SymIndex, types.Type) {
	return checkFileModule(f, opt)
}

func checkFileModule(f *ast.File, opt Options) ([]diag.Diagnostic, *SymIndex, types.Type) {
	c := New(opt)
	c.file = f.Name
	// Macros expand before checking (definitions erase; idempotent).
	c.diags = append(c.diags, expand.ExpandFile(f)...)
	if opt.Ambient != nil {
		c.scopes = []*Scope{newScope(opt.Ambient)}
	} else {
		c.scopes = []*Scope{newScope(nil)}
		c.defineBuiltins(c.cur())
	}
	c.Index = &SymIndex{}
	if f.Block != nil {
		c.recordScope(c.cur(), f.Block.Span())
	}
	c.checkBlock(f.Block)
	return c.diags, c.Index, c.moduleType(f)
}

// moduleType 只认块末 `return M` 的 Ident（如 `return M` 查残留顶层，`do return M end` 不认）。
//
// 设计者视角：moduleType 是模块出口的“点名册”——只在存活顶层层里找 return
// 首表达式若为 Ident 的绑定。障碍是检查中途层已弹栈：必须用“残留顶层”（c.cur()
// 此时即文件顶层），而非新建层。候选：遍历找 ReturnStmt vs 只看 Block.Ret；
// 选后者（Lua 只允块末 return）。不变量：类型 nil 即 Any，不透 nil。排查现场：
// 模块类型意外 Any，先查 return 是否包在 do/if 里（非顶层不认）。
func (c *Checker) moduleType(f *ast.File) types.Type {
	if f.Block == nil || f.Block.Ret == nil || len(f.Block.Ret.Exprs) == 0 {
		return types.Any
	}
	if id, ok := f.Block.Ret.Exprs[0].(*ast.Ident); ok {
		if s := c.cur().lookupVar(id.Name); s != nil && s.Type != nil {
			return s.Type
		}
	}
	return types.Any
}
