// Command htn-demo is the terminal port of HTNDemo: the Domain Runner over
// the demo domains and the NPC simulation of Wanderer agents.
//
//	htn-demo list
//	htn-demo run <domain> [method] [--worldstate=PATH] [--backtracking=none|facts|branches|all] [--rt]
//	htn-demo simulate [--agents=8] [--speed=1] [--fps=30] [--seconds=N] [--ascii] [--rt]
//	htn-demo trace runner [--rt]
//	htn-demo trace simulate [--agents=8] [--steps=3600] [--snapshot-every=300] [--rt]
//
// --rt selects the planners generated with runtime backtracking support (the
// only ones on which the backtracking mode has an effect). "trace" prints the
// deterministic output of the original demo's headless driver
// (tools/oracle/demo_oracle.cpp) used by the tests.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/planner"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/internal/demo"
)

const usage = `usage:
  htn-demo list
  htn-demo run <domain> [method] [--worldstate=PATH] [--backtracking=none|facts|branches|all] [--rt]
  htn-demo simulate [--agents=8] [--speed=1] [--fps=30] [--seconds=N] [--ascii] [--rt]
  htn-demo trace runner [--rt]
  htn-demo trace simulate [--agents=8] [--steps=3600] [--snapshot-every=300] [--rt]
`

// options holds parsed --name=value flags and positional arguments.
type options struct {
	positional []string
	values     map[string]string
}

func parseOptions(args []string) options {
	o := options{values: map[string]string{}}
	for _, argument := range args {
		if strings.HasPrefix(argument, "--") {
			name, value, _ := strings.Cut(argument[2:], "=")
			o.values[name] = value
		} else {
			o.positional = append(o.positional, argument)
		}
	}
	return o
}

func (o options) has(name string) bool { _, ok := o.values[name]; return ok }

func (o options) integer(name string, fallback int) int {
	if text, ok := o.values[name]; ok {
		if value, err := strconv.Atoi(text); err == nil {
			return value
		}
		fail("invalid --%s value %q", name, text)
	}
	return fallback
}

func (o options) number(name string, fallback float64) float64 {
	if text, ok := o.values[name]; ok {
		if value, err := strconv.ParseFloat(text, 64); err == nil {
			return value
		}
		fail("invalid --%s value %q", name, text)
	}
	return fallback
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "htn-demo: "+format+"\n", args...)
	os.Exit(2)
}

