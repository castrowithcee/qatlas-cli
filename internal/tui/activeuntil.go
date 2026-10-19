package tui

import (
	"fmt"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// The active-for row of a connection form: how long an active connection stays active. It follows the state
// row and is shown only while the state is active.
const (
	activeForLabel     = "active for"
	activeForUnlimited = "unlimited"
	activeForHint      = "how long the connection stays active; afterwards it refuses like a disabled one. " +
		"The first choice of a connection with a limit keeps it as it is"
)

// activeForSteps are the durations the row offers besides unlimited.
var activeForSteps = []struct {
	text string
	span time.Duration
}{{"1h", time.Hour}, {"4h", 4 * time.Hour}, {"8h", 8 * time.Hour}, {"24h", 24 * time.Hour}}

// timeNow is the time the form and the list judge a limit at.
func (m *Model) timeNow() time.Time {
	if m.now == nil {
		return time.Now()
	}
	return m.now()
}

// activeForField is the active-for row of conn. A connection that is active and has a limit gets its
// remaining time, or expired, as the first and selected choice, which keeps the instant when saved as it is.
// A disabled connection starts without one: switching it on sets a new duration.
func (m *Model) activeForField(conn config.Connection) field {
	choices := []string{activeForUnlimited}
	for _, step := range activeForSteps {
		choices = append(choices, step.text)
	}
	f := choiceField(activeForLabel, choices, activeForUnlimited).withHint(activeForHint)
	if at, ok := conn.Until(); ok && !conn.Disabled {
		lead := remainingText(at, m.timeNow())
		if lead != expiredText {
			lead += " left"
		}
		f.choices = append([]string{lead}, choices...)
		f.index, f.keep = 0, conn.ActiveUntilCanonical()
	}
	f.hidden = conn.Disabled
	return f
}

// syncActiveFor shows the active-for row only while the state row reads active.
func (m *Model) syncActiveFor() {
	if f := m.field(activeForLabel); f != nil {
		f.hidden = m.fieldValue(stateLabel) != stateActive
	}
}

// activeUntilValue is the active_until the form saves: none unless the state is active and a duration or the
// kept instant is chosen.
func (m *Model) activeUntilValue() string {
	f := m.field(activeForLabel)
	if f == nil || m.fieldValue(stateLabel) != stateActive {
		return ""
	}
	choice := f.value()
	if choice == activeForUnlimited {
		return ""
	}
	for _, step := range activeForSteps {
		if choice == step.text {
			return config.FormatActiveUntil(m.timeNow().Add(step.span))
		}
	}
	return f.keep
}

// connectionStateText is the state column of the connection list: disabled, expired, active with the time
// that remains, or plain active.
func (m *Model) connectionStateText(conn config.Connection) string {
	if conn.Disabled {
		return stateDisabled
	}
	if conn.ActiveUntil == "" {
		return stateActive
	}
	at, ok := conn.Until()
	if !ok {
		return expiredText
	}
	if text := remainingText(at, m.timeNow()); text != expiredText {
		return stateActive + " " + text
	}
	return expiredText
}

const expiredText = "expired"

// remainingText is the time left until at, rounded down to minutes, or expired once it has passed.
func remainingText(at, now time.Time) string {
	left := at.Sub(now)
	switch {
	case left <= 0:
		return expiredText
	case left < time.Minute:
		return "<1m"
	case left < time.Hour:
		return fmt.Sprintf("%dm", int(left.Minutes()))
	case left < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(left.Hours()), int(left.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(left.Hours())/24, int(left.Hours())%24)
}
