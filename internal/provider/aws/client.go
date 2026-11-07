package aws

import (
	"context"
	"net/http"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/awsquery"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/awsretry"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/endpoints"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/httpbind"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/sigv4"
	"github.com/castrowithcee/qatlas-connect/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// Bounds of one request and of one invoke. The budget stays below the 60 s invoke limit even when a call
// needs a role assumption and an account check first.
const (
	requestTimeout = 15 * time.Second
	invokeBudget   = 50 * time.Second

	// minInterval spaces the requests of one connection to one service. STS is generous; this is a
	// conservative start, not a measured limit.
	minInterval = 250 * time.Millisecond
)

// retryPolicy bounds the automatic repetitions of a safe read; tests replace it to fix the jitter.
var retryPolicy = awsretry.Default

// maxRetries is the number of repetitions after the first attempt.
var maxRetries = retryPolicy.MaxRetries

var limiters = ratelimit.NewRegistry(minInterval)

// transport carries every request. A nil value is Go's default transport; tests replace it.
var transport http.RoundTripper

// clock is the time source of signatures and of the session lifetime. Tests replace it.
var clock sigv4.Clock = sigv4.ClockFunc(time.Now)

// userAgent names Qatlas in every request, so CloudTrail can attribute it. Register sets it from the build.
var userAgent = agentFor("")

// Client is one selected connection: its binding, its credential, and the transport it uses. Every host it
// contacts comes from the endpoint table.
type Client struct {
	name    string
	binding binding
	creds   credentials
	http    awsquery.Doer
	red     *redact.Redactor
}

// open resolves a connection's binding and credential. The binding is read first, so a refused target never
// resolves a secret.
func open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	const op = "open"
	if resolved == nil {
		return nil, provider.Fail(op, "no connection was selected")
	}
	b, err := bindingOf(resolved)
	if err != nil {
		return nil, provider.Fail(op, err.Error())
	}
	return openBound(ctx, resolved, b, secrets, red)
}

