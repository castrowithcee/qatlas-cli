package infomaniakdrive

import (
	"errors"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// The two target kinds a connection may combine. Every value is a plain positive integer, the form
// Infomaniak itself uses for every account and drive identifier, so a target is never a free-form string
// that could be mistaken for a path or a URL.
const (
	accountPrefix = "account/"
	drivePrefix   = "drive/"
	// maxTargetIDDigits bounds a configured identifier well above any realistic Infomaniak account or drive
	// ID, so a malformed value fails configuration validation with a clear reason instead of overflowing.
	maxTargetIDDigits = 18
)

// scope is the account and drive boundary of one connection: exactly one bound account, and either every
// drive of it (an empty allow-list) or only the drives explicitly listed.
type scope struct {
	accountID int64
	drives    []int64
}

// allowsDrive reports whether a drive belongs to this connection's boundary.
func (s scope) allowsDrive(driveID int64) bool {
	if len(s.drives) == 0 {
		return true
	}
	for _, allowed := range s.drives {
		if allowed == driveID {
			return true
		}
	}
	return false
}

// scopeOf reads the bound account and drive allow-list of one connection.
func scopeOf(resolved *config.Resolved) (scope, error) {
	values := resolved.Targets
	if len(values) == 0 && strings.TrimSpace(resolved.Target) != "" {
		values = []string{resolved.Target}
	}
	return parseAllowlist(values)
}

// parseAllowlist reads the configured targets of one connection: exactly one account/ACCOUNT_ID, and zero or
// more drive/DRIVE_ID entries, none named twice. No error ever quotes a configured value.
func parseAllowlist(values []string) (scope, error) {
	var bound scope
	accountSet := false
	seenDrives := map[int64]bool{}
	for _, raw := range values {
		kind, id, err := parseTarget(strings.TrimSpace(raw))
		if err != nil {
			return scope{}, err
		}
		switch kind {
		case "account":
			if accountSet {
				return scope{}, errors.New("an Infomaniak connection may bind exactly one account target")
			}
			bound.accountID, accountSet = id, true
		case "drive":
			if seenDrives[id] {
				return scope{}, errors.New("the Infomaniak drive target list names a drive more than once")
			}
			seenDrives[id] = true
			bound.drives = append(bound.drives, id)
		}
	}
	if !accountSet {
		return scope{}, errors.New("an Infomaniak connection needs exactly one account/ACCOUNT_ID target")
	}
	return bound, nil
}

// validateTarget checks the form of one configured target in isolation, before configuration validation
// checks the whole set with parseAllowlist.
func validateTarget(raw string) error {
	_, _, err := parseTarget(strings.TrimSpace(raw))
	return err
}

// parseTarget reads one configured target as account/ACCOUNT_ID or drive/DRIVE_ID.
func parseTarget(raw string) (kind string, id int64, err error) {
	switch {
	case strings.HasPrefix(raw, accountPrefix):
		kind, raw = "account", strings.TrimPrefix(raw, accountPrefix)
	case strings.HasPrefix(raw, drivePrefix):
		kind, raw = "drive", strings.TrimPrefix(raw, drivePrefix)
	default:
		return "", 0, errors.New("an Infomaniak target must be account/ACCOUNT_ID or drive/DRIVE_ID")
	}
	if !validPositiveID(raw) {
		return "", 0, errors.New("an Infomaniak target ID must be a positive integer")
	}
	value, convErr := strconv.ParseInt(raw, 10, 64)
	if convErr != nil {
		return "", 0, errors.New("an Infomaniak target ID must be a positive integer")
	}
	return kind, value, nil
}

// validPositiveID keeps a target identifier to plain, non-padded decimal digits: never a sign, a leading
// zero, a separator, or anything that could change how it is read.
func validPositiveID(raw string) bool {
	if raw == "" || len(raw) > maxTargetIDDigits || raw[0] == '0' {
		return false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// selectDrive checks a drive_id argument against the connection's bound scope before any secret is resolved
// and before any request is sent. A drive outside the connection's allow-list is refused as an invalid
// request, never as a provider failure, because the connection's own configuration decided against it.
func selectDrive(resolved *config.Resolved, driveID int64) error {
	if resolved == nil {
		return providerError("open", "no connection was selected")
	}
	bound, err := scopeOf(resolved)
	if err != nil {
		return providerError("open", err.Error())
	}
	if driveID <= 0 {
		return invalidRequest("drive_id must be a positive integer")
	}
	if !bound.allowsDrive(driveID) {
		return invalidRequest("drive_id is outside the targets of this connection")
	}
	return nil
}
