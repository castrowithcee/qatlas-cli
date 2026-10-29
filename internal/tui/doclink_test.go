package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const evilURL = "https://evil.example/pwn"

// withProfile draws with a colour profile until the test ends.
func withProfile(t *testing.T, p colorProfile) {
	t.Helper()
	before := currentProfile()
	setProfile(p)
	t.Cleanup(func() { setProfile(before) })
}

// words is a view with all blanks removed, so an address wrapped over several lines reads as one.
func words(view string) string { return strings.Join(strings.Fields(view), "") }

func TestProviderDocURL(t *testing.T) {
	const base = "https://github.com/castrowithcee/qatlas-cli/blob/"
	plain := config.ProviderMetadata{ID: "seatable.base"}
	own := config.ProviderMetadata{ID: "brave", DocPage: "brave/README.md"}
	for _, c := range []struct {
		version  string
		metadata config.ProviderMetadata
		want     string
	}{
		{"v1.2.3", plain, base + "v1.2.3/docs/providers/seatable.base/setup.md"},
		{"v0.4.0-rc.1", plain, base + "v0.4.0-rc.1/docs/providers/seatable.base/setup.md"},
		{"", plain, base + "main/docs/providers/seatable.base/setup.md"},
		{"dev", plain, base + "main/docs/providers/seatable.base/setup.md"},
		{"1.2.3", plain, base + "main/docs/providers/seatable.base/setup.md"},
		{"v1.2.3 \x1b[0m", plain, base + "main/docs/providers/seatable.base/setup.md"},
		{"v1.2.3", own, base + "v1.2.3/docs/providers/brave/README.md"},
		{"v1.2.3", config.ProviderMetadata{}, ""},
	} {
		if got := providerDocURL(c.version, c.metadata); got != c.want {
			t.Errorf("providerDocURL(%q, %q) = %q, want %q", c.version, c.metadata.ID, got, c.want)
		}
	}
}

func TestDocLinesKeepTheAddressVisible(t *testing.T) {
	url := providerDocURL("v1.0.0", config.ProviderMetadata{ID: "bookstack"})
	t.Run("hyperlink", func(t *testing.T) {
		withProfile(t, profileTrueColor)
		joined := strings.Join(docLines(url, 30), "\n")
		if !strings.Contains(joined, ansi.SetHyperlink(url)) {
			t.Errorf("lines = %q, want an OSC 8 link to the whole address", joined)
		}
		if got := words(ansi.Strip(joined)); !strings.Contains(got, url) {
			t.Errorf("text = %q, want the address", got)
		}
	})
	t.Run("no hyperlink support", func(t *testing.T) {
		withProfile(t, profileNone)
		got := shown(strings.Join(docLines(url, 30), "\n"))
		if strings.Contains(got, "\x1b") {
			t.Errorf("view = %q, want plain text", got)
		}
		if !strings.Contains(words(got), url) {
			t.Errorf("view = %q, want the address as text", got)
		}
	})
	t.Run("NO_COLOR", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		before := styles
		t.Cleanup(func() { setTheme(before) })
		withProfile(t, detectProfile(&strings.Builder{}))
		applyTheme("")
		got := shown(strings.Join(docLines(url, 200), "\n"))
		if !strings.Contains(got, url) || strings.Contains(got, "\x1b") {
			t.Errorf("view = %q, want the address as plain text", got)
		}
	})
}

func TestFailedTestLinksOnlyWhereTheGuideHelps(t *testing.T) {
	url := providerDocURL("v1.2.3", config.ProviderMetadata{ID: "bookstack"})
	for _, class := range []provider.Class{provider.ClassOK, provider.ClassUnreachable, provider.ClassTLS,
		provider.ClassAuth, provider.ClassPermission, provider.ClassNotFound, provider.ClassTimeout,
		provider.ClassRateLimited, provider.ClassInvalidResponse, provider.ClassProviderError} {
		t.Run(string(class), func(t *testing.T) {
			m := newTestableModel(t, func(context.Context, string) (provider.Class, error) { return class, nil }, nil)
			m.version = "v1.2.3"
			m.width, m.height = 200, 40
			runTest(t, m)
			want := class == provider.ClassAuth || class == provider.ClassPermission
			if got := strings.Contains(words(screenOf(m)), url); got != want {
				t.Errorf("class %s: view shows the address = %v, want %v", class, got, want)
			}
		})
	}
}

func TestFailedTestLinksAreOSC8InColour(t *testing.T) {
	withProfile(t, profileTrueColor)
	m := newTestableModel(t, func(context.Context, string) (provider.Class, error) { return provider.ClassAuth, nil }, nil)
	m.version = "v1.2.3"
	runTest(t, m)
	if line := m.testLine(); !strings.Contains(line, ansi.SetHyperlink(providerDocURL("v1.2.3", config.ProviderMetadata{ID: "bookstack"}))) {
		t.Errorf("test line = %q, want a hyperlink", line)
	}
}

func TestNoAddressFromAProviderAnswerIsLinked(t *testing.T) {
	withProfile(t, profileTrueColor)
	m := newTestableModel(t, func(context.Context, string) (provider.Class, error) {
		return provider.ClassAuth, errors.New("denied, see " + evilURL)
	}, nil)
	runTest(t, m)
	view := m.render()
	if strings.Contains(view, "\x1b]8;;"+evilURL) || strings.Contains(m.testLine(), evilURL) {
		t.Errorf("view = %q, want no link to an address from the answer", view)
	}
}

func TestProviderInfoLinksTheGuide(t *testing.T) {
	m := newTestableModel(t, nil, nil)
	m.version = "v1.2.3"
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	m.focus = 1
	if m.fields[m.focus].kind != fieldProvider {
		t.Fatalf("field %q is no provider row", m.fields[m.focus].label)
	}
	url := providerDocURL("v1.2.3", config.ProviderMetadata{ID: "bookstack"})
	eachLanguage(t, func(t *testing.T) {
		s := m.infoSubject()
		if s.doc != url {
			t.Fatalf("doc = %q, want %q", s.doc, url)
		}
		lines := s.lines(60)
		if got := words(ansi.Strip(strings.Join(lines, "\n"))); !strings.Contains(got, url) || !strings.Contains(got, words(tr(tDocGuide))) {
			t.Errorf("lines = %q, want the guide label and the address", lines)
		}
	})
}

func TestProviderTableInfoLinksTheMarkedProvider(t *testing.T) {
	m := newTestableModel(t, nil, nil)
	m.version = "v1.2.3"
	m.width, m.height, m.termWidth, m.termHeight = 160, 50, 160, 50
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	m.focus = 1
	m.openProviderTable()
	press(t, m, "i")
	if !m.infoOpen {
		t.Fatal("the info key opened nothing on the provider table")
	}
	if want := providerDocURL("v1.2.3", config.ProviderMetadata{ID: "bookstack"}); m.infoShown.doc != want {
		t.Errorf("doc = %q, want %q", m.infoShown.doc, want)
	}
}
