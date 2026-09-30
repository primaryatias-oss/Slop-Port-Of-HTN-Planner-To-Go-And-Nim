package translator_test

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/translator"
)

var generatedExtension = regexp.MustCompile(`\.generated\.go\b`)

// portUsageLine documents the Go-specific --package option, which the
// original translator does not have.
const portUsageLine = "  --package=<go package name> (default: derived from the domain file name)\n"

// TestTranslatorGoldens replays testdata/translator/cases.txt and compares
// exit codes and outputs with the ones recorded from the original
// HTNTranslator.
func TestTranslatorGoldens(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	commands, err := os.Open(filepath.Join(root, "testdata", "translator", "cases.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer commands.Close()
	goldenBytes, err := os.ReadFile(filepath.Join(root, "testdata", "translator", "cases.golden"))
	if err != nil {
		t.Fatal(err)
	}
	golden := strings.Split(string(goldenBytes), "--- end\n")
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(working)
	out := t.TempDir()
	scanner := bufio.NewScanner(commands)
	index := 0
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		var args []string
		for _, field := range strings.Fields(line) {
			args = append(args, strings.ReplaceAll(field, "$OUT", out))
		}
		var stdout, stderr bytes.Buffer
		code := translator.Main(args, &stdout, &stderr)
		normalize := func(text string) string {
			text = strings.ReplaceAll(text, portUsageLine, "")
			return generatedExtension.ReplaceAllString(strings.ReplaceAll(text, out, "$OUT"), ".generated.<ext>")
		}
		actual := "case " + line + "\nexit " + strconv.Itoa(code) + "\n--- stdout\n" + normalize(stdout.String()) +
			"--- stderr\n" + normalize(stderr.String())
		if index >= len(golden) {
			t.Fatalf("golden file has fewer cases than cases.txt")
		}
		if actual != golden[index] {
			t.Errorf("mismatch for %q\n--- want\n%s\n--- got\n%s", line, golden[index], actual)
		}
		index++
	}
}
