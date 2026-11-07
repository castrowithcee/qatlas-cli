package aws

import (
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/awsquery"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/endpoints"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/sigv4"
)

// sendWith runs one call through send with the given safety against a fake that answers every request with
// answer.
func sendWith(t *testing.T, idem capability.Idempotency, answer func(sent, int) reply) (*provider.Error, *fakeAWS, *[]time.Duration) {
	t.Helper()
	f := &fakeAWS{answer: answer}
	c := install(t, f)
	slept := holdLimiter(c, "prod")
	old := retryPolicy
	retryPolicy.Jitter = func() float64 { return 0.5 }
	t.Cleanup(func() { retryPolicy = old })
	client := &Client{name: "prod", http: provider.NoRedirectClient(time.Second, f)}
	failure := client.send(t.Context(), "test op", endpoints.STS, testRegion,
		sigv4.Credentials{AccessKeyID: testKeyID, SecretAccessKey: testSecret}, idem,
		awsquery.Call{Action: "GetAccessKeyInfo", Version: stsVersion}, nil)
	return failure, f, slept
}

func throttled(string, int) reply {
	return reply{status: 400, header: map[string]string{"Retry-After": "4"}, body: errorXML("ProvisionedThroughputExceededException")}
}

func TestOnlySafeOperationsAreRepeated(t *testing.T) {
	for _, idem := range []capability.Idempotency{capability.IdempotencyIdempotent, capability.IdempotencyNonIdempotent, capability.IdempotencyUnknown} {
		for name, answer := range map[string]func(sent, int) reply{
			"throttled with Retry-After": func(s sent, n int) reply { return throttled(s.action, n) },
			"server failure":             func(sent, int) reply { return reply{status: 503, body: errorXML("ServiceUnavailable")} },
		} {
			failure, f, slept := sendWith(t, idem, answer)
			if failure == nil || f.count("GetAccessKeyInfo") != 1 {
				t.Errorf("%s, %s: failure = %v, requests = %v, want exactly one request", idem, name, failure, f.actions())
			}
			if name == "throttled with Retry-After" && (len(*slept) != 0) {
				t.Errorf("%s: slept %v before nothing", idem, *slept)
			}
		}
	}
}

func TestSafeReadIsRepeatedWithJitterAndABound(t *testing.T) {
	failure, f, slept := sendWith(t, capability.IdempotencySafe, func(s sent, n int) reply { return throttled(s.action, n) })
	if failure == nil || failure.Class != provider.ClassRateLimited || f.count("GetAccessKeyInfo") != maxRetries+1 {
		t.Fatalf("failure = %v, requests = %v, want %d attempts", failure, f.actions(), maxRetries+1)
	}
	// 4 s asked, a draw of 0.5 of a quarter adds 0.5 s; the wait is never shorter than the provider asked.
	if len(*slept) != maxRetries || (*slept)[0] != 4500*time.Millisecond {
		t.Errorf("slept = %v, want %d waits of 4.5s", *slept, maxRetries)
	}
}

func TestServerBackoffGrowsAndIsBounded(t *testing.T) {
	failure, f, slept := sendWith(t, capability.IdempotencySafe, func(sent, int) reply { return reply{status: 502} })
	if failure == nil || f.count("GetAccessKeyInfo") != maxRetries+1 {
		t.Fatalf("failure = %v, requests = %v", failure, f.actions())
	}
	// 0.5 s then 1 s, each plus the jitter share.
	if len(*slept) != 2 || (*slept)[0] != 562500*time.Microsecond || (*slept)[1] != 1125*time.Millisecond {
		t.Errorf("slept = %v", *slept)
	}
}
