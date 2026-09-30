package seatableaccount

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// maxBaseNameLength bounds a configured base name well above what SeaTable accepts.
const (
	maxBaseNameLength   = 255
	maxWorkspaceIDDigit = 18
)

// boundBase is the one base a connection is bound to: its workspace and its name within it.
type boundBase struct {
	workspaceID string
	name        string
}

// parseTarget reads one WORKSPACE_ID/BASE_NAME target. The workspace ID is a plain positive integer and the
// name is one path segment without separators, control characters, or surrounding blanks, so neither part
// can change how a request path is read. No error quotes the configured value.
func parseTarget(raw string) (boundBase, error) {
	const form = "a SeaTable account target must be WORKSPACE_ID/BASE_NAME"
	workspace, name, ok := strings.Cut(strings.TrimSpace(raw), "/")
	if !ok || !validWorkspaceID(workspace) {
		return boundBase{}, errors.New(form + " with a positive integer workspace ID")
	}
	if !validBaseName(name) {
		return boundBase{}, errors.New(form + " with a plain base name without a slash or control character")
	}
	return boundBase{workspaceID: workspace, name: name}, nil
}

func validWorkspaceID(value string) bool {
	if value == "" || len(value) > maxWorkspaceIDDigit || value[0] == '0' {
		return false
	}
	_, err := strconv.ParseInt(value, 10, 64)
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return err == nil
}

func validBaseName(value string) bool {
	if value == "" || len(value) > maxBaseNameLength || !utf8.ValidString(value) ||
		strings.TrimSpace(value) != value || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// validateSet requires exactly one target.
func validateSet(values []string) error {
	if len(values) != 1 {
		return errors.New("a SeaTable account connection binds exactly one WORKSPACE_ID/BASE_NAME target")
	}
	_, err := parseTarget(values[0])
	return err
}

// baseOf reads the bound base of a selected connection, before any secret is resolved.
func baseOf(resolved *config.Resolved) (boundBase, error) {
	values := resolved.Targets
	if len(values) == 0 && strings.TrimSpace(resolved.Target) != "" {
		values = []string{resolved.Target}
	}
	if err := validateSet(values); err != nil {
		return boundBase{}, err
	}
	return parseTarget(values[0])
}
