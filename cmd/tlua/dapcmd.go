package main

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/typelua/typelua/internal/compiler"
	"github.com/typelua/typelua/internal/config"
)

// tlua dap: Debug Adapter Protocol over stdio.
//
// The adapter compiles the program (with source mappings), writes it to a
// temp directory and runs it under `dap_agent.lua` with an external Lua
// interpreter. The agent reports line hooks over stderr; the adapter maps
// emitted Lua lines back to .tl positions and speaks DAP to the editor.

//go:embed dap_agent.lua
var dapAgentSource string

func cmdDAP(args []string) error {
	fs := flag.NewFlagSet("dap", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runDAP(os.Stdin, os.Stdout)
}

type dapConn struct {
	r   *bufio.Reader
	w   io.Writer
	mu  sync.Mutex
	seq int
}

func newDAPConn(r io.Reader, w io.Writer) *dapConn {
	return &dapConn{r: bufio.NewReader(r), w: w}
}

func (c *dapConn) send(msg map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	msg["seq"] = c.seq
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n%s", len(raw), raw)
	return err
}

func (c *dapConn) read() (map[string]json.RawMessage, error) {
	length := -1
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if key, value, ok := strings.Cut(line, ":"); ok {
			if strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
				n, err := strconv.Atoi(strings.TrimSpace(value))
				if err != nil {
					return nil, fmt.Errorf("bad Content-Length: %v", err)
				}
				length = n
			}
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return nil, err
	}
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("bad DAP JSON: %v", err)
	}
	return msg, nil
}

func rawString(m map[string]json.RawMessage, key string) string {
	var s string
	if v, ok := m[key]; ok {
		_ = json.Unmarshal(v, &s)
	}
	return s
}

func rawInt(m map[string]json.RawMessage, key string) int {
	var n int
	if v, ok := m[key]; ok {
		_ = json.Unmarshal(v, &n)
	}
	return n
}

type launchArgs struct {
	Program     string   `json:"program"`
	Args        []string `json:"args"`
	Lua         string   `json:"lua"`
	Target      string   `json:"target"`
	Check       bool     `json:"check"`
	Test        string   `json:"test"`
	TestCase    string   `json:"testCase"`
	Cwd         string   `json:"cwd"`
	StopOnEntry bool     `json:"stopOnEntry"`
}

type bridgeFrame struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	File string `json:"file"`
	Line int    `json:"line"`
}

type bridgeVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type bridgeMsg struct {
	Type    string        `json:"type"`
	ID      int           `json:"id"`
	Ok      bool          `json:"ok"`
	Reason  string        `json:"reason"`
	File    string        `json:"file"`
	Line    int           `json:"line"`
	Frames  []bridgeFrame `json:"frames"`
	Vars    []bridgeVar   `json:"vars"`
	Message string        `json:"message"`
	Code    int           `json:"code"`
}

type srcPos struct {
	file string
	line int
}

type dapSession struct {
	conn *dapConn

	cfg         *config.Config
	prog        *compiler.Program
	tmp         string
	entry       string // emitted Lua entry (absolute)
	luaBin      string
	agent       string
	config      string
	stopOnEntry bool

	// path -> generated line -> original position
	genToSrc map[string]map[int]srcPos
	// original source -> line -> generated lines
	srcToGen map[string]map[int][]int
	srcToLua map[string]string

	breakpoints map[string][]int

	child    *exec.Cmd
	childIn  io.WriteCloser
	childOut io.ReadCloser
	childErr io.ReadCloser

	mu         sync.Mutex
	nextID     int
	pending    map[int]chan bridgeMsg
	stopped    bool
	terminated bool
	testMode   bool
	testFailed string
	started    bool
	closed     chan struct{}
	closeOnce  sync.Once
}

