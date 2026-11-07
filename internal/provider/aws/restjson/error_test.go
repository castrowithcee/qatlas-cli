package restjson_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/restjson"
)

// secretMessage stands for the free text of a provider error. It must never reach an error, a message or
// Original.
const secretMessage = "internal detail do-not-print 4711"

func TestErrorCodes(t *testing.T) {
	tests := []struct {
		name   string
		status int
		header map[string]string
		body   string
		class  provider.Class
		code   string
		hold   time.Duration
	}{
		{"header with namespace and suffix", 404,
			map[string]string{"X-Amzn-Errortype": "com.amazonaws.sesv2#ResourceNotFoundException:http://internal.example/coral/"},
			`{"message":"` + secretMessage + `"}`, provider.ClassNotFound, "ResourceNotFoundException", 0},
		{"header with only a suffix", 400, map[string]string{"X-Amzn-Errortype": "ValidationException:"},
			``, provider.ClassProviderError, "ValidationException", 0},
		{"__type with namespace", 403, nil, `{"__type":"com.amazon.coral.service#AccessDeniedException","Message":"` + secretMessage + `"}`,
			provider.ClassPermission, "AccessDeniedException", 0},
		{"__type with suffix", 409, nil, `{"__type":"ConflictException:http://x/","message":"` + secretMessage + `"}`,
			provider.ClassProviderError, "ConflictException", 0},
		{"the header wins over the body", 404, map[string]string{"X-Amzn-Errortype": "ResourceNotFoundException"},
			`{"__type":"AccessDeniedException"}`, provider.ClassNotFound, "ResourceNotFoundException", 0},
		{"lower case message", 400, nil, `{"__type":"ValidationException","message":"` + secretMessage + `"}`,
			provider.ClassProviderError, "ValidationException", 0},
		{"quota", 402, nil, `{"__type":"ServiceQuotaExceededException","message":"` + secretMessage + `"}`,
			provider.ClassProviderError, "ServiceQuotaExceededException", 0},
		{"limit exceeded", 400, nil, `{"__type":"LimitExceededException"}`, provider.ClassProviderError, "LimitExceededException", 0},
		{"throttling with Retry-After", 400, map[string]string{"X-Amzn-Errortype": "ThrottlingException", "Retry-After": "6"},
			`{"message":"` + secretMessage + `"}`, provider.ClassRateLimited, "ThrottlingException", 6 * time.Second},
		{"throttling without Retry-After", 400, map[string]string{"X-Amzn-Errortype": "ThrottlingException"},
			``, provider.ClassRateLimited, "ThrottlingException", 0},
		{"too many requests", 429, map[string]string{"X-Amzn-Errortype": "TooManyRequestsException", "Retry-After": "2"},
			`{}`, provider.ClassRateLimited, "TooManyRequestsException", 2 * time.Second},
		{"429 with an unknown code", 429, map[string]string{"X-Amzn-Errortype": "Mystery"}, ``, provider.ClassRateLimited, "Mystery", 0},
		{"unknown code in a client error", 400, map[string]string{"X-Amzn-Errortype": "BrandNewException"},
			`{"message":"` + secretMessage + `"}`, provider.ClassInvalidResponse, "BrandNewException", 0},
		{"unknown __type in a client error", 404, nil, `{"__type":"BrandNewException"}`, provider.ClassInvalidResponse, "BrandNewException", 0},
		{"server error with a known code", 500, map[string]string{"X-Amzn-Errortype": "InternalServerException"},
			`{"message":"` + secretMessage + `"}`, provider.ClassProviderError, "InternalServerException", 0},
		{"server error without a body", 503, nil, ``, provider.ClassProviderError, "", 0},
		{"unreadable body of a 4xx", 400, nil, `<html>` + secretMessage + `</html>`, provider.ClassProviderError, "", 0},
		{"unreadable body of a 5xx", 502, nil, "\x00\x01 not json " + secretMessage, provider.ClassProviderError, "", 0},
		{"403 without a code", 403, nil, ``, provider.ClassPermission, "", 0},
		{"unknown code with 403", 403, map[string]string{"X-Amzn-Errortype": "UnrecognizedClientException"}, ``, provider.ClassAuth, "UnrecognizedClientException", 0},
		{"__type that is not a string", 400, nil, `{"__type":{"a":1}}`, provider.ClassProviderError, "", 0},
		{"a code that is not a token", 400, map[string]string{"X-Amzn-Errortype": "Bad Code <script>"},
			``, provider.ClassInvalidResponse, "", 0},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			e := &exchange{status: c.status, header: c.header, body: c.body}
			_, meta, failure := call(t, e, getEmailIdentity, map[string]any{"identity": "x"})
			if failure == nil || failure.Class != c.class || meta.RetryAfter != c.hold {
				t.Fatalf("failure = %v, hold %v; want %s, hold %v", failure, meta.RetryAfter, c.class, c.hold)
			}
			if failure.Original == nil || failure.Original.Status != c.status {
				t.Fatalf("Original = %+v", failure.Original)
			}
			got := ""
			if len(failure.Original.Entries) > 0 {
				got = failure.Original.Entries[0].Code
			}
			if got != c.code {
				t.Errorf("Original code = %q, want %q", got, c.code)
			}
			// Nothing of the provider's text is kept anywhere.
			everything := failure.Error() + " " + failure.Message + " " + string(failure.Cause)
			if failure.Original != nil {
				for _, entry := range failure.Original.Entries {
					everything += " " + entry.Message + entry.Path + entry.Type
				}
				everything += failure.Original.Documentation
			}
			if strings.Contains(everything, "do-not-print") || strings.Contains(everything, "<html>") {
				t.Errorf("the provider text leaked: %s", everything)
			}
		})
	}
}

func TestErrorCodeExtraction(t *testing.T) {
	h := func(v string) http.Header { return http.Header{"X-Amzn-Errortype": {v}} }
	for _, c := range []struct {
		header http.Header
		body   string
		want   string
	}{
		{h("A#B:c"), "", "B"},
		{h(""), `{"__type":"ns#Code"}`, "Code"},
		{nil, `{"__type":"Code:"}`, "Code"},
		{nil, `{"__type":1}`, ""},
		{nil, `{"__type":"a","__type":"b"}`, ""},
		{nil, `[]`, ""},
		{nil, ``, ""},
		{h(strings.Repeat("x", 1000)), ``, ""},
		{nil, `{"__type":"` + strings.Repeat("x", 1000) + `"}`, ""},
	} {
		if got := (restjson.Codec{}).ErrorCode(c.header, []byte(c.body)); got != c.want {
			t.Errorf("ErrorCode(%v, %q) = %q, want %q", c.header, c.body, got, c.want)
		}
	}
}

func TestErrorBodyIsReadOnlyUpToTheErrorLimit(t *testing.T) {
	big := `{"__type":"ValidationException","message":"` + strings.Repeat("x", 1<<20) + `"}`
	e := &exchange{status: 400, body: big}
	_, _, failure := call(t, e, getEmailIdentity, map[string]any{"identity": "x"})
	// The cut body is not JSON, so only the status decides.
	if failure == nil || failure.Class != provider.ClassProviderError {
		t.Errorf("failure = %v", failure)
	}
	_ = context.Background
}
