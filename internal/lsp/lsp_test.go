package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

// harness drives a Server over in-memory pipes (race-safe: writes block
// until read, output collected under mutex).
type harness struct {
	pw   *io.PipeWriter
	mu   sync.Mutex
	out  bytes.Buffer
	srv  *Server
	done chan int
}

type lockedWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func newHarness(version string) *harness {
	h := &harness{}
	pr, pw := io.Pipe()
	h.pw = pw
	h.srv = New(pr, version)
	h.srv.out = &lockedWriter{mu: &h.mu, buf: &h.out}
	h.done = make(chan int, 1)
	go func() { h.done <- h.srv.loop() }()
	return h
}

func (h *harness) send(method string, id any, params any) {
	praw, _ := json.Marshal(params)
	msg := map[string]any{"jsonrpc": "2.0", "method": method, "params": json.RawMessage(praw)}
	if id != nil {
		msg["id"] = id
	}
	raw, _ := json.Marshal(msg)
	fmt.Fprintf(h.pw, "Content-Length: %d\r\n\r\n%s", len(raw), raw)
}

type rawMsg struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *respError      `json:"error"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (h *harness) readAll() []rawMsg {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []rawMsg
	rest := h.out.Bytes()
	for len(rest) > 0 {
		head, body, found := bytes.Cut(rest, []byte("\r\n\r\n"))
		if !found {
			break
		}
		n := 0
		for _, line := range bytes.Split(head, []byte("\r\n")) {
			k, v, ok := bytes.Cut(line, []byte(":"))
			if !ok {
				continue
			}
			if strings.EqualFold(string(k), "Content-Length") {
				fmt.Sscanf(string(v), "%d", &n)
			}
		}
		if len(body) < n {
			break
		}
		var m rawMsg
		if err := json.Unmarshal(body[:n], &m); err != nil {
			break
		}
		out = append(out, m)
		rest = body[n:]
	}
	return out
}

const sampleURI = "file:///test/main.tl"
const sampleSrc = `local x: number = 1
local function add(a: number, b: number): number
  return a + b
