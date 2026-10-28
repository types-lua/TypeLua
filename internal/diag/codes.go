package diag

import (
	"fmt"
	"sort"
	"strings"
)

// Code 是稳定的诊断标识符（`TLnnnn`）。编译器能发出的每条诊断
// 都在这里声明一次：一个类型化常量加一个表条目（类别 + 简短描述）。
// 调用点使用常量（`diag.AwaitOutsideAsync`），从不用原始字符串，
// 因此拼写错误是编译错误而非静默的错误码。
//
// 设计者视角：Code 是跨工具的“契约号”——好比国标件号，IDE/测试/用户脚本按号匹配，
// Message 措辞随便改都不碎。障碍是字符串字面量易拼错且难重构：此处用类型化常量收敛，
// 拼错即编译失败。数值分段（TL1xxx 词法、TL2xxx 语法、TL3xxx 检查、TL4xxx 工程）让
// 看到前缀即知去哪个车间查。不变量：已发布编号永不复用、只追加；codeTable 与常量
// 由测试强制配对。排查现场：若新增诊断无码可用，先按阶段选段追加，次跑同步测试验配对。
//
// 数值范围（为工具与 SPEC 引用保持稳定）：
//
//	TL1xxx  词法
//	TL2xxx  语法 (TL203x 类成员, TL2038-TL2041 宏参数)
//	TL3xxx  检查器 (TL30xx 语句/作用域, TL31xx 类型解析,
//	        TL32xx 表达式/调用, TL33xx 类, TL36xx 高级
//	        类型, TL37xx 宏, TL38xx 并发)
//	TL4xxx  工程 / I-O
type Code string

// Category 按发出阶段分组错误码。
//
// 设计者视角：Category 是“车间牌”——lexer/parser/checker/expander/concurrency/project
// 六车间加 unknown 兜底，让诊断按阶段过滤、按车间统计。障碍是 Code 与阶段本可从前缀推导，
// 但 TL37/TL38 在 TL3 大段里需特判（见 categoryOf），显式存 cat 可避免每次前缀分支。
// 不变量：codeTable 中的 cat 与前缀推导一致（测试可验）。排查现场：若某码被分错车间，
// 先看表条目 cat 是否手误，次看 categoryOf 的 TL37/TL38 特判顺序。
type Category int

const (
	// CatLexer 表示词法阶段。
	CatLexer Category = iota
	// CatParser 表示语法阶段。
	CatParser
	// CatChecker 表示类型检查阶段。
	CatChecker
	// CatExpander 表示宏展开阶段。
	CatExpander
	// CatConcurrency 表示并发语义阶段。
	CatConcurrency
	// CatProject 表示工程/发射阶段。
	CatProject
	// CatUnknown 表示注册表之外的错误码。
	CatUnknown
)

// String 返回 Category 的文本形式。
//
// 设计者视角：它是车间牌的“挂牌名”——MarkdownTable 与 CLI 分组全靠它拼表头。
// 取舍是 switch 手写，未知兜底 "unknown" 而非 panic，让野值诊断仍可打印。
// 不变量：已知车间名全小写、恒定拼写。排查现场：若表格里冒出 unknown，先查该码
// 是否注册，次查 cat 是否误填 CatUnknown。
func (c Category) String() string {
	switch c {
	case CatLexer:
		return "lexer"
	case CatParser:
		return "parser"
	case CatChecker:
		return "checker"
	case CatExpander:
		return "expander"
	case CatConcurrency:
		return "concurrency"
	case CatProject:
		return "project"
	}
	return "unknown"
}

// ---- 词法 (TL1xxx) ----

const (
	LexUnterminatedLongComment Code = "TL1001"
	LexInvalidCharacter        Code = "TL1002"
	LexMalformedHexNumber      Code = "TL1003"
	LexUnterminatedString      Code = "TL1004"
	LexUnterminatedLongString  Code = "TL1005"
)

// ---- 语法 (TL2xxx) ----

