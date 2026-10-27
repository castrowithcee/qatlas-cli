package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// chunkReader hands out its chunks one per Read, the way a terminal behind SSH or tmux delivers a short read,
// then reports the end of the input.
type chunkReader struct {
	mu     sync.Mutex
	chunks [][]byte
}

// testClock is the clock of the filter in a test; a pause in the input moves it on.
type testClock struct{ offset atomic.Int64 }

func (c *testClock) now() time.Time { return time.Now().Add(time.Duration(c.offset.Load())) }

// pause is a key that is a gap in the input instead: when the event loop meets it, the real timers run out and
// the filter's clock moves on by a second. Handled there, not by the reader, which runs ahead of the loop.
const pause = "\x1b[23~"

func (r *chunkReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	if n < len(r.chunks[0]) {
		r.chunks[0] = r.chunks[0][n:]
	} else {
		r.chunks = r.chunks[1:]
	}
	return n, nil
}

// inputEnd is a key no screen uses; the filter turns it into the end of the program, so every key before it has
// been handled when Run returns.
const inputEnd = "\x1b[24~"

// runInput feeds the chunks to a real program around model and returns once all of them were handled. The
// production filter runs first; keys are the messages that reached the model.
func runInput(t *testing.T, model *Model, chunks ...string) (keys []string) {
	t.Helper()
	clock := &testClock{}
	reader := &chunkReader{}
	for _, c := range chunks {
		reader.chunks = append(reader.chunks, []byte(c))
	}
	reader.chunks = append(reader.chunks, []byte(inputEnd))
	fragments := newFragmentFilter(clock.now)
	var seen []string
	observe := func(m tea.Model, msg tea.Msg) tea.Msg {
		if key, ok := msg.(tea.KeyMsg); ok && key.Type == tea.KeyF11 {
			clock.offset.Add(int64(time.Second))
			time.Sleep(4 * altBracketHold)
			return nil
		}
		msg = fragments.filter(m, msg)
		if key, ok := msg.(tea.KeyMsg); ok && key.Type == tea.KeyF12 {
			return tea.QuitMsg{}
		}
		switch msg := msg.(type) {
		case tea.KeyMsg:
			seen = append(seen, msg.String())
		case keysMsg:
			for _, key := range msg {
				seen = append(seen, key.String())
			}
		}
		return msg
	}
	p := tea.NewProgram(model, tea.WithInput(reader), tea.WithOutput(io.Discard), tea.WithFilter(observe),
		tea.WithoutSignals(), tea.WithMouseCellMotion())
	fragments.program = p
	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program: %v", err)
		}
	case <-time.After(10 * time.Second):
		p.Kill()
		t.Fatal("the program did not end")
	}
	return seen
}

// splitReports are mouse reports as a terminal sends them: wheel and left click, press and release.
// The motion and release reports move nothing, so the focus stays on the field for the whole stream.
var splitReports = []string{"\x1b[<65;12;5M", "\x1b[<64;3;40M", "\x1b[<0;70;9M\x1b[<0;70;9m", "\x1b[<35;120;40M", "\x1b[<0;7;19m"}

func formWithTextField(t *testing.T) *Model {
	t.Helper()
	m := sizedModel(groupModel(t), 120, 40)
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "abc")
	if !m.typing() {
		t.Fatalf("the form does not read text: screen %v", m.screen)
	}
	return m
}

func passphrasePrompt(t *testing.T) *Model {
	t.Helper()
	m := sizedModel(groupModel(t), 120, 40)
	m.openVaultUnlockPrompt(screenNav)
	return m
}

func TestSplitMouseReportsMakeNoTextInAField(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *Model{"form field": formWithTextField, "passphrase": passphrasePrompt} {
		for _, report := range splitReports {
			stream := strings.Repeat(report, 3)
			for i := 0; i <= len(stream); i++ {
				t.Run(fmt.Sprintf("%s/%q/%d", name, report, i), func(t *testing.T) {
					m := open(t)
					before := m.formState()
					if m.screen == screenVaultUnlock {
						before = m.vaultUnlock.input.Value()
					}
					keys := runInput(t, m, stream[:i], stream[i:])
					after := m.formState()
					if m.screen == screenVaultUnlock {
						after = m.vaultUnlock.input.Value()
					}
					if after != before {
						t.Errorf("field %q -> %q after keys %q", before, after, keys)
					}
				})
			}
		}
	}
}

func fieldValue(m *Model) string {
	if m.screen == screenVaultUnlock {
		return m.vaultUnlock.input.Value()
	}
	return m.fields[m.focus].value()
}

func TestTypedCharactersOfAReportReachTheField(t *testing.T) {
	const paste = "\x1b[200~[<65;12;5M\x1b[201~"
	for _, c := range []struct {
		name   string
		chunks []string
		want   string
	}{
		{"typed in one read", []string{"[<65;12;5M"}, "abc[<65;12;5M"},
		{"typed one by one", []string{"[", "<", "6", "5", ";", "M"}, "abc[<65;M"},
		{"after a pause", []string{"\x1b[", pause, "<65;12;5M"}, "abc[<65;12;5M"},
		{"alt+[ on its own", []string{"\x1b[", pause, "x"}, "abc[x"},
		{"alt+[ and a letter", []string{"\x1b[", "x"}, "abc[x"},
		{"alt+[ twice", []string{"\x1b[", "\x1b[", pause, "x"}, "abc[[x"},
		{"digits after a report", []string{"\x1b[<35;12;5M", "12;5M"}, "abc12;5M"},
		{"paste of a report", []string{paste}, "abc[<65;12;5M"},
		{"paste after a piece", []string{"\x1b[", "<65", paste}, "abc[<65;12;5M"},
		{"text after a report", []string{"\x1b[", "<65;12;5Mxy"}, "abcxy"},
		{"text after a pause", []string{"\x1b[", "<65;1", pause, "2;5M"}, "abc2;5M"},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := formWithTextField(t)
			runInput(t, m, append(c.chunks, pause)...)
			if got := fieldValue(m); got != c.want {
				t.Errorf("field = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAWheelEventDrawsOnlyWhenTheFrameIsStale(t *testing.T) {
	m := sizedModel(groupModel(t), 120, 40)
	openSectionByName(t, m, sectionServices)
	// A wheel event on the frame drawn last draws nothing; on a stale one it draws once.
	m.render()
	m.hits = append(m.hits, hit{x0: -1})
	wheelAt(t, m, 60, 20, tea.MouseButtonWheelDown)
	if !hasMarker(m) {
		t.Errorf("a wheel event on a drawn frame drew it again")
	}
	m.drawn = false
	m.hits = []hit{{x0: -1}}
	wheelAt(t, m, 60, 20, tea.MouseButtonWheelDown)
	if hasMarker(m) {
		t.Errorf("a wheel event on a stale frame did not draw it")
	}
}

func hasMarker(m *Model) bool {
	for _, h := range m.hits {
		if h.x0 == -1 {
			return true
		}
	}
	return false
}
