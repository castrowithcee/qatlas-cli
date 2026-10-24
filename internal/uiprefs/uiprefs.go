// Package uiprefs keeps the device-local preferences of the terminal editor in tui.yaml, beside the
// configuration file but apart from it: the file is never part of an export or import and needs no admin
// session. A missing or damaged file yields the defaults and a warning, never an error that stops the
// editor. The last writer wins.
//
// The format is a flat YAML mapping. Keys this version does not know are ignored on read, so later
// versions can add fields without breaking an older reader; a write by this version writes only the keys
// it knows.
package uiprefs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/castrowithcee/qatlas-cli/internal/fileretry"
)

// FileName is the name of the preferences file inside the configuration directory.
const FileName = "tui.yaml"

// Values of Prefs.Theme and Prefs.Icons.
const (
	ThemeAuto  = "auto"
	ThemeLight = "light"
	ThemeDark  = "dark"

	IconsNone     = "none"
	IconsNerdFont = "nerdfont"
)

// DefaultStart is the area the editor opens on by default. Whether another name is an area is the editor's
// to decide; this package only keeps the text.
const DefaultStart = "overview"

// Prefs are the device-local preferences.
type Prefs struct {
	Theme string `yaml:"theme"`
	Start string `yaml:"start"`
	Icons string `yaml:"icons"`
	Keys  Keys   `yaml:"-"`
}

// Default returns the preferences of a device that has none.
func Default() Prefs {
	return Prefs{Theme: ThemeAuto, Start: DefaultStart, Icons: IconsNone, Keys: DefaultKeys()}
}

// Path returns the preferences file that belongs to the configuration directory dir.
func Path(dir string) string { return filepath.Join(dir, FileName) }

// Load reads the preferences at path. It never fails: a missing file gives the defaults without a warning;
// an unreadable or malformed file, or a value that is not valid, gives the default for that part and a
// warning that names it.
func Load(path string) (Prefs, []string) {
	prefs := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return prefs, nil
		}
		return prefs, []string{fmt.Sprintf("cannot read %s, using the defaults: %v", path, err)}
	}
	var raw struct {
		Theme *string `yaml:"theme"`
		Start *string `yaml:"start"`
		Icons *string `yaml:"icons"`
		Keys  *struct {
			Info    *string `yaml:"info"`
			Save    *string `yaml:"save"`
			Help    *string `yaml:"help"`
			Command *string `yaml:"command"`
			Lock    *string `yaml:"lock"`
		} `yaml:"keys"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return prefs, []string{fmt.Sprintf("cannot parse %s, using the defaults: %v", path, err)}
	}
	var warnings []string
	if raw.Theme != nil {
		switch *raw.Theme {
		case ThemeAuto, ThemeLight, ThemeDark:
			prefs.Theme = *raw.Theme
		default:
			warnings = append(warnings, fmt.Sprintf("%s: theme %q is not auto, light or dark, using %s",
				path, *raw.Theme, ThemeAuto))
		}
	}
	if raw.Start != nil {
		if *raw.Start == "" {
			warnings = append(warnings, fmt.Sprintf("%s: start is empty, using %s", path, DefaultStart))
		} else {
			prefs.Start = *raw.Start
		}
	}
	if raw.Icons != nil {
		switch *raw.Icons {
		case IconsNone, IconsNerdFont:
			prefs.Icons = *raw.Icons
		default:
			warnings = append(warnings, fmt.Sprintf("%s: icons %q is not none or nerdfont, using %s",
				path, *raw.Icons, IconsNone))
		}
	}
	if raw.Keys != nil {
		prefs.Keys, warnings = loadKeys(path, map[string]*string{
			ActionInfo: raw.Keys.Info, ActionSave: raw.Keys.Save, ActionHelp: raw.Keys.Help,
			ActionCommand: raw.Keys.Command, ActionLock: raw.Keys.Lock,
		}, warnings)
	}
	return prefs, warnings
}

// Save writes prefs to path through a temporary file and a rename, so a reader sees the old file or the new
// one, never half of one. A value that is not valid is refused and nothing is written.
func Save(path string, prefs Prefs) error {
	switch prefs.Theme {
	case ThemeAuto, ThemeLight, ThemeDark:
	default:
		return fmt.Errorf("theme %q is not auto, light or dark", prefs.Theme)
	}
	switch prefs.Icons {
	case IconsNone, IconsNerdFont:
	default:
		return fmt.Errorf("icons %q is not none or nerdfont", prefs.Icons)
	}
	if prefs.Start == "" {
		return errors.New("start must not be empty")
	}
	keys := prefs.Keys.WithDefaults()
	if err := keys.Validate(); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(struct {
		Prefs `yaml:",inline"`
		Keys  *keysFile `yaml:"keys,omitempty"`
	}{prefs, keys.file()}); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}

	// A file kept in a dotfiles directory may be a symlink; the real file is the target.
	target := path
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".qatlas-tui-*.tmp")
	if err != nil {
		return fmt.Errorf("cannot write next to %s: %w", target, err)
	}
	name := tmp.Name()
	moved := false
	defer func() {
		if !moved {
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cannot write %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cannot write %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cannot write %s: %w", name, err)
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return fmt.Errorf("cannot set the permissions of %s: %w", name, err)
	}
	if err := fileretry.Do(func() error { return os.Rename(name, target) }, fileretry.Transient, nil); err != nil {
		return fmt.Errorf("cannot replace %s: %w", target, err)
	}
	moved = true
	return nil
}
