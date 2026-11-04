// Package envvar resolves the user-facing environment variables of the program. Their names carry the
// prefix QCONNECT_. Until the next major release the former prefix QATLAS_ is read as well, when the
// variable with the new prefix is not set, and a warning on the configured writer names the new variable.
//
// The package owns no output stream: the process entry point hands it the writer for warnings with
// SetWarnWriter, and without one a fallback stays silent. A warning names variables, never values.
package envvar

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

const (
	// Prefix is the prefix of every user-facing variable.
	Prefix = "QCONNECT_"
	// legacyPrefix is the former prefix, read as a fallback until the next major release.
	legacyPrefix = "QATLAS_"
)

var (
	mu     sync.Mutex
	warnTo io.Writer
	warned = map[string]bool{}
)

// SetWarnWriter sets where fallback warnings go and forgets which variables were already reported. It
// returns a function that restores the previous writer and state.
func SetWarnWriter(w io.Writer) (restore func()) {
	mu.Lock()
	defer mu.Unlock()
	prevTo, prevWarned := warnTo, warned
	warnTo, warned = w, map[string]bool{}
	return func() {
		mu.Lock()
		defer mu.Unlock()
		warnTo, warned = prevTo, prevWarned
	}
}

// renamed lists the variables whose former name differs by more than the prefix.
var renamed = map[string]string{Prefix + "HOME": legacyPrefix + "CLI_HOME"}

// Legacy returns the former name of a variable with the current prefix, or "" for any other name.
func Legacy(name string) string {
	if old, ok := renamed[name]; ok {
		return old
	}
	rest, ok := strings.CutPrefix(name, Prefix)
	if !ok {
		return ""
	}
	return legacyPrefix + rest
}

// Get returns the value of the variable name, which carries the prefix QCONNECT_. An empty value counts as
// not set. When the variable is not set, the one with the former prefix is used and reported once.
func Get(name string) string { return GetWith(os.Getenv, name) }

// GetWith is Get over an explicit environment lookup.
func GetWith(getenv func(string) string, name string) string {
	if v := getenv(name); v != "" {
		return v
	}
	old := Legacy(name)
	if old == "" {
		return ""
	}
	v := getenv(old)
	if v != "" {
		Warn(old, name)
	}
	return v
}

// Set reports whether the variable name or its former name carries a value, without a warning.
func Set(name string) bool {
	if os.Getenv(name) != "" {
		return true
	}
	old := Legacy(name)
	return old != "" && os.Getenv(old) != ""
}

// Warn reports once per process that old was used where name is the current spelling.
func Warn(old, name string) {
	mu.Lock()
	defer mu.Unlock()
	if warnTo == nil || warned[old] {
		return
	}
	warned[old] = true
	fmt.Fprintf(warnTo, "qconnect: warning: the environment variable %s is deprecated and read only until the next "+
		"major release; use %s\n", old, name)
}
