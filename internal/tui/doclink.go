package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// providerDocsBase is the one place that knows where the public provider documentation lives; a later site
// replaces it here. The reference (a release tag or main) and the page path follow it.
const providerDocsBase = "https://github.com/castrowithcee/qatlas-cli/blob/"

var releaseVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// providerDocURL is the address of a provider's user page for the running version. Only a release tag names
// a pinned version; every other build (empty, dev) points at main. The page is part of the provider's
// metadata, so the editor names no provider; a test in internal/cli checks it against the repository.
func providerDocURL(version string, metadata config.ProviderMetadata) string {
	if metadata.ID == "" {
		return ""
	}
	ref := "main"
	if releaseVersion.MatchString(version) {
		ref = version
	}
	return providerDocsBase + ref + "/docs/providers/" + metadata.DocPath()
}

// docLines draws the guide of a provider in width cells: its label, then the address as visible text. The
// address is always shown in full, so a terminal without hyperlinks (or NO_COLOR, which strips the escape
// sequences) still carries it; each wrapped piece links to the whole address.
func docLines(url string, width int) []string {
	if url == "" {
		return nil
	}
	out := strings.Split(helptopics.Wrap(tr(tDocGuide), width), "\n")
	link := styles.cell.Underline(true).Hyperlink(url)
	for _, piece := range strings.Split(ansi.Hardwrap(url, max(width, 1), true), "\n") {
		out = append(out, link.Render(piece))
	}
	return out
}

// docURL is the guide address of a provider for the running version.
func (m *Model) docURL(id string) string {
	metadata, ok := m.cfg.ProviderMetadata(id)
	if !ok {
		return ""
	}
	return providerDocURL(m.version, metadata)
}

// docText is the guide of a provider as lines of a screen, a blank line before them.
func (m *Model) docText(id string) string {
	lines := docLines(m.docURL(id), m.usable(0))
	if len(lines) == 0 {
		return ""
	}
	return "\n" + styles.hint.Render(lines[0]) + "\n" + strings.Join(lines[1:], "\n")
}

// failDocHelps reports whether the user page of the provider helps with a failed test of this class: a
// rejected credential or missing rights are fixed by the token set-up it describes. Every other class (network,
// timeout, rate limit, provider answers) has other causes, and nothing taken from an answer is ever linked.
func failDocHelps(class provider.Class) bool {
	return class == provider.ClassAuth || class == provider.ClassPermission
}

// testProvider is the provider of the connection under test, or empty.
func (m *Model) testProvider() string {
	connection, ok := m.cfg.Connections[m.testName]
	if !ok {
		return ""
	}
	return m.cfg.Services[connection.Service].Provider
}
