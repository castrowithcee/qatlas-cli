package provider_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestMayHaveArrived(t *testing.T) {
	for _, test := range []struct {
		failure provider.Error
		want    bool
	}{
		{provider.Error{Class: provider.ClassTimeout}, true},
		{provider.Error{Class: provider.ClassUnreachable, Cause: provider.CauseConnectionReset}, true},
		{provider.Error{Class: provider.ClassUnreachable, Cause: provider.CauseUnknown}, true},
		{provider.Error{Class: provider.ClassUnreachable, Cause: provider.CauseDNS}, false},
		{provider.Error{Class: provider.ClassUnreachable, Cause: provider.CauseConnectionRefused}, false},
		{provider.Error{Class: provider.ClassTLS}, false},
		{provider.Error{Class: provider.ClassProviderError}, false},
	} {
		if got := test.failure.MayHaveArrived(); got != test.want {
			t.Errorf("%+v: got %v, want %v", test.failure, got, test.want)
		}
	}
}

func TestFailAndInvalidResponse(t *testing.T) {
	if got := provider.Fail("open", "boom"); got.Class != provider.ClassProviderError || got.Op != "open" || got.Message != "boom" {
		t.Fatalf("Fail = %+v", got)
	}
	if got := provider.InvalidResponse("read", "bad"); got.Class != provider.ClassInvalidResponse || got.Op != "read" || got.Message != "bad" {
		t.Fatalf("InvalidResponse = %+v", got)
	}
}

func TestInvalidRequestErrorIsOneErrorType(t *testing.T) {
	var err error = &provider.InvalidRequestError{Message: "bad input"}
	var target *provider.InvalidRequestError
	if !errors.As(err, &target) || target.Error() != "bad input" {
		t.Fatalf("errors.As = %v", target)
	}
}

func TestValidHeaderToken(t *testing.T) {
	for value, want := range map[string]bool{
		"":                        false,
		"1234567":                 false,
		"12345678":                true,
		strings.Repeat("a", 4096): true,
		strings.Repeat("a", 4097): false,
		"abcdefg h":               false,
		"abcdefgh\n":              false,
		"abcdefgh\x7f":            false,
		"abcdefghé":               false,
		"abc!~def":                true,
	} {
		if got := provider.ValidHeaderToken(value); got != want {
			t.Errorf("%q: got %v, want %v", value, got, want)
		}
	}
}

func TestTargetsOf(t *testing.T) {
	for name, test := range map[string]struct {
		resolved config.Resolved
		want     []string
	}{
		"list wins":     {config.Resolved{Targets: []string{"a", "b"}, Target: "c"}, []string{"a", "b"}},
		"single target": {config.Resolved{Target: "c"}, []string{"c"}},
		"blank target":  {config.Resolved{Target: "  "}, nil},
		"nothing":       {config.Resolved{}, nil},
	} {
		if got := provider.TargetsOf(&test.resolved); len(got) != len(test.want) || (len(got) > 0 && !reflect.DeepEqual(got, test.want)) {
			t.Errorf("%s: got %v, want %v", name, got, test.want)
		}
	}
}
