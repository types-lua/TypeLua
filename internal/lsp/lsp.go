package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// 协议错误（JSON-RPC）。
const (
	ErrParse          = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternal       = -32603
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	// Result 始终序列化（可能为 null）：JSON-RPC 要求每个响应
	// 都携带 result 或 error，严格客户端拒绝两者皆无的响应。
	Result any        `json:"result"`
	Error  *respError `json:"error,omitempty"`
}

type respError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Server 是基于帧 stdio 的单线程 LSP 会话。
//
// 物理布局：
//
//	Server
//	├── in       *bufio.Reader // 输入帧读取
//	├── out      io.Writer     // 输出帧写入
//	├── docs     map[string]*document // URI -> 文档状态
//	├── version  string        // 服务器版本
//	├── shutdown bool          // 已收到 shutdown
//	├── exited   bool          // 已收到 exit
//	└── exitCode int           // 退出码
type Server struct {
	in      *bufio.Reader
	out     io.Writer
	docs    map[string]*document
	version string
	// shutdown 跟踪 LSP shutdown 握手以决定退出码。
	shutdown bool
	exited   bool
	exitCode int
}

// New 创建一个 LSP 会话，读帧来自 in，版本号用于 initialize 响应。
func New(in io.Reader, version string) *Server {
	return &Server{in: bufio.NewReader(in), docs: map[string]*document{}, version: version}
}

// Run 服务直到 EOF 或 `exit`，返回进程退出码。
func Run(in io.Reader, out io.Writer, version string) int {
	s := New(in, version)
	s.out = out
	return s.loop()
}

func (s *Server) loop() int {
	for {
		body, ok := s.readFrame()
		if !ok {
			return 1 // EOF 或传输失败
		}
		var msg message
		if err := json.Unmarshal(body, &msg); err != nil {
			// 无法解析：无法路由，丢弃（无 id 可应答）。
			continue
		}
		if s.exited {
			return s.exitCode
		}
		s.dispatch(msg)
		if s.exited {
			return s.exitCode
		}
	}
}

// readFrame 读取一条 Content-Length 帧消息。
func (s *Server) readFrame() ([]byte, bool) {
	length := -1
	for {
		line, err := s.in.ReadString('\n')
		if err != nil {
			return nil, false
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return nil, false
			}
			length = n
		}
	}
	if length < 0 {
		return nil, false
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(s.in, buf); err != nil {
		return nil, false
	}
	return buf, true
}

func (s *Server) writeFrame(body []byte) {
	fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n", len(body))
	s.out.Write(body)
}

func (s *Server) respond(id json.RawMessage, result any) {
	if len(id) == 0 {
		return // 通知：从不应答
	}
	body, _ := json.Marshal(message{JSONRPC: "2.0", ID: id, Result: result})
	s.writeFrame(body)
}

func (s *Server) respondError(id json.RawMessage, code int, format string, args ...any) {
	if len(id) == 0 {
		return
	}
	body, _ := json.Marshal(message{JSONRPC: "2.0", ID: id, Error: &respError{Code: code, Message: fmt.Sprintf(format, args...)}})
	s.writeFrame(body)
}

func (s *Server) dispatch(msg message) {
	if msg.Method == "" {
		// 对我们（从未发送）请求的响应；忽略。
		return
	}
	switch msg.Method {
	case "initialize":
		s.respond(msg.ID, s.capabilities())
	case "initialized":
	case "shutdown":
		s.shutdown = true
		s.respond(msg.ID, nil)
	case "exit":
		if s.shutdown {
			s.exitCode = 0
		} else {
			s.exitCode = 1
		}
		s.exited = true
	case "$/cancelRequest":
	case "textDocument/didOpen":
		s.didOpen(msg.Params)
	case "textDocument/didChange":
		s.didChange(msg.Params)
	case "textDocument/didClose":
		s.didClose(msg.Params)
	case "textDocument/hover":
		s.respond(msg.ID, s.hover(msg.Params))
	case "textDocument/definition":
		s.respond(msg.ID, s.definition(msg.Params))
	case "textDocument/completion":
		s.respond(msg.ID, s.completion(msg.Params))
	case "textDocument/references":
		s.respond(msg.ID, s.references(msg.Params))
	case "textDocument/rename":
		res, errCode, errMsg := s.rename(msg.Params)
		if errCode != 0 {
			s.respondError(msg.ID, errCode, "%s", errMsg)
			return
		}
		s.respond(msg.ID, res)
	case "textDocument/documentSymbol":
		s.respond(msg.ID, s.documentSymbols(msg.Params))
	default:
		s.respondError(msg.ID, ErrMethodNotFound, "method not found: %s", msg.Method)
	}
}

func (s *Server) capabilities() any {
	return map[string]any{
		"capabilities": map[string]any{
			"textDocumentSync":   2,
			"hoverProvider":      true,
			"definitionProvider": true,
			"completionProvider": map[string]any{
				"triggerCharacters": []string{".", ":"},
			},
			"referencesProvider":     true,
			"renameProvider":         true,
			"documentSymbolProvider": true,
		},
		"serverInfo": map[string]any{"name": "tlua", "version": s.version},
	}
}
