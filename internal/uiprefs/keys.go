package uiprefs

import (
	"fmt"
	"regexp"
	"strings"
)

// Names of the global actions whose key can be changed.
const (
	ActionInfo    = "info"
	ActionSave    = "save"
	ActionHelp    = "help"
	ActionCommand = "command"
	ActionLock    = "lock"
)

// Actions lists the rebindable actions in the order they are shown, saved, and checked.
var Actions = []string{ActionSave, ActionHelp, ActionCommand, ActionLock, ActionInfo}

// Keys are the keys of the global actions, named as the terminal library reports them ("f3", "ctrl+g",
// "?"). Info is the exception: it always keeps its letter and Info is a second key beside it, empty by
// default; every other action has exactly one key, which a change replaces.
type Keys struct {
	Info    string
	Save    string
	Help    string
	Command string
	Lock    string
}

// Reserved, when set, names the action that owns a key no binding may take, or "" when the key is free. The
// editor sets it to its key table, so a key it gives to any other action cannot be bound here.
var Reserved func(key string) string

// Fixed keys of other actions and of the editor that no binding may take.
const (
	infoLetter = "i"
	backKey    = "esc"
	deleteKey  = "d"
)

// DefaultKeys returns the keys of a device without a keys section.
func DefaultKeys() Keys {
	return Keys{Save: "f2", Help: "?", Command: ":", Lock: "ctrl+l"}
}

// WithDefaults fills the actions that have no key with their default. Info stays empty on purpose.
func (k Keys) WithDefaults() Keys {
	d := DefaultKeys()
	if k.Save == "" {
		k.Save = d.Save
	}
	if k.Help == "" {
		k.Help = d.Help
	}
	if k.Command == "" {
		k.Command = d.Command
	}
	if k.Lock == "" {
		k.Lock = d.Lock
	}
	return k
}

// Get returns the key of an action.
func (k Keys) Get(action string) string {
	switch action {
	case ActionInfo:
		return k.Info
	case ActionSave:
		return k.Save
	case ActionHelp:
		return k.Help
	case ActionCommand:
		return k.Command
	case ActionLock:
		return k.Lock
	}
	return ""
}

// With returns k with the key of one action replaced.
func (k Keys) With(action, key string) Keys {
	switch action {
	case ActionInfo:
		k.Info = key
	case ActionSave:
		k.Save = key
	case ActionHelp:
		k.Help = key
	case ActionCommand:
		k.Command = key
	case ActionLock:
		k.Lock = key
	}
	return k
}

// Validate checks every key of k against the rules and against each other, and names the first problem.
func (k Keys) Validate() error {
	for _, a := range Actions {
		if _, err := k.Check(a, k.Get(a)); err != nil {
			return err
		}
	}
	return nil
}

var (
	functionKey = regexp.MustCompile(`^f([0-9]{1,2})$`)
	ctrlKey     = regexp.MustCompile(`^ctrl\+[a-z]$`)
)

// interceptedF are the function keys terminals and window systems take for themselves.
var interceptedF = map[int]bool{1: true, 5: true, 10: true, 11: true, 12: true}

// freeCtrl are the control keys no text line or terminal needs: the others edit text (ctrl+a, e, k, u, w),
// stand for enter, tab, or backspace (ctrl+m, i, h), or control the terminal (ctrl+s, q, z).
var freeCtrl = map[string]bool{"ctrl+g": true, "ctrl+l": true, "ctrl+o": true, "ctrl+r": true, "ctrl+t": true, "ctrl+x": true}

// Normalize returns the spelling the terminal library reports for key: trimmed, and lower case for names
// such as F3 or Ctrl+G; a single character keeps its case.
func Normalize(key string) string {
	key = strings.TrimSpace(key)
	if len([]rune(key)) > 1 {
		key = strings.ToLower(key)
	}
	return key
}

