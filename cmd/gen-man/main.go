// Command gen-man generates release manpages from the real Cobra command tree.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/spf13/cobra/doc"

	"github.com/castrowithcee/qatlas-cli/internal/cli"
)

func main() {
	output := flag.String("output", "", "directory for generated manpages")
	version := flag.String("version", "dev", "Qatlas CLI version shown in the manpage metadata")
	flag.Parse()
	if *output == "" {
		fmt.Fprintln(os.Stderr, "gen-man: -output is required")
		os.Exit(2)
	}
	if err := os.MkdirAll(*output, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "gen-man: %v\n", err)
		os.Exit(1)
	}
	header := &doc.GenManHeader{Section: "1", Source: "Qatlas CLI " + *version, Manual: "Qatlas CLI Manual"}
	if err := doc.GenManTree(cli.DocumentationCommand(*version), header, *output); err != nil {
		fmt.Fprintf(os.Stderr, "gen-man: %v\n", err)
		os.Exit(1)
	}
}
