// Command runscenario prints the Go port's output for scenario files (the
// counterpart of the C++ htn-oracle).
//
//	go run ./internal/tools/runscenario [-root ..] file.scn...
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/internal/generated"
	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/internal/scenario"
)

func main() {
	root := flag.String("root", "..", "repository root")
	flag.Parse()
	for _, file := range flag.Args() {
		output, err := scenario.RunFile(file, *root, generated.Planners)
		fmt.Print(output)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
}
