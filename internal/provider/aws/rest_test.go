package aws

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/endpoints"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/httpbind"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/restjson"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/sigv4"
	"github.com/castrowithcee/qatlas-connect/internal/provider/ratelimit"
)

// restOperation is an Account style call: POST with the account in the body.
func restOperation() *httpbind.Operation {
	str := &httpbind.Shape{Kind: httpbind.KindString}
	return &httpbind.Operation{
		Method: "POST", URI: "/getContactInformation",
		Input:  &httpbind.Shape{Kind: httpbind.KindStruct, Fields: []httpbind.Field{{Key: "account_id", Name: "AccountId", Shape: str}}},
		Output: &httpbind.Shape{Kind: httpbind.KindStruct, Fields: []httpbind.Field{{Key: "name", Name: "FullName", Shape: str}}},
	}
}

// sendRestWith runs restOperation through sendRest against a fake that answers every request with answer.
func sendRestWith(t *testing.T, service endpoints.Service, idem capability.Idempotency, answer func(sent, int) reply) (map[string]any, *provider.Error, *fakeAWS, *[]time.Duration) {
	t.Helper()
	f := &fakeAWS{answer: answer}
	c := install(t, f)
	var slept []time.Duration
	limiters.Replace(limiterKey("prod", service), ratelimit.New(0, c.Now,
		func(_ context.Context, d time.Duration) error { slept = append(slept, d); c.advance(d); return nil }))
	old := retryPolicy
	retryPolicy.Jitter = func() float64 { return 0.5 }
	t.Cleanup(func() { retryPolicy = old })
	o := restOperation()
	req, err := o.Encode(restjson.Codec{}, map[string]any{"account_id": testAccount}, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{name: "prod", http: provider.NoRedirectClient(time.Second, f)}
	var out map[string]any
	_, failure := client.sendRest(t.Context(), "get contact", service, testRegion,
		sigv4.Credentials{AccessKeyID: testKeyID, SecretAccessKey: testSecret}, idem, restjson.Codec{}, o, req, &out)
	return out, failure, f, &slept
}

func TestRestCallUsesTheEndpointTableAndScope(t *testing.T) {
	good := func(sent, int) reply { return ok(`{"FullName":"Test Person"}`) }
	for _, c := range []struct {
		service     endpoints.Service
		host, scope string
	}{
		{endpoints.Account, "account.us-east-1.amazonaws.com", "/us-east-1/account/aws4_request"},
		{endpoints.SES, "email." + testRegion + ".amazonaws.com", "/" + testRegion + "/ses/aws4_request"},
	} {
		out, failure, f, _ := sendRestWith(t, c.service, capability.IdempotencySafe, good)
		if failure != nil || out["name"] != "Test Person" || len(f.requests) != 1 {
			t.Fatalf("%s: out = %v, failure = %v", c.service, out, failure)
		}
		r := f.requests[0]
		auth := r.header.Get("Authorization")
		if r.host != c.host || !strings.Contains(auth, c.scope) || !strings.Contains(auth, "content-type") ||
			r.header.Get("X-Amz-Content-Sha256") != "" || r.header.Get("Accept") != "application/json" ||
			r.header.Get("Content-Type") != "application/json" || r.header.Get("User-Agent") == "" {
			t.Errorf("%s: host %s, headers %v", c.service, r.host, r.header)
		}
		var body map[string]string
		if json.Unmarshal([]byte(r.body), &body) != nil || body["AccountId"] != testAccount {
			t.Errorf("body = %s", r.body)
		}
	}
}

func throttledJSON(sent, int) reply {
	return reply{status: 429, header: map[string]string{"Retry-After": "4", "X-Amzn-Errortype": "TooManyRequestsException"}, body: `{"message":"slow"}`}
}

func TestOnlySafeRestOperationsAreRepeated(t *testing.T) {
	for _, idem := range []capability.Idempotency{capability.IdempotencyIdempotent, capability.IdempotencyNonIdempotent, capability.IdempotencyUnknown} {
		for name, answer := range map[string]func(sent, int) reply{
			"throttled with Retry-After": throttledJSON,
			"server failure":             func(sent, int) reply { return reply{status: 503} },
		} {
			_, failure, f, _ := sendRestWith(t, endpoints.Account, idem, answer)
			if failure == nil || len(f.requests) != 1 {
				t.Errorf("%s, %s: failure = %v, requests = %d, want exactly one", idem, name, failure, len(f.requests))
			}
		}
	}
}

func TestSafeRestReadIsRepeatedOnlyWithRetryAfter(t *testing.T) {
	_, failure, f, slept := sendRestWith(t, endpoints.Account, capability.IdempotencySafe, throttledJSON)
	if failure == nil || failure.Class != provider.ClassRateLimited || len(f.requests) != maxRetries+1 {
		t.Fatalf("failure = %v, requests = %d, want %d", failure, len(f.requests), maxRetries+1)
	}
	if len(*slept) != maxRetries || (*slept)[0] != 4500*time.Millisecond {
		t.Errorf("slept = %v", *slept)
	}
	// Without Retry-After a rate limit is not repeated.
	_, failure, f, _ = sendRestWith(t, endpoints.Account, capability.IdempotencySafe, func(sent, int) reply {
		return reply{status: 400, header: map[string]string{"X-Amzn-Errortype": "ThrottlingException"}}
	})
	if failure == nil || failure.Class != provider.ClassRateLimited || len(f.requests) != 1 {
		t.Errorf("failure = %v, requests = %d", failure, len(f.requests))
	}
	// A server failure is repeated and a later success wins.
	out, failure, f, _ := sendRestWith(t, endpoints.Account, capability.IdempotencySafe, func(_ sent, n int) reply {
		if n == 1 {
			return reply{status: 503, body: "<html>"}
		}
		return ok(`{"FullName":"Test Person"}`)
	})
	if failure != nil || out["name"] != "Test Person" || len(f.requests) != 2 {
		t.Errorf("out = %v, failure = %v, requests = %d", out, failure, len(f.requests))
	}
	// An unknown client error code is not repeated and is not guessed.
	_, failure, f, _ = sendRestWith(t, endpoints.Account, capability.IdempotencySafe, func(sent, int) reply {
		return reply{status: 400, header: map[string]string{"X-Amzn-Errortype": "BrandNewException"}}
	})
	if failure == nil || failure.Class != provider.ClassInvalidResponse || len(f.requests) != 1 {
		t.Errorf("failure = %v, requests = %d", failure, len(f.requests))
	}
}

func TestAccountArgumentCheckAppliesToTheBodyMember(t *testing.T) {
	spec := toolSpec{targetCheck: "account-argument", targetArgs: []string{"account_id"}}
	b := binding{account: testAccount, regions: []string{testRegion}}
	if err := spec.checkArguments(b, true, map[string]any{}); err != nil {
		t.Errorf("an absent AccountId was refused: %v", err)
	}
	if err := spec.checkArguments(b, true, map[string]any{"account_id": testAccount}); err != nil {
		t.Errorf("the bound account was refused: %v", err)
	}
	if err := spec.checkArguments(b, true, map[string]any{"account_id": "210987654321"}); err == nil {
		t.Error("another account was accepted")
	}
}
