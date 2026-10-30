// The one-time question of the first unlock with the passphrase after the idle timeout, the admin session
// timeout and the log retention moved into the vault. The rule (whether to ask, what to write, removing the
// old fields from config.yaml with a backup) is manage.Service.PrepareSettingsTakeover; this file only shows
// the values, asks, and reports.
package tui

import "strings"

// beginSettingsTakeover asks the question after a successful 'ctrl+l' unlock. A problem is a status
// warning, never a failure of the unlock. With no value the vault can store nothing is asked and the old
// fields are removed at once.
func (m *Model) beginSettingsTakeover() {
	if m.svc == nil {
		return
	}
	take, notice, err := m.svc.PrepareSettingsTakeover()
	if err != nil {
		m.status = tr(tTakeoverWarning, m.redactor.Apply(err.Error()))
		return
	}
	if notice != "" {
		m.status = tr(tTakeoverWarning, m.redactor.Apply(notice))
	}
	if take == nil {
		return
	}
	m.settingsTakeover, m.settingsTakeoverBack = take, m.screen
	if !take.Ask() {
		m.answerSettingsTakeover(false)
		return
	}
	m.screen = screenConfirm
	m.clearMessages()
}

// answerSettingsTakeover applies the answer and returns to the screen the unlock came from.
func (m *Model) answerSettingsTakeover(yes bool) {
	take, back := m.settingsTakeover, m.settingsTakeoverBack
	m.settingsTakeover, m.screen = nil, back
	res, err := take.Apply(yes)
	switch {
	case err != nil:
		m.status = tr(tTakeoverWarning, m.redactor.Apply(err.Error()))
		return
	case res.Warning != "":
		m.status = tr(tTakeoverWarning, res.Warning)
	case yes:
		m.status = tr(tTakeoverTook, res.Backup)
	default:
		m.status = tr(tTakeoverKept, res.Backup)
	}
	m.RefreshAdminTimeout()
}

func (m *Model) settingsTakeoverView() string {
	var lines []string
	for _, o := range m.settingsTakeover.Offered {
		lines = append(lines, m.indented(o.Field+": "+o.Value))
	}
	return m.questionText(question{
		title:       tr(tTakeoverTitle),
		consequence: tr(tTakeoverConsequence),
		details:     []string{strings.Join(lines, "\n")},
		keys:        "{yes} take over · {no}/{back} keep the vault's settings · {command} cmds · {help} help",
	})
}
