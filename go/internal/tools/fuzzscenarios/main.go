// Command fuzzscenarios writes random scenario files for differential testing
// against the C++ oracle: random world states built from the facts and
// literals of each domain, random entry calls and backtracking modes.
//
//	go run ./internal/tools/fuzzscenarios -seed 1 -per-variant 4 > fuzz.scn
package main

import (
	"bufio"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/atom"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/compiler"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/worldstate"
)

type fact struct {
	name  string
	arity int
}

type entry struct {
	name     string
	arity    int
	deferred bool
}

type domainInfo struct {
	facts      []fact
	pool       []string
	entries    []entry
	positional map[fact][][]string
}

// worldRows indexes the rows of every world-state file of the repository by
// fact name and arity, rendered in world-state syntax.
var worldRows = map[fact][][]string{}

func loadWorldStates(root string) {
	_ = filepath.Walk(filepath.Join(root, "WorldStates"), func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(path, ".worldstate") {
			return nil
		}
		w := worldstate.New()
		if worldstate.ParseFile(w, path) != nil {
			return nil
		}
		for _, symbol := range w.Facts() {
			tables := w.FindTables(symbol)
			for arity := range tables {
				for _, row := range tables[arity].Rows() {
					rendered := make([]string, len(row))
					valid := true
					for i, value := range row {
						rendered[i] = renderValue(value)
						if rendered[i] == "" {
							valid = false
						}
					}
					if valid {
						key := fact{symbol.Text(), arity}
						worldRows[key] = append(worldRows[key], rendered)
					}
				}
			}
		}
		return nil
	})
}

func renderValue(a atom.Atom) string {
	if a.Is(atom.KindList) {
		parts := make([]string, 0, a.Len())
		for _, element := range a.Elements() {
			text := renderValue(element)
			if text == "" {
				return ""
			}
			parts = append(parts, text)
		}
		if len(parts) == 0 {
			return ""
		}
		return "(" + strings.Join(parts, " ") + ")"
	}
	return literalText(a)
}