const (
	ParseExpectedToken       Code = "TL2001"
	ParseExpectedGt          Code = "TL2002"
	ParseExpectedLocalName   Code = "TL2003"
	ParseUnknownAttribute    Code = "TL2004"
	ParseInvalidExport       Code = "TL2005"
	ParseInvalidDeclare      Code = "TL2006"
	ParseInvalidAssignTarget Code = "TL2007"
	ParseExpectedAssign      Code = "TL2008"
	ParseExpectedCall        Code = "TL2009"
	ParseExpectedIdentifier  Code = "TL2010"
	ParseExpectedOperand     Code = "TL2011"
	ParseExpectedMethodArgs  Code = "TL2012"
	ParseExpectedExpression  Code = "TL2013"
	ParseExpectedType        Code = "TL2014"
	ParseNestingTooDeep      Code = "TL2015"
	ParseUnexpectedSyntax    Code = "TL2016"

	ParseDuplicateVisibility  Code = "TL2030"
	ParseDuplicateModifier    Code = "TL2031"
	ParseInvalidCtorSignature Code = "TL2032"
	ParseMethodBodyMissing    Code = "TL2033"
	ParseCtorNotMethod        Code = "TL2034"
	ParseDeclareFieldInit     Code = "TL2035"
	ParseFieldNeedsTypeOrInit Code = "TL2036"
	ParseOnlyMethodsAbstract  Code = "TL2037"
	ParseMacroParamReserved   Code = "TL2038"
	ParseMacroParamDuplicate  Code = "TL2039"
	ParseMacroBodyReturn      Code = "TL2040"
	ParseMacroVarargLast      Code = "TL2041"
)

// ---- 检查器：语句与作用域 (TL30xx) ----

const (
	CheckerUnsupportedStmt  Code = "TL3000"
	DuplicateType           Code = "TL3001"
	DuplicateLabel          Code = "TL3002"
	TypeNotAssignable       Code = "TL3010"
	RedefinitionOfLocal     Code = "TL3011"
	CloseVarUninitialized   Code = "TL3012"
	UndefinedTableInFuncDef Code = "TL3013"
	UndefinedVariable       Code = "TL3014"
	AssignToConst           Code = "TL3015"
	AssignTargetTypeError   Code = "TL3016"
	AssignTargetInvalid     Code = "TL3017"
	AssignToReadonly        Code = "TL3018"
	NumericForBoundNotNum   Code = "TL3020"
	VoidFunctionReturns     Code = "TL3030"
	ReturnTypeMismatch      Code = "TL3031"
	ImplicitAnyParam        Code = "TL3040"
	UndefinedLabel          Code = "TL3050"
)

// ---- 检查器：类型解析与声明 (TL31xx) ----

const (
	UnsupportedTypeNode       Code = "TL3100"
	QualifiedTypeUnsupported  Code = "TL3101"
	BuiltinTypeArgCount       Code = "TL3102"
	UndefinedType             Code = "TL3103"
	TypeofUndefinedVariable   Code = "TL3104"
	KeyofUnsupported          Code = "TL3105"
	TypeRecursionLimit        Code = "TL3106"
	TypeArgCount              Code = "TL3107"
	TypeArgConstraint         Code = "TL3108"
	InterfaceExtendsNonObject Code = "TL3109"
	DuplicateField            Code = "TL3110"
	EnumMemberNeedsValue      Code = "TL3120"
	EnumMemberNotLiteral      Code = "TL3121"
	DuplicateEnumValue        Code = "TL3122"
	DuplicateEnumMember       Code = "TL3123"
	CyclicInheritance         Code = "TL3130"
	ExtendsNotClass           Code = "TL3131"
	CyclicInheritanceRef      Code = "TL3132"
)

// ---- 检查器：表达式与调用 (TL32xx) ----

const (
	InvalidExpression      Code = "TL3200"
	UndefinedVariableExpr  Code = "TL3201"
	CannotIndex            Code = "TL3202"
	NoField                Code = "TL3203"
	SuspiciousConversion   Code = "TL3204"
	NonNullOnNil           Code = "TL3205"
	ArithmeticOnNonNumber  Code = "TL3210"
	BitwiseRequiresInteger Code = "TL3211"
	ConcatRequiresString   Code = "TL3212"
	IncomparableTypes      Code = "TL3213"
	UnsupportedOperator    Code = "TL3214"
	LengthOnNonLengthable  Code = "TL3220"
	UnaryMinusOnNonNumber  Code = "TL3221"
	BitwiseNotOnNonInteger Code = "TL3222"
	NoMethod               Code = "TL3230"
	NotCallable            Code = "TL3231"
	ArgCountMismatch       Code = "TL3232"
	ArgTypeMismatch        Code = "TL3233"
	GenericArgsInvalid     Code = "TL3234"
	BareVarargInTaskCall   Code = "TL3235"
)

