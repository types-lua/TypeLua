package diag

import (
	"fmt"

	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
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

// Diagnostic is a single compiler message with a stable code (see
// codes.go for the registry of everything the compiler can emit).
type Diagnostic struct {
	Code     Code
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
	// Ultra-deep types collapse (readability; shallow messages identical).
	return fmt.Sprintf("%s%s[%s]: %s", loc, d.Severity, d.Code, types.Abbrev(d.Message))
}

// Helpers to construct diagnostics. Codes come from the registry in
// codes.go (TL1xxx lexer, TL2xxx parser, TL37xx macros, TL38xx
// concurrency, TL3xxx checker, TL4xxx project/emit).

func New(code Code, span token.Span, sev Severity, format string, args ...any) Diagnostic {
	return Diagnostic{Code: code, Span: span, Severity: sev, Message: fmt.Sprintf(format, args...)}
}

func Errorf(code Code, span token.Span, format string, args ...any) Diagnostic {
	return New(code, span, Error, format, args...)
}