end
local y = add(x, 2)
local bad: string = 1
print(y)
`

func openSample(h *harness) {
	h.send("initialize", 1, map[string]any{"capabilities": map[string]any{}})
	h.send("initialized", nil, map[string]any{})
	h.send("textDocument/didOpen", nil, map[string]any{"textDocument": map[string]any{
		"uri": sampleURI, "version": 1, "text": sampleSrc,
	}})
}

func findMsg(msgs []rawMsg, id string) *rawMsg {
	for i := range msgs {
		if string(msgs[i].ID) == id {
			return &msgs[i]
		}
	}
	return nil
}

func findNotify(msgs []rawMsg, method string) *rawMsg {
	for i := range msgs {
		if msgs[i].Method == method && len(msgs[i].ID) == 0 {
			return &msgs[i]
		}
	}
	return nil
}

func TestInitializeAndDiagnostics(t *testing.T) {
	h := newHarness("test")
	openSample(h)
	h.send("shutdown", 2, nil)
	h.send("exit", nil, nil)
	if code := <-h.done; code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	msgs := h.readAll()
	init := findMsg(msgs, "1")
	if init == nil {
		t.Fatalf("no initialize response: %+v", msgs)
	}
	var caps struct {
		Capabilities struct {
			HoverProvider      bool `json:"hoverProvider"`
			DefinitionProvider bool `json:"definitionProvider"`
			ReferencesProvider bool `json:"referencesProvider"`
			RenameProvider     bool `json:"renameProvider"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(init.Result, &caps); err != nil {
		t.Fatal(err)
	}
	if !caps.Capabilities.HoverProvider || !caps.Capabilities.DefinitionProvider {
		t.Fatalf("missing capabilities: %+v", caps)
	}
	pub := findNotify(msgs, "textDocument/publishDiagnostics")
	if pub == nil {
		t.Fatalf("no diagnostics published")
	}
	var dp struct {
		Diagnostics []struct {
			Code     string `json:"code"`
			Severity int    `json:"severity"`
			Message  string `json:"message"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(pub.Params, &dp); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range dp.Diagnostics {
		if d.Code == "TL3010" && d.Severity == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected TL3010 diagnostic, got %+v", dp.Diagnostics)
	}
}

func TestHoverDefinitionReferencesRename(t *testing.T) {
	h := newHarness("test")
	openSample(h)
	// `add` use at line 4 (0-based), col 10-13: "local y = add(x, 2)".
	pos := map[string]any{"line": 4, "character": 11}
	doc := map[string]any{"uri": sampleURI}
	h.send("textDocument/hover", 10, map[string]any{"textDocument": doc, "position": pos})
	h.send("textDocument/definition", 11, map[string]any{"textDocument": doc, "position": pos})
	h.send("textDocument/references", 12, map[string]any{
		"textDocument": doc, "position": pos,
		"context": map[string]any{"includeDeclaration": true},
	})
	h.send("textDocument/rename", 13, map[string]any{
		"textDocument": doc, "position": pos, "newName": "plus",
	})
	h.send("shutdown", 14, nil)
	h.send("exit", nil, nil)
	<-h.done
	msgs := h.readAll()

	hover := findMsg(msgs, "10")
	if hover == nil {
		t.Fatalf("no hover response")
	}
	var hv struct {
		Contents struct {
			Value string `json:"value"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(hover.Result, &hv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hv.Contents.Value, "add") || !strings.Contains(hv.Contents.Value, "number") {
		t.Fatalf("bad hover: %q", hv.Contents.Value)
	}

	def := findMsg(msgs, "11")
	if def == nil {
		t.Fatalf("no definition response")
	}
	var locs []struct {
		Range struct {
			Start struct {
				Line int `json:"line"`
			} `json:"start"`
		} `json:"range"`
	}
	if err := json.Unmarshal(def.Result, &locs); err != nil {
		t.Fatal(err)
	}
	if len(locs) != 1 || locs[0].Range.Start.Line != 1 {
		t.Fatalf("definition should jump to line 1: %+v", locs)
	}

	refs := findMsg(msgs, "12")
	if refs == nil {
		t.Fatalf("no references response")
	}
	var rlocs []any
	if err := json.Unmarshal(refs.Result, &rlocs); err != nil {
		t.Fatal(err)
	}
	// def + one use.
	if len(rlocs) != 2 {
		t.Fatalf("expected 2 references, got %d", len(rlocs))
	}

	rn := findMsg(msgs, "13")
	if rn == nil || rn.Error != nil {
		t.Fatalf("rename failed: %+v", rn)
	}
	var edit struct {
		Changes map[string][]struct {
			NewText string `json:"newText"`
			Range   struct {
				Start struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"range"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(rn.Result, &edit); err != nil {
		t.Fatal(err)
	}
	got := edit.Changes[sampleURI]
	if len(got) != 2 {
		t.Fatalf("expected 2 edits, got %d", len(got))
	}
	for _, e := range got {
		if e.NewText != "plus" {
			t.Fatalf("bad edit text %q", e.NewText)
		}
	}
}

func TestRenameRejected(t *testing.T) {
	h := newHarness("test")
	openSample(h)
	// `print` is a builtin: rename must be refused.
	h.send("textDocument/rename", 20, map[string]any{
		"textDocument": map[string]any{"uri": sampleURI},
		"position":     map[string]any{"line": 6, "character": 1},
		"newName":      "p",
	})
	// Bad identifier.
	h.send("textDocument/rename", 21, map[string]any{
		"textDocument": map[string]any{"uri": sampleURI},
		"position":     map[string]any{"line": 4, "character": 11},
		"newName":      "not-a-name",
	})
	h.send("shutdown", 22, nil)
	h.send("exit", nil, nil)
	<-h.done
	msgs := h.readAll()
	for _, id := range []string{"20", "21"} {
		m := findMsg(msgs, id)
		if m == nil || m.Error == nil {
			t.Fatalf("expected error for %s, got %+v", id, m)
		}
	}
}

func TestCompletionAndSymbols(t *testing.T) {
	h := newHarness("test")
	openSample(h)
	h.send("textDocument/completion", 30, map[string]any{
		"textDocument": map[string]any{"uri": sampleURI},
		"position":     map[string]any{"line": 4, "character": 0},
	})
	h.send("textDocument/documentSymbol", 31, map[string]any{
		"textDocument": map[string]any{"uri": sampleURI},
	})
	h.send("shutdown", 32, nil)
	h.send("exit", nil, nil)
	<-h.done
	msgs := h.readAll()

	comp := findMsg(msgs, "30")
	if comp == nil {
		t.Fatalf("no completion response")
	}
	var cl struct {
		Items []struct {
			Label string `json:"label"`
		} `json:"items"`
	}
	if err := json.Unmarshal(comp.Result, &cl); err != nil {
		t.Fatal(err)
	}
	labels := map[string]bool{}
	for _, it := range cl.Items {
		labels[it.Label] = true
	}
	for _, want := range []string{"add", "x", "print", "local"} {
		if !labels[want] {
			t.Fatalf("completion missing %q", want)
		}
	}

	sym := findMsg(msgs, "31")
	if sym == nil {
		t.Fatalf("no symbols response")
	}
	var sl []struct {
		Name string `json:"name"`
		Kind int    `json:"kind"`
	}
	if err := json.Unmarshal(sym.Result, &sl); err != nil {
		t.Fatal(err)
	}
	found := map[string]int{}
	for _, s := range sl {
		found[s.Name] = s.Kind
	}
	if found["add"] != 12 {
		t.Fatalf("symbols: %+v", found)
	}
}

func TestUnknownMethod(t *testing.T) {
	h := newHarness("test")
	h.send("weird/method", 40, map[string]any{})
	h.send("shutdown", 41, nil)
	h.send("exit", nil, nil)
	<-h.done
	msgs := h.readAll()
	m := findMsg(msgs, "40")
	if m == nil || m.Error == nil || m.Error.Code != ErrMethodNotFound {
		t.Fatalf("expected method-not-found, got %+v", m)
	}
}

func TestDidChangeIncremental(t *testing.T) {
	h := newHarness("test")
	h.send("initialize", 1, map[string]any{"capabilities": map[string]any{}})
	h.send("textDocument/didOpen", nil, map[string]any{"textDocument": map[string]any{
		"uri": sampleURI, "version": 1, "text": "local x = 1\n",
	}})
	// Full replace introducing an error.
	h.send("textDocument/didChange", nil, map[string]any{
		"textDocument":   map[string]any{"uri": sampleURI, "version": 2},
		"contentChanges": []any{map[string]any{"text": "local x: number = \"s\"\n"}},
	})
	// Range edit fixing it back: replace `"s"` (line 0, cols 18-21) with `1`.
	h.send("textDocument/didChange", nil, map[string]any{
		"textDocument": map[string]any{"uri": sampleURI, "version": 3},
		"contentChanges": []any{map[string]any{
			"range": map[string]any{
				"start": map[string]any{"line": 0, "character": 18},
				"end":   map[string]any{"line": 0, "character": 21},
			},
			"text": "1",
		}},
	})
	h.send("textDocument/hover", 50, map[string]any{
		"textDocument": map[string]any{"uri": sampleURI},
		"position":     map[string]any{"line": 0, "character": 7},
	})
	h.send("shutdown", 51, nil)
	h.send("exit", nil, nil)
	<-h.done
	msgs := h.readAll()
	// Last diagnostics (after fix) must be clean; hover must see x.
	var lastDiag *rawMsg
	for i := range msgs {
		if msgs[i].Method == "textDocument/publishDiagnostics" {
			lastDiag = &msgs[i]
		}
	}
	if lastDiag == nil {
		t.Fatalf("no diagnostics")
	}
	var dp struct {
		Diagnostics []any `json:"diagnostics"`
	}
	if err := json.Unmarshal(lastDiag.Params, &dp); err != nil {
		t.Fatal(err)
	}
	if len(dp.Diagnostics) != 0 {
		t.Fatalf("expected clean after fix, got %v", dp.Diagnostics)
	}
	hv := findMsg(msgs, "50")
	if hv == nil {
		t.Fatalf("no hover response")
	}
	var hvr struct {
		Contents struct {
			Value string `json:"value"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(hv.Result, &hvr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hvr.Contents.Value, "x") {
		t.Fatalf("bad hover: %q", hvr.Contents.Value)
	}
}
