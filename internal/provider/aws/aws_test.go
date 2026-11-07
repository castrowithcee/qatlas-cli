package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/endpoints"
	"github.com/castrowithcee/qatlas-connect/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// Synthetic values: none belongs to a real account. The documented AWS example keys are used where a key is
// needed.
const (
	testAccount  = "123456789012"
	testKeyID    = "AKIA" + "IOSFODNN7EXAMPLE" // split so secret scanners do not flag the documented example
	testSecret   = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	testToken    = "FQoGZXIvYXdzEJr1EXAMPLETOKENvalue0123456789"
	testRoleARN  = "arn:aws:iam::123456789012:role/Qatlas"
	testExternal = "external-id-secret-4711"

	sessionKeyID  = "ASIA" + "SESSIONEXAMPLE01"
	sessionSecret = "sessionSecretEXAMPLEvalue0123456789abcdef"
	sessionToken  = "sessionTokenEXAMPLEvalue0123456789abcdefghij"

	testRegion = "eu-central-1"
)

var testNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

const identityXML = `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <GetCallerIdentityResult>
    <Arn>arn:aws:iam::123456789012:user/Alice</Arn>
    <UserId>AIDACKCEVSQ6C2EXAMPLE</UserId>
    <Account>123456789012</Account>
  </GetCallerIdentityResult>
  <ResponseMetadata><RequestId>01234567-89ab-cdef-0123-456789abcdef</RequestId></ResponseMetadata>
</GetCallerIdentityResponse>`

func identityFor(account string) string {
	return strings.ReplaceAll(identityXML, testAccount, account)
}

func assumeXML(expires time.Time) string { return assumeNumbered(expires, 0) }

// assumeNumbered issues a session whose token differs per n, as every real session does.
func assumeNumbered(expires time.Time, n int) string {
	token := sessionToken
	if n > 0 {
		token += fmt.Sprint(n)
	}
	return `<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleResult><Credentials>` +
		`<AccessKeyId>` + sessionKeyID + `</AccessKeyId><SecretAccessKey>` + sessionSecret + `</SecretAccessKey>` +
		`<SessionToken>` + token + `</SessionToken><Expiration>` + expires.Format(time.RFC3339) +
		`</Expiration></Credentials><AssumedRoleUser><Arn>arn:aws:sts::123456789012:assumed-role/Qatlas/x</Arn>` +
		`<AssumedRoleId>AROAEXAMPLE:x</AssumedRoleId></AssumedRoleUser></AssumeRoleResult>` +
		`<ResponseMetadata><RequestId>aaaaaaaa-0000-0000-0000-000000000000</RequestId></ResponseMetadata></AssumeRoleResponse>`
}

func errorXML(code string) string {
	return `<ErrorResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><Error><Type>Sender</Type><Code>` + code +
		`</Code><Message>provider text with ` + testSecret + `</Message></Error><RequestId>bbbbbbbb-0000-0000-0000-000000000000</RequestId></ErrorResponse>`
}

// sent is one request the fake received.
type sent struct {
	// method, path and rawQuery matter to the REST protocols; the awsQuery tests leave them be.
	method, path, rawQuery string
	host                   string
	action                 string
	form                   url.Values
	header                 http.Header
	body                   string
}

// reply is what the fake answers.
type reply struct {
	status int
	header map[string]string
	body   string
	err    error
}

func ok(body string) reply { return reply{status: 200, body: body} }

// fakeAWS is the whole AWS the tests talk to. It answers by action; a handler may look at the request.
type fakeAWS struct {
	mu       sync.Mutex
	requests []sent
	answer   func(sent, int) reply
}

func (f *fakeAWS) RoundTrip(req *http.Request) (*http.Response, error) {
	data, _ := io.ReadAll(req.Body)
	form, _ := url.ParseQuery(string(data))
	f.mu.Lock()
	r := sent{method: req.Method, path: req.URL.EscapedPath(), rawQuery: req.URL.RawQuery, host: req.URL.Host, action: form.Get("Action"), form: form, header: req.Header.Clone(), body: string(data)}
	f.requests = append(f.requests, r)
	n := 0
	for _, prior := range f.requests {
		if prior.action == r.action {
			n++
		}
	}
	f.mu.Unlock()
	if req.URL.Scheme != "https" {
		return nil, errors.New("plain http request")
	}
	answer := f.answer(r, n)
	if answer.err != nil {
		return nil, answer.err
	}
	header := http.Header{}
	for k, v := range answer.header {
		header.Set(k, v)
	}
	return &http.Response{StatusCode: answer.status, Header: header, Body: io.NopCloser(strings.NewReader(answer.body)),
		Request: req}, nil
}

func (f *fakeAWS) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		out = append(out, r.action)
	}
	return out
}

func (f *fakeAWS) count(action string) int {
	n := 0
	for _, a := range f.actions() {
		if a == action {
			n++
		}
	}
	return n
}

