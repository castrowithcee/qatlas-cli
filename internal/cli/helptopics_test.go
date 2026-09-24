package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
)

// Every topic is printed by 'qatlas help <topic>' as exactly its text, and the root help lists it.
func TestHelpTopicsArePrintedAndListed(t *testing.T) {
	var root bytes.Buffer
	if code := Run([]string{"--help"}, &root, &bytes.Buffer{}); code != exitOK {
		t.Fatalf("--help exit code = %d", code)
	}
	_, listed, found := strings.Cut(root.String(), "Additional help topics:")
	if !found {
		t.Fatalf("the root help lists no help topics:\n%s", root.String())
	}
	for _, topic := range helptopics.All() {
		if !strings.Contains(listed, "qatlas "+topic.Name+" ") || !strings.Contains(listed, topic.Short) {
			t.Errorf("the root help does not list topic %q:\n%s", topic.Name, listed)
		}
		var stdout, stderr bytes.Buffer
		if code := Run([]string{"help", topic.Name}, &stdout, &stderr); code != exitOK || stderr.Len() != 0 {
			t.Fatalf("help %s: exit %d stderr %q", topic.Name, code, stderr.String())
		}
		if got := strings.TrimSpace(stdout.String()); got != helptopics.Wrap(topic.Text, 80) {
			t.Errorf("help %s printed\n%s\nwant exactly its text, wrapped at 80 columns", topic.Name, got)
		}
	}

	// The configuration topic does not take the name of the config command.
	var config bytes.Buffer
	if code := Run([]string{"help", "config"}, &config, &bytes.Buffer{}); code != exitOK ||
		!strings.Contains(config.String(), "Inspect the qatlas configuration") ||
		!strings.Contains(config.String(), "validate") {
		t.Errorf("help config no longer describes the config command:\n%s", config.String())
	}
}

var (
	quotedCommand = regexp.MustCompile(`["']qatlas ([^"']+)["']`)
	flagName      = regexp.MustCompile(`--[a-z][a-z-]*`)
	toolID        = regexp.MustCompile(`\b[a-z]+\.[a-z]+\.[a-z]+\b`)
	listedCode    = regexp.MustCompile(`(?m)^- ([a-z-]+): \S`)
)

