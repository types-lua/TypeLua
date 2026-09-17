package lexer

import (
	"testing"

	"github.com/typelua/typelua/internal/token"
)

func lexKinds(src string) []token.Kind {
	toks, _ := Lex("test.tl", src)
	var kinds []token.Kind
	for _, t := range toks {
		kinds = append(kinds, t.Kind)
	}
	return kinds
}

func TestBasic(t *testing.T) {
	toks, diags := Lex("a.tl", `local x: number = 1 + 2.5 -- comment`)
	if len(diags) != 0 {
		t.Fatalf("diags: %v", diags)
	}
	if len(toks) == 0 || toks[len(toks)-1].Kind != token.EOF {
		t.Fatalf("missing EOF")
	}
}

func TestLongString(t *testing.T) {
	_, diags := Lex("a.tl", "local s = [[hello\nworld]]")
	if len(diags) != 0 {
		t.Fatalf("diags: %v", diags)
	}
	_, diags = Lex("a.tl", "local s = [[unterminated")
	if len(diags) == 0 {
		t.Fatalf("expected unterminated error")
	}
}

func TestNumbers(t *testing.T) {
	for _, s := range []string{"1", "3.14", "0xFF", "1e10", "1.5e-3", "0x1p4", "1_000"} {
		toks, _ := Lex("a.tl", s)
		if toks[0].Kind != token.Number {
			t.Fatalf("%s not number: %v", s, toks[0])
		}
	}
}

func TestOperators(t *testing.T) {
	toks, _ := Lex("a.tl", "a ... b .. c . d // e / f -> g")
	found := map[token.Kind]bool{}
	for _, tk := range toks {
		found[tk.Kind] = true
	}
	for _, k := range []token.Kind{token.DotDotDot, token.DotDot, token.Dot, token.DoubleSlash, token.Slash, token.Arrow} {
		if !found[k] {
			t.Fatalf("missing %v", k)
		}
	}
}

func TestUnterminatedString(t *testing.T) {
	_, diags := Lex("a.tl", `local s = "abc`)
	if len(diags) == 0 {
		t.Fatalf("expected error")
	}
}
