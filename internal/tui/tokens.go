// The Tokens section (see the sectionTokens comment in tui.go): the agent tokens of an encrypted vault, each
// with its model connections and its expiry, and the screens that create, show, and revoke one.
//
// Every function here that reads or changes a token defers to package vault, the same core 'qatlas vault
// token' uses, and asks package approval which models cover nothing now; nothing here reimplements
// either. A token's value is read from the vault only to draw it on the detail screen, and only while it is
// revealed there in an admin session: it never enters the model, a message, a status, or an error.
package tui

import (
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// The rows of the form that creates an agent token, besides its name.
const (
	vorbilderLabel = "models"
	expiresLabel   = "expires"
)

// tokenLapsedText is what a model that covers nothing now is marked with, symbol and words together.
func tokenLapsedText() string { return tr(tTokenLapsed) }

// tokenEntry is one agent token as the Tokens section shows it: never its value, which only tokenValue
// reads, and only for the revealed detail screen. lapsed names its models that cover nothing now, and
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
		return nil, tr(tNoVaultSentence)
	}
	state, err := v.State()
	if err != nil {
		return nil, m.redactor.Apply(err.Error())
	}
	switch state {
	case vault.StateAbsent, vault.StateUnencrypted:
		return nil, tr(tTokensNeedVault, sectionVault.ref())
	case vault.StateLocked:
		return nil, tr(tTokensLocked)
	}
	tokens, err := v.Tokens()
	if err != nil {
		return nil, m.redactor.Apply(err.Error())
	}
	entries = make([]tokenEntry, 0, len(tokens))
	for _, token := range tokens {
		entry := tokenEntry{token: token}
		entry.lapsed, err = approval.LapsedModels(m.cfg, v, token, m.timeNow())
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
// never rests on colour alone; "" when nothing keeps the token from approving what its models allow.
func tokenWarning(entry tokenEntry) string {
	var parts []string
	if entry.token.Expired(time.Now()) {
		parts = append(parts, tr(tTokenExpired))
	}
	switch n := len(entry.lapsed); {
	case entry.checkErr != "":
		parts = append(parts, tr(tTokenNotChecked))
	case n == 1:
		parts = append(parts, tr(tTokenCoversOne))
	case n > 1:
		parts = append(parts, tr(tTokenCoversMany, n))
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
		return tr(tTokenNoExpiry)
	}
	at := token.Expires.Local()
	if at.Hour() == 0 && at.Minute() == 0 && at.Second() == 0 {
		return at.Format("2006-01-02")
	}
	return at.Format("2006-01-02 15:04")
}

// tokenCells are the cells of one token in the Tokens list: its name with any warning behind it, since the
// first column is never cut, its expiry, and its models.
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

// vorbildChoices are the connections a token may name as a model: every one that reads a vault credential,
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
// connection could be a model. A locked vault does not block it; requireAdmin unlocks it first.
func (m *Model) tokensBlocked() string {
	_, unavailable := m.tokensReport()
	if unavailable != "" && !m.vaultLocked() {
		return unavailable
	}
	if len(m.vorbildChoices()) == 0 {
		return tr(tTokensNeedVorbild, int(sectionConnections.area())+1)
	}
	return ""
}

// tokenFields are the rows of the form that creates an agent token: its name, its models ticked in a
// picker, and its optional expiry. A token is never edited; it is revoked and created anew.
//
// A token filled from source (see template.go) starts with that token's copy name, its models that still
// read a vault credential, and its expiry only while it still lies ahead; its value is always a new one.
func (m *Model) tokenFields(source string) []field {
	var name, expires string
	vorbilder := field{label: vorbilderLabel, kind: fieldToolList, choices: m.vorbildChoices(),
		selected: map[string]bool{}, hint: tr(tVorbilderHint), detail: tr(tVorbilderDetail)}
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
	return []field{textField("name", name, false).withHint(tr(tTokenNameHint)).withDetail(tr(tTokenNameDetail)), vorbilder,
		textField(expiresLabel, expires, false).withHint(tr(tExpiresHint)).withDetail(tr(tExpiresDetail))}
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

// parseTokenExpiry reads the expires row through the shared expiry rule, which 'qatlas vault token create
// --expires' follows too.
func parseTokenExpiry(text string, now time.Time) (*time.Time, error) {
	at, err := config.ParseExpiry(text, now)
	switch {
	case errors.Is(err, config.ErrExpiryFormat):
		return nil, errors.New(tr(tExpiresFormat))
	case errors.Is(err, config.ErrExpiryPast):
		return nil, errors.New(tr(tExpiresPast))
	}
	return at, err
}

// tokenActionMsg carries the outcome of createToken or revokeToken back into the event loop. It names the
// token and never carries its value.
type tokenActionMsg struct {
	created string
	revoked string
	err     error
}

// createToken creates the token the form describes, once requireAdmin proved the admin session. What the
// form can check itself (the name, at least one model that still reads a vault credential, the expiry) is
// refused on the form with every input kept; the vault write runs as a command, off the event loop.
func (m *Model) createToken() tea.Cmd {
	if m.vaultBusy {
		m.status = tr(tVaultWriteBusy)
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
		m.fail = tr(tTokenTickOne)
		return nil
	}
	for _, model := range models {
		connection, ok := m.cfg.Connections[model]
		if !ok || m.cfg.Credentials[connection.Credential].Type != config.CredentialTypeVault {
			m.fail = tr(tTokenVorbildInvalid, model)
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
		m.fail = tr(tVaultNone)
		return nil
	}
	m.vaultBusy = true
	m.writes++
	m.busy = tr(tBusyCreatingToken, name)
	return func() tea.Msg {
		// The value the vault returns stays here: the detail screen reads it back only when asked to.
		_, err := v.CreateToken(name, models, expires)
		return tokenActionMsg{created: name, err: err}
	}
}

// revokeToken revokes the named token, once requireAdmin proved the admin session, as a command.
func (m *Model) revokeToken(name string) tea.Cmd {
	if m.vaultBusy {
		m.status = tr(tVaultWriteBusy)
		return nil
	}
	v := m.secrets.Vault()
	if v == nil {
		m.fail = tr(tVaultNone)
		return nil
	}
	m.vaultBusy = true
	m.writes++
	m.busy = tr(tBusyRevokingToken, name)
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
			m.fail = tr(tTokenExists, name)
		case errors.Is(msg.err, vault.ErrTokenNotFound):
			m.fail = tr(tTokenError, name, msg.err)
		default:
			m.fail = m.redactor.Apply(msg.err.Error())
		}
		return nil
	}
	m.tokenReveal = false
	if msg.revoked != "" {
		m.tokenDetail, m.tokenRevoke = "", ""
		cmd := m.returnToList("")
		m.status = tr(tTokenRevoked, name)
		return cmd
	}
	m.fields = nil
	cmd := m.returnToList(name)
	m.tokenDetail = name
	m.screen = screenConfirm
	m.status = tr(tTokenCreated, name)
	return cmd
}

// updateTokenScreens handles the keys of the token detail screen and of the revoke question, both standing
// over the Tokens list like the Approvals detail does. It reports false when neither is open.
func (m *Model) updateTokenScreens(key tea.KeyPressMsg) (tea.Cmd, bool) {
	if name := m.tokenRevoke; name != "" {
		switch {
		case keyIs(key, actYes):
			m.tokenRevoke, m.tokenDetail, m.tokenReveal = "", "", false
			m.screen = screenList
			return m.requireAdmin(func() tea.Cmd { return m.revokeToken(name) }), true
		case keyIs(key, actNo) || keyIs(key, actBack):
			// The question returns to where it was asked: the detail screen, or the list.
			m.tokenRevoke = ""
			if m.tokenDetail == "" {
				m.screen = screenList
			}
			m.status = tr(tTokenStays)
		}
		return nil, true
	}
	name := m.tokenDetail
	if name == "" {
		return nil, false
	}
	switch {
	case keyIs(key, actReveal):
		if m.tokenValueShown() {
			m.tokenReveal = false
			return nil, true
		}
		m.clearMessages()
		return m.requireAdmin(func() tea.Cmd {
			m.tokenReveal = true
			return nil
		}), true
	case keyIs(key, actDelete):
		m.tokenRevoke = name
		m.clearMessages()
	case keyIs(key, actBack):
		m.tokenDetail, m.tokenReveal = "", false
		m.screen = screenList
		m.clearMessages()
	}
	return nil, true
}

// tokenDetailView draws the detail screen of one token: its state, its value, masked until v reveals it, its
// models with a warning in words beside every one that covers nothing now, its expiry, and when it was
// created.
func (m *Model) tokenDetailView() string {
	name := m.tokenDetail
	heading := "Token " + name
	title := m.wrapped(styles.title, heading) + "\n\n"
	entry, ok, unavailable := m.tokenByName(name)
	if unavailable != "" {
		return title + m.wrapped(styles.fail, tr(tNoteError, unavailable)) + m.keyHint("{back} back · {command} cmds · {help} help")
	}
	if !ok {
		return title + m.wrapped(styles.hint, tr(tTokenGone)) + m.keyHint("{back} back · {command} cmds · {help} help")
	}
	inner, _ := m.detailInner()
	expired := entry.token.Expired(time.Now())
	state := statusMark(statusOK, tr(tStatusValid))
	if expired {
		state = statusMark(statusFail, tr(tStatusExpired))
	}
	fact := func(label, value string, style lipgloss.Style) styledFact {
		return styledFact{detailFact: detailFact{label: labelText(label), value: value}, style: style}
	}
	facts := m.statusFact(state)
	column := factColumn(append(facts, fact("value", "x", styles.hint), fact(vorbilderLabel, "x", styles.hint),
		fact(expiresLabel, "x", styles.hint), fact("created", "x", styles.hint)), inner)
	value := strings.Repeat("●", 24) + "  " + tr(tTokenReveal)
	if m.tokenValueShown() {
		if v := m.tokenValue(name); v != "" {
			if column+lipgloss.Width(v)+2+lipgloss.Width(tr(tTokenHide)) <= inner {
				value = v + "  " + tr(tTokenHide)
			} else {
				// Too narrow for the row: the value takes a line of its own, so it is not broken where it
				// could be copied whole.
				facts = append(facts, fact("value", tr(tTokenHide), styles.hint),
					styledFact{detailFact: detailFact{value: v}, flush: true})
				value = ""
			}
		}
	}
	if value != "" {
		facts = append(facts, fact("value", value, lipgloss.NewStyle()))
	}
	lapsed := map[string]bool{}
	for _, model := range entry.lapsed {
		lapsed[model] = true
	}
	for i, model := range entry.token.Models {
		label, style, text := "", lipgloss.NewStyle(), m.logText(model)
		if i == 0 {
			label = labelText(vorbilderLabel)
		}
		if lapsed[model] {
			style, text = styles.warning, "⚠ "+text+" ("+tokenLapsedText()+")"
		}
		facts = append(facts, styledFact{detailFact: detailFact{label: label, value: text}, style: style})
	}
	if entry.checkErr != "" {
		facts = append(facts, styledFact{detailFact: detailFact{value: tr(tTokenCheckFailed, entry.checkErr)}, style: styles.warning})
	}
	facts = append(facts, fact(expiresLabel, tokenExpiry(entry.token), lipgloss.NewStyle()))
	if expired {
		facts = append(facts, styledFact{detailFact: detailFact{value: tr(tTokenExpiredNote)}, style: styles.warning})
	}
	facts = append(facts, fact("created", entry.token.Created.Local().Format("2006-01-02 15:04"), lipgloss.NewStyle()))
	lines := factLinesIn(facts, inner, column, func(s string) string { return s })
	return m.frameDetail(heading, state, lines) + "\n" + m.keyHint("V reveal/hide · {delete} revoke · {back} back · {command} cmds · {help} help")
}

// tokenRevokeView draws the question asked before a token is revoked.
func (m *Model) tokenRevokeView() string {
	return m.questionText(question{
		title:       tr(tTokenRevokeTitle, m.tokenRevoke),
		consequence: tr(tTokenRevokeConsequence),
		danger:      true,
		keys:        "y revoke · n/{back} keep · {command} cmds · {help} help",
	})
}
