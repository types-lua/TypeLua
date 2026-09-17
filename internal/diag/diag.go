package diag

import (
	"fmt"

	"github.com/typelua/typelua/internal/token"
)

// Severity of a diagnostic.
type Severity int

const (
	Error Severity = iota
	Warning
)

func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// Diagnostic is a single compiler message with a stable code.
type Diagnostic struct {
	Code     string
	Message  string
	Span     token.Span
	Severity Severity
}

func (d Diagnostic) Error() string { return d.String() }

func (d Diagnostic) String() string {
	loc := ""
	if d.Span.File != "" {
		loc = fmt.Sprintf("%s:%s: ", d.Span.File, d.Span.Start)
	}
	return fmt.Sprintf("%s%s[%s]: %s", loc, d.Severity, d.Code, d.Message)
}

// Helpers to construct diagnostics. Codes:
// TL1xxx lexer, TL2xxx parser, TL3xxx checker, TL4xxx config/emit.

func New(code string, span token.Span, sev Severity, format string, args ...any) Diagnostic {
	return Diagnostic{Code: code, Span: span, Severity: sev, Message: fmt.Sprintf(format, args...)}
}

func Errorf(code string, span token.Span, format string, args ...any) Diagnostic {
	return New(code, span, Error, format, args...)
}
