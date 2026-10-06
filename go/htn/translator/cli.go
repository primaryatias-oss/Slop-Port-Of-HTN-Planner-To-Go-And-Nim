package translator

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/codegen"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/compiler"
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
		"Example: htn-translator Domains/Test/human.domain CreateHumanHTN Generated\n")
}

func printDiagnostics(w io.Writer, diagnostics []compiler.Diagnostic, fallback string) {
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
		fmt.Fprintf(w, "%s(%d,%d): %s: %s\n", file, line, column, d.Severity, d.Message)
	}
}

// Main runs the htn-translator command line and returns its exit code:
// 0 success, 1 usage error, 4 domain load/validation failure, 5 code
// generation or output failure.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 2 && args[0] == "--check" {
		var sink compiler.DiagnosticSink
		result, ok := compiler.Load(args[1], &sink, compiler.DefaultLoadOptions())
		printDiagnostics(stderr, sink.Diagnostics(), args[1])
		if !ok || sink.HasErrors() {
			fmt.Fprintf(stderr, "htn-translator: check failed for '%s' with %d error(s).\n", args[1], sink.ErrorCount())
			return 4
		}
		fmt.Fprintf(stdout, "htn-translator: '%s' compiled successfully (%d linked source file(s)).\n", args[1], len(result.SourceFiles))
		return 0
	}
	if len(args) < 2 {
		printUsage(stdout)
		return 1
	}
	request := NewRequest(args[0], args[1])
	outputSpecified := false
	for _, argument := range args[2:] {
		if strings.HasPrefix(argument, "-") {
			if err := request.ApplyOption(argument); err != nil {
				var optionError *OptionError
				switch {
				case errors.Is(err, ErrUnknownOption):
					fmt.Fprintf(stderr, "htn-translator: unknown option '%s'.\n", argument)
					printUsage(stdout)
				case errors.As(err, &optionError):
					fmt.Fprintf(stderr, "htn-translator: %s.\n", optionError.Message)
					if optionError.Usage {
						printUsage(stdout)
					}
				}
				return 1
			}
			continue
		}
		if outputSpecified {
			fmt.Fprintln(stderr, "htn-translator: more than one output directory was specified.")
			printUsage(stdout)
			return 1
		}
		request.OutputDirectory = argument
		outputSpecified = true
	}
	result := Translate(request)
	if !result.Succeeded {
		printDiagnostics(stderr, result.Diagnostics, request.DomainPath)
		fmt.Fprintf(stderr, "htn-translator: %s\n", result.ErrorMessage)
		if result.Failure == FailureDomainLoad {
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
	fmt.Fprintf(stdout, "Translated domain '%s' (%d linked source file(s)).\n", result.DomainID, result.LinkedSourceFileCount)
	fmt.Fprintf(stdout, "  Entry point: %s\n", request.EntryPointName)
	fmt.Fprintf(stdout, "  Backtracking: %s (capacity %d)\n", policy, request.BacktrackingCapacity)
	fmt.Fprintf(stdout, "  Call frames: fixed capacity %d\n", request.CallFrameCapacity)
	fmt.Fprintf(stdout, "  Runtime backtracking support: %s\n", support)
	fmt.Fprintf(stdout, "  Output: %s\n", result.OutputSourcePath)
	return 0
}
