//go:build windows

package i18n

import "golang.org/x/sys/windows"

// detect asks Windows for the preferred UI languages, most preferred first, and takes the first one this
// package supports.
func detect() Lang {
	names, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil {
		return English
	}
	for _, name := range names {
		if lang, ok := Parse(name); ok {
			return lang
		}
	}
	return English
}
