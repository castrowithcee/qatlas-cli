package aws

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
)

// toolFails answers every call but GetCallerIdentity (the account check) with the given reply and invokes
// the lookup tool, so the failure hits the tool's own request.
func toolFails(t *testing.T, answer func(sent, int) reply) (*env, *fakeAWS, *[]time.Duration) {
	t.Helper()
	f := direct(answer)
	e := newEnv(t, f, baseValues())
	slept := holdLimiter(clockOf(t), "prod")
	return e, f, slept
}

const lookup = `{"access_key_id":"` + testKeyID + `"}`

func TestThrottlingWithoutRetryAfterIsNeverRepeated(t *testing.T) {
	for _, code := range []string{"Throttling", "ThrottlingException", "RequestLimitExceeded"} {
		e, f, slept := toolFails(t, func(sent, int) reply { return reply{status: 400, body: errorXML(code)} })
		_, err := e.invoke("aws.sts.access-key.get-info", lookup)
		if classOf(err) != provider.ClassRateLimited || f.count("GetAccessKeyInfo") != 1 || len(*slept) != 0 {
			t.Errorf("%s: err = %v, requests = %v, slept = %v, want one request and no wait", code, err, f.actions(), *slept)
		}
	}
	// HTTP 429 without a code behaves the same.
	e, f, _ := toolFails(t, func(sent, int) reply { return reply{status: 429} })
	if _, err := e.invoke("aws.sts.access-key.get-info", lookup); classOf(err) != provider.ClassRateLimited || f.count("GetAccessKeyInfo") != 1 {
		t.Errorf("429: err = %v, requests = %v", err, f.actions())
	}
}

func TestThrottlingWithRetryAfterIsRepeatedWithinBounds(t *testing.T) {
	e, f, slept := toolFails(t, func(r sent, n int) reply {
		if n < 2 {
			return reply{status: 400, header: map[string]string{"Retry-After": "2"}, body: errorXML("Throttling")}
		}
		return ok(`<GetAccessKeyInfoResponse><GetAccessKeyInfoResult><Account>210987654321</Account></GetAccessKeyInfoResult></GetAccessKeyInfoResponse>`)
	})
	if _, err := e.invoke("aws.sts.access-key.get-info", lookup); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if f.count("GetAccessKeyInfo") != 2 || len(*slept) != 1 || (*slept)[0] < 2*time.Second {
		t.Errorf("requests = %v, slept = %v, want a repeat after the two seconds AWS asked for", f.actions(), *slept)
	}

	// A throttling that never ends stops after the bound.
	e, f, _ = toolFails(t, func(sent, int) reply {
		return reply{status: 400, header: map[string]string{"Retry-After": "1"}, body: errorXML("RequestLimitExceeded")}
	})
	_, err := e.invoke("aws.sts.access-key.get-info", lookup)
	if classOf(err) != provider.ClassRateLimited || f.count("GetAccessKeyInfo") != maxRetries+1 {
		t.Errorf("err = %v, requests = %v, want %d attempts", err, f.actions(), maxRetries+1)
	}
	if !strings.Contains(err.Error(), "retry after 1 seconds") {
		t.Errorf("message = %q, want the hold", err.Error())
	}

	// A wait beyond what a request may take is reported, not slept.
	e, f, slept = toolFails(t, func(sent, int) reply {
		return reply{status: 400, header: map[string]string{"Retry-After": "120"}, body: errorXML("Throttling")}
	})
	_, err = e.invoke("aws.sts.access-key.get-info", lookup)
	if classOf(err) != provider.ClassRateLimited || f.count("GetAccessKeyInfo") != 1 || len(*slept) != 0 {
		t.Errorf("err = %v, requests = %v, slept = %v", err, f.actions(), *slept)
	}
}

func TestServerErrorsAreRepeatedOnceMoreThanTwice(t *testing.T) {
	e, f, _ := toolFails(t, func(sent, int) reply { return reply{status: 503, body: errorXML("ServiceUnavailable")} })
	_, err := e.invoke("aws.sts.access-key.get-info", lookup)
	if classOf(err) != provider.ClassProviderError || f.count("GetAccessKeyInfo") != maxRetries+1 {
		t.Errorf("err = %v, requests = %v, want a bounded repeat", err, f.actions())
	}
	e, f, _ = toolFails(t, func(_ sent, n int) reply {
		if n == 1 {
			return reply{status: 500}
		}
		return ok(`<GetAccessKeyInfoResponse><GetAccessKeyInfoResult><Account>210987654321</Account></GetAccessKeyInfoResult></GetAccessKeyInfoResponse>`)
	})
	if _, err := e.invoke("aws.sts.access-key.get-info", lookup); err != nil || f.count("GetAccessKeyInfo") != 2 {
		t.Errorf("err = %v, requests = %v", err, f.actions())
	}
}

