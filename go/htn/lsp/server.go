// Package lsp implements the HTN language server (HTNLanguageServer):
// JSON-RPC over stdio with Content-Length framing, full-document sync,
// diagnostics, go-to-definition, variable completion and the custom
// "htn/compile" request used by the editor's compile command.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/compiler"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/tooling"
)

// Transport reads and writes LSP messages with Content-Length framing.
type Transport struct {
	reader *bufio.Reader
	writer io.Writer
}

// NewTransport creates a transport over the given streams.
func NewTransport(input io.Reader, output io.Writer) *Transport {
	return &Transport{reader: bufio.NewReader(input), writer: output}
}

// ReadMessage reads one JSON payload. It returns false at end of input or on
// malformed framing.
func (t *Transport) ReadMessage() ([]byte, bool) {
	contentLength, hasLength := 0, false
	for {
		line, err := t.reader.ReadString('\n')
		if err != nil && line == "" {
			return nil, false
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		const prefix = "content-length:"
		if len(line) >= len(prefix) && strings.EqualFold(line[:len(prefix)], prefix) {
			value := strings.TrimLeft(line[len(prefix):], " \t")
			parsed, parseErr := strconv.ParseUint(value, 10, 63)
			if parseErr != nil {
				return nil, false
			}
			contentLength, hasLength = int(parsed), true
		}
		if err != nil {
			return nil, false
		}
	}
	if !hasLength {
		return nil, false
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(t.reader, payload); err != nil {
		return nil, false
	}
	return payload, true
}

// WriteMessage writes one framed JSON payload.
func (t *Transport) WriteMessage(payload []byte) {
	fmt.Fprintf(t.writer, "Content-Length: %d\r\n\r\n", len(payload))
	t.writer.Write(payload)
	if flusher, ok := t.writer.(interface{ Flush() error }); ok {
		flusher.Flush()
	}
}

// Server is the language server state.
type Server struct {
	transport         *Transport
	documents         *tooling.Store
	shutdownRequested bool
	exitRequested     bool
	// Log receives protocol errors (stderr in the command).
	Log io.Writer
}

// NewServer creates a server on a transport.
func NewServer(transport *Transport) *Server {
	return &Server{transport: transport, documents: tooling.NewStore(), Log: io.Discard}
}

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type textDocumentPosition struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Text    string `json:"text"`
		Version *int64 `json:"version"`
	} `json:"textDocument"`
	Position *struct {
		Line      *int `json:"line"`
		Character *int `json:"character"`
	} `json:"position"`
	ContentChanges []struct {
		Text *string `json:"text"`
	} `json:"contentChanges"`
}

// Run processes messages until "exit" or end of input. It returns 0 when a
// shutdown was requested first, 1 otherwise.
func (s *Server) Run() int {
	for !s.exitRequested {
		payload, ok := s.transport.ReadMessage()
		if !ok {
			break
		}
		var m message
		if err := json.Unmarshal(payload, &m); err != nil {
			fmt.Fprintf(s.Log, "htn-lsp: invalid JSON-RPC payload: %v\n", err)
			continue
		}
		if m.Method == "" {
			continue
		}
		if len(m.ID) != 0 && string(m.ID) != "null" {
			s.handleRequest(m.ID, m.Method, m.Params)
		} else {
			s.handleNotification(m.Method, m.Params)
		}
	}
	if s.shutdownRequested {
		return 0
	}
	return 1
}

func (s *Server) send(value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		return
	}
	s.transport.WriteMessage(payload)
}

func (s *Server) sendResponse(id json.RawMessage, result any) {
	s.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *Server) sendError(id json.RawMessage, code int, text string) {
	s.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": text}})
}

func (s *Server) sendNotification(method string, params any) {
	s.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (s *Server) handleRequest(id json.RawMessage, method string, params json.RawMessage) {
	switch method {
	case "initialize":
		s.sendResponse(id, map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync":   1,
				"definitionProvider": true,
				"completionProvider": map[string]any{"resolveProvider": false},
			},
			"serverInfo": map[string]any{"name": "htn-lsp", "version": "0.1.0"},
		})
	case "textDocument/definition":
		if len(params) == 0 {
			s.sendError(id, -32602, "Missing definition params")
			return
		}
		s.handleDefinition(id, params)
	case "textDocument/completion":
		if len(params) == 0 {
			s.sendError(id, -32602, "Missing completion params")
			return
		}
		s.handleCompletion(id, params)
	case "htn/compile":
		if len(params) == 0 {
			s.sendError(id, -32602, "Missing compile params")
			return
		}
		s.handleCompile(id, params)
	case "shutdown":
		s.shutdownRequested = true
		s.sendResponse(id, nil)
	default:
		s.sendError(id, -32601, "Method not found: "+method)
	}
}