// ---- 检查器：类 (TL33xx) ----

const (
	SuperOutsideDerivedMethod Code = "TL3301"
	SuperMisuse               Code = "TL3302"
	StaticViaInstance         Code = "TL3303"
	AccessViolation           Code = "TL3310"
	DuplicateClassMember      Code = "TL3320"
	DuplicateConstructor      Code = "TL3321"
	InvalidMemberName         Code = "TL3322"
	ModifierNotAllowed        Code = "TL3323"
	AbstractMemberInvalid     Code = "TL3324"
	ExtendsPrivateCtor        Code = "TL3325"
	InstantiateAbstract       Code = "TL3330"
	CtorAccessViolation       Code = "TL3331"
	FieldNotInitialized       Code = "TL3340"
	ReservedParamName         Code = "TL3341"
	DerivedCtorMustCallSuper  Code = "TL3342"
	SelfBeforeSuper           Code = "TL3343"
	OverrideMismatch          Code = "TL3350"
	UnimplementedAbstract     Code = "TL3351"
	ImplementsNotSatisfied    Code = "TL3352"
)

// ---- 检查器：高级类型 (TL36xx) ----

const (
	InferOutsideExtends  Code = "TL3601"
	CannotMapOverType    Code = "TL3602"
	IndexedAccessInvalid Code = "TL3603"
)

// ---- 展开器：宏 (TL37xx) ----

const (
	UnexpandedMacro            Code = "TL3700"
	DuplicateMacro             Code = "TL3701"
	MacroArityMismatch         Code = "TL3702"
	RecursiveMacro             Code = "TL3703"
	StmtMacroInExprPosition    Code = "TL3704"
	MacroExpandsToInvalidStmt  Code = "TL3705"
	MacroVarargOutsideCallArgs Code = "TL3706"
)

// ---- 检查器：并发 (TL38xx) ----

const (
	AwaitOutsideAsync     Code = "TL3801"
	SelectOutsideAsync    Code = "TL3802"
	MakeChanInvalid       Code = "TL3803"
	SelectGuardInvalid    Code = "TL3804"
	AsyncCallOutsideAsync Code = "TL3805"
)

// ---- 工程 / I-O (TL4xxx) ----

const (
	CannotReadFile Code = "TL4001"
)

// codeDef 是注册表的一行。Name 必须与上面声明的常量匹配
// （测试通过解析本文件强制配对）。
//
// 设计者视角：它是注册表的“一行档案”——code 定编号、name 定常量名、cat 定车间、
// desc 定人话，一行即全部真相。障碍是常量与表易脱钩（加码忘加行）：测试解析本文件
// 强制“每个常量必有一行、每行必有常量”，脱钩即红。不变量：code 唯一、表按码排序。
// 排查现场：若配对测试红，先看新码是否只加了 const 而忘加 codeTable 行。
type codeDef struct {
	code Code
	name string
	cat  Category
	desc string
}

