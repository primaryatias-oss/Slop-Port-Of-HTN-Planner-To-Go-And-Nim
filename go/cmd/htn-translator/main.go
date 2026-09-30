// Command htn-translator validates HTN domains and translates them into
// native Go planners.
//
//	htn-translator <domain-file> <entry-point> [output-directory] [options]
//	htn-translator --check <domain-file>
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/codegen"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/compiler"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/translator"
)

func printUsage(w io.Writer) {
	fmt.Fprint(w, "htn-translator <domain-file> <entry-point> [output-directory] [options]\n"+
		"htn-translator --check <domain-file>\n"+
		"Options:\n"+
		"  --backtracking-policy=fixed-with-overflow|fixed-capacity (default: fixed-with-overflow)\n"+
		"  --backtracking-capacity=<positive integer> (default: 32)\n"+
		"  --call-frame-capacity=<positive integer> (default: 8192)\n"+
		"  --runtime-backtracking-support=disabled|enabled (default: disabled)\n"+
		"  --package=<go package name> (default: derived from the domain file name)\n"+
		"Example: htn-translator Domains/Test/human.domain CreateHumanHTN generated\n")
}

func printDiagnostics(diagnostics []compiler.Diagnostic, fallback string) {
	for _, d := range diagnostics {
		file := d.FilePath
		if file == "" {
			file = fallback
		}
		line, column := d.Range.Begin.Line, d.Range.Begin.Column
		if line < 1 {
			line = 1
		}
		if column < 1 {
			column = 1
		}
		fmt.Fprintf(os.Stderr, "%s(%d,%d): %s: %s\n", file, line, column, d.Severity, d.Message)
	}
}

func run(args []string) int {
	if len(args) == 2 && args[0] == "--check" {
		var sink compiler.DiagnosticSink
		result, ok := compiler.Load(args[1], &sink, compiler.DefaultLoadOptions())
		printDiagnostics(sink.Diagnostics(), args[1])
		if !ok || sink.HasErrors() {
			fmt.Fprintf(os.Stderr, "htn-translator: check failed for '%s' with %d error(s).\n", args[1], sink.ErrorCount())
			return 4
		}
		fmt.Printf("htn-translator: '%s' compiled successfully (%d linked source file(s)).\n", args[1], len(result.SourceFiles))
		return 0
	}
	if len(args) < 2 {
		printUsage(os.Stdout)
		return 1
	}
	request := translator.NewRequest(args[0], args[1])
	outputSpecified := false
	for _, argument := range args[2:] {
		if strings.HasPrefix(argument, "-") {
			if err := request.ApplyOption(argument); err != nil {
				var optionError *translator.OptionError
				switch {
				case errors.Is(err, translator.ErrUnknownOption):
					fmt.Fprintf(os.Stderr, "htn-translator: unknown option '%s'.\n", argument)
					printUsage(os.Stdout)
				case errors.As(err, &optionError):
					fmt.Fprintf(os.Stderr, "htn-translator: %s.\n", optionError.Message)
					if optionError.Usage {
						printUsage(os.Stdout)
					}
				}
				return 1
			}
			continue
		}
		if outputSpecified {
			fmt.Fprintln(os.Stderr, "htn-translator: more than one output directory was specified.")
			printUsage(os.Stdout)
			return 1
		}
		request.OutputDirectory = argument
		outputSpecified = true
	}
	result := translator.Translate(request)
	if !result.Succeeded {
		printDiagnostics(result.Diagnostics, request.DomainPath)
		fmt.Fprintf(os.Stderr, "htn-translator: %s\n", result.ErrorMessage)
		if result.Failure == translator.FailureDomainLoad {
			return 4
		}
		return 5
	}
	policy := "fixed-with-overflow"
	if request.BacktrackingPolicy == codegen.FixedCapacity {
		policy = "fixed-capacity"
	}
	support := "disabled"
	if request.RuntimeBacktrackingSupport {
		support = "enabled"
	}
	fmt.Printf("Translated domain '%s' (%d linked source file(s)).\n", result.DomainID, result.LinkedSourceFileCount)
	fmt.Printf("  Entry point: %s\n", request.EntryPointName)
	fmt.Printf("  Backtracking: %s (capacity %d)\n", policy, request.BacktrackingCapacity)
	fmt.Printf("  Call frames: fixed capacity %d\n", request.CallFrameCapacity)
	fmt.Printf("  Runtime backtracking support: %s\n", support)
	fmt.Printf("  Output: %s\n", result.OutputSourcePath)
	return 0
}

func main() {
	os.Exit(run(os.Args[1:]))
}