func (s *Server) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "exit":
		s.exitRequested = true
		return
	case "initialized":
		return
	}
	if len(params) == 0 {
		return
	}
	var p textDocumentPosition
	if json.Unmarshal(params, &p) != nil || p.TextDocument.URI == "" {
		return
	}
	version := uint64(0)
	if p.TextDocument.Version != nil && *p.TextDocument.Version > 0 {
		version = uint64(*p.TextDocument.Version)
	}
	path := URIToPath(p.TextDocument.URI)
	switch method {
	case "textDocument/didOpen":
		s.documents.Open(path, p.TextDocument.Text, version)
		s.publishDiagnostics(p.TextDocument.URI, path)
	case "textDocument/didChange":
		// Full document sync: the first change carries the complete text.
		if len(p.ContentChanges) == 0 || p.ContentChanges[0].Text == nil {
			return
		}
		text := *p.ContentChanges[0].Text
		if !s.documents.Update(path, text, version) {
			s.documents.Open(path, text, version)
		}
		s.publishDiagnostics(p.TextDocument.URI, path)
	case "textDocument/didClose":
		s.documents.Close(path)
		s.sendNotification("textDocument/publishDiagnostics", map[string]any{
			"uri": p.TextDocument.URI, "diagnostics": []any{}})
	}
}

func (s *Server) positionParams(params json.RawMessage) (string, int, int, bool) {
	var p textDocumentPosition
	if json.Unmarshal(params, &p) != nil || p.TextDocument.URI == "" || p.Position == nil ||
		p.Position.Line == nil || p.Position.Character == nil {
		return "", 0, 0, false
	}
	return p.TextDocument.URI, *p.Position.Line, *p.Position.Character, true
}

func makePosition(line, column int) position {
	if line < 1 {
		line = 1
	}
	if column < 1 {
		column = 1
	}
	return position{Line: line - 1, Character: column - 1}
}

func (s *Server) handleDefinition(id json.RawMessage, params json.RawMessage) {
	uri, line, character, ok := s.positionParams(params)
	if !ok {
		s.sendError(id, -32602, "Invalid definition params")
		return
	}
	path := URIToPath(uri)
	document := s.documents.Document(path)
	if document == nil {
		s.sendResponse(id, nil)
		return
	}
	offset := PositionToOffset(document.Text, line, character)
	if model := s.documents.Model(path); model != nil {
		if definition, found := model.DefinitionAt(offset); found {
			s.sendResponse(id, map[string]any{
				"uri": PathToURI(definition.FilePath),
				"range": lspRange{
					Start: makePosition(definition.Range.Begin.Line, definition.Range.Begin.Column),
					End:   makePosition(definition.Range.End.Line, definition.Range.End.Column),
				},
			})
			return
		}
	}
	s.sendResponse(id, nil)
}

func (s *Server) handleCompletion(id json.RawMessage, params json.RawMessage) {
	uri, line, character, ok := s.positionParams(params)
	if !ok {
		s.sendError(id, -32602, "Invalid completion params")
		return
	}
	path := URIToPath(uri)
	document := s.documents.Document(path)
	items := []any{}
	if document == nil {
		s.sendResponse(id, items)
		return
	}
	offset := PositionToOffset(document.Text, line, character)
	if model := s.documents.Model(path); model != nil {
		for _, candidate := range model.AutocompleteCandidates(offset) {
			items = append(items, map[string]any{"label": candidate, "kind": 6, "insertText": candidate})
		}
	}
	s.sendResponse(id, items)
}

func diagnosticFields(d compiler.Diagnostic, source string) map[string]any {
	beginLine, beginColumn := d.Range.Begin.Line, d.Range.Begin.Column
	if beginLine < 1 {
		beginLine = 1
	}
	if beginColumn < 1 {
		beginColumn = 1
	}
	endLine, endColumn := d.Range.End.Line, d.Range.End.Column
	if endLine < beginLine {
		endLine = beginLine
	}
	if endColumn < 1 {
		endColumn = 1
	}
	// Semantic/link diagnostics do not all carry exact ranges: keep them
	// visible with a one-character fallback range.
	if endLine == beginLine && endColumn <= beginColumn {
		endColumn = beginColumn + 1
	}
	severity := 1
	switch d.Severity {
	case compiler.SeverityWarning:
		severity = 2
	case compiler.SeverityInfo:
		severity = 3
	}
	return map[string]any{
		"range":    lspRange{Start: makePosition(beginLine, beginColumn), End: makePosition(endLine, endColumn)},
		"severity": severity,
		"source":   source,
		"message":  d.Message,
	}
}

