package infomaniakdrive

import (
	"errors"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The target kinds a connection may combine. Every value is a plain positive integer, the form
// Infomaniak itself uses for every account and drive identifier, so a target is never a free-form string
// that could be mistaken for a path or a URL.
const (
	accountPrefix = "account/"
	drivePrefix   = "drive/"
	// maxTargetIDDigits bounds a configured identifier well above any realistic Infomaniak account or drive
	// ID, so a malformed value fails configuration validation with a clear reason instead of overflowing.
	maxTargetIDDigits = 18
)

// scope is the account, drive, and subtree boundary of one connection: exactly one bound account, and
// either every drive of it (no drive and no files target) or the union of the drives listed (the whole
// drive) and the files targets listed (only the folder or file with everything below it).
type scope struct {
	accountID int64
	drives    []int64
	files     []fileTarget
}

// fileTarget is one folder or file of one drive a connection is limited to. A file identifier is unique
// only inside its drive.
type fileTarget struct {
	driveID, fileID int64
}

// unrestricted reports whether the connection names neither a drive nor a files target.
func (s scope) unrestricted() bool { return len(s.drives) == 0 && len(s.files) == 0 }

// allowsDrive reports whether a drive belongs to this connection's boundary, wholly or as the drive of a
// files target.
func (s scope) allowsDrive(driveID int64) bool {
	if s.wholeDrive(driveID) {
		return true
	}
	for _, target := range s.files {
		if target.driveID == driveID {
			return true
		}
	}
	return false
}

// wholeDrive reports whether the connection reaches everything in a drive, not only a subtree of it.
func (s scope) wholeDrive(driveID int64) bool {
	if s.unrestricted() {
		return true
	}
	for _, allowed := range s.drives {
		if allowed == driveID {
			return true
		}
	}
	return false
}

// subtreeRoots returns the files targets of one drive.
func (s scope) subtreeRoots(driveID int64) []int64 {
	var roots []int64
	for _, target := range s.files {
		if target.driveID == driveID {
			roots = append(roots, target.fileID)
		}
	}
	return roots
}

// isSubtreeRoot reports whether a file is itself a files target of its drive.
func (s scope) isSubtreeRoot(driveID, fileID int64) bool {
	for _, target := range s.files {
		if target.driveID == driveID && target.fileID == fileID {
			return true
		}
	}
	return false
}

// scopeOf reads the bound account and drive allow-list of one connection.
func scopeOf(resolved *config.Resolved) (scope, error) {
	values := provider.TargetsOf(resolved)
	return parseAllowlist(values)
}

// parseAllowlist reads the configured targets of one connection: exactly one account/ACCOUNT_ID, and zero or
// more drive/DRIVE_ID and drive/DRIVE_ID/files/FILE_ID entries, none named twice. No error ever quotes a
// configured value.
func parseAllowlist(values []string) (scope, error) {
	var bound scope
	accountSet := false
	seenDrives := map[int64]bool{}
	seenFiles := map[fileTarget]bool{}
	for _, raw := range values {
		target, err := parseAnyTarget(strings.TrimSpace(raw))
		if err != nil {
			return scope{}, err
		}
		switch target.kind {
		case "account":
			if accountSet {
				return scope{}, errors.New("an Infomaniak connection may bind exactly one account target")
			}
			bound.accountID, accountSet = target.id, true
		case "drive":
			if seenDrives[target.id] {
				return scope{}, errors.New("the Infomaniak drive target list names a drive more than once")
			}
			seenDrives[target.id] = true
			bound.drives = append(bound.drives, target.id)
		case "files":
			file := fileTarget{driveID: target.id, fileID: target.fileID}
			if seenFiles[file] {
				return scope{}, errors.New("the Infomaniak target list names a file or folder more than once")
			}
			seenFiles[file] = true
			bound.files = append(bound.files, file)
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
	_, err := parseAnyTarget(strings.TrimSpace(raw))
	return err
}

// target is one parsed configured target. id is the account or drive identifier; fileID is set for a files
// target only.
type target struct {
	kind   string
	id     int64
	fileID int64
}

const targetForms = "an Infomaniak target must be account/ACCOUNT_ID, drive/DRIVE_ID, or drive/DRIVE_ID/files/FILE_ID"

// parseAnyTarget reads one configured target as account/ACCOUNT_ID, drive/DRIVE_ID, or
// drive/DRIVE_ID/files/FILE_ID.
func parseAnyTarget(raw string) (target, error) {
	segments := strings.Split(raw, "/")
	switch {
	case len(segments) == 2 && segments[0] == "account":
		id, err := positiveID(segments[1])
		return target{kind: "account", id: id}, err
	case len(segments) == 2 && segments[0] == "drive":
		id, err := positiveID(segments[1])
		return target{kind: "drive", id: id}, err
	case len(segments) == 4 && segments[0] == "drive" && segments[2] == "files":
		id, err := positiveID(segments[1])
		if err != nil {
			return target{}, err
		}
		fileID, err := positiveID(segments[3])
		return target{kind: "files", id: id, fileID: fileID}, err
	case strings.HasPrefix(raw, accountPrefix) || strings.HasPrefix(raw, drivePrefix):
		return target{}, errors.New("an Infomaniak target ID must be a positive integer")
	default:
		return target{}, errors.New(targetForms)
	}
}

// parseTarget reads one configured target as account/ACCOUNT_ID or drive/DRIVE_ID.
func parseTarget(raw string) (kind string, id int64, err error) {
	parsed, err := parseAnyTarget(raw)
	if err != nil {
		return "", 0, err
	}
	if parsed.kind == "files" {
		return "", 0, errors.New("an Infomaniak target must be account/ACCOUNT_ID or drive/DRIVE_ID")
	}
	return parsed.kind, parsed.id, nil
}

func positiveID(raw string) (int64, error) {
	if !validPositiveID(raw) {
		return 0, errors.New("an Infomaniak target ID must be a positive integer")
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, errors.New("an Infomaniak target ID must be a positive integer")
	}
	return value, nil
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
