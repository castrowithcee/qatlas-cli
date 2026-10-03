package web

import (
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// recordConnections logs one entry per connection that differs between before and after, once after was
// saved to the configuration file, and returns the warning to show when that failed ("" otherwise). A log
// failure never undoes the save.
func (s *Server) recordConnections(before, after *config.Config) string {
	if len(connlog.Changes(before, after)) == 0 {
		return ""
	}
	recorder := connlog.NewRecorder(connlog.SurfaceWeb, s.store.Path(), after.LogRetentionDays(),
		s.secrets.Vault(), vaultproc.Supported)
	if err := recorder.Record(before, after); err != nil {
		return "warning: " + s.redact(err.Error())
	}
	return ""
}

// withWarning joins a notice and a warning for the one-time hint of the next page.
func withWarning(notice, warning string) string {
	switch {
	case warning == "":
		return notice
	case notice == "":
		return warning
	}
	return warning + "; " + notice
}
