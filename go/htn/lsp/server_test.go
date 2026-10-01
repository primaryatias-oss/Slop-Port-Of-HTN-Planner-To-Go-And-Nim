package lsp_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/lsp"
)

func frame(messages ...any) *bytes.Buffer {
	var input bytes.Buffer
	for _, m := range messages {
		payload, _ := json.Marshal(m)
		fmt.Fprintf(&input, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
	}
	return &input
}

func readAll(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()
	transport := lsp.NewTransport(output, nil)
	var messages []map[string]any
	for {
		payload, ok := transport.ReadMessage()
		if !ok {
			return messages
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, m)
	}
}

func TestLanguageServerSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.domain")
	uri := lsp.PathToURI(path)
	valid := "(:domain Session top_level_domain\n" +
		" (:constants (speed 3))\n" +
		" (:method (run) top_level_method (b (and (enemy ?enemy)) ((work ?enemy) (!move @speed))))\n" +
		" (:method (work ?inp_target) (b () ((!attack ?inp_target))))\n)"
	invalid := strings.Replace(valid, "(!attack ?inp_target)", "(!attack ?inp_target ?unknown)", 1)
	line2 := strings.Split(valid, "\n")[2]
	workColumn := strings.Index(line2, "(work") + 1
	moveColumn := strings.Index(line2, "(!move")
	input := frame(
		map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}},
		map[string]any{"jsonrpc": "2.0", "method": "initialized", "params": map[string]any{}},
		map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
			"textDocument": map[string]any{"uri": uri, "languageId": "htn", "version": 1, "text": valid}}},
		map[string]any{"jsonrpc": "2.0", "id": 2, "method": "textDocument/definition", "params": map[string]any{
			"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": 2, "character": workColumn}}},
		map[string]any{"jsonrpc": "2.0", "id": 3, "method": "textDocument/completion", "params": map[string]any{
			"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": 2, "character": moveColumn}}},
		map[string]any{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": 2},
			"contentChanges": []any{map[string]any{"text": invalid}}}},
		map[string]any{"jsonrpc": "2.0", "id": 4, "method": "htn/compile", "params": map[string]any{
			"textDocument": map[string]any{"uri": uri}}},
		map[string]any{"jsonrpc": "2.0", "id": 5, "method": "unknown/method"},
		map[string]any{"jsonrpc": "2.0", "method": "textDocument/didClose", "params": map[string]any{
			"textDocument": map[string]any{"uri": uri}}},
		map[string]any{"jsonrpc": "2.0", "id": 6, "method": "shutdown"},
		map[string]any{"jsonrpc": "2.0", "method": "exit"},
	)
	var output bytes.Buffer
	server := lsp.NewServer(lsp.NewTransport(input, &output))
	if code := server.Run(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	messages := readAll(t, &output)
	if len(messages) != 9 {
		t.Fatalf("got %d messages: %v", len(messages), messages)
	}
	capabilities := messages[0]["result"].(map[string]any)["capabilities"].(map[string]any)
	if capabilities["definitionProvider"] != true {
		t.Errorf("capabilities = %v", capabilities)
	}
	if diagnostics := messages[1]["params"].(map[string]any)["diagnostics"].([]any); len(diagnostics) != 0 {
		t.Errorf("unexpected diagnostics = %v", diagnostics)
	}
	definition, _ := messages[2]["result"].(map[string]any)
	if definition == nil || definition["range"].(map[string]any)["start"].(map[string]any)["line"] != float64(3) {
		t.Errorf("definition = %v", messages[2])
	}
	completion := messages[3]["result"].([]any)
	if len(completion) != 1 || completion[0].(map[string]any)["label"] != "?enemy" {
		t.Errorf("completion = %v", completion)
	}
	diagnostics := messages[4]["params"].(map[string]any)["diagnostics"].([]any)
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].(map[string]any)["message"].(string), "unknown") {
		t.Errorf("diagnostics after change = %v", diagnostics)
	}
	compile := messages[5]["result"].(map[string]any)
	if compile["success"] != false || !strings.Contains(compile["message"].(string), "1 error") {
		t.Errorf("compile = %v", compile)
	}
	if messages[6]["error"].(map[string]any)["code"] != float64(-32601) {
		t.Errorf("unknown method = %v", messages[6])
	}
	if diagnostics := messages[7]["params"].(map[string]any)["diagnostics"].([]any); len(diagnostics) != 0 {
		t.Errorf("close diagnostics = %v", diagnostics)
	}
}

func TestPositionToOffsetCountsUTF16Units(t *testing.T) {
	text := "a\né\U0001F600x"
	if offset := lsp.PositionToOffset(text, 1, 3); offset != 2+2+4 {
		t.Fatalf("offset = %d", offset)
	}
	if offset := lsp.PositionToOffset(text, 5, 0); offset != len(text) {
		t.Fatalf("offset past end = %d", offset)
	}
}
