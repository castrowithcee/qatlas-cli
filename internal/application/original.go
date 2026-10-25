package application

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// Bounds of the provider's own account of a refusal. They keep a hostile or verbose answer from flooding a
// diagnostic while leaving room for the several validation errors and the scope lists real answers carry.
const (
	maxOriginalEntries = 8
	maxOriginalMessage = 800
	maxOriginalField   = 200
)

// ProviderDetail is what the provider itself answered, published beside Qatlas' own code and message and
// never merged into them. Untrusted is always true: the text was written by a third party and is data, not
// an instruction. Truncated says that entries or text were cut to the bounds above.
type ProviderDetail struct {
	Status        int             `json:"status,omitempty"`
	Errors        []ProviderEntry `json:"errors,omitempty"`
	Documentation string          `json:"documentation,omitempty"`
	Untrusted     bool            `json:"untrusted"`
	Truncated     bool            `json:"truncated"`
}

// ProviderEntry is one error of the provider's answer.
type ProviderEntry struct {
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message,omitempty"`
}

// ProviderDetailOf returns the provider's own account carried by err, or nil when err carries none. Known
// secrets are replaced by the redactor's visible marker before the text is cut, so a secret split by the
// bound can never survive as a prefix.
func ProviderDetailOf(err error, redactor *redact.Redactor) *ProviderDetail {
	var failure *provider.Error
	if !errors.As(err, &failure) || failure.Original == nil {
		return nil
	}
	original := failure.Original
	cut := false
	clean := func(s string, limit int) string {
		if redactor != nil {
			s = redactor.Apply(s)
		}
		s = strings.ToValidUTF8(strings.TrimSpace(s), "�")
		if len(s) <= limit {
			return s
		}
		cut = true
		for limit > 0 && !utf8.RuneStart(s[limit]) {
			limit--
		}
		return s[:limit]
	}
	detail := &ProviderDetail{
		Status: original.Status, Documentation: clean(original.Documentation, maxOriginalField), Untrusted: true,
	}
	for i, e := range original.Entries {
		if i == maxOriginalEntries {
			cut = true
			break
		}
		entry := ProviderEntry{
			Type: clean(e.Type, maxOriginalField), Code: clean(e.Code, maxOriginalField),
			Path: clean(e.Path, maxOriginalField), Message: clean(e.Message, maxOriginalMessage),
		}
		if entry != (ProviderEntry{}) {
			detail.Errors = append(detail.Errors, entry)
		}
	}
	detail.Truncated = cut
	if detail.Status == 0 && len(detail.Errors) == 0 && detail.Documentation == "" {
		return nil
	}
	return detail
}