func runDAP(r io.Reader, w io.Writer) error {
	s := &dapSession{
		conn:        newDAPConn(r, w),
		genToSrc:    map[string]map[int]srcPos{},
		srcToGen:    map[string]map[int][]int{},
		srcToLua:    map[string]string{},
		breakpoints: map[string][]int{},
		pending:     map[int]chan bridgeMsg{},
		closed:      make(chan struct{}),
	}
	defer s.shutdown()

	for {
		msg, err := s.conn.read()
		if err != nil {
			return nil
		}
		command := rawString(msg, "command")
		seq := rawInt(msg, "seq")
		switch command {
		case "initialize":
			s.onInitialize(seq)
		case "launch":
			s.onLaunch(seq, msg["arguments"])
		case "setBreakpoints":
			s.onSetBreakpoints(seq, msg["arguments"])
		case "configurationDone":
			s.respond(seq, command, nil)
			if !s.started {
				if err := s.startDebuggee(); err != nil {
					s.event("output", map[string]any{"category": "stderr", "output": err.Error() + "\n"})
					s.event("terminated", map[string]any{})
					return nil
				}
			}
		case "threads":
			s.respond(seq, command, map[string]any{"threads": []map[string]any{{"id": 1, "name": "main"}}})
		case "stackTrace":
			s.onStackTrace(seq, msg["arguments"])
		case "scopes":
			s.onScopes(seq, msg["arguments"])
		case "variables":
			s.onVariables(seq, msg["arguments"])
		case "continue", "next", "stepIn", "stepOut":
			s.onResume(seq, command)
		case "pause":
			s.fail(seq, command, "pause is not supported while the program is running")
		case "evaluate":
			s.fail(seq, command, "evaluate is not supported")
		case "setExceptionBreakpoints":
			s.respond(seq, command, map[string]any{"breakpoints": []any{}})
		case "disconnect", "terminate":
			s.respond(seq, command, nil)
			return nil
		default:
			// Unknown requests are acknowledged so clients do not hang.
			s.respond(seq, command, nil)
		}
	}
}

func (s *dapSession) respond(seq int, command string, body map[string]any) {
	msg := map[string]any{
		"type":        "response",
		"request_seq": seq,
		"command":     command,
		"success":     true,
	}
	if body != nil {
		msg["body"] = body
	}
	_ = s.conn.send(msg)
}

func (s *dapSession) fail(seq int, command string, message string) {
	_ = s.conn.send(map[string]any{
		"type":        "response",
		"request_seq": seq,
		"command":     command,
		"success":     false,
		"message":     message,
	})
}

func (s *dapSession) event(name string, body map[string]any) {
	// DAP events carry their payload under "body" (unlike requests, which use
	// "arguments"). Putting fields at the top level silently breaks VS Code.
	msg := map[string]any{"type": "event", "event": name}
	if body == nil {
		body = map[string]any{}
	}
	msg["body"] = body
	_ = s.conn.send(msg)
}

func (s *dapSession) onInitialize(seq int) {
	s.respond(seq, "initialize", map[string]any{
		"supportsConfigurationDoneRequest":  true,
		"supportsTerminateRequest":          true,
		"supportsFunctionBreakpoints":       false,
		"supportsConditionalBreakpoints":    false,
		"supportsHitConditionalBreakpoints": false,
		"supportsEvaluateForHovers":         false,
		"supportsStepBack":                  false,
		"supportsSetVariable":               false,
		"supportsRestartRequest":            false,
	})
	s.event("initialized", map[string]any{})
}

