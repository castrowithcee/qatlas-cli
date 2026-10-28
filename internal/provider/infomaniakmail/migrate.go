package infomaniakmail

import (
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// migrateTargets carries the folder targets of a configuration of an older schema over to their current form
// with the same reach. There, folder/PATH meant exactly that folder and folder/PATH/* the folder with its
// subfolders. folder/PATH/* becomes folder/PATH, and folder/PATH stays and gains the exclusion folder/PATH/*.
// Every other target keeps its spelling.
//
// A connection is refused when the added exclusion would take a folder from another target, that is when a
// former exact folder has another folder target that may lie below it (its name begins with the folder's name
// and is longer, whatever the server's delimiter) or lies below or is a former folder/PATH/* target. It is
// also refused with a folder exclusion, whose former meaning the current forms do not keep.
func migrateTargets(targets, exclude []string) (config.TargetMigration, bool) {
	type folderTarget struct {
		value    string
		children bool
	}
	var folders []folderTarget
	for _, raw := range targets {
		if kind, value, err := parseTarget(strings.TrimSpace(raw)); err == nil && (kind == "folder" || kind == "children") {
			folders = append(folders, folderTarget{value: inboxFolded(value), children: kind == "children"})
		}
	}
	for _, raw := range exclude {
		if kind, _, err := parseTarget(strings.TrimSpace(raw)); err == nil && (kind == "folder" || kind == "children") {
			return config.TargetMigration{}, false
		}
	}
	out := config.TargetMigration{Targets: make([]string, 0, len(targets)), Exclude: slices.Clone(exclude)}
	for _, raw := range targets {
		trimmed := strings.TrimSpace(raw)
		kind, value, err := parseTarget(trimmed)
		switch {
		case err != nil || (kind != "folder" && kind != "children"):
			out.Targets = append(out.Targets, raw)
		case kind == "children":
			out.Targets = append(out.Targets, strings.TrimSuffix(trimmed, childSuffix))
		default:
			exact := inboxFolded(value)
			for _, other := range folders {
				if other.value == exact && !other.children {
					continue
				}
				if other.value == exact || longerPrefix(other.value)(exact) ||
					other.children && longerPrefix(exact)(other.value) {
					return config.TargetMigration{}, false
				}
			}
			out.Targets = append(out.Targets, trimmed)
			out.Exclude = append(out.Exclude, trimmed+childSuffix)
		}
	}
	return out, true
}
