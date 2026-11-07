package awserr

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
		retry  string
		class  provider.Class
		hold   time.Duration
	}{
		{"throttling", 400, "Throttling", "", provider.ClassRateLimited, 0},
		{"throttling exception", 400, "ThrottlingException", "7", provider.ClassRateLimited, 7 * time.Second},
		{"request limit", 503, "RequestLimitExceeded", "", provider.ClassRateLimited, 0},
		{"http 429 without code", 429, "", "3", provider.ClassRateLimited, 3 * time.Second},
		{"http 429 unknown code", 429, "Foo", "", provider.ClassRateLimited, 0},
		{"access denied", 403, "AccessDenied", "", provider.ClassPermission, 0},
		{"invalid client token", 403, "InvalidClientTokenId", "", provider.ClassAuth, 0},
		{"signature", 403, "SignatureDoesNotMatch", "", provider.ClassAuth, 0},
		{"expired token", 400, "ExpiredToken", "", provider.ClassAuth, 0},
		{"server error", 500, "InternalFailure", "", provider.ClassProviderError, 0},
		{"unavailable", 503, "", "", provider.ClassProviderError, 0},
		{"forbidden without code", 403, "", "", provider.ClassPermission, 0},
		{"unauthorized without code", 401, "", "", provider.ClassAuth, 0},
		{"other 4xx", 400, "ValidationError", "", provider.ClassProviderError, 0},
		{"retry-after ignored when not limited", 400, "AccessDenied", "9", provider.ClassPermission, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := http.Header{}
			if test.retry != "" {
				header.Set("Retry-After", test.retry)
			}
			got, hold := Classify("aws.test", test.status, header, test.code, "Sender")
			if got.Class != test.class || hold != test.hold || got.Op != "aws.test" {
				t.Fatalf("got %s hold %v op %q; want %s hold %v", got.Class, hold, got.Op, test.class, test.hold)
			}
			if got.Original == nil || got.Original.Status != test.status {
				t.Fatalf("Original = %+v", got.Original)
			}
		})
	}
}

func TestRateLimitMessageNamesTheWait(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "17")
	got, _ := Classify("op", 429, header, "", "")
	if !strings.Contains(got.Message, "retry after 17 seconds") {
		t.Fatalf("message = %q", got.Message)
	}
	got, hold := Classify("op", 400, nil, "Throttling", "")
	if hold != 0 || strings.Contains(got.Message, "retry after") {
		t.Fatalf("no header: %q hold %v", got.Message, hold)
	}
}

func TestProviderTextNeverAppears(t *testing.T) {
	// Code and type are provider text too: only well-formed tokens reach Original, and no message echoes them.
	got, _ := Classify("op", 400, nil, "Bad Code with spaces and secret=AKIAXYZ", "<script>")
	if got.Original.Entries != nil {
		t.Fatalf("unsafe tokens kept: %+v", got.Original.Entries)
	}
	got, _ = Classify("op", 400, nil, "CustomCode", "Sender")
	if len(got.Original.Entries) != 1 || got.Original.Entries[0] != (provider.OriginalEntry{Type: "Sender", Code: "CustomCode"}) {
		t.Fatalf("entries = %+v", got.Original.Entries)
	}
	if strings.Contains(got.Error(), "CustomCode") || got.Original.Entries[0].Message != "" {
		t.Fatalf("code echoed: %v %+v", got, got.Original)
	}
}

func TestNormalizeCode(t *testing.T) {
	for in, want := range map[string]string{
		"Throttling": "Throttling",
		"com.amazon.coral.availability#ThrottlingException":       "ThrottlingException",
		"AccessDeniedException:http://internal.amazon.com/coral/": "AccessDeniedException",
		" ExpiredToken ": "ExpiredToken",
		"":               "",
	} {
		if got := NormalizeCode(in); got != want {
			t.Errorf("NormalizeCode(%q) = %q, want %q", in, got, want)
		}
	}
}