func (s *dapSession) onLaunch(seq int, args json.RawMessage) {
	var la launchArgs
	if err := json.Unmarshal(args, &la); err != nil {
		s.fail(seq, "launch", "invalid launch arguments: "+err.Error())
		return
	}
	program := strings.TrimSpace(la.Program)
	if la.Test != "" {
		program = la.Test
	}
	if program == "" {
		s.fail(seq, "launch", "launch requires a 'program' (or 'test') path")
		return
	}
	if !filepath.IsAbs(program) {
		if la.Cwd != "" {
			program = filepath.Join(la.Cwd, program)
		} else if cwd, err := os.Getwd(); err == nil {
			program = filepath.Join(cwd, program)
		}
	}
	program = filepath.Clean(program)

	tmp, err := os.MkdirTemp("", "tlua-dap-*")
	if err != nil {
		s.fail(seq, "launch", err.Error())
		return
	}
	s.tmp = tmp

	var cfg *config.Config
	if cfgPath := config.FindUp(filepath.Dir(program)); cfgPath != "" {
		cfg, err = config.Load(cfgPath)
		if err != nil {
			s.fail(seq, "launch", "bad config: "+err.Error())
			return
		}
	} else {
		cfg = config.Default(filepath.Dir(program))
	}
	cfg.CompilerOptions.OutDir = tmp
	s.cfg = cfg

	p := compiler.NewProgram(cfg)
	if err := p.Load(); err != nil {
		s.fail(seq, "launch", err.Error())
		return
	}
	if !programHasFile(p, program) {
		data, err := os.ReadFile(program)
		if err != nil {
			s.fail(seq, "launch", "cannot read program: "+err.Error())
			return
		}
		p.AddVirtual(program, string(data))
	}

	entry := program
	if la.Test != "" {
		entry = s.addTestRunner(p, program, la.TestCase)
	}
	s.stopOnEntry = la.StopOnEntry
	s.testMode = la.Test != ""
	s.prog = p

	if la.Check {
		p.Check()
		if len(p.Diags) > 0 {
			s.event("output", map[string]any{"category": "stderr", "output": p.FormatDiags()})
		}
		if p.HasErrors() {
			s.fail(seq, "launch", "program has type errors")
			return
		}
	}

	rendered := p.Render()
	for _, rf := range rendered {
		if err := os.MkdirAll(filepath.Dir(rf.Path), 0o755); err != nil {
			s.fail(seq, "launch", err.Error())
			return
		}
		if err := os.WriteFile(rf.Path, []byte(rf.Lua), 0o644); err != nil {
			s.fail(seq, "launch", err.Error())
			return
		}
	}
	s.buildMappings(rendered)

	entryAbs := filepath.Clean(entry)
	found := false
	for _, rf := range rendered {
		if filepath.Clean(rf.Path) == entryAbs || filepath.Clean(rf.SourcePath) == entryAbs {
			entryAbs = filepath.Clean(rf.Path)
			found = true
			break
		}
	}
	if !found {
		s.fail(seq, "launch", "cannot locate the emitted entry for "+entry)
		return
	}
	s.entry = entryAbs

	if la.Lua != "" {
		s.luaBin = la.Lua
	} else {
		s.luaBin = detectLua(cfg.Target())
	}
	if s.luaBin == "" {
		s.fail(seq, "launch", "no Lua interpreter found; install lua5.4 or luajit, or set 'lua' in launch.json")
		return
	}

	s.respond(seq, "launch", nil)
}

// addTestRunner injects tlua_test plus a synthetic entry that invokes one case
// (or every case) with no pcall, so breakpoints and errors surface directly.
func (s *dapSession) addTestRunner(p *compiler.Program, testPath string, testCase string) string {
	p.AddVirtual(filepath.Join(s.cfg.Dir, "tlua_modules", "tlua_test.tl"), tluaTestSource)
	rel, err := filepath.Rel(s.cfg.Dir, testPath)
	if err != nil {
		rel = filepath.Base(testPath)
	}
	module := strings.ReplaceAll(strings.TrimSuffix(filepath.ToSlash(rel), ".tl"), "/", ".")
	var sb strings.Builder
	fmt.Fprintf(&sb, "local suite = require(%q)\n", module)
	if testCase != "" {
		fmt.Fprintf(&sb, "suite[%q]()\n", testCase)
	} else {
		sb.WriteString("local names = {}\nfor name, value in pairs(suite) do if type(value) == \"function\" then names[#names + 1] = name end end\n")
		sb.WriteString("table.sort(names)\nfor i = 1, #names do suite[names[i]]() end\n")
	}
	runner := filepath.Join(s.cfg.Dir, "__tlua_test_debug_main.tl")
	p.AddVirtual(runner, sb.String())
	return runner
}

