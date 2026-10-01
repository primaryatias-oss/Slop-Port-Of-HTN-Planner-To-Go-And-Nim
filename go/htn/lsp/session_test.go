package lsp_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/lsp"
)

// percentEncodePath mirrors the server's URI encoding of the repository root.
func percentEncodePath(path string) string {
	var b strings.Builder
	for i := 0; i < len(path); i++ {
		c := path[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || strings.IndexByte("-_.~/:", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

var framePattern = regexp.MustCompile(`^Content-Length: (\d+)\r\n\r\n`)

// TestGoldenSessions replays testdata/lsp/*.session and compares every
// response with the ones recorded from the original HTNLanguageServer
// (tools/lsp/make_goldens.py).
func TestGoldenSessions(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)
	rootURI := "file://" + percentEncodePath(root)
	sessions, _ := filepath.Glob(filepath.Join(root, "testdata", "lsp", "*.session"))
	if len(sessions) == 0 {
		t.Fatal("no sessions found")
	}
	for _, sessionPath := range sessions {
		name := strings.TrimSuffix(filepath.Base(sessionPath), ".session")
		t.Run(name, func(t *testing.T) {
			session, err := os.ReadFile(sessionPath)
			if err != nil {
				t.Fatal(err)
			}
			var input bytes.Buffer
			for _, record := range strings.Split(string(session), "--- message")[1:] {
				header, payload, _ := strings.Cut(record, "\n")
				payload = strings.TrimSuffix(payload, "\n")
				payload = strings.ReplaceAll(strings.ReplaceAll(payload, "$ROOT_URI", rootURI), "$ROOT", root)
				if strings.TrimSpace(header) == "lowercase" {
					fmt.Fprintf(&input, "content-length: %d\r\nContent-Type: application/vscode-jsonrpc; charset=utf-8\r\n\r\n", len(payload))
				} else {
					fmt.Fprintf(&input, "Content-Length: %d\r\n\r\n", len(payload))
				}
				input.WriteString(payload)
			}
			var output, log bytes.Buffer
			server := lsp.NewServer(lsp.NewTransport(&input, &output))
			server.Log = &log
			code := server.Run()
			normalize := func(text string) string {
				return strings.ReplaceAll(strings.ReplaceAll(text, rootURI, "$ROOT_URI"), root, "$ROOT")
			}
			var actual strings.Builder
			rest := output.String()
			for rest != "" {
				match := framePattern.FindStringSubmatch(rest)
				if match == nil {
					t.Fatalf("unexpected output framing: %q", rest)
				}
				length, _ := strconv.Atoi(match[1])
				payload := rest[len(match[0]) : len(match[0])+length]
				actual.WriteString("--- message\n" + normalize(payload) + "\n")
				rest = rest[len(match[0])+length:]
			}
			actual.WriteString("--- stderr\n" + normalize(log.String()) + fmt.Sprintf("--- exit %d\n", code))
			golden, err := os.ReadFile(filepath.Join(root, "testdata", "lsp", name+".golden"))
			if err != nil {
				t.Fatal(err)
			}
			if actual.String() != string(golden) {
				want := strings.Split(string(golden), "\n")
				got := strings.Split(actual.String(), "\n")
				for i := 0; i < len(want) || i < len(got); i++ {
					var w, g string
					if i < len(want) {
						w = want[i]
					}
					if i < len(got) {
						g = got[i]
					}
					if w != g {
						t.Fatalf("line %d differs\nwant: %s\ngot:  %s", i+1, w, g)
					}
				}
			}
		})
	}
}
