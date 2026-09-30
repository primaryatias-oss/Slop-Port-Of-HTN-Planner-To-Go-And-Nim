// Command htn-check validates domains (temporary development helper).
package main

import (
	"fmt"
	"os"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/compiler"
)

func main() {
	status := 0
	for _, path := range os.Args[1:] {
		var sink compiler.DiagnosticSink
		result, ok := compiler.Load(path, &sink, compiler.DefaultLoadOptions())
		for _, d := range sink.Diagnostics() {
			fmt.Printf("%s(%d,%d): %s: %s\n", d.FilePath, d.Range.Begin.Line, d.Range.Begin.Column, d.Severity, d.Message)
		}
		if ok {
			fmt.Printf("OK %s (%d files) axioms=%d methods=%d\n", path, len(result.SourceFiles), len(result.Domain.Axioms), len(result.Domain.Methods))
		} else {
			fmt.Printf("FAIL %s\n", path)
			status = 1
		}
	}
	os.Exit(status)
}
