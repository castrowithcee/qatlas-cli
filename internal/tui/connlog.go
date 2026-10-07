package tui

// addWarning adds a warning from the connection log (see manage.Service.RecordConnections) to the status
// line.
func (m *Model) addWarning(warning string) {
	if warning != "" {
		m.status += "; " + warning
	}
}
