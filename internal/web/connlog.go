package web

import "github.com/castrowithcee/qatlas-cli/internal/config"

// loadConfig returns the configuration as the file holds it now.
func (s *Server) loadConfig() (*config.Config, error) {
	cfg, _, err := s.svc.Load()
	return cfg, err
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
