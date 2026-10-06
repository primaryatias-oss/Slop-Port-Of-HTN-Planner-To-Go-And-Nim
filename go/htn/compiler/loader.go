package compiler

import (
	"path/filepath"
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/internal/sourcefile"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/lexer"
)

// SourceProvider supplies source text for a path (for example unsaved editor
// buffers). Returning false falls back to reading the file system.
type SourceProvider func(path string) (string, bool)

// LoadOptions configures domain loading.
type LoadOptions struct {
	// RequireTopLevelRoot demands a top_level_domain root with at least one
	// top_level_method (the translator default).
	RequireTopLevelRoot bool
}

// DefaultLoadOptions returns the translator defaults.
func DefaultLoadOptions() LoadOptions { return LoadOptions{RequireTopLevelRoot: true} }

// LoadResult is a linked, validated domain.
type LoadResult struct {
	Domain           *Domain
	LinkedSourceText string
	SourceFiles      []string
}

// canonicalKey mirrors std::filesystem::weakly_canonical for visit tracking.
func canonicalKey(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// parentPath mirrors std::filesystem::path::parent_path for '/' separators.
func parentPath(path string) string {
	index := strings.LastIndexByte(path, '/')
	if index < 0 {
		return ""
	}
	if index == 0 {
		return "/"
	}
	return path[:index]
}

// joinIncludePath resolves an include relative to its including file without
// normalizing the result (std::filesystem operator/ semantics).
func joinIncludePath(including, include string) string {
	if filepath.IsAbs(include) {
		return include
	}
	parent := parentPath(including)
	if parent == "" {
		return include
	}
	if strings.HasSuffix(parent, "/") {
		return parent + include
	}
	return parent + "/" + include
}

type loaderContext struct {
	rootText    *string
	provider    SourceProvider
	diagnostics *DiagnosticSink
	stack       []string
	visited     map[string]bool
	files       []string
	texts       []string
	linked      strings.Builder
}

func (c *loaderContext) visit(path string, isRoot bool, includingFile string, includeRange lexer.Range) (string, bool) {
	key := canonicalKey(path)
	if c.visited[key] {
		return "", true
	}
	for i, entry := range c.stack {
		if entry == key {
			message := "Circular domain dependency: "
			for _, item := range c.stack[i:] {
				message += item + " -> "
			}
			message += key
			reportFile := includingFile
			if reportFile == "" {
				reportFile = path
			}
			c.diagnostics.Error(reportFile, message, RecoveryFatal, includeRange)
			return message, false
		}
	}
	var text string
	found := false
	if isRoot && c.rootText != nil {
		text = *c.rootText
		found = true
	} else if c.provider != nil {
		text, found = c.provider(path)
	}
	if !found {
		if text, found = sourcefile.Read(path); !found {
			message := "Could not read included domain '" + path + "'"
			reportFile := includingFile
			if reportFile == "" {
				reportFile = path
			}
			c.diagnostics.Error(reportFile, message, RecoveryFatal, includeRange)
			return message, false
		}
	}
	includes, domainText, fileError := SplitDomainFile(text)
	if fileError.HasError() {
		c.diagnostics.Error(path, fileError.Message, RecoveryFatal, fileError.Range)
		return fileError.Message, false
	}
	parsed := ParseDomainSyntax(domainText, 0, c.diagnostics, path)
	if !parsed.OK {
		if !c.diagnostics.HasErrors() {
			c.diagnostics.Error(path, parsed.Error, RecoveryFatal, parsed.ErrorRange)
		}
		return parsed.Error, false
	}
	if !isRoot && parsed.Domain.IsTopLevel {
		message := "Included domain '" + path + "' cannot be top_level_domain"
		c.diagnostics.Error(path, message, RecoveryRecoverable, parsed.Domain.Range)
		return message, false
	}
	c.stack = append(c.stack, key)
	for _, include := range includes {
		child := joinIncludePath(path, include.Path)
		if message, ok := c.visit(child, false, path, include.Range); !ok {
			return message, false
		}
	}
	c.stack = c.stack[:len(c.stack)-1]
	c.files = append(c.files, path)
	c.texts = append(c.texts, domainText)
	c.linked.WriteString("\n// ---- linked source: " + path + " ----\n" + domainText + "\n")
	c.visited[key] = true
	return "", true
}

func buildLinkedDomain(c *loaderContext, requireTopLevelRoot bool) (*LoadResult, string, bool) {
	modules := make([]*Domain, 0, len(c.files))
	for index, file := range c.files {
		parsed := ParseDomainSyntax(c.texts[index], uint32(index), c.diagnostics, file)
		if !parsed.OK {
			if !c.diagnostics.HasErrors() {
				c.diagnostics.Error(file, parsed.Error, RecoveryFatal, parsed.ErrorRange)
			}
			return nil, parsed.Error, false
		}
		modules = append(modules, parsed.Domain)
	}
	if len(modules) == 0 {
		return nil, "Compiler domain has no source modules", false
	}
	if !ValidateDomainModules(modules, c.files, requireTopLevelRoot, c.diagnostics) {
		return nil, "Compiler syntax validation failed", false
	}
	// Build the effective declarations in post-order: later modules replace
	// earlier declarations with the same name/arity.
	effectiveConstants := map[string]*Constant{}
	effectiveAxioms := map[string]*Axiom{}
	effectiveMethods := map[string]*Method{}
	for _, module := range modules {
		for _, group := range module.ConstantGroups {
			for _, constant := range group.Constants {
				effectiveConstants[constant.ID] = constant
			}
		}
		for _, axiom := range module.Axioms {
			effectiveAxioms[CallableSignature(axiom.ID, len(axiom.Parameters))] = axiom
		}
		for _, method := range module.Methods {
			effectiveMethods[CallableSignature(method.ID, len(method.Parameters))] = method
		}
	}
	domain := &Domain{ID: modules[len(modules)-1].ID}
	for _, module := range modules {
		for _, group := range module.ConstantGroups {
			effectiveGroup := *group
			effectiveGroup.Constants = nil
			for _, constant := range group.Constants {
				if effectiveConstants[constant.ID] == constant {
					effectiveGroup.Constants = append(effectiveGroup.Constants, constant)
				}
			}
			if len(effectiveGroup.Constants) > 0 {
				domain.ConstantGroups = append(domain.ConstantGroups, &effectiveGroup)
			}
		}
		for _, axiom := range module.Axioms {
			qualified := *axiom
			qualified.ID = module.ID + "::" + axiom.ID
			domain.Axioms = append(domain.Axioms, &qualified)
		}
		for _, method := range module.Methods {
			qualified := *method
			qualified.ID = module.ID + "::" + method.ID
			qualified.TopLevel = false
			domain.Methods = append(domain.Methods, &qualified)
		}
	}
	for _, module := range modules {
		for _, axiom := range module.Axioms {
			if effectiveAxioms[CallableSignature(axiom.ID, len(axiom.Parameters))] == axiom {
				domain.Axioms = append(domain.Axioms, axiom)
			}
		}
	}
	for _, module := range modules {
		for _, method := range module.Methods {
			if effectiveMethods[CallableSignature(method.ID, len(method.Parameters))] == method {
				domain.Methods = append(domain.Methods, method)
			}
		}
	}
	return &LoadResult{Domain: domain, LinkedSourceText: c.linked.String(), SourceFiles: c.files}, "", true
}

// Load reads, links and validates the domain rooted at rootPath. Diagnostics
// are cleared first and receive every reported problem.
func Load(rootPath string, diagnostics *DiagnosticSink, options LoadOptions) (*LoadResult, bool) {
	return load(rootPath, nil, nil, diagnostics, options)
}

// LoadFromSource is Load with in-memory root text and an optional provider
// for included files.
func LoadFromSource(rootPath, rootText string, provider SourceProvider, diagnostics *DiagnosticSink,
	options LoadOptions) (*LoadResult, bool) {
	return load(rootPath, &rootText, provider, diagnostics, options)
}

func load(rootPath string, rootText *string, provider SourceProvider, diagnostics *DiagnosticSink,
	options LoadOptions) (*LoadResult, bool) {
	diagnostics.Clear()
	c := &loaderContext{rootText: rootText, provider: provider, diagnostics: diagnostics, visited: map[string]bool{}}
	message, ok := c.visit(rootPath, true, "", lexer.DefaultRange)
	if ok {
		var result *LoadResult
		result, message, ok = buildLinkedDomain(c, options.RequireTopLevelRoot)
		if ok {
			return result, true
		}
	}
	if !diagnostics.HasErrors() {
		diagnostics.Error(rootPath, message, RecoveryFatal, lexer.DefaultRange)
	}
	return nil, false
}
