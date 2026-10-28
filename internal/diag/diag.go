package diag

import (
	"fmt"

	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// 诊断的严重度。
type Severity int

const (
	// Error 表示错误级别诊断。
	Error Severity = iota
	// Warning 表示警告级别诊断。
	Warning
)

// String 返回 Severity 的文本形式（error/warning）。
func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// Diagnostic 是一条编译器消息：稳定 Code + 人读 Message + 位置 Span + 严重度。
//
// 物理布局：
//
//	Diagnostic
//	├── Code     Code           // 稳定编号 TLnnnn
//	├── Message  string         // 人读解释
//	├── Span     token.Span     // 文件/行列（半开区间）
//	└── Severity Severity       // Error/Warning
type Diagnostic struct {
	Code     Code
	Message  string
	Span     token.Span
	Severity Severity
}

// Error 实现 error 接口，返回 String()。
func (d Diagnostic) Error() string { return d.String() }

// String 渲染为 "file:line:col severity[code]: message"。
func (d Diagnostic) String() string {
	loc := ""
	if d.Span.File != "" {
		loc = fmt.Sprintf("%s:%s: ", d.Span.File, d.Span.Start)
	}
	// 超深类型折叠（可读性；浅层消息相同）。
	return fmt.Sprintf("%s%s[%s]: %s", loc, d.Severity, d.Code, types.Abbrev(d.Message))
}

// 构造诊断的辅助函数；错误码来自 codes.go 的注册表。

// New 构造一条诊断。
func New(code Code, span token.Span, sev Severity, format string, args ...any) Diagnostic {
	return Diagnostic{Code: code, Span: span, Severity: sev, Message: fmt.Sprintf(format, args...)}
}

// Errorf 构造一条 Error 级别诊断。
func Errorf(code Code, span token.Span, format string, args ...any) Diagnostic {
	return New(code, span, Error, format, args...)
}
