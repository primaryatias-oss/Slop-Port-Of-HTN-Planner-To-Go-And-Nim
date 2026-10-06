// Package translator is the single translation entry point shared by the
// command-line translator and tools: it loads, links and validates a domain
// and writes the generated Go planner (HTNTranslateDomain).
package translator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/codegen"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/compiler"
)

// Request holds every translation input.
type Request struct {
	DomainPath      string
	OutputDirectory string
	EntryPointName  string
	// PackageName of the generated Go file; defaults to the domain file stem.
	PackageName                string
	BacktrackingPolicy         codegen.BacktrackingPolicy
	RuntimeBacktrackingSupport bool
	BacktrackingCapacity       uint32
	CallFrameCapacity          uint32
	// ModulePath overrides the import path of the htn packages.
	ModulePath string
}

// NewRequest returns a request with the translator defaults.
func NewRequest(domainPath, entryPoint string) Request {
	return Request{DomainPath: domainPath, EntryPointName: entryPoint, BacktrackingCapacity: 32, CallFrameCapacity: 8192}
}

// ErrUnknownOption is returned by ApplyOption for arguments that are not
// translator options.
var ErrUnknownOption = errors.New("unknown option")

// OptionError is an invalid option value. Usage reports whether the command
// line usage should be printed with it.
type OptionError struct {
	Message string
	Usage   bool
}

func (e *OptionError) Error() string { return e.Message }

func parsePositive(text string) (uint32, bool) {
	value, err := strconv.ParseUint(text, 10, 32)
	if err != nil || value == 0 {
		return 0, false
	}
	return uint32(value), true
}

// ApplyOption applies one "--name=value" translator option to the request.
func (r *Request) ApplyOption(argument string) error {
	switch {
	case strings.HasPrefix(argument, "--backtracking-policy="):
		value := strings.TrimPrefix(argument, "--backtracking-policy=")
		switch value {
		case "fixed-with-overflow":
			r.BacktrackingPolicy = codegen.FixedWithOverflow
		case "fixed-capacity":
			r.BacktrackingPolicy = codegen.FixedCapacity
		default:
			return &OptionError{Message: fmt.Sprintf("unknown backtracking policy '%s'", value), Usage: true}
		}
	case strings.HasPrefix(argument, "--call-frame-capacity="):
		value, ok := parsePositive(strings.TrimPrefix(argument, "--call-frame-capacity="))
		if !ok {
			return &OptionError{Message: "call frame capacity must be a positive integer"}
		}
		r.CallFrameCapacity = value
	case strings.HasPrefix(argument, "--backtracking-capacity="):
		value, ok := parsePositive(strings.TrimPrefix(argument, "--backtracking-capacity="))
		if !ok {
			return &OptionError{Message: "backtracking capacity must be a positive integer"}
		}
		r.BacktrackingCapacity = value
	case strings.HasPrefix(argument, "--runtime-backtracking-support="):
		value := strings.TrimPrefix(argument, "--runtime-backtracking-support=")
		switch value {
		case "disabled":
			r.RuntimeBacktrackingSupport = false
		case "enabled":
			r.RuntimeBacktrackingSupport = true
		default:
			return &OptionError{Message: fmt.Sprintf("unknown runtime backtracking support mode '%s'", value), Usage: true}
		}
	case strings.HasPrefix(argument, "--package="):
		r.PackageName = strings.TrimPrefix(argument, "--package=")
	default:
		return ErrUnknownOption
	}
	return nil
}

// Failure identifies the stage that stopped a translation.
type Failure uint8

const (
	FailureNone Failure = iota
	FailureInvalidOptions
	FailureDomainLoad
	FailureCodeGeneration
)

// Result describes a translation.
type Result struct {
	Succeeded             bool
	Failure               Failure
	ErrorMessage          string
	Diagnostics           []compiler.Diagnostic
	DomainID              string
	LinkedSourceFileCount int
	OutputSourcePath      string
	Source                string
}

// PortableDomainPath shortens a path to its "Domains/..." suffix (or file
// name) so generated code does not embed machine-specific paths.
func PortableDomainPath(path string) string {
	generic := filepath.ToSlash(path)
	if index := strings.Index(generic, "Domains/"); index >= 0 {
		return generic[index:]
	}
	return filepath.Base(generic)
}