func TestAccessErrorsMapToClassesAndAreNotRepeated(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
		class  provider.Class
	}{
		{403, "ExpiredToken", provider.ClassAuth},
		{400, "ExpiredToken", provider.ClassAuth},
		{403, "SignatureDoesNotMatch", provider.ClassAuth},
		{403, "InvalidClientTokenId", provider.ClassAuth},
		{403, "AccessDenied", provider.ClassPermission},
		{403, "", provider.ClassPermission},
		{401, "", provider.ClassAuth},
		{400, "ValidationError", provider.ClassProviderError},
	} {
		e, f, _ := toolFails(t, func(sent, int) reply { return reply{status: tc.status, body: errorXML(tc.code)} })
		_, err := e.invoke("aws.sts.access-key.get-info", lookup)
		if classOf(err) != tc.class || f.count("GetAccessKeyInfo") != 1 {
			t.Errorf("%d %s: err = %v, requests = %v, want class %s once", tc.status, tc.code, err, f.actions(), tc.class)
		}
		mustNotLeak(t, "the error", err.Error())
		if strings.Contains(err.Error(), "provider text") {
			t.Errorf("%d %s: provider text in %v", tc.status, tc.code, err)
		}
	}
}

func TestUnreadableAnswers(t *testing.T) {
	for name, answer := range map[string]reply{
		"html":             ok("<html>maintenance</html>"),
		"empty":            ok(""),
		"not xml":          ok("{\"json\":true}"),
		"a truncated body": ok("<GetAccessKeyInfoResponse><GetAccessKeyInfoResult><Account>1"),
		"a bad account":    ok(`<GetAccessKeyInfoResponse><GetAccessKeyInfoResult><Account>abc</Account></GetAccessKeyInfoResult></GetAccessKeyInfoResponse>`),
		"too large":        ok(`<GetAccessKeyInfoResponse><GetAccessKeyInfoResult><Account>` + strings.Repeat("1", 5<<20) + `</Account></GetAccessKeyInfoResult></GetAccessKeyInfoResponse>`),
	} {
		e, f, _ := toolFails(t, func(sent, int) reply { return answer })
		_, err := e.invoke("aws.sts.access-key.get-info", lookup)
		if classOf(err) != provider.ClassInvalidResponse || f.count("GetAccessKeyInfo") != 1 {
			t.Errorf("%s: err = %v, requests = %v", name, err, f.actions())
		}
	}
	// An error answer whose body is not XML still maps by its status.
	e, _, _ := toolFails(t, func(sent, int) reply { return reply{status: 403, body: "<<<garbage"} })
	if _, err := e.invoke("aws.sts.access-key.get-info", lookup); classOf(err) != provider.ClassPermission {
		t.Errorf("garbage error body: %v", err)
	}
	// The account check answers garbage too.
	f := &fakeAWS{answer: func(sent, int) reply { return ok("<nope/>") }}
	e = newEnv(t, f, baseValues())
	if _, err := e.invoke("aws.sts.caller-identity.get", `{}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("garbage identity: %v", err)
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout to sts.eu-central-1.amazonaws.com" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestTimeoutsAndTransportFailures(t *testing.T) {
	for name, err := range map[string]error{"a deadline": context.DeadlineExceeded, "a net timeout": timeoutError{}} {
		e, f, _ := toolFails(t, func(sent, int) reply { return reply{err: err} })
		_, got := e.invoke("aws.sts.access-key.get-info", lookup)
		if classOf(got) != provider.ClassTimeout || f.count("GetAccessKeyInfo") != 1 {
			t.Errorf("%s: err = %v, requests = %v, want a timeout and no repeat", name, got, f.actions())
		}
		if strings.Contains(got.Error(), "amazonaws.com") {
			t.Errorf("%s: the host reached the message: %v", name, got)
		}
	}
	e, _, _ := toolFails(t, func(sent, int) reply {
		return reply{err: &net.DNSError{Name: "sts.eu-central-1.amazonaws.com", Err: "no such host"}}
	})
	_, err := e.invoke("aws.sts.access-key.get-info", lookup)
	var failed *provider.Error
	if !errors.As(err, &failed) || failed.Class != provider.ClassUnreachable || strings.Contains(err.Error(), "amazonaws.com") {
		t.Errorf("dns failure: %v", err)
	}
}

// The deadline of one invoke stays below the 60 s invoke limit, and the HTTP client's below that.
func TestTimeoutsStayBelowTheInvokeLimit(t *testing.T) {
	if invokeBudget >= 60*time.Second || requestTimeout >= invokeBudget {
		t.Errorf("budget %s, request timeout %s", invokeBudget, requestTimeout)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	e, f, _ := toolFails(t, func(sent, int) reply {
		return reply{status: 302, header: map[string]string{"Location": "https://evil.example/"}}
	})
	_, err := e.invoke("aws.sts.access-key.get-info", lookup)
	if err == nil || len(f.requests) != 2 || f.count("GetAccessKeyInfo") != 1 || strings.Contains(err.Error(), "evil") {
		t.Errorf("err = %v, requests = %v", err, f.actions())
	}
	for _, r := range f.requests {
		if r.host != "sts.eu-central-1.amazonaws.com" {
			t.Errorf("a request went to %q", r.host)
		}
	}
}

func TestConnectionTest(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		f := direct(nil)
		install(t, f)
		red := &redact.Redactor{}
		class, err := TestConnection(context.Background(), connection("account/"+testAccount, "region/us-east-1", "region/eu-central-1"),
			resolverFor(baseValues(), red), red)
		if err != nil || class != provider.ClassOK || len(f.requests) != 1 || f.requests[0].action != "GetCallerIdentity" ||
			f.requests[0].host != "sts.us-east-1.amazonaws.com" {
			t.Errorf("class = %q, err = %v, requests = %v", class, err, f.actions())
		}
	})
	t.Run("a role is assumed first", func(t *testing.T) {
		f := &fakeAWS{}
		install(t, f)
		*f = *roleAWS(clockOf(t), time.Hour, testAccount)
		red := &redact.Redactor{}
		class, err := TestConnection(context.Background(), connection(), resolverFor(roleValues(), red), red)
		if err != nil || class != provider.ClassOK || strings.Join(f.actions(), ",") != "AssumeRole,GetCallerIdentity" {
			t.Errorf("class = %q, err = %v, requests = %v", class, err, f.actions())
		}
	})
	t.Run("another account", func(t *testing.T) {
		f := &fakeAWS{answer: func(sent, int) reply { return ok(identityFor("999999999999")) }}
		install(t, f)
		red := &redact.Redactor{}
		class, err := TestConnection(context.Background(), connection(), resolverFor(baseValues(), red), red)
		if class != "" || err == nil || !strings.Contains(err.Error(), "different AWS account") || strings.Contains(err.Error(), "999999999999") {
			t.Errorf("class = %q, err = %v, want the account refusal as an error", class, err)
		}
	})
	t.Run("a role of another account", func(t *testing.T) {
		f := direct(nil)
		install(t, f)
		red := &redact.Redactor{}
		_, err := TestConnection(context.Background(), connection(), resolverFor(
			roleValues().with(envRole, "arn:aws:iam::999999999999:role/X"), red), red)
		if err == nil || len(f.requests) != 0 {
			t.Errorf("err = %v, requests = %v", err, f.actions())
		}
	})
	for name, tc := range map[string]struct {
		reply reply
		class provider.Class
	}{
		"an expired token": {reply{status: 403, body: errorXML("ExpiredToken")}, provider.ClassAuth},
		"a bad signature":  {reply{status: 403, body: errorXML("SignatureDoesNotMatch")}, provider.ClassAuth},
		"throttling":       {reply{status: 400, body: errorXML("Throttling")}, provider.ClassRateLimited},
		"garbage":          {ok("<x/>"), provider.ClassInvalidResponse},
		"a timeout":        {reply{err: context.DeadlineExceeded}, provider.ClassTimeout},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeAWS{answer: func(sent, int) reply { return tc.reply }}
			install(t, f)
			red := &redact.Redactor{}
			class, err := TestConnection(context.Background(), connection(), resolverFor(baseValues(), red), red)
			if err != nil || class != tc.class {
				t.Errorf("class = %q, err = %v, want %q", class, err, tc.class)
			}
		})
	}
	t.Run("an invalid binding and a missing secret", func(t *testing.T) {
		f := direct(nil)
		install(t, f)
		red := &redact.Redactor{}
		if class, err := TestConnection(context.Background(), connection("account/"+testAccount), resolverFor(baseValues(), red), red); class == provider.ClassOK || err == nil && class == "" {
			t.Errorf("a binding without a region passed: %q, %v", class, err)
		}
		if class, err := TestConnection(context.Background(), connection(), resolverFor(values{}, red), red); class == provider.ClassOK || (err == nil && class == "") {
			t.Errorf("missing secrets passed: %q, %v", class, err)
		}
		if len(f.requests) != 0 {
			t.Errorf("requests = %v", f.actions())
		}
	})
}
