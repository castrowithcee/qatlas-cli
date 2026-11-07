package aws

import (
	"context"
	"net/http"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/awsquery"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/endpoints"
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

	// maxRetries bounds the automatic repetitions of a read; maxRetryWait is the longest Retry-After a
	// repetition waits for, and maxHold the longest one the limiter keeps for later requests.
	maxRetries    = 2
	maxRetryWait  = 10 * time.Second
	maxHold       = time.Minute
	serverBackoff = 500 * time.Millisecond
)

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
// It repeats a read a bounded number of times: only a rate limit that carries a usable Retry-After, and a
// server-side failure once more after a short pause. A rate limit without Retry-After is never repeated.
func (c *Client) send(ctx context.Context, op string, service endpoints.Service, region string,
	creds sigv4.Credentials, call awsquery.Call, out any) *provider.Error {
	endpoint, err := endpoints.Resolve(service, region)
	if err != nil {
		return provider.Fail(op, "the AWS endpoint is not available for this region")
	}
	client := &awsquery.Client{
		HTTP: c.http, URL: endpoint.URL(), UserAgent: userAgent,
		Signer: &sigv4.Signer{Credentials: creds, Region: endpoint.SigningRegion, Service: endpoint.SigningName, Clock: clock},
	}
	limiter := limiters.For(limiterKey(c.name, service))
	for attempt := 0; ; attempt++ {
		if err := limiter.Wait(ctx); err != nil {
			return provider.Waited(op, "AWS", err)
		}
		meta, failure := client.Do(ctx, op, call, out)
		if failure == nil {
			return nil
		}
		hold := meta.RetryAfter
		if hold > maxHold {
			hold = maxHold
		}
		limiter.HoldFor(hold)
		again := false
		switch {
		case failure.Class == provider.ClassRateLimited:
			again = meta.RetryAfter > 0 && meta.RetryAfter <= maxRetryWait
		case failure.Class == provider.ClassProviderError && failure.Original != nil && failure.Original.Status >= 500:
			limiter.HoldFor(serverBackoff)
			again = true
		}
		if !again || attempt >= maxRetries {
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
	if failure := c.send(ctx, op, endpoints.STS, region, creds,
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
