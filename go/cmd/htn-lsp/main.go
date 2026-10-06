// Command htn-lsp is the HTN language server: JSON-RPC over stdio for
// editors (diagnostics, go-to-definition, completion, htn/compile).
package main

import (
	"bufio"
	"os"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/lsp"
)

func main() {
	output := bufio.NewWriter(os.Stdout)
	server := lsp.NewServer(lsp.NewTransport(os.Stdin, output))
	server.Log = os.Stderr
	code := server.Run()
	output.Flush()
	os.Exit(code)
}