func openBound(ctx context.Context, resolved *config.Resolved, b binding, secrets *secret.Resolver,
	red *redact.Redactor) (*Client, error) {
	creds, err := resolveCredentials(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// A role of another account cannot reach the bound one, so the mismatch is refused before any request.
	if account := creds.roleAccount(); account != "" && account != b.account {
		return nil, mismatch("open")
	}
	return &Client{
		name: resolved.Name, binding: b, creds: creds, red: red,
		http: provider.NoRedirectClient(requestTimeout, transport),
	}, nil
}

// mismatchMessage is the refusal of a credential that belongs to another account than the connection's.
// It names no account ID: neither the bound one nor the one that answered.
const mismatchMessage = "the credential belongs to a different AWS account than the account this connection " +
	"is bound to; the request was refused and nothing was sent with it"

func mismatch(op string) *provider.Error {
	return &provider.Error{Class: provider.ClassPermission, Op: op, Message: mismatchMessage}
}

// send runs one signed awsQuery call against the fixed endpoint of service, one limiter slot per attempt.
// It repeats the call only when idem is IdempotencySafe and within retryPolicy: a rate limit that carries a
// usable Retry-After, and a server-side failure after a short pause, each with jitter. A rate limit without
// Retry-After is never repeated, and no other operation is repeated at all.
func (c *Client) send(ctx context.Context, op string, service endpoints.Service, region string,
	creds sigv4.Credentials, idem capability.Idempotency, call awsquery.Call, out any) *provider.Error {
	_, failure := c.sendMeta(ctx, op, service, region, creds, idem, call, out)
	return failure
}

// sendMeta is send that also returns what the successful answer said about itself, such as a cut string.
func (c *Client) sendMeta(ctx context.Context, op string, service endpoints.Service, region string,
	creds sigv4.Credentials, idem capability.Idempotency, call awsquery.Call, out any) (awsquery.Meta, *provider.Error) {
	endpoint, err := endpoints.Resolve(service, region)
	if err != nil {
		return awsquery.Meta{}, provider.Fail(op, "the AWS endpoint is not available for this region")
	}
	client := &awsquery.Client{
		HTTP: c.http, URL: endpoint.URL(), UserAgent: userAgent,
		Signer: &sigv4.Signer{Credentials: creds, Region: endpoint.SigningRegion, Service: endpoint.SigningName, Clock: clock},
	}
	var meta awsquery.Meta
	failure := c.attempts(ctx, op, service, idem, func() (time.Duration, *provider.Error) {
		var failure *provider.Error
		meta, failure = client.Do(ctx, op, call, out)
		return meta.RetryAfter, failure
	})
	return meta, failure
}

// sendRest is sendMeta for a REST protocol: one signed call of an httpbind operation, with the same limiter,
// retry and endpoint rules. req is the encoded input (Operation.Encode), codec the body format of the
// protocol (restjson.Codec). The signature covers the body hash and no x-amz-content-sha256 header, which
// only S3 sends.
func (c *Client) sendRest(ctx context.Context, op string, service endpoints.Service, region string,
	creds sigv4.Credentials, idem capability.Idempotency, codec httpbind.Codec, o *httpbind.Operation,
	req httpbind.Request, out *map[string]any) (httpbind.Meta, *provider.Error) {
	endpoint, err := endpoints.Resolve(service, region)
	if err != nil {
		return httpbind.Meta{}, provider.Fail(op, "the AWS endpoint is not available for this region")
	}
	client := &httpbind.Client{
		HTTP: c.http, Codec: codec, URL: endpoint.URL(), UserAgent: userAgent,
		Signer: &sigv4.Signer{Credentials: creds, Region: endpoint.SigningRegion, Service: endpoint.SigningName, Clock: clock},
	}
	var meta httpbind.Meta
	failure := c.attempts(ctx, op, service, idem, func() (time.Duration, *provider.Error) {
		var failure *provider.Error
		meta, failure = client.Do(ctx, op, o, req, out)
		return meta.RetryAfter, failure
	})
	return meta, failure
}

// attempts runs one call through the limiter of the connection and service, one slot per attempt. do makes
// one attempt and returns the Retry-After of its answer. The loop repeats the call only when the policy
// allows it, which it does for an IdempotencySafe operation only.
func (c *Client) attempts(ctx context.Context, op string, service endpoints.Service, idem capability.Idempotency,
	do func() (time.Duration, *provider.Error)) *provider.Error {
	limiter := limiters.For(limiterKey(c.name, service))
	for attempt := 0; ; attempt++ {
		if err := limiter.Wait(ctx); err != nil {
			return provider.Waited(op, "AWS", err)
		}
		retryAfter, failure := do()
		if failure == nil {
			return nil
		}
		decision := retryPolicy.Decide(idem, attempt, failure, retryAfter)
		limiter.HoldFor(decision.Hold)
		if !decision.Again {
			return failure
		}
	}
}

// limiterKey is the rate-limit budget: one per connection and service.
func limiterKey(connection string, service endpoints.Service) string {
	return "aws|" + connection + "|" + string(service)
}

// authorize returns the credentials that sign a call in region, after proving that they belong to the bound
// account: it assumes the role when one is configured, then checks GetCallerIdentity once per credential set
// (see accountSeen).
func (c *Client) authorize(ctx context.Context, op, region string) (sigv4.Credentials, error) {
	creds, err := c.effective(ctx, region)
	if err != nil {
		return sigv4.Credentials{}, err
	}
	if accountSeen(creds, c.binding.account) {
		return creds, nil
	}
	if _, failure := c.identity(ctx, op, region, creds); failure != nil {
		return sigv4.Credentials{}, failure
	}
	return creds, nil
}

// callerIdentity is the answer of GetCallerIdentity.
type callerIdentity struct {
	UserID  string `xml:"UserId"`
	Account string `xml:"Account"`
	Arn     string `xml:"Arn"`
}

// identity asks AWS who the credentials are and refuses them unless they belong to the bound account. Only
// a match is remembered.
func (c *Client) identity(ctx context.Context, op, region string, creds sigv4.Credentials) (callerIdentity, *provider.Error) {
	var who callerIdentity
	if failure := c.send(ctx, op, endpoints.STS, region, creds, capability.IdempotencySafe,
		awsquery.Call{Action: "GetCallerIdentity", Version: stsVersion}, &who); failure != nil {
		return callerIdentity{}, failure
	}
	if !accountID.MatchString(who.Account) {
		return callerIdentity{}, provider.InvalidResponse(op, "AWS returned an invalid response")
	}
	if who.Account != c.binding.account {
		return callerIdentity{}, mismatch(op)
	}
	rememberAccount(creds, c.binding.account)
	return who, nil
}
