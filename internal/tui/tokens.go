// The Tokens section (see the sectionTokens comment in tui.go): the agent tokens of an encrypted vault, each
// with its vorbild connections and its expiry, and the screens that create, show, and revoke one.
//
// Every function here that reads or changes a token defers to package vault, the same core 'qatlas vault
// token' uses, and asks package approval which vorbilder cover nothing now; nothing here reimplements
// either. A token's value is read from the vault only to draw it on the detail screen, and only while it is
// revealed there in an admin session: it never enters the model, a message, a status, or an error.
package tui

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// The rows of the form that creates an agent token, besides its name.
const (
	vorbilderLabel = "vorbilder"
	expiresLabel   = "expires"
)

// Hints and notes of the token form.
const (
	tokenNameHint = "a name you choose, e.g. the agent or customer the token is for: letters, digits, '-', " +
		"'_' or '.'"
	vorbilderHint = "enter opens the connections that read a vault credential, to tick one or more"
	expiresHint   = "optional, empty = no expiry; a date YYYY-MM-DD (its start, local time) or an RFC 3339 time"
	vorbildNote   = "each vorbild sets the ceiling: same service and credential, and everything else " +
		"(rights, tools, targets, paths) within that connection"
	vorbildSelfNote = "this token can never change or delete a vorbild connection itself"
)

// tokenLapsedText is what a vorbild that covers nothing now is marked with, symbol and words together.
const tokenLapsedText = "renamed, deleted, not approved, or its credential stored anew: covers nothing"

// tokenEntry is one agent token as the Tokens section shows it: never its value, which only tokenValue
// reads, and only for the revealed detail screen. lapsed names its vorbilder that cover nothing now, and
// checkErr says why that could not be found out, "" when it could.
type tokenEntry struct {
	token    vault.Token
	lapsed   []string
	checkErr string
}

// tokensReport is the Tokens section's data: every agent token of the vault unlocked in this process, sorted
// by name, without values. unavailable names why the section cannot show that list otherwise, in the words a
// person reads on the section itself, so a caller shows it instead of an empty list: there is no vault, it
// is unencrypted (agent tokens need a passphrase, adr-0002), or it is locked. A locked vault is never
// unlocked merely to compute this list; enter on the list does that, through requireAdmin, exactly like the
// Approvals section (see approvalsReport).
func (m *Model) tokensReport() (entries []tokenEntry, unavailable string) {
	v := m.secrets.Vault()
	if v == nil {
		return nil, "There is no vault configured for this run."
	}
	state, err := v.State()
	if err != nil {
		return nil, m.redactor.Apply(err.Error())
	}
	switch state {
	case vault.StateAbsent, vault.StateUnencrypted:
		return nil, fmt.Sprintf("Agent tokens need an encrypted vault. Open Vault (%d) to set a passphrase.",
			int(sectionVault)+1)
	case vault.StateLocked:
		return nil, "The vault is locked, so agent tokens cannot be listed; press enter or ctrl+l to unlock it."
	}
	tokens, err := v.Tokens()
	if err != nil {
		return nil, m.redactor.Apply(err.Error())
	}
	entries = make([]tokenEntry, 0, len(tokens))
	for _, token := range tokens {
		entry := tokenEntry{token: token}
		entry.lapsed, err = approval.LapsedModels(m.cfg, v, token)
		if err != nil {
			entry.checkErr = m.redactor.Apply(err.Error())
		}
		entry.token.Value = ""
		entries = append(entries, entry)
	}
	return entries, ""
}

// tokenByName is the entry of the named token, and whether the vault still holds it.
func (m *Model) tokenByName(name string) (tokenEntry, bool, string) {
	entries, unavailable := m.tokensReport()
	if unavailable != "" {
		return tokenEntry{}, false, unavailable
	}
	for _, entry := range entries {
		if entry.token.Name == name {
			return entry, true, ""
		}
	}
	return tokenEntry{}, false, ""
}

// tokenValue reads the value of the named token for the detail screen to draw, "" when the vault cannot
// give it now. Only tokenDetailView calls it, and only while tokenValueShown is true.
func (m *Model) tokenValue(name string) string {
	v := m.secrets.Vault()
	if v == nil {
		return ""
	}
	tokens, err := v.Tokens()
	if err != nil {
		return ""
	}
	for _, token := range tokens {
		if token.Name == name {
			return token.Value
		}
	}
	return ""
}

// tokenValueShown reports whether the detail screen draws the token's value: only once s asked for it, and
// only while this window's admin session lasts. With vault.admin_timeout: 0 there never is a session to
// last; the passphrase s just asked for is the proof then, and the value stays shown until s hides it or
// the detail screen is left.
func (m *Model) tokenValueShown() bool {
	return m.tokenReveal && (m.adminSessionActive() || m.cfg.VaultAdminTimeout() <= 0)
}