// repositoryRoot finds the directory holding WorldStates from the current
// directory upwards (or --root).
func repositoryRoot(o options) string {
	if root := o.values["root"]; root != "" {
		return root
	}
	probe, _ := os.Getwd()
	for depth := 0; depth < 8; depth++ {
		if info, err := os.Stat(filepath.Join(probe, "WorldStates")); err == nil && info.IsDir() {
			return probe
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	fail("cannot find the repository's WorldStates directory; run inside the repository or pass --root=DIR")
	return ""
}

func relative(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}

func list(o options) {
	root := repositoryRoot(o)
	worldStates := demo.FindWorldStates(root)
	fmt.Println("Domains (top-level methods; preferred world state):")
	for _, d := range demo.Domains {
		best := "-"
		if index := demo.BestWorldState(d, worldStates); index >= 0 {
			best = relative(root, worldStates[index])
		}
		fmt.Printf("  %-25s %s\n  %-25s   world state: %s\n", d.Name, strings.Join(d.Methods, ", "), "", best)
	}
	fmt.Println("\nWorld states:")
	for _, path := range worldStates {
		fmt.Printf("  %s\n", relative(root, path))
	}
}

func parseMode(text string) planner.BacktrackingMode {
	switch strings.ToLower(text) {
	case "", "all":
		return planner.BacktrackingAll
	case "none":
		return planner.BacktrackingNone
	case "facts", "facts-and-axioms", "facts_and_axioms":
		return planner.BacktrackingFactsAndAxioms
	case "branches":
		return planner.BacktrackingBranches
	}
	fail("unknown backtracking mode %q (none, facts, branches or all)", text)
	return planner.BacktrackingAll
}

func run(o options) {
	if len(o.positional) < 1 {
		fail("run needs a domain name (see htn-demo list)")
	}
	root := repositoryRoot(o)
	d, ok := demo.FindDomain(o.positional[0])
	if !ok {
		fail("unknown domain %q (see htn-demo list)", o.positional[0])
	}
	method := d.Methods[0]
	if len(o.positional) > 1 {
		method = o.positional[1]
	}
	worldState := o.values["worldstate"]
	if worldState == "" {
		worldStates := demo.FindWorldStates(root)
		index := demo.BestWorldState(d, worldStates)
		if index < 0 {
			fail("no world states found under %s", root)
		}
		worldState = worldStates[index]
	}
	mode := parseMode(o.values["backtracking"])
	runtimeBacktracking := o.has("rt")
	runner := demo.NewRunner(demo.CallTermErrorReporter(os.Stderr))
	definition := d.Definition(runtimeBacktracking)
	if definition == nil || !runner.Select(definition, method) {
		fail("could not load the generated planner of %s", d.Name)
	}
	if err := runner.Database.ParseWorldStateFile(worldState); err != nil {
		fail("%v", err)
	}
	variant := "generated"
	if runtimeBacktracking {
		variant = "generated with runtime backtracking support"
	}
	fmt.Printf("Domain:        %s (%s)\nMethod:        %s\nWorld state:   %s\nBacktracking:  %s\n",
		d.Name, variant, method, relative(root, worldState), demo.ModeName(mode))
	start := time.Now()
	status, steps := runner.Run(method, mode)
	elapsed := float64(time.Since(start).Nanoseconds()) / 1e6
	if status != planner.Succeeded {
		fmt.Printf("Result:        Failure (%s) in %.3f ms\n", status, elapsed)
		if status == planner.CallFrameCapacityExceeded {
			fmt.Println("Generated call-frame capacity exceeded. Regenerate this domain with a larger " +
				"--call-frame-capacity and rebuild it.")
		}
		os.Exit(1)
	}
	fmt.Printf("Result:        Success in %.3f ms\nPlan (%d step(s)):\n", elapsed, len(steps))
	for i, step := range steps {
		fmt.Printf("  %2d. %s\n", i+1, step)
	}
}

func trace(o options) {
	if len(o.positional) < 1 {
		fail("trace needs runner or simulate")
	}
	output := bufio.NewWriter(os.Stdout)
	defer output.Flush()
	switch o.positional[0] {
	case "runner":
		demo.RunnerTrace(output, repositoryRoot(o), o.has("rt"))
	case "simulate":
		demo.SimulationTrace(output, o.integer("agents", 8), o.integer("steps", 3600), o.integer("snapshot-every", 300),
			o.has("rt"))
	default:
		fail("unknown trace %q", o.positional[0])
	}
}

// renderer draws the simulation with ANSI escape sequences: two grid rows
// per text row with half blocks, or one character per cell with --ascii.
type renderer struct {
	ascii bool
}

var npcColors = []int{196, 46, 51, 201, 226, 208, 39, 129, 118, 214, 45, 207}

func npcSymbol(id uint32) byte {
	const symbols = "123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	return symbols[int(id-1)%len(symbols)]
}

func (r renderer) frame(w io.Writer, s *demo.Simulation, elapsed time.Duration) {
	t := s.Terrain
	occupant := make(map[demo.Cell]*demo.Agent)
	path := make(map[demo.Cell]bool)
	for _, agent := range s.Agents {
		occupant[agent.Wanderer().Location()] = agent
		if cells, ok := agent.NavigationPath(); ok {
			for _, cell := range cells {
				path[cell] = true
			}
		}
	}
	color := func(c demo.Cell) int {
		if agent := occupant[c]; agent != nil {
			return npcColors[int(agent.ID()-1)%len(npcColors)]
		}
		switch t.CellType(c) {
		case demo.CellBlocked:
			return 94
		case demo.CellInteractable:
			return 220
		}
		if path[c] {
			return 244
		}
		return 236
	}
	var b strings.Builder
	b.WriteString("\x1b[H")
	if r.ascii {
		for y := t.Height() - 1; y >= 0; y-- {
			for x := 0; x < t.Width(); x++ {
				c := demo.Cell{X: int32(x), Y: int32(y)}
				switch {
				case occupant[c] != nil:
					b.WriteByte(npcSymbol(occupant[c].ID()))
				case t.CellType(c) == demo.CellBlocked:
					b.WriteByte('#')
				case t.CellType(c) == demo.CellInteractable:
					b.WriteByte('o')
				case path[c]:
					b.WriteByte('+')
				default:
					b.WriteByte('.')
				}
			}
			b.WriteString("\x1b[K\n")
		}
	} else {
		for y := t.Height() - 1; y >= 0; y -= 2 {
			lastTop, lastBottom := -1, -1
			for x := 0; x < t.Width(); x++ {
				top := color(demo.Cell{X: int32(x), Y: int32(y)})
				bottom := 0
				if y > 0 {
					bottom = color(demo.Cell{X: int32(x), Y: int32(y - 1)})
				}
				// Only emit the colors that change.
				if top != lastTop {
					fmt.Fprintf(&b, "\x1b[38;5;%dm", top)
					lastTop = top
				}
				if bottom != lastBottom {
					fmt.Fprintf(&b, "\x1b[48;5;%dm", bottom)
					lastBottom = bottom
				}
				b.WriteString("▀")
			}
			b.WriteString("\x1b[0m\x1b[K\n")
		}
	}
	fmt.Fprintf(&b, "\nSimulated %.1f s (wall %.1f s)  NPCs: %d  terrain %dx%d  Ctrl-C to quit\x1b[K\n",
		s.Age, elapsed.Seconds(), len(s.Agents), t.Width(), t.Height())
	fmt.Fprintf(&b, "%-4s %-21s %-9s %-11s %-6s %-8s %s\x1b[K\n", "NPC", "State", "Location", "Destination", "Plans",
		"Journeys", "Current task")
	for _, agent := range s.Agents {
		task := agent.CurrentTaskName()
		if plan := agent.CurrentPlan(); agent.CurrentTaskIndex() < len(plan) {
			task = agent.FormatTask(plan[agent.CurrentTaskIndex()])
		}
		label := strconv.Itoa(int(agent.ID()))
		if !r.ascii {
			label = fmt.Sprintf("\x1b[38;5;%dm%-4s\x1b[0m", npcColors[int(agent.ID()-1)%len(npcColors)], label)
		} else {
			label = fmt.Sprintf("%-4s", string(npcSymbol(agent.ID())))
		}
		w := agent.Wanderer()
		fmt.Fprintf(&b, "%s %-21s %-9s %-11s %-6d %-8d %s\x1b[K\n", label, w.StateName(), w.Location(), w.Destination(),
			agent.PlanCount(), w.Journeys(), task)
	}
	b.WriteString("\x1b[J")
	io.WriteString(w, b.String())
}

func simulate(o options) {
	agents := o.integer("agents", 8)
	speed := o.number("speed", 1)
	fps := o.integer("fps", 30)
	seconds := o.number("seconds", 0)
	if agents < 0 || fps <= 0 || speed <= 0 {
		fail("--agents must be >= 0, --fps and --speed > 0")
	}
	s := demo.NewSimulation(agents, o.has("rt"), demo.CallTermErrorReporter(os.Stderr))
	info, _ := os.Stdout.Stat()
	interactive := info != nil && info.Mode()&os.ModeCharDevice != 0
	r := renderer{ascii: o.has("ascii") || !interactive}
	deltaTime := float32(speed / float64(fps))
	if !interactive {
		// Not a terminal: simulate without delays and print one ASCII frame
		// per simulated second.
		limit := seconds
		if limit <= 0 {
			limit = 10
		}
		framesPerSecond := max(1, fps)
		for frame := 1; float64(s.Age) < limit; frame++ {
			s.Update(deltaTime)
			if frame%framesPerSecond == 0 {
				var b strings.Builder
				r.frame(&b, s, 0)
				text := strings.NewReplacer("\x1b[H", "", "\x1b[K", "", "\x1b[J", "").Replace(b.String())
				fmt.Print(text, "\n")
			}
		}
		return
	}
	output := bufio.NewWriterSize(os.Stdout, 1<<16)
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
	fmt.Fprint(output, "\x1b[?25l\x1b[2J")
	defer func() {
		fmt.Fprint(output, "\x1b[0m\x1b[?25h\n")
		output.Flush()
	}()
	ticker := time.NewTicker(time.Second / time.Duration(fps))
	defer ticker.Stop()
	start := time.Now()
	for {
		select {
		case <-interrupted:
			return
		case <-ticker.C:
		}
		s.Update(deltaTime)
		r.frame(output, s, time.Since(start))
		output.Flush()
		if seconds > 0 && float64(s.Age) >= seconds {
			return
		}
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	o := parseOptions(os.Args[2:])
	switch os.Args[1] {
	case "list":
		list(o)
	case "run":
		run(o)
	case "simulate":
		simulate(o)
	case "trace":
		trace(o)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}
