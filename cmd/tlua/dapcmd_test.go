package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// dapTestClient drives runDAP over in-memory pipes and buffers messages so a
// test can wait for responses and events independently.
type dapTestClient struct {
	conn    *dapConn
	mu      sync.Mutex
	seq     int
	msgs    chan map[string]json.RawMessage
	backlog []map[string]json.RawMessage
	done    chan struct{}
	closeIn func()
}

func newDAPTestClient(t *testing.T) *dapTestClient {
	t.Helper()
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	done := make(chan struct{})
	go func() {
		_ = runDAP(c2sR, s2cW)
		_ = s2cW.Close()
		close(done)
	}()
	c := &dapTestClient{
		conn:    newDAPConn(s2cR, c2sW),
		msgs:    make(chan map[string]json.RawMessage, 64),
		done:    done,
		closeIn: func() { _ = c2sW.Close() },
	}
	go func() {
		for {
			m, err := c.conn.read()
			if err != nil {
				close(c.msgs)
				return
			}
			select {
			case c.msgs <- m:
			case <-done:
				return
			}
		}
	}()
	t.Cleanup(func() {
		c.closeIn()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})
	return c
}

func (c *dapTestClient) request(t *testing.T, command string, args any) int {
	t.Helper()
	c.mu.Lock()
	c.seq++
	seq := c.seq
	c.mu.Unlock()
	msg := map[string]any{"seq": seq, "type": "request", "command": command}
	if args != nil {
		msg["arguments"] = args
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(c.conn.w, "Content-Length: %d\r\n\r\n%s", len(raw), raw); err != nil {
		t.Fatal(err)
	}
	return seq
}

func (c *dapTestClient) wait(t *testing.T, what string, pred func(map[string]json.RawMessage) bool, timeout time.Duration) map[string]json.RawMessage {
	t.Helper()
	c.mu.Lock()
	for i, m := range c.backlog {
		if pred(m) {
			c.backlog = append(c.backlog[:i], c.backlog[i+1:]...)
			c.mu.Unlock()
			return m
		}
	}
	c.mu.Unlock()
	deadline := time.After(timeout)
	for {
		select {
		case m, ok := <-c.msgs:
			if !ok {
				t.Fatalf("adapter closed while waiting for %s", what)
			}
			if pred(m) {
				return m
			}
			c.mu.Lock()
			c.backlog = append(c.backlog, m)
			c.mu.Unlock()
		case <-deadline:
			t.Fatalf("timeout waiting for %s; recent messages: %s", what, c.recentSummary())
		}
	}
}

// recentSummary drains buffered messages and formats their kinds so a timeout
// shows whether the adapter stopped again, rejected a command, or went silent.
func (c *dapTestClient) recentSummary() string {
	c.mu.Lock()
	msgs := append([]map[string]json.RawMessage{}, c.backlog...)
	c.mu.Unlock()
	for {
		select {
		case m, ok := <-c.msgs:
			if !ok {
				return summarizeDAP(msgs)
			}
			msgs = append(msgs, m)
		default:
			return summarizeDAP(msgs)
		}
	}
}

func summarizeDAP(msgs []map[string]json.RawMessage) string {
	start := 0
	if len(msgs) > 20 {
		start = len(msgs) - 20
	}
	parts := make([]string, 0, len(msgs)-start)
	for _, m := range msgs[start:] {
		switch cstr(m, "type") {
		case "event":
			name := cstr(m, "event")
			if name == "output" {
				var body struct {
					Output string `json:"output"`
				}
				_ = json.Unmarshal(m["body"], &body)
				if len(body.Output) > 60 {
					body.Output = body.Output[:60] + "..."
				}
				parts = append(parts, "output("+body.Output+")")
			} else {
				parts = append(parts, "event:"+name)
			}
		case "response":
			parts = append(parts, "response:"+cstr(m, "command"))
		default:
			parts = append(parts, cstr(m, "type"))
		}
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, ", ")
}

func cstr(m map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(m[key], &s)
	return s
}

func cbody(m map[string]json.RawMessage, key string) json.RawMessage {
	var body map[string]json.RawMessage
	_ = json.Unmarshal(m["body"], &body)
	return body[key]
}

func cbodyString(m map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(cbody(m, key), &s)
	return s
}

func cint(m map[string]json.RawMessage, key string) int {
	var n int
	_ = json.Unmarshal(m[key], &n)
	return n
}

func eventNamed(name string) func(map[string]json.RawMessage) bool {
	return func(m map[string]json.RawMessage) bool {
		return cstr(m, "type") == "event" && cstr(m, "event") == name
	}
}

func responseFor(seq int) func(map[string]json.RawMessage) bool {
	return func(m map[string]json.RawMessage) bool {
		return cstr(m, "type") == "response" && cint(m, "request_seq") == seq
	}
}

func TestDAPBreakpointStackVariables(t *testing.T) {
	if _, err := exec.LookPath("lua5.4"); err != nil {
		t.Skip("lua5.4 not installed")
	}
	dir := t.TempDir()
	writeProject(t, dir)
	main := filepath.Join(dir, "main.tl")
	writeFile(t, main, `local function add(a: number, b: number): number
  local c: number = a + b
  return c
end

local r: number = add(2, 3)
print("sum", r)
`)
	t.Chdir(dir)

	c := newDAPTestClient(t)
	seq := c.request(t, "initialize", map[string]any{"adapterID": "typelua"})
	c.wait(t, "initialize response", responseFor(seq), 5*time.Second)
	c.wait(t, "initialized event", eventNamed("initialized"), 5*time.Second)

	seq = c.request(t, "launch", map[string]any{"program": main})
	resp := c.wait(t, "launch response", responseFor(seq), 10*time.Second)
	var launchOK bool
	_ = json.Unmarshal(resp["success"], &launchOK)
	if !launchOK {
		t.Fatalf("launch failed: %s", string(resp["message"]))
	}

	seq = c.request(t, "setBreakpoints", map[string]any{
		"source":      map[string]any{"path": main},
		"breakpoints": []map[string]any{{"line": 3}},
	})
	c.wait(t, "setBreakpoints response", responseFor(seq), 5*time.Second)
	seq = c.request(t, "configurationDone", nil)
	c.wait(t, "configurationDone response", responseFor(seq), 5*time.Second)
	stop := c.wait(t, "stopped event", eventNamed("stopped"), 10*time.Second)
	if cbodyString(stop, "reason") != "breakpoint" {
		t.Fatalf("stop reason = %q, want breakpoint", cbodyString(stop, "reason"))
	}

	seq = c.request(t, "stackTrace", map[string]any{"threadId": 1})
	resp = c.wait(t, "stackTrace response", responseFor(seq), 5*time.Second)
	var stack struct {
		StackFrames []struct {
			ID   int `json:"id"`
			Line int `json:"line"`
		} `json:"stackFrames"`
	}
	if err := json.Unmarshal(resp["body"], &stack); err != nil {
		t.Fatal(err)
	}
	if len(stack.StackFrames) == 0 || stack.StackFrames[0].Line != 3 {
		t.Fatalf("top frame = %+v, want line 3", stack.StackFrames)
	}
	top := stack.StackFrames[0].ID

	seq = c.request(t, "scopes", map[string]any{"frameId": top})
	resp = c.wait(t, "scopes response", responseFor(seq), 5*time.Second)
	var scopes struct {
		Scopes []struct {
			VariablesReference int `json:"variablesReference"`
		} `json:"scopes"`
	}
	if err := json.Unmarshal(resp["body"], &scopes); err != nil {
		t.Fatal(err)
	}
	if len(scopes.Scopes) == 0 {
		t.Fatal("no scopes")
	}

	seq = c.request(t, "variables", map[string]any{"variablesReference": scopes.Scopes[0].VariablesReference})
	resp = c.wait(t, "variables response", responseFor(seq), 5*time.Second)
	var vars struct {
		Variables []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(resp["body"], &vars); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range vars.Variables {
		got[v.Name] = v.Value
	}
	if got["c"] != "5" || got["a"] != "2" || got["b"] != "3" {
		t.Fatalf("locals = %+v", got)
	}

	seq = c.request(t, "next", map[string]any{"threadId": 1})
	c.wait(t, "next response", responseFor(seq), 5*time.Second)
	stoppedAgain := c.wait(t, "stopped event (step)", eventNamed("stopped"), 10*time.Second)
	if cbodyString(stoppedAgain, "reason") != "step" {
		t.Fatalf("step reason = %q", cbodyString(stoppedAgain, "reason"))
	}

	seq = c.request(t, "continue", map[string]any{"threadId": 1})
	c.wait(t, "continue response", responseFor(seq), 5*time.Second)
	c.wait(t, "terminated event", eventNamed("terminated"), 10*time.Second)
	seq = c.request(t, "disconnect", nil)
	c.wait(t, "disconnect response", responseFor(seq), 5*time.Second)
}

func TestDAPTestModeReportsResult(t *testing.T) {
	if _, err := exec.LookPath("lua5.4"); err != nil {
		t.Skip("lua5.4 not installed")
	}
	dir := t.TempDir()
	writeProject(t, dir)
	testFile := filepath.Join(dir, "tests", "math_test.tl")
	writeFile(t, testFile, `import T from "tlua_test"

return {
  ["adds"] = function(): void
    local got: number = 1 + 1
    T.eq(got, 2)
  end,
}
`)
	t.Chdir(dir)

	c := newDAPTestClient(t)
	seq := c.request(t, "initialize", map[string]any{"adapterID": "typelua"})
	c.wait(t, "initialize response", responseFor(seq), 5*time.Second)
	c.wait(t, "initialized event", eventNamed("initialized"), 5*time.Second)
	seq = c.request(t, "launch", map[string]any{
		"program":  testFile,
		"test":     testFile,
		"testCase": "adds",
	})
	c.wait(t, "launch response", responseFor(seq), 10*time.Second)
	seq = c.request(t, "configurationDone", nil)
	c.wait(t, "configurationDone response", responseFor(seq), 5*time.Second)
	c.wait(t, "terminated event", eventNamed("terminated"), 10*time.Second)
	result := c.wait(t, "test result event", eventNamed("typelua/testResult"), 5*time.Second)
	var ok bool
	if err := json.Unmarshal(cbody(result, "ok"), &ok); err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("test result = %s", string(result["body"]))
	}
	seq = c.request(t, "disconnect", nil)
	c.wait(t, "disconnect response", responseFor(seq), 5*time.Second)
}

func TestProbeLuaRejectsNonInterpreter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/true")
	}
	if err := probeLua("/bin/true"); err == nil {
		t.Fatal("probeLua accepted a non-Lua executable")
	}
}

func TestProbeLuaAcceptsLua54(t *testing.T) {
	path, err := exec.LookPath("lua5.4")
	if err != nil {
		t.Skip("lua5.4 not installed")
	}
	if err := probeLua(path); err != nil {
		t.Fatalf("probeLua rejected lua5.4: %v", err)
	}
}

func TestBuildTestRunnerListMode(t *testing.T) {
	src := buildTestRunner("/proj", []string{"/proj/tests/a_test.tl"}, "", "", "", false, true)
	for _, want := range []string{"function __list", `__list("tests.a_test")`, "##tlua-test-case"} {
		if !strings.Contains(src, want) {
			t.Fatalf("listing runner missing %q:\n%s", want, src)
		}
	}
}