// Check says whether key may carry action in k, where k holds the keys of the other actions. It returns
// the normalized key, or an error that names the reason. An empty key is valid for Info only (no second
// key).
func (k Keys) Check(action, key string) (string, error) {
	key = Normalize(key)
	if key == "" {
		if action == ActionInfo {
			return "", nil
		}
		return "", fmt.Errorf("%s needs a key", action)
	}
	switch key {
	case "ctrl+c":
		return "", fmt.Errorf("%s: ctrl+c quits the editor", key)
	case backKey:
		return "", fmt.Errorf("%s: esc goes back", key)
	case "tab", "shift+tab":
		return "", fmt.Errorf("%s: tab and shift+tab move between fields", key)
	}
	if owner := k.owner(action, key); owner != "" {
		return "", fmt.Errorf("%s: already the key of %s", key, owner)
	}
	runes := []rune(key)
	switch {
	case len(runes) == 1:
		if err := checkCharacter(action, key, runes[0]); err != nil {
			return "", err
		}
	case functionKey.MatchString(key):
		var n int
		_, _ = fmt.Sscanf(key[1:], "%d", &n)
		if n < 1 || n > 20 {
			return "", fmt.Errorf("%s: function keys go from F1 to F20", key)
		}
		if interceptedF[n] {
			return "", fmt.Errorf("%s: terminals take F1, F5, F10, F11, and F12 for themselves", key)
		}
	case ctrlKey.MatchString(key):
		if !freeCtrl[key] {
			return "", fmt.Errorf("%s: text lines or the terminal use it; free are ctrl+g, l, o, r, t, and x", key)
		}
	default:
		return "", fmt.Errorf("%s: not a single key; use a character, F2 to F20, or ctrl with a letter", key)
	}
	if Reserved != nil {
		if owner := Reserved(key); owner != "" {
			return "", fmt.Errorf("%s: already the key of %s", key, owner)
		}
	}
	return key, nil
}

// checkCharacter applies the rules of a printable key.
func checkCharacter(action, key string, r rune) error {
	switch {
	case r >= '0' && r <= '9':
		return fmt.Errorf("%s: digits choose an area", key)
	case r == ' ' || r < ' ' || r == 0x7f:
		return fmt.Errorf("%s: space and control characters are no binding", key)
	case key == deleteKey:
		return fmt.Errorf("%s: d deletes", key)
	case action == ActionSave || action == ActionLock || action == ActionInfo:
		return fmt.Errorf("%s: %s acts on text lines, where a printable key is typed text", key, action)
	case r == '/':
		return fmt.Errorf("%s: / searches", key)
	case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 0x7f:
		return fmt.Errorf("%s: letters belong to the screens' own keys", key)
	}
	return nil
}

// owner names the action that already uses key, including the keys no action may take.
func (k Keys) owner(action, key string) string {
	if key == infoLetter {
		return ActionInfo
	}
	for _, other := range Actions {
		if other != action && k.Get(other) == key {
			return other
		}
	}
	return ""
}

// keysFile holds the keys that differ from their default, so the file stays free of what is standard.
type keysFile struct {
	Info    string `yaml:"info,omitempty"`
	Save    string `yaml:"save,omitempty"`
	Help    string `yaml:"help,omitempty"`
	Command string `yaml:"command,omitempty"`
	Lock    string `yaml:"lock,omitempty"`
}

func (k Keys) file() *keysFile {
	d := DefaultKeys()
	f := keysFile{Info: k.Info}
	if k.Save != d.Save {
		f.Save = k.Save
	}
	if k.Help != d.Help {
		f.Help = k.Help
	}
	if k.Command != d.Command {
		f.Command = k.Command
	}
	if k.Lock != d.Lock {
		f.Lock = k.Lock
	}
	if f == (keysFile{}) {
		return nil
	}
	return &f
}

// loadKeys applies the keys of a file. Every key is first checked against the rules of its action alone,
// then the final assignment is checked for two actions on one key, so a swap such as save on ctrl+l and
// lock on ctrl+r is valid as a whole. A key that breaks a rule, or collides, keeps the default of its
// action and adds a warning that names the reason; the result is always free of collisions.
func loadKeys(path string, given map[string]*string, warnings []string) (Keys, []string) {
	keys, fromFile := DefaultKeys(), map[string]bool{}
	for _, a := range Actions {
		v := given[a]
		if v == nil {
			continue
		}
		key, err := Keys{}.Check(a, *v)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: keys.%s %v, using %s", path, a, err, describeDefault(a)))
			continue
		}
		keys, fromFile[a] = keys.With(a, key), true
	}
	for changed := true; changed; {
		changed = false
		for i, a := range Actions {
			for _, b := range Actions[i+1:] {
				if keys.Get(a) == "" || keys.Get(a) != keys.Get(b) {
					continue
				}
				for _, x := range []string{a, b} {
					if fromFile[x] {
						warnings = append(warnings, fmt.Sprintf("%s: keys.%s %s is also the key of %s, using %s",
							path, x, keys.Get(x), map[bool]string{true: b, false: a}[x == a], describeDefault(x)))
						keys, fromFile[x], changed = keys.With(x, DefaultKeys().Get(x)), false, true
					}
				}
			}
		}
	}
	if keys.Validate() != nil {
		keys = DefaultKeys()
	}
	return keys, warnings
}

func describeDefault(action string) string {
	if k := DefaultKeys().Get(action); k != "" {
		return k
	}
	return "no extra key"
}