// tokenWarning is the short warning the list shows behind a token's name, symbol and words together so it
// never rests on colour alone; "" when nothing keeps the token from approving what its vorbilder allow.
func tokenWarning(entry tokenEntry) string {
	var parts []string
	if entry.token.Expired(time.Now()) {
		parts = append(parts, "expired")
	}
	switch n := len(entry.lapsed); {
	case entry.checkErr != "":
		parts = append(parts, "vorbilder not checked")
	case n == 1:
		parts = append(parts, "1 vorbild covers nothing")
	case n > 1:
		parts = append(parts, fmt.Sprintf("%d vorbilder cover nothing", n))
	}
	if len(parts) == 0 {
		return ""
	}
	return "⚠ " + strings.Join(parts, ", ")
}

// tokenExpiry is how a token's expiry reads: its date, with the time only when it does not start the day,
// in local time, or "no expiry".
func tokenExpiry(token vault.Token) string {
	if token.Expires == nil {
		return "no expiry"
	}
	at := token.Expires.Local()
	if at.Hour() == 0 && at.Minute() == 0 && at.Second() == 0 {
		return at.Format("2006-01-02")
	}
	return at.Format("2006-01-02 15:04")
}

// tokenCells are the cells of one token in the Tokens list: its name with any warning behind it, since the
// first column is never cut, its expiry, and its vorbilder.
func (m *Model) tokenCells(name string) []string {
	entry, ok, _ := m.tokenByName(name)
	if !ok {
		return []string{name, "", ""}
	}
	label := name
	if warning := tokenWarning(entry); warning != "" {
		label += "  " + warning
	}
	return []string{label, tokenExpiry(entry.token), strings.Join(entry.token.Models, ", ")}
}

// tokenNames are the names the Tokens list holds.
func (m *Model) tokenNames() []string {
	entries, _ := m.tokensReport()
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.token.Name)
	}
	return names
}