// buildMappings indexes source maps in both directions.
func (s *dapSession) buildMappings(rendered []compiler.RenderedFile) {
	for _, rf := range rendered {
		luaPath := filepath.Clean(rf.Path)
		byGen := map[int]srcPos{}
		for _, m := range rf.Mappings {
			src := m.SrcFile
			if !filepath.IsAbs(src) {
				src = filepath.Join(s.cfg.Dir, src)
			}
			src = filepath.Clean(src)
			srcLine := m.SrcLine + 1
			byGen[m.GenLine] = srcPos{file: src, line: srcLine}
			if s.srcToGen[src] == nil {
				s.srcToGen[src] = map[int][]int{}
			}
			s.srcToGen[src][srcLine] = append(s.srcToGen[src][srcLine], m.GenLine)
			s.srcToLua[src] = luaPath
		}
		s.genToSrc[luaPath] = byGen
	}
}

func detectLua(target string) string {
	candidates := []string{"lua5.4", "lua", "luajit"}
	if target == "5.1" {
		candidates = []string{"luajit", "lua5.1", "lua5.4", "lua"}
	}
	for _, c := range candidates {
		if path, err := exec.LookPath(c); err == nil {
			return path
		}
	}
	return ""
}

func (s *dapSession) onSetBreakpoints(seq int, args json.RawMessage) {
	var payload struct {
		Source struct {
			Path string `json:"path"`
		} `json:"source"`
		Breakpoints []struct {
			Line int `json:"line"`
		} `json:"breakpoints"`
		Lines []int `json:"lines"`
	}
	_ = json.Unmarshal(args, &payload)
	source := payload.Source.Path
	var requested []int
	for _, bp := range payload.Breakpoints {
		requested = append(requested, bp.Line)
	}
	if len(requested) == 0 {
		requested = payload.Lines
	}
	if source == "" {
		s.fail(seq, "setBreakpoints", "source.path is required")
		return
	}
	if abs, err := filepath.Abs(source); err == nil {
		source = abs
	}
	source = filepath.Clean(source)
	s.breakpoints[source] = requested

	verified := make([]map[string]any, 0, len(requested))
	for _, line := range requested {
		ok := len(s.srcToGen[source][line]) > 0
		verified = append(verified, map[string]any{"verified": ok, "line": line})
	}
	s.sendBreakpoints(source)
	s.respond(seq, "setBreakpoints", map[string]any{"breakpoints": verified})
}

// sendBreakpoints pushes the current set for one source to the agent. Updates
// while the program is running are applied at the next stop (the agent reads
// stdin only while paused); pre-launch sets are baked into the config.
func (s *dapSession) sendBreakpoints(source string) {
	if !s.started || s.childIn == nil {
		return
	}
	luaPath, ok := s.srcToLua[source]
	if !ok {
		return
	}
	var gens []string
	for _, line := range s.breakpoints[source] {
		for _, g := range s.srcToGen[source][line] {
			gens = append(gens, strconv.Itoa(g))
		}
	}
	sort.Strings(gens)
	_, _ = fmt.Fprintf(s.childIn, "bp\t%s\t%s\n", luaPath, strings.Join(gens, ","))
}

func (s *dapSession) onStackTrace(seq int, args json.RawMessage) {
	frameID := 1
	var payload struct {
		StartFrame int `json:"startFrame"`
		Levels     int `json:"levels"`
	}
	_ = json.Unmarshal(args, &payload)
	frames := []map[string]any{}
	if s.stopped {
		if msg, err := s.bridgeCall("stack", 0); err == nil {
			for _, f := range msg.Frames {
				frameID = f.ID
				frames = append(frames, map[string]any{
					"id":     f.ID,
					"name":   f.Name,
					"line":   f.Line,
					"column": 1,
					"source": map[string]any{"name": filepath.Base(f.File), "path": f.File},
				})
			}
		}
	}
	if len(frames) == 0 {
		frames = append(frames, map[string]any{"id": frameID, "name": "main", "line": 1, "column": 1})
	}
	s.respond(seq, "stackTrace", map[string]any{"stackFrames": frames, "totalFrames": len(frames)})
}