// PackageNameFor derives a Go package name from a domain file path.
func PackageNameFor(domainPath string) string {
	stem := strings.TrimSuffix(filepath.Base(domainPath), filepath.Ext(domainPath))
	var b strings.Builder
	for i := 0; i < len(stem); i++ {
		c := stem[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
			b.WriteByte(c)
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + 'a' - 'A')
		default:
			b.WriteByte('_')
		}
	}
	name := b.String()
	if name == "" || (name[0] >= '0' && name[0] <= '9') {
		name = "domain_" + name
	}
	return name
}

// Generate loads a domain and produces the generated Go source without
// writing it.
func Generate(request Request) Result {
	var result Result
	if request.DomainPath == "" {
		result.Failure = FailureInvalidOptions
		result.ErrorMessage = "A domain file must be specified."
		return result
	}
	if request.EntryPointName == "" {
		result.Failure = FailureInvalidOptions
		result.ErrorMessage = "An entry point name must be specified."
		return result
	}
	if request.BacktrackingPolicy != codegen.FixedWithOverflow && request.BacktrackingPolicy != codegen.FixedCapacity {
		result.Failure = FailureInvalidOptions
		result.ErrorMessage = "Unknown backtracking policy."
		return result
	}
	if request.CallFrameCapacity == 0 {
		result.Failure = FailureInvalidOptions
		result.ErrorMessage = "Call frame capacity must be greater than zero."
		return result
	}
	if request.BacktrackingCapacity == 0 {
		result.Failure = FailureInvalidOptions
		result.ErrorMessage = "Backtracking capacity must be greater than zero."
		return result
	}
	var sink compiler.DiagnosticSink
	loaded, ok := compiler.Load(request.DomainPath, &sink, compiler.DefaultLoadOptions())
	result.Diagnostics = sink.Diagnostics()
	if !ok || sink.HasErrors() {
		result.Failure = FailureDomainLoad
		result.ErrorMessage = "Loading/linking failed for '" + request.DomainPath + "'."
		return result
	}
	options := codegen.DefaultOptions()
	options.EntryPointName = request.EntryPointName
	options.PackageName = request.PackageName
	if options.PackageName == "" {
		options.PackageName = PackageNameFor(request.DomainPath)
	}
	options.SourceFilePath = PortableDomainPath(request.DomainPath)
	options.BacktrackingPolicy = request.BacktrackingPolicy
	options.RuntimeBacktrackingSupport = request.RuntimeBacktrackingSupport
	options.BacktrackingCapacity = request.BacktrackingCapacity
	options.CallFrameCapacity = request.CallFrameCapacity
	if request.ModulePath != "" {
		options.ModulePath = request.ModulePath
	}
	for _, file := range loaded.SourceFiles {
		options.LinkedSourceFiles = append(options.LinkedSourceFiles, PortableDomainPath(file))
	}
	source, err := codegen.Generate(loaded.Domain, options)
	if err != nil {
		result.Failure = FailureCodeGeneration
		result.ErrorMessage = err.Error()
		return result
	}
	result.Succeeded = true
	result.DomainID = loaded.Domain.ID
	result.LinkedSourceFileCount = len(loaded.SourceFiles)
	result.Source = source
	return result
}

// Translate loads a domain and writes <stem>.generated.go into the output
// directory (the domain's directory when empty).
func Translate(request Request) Result {
	result := Generate(request)
	if !result.Succeeded {
		return result
	}
	directory := request.OutputDirectory
	if directory == "" {
		directory = filepath.Dir(request.DomainPath)
	}
	stem := strings.TrimSuffix(filepath.Base(request.DomainPath), filepath.Ext(request.DomainPath))
	result.OutputSourcePath = filepath.Join(directory, stem+".generated.go")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		result.Succeeded = false
		result.Failure = FailureCodeGeneration
		result.ErrorMessage = "Could not create output directory: " + err.Error()
		return result
	}
	if err := os.WriteFile(result.OutputSourcePath, []byte(result.Source), 0o644); err != nil {
		result.Succeeded = false
		result.Failure = FailureCodeGeneration
		result.ErrorMessage = "Could not write output file: " + result.OutputSourcePath
		return result
	}
	return result
}
