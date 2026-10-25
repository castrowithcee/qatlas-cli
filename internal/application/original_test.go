package application

import (
	"fmt"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func TestProviderDetailOfRedactsAndMarksUntrusted(t *testing.T) {
	const secretValue = "ghp_supersecretvalue"
	red := &redact.Redactor{}
	red.Add(secretValue)
	err := fmt.Errorf("wrapped: %w", &provider.Error{Class: provider.ClassPermission, Op: "x", Message: "refused",
		Original: &provider.Original{Status: 403, Documentation: "https://docs.example/" + secretValue,
			Entries: []provider.OriginalEntry{{Type: "INSUFFICIENT_SCOPES", Path: "a.b",
				Message: "token " + secretValue + " has only: [repo]"}}}})

	detail := ProviderDetailOf(err, red)
	if detail == nil || !detail.Untrusted || detail.Truncated || detail.Status != 403 {
		t.Fatalf("detail = %+v", detail)
	}
	got := detail.Errors[0]
	if got.Type != "INSUFFICIENT_SCOPES" || got.Path != "a.b" || got.Message != "token "+redact.Marker+" has only: [repo]" {
		t.Errorf("entry = %+v", got)
	}
	if strings.Contains(detail.Documentation, secretValue) || !strings.Contains(detail.Documentation, redact.Marker) {
		t.Errorf("documentation = %q", detail.Documentation)
	}
}

func TestProviderDetailOfBoundsTheAnswer(t *testing.T) {
	const secretValue = "ghp_supersecretvalue"
	red := &redact.Redactor{}
	red.Add(secretValue)
	// The secret straddles the message bound: it is redacted before the cut, so no prefix of it survives.
	long := strings.Repeat("x", maxOriginalMessage-5) + secretValue + "tail"
	original := &provider.Original{}
	for i := 0; i < maxOriginalEntries+3; i++ {
		original.Entries = append(original.Entries, provider.OriginalEntry{Message: long})
	}
	detail := ProviderDetailOf(&provider.Error{Original: original}, red)
	if detail == nil || !detail.Truncated || len(detail.Errors) != maxOriginalEntries {
		t.Fatalf("detail = %+v", detail)
	}
	for _, e := range detail.Errors {
		if len(e.Message) > maxOriginalMessage || strings.Contains(e.Message, "ghp_") {
			t.Fatalf("message = %q", e.Message)
		}
	}
}

func TestProviderDetailOfIsNilWithoutAnOriginalAnswer(t *testing.T) {
	if ProviderDetailOf(&provider.Error{Class: provider.ClassAuth}, &redact.Redactor{}) != nil {
		t.Error("an error without an original answer carries no detail")
	}
	if ProviderDetailOf(&provider.Error{Original: &provider.Original{}}, nil) != nil {
		t.Error("an empty original answer carries no detail")
	}
	if ProviderDetailOf(fmt.Errorf("plain"), nil) != nil {
		t.Error("a foreign error carries no detail")
	}
}
