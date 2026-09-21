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

const memberURI = "file:///test/members.tl"
const memberSrc = `type Alias = { x: number }
interface Shape {
  name: string,
}
enum Color { Red = "red", Blue = "blue" }
class Dog {
  name: string,
  constructor(name: string) self.name = name end,
  greet(): string return self.name end,
}
local a: Alias = { x = 1 }
local s: Shape = { name = "c" }
local sn = s.name
local c: Color = Color.Red
local d = Dog.new("rex")
local n = d.name
local g = d:greet()
`

func openMembers(h *harness) {
	h.send("initialize", 1, map[string]any{"capabilities": map[string]any{}})
	h.send("initialized", nil, map[string]any{})
	h.send("textDocument/didOpen", nil, map[string]any{"textDocument": map[string]any{
		"uri": memberURI, "version": 1, "text": memberSrc,
	}})
}

func TestTypeRename(t *testing.T) {
	h := newHarness("test")
	openMembers(h)
	doc := map[string]any{"uri": memberURI}
	// Rename Alias at its declaration: decl + one annotation use.
	h.send("textDocument/rename", 70, map[string]any{
		"textDocument": doc, "position": map[string]any{"line": 0, "character": 7},
		"newName": "Rec",
	})
	// Rename Dog from a value use: declaration + constructor call.
	h.send("textDocument/rename", 71, map[string]any{
		"textDocument": doc, "position": map[string]any{"line": 14, "character": 11},
		"newName": "Cat",
	})
	// Rename Color: declaration + annotation + value base (not the member).
	h.send("textDocument/rename", 72, map[string]any{
		"textDocument": doc, "position": map[string]any{"line": 4, "character": 6},
		"newName": "Hue",
	})
	h.send("shutdown", 73, nil)
	h.send("exit", nil, nil)
	<-h.done
	msgs := h.readAll()
	checkEdits := func(id string, wantLines []int) {
		t.Helper()
		m := findMsg(msgs, id)
		if m == nil || m.Error != nil {
			t.Fatalf("rename %s failed: %+v", id, m)
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
		if err := json.Unmarshal(m.Result, &edit); err != nil {
			t.Fatal(err)
		}
		got := edit.Changes[memberURI]
		if len(got) != len(wantLines) {
			t.Fatalf("rename %s: want %d edits, got %d (%+v)", id, len(wantLines), len(got), got)
		}
		for i, e := range got {
			if e.Range.Start.Line != wantLines[i] {
				t.Fatalf("rename %s edit %d: want line %d, got %+v", id, i, wantLines[i], e.Range.Start)
			}
		}
	}
	checkEdits("70", []int{0, 10})
	checkEdits("71", []int{5, 14})
	checkEdits("72", []int{4, 13, 13})
}

func TestMemberRenameReferences(t *testing.T) {
	h := newHarness("test")
	openMembers(h)
	doc := map[string]any{"uri": memberURI}
	// `name` field: def (6) + self uses (7, 8) + dot uses (12, 15).
	// (`s.name` belongs to Shape and stays out.)
	h.send("textDocument/references", 80, map[string]any{
		"textDocument": doc, "position": map[string]any{"line": 15, "character": 13},
		"context": map[string]any{"includeDeclaration": true},
	})
	// Rename greet method: def (8) + call (16).
	h.send("textDocument/rename", 81, map[string]any{
		"textDocument": doc, "position": map[string]any{"line": 16, "character": 15},
		"newName": "bark",
	})
	// Rename enum member Red: def (4) + use (13).
	h.send("textDocument/rename", 82, map[string]any{
		"textDocument": doc, "position": map[string]any{"line": 13, "character": 24},
		"newName": "Crimson",
	})
	// Definition of the field use jumps to the class body.
	h.send("textDocument/definition", 83, map[string]any{
		"textDocument": doc, "position": map[string]any{"line": 15, "character": 13},
	})
	// Hover on the method call names the owner.
	h.send("textDocument/hover", 84, map[string]any{
		"textDocument": doc, "position": map[string]any{"line": 16, "character": 15},
	})
	h.send("shutdown", 85, nil)
	h.send("exit", nil, nil)
	<-h.done
	msgs := h.readAll()

	refs := findMsg(msgs, "80")
	if refs == nil {
		t.Fatalf("no references response")
	}
	var rlocs []struct {
		Range struct {
			Start struct {
				Line int `json:"line"`
			} `json:"start"`
		} `json:"range"`
	}
	if err := json.Unmarshal(refs.Result, &rlocs); err != nil {
		t.Fatal(err)
	}
	if len(rlocs) != 4 {
		t.Fatalf("bad member references: %+v", rlocs)
	}
	for i, want := range []int{6, 7, 8, 15} {
		if rlocs[i].Range.Start.Line != want {
			t.Fatalf("bad member references: %+v", rlocs)
		}
	}

	m := findMsg(msgs, "81")
	if m == nil || m.Error != nil {
		t.Fatalf("method rename failed: %+v", m)
	}
	var edit struct {
		Changes map[string][]struct {
			Range struct {
				Start struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"range"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(m.Result, &edit); err != nil {
		t.Fatal(err)
	}
	got := edit.Changes[memberURI]
	if len(got) != 2 || got[0].Range.Start.Line != 8 || got[1].Range.Start.Line != 16 {
		t.Fatalf("bad method rename: %+v", got)
	}

	m82 := findMsg(msgs, "82")
	if m82 == nil || m82.Error != nil {
		t.Fatalf("enum member rename failed: %+v", m82)
	}
	var edit82 struct {
		Changes map[string][]struct {
			Range struct {
				Start struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"range"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(m82.Result, &edit82); err != nil {
		t.Fatal(err)
	}
	got82 := edit82.Changes[memberURI]
	if len(got82) != 2 || got82[0].Range.Start.Line != 4 || got82[1].Range.Start.Line != 13 {
		t.Fatalf("bad enum member rename: %+v", got82)
	}

	def := findMsg(msgs, "83")
	if def == nil {
		t.Fatalf("no definition response")
	}
	var dlocs []struct {
		Range struct {
			Start struct {
				Line int `json:"line"`
			} `json:"start"`
		} `json:"range"`
	}
	if err := json.Unmarshal(def.Result, &dlocs); err != nil {
		t.Fatal(err)
	}
	if len(dlocs) != 1 || dlocs[0].Range.Start.Line != 6 {
		t.Fatalf("member definition should jump to line 6: %+v", dlocs)
	}

	hv := findMsg(msgs, "84")
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
	if !strings.Contains(hvr.Contents.Value, "Dog.greet") {
		t.Fatalf("bad member hover: %q", hvr.Contents.Value)
	}
}

const indexURI = "file:///test/indexkeys.tl"
const indexSrc = `interface Box {
  label: string,
  get(): number,
}
local b: Box = { label = "x", get = function(): number return 1 end }
local l1 = b["label"]
b["label"] = "y"
local g2 = b.get()
`

func TestStringIndexAndImplKeys(t *testing.T) {
	h := newHarness("test")
	h.send("initialize", 1, map[string]any{"capabilities": map[string]any{}})
	h.send("textDocument/didOpen", nil, map[string]any{"textDocument": map[string]any{
		"uri": indexURI, "version": 1, "text": indexSrc,
	}})
	doc := map[string]any{"uri": indexURI}
	// `label`: def (1) + impl key (4) + string-index read (5) + write (6).
	h.send("textDocument/references", 95, map[string]any{
		"textDocument": doc, "position": map[string]any{"line": 5, "character": 16},
		"context": map[string]any{"includeDeclaration": true},
	})
	// Rename `get`: def (2) + impl key (4) + call (7).
	h.send("textDocument/rename", 96, map[string]any{
		"textDocument": doc, "position": map[string]any{"line": 7, "character": 13},
		"newName": "fetch",
	})
	h.send("shutdown", 97, nil)
	h.send("exit", nil, nil)
	<-h.done
	msgs := h.readAll()

	refs := findMsg(msgs, "95")
	if refs == nil {
		t.Fatalf("no references response")
	}
	var rlocs []struct {
		Range struct {
			Start struct {
				Line int `json:"line"`
				Char int `json:"character"`
			} `json:"start"`
		} `json:"range"`
	}
	if err := json.Unmarshal(refs.Result, &rlocs); err != nil {
		t.Fatal(err)
	}
	var gotLines []int
	for _, l := range rlocs {
		gotLines = append(gotLines, l.Range.Start.Line)
	}
	wantLines := []int{1, 4, 5, 6}
	if len(gotLines) != len(wantLines) {
		t.Fatalf("bad string-index references: %+v", gotLines)
	}
	for i := range wantLines {
		if gotLines[i] != wantLines[i] {
			t.Fatalf("bad string-index references: %+v", gotLines)
		}
	}

	m := findMsg(msgs, "96")
	if m == nil || m.Error != nil {
		t.Fatalf("impl-key rename failed: %+v", m)
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
	if err := json.Unmarshal(m.Result, &edit); err != nil {
		t.Fatal(err)
	}
	got := edit.Changes[indexURI]
	if len(got) != 3 {
		t.Fatalf("bad impl-key rename: %+v", got)
	}
	for i, want := range []int{2, 4, 7} {
		if got[i].Range.Start.Line != want || got[i].NewText != "fetch" {
			t.Fatalf("bad impl-key rename: %+v", got)
		}
	}
}

const genericURI = "file:///test/generic.tl"
const genericSrc = `class Box<T> {
  v: T,
  constructor(v: T) self.v = v end,
  get(): T return self.v end,
}
local b = Box.new<string>("hi")
local v = b:get()
local w = b.v
`

func TestGenericMemberRename(t *testing.T) {
	h := newHarness("test")
	h.send("initialize", 1, map[string]any{"capabilities": map[string]any{}})
	h.send("textDocument/didOpen", nil, map[string]any{"textDocument": map[string]any{
		"uri": genericURI, "version": 1, "text": genericSrc,
	}})
	doc := map[string]any{"uri": genericURI}
	// Rename get through a generic instantiation: def (3) + call (6).
	h.send("textDocument/rename", 100, map[string]any{
		"textDocument": doc, "position": map[string]any{"line": 6, "character": 13},
		"newName": "fetch",
	})
	// References of the value field: def (1) + self uses (2, 3) + use (7).
	h.send("textDocument/references", 101, map[string]any{
		"textDocument": doc, "position": map[string]any{"line": 7, "character": 12},
		"context": map[string]any{"includeDeclaration": true},
	})
	h.send("shutdown", 102, nil)
	h.send("exit", nil, nil)
	<-h.done
	msgs := h.readAll()
	m := findMsg(msgs, "100")
	if m == nil || m.Error != nil {
		t.Fatalf("generic method rename failed: %+v", m)
	}
	var edit struct {
		Changes map[string][]struct {
			Range struct {
				Start struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"range"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(m.Result, &edit); err != nil {
		t.Fatal(err)
	}
	got := edit.Changes[genericURI]
	if len(got) != 2 || got[0].Range.Start.Line != 3 || got[1].Range.Start.Line != 6 {
		t.Fatalf("bad generic rename: %+v", got)
	}
	refs := findMsg(msgs, "101")
	if refs == nil {
		t.Fatalf("no references response")
	}
	var rlocs []struct {
		Range struct {
			Start struct {
				Line int `json:"line"`
			} `json:"start"`
		} `json:"range"`
	}
	if err := json.Unmarshal(refs.Result, &rlocs); err != nil {
		t.Fatal(err)
	}
	if len(rlocs) != 4 {
		t.Fatalf("bad generic references: %+v", rlocs)
	}
	for i, want := range []int{1, 2, 3, 7} {
		if rlocs[i].Range.Start.Line != want {
			t.Fatalf("bad generic references: %+v", rlocs)
		}
	}
}

func TestMemberCompletion(t *testing.T) {
	h := newHarness("test")
	openMembers(h)
	doc := map[string]any{"uri": memberURI}
	// After `d.`: instance members, no keywords.
	h.send("textDocument/completion", 110, map[string]any{
		"textDocument": doc, "position": map[string]any{"line": 15, "character": 12},
	})
	// After `d:`: same members.
	h.send("textDocument/completion", 111, map[string]any{
		"textDocument": doc, "position": map[string]any{"line": 16, "character": 12},
	})
	// After `Color.`: enum members.
	h.send("textDocument/completion", 112, map[string]any{
		"textDocument": doc, "position": map[string]any{"line": 13, "character": 23},
	})
	h.send("shutdown", 113, nil)
	h.send("exit", nil, nil)
	<-h.done
	msgs := h.readAll()
	labels := func(id string) []string {
		t.Helper()
		m := findMsg(msgs, id)
		if m == nil {
			t.Fatalf("no completion %s", id)
		}
		var res struct {
			Items []struct {
				Label string `json:"label"`
			} `json:"items"`
		}
		if err := json.Unmarshal(m.Result, &res); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, it := range res.Items {
			out = append(out, it.Label)
		}
		return out
	}
	has := func(ls []string, want ...string) bool {
		set := map[string]bool{}
		for _, l := range ls {
			set[l] = true
		}
		for _, w := range want {
			if !set[w] {
				return false
			}
		}
		return true
	}
	dot := labels("110")
	if !has(dot, "name", "greet") {
		t.Fatalf("dot completion missing members: %v", dot)
	}
	if has(dot, "and", "while") {
		t.Fatalf("dot completion must not list keywords: %v", dot)
	}
	colon := labels("111")
	if !has(colon, "greet") {
		t.Fatalf("colon completion missing method: %v", colon)
	}
	enum := labels("112")
	if !has(enum, "Red", "Blue") {
		t.Fatalf("enum completion missing members: %v", enum)
	}
}

func TestRenameTypeConflict(t *testing.T) {
	h := newHarness("test")
	h.send("initialize", 1, map[string]any{"capabilities": map[string]any{}})
	h.send("textDocument/didOpen", nil, map[string]any{"textDocument": map[string]any{
		"uri": memberURI, "version": 1,
		"text": "type Foo = number\nlocal Foo = 1\nlocal y: Foo = Foo\n",
	}})
	// The value use is shadowed by an unrelated type: refuse half renames.
	h.send("textDocument/rename", 90, map[string]any{
		"textDocument": map[string]any{"uri": memberURI},
		"position":     map[string]any{"line": 2, "character": 16},
		"newName":      "Bar",
	})
	h.send("shutdown", 91, nil)
	h.send("exit", nil, nil)
	<-h.done
	m := findMsg(h.readAll(), "90")
	if m == nil || m.Error == nil {
		t.Fatalf("expected conflict error, got %+v", m)
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

func TestIncrementalSyncAdvertised(t *testing.T) {
	h := newHarness("test")
	h.send("initialize", 1, map[string]any{"capabilities": map[string]any{}})
	h.send("shutdown", 2, nil)
	h.send("exit", nil, nil)
	<-h.done
	m := findMsg(h.readAll(), "1")
	if m == nil {
		t.Fatalf("no initialize response")
	}
	var res struct {
		Capabilities struct {
			TextDocumentSync int `json:"textDocumentSync"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(m.Result, &res); err != nil {
		t.Fatal(err)
	}
	if res.Capabilities.TextDocumentSync != 2 {
		t.Fatalf("want incremental(2), got %d", res.Capabilities.TextDocumentSync)
	}
}

func TestDidChangeBatchAndUnicode(t *testing.T) {
	h := newHarness("test")
	h.send("initialize", 1, map[string]any{"capabilities": map[string]any{}})
	h.send("textDocument/didOpen", nil, map[string]any{"textDocument": map[string]any{
		"uri": sampleURI, "version": 1, "text": "local 名称 = 1\nlocal y = 2\n",
	}})
	// One notification, two ranged edits (applied in order): rename the
	// CJK variable (UTF-16 columns) and join the lines with a multiline edit.
	h.send("textDocument/didChange", nil, map[string]any{
		"textDocument": map[string]any{"uri": sampleURI, "version": 2},
		"contentChanges": []any{
			map[string]any{
				"range": map[string]any{
					"start": map[string]any{"line": 0, "character": 6},
					"end":   map[string]any{"line": 0, "character": 8},
				},
				"text": "name",
			},
			map[string]any{
				"range": map[string]any{
					"start": map[string]any{"line": 0, "character": 15},
					"end":   map[string]any{"line": 1, "character": 0},
				},
				"text": "; ",
			},
		},
	})
	h.send("textDocument/hover", 60, map[string]any{
		"textDocument": map[string]any{"uri": sampleURI},
		"position":     map[string]any{"line": 0, "character": 8},
	})
	h.send("shutdown", 61, nil)
	h.send("exit", nil, nil)
	<-h.done
	msgs := h.readAll()
	hv := findMsg(msgs, "60")
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
	if !strings.Contains(hvr.Contents.Value, "name") {
		t.Fatalf("batch edit misapplied, hover: %q", hvr.Contents.Value)
	}
}