// The topics may only name commands, flags, tools, codes, and contract fields this build has.
func TestHelpTopicsNameWhatExists(t *testing.T) {
	root := newRootCommand(&Options{}, defaultRegistry())
	root.InitDefaultHelpCmd()
	var defined func(c *cobra.Command, name string) bool
	defined = func(c *cobra.Command, name string) bool {
		if c.Flags().Lookup(name) != nil || c.PersistentFlags().Lookup(name) != nil {
			return true
		}
		for _, sub := range c.Commands() {
			if defined(sub, name) {
				return true
			}
		}
		return false
	}
	topics := map[string]bool{}
	for _, topic := range helptopics.All() {
		topics[topic.Name] = true
	}

	for _, topic := range helptopics.All() {
		for _, flag := range flagName.FindAllString(topic.Text, -1) {
			if !defined(root, strings.TrimPrefix(flag, "--")) {
				t.Errorf("topic %s names the unknown flag %s", topic.Name, flag)
			}
		}
		for _, snippet := range commandSnippets(topic.Text) {
			// The former name of describe still runs, but no guide teaches it.
			if strings.HasPrefix(snippet, "tool ") {
				t.Errorf("topic %s names the hidden command %q; it is 'qatlas describe'", topic.Name, snippet)
			}
			assertCommandSnippet(t, root, topics, topic.Name, snippet)
		}
		for _, id := range toolID.FindAllString(topic.Text, -1) {
			if _, _, ok := defaultRegistry().Lookup(id); !ok {
				t.Errorf("topic %s names the unknown tool %s", topic.Name, id)
			}
		}
	}

	agents := topicText(t, "agents")
	// Every code this build can emit is listed once, in the order the documentation shows them, each with
	// what it means.
	var listed []output.Code
	for _, match := range listedCode.FindAllStringSubmatch(agents, -1) {
		listed = append(listed, output.Code(match[1]))
	}
	if !reflect.DeepEqual(listed, output.AllCodes()) {
		t.Errorf("the agents topic lists the error codes\n%v\nwant\n%v", listed, output.AllCodes())
	}
	// Discovery starts with the providers and narrows step by step to one invocation.
	last := -1
	for _, step := range []string{"agents", "providers", "connections", "tools", "describe", "invoke"} {
		at := strings.Index(agents, "qatlas "+step)
		if at <= last {
			t.Errorf("the agents topic does not name 'qatlas %s' as the next discovery step", step)
		}
		last = at
	}
	for _, tool := range []string{"qatlas.search", "qatlas.describe", "qatlas.invoke"} {
		if !strings.Contains(agents, tool) || !knownMCPTool(tool) {
			t.Errorf("the agents topic and the broker disagree on %s", tool)
		}
	}
	contract, err := json.Marshal(capability.Descriptor{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(agents, "requires_explicit_connection") ||
		!strings.Contains(string(contract), `"requires_explicit_connection"`) {
		t.Errorf("the agents topic names a contract field the contract does not have")
	}
	for _, want := range []string{"AGENTS.md", "CLAUDE.md", "<name>", "Exit code 0", "Exit code 2", "Exit code 1",
		`--arg 'labels=["bug"]'`, "on stdin"} {
		if !strings.Contains(agents, want) {
			t.Errorf("the agents topic does not say %q", want)
		}
	}
	if exitUsage != 2 || exitRuntime != 1 {
		t.Errorf("the exit codes changed; the agents topic names 2 and 1")
	}
}

// commandSnippets returns every quoted qatlas command and every example line that runs qatlas.
func commandSnippets(text string) []string {
	var snippets []string
	for _, match := range quotedCommand.FindAllStringSubmatch(text, -1) {
		snippets = append(snippets, match[1])
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if _, after, found := strings.Cut(trimmed, "| qatlas "); found {
			snippets = append(snippets, after)
		} else if strings.HasPrefix(line, "  ") && strings.HasPrefix(trimmed, "qatlas ") {
			snippets = append(snippets, strings.TrimPrefix(trimmed, "qatlas "))
		}
	}
	return snippets
}

// assertCommandSnippet resolves the command words of one snippet and checks its flags against that command.
func assertCommandSnippet(t *testing.T, root *cobra.Command, topics map[string]bool, topic, snippet string) {
	t.Helper()
	words := strings.Fields(snippet)
	if len(words) == 0 {
		t.Errorf("topic %s quotes an empty qatlas command", topic)
		return
	}
	if words[0] == "help" {
		if len(words) < 2 || !topics[words[1]] {
			t.Errorf("topic %s points to a help topic that does not exist: %q", topic, snippet)
		}
		return
	}
	cmd, _, err := root.Find(words[:1])
	if err != nil || cmd == root {
		t.Errorf("topic %s names the unknown command %q", topic, snippet)
		return
	}
	if cmd.HasSubCommands() && len(words) > 1 && !strings.HasPrefix(words[1], "-") {
		sub, _, err := root.Find(words[:2])
		if err != nil || sub == cmd {
			t.Errorf("topic %s names the unknown command %q", topic, snippet)
			return
		}
		cmd = sub
	}
	for _, flag := range flagName.FindAllString(snippet, -1) {
		name := strings.TrimPrefix(flag, "--")
		if cmd.Flags().Lookup(name) == nil && cmd.InheritedFlags().Lookup(name) == nil {
			t.Errorf("topic %s gives %s to %q, which has no such flag", topic, flag, cmd.CommandPath())
		}
	}
}

func topicText(t *testing.T, name string) string {
	t.Helper()
	for _, topic := range helptopics.All() {
		if topic.Name == name {
			return topic.Text
		}
	}
	t.Fatalf("no topic %q", name)
	return ""
}

// The editor looks for a newer release only in a release build and only while the person has not opted out.
func TestTUIUpdaterIsGated(t *testing.T) {
	t.Setenv(noUpdateCheck, "")
	for _, build := range []string{"", "dev"} {
		if updater := tuiUpdater(&Options{}, build); updater != nil {
			t.Errorf("build %q got an updater", build)
		}
	}
	if updater := tuiUpdater(&Options{}, "v0.4.0"); updater == nil {
		t.Error("a release build got no updater")
	}
	seam := selfupdate.New("v0.4.0")
	if updater := tuiUpdater(&Options{Updater: seam}, "v0.4.0"); updater != seam {
		t.Errorf("the updater seam was not used: %v", updater)
	}

	t.Setenv(noUpdateCheck, "1")
	if updater := tuiUpdater(&Options{Updater: seam}, "v0.4.0"); updater != nil {
		t.Errorf("%s=1 still got an updater", noUpdateCheck)
	}
}
