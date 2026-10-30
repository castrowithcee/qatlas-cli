package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// cursorAt draws the frame and returns the text left of the terminal cursor and the cell under it, read from
// the rendered image. It fails when the view sets no cursor.
func cursorAt(t *testing.T, m *Model) (before, at string) {
	t.Helper()
	frame := m.render()
	if strings.Contains(frame, cursorMark) {
		t.Fatal("the frame still carries the cursor mark")
	}
	c := m.View().Cursor
	if c == nil {
		t.Fatalf("no cursor set:\n%s", ansi.Strip(frame))
	}
	if c.Blink {
		t.Error("the cursor blinks, want a static one")
	}
	lines := strings.Split(roundFrame(ansi.Strip(frame)), "\n")
	if c.Y >= len(lines) {
		t.Fatalf("cursor row %d outside the %d rows drawn", c.Y, len(lines))
	}
	line := lines[c.Y]
	at, _ = ansi.FirstGraphemeCluster(ansi.Cut(line, c.X, c.X+2), ansi.GraphemeWidth)
	return ansi.Cut(line, 0, c.X), at
}

func focusBaseURL(t *testing.T, m *Model) {
	t.Helper()
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	press(t, m, "tab", "tab") // name, provider, base url
	if got := m.fields[m.focus].label; got != "base url" {
		t.Fatalf("focused field = %q, want the base url", got)
	}
}

func TestTheCursorStandsInTheFocusedFormField(t *testing.T) {
	const url = "https://wiki.example.invalid"
	for _, p := range []colorProfile{profileTrueColor, profile16, profileNone} {
		restoreRenderer(t)
		setProfile(p)
		m, _, _ := newModel(t)
		m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
		focusBaseURL(t, m)
		typeText(t, m, url)

		before, at := cursorAt(t, m)
		if !strings.HasSuffix(strings.TrimRight(before, " "), "> base url │ "+url) && !strings.HasSuffix(before, url) {
			t.Errorf("profile %v: text before the cursor = %q, want it to end with the value", p, before)
		}
		if at != " " {
			t.Errorf("profile %v: cell under the cursor at the end = %q, want a blank", p, at)
		}
		press(t, m, "left", "left", "left")
		before, at = cursorAt(t, m)
		if !strings.HasSuffix(before, url[:len(url)-3]) || at != "l" {
			t.Errorf("profile %v: cursor after three lefts: before %q, at %q", p, before, at)
		}
	}
}

func TestTheCursorFollowsALongScrolledValue(t *testing.T) {
	for name, unit := range map[string]string{"narrow": "abcdefghij", "wide": "日本語", "mixed": "aé日"} {
		m, _, _ := newModel(t)
		m.Update(tea.WindowSizeMsg{Width: 70, Height: 30})
		focusBaseURL(t, m)
		typeText(t, m, strings.Repeat(unit, 20))
		runes := []rune(m.fields[m.focus].input.Value())

		for _, back := range []int{0, 1, 4, 30} {
			press(t, m, "end")
			for range back {
				press(t, m, "left")
			}
			before, at := cursorAt(t, m)
			want := " "
			if back > 0 {
				want = string(runes[len(runes)-back])
			}
			if at != want {
				t.Errorf("%s, %d back: cell under the cursor = %q, want %q (before %q)", name, back, at, want, before)
			}
			// A cursor scrolled to the left edge of the field has no text before it.
			if edge := strings.HasSuffix(before, "│ "); back < len(runes) && !edge && !strings.HasSuffix(before, string(runes[len(runes)-back-1])) {
				t.Errorf("%s, %d back: text before the cursor %q does not end with the previous character", name, back, before)
			}
		}
	}
}

func TestTheCursorStandsInTheCommandPalette(t *testing.T) {
	m := cmdModel(t)
	press(t, m, ":")
	if m.screen != screenCommand {
		t.Fatalf("screen = %v, want the palette", m.screen)
	}
	// An empty field shows its placeholder with the cursor on the first cell behind the prompt.
	before, at := cursorAt(t, m)
	if !strings.HasSuffix(before, " : ") || at != "s" {
		t.Errorf("empty palette: before %q, at %q, want the placeholder's first cell behind the prompt", before, at)
	}
	typeText(t, m, "ser")
	before, at = cursorAt(t, m)
	if !strings.HasSuffix(before, " : ser") || at != " " {
		t.Errorf("palette with text: before %q, at %q", before, at)
	}
	press(t, m, "left")
	_, at = cursorAt(t, m)
	if at != "r" {
		t.Errorf("palette after left: cell under the cursor = %q, want %q", at, "r")
	}
}

func TestTheCursorStandsInAMaskedModalField(t *testing.T) {
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.openVaultUnlockPrompt(m.screen)
	typeText(t, m, "pä日")
	if _, ok := m.modalView(); !ok {
		t.Skip("the dialog is not drawn as a modal here")
	}
	before, at := cursorAt(t, m)
	mask := string(m.vaultUnlock.input.EchoCharacter)
	if mask == "" || strings.Contains(before, "pä") || !strings.HasSuffix(before, strings.Repeat(mask, 3)) || at != " " {
		t.Errorf("masked field: before %q, at %q, want three mask cells then a blank", before, at)
	}
	if left := ansi.StringWidth(before); left < 10 {
		t.Errorf("cursor column %d does not include the modal's offset", left)
	}
}

func TestNoCursorWithoutAFocusedField(t *testing.T) {
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	for name, show := range map[string]func(){
		"overview": func() { m.screen = screenNav },
		"list":     func() { openSectionByName(t, m, sectionServices) },
	} {
		show()
		if c := m.View().Cursor; c != nil {
			t.Errorf("%s: cursor at %v, want none", name, *c)
		}
	}
	focusBaseURL(t, m)
	if m.View().Cursor == nil {
		t.Fatal("a focused field sets no cursor")
	}
	m.fields[m.focus].input.Blur()
	if c := m.View().Cursor; c != nil {
		t.Errorf("blurred field: cursor at %v, want none", *c)
	}
}