func (s *dapSession) onScopes(seq int, args json.RawMessage) {
	var payload struct {
		FrameID int `json:"frameId"`
	}
	_ = json.Unmarshal(args, &payload)
	ref := 100000 + payload.FrameID
	s.respond(seq, "scopes", map[string]any{
		"scopes": []map[string]any{{
			"name":               "Locals",
			"variablesReference": ref,
			"expensive":          false,
		}},
	})
}

func (s *dapSession) onVariables(seq int, args json.RawMessage) {
	var payload struct {
		VariablesReference int `json:"variablesReference"`
	}
	_ = json.Unmarshal(args, &payload)
	level := payload.VariablesReference - 100000
	vars := []map[string]any{}
	if level > 0 {
		if msg, err := s.bridgeCall("vars", level); err == nil {
			for _, v := range msg.Vars {
				vars = append(vars, map[string]any{
					"name":               v.Name,
					"value":              v.Value,
					"type":               v.Type,
					"variablesReference": 0,
				})
			}
		}
	}
	s.respond(seq, "variables", map[string]any{"variables": vars})
}

func (s *dapSession) onResume(seq int, command string) {
	if !s.stopped {
		s.fail(seq, command, "program is not stopped")
		return
	}
	cmd := map[string]string{
		"continue": "continue",
		"next":     "next",
		"stepIn":   "stepin",
		"stepOut":  "stepout",
	}[command]
	if cmd == "" {
		s.fail(seq, command, "unsupported resume command")
		return
	}
	_, _ = fmt.Fprintf(s.childIn, "%s\n", cmd)
	s.stopped = false
	body := map[string]any{}
	if command == "continue" {
		body["allThreadsContinued"] = true
	}
	s.respond(seq, command, body)
}

func (s *dapSession) bridgeCall(kind string, level int) (bridgeMsg, error) {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	ch := make(chan bridgeMsg, 1)
	s.pending[id] = ch
	s.mu.Unlock()

	var line string
	if kind == "vars" {
		line = fmt.Sprintf("vars\t%d\t%d\n", id, level)
	} else {
		line = fmt.Sprintf("stack\t%d\n", id)
	}
	if _, err := io.WriteString(s.childIn, line); err != nil {
		return bridgeMsg{}, err
	}
	select {
	case msg := <-ch:
		return msg, nil
	case <-time.After(5 * time.Second):
		return bridgeMsg{}, fmt.Errorf("debug agent did not answer %s", kind)
	case <-s.closed:
		return bridgeMsg{}, fmt.Errorf("debug session closed")
	}
}

func (s *dapSession) startDebuggee() error {
	if s.started {
		return nil
	}
	configPath := filepath.Join(s.tmp, "__tlua_dap_config.lua")
	if err := os.WriteFile(configPath, []byte(s.agentConfig()), 0o644); err != nil {
		return err
	}
	agentPath := filepath.Join(s.tmp, "__tlua_dap_agent.lua")
	if err := os.WriteFile(agentPath, []byte(dapAgentSource), 0o644); err != nil {
		return err
	}

	cmd := exec.Command(s.luaBin, agentPath, configPath)
	cmd.Dir = s.cfg.Dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	s.child = cmd
	s.childIn = stdin
	s.childOut = stdout
	s.childErr = stderr
	s.started = true

	go s.readStdout(stdout)
	go s.readStderr(stderr)
	go func() {
		err := cmd.Wait()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
		}
		if !s.terminated {
			s.terminated = true
			s.emitTestResult()
			s.event("exited", map[string]any{"exitCode": code})
			s.event("terminated", map[string]any{})
		}
		s.closeOnce.Do(func() { close(s.closed) })
	}()
	return nil
}

func (s *dapSession) readStdout(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		s.event("output", map[string]any{"category": "stdout", "output": scanner.Text() + "\n"})
	}
}