// install replaces the transport, the clock, the caches, and the rate limits for one test. The limiter reads
// the same clock as the signer and never sleeps, so a test can see how long a hold was asked for.
func install(t *testing.T, f *fakeAWS) *testClock {
	t.Helper()
	c := &testClock{now: testNow}
	oldTransport, oldClock, oldLimiters, oldAgent := transport, clock, limiters, userAgent
	transport, clock, limiters = f, c, ratelimit.NewRegistry(0)
	userAgent = agentFor("1.2.3")
	resetCaches()
	t.Cleanup(func() {
		transport, clock, limiters, userAgent = oldTransport, oldClock, oldLimiters, oldAgent
		resetCaches()
	})
	return c
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// holdLimiter replaces the limiter of the test connection with one on the test clock that records every
// sleep instead of sleeping.
func holdLimiter(c *testClock, connection string) *[]time.Duration {
	var slept []time.Duration
	limiters.Replace(limiterKey(connection, endpoints.STS), ratelimit.New(0, c.Now,
		func(_ context.Context, d time.Duration) error { slept = append(slept, d); c.advance(d); return nil }))
	return &slept
}

const (
	envKeyID, envSecret, envToken, envRole, envExternal = "AWS_T_KEY", "AWS_T_SECRET", "AWS_T_TOKEN", "AWS_T_ROLE", "AWS_T_EXT"
)

// values holds the secrets the resolver serves, by environment variable.
type values map[string]string

func baseValues() values {
	return values{envKeyID: testKeyID, envSecret: testSecret}
}

func roleValues() values {
	v := baseValues()
	v[envRole], v[envExternal] = testRoleARN, testExternal
	return v
}

func (v values) with(name, value string) values {
	out := values{}
	for k, val := range v {
		out[k] = val
	}
	out[name] = value
	return out
}

func resolverFor(v values, red *redact.Redactor) *secret.Resolver {
	return secret.NewWith(func(name string) string { return v[name] }, nil, nil, red)
}

func credentialConfig() config.Credential {
	return config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{
		roleAccessKeyID: envKeyID, roleSecretAccessKey: envSecret, roleSessionToken: envToken,
		roleRoleARN: envRole, roleExternalID: envExternal,
	}}
}

func connection(targets ...string) *config.Resolved {
	if len(targets) == 0 {
		targets = []string{"account/" + testAccount, "region/" + testRegion}
	}
	return &config.Resolved{Name: "prod", Provider: Provider, BaseURL: consoleOrigin, Credential: "aws-cred",
		Secrets: credentialConfig(), Targets: targets}
}

func registered(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg, "1.2.3"); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatalf("ValidateProfiles() = %v", err)
	}
	return reg
}

// env is an application core over the fake AWS.
type env struct {
	fake *fakeAWS
	core *application.Core
	red  *redact.Redactor
}

func newEnv(t *testing.T, f *fakeAWS, v values, targets ...string) *env {
	t.Helper()
	install(t, f)
	red := &redact.Redactor{}
	conn := config.Connection{Service: "aws", Credential: "aws-cred", Targets: targets}
	if len(targets) == 0 {
		conn.Targets = []string{"account/" + testAccount, "region/" + testRegion}
	}
	cfg := &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"aws": {Provider: Provider, BaseURL: consoleOrigin}},
		Credentials: map[string]config.Credential{"aws-cred": credentialConfig()},
		Connections: map[string]config.Connection{"prod": conn},
	}
	core := application.New(registered(t), cfg, resolverFor(v, red), red)
	return &env{fake: f, core: core, red: red}
}

func (e *env) invoke(operation, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "prod", Arguments: json.RawMessage(arguments)})
	return string(response.Result), err
}

func classOf(err error) provider.Class {
	var failed *provider.Error
	if errors.As(err, &failed) {
		return failed.Class
	}
	return ""
}

func isInvalidRequest(err error) bool {
	var invalid *provider.InvalidRequestError
	return errors.As(err, &invalid)
}

// direct answers every GetCallerIdentity with the bound account and everything else by the given function.
func direct(other func(sent, int) reply) *fakeAWS {
	return &fakeAWS{answer: func(r sent, n int) reply {
		if r.action == "GetCallerIdentity" {
			return ok(identityXML)
		}
		if other != nil {
			return other(r, n)
		}
		return reply{status: 500}
	}}
}

// mustNotLeak fails when text holds any credential value.
func mustNotLeak(t *testing.T, what, text string, extra ...string) {
	t.Helper()
	for _, secretValue := range append([]string{testSecret, testToken, testExternal, sessionSecret, sessionToken}, extra...) {
		if strings.Contains(text, secretValue) {
			t.Errorf("%s holds a credential value: %s", what, fmt.Sprintf("%.60s", text))
		}
	}
}

func errorsAs(err error, target any) bool { return errors.As(err, target) }

// newRedactorFor is not available for an env built by newEnv, whose core holds its redactor; this one tests
// the registration of a cached session with a redactor that never saw the fetch.
func newRedactorFor(t *testing.T, e *env) *redact.Redactor {
	t.Helper()
	fresh := &redact.Redactor{}
	client, err := open(context.Background(), connection(), resolverFor(roleValues(), fresh), fresh)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.effective(context.Background(), testRegion); err != nil {
		t.Fatal(err)
	}
	return fresh
}