func isIdentifier(text string) bool {
	if text == "" || text == "true" || text == "false" {
		return false
	}
	for i, c := range text {
		letter := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// literalText renders an atom in world-state syntax, or "" when the atom
// cannot be written (negative numbers, strings with quotes...).
func literalText(a atom.Atom) string {
	switch a.Kind() {
	case atom.KindBool:
		if a.Bool() {
			return "true"
		}
		return "false"
	case atom.KindInt:
		if a.Int() < 0 {
			return ""
		}
		return strconv.FormatInt(int64(a.Int()), 10)
	case atom.KindFloat:
		if a.Float() < 0 {
			return ""
		}
		text := strconv.FormatFloat(float64(a.Float()), 'f', -1, 32)
		if !strings.Contains(text, ".") {
			text += ".0"
		}
		return text
	case atom.KindString:
		if strings.ContainsAny(a.Str(), "\"\\\n") {
			return ""
		}
		return strconv.Quote(a.Str())
	case atom.KindSymbol:
		if !isIdentifier(a.Symbol().Text()) {
			return ""
		}
		return a.Symbol().Text()
	}
	return ""
}

func analyze(path string) (*domainInfo, error) {
	var sink compiler.DiagnosticSink
	loaded, ok := compiler.Load(path, &sink, compiler.DefaultLoadOptions())
	if !ok || sink.HasErrors() {
		return nil, fmt.Errorf("%s: load failed", path)
	}
	ir, message := compiler.BuildIR(loaded.Domain, loaded.SourceFiles, false)
	if ir == nil {
		return nil, fmt.Errorf("%s: %s", path, message)
	}
	info := &domainInfo{positional: map[fact][][]string{}}
	seenFacts := map[fact]bool{}
	for _, c := range ir.Conditions {
		if c.Kind != compiler.IRCondFact {
			continue
		}
		f := fact{ir.Strings.Get(c.ID), int(c.ArgumentCount)}
		if !isIdentifier(f.name) {
			continue
		}
		if !seenFacts[f] {
			seenFacts[f] = true
			info.facts = append(info.facts, f)
			info.positional[f] = make([][]string, f.arity)
		}
		for i := 0; i < f.arity; i++ {
			v := ir.Values[c.FirstArgument+uint32(i)]
			text := ""
			switch v.Kind {
			case compiler.ValueLiteral:
				text = literalText(v.Literal)
			case compiler.ValueIdentifier:
				if name := ir.Strings.Get(v.Text); isIdentifier(name) {
					text = name
				}
			}
			if text != "" {
				info.positional[f][i] = append(info.positional[f][i], text)
			}
		}
	}
	for f, rows := range worldRows {
		if !seenFacts[f] {
			continue
		}
		for _, row := range rows {
			for i, text := range row {
				info.positional[f][i] = append(info.positional[f][i], text)
			}
		}
	}
	seenValues := map[string]bool{}
	add := func(text string) {
		if text != "" && !seenValues[text] {
			seenValues[text] = true
			info.pool = append(info.pool, text)
		}
	}
	for _, v := range ir.Values {
		switch v.Kind {
		case compiler.ValueLiteral:
			add(literalText(v.Literal))
		case compiler.ValueIdentifier:
			if text := ir.Strings.Get(v.Text); isIdentifier(text) {
				add(text)
			}
		}
	}
	for _, s := range ir.StaticValues {
		add(literalText(s.Literal))
	}
	for i := 0; i <= 4; i++ {
		add(strconv.Itoa(i))
	}
	add("1.5")
	add("sym")
	add("\"text\"")
	for _, m := range ir.Methods {
		if !m.IsTopLevel && !m.IsExternallyDecomposable {
			continue
		}
		info.entries = append(info.entries, entry{ir.Strings.Get(m.ID), int(m.ParameterCount), !m.IsTopLevel})
	}
	sort.Slice(info.facts, func(i, j int) bool {
		if info.facts[i].name != info.facts[j].name {
			return info.facts[i].name < info.facts[j].name
		}
		return info.facts[i].arity < info.facts[j].arity
	})
	sort.Strings(info.pool)
	return info, nil
}

func randomValue(r *rand.Rand, pool []string, depth int) string {
	if depth < 2 && r.Intn(12) == 0 {
		n := 1 + r.Intn(3)
		parts := make([]string, n)
		for i := range parts {
			parts[i] = randomValue(r, pool, depth+1)
		}
		return "(" + strings.Join(parts, " ") + ")"
	}
	return pool[r.Intn(len(pool))]
}

func main() {
	seed := flag.Int64("seed", 1, "random seed")
	perVariant := flag.Int("per-variant", 3, "scenarios per planner variant")
	root := flag.String("root", "..", "repository root")
	only := flag.String("only", "", "comma-separated variant names (default: all)")
	flag.Parse()
	r := rand.New(rand.NewSource(*seed))
	loadWorldStates(*root)

	manifest, err := os.Open(filepath.Join(*root, "testdata", "planners.txt"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer manifest.Close()
	selected := map[string]bool{}
	for _, name := range strings.Split(*only, ",") {
		if name != "" {
			selected[name] = true
		}
	}
	cache := map[string]*domainInfo{}
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	fmt.Fprintf(out, "# Generated by fuzzscenarios -seed %d -per-variant %d\n", *seed, *perVariant)
	scanner := bufio.NewScanner(manifest)
	modes := []string{"none", "facts_and_axioms", "branches", "all"}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		variant, domainPath := fields[0], fields[1]
		if len(selected) != 0 && !selected[variant] {
			continue
		}
		info := cache[domainPath]
		if info == nil {
			info, err = analyze(filepath.Join(*root, domainPath))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			cache[domainPath] = info
		}
		runtime := strings.HasSuffix(variant, "RT")
		for i := 0; i < *perVariant; i++ {
			fmt.Fprintf(out, "\nscenario fuzz/%s/%d\n", variant, i)
			fmt.Fprintf(out, "planner %s\npolicy report\n", variant)
			for _, f := range info.facts {
				rows := r.Intn(4)
				if r.Intn(3) == 0 {
					rows = 0
				}
				for row := 0; row < rows; row++ {
					args := make([]string, f.arity)
					known := worldRows[f]
					if len(known) != 0 && r.Intn(3) == 0 {
						copy(args, known[r.Intn(len(known))])
					} else {
						for a := range args {
							if candidates := info.positional[f][a]; len(candidates) != 0 && r.Intn(5) != 0 {
								args[a] = candidates[r.Intn(len(candidates))]
							} else {
								args[a] = randomValue(r, info.pool, 0)
							}
						}
					}
					fmt.Fprintf(out, "fact %s\n", strings.TrimSpace(f.name+" "+strings.Join(args, " ")))
				}
			}
			calls := 1 + r.Intn(len(info.entries)+1)
			for c := 0; c < calls; c++ {
				e := info.entries[r.Intn(len(info.entries))]
				args := make([]string, e.arity)
				for a := range args {
					args[a] = randomValue(r, info.pool, 0)
				}
				call := strings.TrimSpace(e.name + " " + strings.Join(args, " "))
				if runtime {
					fmt.Fprintf(out, "mode %s\n", modes[r.Intn(len(modes))])
				}
				if r.Intn(6) == 0 && len(info.facts) != 0 {
					f := info.facts[r.Intn(len(info.facts))]
					fmt.Fprintf(out, "remove_fact %s %d 0\n", f.name, f.arity)
				}
				switch {
				case e.deferred:
					fmt.Fprintf(out, "rawdeferred %s\n", call)
				case r.Intn(4) == 0:
					fmt.Fprintf(out, "raw %s\n", call)
				default:
					fmt.Fprintf(out, "call %s\nresolve\n", call)
				}
			}
			fmt.Fprintln(out, "end")
		}
	}
}