// codeTable 是错误码元数据的唯一真相来源。按错误码排序；
// 测试验证排序、唯一性与上面常量的完整覆盖。
//
// 设计者视角：它是全编译器的“花名册正本”——Category()/Description()/MarkdownTable()
// 都读它，调用点只读常量。障碍是分散注册易重号：此处集中一表，按码排序让人眼即见空洞，
// 测试再验排序+唯一+全覆盖三重门。取舍是启动即常驻内存，换查询零解析。
// 不变量：有序、唯一、与常量全配对；desc 为英文短语，供表与文档复用。
// 排查现场：若 Codes() 顺序飘，先看本表是否手误乱序，次看测试的排序断言。
var codeTable = []codeDef{
	{LexUnterminatedLongComment, "LexUnterminatedLongComment", CatLexer, "unterminated long comment"},
	{LexInvalidCharacter, "LexInvalidCharacter", CatLexer, "invalid character"},
	{LexMalformedHexNumber, "LexMalformedHexNumber", CatLexer, "malformed hexadecimal number"},
	{LexUnterminatedString, "LexUnterminatedString", CatLexer, "unterminated string literal"},
	{LexUnterminatedLongString, "LexUnterminatedLongString", CatLexer, "unterminated long string"},

	{ParseExpectedToken, "ParseExpectedToken", CatParser, "expected a specific token"},
	{ParseExpectedGt, "ParseExpectedGt", CatParser, "expected '>' while closing type arguments"},
	{ParseExpectedLocalName, "ParseExpectedLocalName", CatParser, "expected a local variable name"},
	{ParseUnknownAttribute, "ParseUnknownAttribute", CatParser, "unknown local attribute (expected <const> or <close>)"},
	{ParseInvalidExport, "ParseInvalidExport", CatParser, "invalid or unsupported declaration after 'export'"},
	{ParseInvalidDeclare, "ParseInvalidDeclare", CatParser, "invalid or unsupported declaration after 'declare'"},
	{ParseInvalidAssignTarget, "ParseInvalidAssignTarget", CatParser, "invalid assignment target"},
	{ParseExpectedAssign, "ParseExpectedAssign", CatParser, "expected '=' in assignment"},
	{ParseExpectedCall, "ParseExpectedCall", CatParser, "expected an assignment or function call"},
	{ParseExpectedIdentifier, "ParseExpectedIdentifier", CatParser, "expected an identifier"},
	{ParseExpectedOperand, "ParseExpectedOperand", CatParser, "expected an expression after a binary operator"},
	{ParseExpectedMethodArgs, "ParseExpectedMethodArgs", CatParser, "expected arguments after ':method'"},
	{ParseExpectedExpression, "ParseExpectedExpression", CatParser, "expected an expression"},
	{ParseExpectedType, "ParseExpectedType", CatParser, "expected a type"},
	{ParseNestingTooDeep, "ParseNestingTooDeep", CatParser, "nesting exceeds the parser depth limit"},
	{ParseUnexpectedSyntax, "ParseUnexpectedSyntax", CatParser, "unexpected syntax in import/defer/spawn/select/async"},

	{ParseDuplicateVisibility, "ParseDuplicateVisibility", CatParser, "duplicate visibility modifier"},
	{ParseDuplicateModifier, "ParseDuplicateModifier", CatParser, "duplicate member modifier"},
	{ParseInvalidCtorSignature, "ParseInvalidCtorSignature", CatParser, "constructor must not declare a return type or be async"},
	{ParseMethodBodyMissing, "ParseMethodBodyMissing", CatParser, "method must have a body ending with 'end'"},
	{ParseCtorNotMethod, "ParseCtorNotMethod", CatParser, "'constructor' must be declared as a method"},
	{ParseDeclareFieldInit, "ParseDeclareFieldInit", CatParser, "declare-class field must not have an initializer"},
	{ParseFieldNeedsTypeOrInit, "ParseFieldNeedsTypeOrInit", CatParser, "field needs a type annotation or an initializer"},
	{ParseOnlyMethodsAbstract, "ParseOnlyMethodsAbstract", CatParser, "only methods can be 'abstract'"},
	{ParseMacroParamReserved, "ParseMacroParamReserved", CatParser, "macro parameter cannot be named 'self' or 'super'"},
	{ParseMacroParamDuplicate, "ParseMacroParamDuplicate", CatParser, "duplicate macro parameter"},
	{ParseMacroBodyReturn, "ParseMacroBodyReturn", CatParser, "statement macro body cannot return a value"},
	{ParseMacroVarargLast, "ParseMacroVarargLast", CatParser, "'...' must be the last macro parameter"},

	{CheckerUnsupportedStmt, "CheckerUnsupportedStmt", CatChecker, "internal: unsupported statement node"},
	{DuplicateType, "DuplicateType", CatChecker, "duplicate type declaration"},
	{DuplicateLabel, "DuplicateLabel", CatChecker, "duplicate label"},
	{TypeNotAssignable, "TypeNotAssignable", CatChecker, "type is not assignable to the expected type"},
	{RedefinitionOfLocal, "RedefinitionOfLocal", CatChecker, "redefinition of a local in the same scope"},
	{CloseVarUninitialized, "CloseVarUninitialized", CatChecker, "to-be-closed variable must be initialized"},
	{UndefinedTableInFuncDef, "UndefinedTableInFuncDef", CatChecker, "undefined table in a function definition name"},
	{UndefinedVariable, "UndefinedVariable", CatChecker, "undefined variable (assignment/select contexts)"},
	{AssignToConst, "AssignToConst", CatChecker, "cannot assign to a const variable"},
	{AssignTargetTypeError, "AssignTargetTypeError", CatChecker, "assignment target has no such field or is not indexable"},
	{AssignTargetInvalid, "AssignTargetInvalid", CatChecker, "invalid assignment target"},
	{AssignToReadonly, "AssignToReadonly", CatChecker, "cannot assign to a readonly field"},
	{NumericForBoundNotNum, "NumericForBoundNotNum", CatChecker, "numeric for bounds must be numbers"},
	{VoidFunctionReturns, "VoidFunctionReturns", CatChecker, "function returns no values but values were returned"},
	{ReturnTypeMismatch, "ReturnTypeMismatch", CatChecker, "return value is not assignable to the declared return type"},
	{ImplicitAnyParam, "ImplicitAnyParam", CatChecker, "parameter implicitly has 'any' type (noImplicitAny)"},
	{UndefinedLabel, "UndefinedLabel", CatChecker, "undefined label"},

	{UnsupportedTypeNode, "UnsupportedTypeNode", CatChecker, "internal: unsupported type node"},
	{QualifiedTypeUnsupported, "QualifiedTypeUnsupported", CatChecker, "qualified type names are not supported"},
	{BuiltinTypeArgCount, "BuiltinTypeArgCount", CatChecker, "wrong number of type arguments for a builtin generic (map/table/Array/chan/...)"},
	{UndefinedType, "UndefinedType", CatChecker, "undefined type"},
	{TypeofUndefinedVariable, "TypeofUndefinedVariable", CatChecker, "typeof of an undefined variable"},
	{KeyofUnsupported, "KeyofUnsupported", CatChecker, "keyof is not supported for this type"},
	{TypeRecursionLimit, "TypeRecursionLimit", CatChecker, "cyclic type alias/enum or over-deep generic instantiation"},
	{TypeArgCount, "TypeArgCount", CatChecker, "wrong number of type arguments for a named type"},
	{TypeArgConstraint, "TypeArgConstraint", CatChecker, "type argument does not satisfy its constraint"},
	{InterfaceExtendsNonObject, "InterfaceExtendsNonObject", CatChecker, "interface can only extend object types"},
	{DuplicateField, "DuplicateField", CatChecker, "duplicate field"},
	{EnumMemberNeedsValue, "EnumMemberNeedsValue", CatChecker, "enum member needs an explicit value in a mixed string enum"},
	{EnumMemberNotLiteral, "EnumMemberNotLiteral", CatChecker, "enum member value must be a literal"},
	{DuplicateEnumValue, "DuplicateEnumValue", CatChecker, "duplicate enum value"},
	{DuplicateEnumMember, "DuplicateEnumMember", CatChecker, "duplicate enum member"},
	{CyclicInheritance, "CyclicInheritance", CatChecker, "class extends itself or forms an inheritance cycle"},
	{ExtendsNotClass, "ExtendsNotClass", CatChecker, "extends must name a class"},
	{CyclicInheritanceRef, "CyclicInheritanceRef", CatChecker, "cyclic inheritance while resolving an extends reference"},

	{InvalidExpression, "InvalidExpression", CatChecker, "unsupported expression or misplaced '...'"},
	{UndefinedVariableExpr, "UndefinedVariableExpr", CatChecker, "undefined variable or misplaced 'self' in an expression"},
	{CannotIndex, "CannotIndex", CatChecker, "type cannot be indexed"},
	{NoField, "NoField", CatChecker, "type has no such field"},
	{SuspiciousConversion, "SuspiciousConversion", CatChecker, "conversion may be a mistake"},
	{NonNullOnNil, "NonNullOnNil", CatChecker, "non-null assertion on 'nil'"},
	{ArithmeticOnNonNumber, "ArithmeticOnNonNumber", CatChecker, "arithmetic on non-numbers"},
	{BitwiseRequiresInteger, "BitwiseRequiresInteger", CatChecker, "bitwise operation requires integers"},
	{ConcatRequiresString, "ConcatRequiresString", CatChecker, "concatenation requires strings or numbers"},
	{IncomparableTypes, "IncomparableTypes", CatChecker, "comparison of incompatible types"},
	{UnsupportedOperator, "UnsupportedOperator", CatChecker, "unsupported operator"},
	{LengthOnNonLengthable, "LengthOnNonLengthable", CatChecker, "length operator on a non-lengthable value"},
	{UnaryMinusOnNonNumber, "UnaryMinusOnNonNumber", CatChecker, "unary minus on a non-number"},
	{BitwiseNotOnNonInteger, "BitwiseNotOnNonInteger", CatChecker, "bitwise not on a non-integer"},
	{NoMethod, "NoMethod", CatChecker, "type has no such method"},
	{NotCallable, "NotCallable", CatChecker, "type is not callable"},
	{ArgCountMismatch, "ArgCountMismatch", CatChecker, "wrong number of arguments"},
	{ArgTypeMismatch, "ArgTypeMismatch", CatChecker, "argument type is not assignable to the parameter"},
	{GenericArgsInvalid, "GenericArgsInvalid", CatChecker, "invalid explicit type arguments"},
	{BareVarargInTaskCall, "BareVarargInTaskCall", CatChecker, "bare '...' in a deferred or spawned call"},

	{SuperOutsideDerivedMethod, "SuperOutsideDerivedMethod", CatChecker, "'super' outside a derived-class method"},
	{SuperMisuse, "SuperMisuse", CatChecker, "'super' in a static method or 'super(...)' outside a constructor"},
	{StaticViaInstance, "StaticViaInstance", CatChecker, "static member called through an instance"},
	{AccessViolation, "AccessViolation", CatChecker, "private/protected member accessed outside its allowed scope"},
	{DuplicateClassMember, "DuplicateClassMember", CatChecker, "duplicate class member"},
	{DuplicateConstructor, "DuplicateConstructor", CatChecker, "duplicate constructor"},
	{InvalidMemberName, "InvalidMemberName", CatChecker, "member name reserved by the class runtime"},
	{ModifierNotAllowed, "ModifierNotAllowed", CatChecker, "modifier not allowed on this member kind"},
	{AbstractMemberInvalid, "AbstractMemberInvalid", CatChecker, "abstract member must be non-private and in an abstract class"},
	{ExtendsPrivateCtor, "ExtendsPrivateCtor", CatChecker, "cannot extend a class with a private constructor"},
	{InstantiateAbstract, "InstantiateAbstract", CatChecker, "cannot instantiate an abstract class"},
	{CtorAccessViolation, "CtorAccessViolation", CatChecker, "constructor is private/protected"},
	{FieldNotInitialized, "FieldNotInitialized", CatChecker, "required field is not initialized"},
	{ReservedParamName, "ReservedParamName", CatChecker, "parameter cannot be named 'self' or 'super' in a method"},
	{DerivedCtorMustCallSuper, "DerivedCtorMustCallSuper", CatChecker, "derived constructor must call 'super(...)'"},
	{SelfBeforeSuper, "SelfBeforeSuper", CatChecker, "cannot use 'self' before 'super(...)'"},
	{OverrideMismatch, "OverrideMismatch", CatChecker, "override is incompatible with the base declaration"},
	{UnimplementedAbstract, "UnimplementedAbstract", CatChecker, "class does not implement an abstract member"},
	{ImplementsNotSatisfied, "ImplementsNotSatisfied", CatChecker, "class does not implement the interface"},

	{InferOutsideExtends, "InferOutsideExtends", CatChecker, "'infer' outside a conditional type's extends clause"},
	{CannotMapOverType, "CannotMapOverType", CatChecker, "cannot map over this type"},
	{IndexedAccessInvalid, "IndexedAccessInvalid", CatChecker, "invalid indexed access"},

	{UnexpandedMacro, "UnexpandedMacro", CatExpander, "internal: macro reached the checker unexpanded"},
	{DuplicateMacro, "DuplicateMacro", CatExpander, "duplicate macro definition"},
	{MacroArityMismatch, "MacroArityMismatch", CatExpander, "macro argument count mismatch"},
	{RecursiveMacro, "RecursiveMacro", CatExpander, "recursive or too-deep macro expansion"},
	{StmtMacroInExprPosition, "StmtMacroInExprPosition", CatExpander, "statement macro used as an expression"},
	{MacroExpandsToInvalidStmt, "MacroExpandsToInvalidStmt", CatExpander, "macro expanded to an invalid statement"},
	{MacroVarargOutsideCallArgs, "MacroVarargOutsideCallArgs", CatExpander, "macro '...' outside call arguments"},

	{AwaitOutsideAsync, "AwaitOutsideAsync", CatConcurrency, "'await' outside an async task"},
	{SelectOutsideAsync, "SelectOutsideAsync", CatConcurrency, "'select' outside an async task"},
	{MakeChanInvalid, "MakeChanInvalid", CatConcurrency, "invalid makeChan call (missing type argument or bad capacity)"},
	{SelectGuardInvalid, "SelectGuardInvalid", CatConcurrency, "invalid select guard or channel"},
	{AsyncCallOutsideAsync, "AsyncCallOutsideAsync", CatConcurrency, "async callee invoked outside an async task"},

	{CannotReadFile, "CannotReadFile", CatProject, "cannot read a source file"},
}

