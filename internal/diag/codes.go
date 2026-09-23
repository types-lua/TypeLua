package diag

import (
	"fmt"
	"sort"
	"strings"
)

// Code is a stable diagnostic identifier (`TLnnnn`). Every diagnostic the
// compiler can emit is declared here exactly once: a typed constant plus
// a table entry with its category and a short description. Call sites use
// the constant (`diag.AwaitOutsideAsync`), never a raw string, so a typo
// is a compile error instead of a silently wrong code.
//
// Numeric ranges (kept stable for tooling and SPEC references):
//
//	TL1xxx  lexer
//	TL2xxx  parser (TL203x class members, TL2038-TL2041 macro params)
//	TL3xxx  checker (TL30xx statements/scopes, TL31xx type resolution,
//	        TL32xx expressions/calls, TL33xx classes, TL36xx advanced
//	        types, TL37xx macros, TL38xx concurrency)
//	TL4xxx  project / I-O
type Code string

// Category groups codes by the stage that emits them.
type Category int

const (
	CatLexer Category = iota
	CatParser
	CatChecker
	CatExpander
	CatConcurrency
	CatProject
	// CatUnknown is reported for codes outside the registry.
	CatUnknown
)

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

// ---- Lexer (TL1xxx) ----

const (
	LexUnterminatedLongComment Code = "TL1001"
	LexInvalidCharacter        Code = "TL1002"
	LexMalformedHexNumber      Code = "TL1003"
	LexUnterminatedString      Code = "TL1004"
	LexUnterminatedLongString  Code = "TL1005"
)

// ---- Parser (TL2xxx) ----

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

// ---- Checker: statements & scopes (TL30xx) ----

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

// ---- Checker: type resolution & declarations (TL31xx) ----

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

// ---- Checker: expressions & calls (TL32xx) ----

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

// ---- Checker: classes (TL33xx) ----

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

// ---- Checker: advanced types (TL36xx) ----

const (
	InferOutsideExtends  Code = "TL3601"
	CannotMapOverType    Code = "TL3602"
	IndexedAccessInvalid Code = "TL3603"
)

// ---- Expander: macros (TL37xx) ----

const (
	UnexpandedMacro            Code = "TL3700"
	DuplicateMacro             Code = "TL3701"
	MacroArityMismatch         Code = "TL3702"
	RecursiveMacro             Code = "TL3703"
	StmtMacroInExprPosition    Code = "TL3704"
	MacroExpandsToInvalidStmt  Code = "TL3705"
	MacroVarargOutsideCallArgs Code = "TL3706"
)

// ---- Checker: concurrency (TL38xx) ----

const (
	AwaitOutsideAsync     Code = "TL3801"
	SelectOutsideAsync    Code = "TL3802"
	MakeChanInvalid       Code = "TL3803"
	SelectGuardInvalid    Code = "TL3804"
	AsyncCallOutsideAsync Code = "TL3805"
)

// ---- Project / I-O (TL4xxx) ----

const (
	CannotReadFile Code = "TL4001"
)

// codeDef is one registry row. Name must match the constant declared
// above (a test enforces the pairing by parsing this file).
type codeDef struct {
	code Code
	name string
	cat  Category
	desc string
}

// codeTable is the single source of truth for code metadata. Keep it
// sorted by code; a test verifies sorting, uniqueness, and that the
// constants above cover it exactly.
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

// codeIndex maps every registered code to its row.
var codeIndex = func() map[Code]codeDef {
	m := make(map[Code]codeDef, len(codeTable))
	for _, d := range codeTable {
		m[d.code] = d
	}
	return m
}()

// String returns the wire spelling (`TLnnnn`).
func (c Code) String() string { return string(c) }

// Known reports whether the code is in the registry.
func (c Code) Known() bool {
	_, ok := codeIndex[c]
	return ok
}

// Valid reports whether c has the `TLnnnn` shape (known or not).
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

// Category reports the stage that emits the code. Unknown but valid
// codes fall back to the numeric prefix; malformed codes report
// CatUnknown.
func (c Code) Category() Category {
	if d, ok := codeIndex[c]; ok {
		return d.cat
	}
	if !c.Valid() {
		return CatUnknown
	}
	return categoryOf(string(c))
}

// Description returns the registry description, or "" when unknown.
func (c Code) Description() string {
	if d, ok := codeIndex[c]; ok {
		return d.desc
	}
	return ""
}

// Name returns the constant identifier for a registered code, or "".
func (c Code) Name() string {
	if d, ok := codeIndex[c]; ok {
		return d.name
	}
	return ""
}

// categoryOf derives the category from the numeric prefix.
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

// Lookup parses a wire spelling and returns the registered code.
func Lookup(s string) (Code, bool) {
	c := Code(s)
	if !c.Known() {
		return c, false
	}
	return c, true
}

// Codes returns every registered code in ascending order.
func Codes() []Code {
	out := make([]Code, len(codeTable))
	for i, d := range codeTable {
		out[i] = d.code
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// MarkdownTable renders the full registry as a Markdown table (used by
// docs/11-diagnostics.md and kept in sync by a test).
//
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
