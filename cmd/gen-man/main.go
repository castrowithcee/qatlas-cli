// Command gen-man generates release manpages from the real Cobra command tree.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra/doc"

	"github.com/castrowithcee/qatlas-cli/internal/cli"
	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
)

func main() {
	output := flag.String("output", "", "directory for generated manpages")
	version := flag.String("version", "dev", "Qatlas CLI version shown in the manpage metadata")
	flag.Parse()
	if *output == "" {
		fmt.Fprintln(os.Stderr, "gen-man: -output is required")
		os.Exit(2)
	}
	if err := generate(*output, *version); err != nil {
		fmt.Fprintf(os.Stderr, "gen-man: %v\n", err)
		os.Exit(1)
	}
}

// generate writes one manpage per command into output. Cobra leaves help topics out of the tree, so their
// text becomes a section of qatlas.1, the one page every installation carries.
func generate(output, version string) error {
	if err := os.MkdirAll(output, 0o755); err != nil {
		return err
	}
	header := &doc.GenManHeader{Section: "1", Source: "Qatlas CLI " + version, Manual: "Qatlas CLI Manual"}
	if err := doc.GenManTree(cli.DocumentationCommand(version), header, output); err != nil {
		return err
	}
	root := filepath.Join(output, "qatlas.1")
	page, err := os.ReadFile(root)
	if err != nil {
		return err
	}
	return os.WriteFile(root, []byte(withHelpTopics(string(page))), 0o644)
}

// withHelpTopics inserts the help topics before SEE ALSO, or at the end of a page without it.
func withHelpTopics(page string) string {
	var b strings.Builder
	b.WriteString(".SH HELP TOPICS\n.PP\nThe same text is printed by \\fBqatlas help\\fP \\fItopic\\fP " +
		"and shown by ? in \\fBqatlas tui\\fP.\n")
	for _, topic := range helptopics.All() {
		b.WriteString(".SS " + roff(topic.Name) + "\n.PP\n" + roff(topic.Short) + "\n.PP\n.nf\n")
		// The page indents its text, so the topic is wrapped narrower than for a terminal.
		for _, line := range strings.Split(helptopics.Wrap(topic.Text, 72), "\n") {
			b.WriteString(roff(line) + "\n")
		}
		b.WriteString(".fi\n\n")
	}
	if before, after, found := strings.Cut(page, ".SH SEE ALSO"); found {
		return before + b.String() + "\n.SH SEE ALSO" + after
	}
	return page + "\n" + b.String()
}

// roff escapes one line of plain text for a manpage: backslashes and hyphens keep their meaning, and a
// line that starts like a request is kept as text.
func roff(line string) string {
	line = strings.ReplaceAll(line, `\`, `\e`)
	line = strings.ReplaceAll(line, "-", `\-`)
	if strings.HasPrefix(line, ".") || strings.HasPrefix(line, "'") {
		line = `\&` + line
	}
	return line
}