// codeIndex 把每个注册错误码映射到其行。
//
// 设计者视角：它是花名册的“索引卡”——包 init 时一次建成 map，让 Known()/Category()/
// Description() 从 O(n) 扫表降为 O(1) 命中。取舍是双份内存（表+索引）换热点零遍历。
// 不变量：与 codeTable 严格同源；只读，永不写。排查现场：若新码 Known() 为 false，
// 先看 codeTable 是否漏行，次看本索引是否在表之后初始化（本文件用 func() 即时构建）。
var codeIndex = func() map[Code]codeDef {
	m := make(map[Code]codeDef, len(codeTable))
	for _, d := range codeTable {
		m[d.code] = d
	}
	return m
}()

// String 返回 Code 的线上传输拼写（TLnnnn）。
//
// 设计者视角：它是契约号的“原样拓印”——string(c) 直出，让诊断落盘/传 LSP 时拼写稳定。
// 取舍是值接收者、无分配分支。不变量：String() 与底层 string 强一致。
// 排查现场：若线上传输多了引号或空格，先看调用方是否用了 %q/%s 之外的动词。
func (c Code) String() string { return string(c) }

// Known 报告 Code 是否在注册表中。
//
// 设计者视角：它是“查花名册”——好比收文先验章，真码才放行按码断言，野码早暴露。
// 障碍是外部输入（LSP/基线文件）可带任意串：先 Known 再用，避免把拼写错误当新码吞下。
// 不变量：纯只读，查 codeIndex 一次命中即返。排查现场：若 Lookup 明明有码却报未知，
// 先看码大小写/空格（TL 大写、六位），次看 codeTable 是否漏行。
func (c Code) Known() bool {
	_, ok := codeIndex[c]
	return ok
}

