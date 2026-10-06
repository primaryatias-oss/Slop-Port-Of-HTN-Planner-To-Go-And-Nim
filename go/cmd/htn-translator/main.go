// Command htn-translator validates HTN domains and translates them into
// native Go planners.
//
//	htn-translator <domain-file> <entry-point> [output-directory] [options]
//	htn-translator --check <domain-file>
package main

import (
	"os"

	"github.com/primaryatias-oss/Slop-Port-Of-HTN-Planner-To-Go-And-Nim/go/htn/translator"
)

func main() {
	os.Exit(translator.Main(os.Args[1:], os.Stdout, os.Stderr))
}
