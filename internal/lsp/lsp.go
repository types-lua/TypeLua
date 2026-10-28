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
// 设计者视角：Server 是“单窗口问答台”——in/out 走 Content-Length 帧，docs 按 URI
// 存档，shutdown/exited 双旗管退出握手。障碍是并发请求：单线程串行，免锁；
// 障碍是通知无 id：respond 遇空 id 即弃。图：readFrame→dispatch→respond。不变量：
// 单 goroutine。排查现场：响应丢失先查 id 空，退出码错先查 shutdown 握手。
//
// …（略，详见实现；排查先看 Span/诊断）
//
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
//
// 设计者视角：New 只备 in 缓冲+空档+版本，out 由 Run 注入（测时可换 buffer）。
// 障碍是 bufio：帧头按行读须缓冲。候选：直读 vs 缓冲；选缓冲。不变量：docs 非 nil。
// 排查现场：读帧阻塞先查 in。
func New(in io.Reader, version string) *Server {
	return &Server{in: bufio.NewReader(in), docs: map[string]*document{}, version: version}
}

// Run 服务直到 EOF 或 `exit`，返回进程退出码。
//
// 设计者视角：Run 是 stdio 的“总闸”——New+注 out+loop，退出码直返进程。障碍是
// out 未设：调用方须给。候选：Run 包办 vs 手排；选包办，cmd/tlua 一行起服。不变量：
// 纯转调。排查现场：无输出先查 out。
func Run(in io.Reader, out io.Writer, version string) int {
	s := New(in, version)
	s.out = out
	return s.loop()
}

func (s *Server) loop() int {
	// 设计者视角：loop 逐帧 Unmarshal→dispatch，坏帧丢弃（无 id 无法应），exit 后
	// 即返码。障碍是坏 JSON 卡死：continue 跳过。候选：报错 vs 丢弃；选丢弃保活。
	// 排查现场：服务无响应先查 readFrame 是否 EOF 返 1。
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
//
// 设计者视角：readFrame 按 LSP 帧规读头（空行止，大小写不敏感取 Content-Length，
// 非法即 false）再 ReadFull 取体。障碍是粘包/半包：ReadFull 保足长；头缺失即 false。
// 候选：Content-Type 忽略 vs 校验；选忽略。排查现场：帧失败先查头大小写与长度。
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
	// 设计者视角：writeFrame 按 `Content-Length: n\r\n\r\n` 封帧直写，无缓冲刷
	// （调用方 out 自决）。障碍是 \n vs \r\n：须双回车合规。排查现场：客户端卡帧
	// 先查分隔符。
	fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n", len(body))
	s.out.Write(body)
}

func (s *Server) respond(id json.RawMessage, result any) {
	// 设计者视角：respond 给请求回 result（含 null 保形，见 message 注释），通知
	// （无 id）静默弃。障碍是严格客户端拒无 result/error 帧：此处恒带 result。
	// 排查现场：通知误回先查 id 判空。
	if len(id) == 0 {
		return // 通知：从不应答
	}
	body, _ := json.Marshal(message{JSONRPC: "2.0", ID: id, Result: result})
	s.writeFrame(body)
}

func (s *Server) respondError(id json.RawMessage, code int, format string, args ...any) {
	// 设计者视角：错误应答与 respond 对仗，无 id 亦弃。障碍是 code 须 JSON-RPC 标
	// （见 Err*）。排查现场：客户端不识别先查 code。
	if len(id) == 0 {
		return
	}
	body, _ := json.Marshal(message{JSONRPC: "2.0", ID: id, Error: &respError{Code: code, Message: fmt.Sprintf(format, args...)}})
	s.writeFrame(body)
}

func (s *Server) dispatch(msg message) {
	// 设计者视角：dispatch 是方法“总机”——空 Method（对我方请求的响应）丢弃；
	// initialize/shutdown/exit 走握手，文本同步走 did*，查询走 hover/definition/
	// completion/references/rename/symbols，未知报 MethodNotFound。障碍是 rename
	// 三元返详见实现，排查看 Span。
	// 各法签名不一。不变量：未知法恒错应。排查现场：新能力无响应先查此表。
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
	// 设计者视角：能力表宣告全量问答——sync 增量 2、hover/definition/references/
	// rename/symbols 全开，补全挂 `.`/`:` 触发。障碍是客户端按表裁剪：少宣即少调。
	// 候选：渐进开 vs 全开；选全开。不变量：serverInfo 带名版。排查现场：补全不触发
	// 先查 triggerCharacters。
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