// Valid 报告 Code 是否符合 TLnnnn 形状（无论是否注册）。
//
// 设计者视角：它是“验章形”——不看花名册，只看章的形制（6 位、TL 开头、后四位数字），
// 把“形错”（拼写坏）与“无此号”（未注册）分两层报。障碍是 Valid 为真但 Known 为假的
// 未来码：Category() 对这类码仍按前缀归车间，而非一棍子打 unknown。取舍是字节级手验，
// 零正则零分配。不变量：Valid 为假则 Known 必假。排查现场：若 "TL12" 被判有效，
// 先数长度（必须 6），次看前后空格是否未剪。
func (c Code) Valid() bool {
	s := string(c)
	if len(s) != 6 || s[0] != 'T' || s[1] != 'L' {
		return false
	}
	for i := 2; i < 6; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Category 返回发出该错误码的阶段。
//
// 设计者视角：它是“看章定车间”——注册码读表的 cat 栏，未注册但形对的按前缀推导
// （categoryOf），形都不对才归 unknown。障碍是 TL37/TL38 藏在 TL3 大段里：
// 若先判 TL3 会把宏/并发误归 checker，故推导时 TL37/TL38 优先。取舍是注册优先、
// 推导兜底，让未来码未注册仍有合理车间。不变量：已知码的车间恒等于表栏。
// 排查现场：若宏错误被标 checker，先看表栏 cat 是否手误 Expander，次看推导分支顺序。
func (c Code) Category() Category {
	if d, ok := codeIndex[c]; ok {
		return d.cat
	}
	if !c.Valid() {
		return CatUnknown
	}
	return categoryOf(string(c))
}

// Description 返回注册表中的简短描述（未知返回空）。
//
// 设计者视角：它是花名册上的“备注栏”——MarkdownTable 与 hover 提示直接抄它，
// 让文档与代码同源。取舍是未知返回空而非报错，让调用方自行决定兜底措辞。
// 不变量：已知码永不返回空。排查现场：若表格某行描述空白，先看 codeTable 该行
// desc 是否漏填，次看码是否拼错导致走了未知分支。
func (c Code) Description() string {
	if d, ok := codeIndex[c]; ok {
		return d.desc
	}
	return ""
}

// Name 返回注册表中对应的常量标识符（未知返回空）。
//
// 设计者视角：它是编号到“人名”的反查——`TL3801` 反查得 `AwaitOutsideAsync`，
// 供文档与测试把码与常量互相印证。取舍是运行时查表而非编译期，保证表是唯一真相。
// 不变量：已知码的 Name 恒与本文件 const 标识符一致（测试解析源码强制配对）。
// 排查现场：若 Name 为空但码看着眼熟，先看大小写（TL 大写），次看表行 name 是否拼错。
func (c Code) Name() string {
	if d, ok := codeIndex[c]; ok {
		return d.name
	}
	return ""
}

// categoryOf 从数值前缀推导类别。
//
// 设计者视角：它是“看号段定车间”的备用尺——注册表未覆盖的未来码仍能按 TL1/2/37/38/3/4
// 归位。障碍是号段嵌套：TL37（宏）与 TL38（并发）是 TL3 的子段，必须先判子段再判大段，
// 否则宏/并发全被吞进 checker。取舍是 HasPrefix 链而非数值运算，让段界一目了然。
// 不变量：输入非 TL 开头一律 unknown。排查现场：若新段（如 TL39）全进 unknown，
// 先在此加分支，次给 codeTable 补 cat。
func categoryOf(s string) Category {
	switch {
	case strings.HasPrefix(s, "TL1"):
		return CatLexer
	case strings.HasPrefix(s, "TL2"):
		return CatParser
	case strings.HasPrefix(s, "TL37"):
		return CatExpander
	case strings.HasPrefix(s, "TL38"):
		return CatConcurrency
	case strings.HasPrefix(s, "TL3"):
		return CatChecker
	case strings.HasPrefix(s, "TL4"):
		return CatProject
	}
	return CatUnknown
}

// Lookup 解析线上传输拼写并返回注册错误码。
//
// 设计者视角：它是外部码串的“验章入口”——基线文件/LSP 传来的 "TL3014" 在此验真伪，
// 真即返码+true，假即原样返+false 让调用方报“未知码”而非误用。取舍是不剪空格不改大小写，
// 严格匹配，倒逼上游传干净串。不变量：ok=true 当且仅当 c.Known()。
// 排查现场：若合法码 Lookup 失败，先看首尾空格/换行是否未剪，次看是否为全角字符。
func Lookup(s string) (Code, bool) {
	c := Code(s)
	if !c.Known() {
		return c, false
	}
	return c, true
}

// Codes 返回所有注册错误码（升序）。
//
// 设计者视角：它是花名册的“点名册”——MarkdownTable、补全、覆盖率检查都按此遍历，
// 升序让 diff 稳定可读。障碍是 codeTable 的书写序可能手抖：此处现排序而非信源头，
// 让输出与书写序解耦。取舍是每次分配新切片（不暴露底层），换调用方随便改不污染正本。
// 不变量：长度恒等于 codeTable，严格升序。排查现场：若文档表顺序飘，先看是否绕过本函数
// 直读 codeTable，次看 sort 是否被改动。
func Codes() []Code {
	out := make([]Code, len(codeTable))
	for i, d := range codeTable {
		out[i] = d.code
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// MarkdownTable 把完整注册表渲染为 Markdown 表格
// （被 docs/11-diagnostics.md 使用，并由测试保持同步）。
//
// 设计者视角：它是花名册的“印刷版”——表头 Code/Constant/Category/Description 四栏，
// 按 Codes() 升序逐行抄 codeIndex，让线上文档与代码同源，改码不改文档即红（同步测试）。
// 障碍是 desc 含 `|` 会破表：此处约定 desc 永不含竖线（英文短语），换渲染零转义。
// 取舍是每次现拼字符串，换文档生成永远新鲜。不变量：行数恒等于注册码数，首两行为表头。
// 排查现场：若 docs 里的表与本函数输出对不上，先跑同步测试的 -update，再看是否手改了
// md 而没回写本表（正本永远是 codeTable）。
//
//go:generate go test -run TestDiagnosticsDocInSync -update .
//go:generate go test -run TestDiagnosticsDocInSync -update .
func MarkdownTable() string {
	var sb strings.Builder
	sb.WriteString("| Code | Constant | Category | Description |\n")
	sb.WriteString("|---|---|---|---|\n")
	for _, c := range Codes() {
		d := codeIndex[c]
		fmt.Fprintf(&sb, "| `%s` | `%s` | %s | %s |\n", c, d.name, d.cat, d.desc)
	}
	return sb.String()
}
