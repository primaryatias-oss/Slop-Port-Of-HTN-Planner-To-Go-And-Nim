// Package lsp implements the HTN language server (port of
// HTNLanguageServer): JSON-RPC over stdio with Content-Length framing,
// full-document sync, diagnostics, go-to-definition, variable completion and
// the custom "htn/compile" request used by the editor's compile command.
//
// Responses are byte-identical to the original server's: the JSON library is
// a port of HTNLspJson and paths follow std::filesystem semantics.
package lsp

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/compiler"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/internal/fspath"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/tooling"
)

// ServerName is the name reported in the initialize response.
const ServerName = "HTNLanguageServer"

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
func (t *Transport) ReadMessage() (string, bool) {
	contentLength, hasLength := uint64(0), false
	for {
		line, err := t.reader.ReadString('\n')
		if err != nil && line == "" {
			break
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			break
		}
		const prefix = "content-length:"
		if len(line) >= len(prefix) && strings.EqualFold(line[:len(prefix)], prefix) {
			value := strings.TrimLeft(line[len(prefix):], " \t")
			if value == "" {
				return "", false
			}
			parsed, parseErr := strconv.ParseUint(value, 10, 64)
			if parseErr != nil || value[0] == '+' {
				return "", false
			}
			contentLength, hasLength = parsed, true
		}
		if err != nil {
			break
		}
	}
	if !hasLength {
		return "", false
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(t.reader, payload); err != nil {
		return "", false
	}
	return string(payload), true
}

// WriteMessage writes one framed JSON payload.
func (t *Transport) WriteMessage(payload string) {
	fmt.Fprintf(t.writer, "Content-Length: %d\r\n\r\n", len(payload))
	io.WriteString(t.writer, payload)
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

// Run processes messages until "exit" or end of input. It returns 0 when a
// shutdown was requested first, 1 otherwise.
func (s *Server) Run() int {
	for !s.exitRequested {
		payload, ok := s.transport.ReadMessage()
		if !ok {
			break
		}
		message, parseError, ok := ParseJSON(payload)
		if !ok {
			fmt.Fprintf(s.Log, "%s: invalid JSON-RPC payload: %s\n", ServerName, parseError)
			continue
		}
		s.handleMessage(message)
	}
	if s.shutdownRequested {
		return 0
	}
	return 1
}

func (s *Server) handleMessage(message JSON) {
	method, ok := message.Find("method")
	if !ok || !method.IsString() {
		return
	}
	params, hasParams := message.Find("params")
	if id, hasID := message.Find("id"); hasID {
		s.handleRequest(id, method.AsString(), params, hasParams)
	} else {
		s.handleNotification(method.AsString(), params, hasParams)
	}
}

func (s *Server) handleRequest(id JSON, method string, params JSON, hasParams bool) {
	switch method {
	case "initialize":
		s.sendResponse(id, Object(
			Field{"capabilities", Object(
				Field{"textDocumentSync", Integer(1)},
				Field{"definitionProvider", Bool(true)},
				Field{"completionProvider", Object(Field{"resolveProvider", Bool(false)})})},
			Field{"serverInfo", Object(Field{"name", String(ServerName)}, Field{"version", String("0.1.0")})}))
	case "textDocument/definition":
		if !hasParams {
			s.sendError(id, -32602, "Missing definition params")
			return
		}
		s.handleDefinition(id, params)
	case "textDocument/completion":
		if !hasParams {
			s.sendError(id, -32602, "Missing completion params")
			return
		}
		s.handleCompletion(id, params)
	case "htn/compile":
		if !hasParams {
			s.sendError(id, -32602, "Missing compile params")
			return
		}
		s.handleCompile(id, params)
	case "shutdown":
		s.shutdownRequested = true
		s.sendResponse(id, Null())
	default:
		s.sendError(id, -32601, "Method not found: "+method)
	}
}

func (s *Server) handleNotification(method string, params JSON, hasParams bool) {
	switch method {
	case "exit":
		s.exitRequested = true
		return
	case "initialized":
		return
	}
	if !hasParams {
		return
	}
	switch method {
	case "textDocument/didOpen":
		s.handleDidOpen(params)
	case "textDocument/didChange":
		s.handleDidChange(params)
	case "textDocument/didClose":
		s.handleDidClose(params)
	}
}

func documentVersion(params JSON) uint64 {
	version, ok := params.FindNested("textDocument", "version")
	if !ok || !version.IsInteger() || version.AsInteger() < 0 {
		return 0
	}
	return uint64(version.AsInteger())
}

func (s *Server) handleDidOpen(params JSON) {
	uri, hasURI := params.FindNested("textDocument", "uri")
	text, hasText := params.FindNested("textDocument", "text")
	if !hasURI || !hasText || !uri.IsString() || !text.IsString() {
		return
	}
	path := URIToPath(uri.AsString())
	s.documents.Open(path, text.AsString(), documentVersion(params))
	s.publishDiagnostics(uri.AsString(), path)
}

func (s *Server) handleDidChange(params JSON) {
	uri, hasURI := params.FindNested("textDocument", "uri")
	changes, hasChanges := params.Find("contentChanges")
	if !hasURI || !uri.IsString() || !hasChanges || !changes.IsArray() || len(changes.AsArray()) == 0 {
		return
	}
	// Full document sync: the first change carries the complete text.
	text, hasText := changes.AsArray()[0].Find("text")
	if !hasText || !text.IsString() {
		return
	}
	version := documentVersion(params)
	path := URIToPath(uri.AsString())
	if !s.documents.Update(path, text.AsString(), version) {
		s.documents.Open(path, text.AsString(), version)
	}
	s.publishDiagnostics(uri.AsString(), path)
}

func (s *Server) handleDidClose(params JSON) {
	uri, hasURI := params.FindNested("textDocument", "uri")
	if !hasURI || !uri.IsString() {
		return
	}
	s.documents.Close(URIToPath(uri.AsString()))
	s.sendNotification("textDocument/publishDiagnostics",
		Object(Field{"uri", uri}, Field{"diagnostics", Array()}))
}

func (s *Server) send(value JSON) { s.transport.WriteMessage(value.Serialize()) }

func (s *Server) sendResponse(id, result JSON) {
	s.send(Object(Field{"jsonrpc", String("2.0")}, Field{"id", id}, Field{"result", result}))
}

func (s *Server) sendError(id JSON, code int64, text string) {
	s.send(Object(Field{"jsonrpc", String("2.0")}, Field{"id", id},
		Field{"error", Object(Field{"code", Integer(code)}, Field{"message", String(text)})}))
}

func (s *Server) sendNotification(method string, params JSON) {
	s.send(Object(Field{"jsonrpc", String("2.0")}, Field{"method", String(method)}, Field{"params", params}))
}

// positionParams extracts the document URI and position of a request. The
// position is truncated to int like the original's static_cast<int>.
func positionParams(params JSON) (string, int, int, bool) {
	uri, hasURI := params.FindNested("textDocument", "uri")
	line, hasLine := params.FindNested("position", "line")
	character, hasCharacter := params.FindNested("position", "character")
	if !hasURI || !uri.IsString() || !hasLine || !line.IsInteger() || !hasCharacter || !character.IsInteger() {
		return "", 0, 0, false
	}
	return uri.AsString(), int(int32(line.AsInteger())), int(int32(character.AsInteger())), true
}

func makePosition(lineOneBased, columnOneBased int) JSON {
	return Object(Field{"line", Integer(int64(max(0, lineOneBased-1)))},
		Field{"character", Integer(int64(max(0, columnOneBased-1)))})
}

func (s *Server) handleDefinition(id, params JSON) {
	uri, line, character, ok := positionParams(params)
	if !ok {
		s.sendError(id, -32602, "Invalid definition params")
		return
	}
	path := URIToPath(uri)
	document := s.documents.Document(path)
	if document == nil {
		s.sendResponse(id, Null())
		return
	}
	offset := PositionToOffset(document.Text, line, character)
	if model := s.documents.Model(path); model != nil {
		if definition, found := model.DefinitionAt(offset); found {
			s.sendResponse(id, Object(
				Field{"uri", String(PathToURI(definition.FilePath))},
				Field{"range", Object(
					Field{"start", makePosition(definition.Range.Begin.Line, definition.Range.Begin.Column)},
					Field{"end", makePosition(definition.Range.End.Line, definition.Range.End.Column)})}))
			return
		}
	}
	s.sendResponse(id, Null())
}

func (s *Server) handleCompletion(id, params JSON) {
	uri, line, character, ok := positionParams(params)
	if !ok {
		s.sendError(id, -32602, "Invalid completion params")
		return
	}
	path := URIToPath(uri)
	document := s.documents.Document(path)
	items := Array()
	if document == nil {
		s.sendResponse(id, items)
		return
	}
	offset := PositionToOffset(document.Text, line, character)
	if model := s.documents.Model(path); model != nil {
		added := map[string]bool{}
		for _, candidate := range model.AutocompleteCandidates(offset) {
			if added[candidate] {
				continue
			}
			added[candidate] = true
			items.Append(Object(Field{"label", String(candidate)}, Field{"kind", Integer(6)},
				Field{"insertText", String(candidate)}))
		}
	}
	s.sendResponse(id, items)
}

func diagnosticFields(d compiler.Diagnostic, source string) JSON {
	beginLine := max(1, d.Range.Begin.Line)
	beginColumn := max(1, d.Range.Begin.Column)
	endLine := max(beginLine, d.Range.End.Line)
	endColumn := max(1, d.Range.End.Column)
	// Semantic/link diagnostics do not all carry exact ranges: keep them
	// visible with a one-character fallback range.
	if endLine == beginLine && endColumn <= beginColumn {
		endColumn = beginColumn + 1
	}
	severity := int64(1)
	switch d.Severity {
	case compiler.SeverityWarning:
		severity = 2
	case compiler.SeverityInfo:
		severity = 3
	}
	return Object(
		Field{"range", Object(Field{"start", makePosition(beginLine, beginColumn)},
			Field{"end", makePosition(endLine, endColumn)})},
		Field{"severity", Integer(severity)},
		Field{"source", String(source)},
		Field{"message", String(d.Message)})
}

func (s *Server) handleCompile(id, params JSON) {
	uri, hasURI := params.FindNested("textDocument", "uri")
	if !hasURI || !uri.IsString() {
		s.sendError(id, -32602, "Invalid compile params")
		return
	}
	path := URIToPath(uri.AsString())
	document := s.documents.Document(path)
	if document == nil {
		s.sendResponse(id, Object(Field{"success", Bool(false)},
			Field{"message", String("The active HTN document is not open in the language server.")},
			Field{"diagnostics", Array()}))
		return
	}
	// Compile exactly what the editor sees: open buffers win over disk.
	var sink compiler.DiagnosticSink
	result, ok := compiler.LoadFromSource(path, document.Text, s.documents.Read, &sink, compiler.DefaultLoadOptions())
	diagnostics := Array()
	for _, d := range sink.Diagnostics() {
		fields := diagnosticFields(d, "htn-compile")
		file := d.FilePath
		if file == "" {
			file = path
		}
		fields.Set("uri", String(PathToURI(file)))
		diagnostics.Append(fields)
	}
	if ok && !sink.HasErrors() {
		s.sendResponse(id, Object(Field{"success", Bool(true)},
			Field{"message", String(fmt.Sprintf("Compile succeeded: %s (%d linked source file(s))",
				result.Domain.ID, len(result.SourceFiles)))},
			Field{"diagnostics", diagnostics}))
		return
	}
	s.sendResponse(id, Object(Field{"success", Bool(false)},
		Field{"message", String(fmt.Sprintf("Compile failed with %d error(s).", sink.ErrorCount()))},
		Field{"diagnostics", diagnostics}))
}

func (s *Server) publishDiagnostics(uri, path string) {
	diagnostics := Array()
	if document := s.documents.Document(path); document != nil {
		var sink compiler.DiagnosticSink
		compiler.LoadFromSource(path, document.Text, s.documents.Read, &sink,
			compiler.LoadOptions{RequireTopLevelRoot: false})
		current := fspath.Key(path)
		for _, d := range sink.Diagnostics() {
			file := d.FilePath
			if file == "" {
				file = path
			}
			// didOpen/didChange publishes diagnostics of this document only;
			// htn/compile reports every linked file.
			if fspath.Key(file) != current {
				continue
			}
			diagnostics.Append(diagnosticFields(d, "htn"))
		}
	}
	s.sendNotification("textDocument/publishDiagnostics",
		Object(Field{"uri", String(uri)}, Field{"diagnostics", diagnostics}))
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
	if absolute, ok := fspath.Absolute(path); ok {
		path = absolute
	}
	return "file://" + percentEncodePath(path)
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