// vorbildChoices are the connections a token may name as a vorbild: every one that reads a vault credential,
// since the vault approves nothing for any other, sorted.
func (m *Model) vorbildChoices() []string {
	var names []string
	for name, connection := range m.cfg.Connections {
		if m.cfg.Credentials[connection.Credential].Type == config.CredentialTypeVault {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// tokensBlocked is why n cannot start a token now, "" when it can: the vault cannot hold tokens at all, or no
// connection could be a vorbild. A locked vault does not block it; requireAdmin unlocks it first.
func (m *Model) tokensBlocked() string {
	_, unavailable := m.tokensReport()
	if unavailable != "" && !m.vaultLocked() {
		return unavailable
	}
	if len(m.vorbildChoices()) == 0 {
		return fmt.Sprintf("An agent token needs a connection that reads a vault credential as its vorbild. "+
			"Press %d to open Connections.", int(sectionConnections)+1)
	}
	return ""
}

// tokenFields are the rows of the form that creates an agent token: its name, its vorbilder ticked in a
// picker, and its optional expiry. A token is never edited; it is revoked and created anew.
//
// A token filled from source (see template.go) starts with that token's copy name, its vorbilder that still
// read a vault credential, and its expiry only while it still lies ahead; its value is always a new one.
func (m *Model) tokenFields(source string) []field {
	var name, expires string
	vorbilder := field{label: vorbilderLabel, kind: fieldToolList, choices: m.vorbildChoices(),
		selected: map[string]bool{}, hint: vorbilderHint}
	if source != "" {
		name = m.copyName(source)
		if entry, ok, _ := m.tokenByName(source); ok {
			for _, model := range entry.token.Models {
				if slices.Contains(vorbilder.choices, model) {
					vorbilder.selected[model] = true
				}
			}
			expires = futureExpiryText(entry.token.Expires, time.Now())
		}
	}
	return []field{textField("name", name, false).withHint(tokenNameHint), vorbilder,
		textField(expiresLabel, expires, false).withHint(expiresHint)}
}

// futureExpiryText writes an expiry the way the expires row reads it, or "" for none or one that has passed.
func futureExpiryText(at *time.Time, now time.Time) string {
	if at == nil || !at.After(now) {
		return ""
	}
	local := at.Local()
	if local.Hour() == 0 && local.Minute() == 0 && local.Second() == 0 {
		return local.Format("2006-01-02")
	}
	return local.Format(time.RFC3339)
}

// newToken is n on the Tokens list: it opens the form of a new agent token in an admin session only, like
// every other action on a token (adr-0002).
func (m *Model) newToken() tea.Cmd {
	return m.requireAdmin(func() tea.Cmd { return m.openForm("") })
}

// parseTokenExpiry reads the expires row: empty for no expiry, else a date, meaning its start in local time,
// or an RFC 3339 time, which must lie ahead of now. It is the rule 'qatlas vault token create --expires'
// follows too.
func parseTokenExpiry(text string, now time.Time) (*time.Time, error) {
	if text == "" {
		return nil, nil
	}
	at, err := time.ParseInLocation("2006-01-02", text, time.Local)
	if err != nil {
		if at, err = time.Parse(time.RFC3339, text); err != nil {
			return nil, errors.New("expires takes a date as YYYY-MM-DD or an RFC 3339 time")
		}
	}
	if !at.After(now) {
		return nil, errors.New("expires lies in the past; the token would never work")
	}
	return &at, nil
}

// tokenActionMsg carries the outcome of createToken or revokeToken back into the event loop. It names the
// token and never carries its value.
type tokenActionMsg struct {
	created string
	revoked string
	err     error
}

// createToken creates the token the form describes, once requireAdmin proved the admin session. What the
// form can check itself (the name, at least one vorbild that still reads a vault credential, the expiry) is
// refused on the form with every input kept; the vault write runs as a command, off the event loop.
func (m *Model) createToken() tea.Cmd {
	if m.vaultBusy {
		m.status = "a vault write is already in progress; wait for it to finish"
		return nil
	}
	m.clearMessages()
	name := m.fieldValue("name")
	if err := vault.CheckTokenName(name); err != nil {
		m.fail = err.Error()
		return nil
	}
	var models []string
	if f := m.field(vorbilderLabel); f != nil {
		models = f.marked()
	}
	if len(models) == 0 {
		m.fail = "tick at least one vorbild connection"
		return nil
	}
	for _, model := range models {
		connection, ok := m.cfg.Connections[model]
		if !ok || m.cfg.Credentials[connection.Credential].Type != config.CredentialTypeVault {
			m.fail = fmt.Sprintf("vorbild %s is no connection that reads a vault credential", model)
			return nil
		}
	}
	expires, err := parseTokenExpiry(m.fieldValue(expiresLabel), time.Now())
	if err != nil {
		m.fail = err.Error()
		return nil
	}
	v := m.secrets.Vault()
	if v == nil {
		m.fail = "no vault is configured for this run"
		return nil
	}
	m.vaultBusy = true
	m.writes++
	m.busy = "creating agent token " + name
	return func() tea.Msg {
		// The value the vault returns stays here: the detail screen reads it back only when asked to.
		_, err := v.CreateToken(name, models, expires)
		return tokenActionMsg{created: name, err: err}
	}
}

// revokeToken revokes the named token, once requireAdmin proved the admin session, as a command.
func (m *Model) revokeToken(name string) tea.Cmd {
	if m.vaultBusy {
		m.status = "a vault write is already in progress; wait for it to finish"
		return nil
	}
	v := m.secrets.Vault()
	if v == nil {
		m.fail = "no vault is configured for this run"
		return nil
	}
	m.vaultBusy = true
	m.writes++
	m.busy = "revoking agent token " + name
	return func() tea.Msg { return tokenActionMsg{revoked: name, err: v.RevokeToken(name)} }
}

// handleTokenAction applies the outcome of createToken or revokeToken. A refused create keeps the form open
// with every input and the reason; a created token opens its detail screen, its value still hidden; a
// revoked one returns to the list.
func (m *Model) handleTokenAction(msg tokenActionMsg) tea.Cmd {
	m.vaultBusy = false
	if m.writes > 0 {
		m.writes--
	}
	if m.writes == 0 {
		m.busy = ""
	}
	name := msg.created + msg.revoked
	if msg.err != nil {
		switch {
		case errors.Is(msg.err, vault.ErrTokenExists):
			m.fail = fmt.Sprintf("agent token %s exists already; revoke it first or choose another name", name)
		case errors.Is(msg.err, vault.ErrTokenNotFound):
			m.fail = fmt.Sprintf("agent token %s: %s", name, msg.err)
		default:
			m.fail = m.redactor.Apply(msg.err.Error())
		}
		return nil
	}
	m.tokenReveal = false
	if msg.revoked != "" {
		m.tokenDetail, m.tokenRevoke = "", ""
		cmd := m.returnToList("")
		m.status = "Revoked agent token " + name
		return cmd
	}
	m.fields = nil
	cmd := m.returnToList(name)
	m.tokenDetail = name
	m.screen = screenConfirm
	m.status = "Created agent token " + name + "; s reveals its value"
	return cmd
}

// updateTokenScreens handles the keys of the token detail screen and of the revoke question, both standing
// over the Tokens list like the Approvals detail does. It reports false when neither is open.
func (m *Model) updateTokenScreens(key tea.KeyMsg) (tea.Cmd, bool) {
	if name := m.tokenRevoke; name != "" {
		switch key.String() {
		case "y":
			m.tokenRevoke, m.tokenDetail, m.tokenReveal = "", "", false
			m.screen = screenList
			return m.requireAdmin(func() tea.Cmd { return m.revokeToken(name) }), true
		case "n", "esc":
			// The question returns to where it was asked: the detail screen, or the list.
			m.tokenRevoke = ""
			if m.tokenDetail == "" {
				m.screen = screenList
			}
			m.status = "Cancelled; the token stays"
		case "ctrl+c":
			return m.quit(), true
		}
		return nil, true
	}
	name := m.tokenDetail
	if name == "" {
		return nil, false
	}
	switch key.String() {
	case "s":
		if m.tokenValueShown() {
			m.tokenReveal = false
			return nil, true
		}
		m.clearMessages()
		return m.requireAdmin(func() tea.Cmd {
			m.tokenReveal = true
			return nil
		}), true
	case "x":
		m.tokenRevoke = name
		m.clearMessages()
	case "esc":
		m.tokenDetail, m.tokenReveal = "", false
		m.screen = screenList
		m.clearMessages()
	case "ctrl+c":
		return m.quit(), true
	}
	return nil, true
}

// tokenDetailView draws the detail screen of one token: its value, masked until s reveals it, its vorbilder
// with a warning in words beside every one that covers nothing now, its expiry, and when it was created.
func (m *Model) tokenDetailView() string {
	name := m.tokenDetail
	title := m.wrapped(titleStyle, "Token "+name) + "\n\n"
	entry, ok, unavailable := m.tokenByName(name)
	if unavailable != "" {
		return title + m.wrapped(failStyle, "error: "+unavailable) + m.hint("esc back")
	}
	if !ok {
		return title + m.wrapped(hintStyle, "this token no longer exists; nothing to show") + m.hint("esc back")
	}
	var b strings.Builder
	b.WriteString(title)
	value, own := strings.Repeat("●", 24)+"  (s to reveal)", ""
	if m.tokenValueShown() {
		if v := m.tokenValue(name); v != "" {
			value = v + "  (s to hide)"
			if formIndent+lipgloss.Width(value) > m.usable(0) {
				// Too narrow for the row: the value takes a line of its own, so it is not broken where it
				// could be copied whole.
				value, own = "(s to hide)", v
			}
		}
	}
	b.WriteString(m.formRow(false, "value", value) + "\n")
	if own != "" {
		b.WriteString(m.indentedWith(lipgloss.NewStyle(), own) + "\n")
	}
	lapsed := map[string]bool{}
	for _, model := range entry.lapsed {
		lapsed[model] = true
	}
	models := make([]string, len(entry.token.Models))
	for i, model := range entry.token.Models {
		models[i] = model
		if lapsed[model] {
			models[i] = warningStyle.Render("⚠ " + model + " (" + tokenLapsedText + ")")
		}
	}
	b.WriteString(m.formRow(false, vorbilderLabel, strings.Join(models, "\n")) + "\n")
	if entry.checkErr != "" {
		b.WriteString(m.indentedWith(warningStyle, "warning: the vorbilder could not be checked: "+
			entry.checkErr) + "\n")
	}
	expires := tokenExpiry(entry.token)
	if entry.token.Expired(time.Now()) {
		expires += "  " + warningStyle.Render("⚠ expired, approves nothing")
	}
	b.WriteString(m.formRow(false, expiresLabel, expires) + "\n")
	b.WriteString(m.formRow(false, "created", entry.token.Created.Local().Format("2006-01-02 15:04")) + "\n")
	b.WriteString(m.hint("s reveal/hide · x revoke · esc back"))
	return b.String()
}

// tokenRevokeView draws the question asked before a token is revoked.
func (m *Model) tokenRevokeView() string {
	var b strings.Builder
	b.WriteString(m.wrapped(titleStyle, "Revoke agent token "+m.tokenRevoke+"?") + "\n\n")
	b.WriteString(m.indented("it stops working at once, in a running vault process too; approvals it gave stay") +
		"\n")
	b.WriteString(m.hint("y revoke · n/esc keep"))
	return b.String()
}

// tokenFormNotes are the lines under the rows of the token form: what a vorbild means, said once for all.
func (m *Model) tokenFormNotes() string {
	return "\n" + m.indentedWith(hintStyle, vorbildNote) + "\n" + m.indentedWith(hintStyle, vorbildSelfNote) + "\n"
}
