package tui

import (
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// recordConnections logs one entry per connection that differs between before and after, once after was
// saved to the configuration file, and returns the warning to show when that failed ("" otherwise). A log
// failure never undoes the save.
func recordConnections(store *config.Store, secrets Secrets, before, after *config.Config) string {
	if len(connlog.Changes(before, after)) == 0 {
		return ""
	}
	recorder := connlog.NewRecorder(connlog.SurfaceTUI, store.Path(), after.LogRetentionDays(), secrets.Vault(),
		vaultproc.Supported)
	if err := recorder.Record(before, after); err != nil {
		return "warning: " + err.Error()
	}
	return ""
}

// addWarning adds a warning from recordConnections to the status line.
func (m *Model) addWarning(warning string) {
	if warning != "" {
		m.status += "; " + warning
	}
}