func (s *Server) handleCompile(id json.RawMessage, params json.RawMessage) {
	var p textDocumentPosition
	if json.Unmarshal(params, &p) != nil || p.TextDocument.URI == "" {
		s.sendError(id, -32602, "Invalid compile params")
		return
	}
	path := URIToPath(p.TextDocument.URI)
	document := s.documents.Document(path)
	if document == nil {
		s.sendResponse(id, map[string]any{"success": false,
			"message": "The active HTN document is not open in the language server.", "diagnostics": []any{}})
		return
	}
	// Compile exactly what the editor sees: open buffers win over disk.
	var sink compiler.DiagnosticSink
	result, ok := compiler.LoadFromSource(path, document.Text, s.documents.Read, &sink, compiler.DefaultLoadOptions())
	diagnostics := []any{}
	for _, d := range sink.Diagnostics() {
		fields := diagnosticFields(d, "htn-compile")
		file := d.FilePath
		if file == "" {
			file = path
		}
		fields["uri"] = PathToURI(file)
		diagnostics = append(diagnostics, fields)
	}
	if ok && !sink.HasErrors() {
		s.sendResponse(id, map[string]any{"success": true,
			"message":     fmt.Sprintf("Compile succeeded: %s (%d linked source file(s))", result.Domain.ID, len(result.SourceFiles)),
			"diagnostics": diagnostics})
		return
	}
	s.sendResponse(id, map[string]any{"success": false,
		"message": fmt.Sprintf("Compile failed with %d error(s).", sink.ErrorCount()), "diagnostics": diagnostics})
}

func (s *Server) publishDiagnostics(uri, path string) {
	diagnostics := []any{}
	if document := s.documents.Document(path); document != nil {
		var sink compiler.DiagnosticSink
		compiler.LoadFromSource(path, document.Text, s.documents.Read, &sink, compiler.LoadOptions{RequireTopLevelRoot: false})
		for _, d := range sink.Diagnostics() {
			file := d.FilePath
			if file == "" {
				file = path
			}
			// didOpen/didChange publishes diagnostics of this document only;
			// htn/compile reports every linked file.
			if !tooling.SamePath(file, path) {
				continue
			}
			diagnostics = append(diagnostics, diagnosticFields(d, "htn"))
		}
	}
	s.sendNotification("textDocument/publishDiagnostics", map[string]any{"uri": uri, "diagnostics": diagnostics})
}

// URIToPath converts a file:// URI into a local path.
func URIToPath(uri string) string {
	if !strings.HasPrefix(uri, "file://") {
		return percentDecode(uri)
	}
	return percentDecode(uri[len("file://"):])
}

// PathToURI converts a local path into a file:// URI.
func PathToURI(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return "file://" + percentEncodePath(filepath.ToSlash(path))
}

func percentEncodePath(text string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		c := text[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			c == '-' || c == '_' || c == '.' || c == '~' || c == '/' || c == ':' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

func percentDecode(text string) string {
	hexValue := func(c byte) int {
		switch {
		case c >= '0' && c <= '9':
			return int(c - '0')
		case c >= 'a' && c <= 'f':
			return int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			return int(c-'A') + 10
		}
		return -1
	}
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '%' && i+2 < len(text) {
			hi, lo := hexValue(text[i+1]), hexValue(text[i+2])
			if hi >= 0 && lo >= 0 {
				b.WriteByte(byte(hi<<4 | lo))
				i += 2
				continue
			}
		}
		b.WriteByte(text[i])
	}
	return b.String()
}

// PositionToOffset converts a zero-based LSP line and UTF-16 character into a
// byte offset.
func PositionToOffset(text string, line, character int) int {
	if line < 0 || character < 0 {
		return 0
	}
	offset, current := 0, 0
	for offset < len(text) && current < line {
		if text[offset] == '\n' {
			current++
		}
		offset++
	}
	if current != line {
		return len(text)
	}
	units := 0
	for offset < len(text) && text[offset] != '\n' && units < character {
		lead := text[offset]
		count, codePoint := 1, uint32(lead)
		switch {
		case lead&0xE0 == 0xC0 && offset+1 < len(text):
			count = 2
			codePoint = uint32(lead&0x1F)<<6 | uint32(text[offset+1]&0x3F)
		case lead&0xF0 == 0xE0 && offset+2 < len(text):
			count = 3
			codePoint = uint32(lead&0x0F)<<12 | uint32(text[offset+1]&0x3F)<<6 | uint32(text[offset+2]&0x3F)
		case lead&0xF8 == 0xF0 && offset+3 < len(text):
			count = 4
			codePoint = uint32(lead&0x07)<<18 | uint32(text[offset+1]&0x3F)<<12 | uint32(text[offset+2]&0x3F)<<6 |
				uint32(text[offset+3]&0x3F)
		}
		width := 1
		if codePoint > 0xFFFF {
			width = 2
		}
		if units+width > character {
			break
		}
		units += width
		offset += count
	}
	return offset
}
