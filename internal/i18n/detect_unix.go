//go:build !windows

package i18n

import "os"

// detect reads the locale variables. macOS has no preferred-language call without cgo, so it uses LANG too
// and falls back to English.
func detect() Lang { return detectFromEnv(os.Getenv) }