func (s *dapSession) readStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "##tlua-dap ") {
			s.event("output", map[string]any{"category": "stderr", "output": line + "\n"})
			continue
		}
		var msg bridgeMsg
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "##tlua-dap ")), &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "stopped":
			s.stopped = true
			s.event("stopped", map[string]any{
				"reason":            msg.Reason,
				"threadId":          1,
				"allThreadsStopped": true,
			})
		case "response":
			s.mu.Lock()
			ch := s.pending[msg.ID]
			delete(s.pending, msg.ID)
			s.mu.Unlock()
			if ch != nil {
				select {
				case ch <- msg:
				default:
				}
			}
		case "exited":
			if !s.terminated {
				s.terminated = true
				s.emitTestResult()
				s.event("exited", map[string]any{"exitCode": msg.Code})
				s.event("terminated", map[string]any{})
			}
		case "error":
			if s.testMode {
				s.testFailed = msg.Message
			}
			s.event("output", map[string]any{"category": "stderr", "output": msg.Message + "\n"})
			s.stopped = false
		}
	}
}

// emitTestResult tells the extension whether a debugged test case passed.
// It is a custom DAP event, only sent for test-mode launches.
func (s *dapSession) emitTestResult() {
	if !s.testMode {
		return
	}
	ok := s.testFailed == ""
	s.event("typelua/testResult", map[string]any{"ok": ok, "message": s.testFailed})
}

func (s *dapSession) agentConfig() string {
	var sb strings.Builder
	sb.WriteString("return {\n")
	sb.WriteString("  entry = " + luaQuote(s.entry) + ",\n")
	if s.stopOnEntry {
		sb.WriteString("  stopOnEntry = true,\n")
	}
	sb.WriteString("  maps = {\n")
	luaPaths := make([]string, 0, len(s.genToSrc))
	for luaPath := range s.genToSrc {
		luaPaths = append(luaPaths, luaPath)
	}
	sort.Strings(luaPaths)
	for _, luaPath := range luaPaths {
		fmt.Fprintf(&sb, "    [%s] = {\n", luaQuote(luaPath))
		gens := make([]int, 0, len(s.genToSrc[luaPath]))
		for gen := range s.genToSrc[luaPath] {
			gens = append(gens, gen)
		}
		sort.Ints(gens)
		for _, gen := range gens {
			pos := s.genToSrc[luaPath][gen]
			fmt.Fprintf(&sb, "      [%d] = { %s, %d },\n", gen, luaQuote(pos.file), pos.line)
		}
		sb.WriteString("    },\n")
	}
	sb.WriteString("  },\n  breakpoints = {\n")
	sources := make([]string, 0, len(s.breakpoints))
	for src := range s.breakpoints {
		sources = append(sources, src)
	}
	sort.Strings(sources)
	byLua := map[string]map[int]bool{}
	for _, src := range sources {
		luaPath, ok := s.srcToLua[src]
		if !ok {
			continue
		}
		if byLua[luaPath] == nil {
			byLua[luaPath] = map[int]bool{}
		}
		for _, line := range s.breakpoints[src] {
			for _, gen := range s.srcToGen[src][line] {
				byLua[luaPath][gen] = true
			}
		}
	}
	luaPaths = luaPaths[:0]
	for luaPath := range byLua {
		luaPaths = append(luaPaths, luaPath)
	}
	sort.Strings(luaPaths)
	for _, luaPath := range luaPaths {
		fmt.Fprintf(&sb, "    [%s] = {\n", luaQuote(luaPath))
		gens := make([]int, 0, len(byLua[luaPath]))
		for gen := range byLua[luaPath] {
			gens = append(gens, gen)
		}
		sort.Ints(gens)
		for _, gen := range gens {
			fmt.Fprintf(&sb, "      [%d] = true,\n", gen)
		}
		sb.WriteString("    },\n")
	}
	sb.WriteString("  },\n}\n")
	return sb.String()
}

func (s *dapSession) shutdown() {
	if s.childIn != nil {
		_, _ = io.WriteString(s.childIn, "quit\n")
		_ = s.childIn.Close()
	}
	if s.child != nil && s.child.Process != nil {
		done := make(chan struct{})
		go func() {
			select {
			case <-s.closed:
			case <-time.After(500 * time.Millisecond):
				_ = s.child.Process.Kill()
			}
			close(done)
		}()
		<-done
	}
	if s.tmp != "" {
		_ = os.RemoveAll(s.tmp)
	}
}
