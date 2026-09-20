package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Protocol errors (JSON-RPC).
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
	Result  any             `json:"result,omitempty"`
	Error   *respError      `json:"error,omitempty"`
}

type respError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Server is a single-threaded LSP session over framed stdio.
type Server struct {
	in      *bufio.Reader
	out     io.Writer
	docs    map[string]*document
	version string
	// shutdown tracks the LSP shutdown handshake for the exit code.
	shutdown bool
	exited   bool
	exitCode int
}

func New(in io.Reader, version string) *Server {
	return &Server{in: bufio.NewReader(in), docs: map[string]*document{}, version: version}
}

// Run serves until EOF or `exit`. Returns the process exit code.
func Run(in io.Reader, out io.Writer, version string) int {
	s := New(in, version)
	s.out = out
	return s.loop()
}

func (s *Server) loop() int {
	for {
		body, ok := s.readFrame()
		if !ok {
			return 1 // EOF or transport failure
		}
		var msg message
		if err := json.Unmarshal(body, &msg); err != nil {
			// Unparseable: cannot route, drop (no id to answer).
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

// readFrame reads one Content-Length framed message.
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
		return // notification: never answer
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
		// Response to our (never sent) request; ignore.
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
