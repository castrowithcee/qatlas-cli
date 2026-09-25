// Package redact removes known secret values from text before it is shown. Whoever resolves a secret
// registers its value here; every diagnostic and error passes through the redactor afterwards.
package redact

import (
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Marker replaces a secret value.
const Marker = "[redacted]"

// minLength keeps very short values out of the redactor. Replacing a two-character value would mangle
// unrelated output without protecting anything meaningful.
const minLength = 4

// Redactor holds the secret values seen in this process. The zero value is ready to use.
type Redactor struct {
	mu       sync.RWMutex
	secrets  []string
	replacer *strings.Replacer
}

// Add registers secret values. Empty and very short values are ignored. A value that Go quoting would
// escape is registered in its escaped form as well, so a diagnostic that prints it with %q is covered too.
func (r *Redactor) Add(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	changed := false
	for _, v := range withQuoted(values) {
		if len(v) < minLength || contains(r.secrets, v) {
			continue
		}
		r.secrets = append(r.secrets, v)
		changed = true
	}
	if !changed {
		return
	}
	// Longest first, so a secret that contains another one is replaced as a whole.
	sort.SliceStable(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })

	pairs := make([]string, 0, 2*len(r.secrets))
	for _, secret := range r.secrets {
		pairs = append(pairs, secret, Marker)
	}
	r.replacer = strings.NewReplacer(pairs...)
}

// Apply replaces every registered secret in s.
//
// The replacement is a single left-to-right pass. Replacing one secret after another would let a short
// secret match inside a marker that an earlier replacement had already written.
func (r *Redactor) Apply(s string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.replacer == nil {
		return s
	}
	return r.replacer.Replace(s)
}

// Error renders an error with every registered secret removed.
func (r *Redactor) Error(err error) string {
	if err == nil {
		return ""
	}
	return r.Apply(err.Error())
}

// Writer returns a writer that removes every registered secret from what it passes on to w. Each write is
// redacted on its own, so a caller hands over a whole message at once, as the standard logger does.
func (r *Redactor) Writer(w io.Writer) io.Writer {
	return &writer{redactor: r, out: w}
}

type writer struct {
	redactor *Redactor
	out      io.Writer
}

// Write reports the length of p rather than of the redacted text, so the caller sees its message accepted
// as a whole.
func (w *writer) Write(p []byte) (int, error) {
	if _, err := io.WriteString(w.out, w.redactor.Apply(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// withQuoted adds to values the escaped form strconv.Quote gives each of them, without the surrounding
// quotes, where it differs from the value.
func withQuoted(values []string) []string {
	// The capped slice makes append copy, so the caller's slice is never written to.
	all := values[:len(values):len(values)]
	for _, v := range values {
		quoted := strconv.Quote(v)
		if escaped := quoted[1 : len(quoted)-1]; escaped != v {
			all = append(all, escaped)
		}
	}
	return all
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
