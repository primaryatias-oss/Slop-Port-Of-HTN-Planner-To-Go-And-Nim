// Command listmethods prints the entry methods (top-level and externally
// decomposable) of domains: "<domain> <method> <top|deferred> <param>...".
package main

import (
	"fmt"
	"os"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/compiler"
)

func main() {
	for _, path := range os.Args[1:] {
		var sink compiler.DiagnosticSink
		loaded, ok := compiler.Load(path, &sink, compiler.DefaultLoadOptions())
		if !ok || sink.HasErrors() {
			fmt.Fprintf(os.Stderr, "%s: load failed\n", path)
			continue
		}
		ir, message := compiler.BuildIR(loaded.Domain, loaded.SourceFiles, false)
		if ir == nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", path, message)
			continue
		}
		for _, m := range ir.Methods {
			if !m.IsTopLevel && !m.IsExternallyDecomposable {
				continue
			}
			kind := "top"
			if !m.IsTopLevel {
				kind = "deferred"
			}
			line := fmt.Sprintf("%s %s %s", path, ir.Strings.Get(m.ID), kind)
			for i := uint32(0); i < m.ParameterCount; i++ {
				line += " " + ir.Strings.Get(ir.Values[m.FirstParameter+i].Text)
			}
			fmt.Println(line)
		}
	}
}
