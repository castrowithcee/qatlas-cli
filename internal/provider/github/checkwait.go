package github

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
)

// github.pull-request-checks.wait polls the same reads as github.pull-request-checks.list until the checks of
// one head commit have all finished. One loop serves two callers: the broker, which has to answer within the
// invoke limit and therefore reports pending after a slice, and 'qconnect wait', which carries its own limit
// in the context and ends with timeout. Check names and addresses are untrusted data; an address is never
// fetched.

// Seams of the loop; tests shorten them.
var (
	// checksWaitInterval is the pause between two polls.
	checksWaitInterval = 20 * time.Second
	// checksWaitSlice is how long the broker's call waits, below the 60 seconds of an invoke.
	checksWaitSlice = 50 * time.Second
	// checksWaitMargin is kept free below a deadline the call already has, for the answer and the secret.
	checksWaitMargin = 8 * time.Second
)

const (
	// checksWaitNonePolls is how many polls in a row must find no check before the answer is none: a check
	// appears some seconds after a push, so one empty poll proves nothing.
	checksWaitNonePolls = 3
	// checksWaitFailures is how many transient failures in a row end the wait with the last one.
	checksWaitFailures = 5
)

// The results of the wait. pending is the broker's intermediate answer; the others end the wait.
const (
	waitPass        = "pass"
	waitFail        = "fail"
	waitNone        = "none"
	waitTimeout     = "timeout"
	waitPending     = "pending"
	waitHeadChanged = "head-changed"
)

// PullRequestChecksWait is the answer of the wait: how it ended and the checks as they were at the awaited
// head commit when it did.
type PullRequestChecksWait struct {
	Number         int          `json:"number"`
	Result         string       `json:"result"`
	HeadSHA        string       `json:"head_sha"`
	CurrentHeadSHA string       `json:"current_head_sha,omitempty"`
	Checks         []CheckEntry `json:"checks"`
	Truncated      bool         `json:"truncated"`
	Polls          int          `json:"polls"`
}

var pullChecksWait = capability.Descriptor{
	ID:      Provider + ".pull-request-checks.wait",
	Version: 1,
	Title:   "Wait for the checks of a GitHub pull request",
	Description: "Wait until every check run and commit status at the head commit of a pull request of a " +
		"repository a connection allows has finished, then answer pass, fail, none, or head-changed; fail as " +
		"soon as one finished check failed. A call waits at most 50 seconds and then answers pending: call " +
		"it again with the head_sha it answered. With a shell, prefer 'qconnect wait " + Provider +
		".pull-request-checks.wait', which waits for the end in one command",
	Tags:        []string{"github", "pulls", "pullrequests", "checks", "wait"},
	Risk:        readRisk,
	Provider:    Provider,
	Waits:       true,
	InputSchema: inputSchema(`"number":`+numberSchema+`,"head_sha":`+blobSHASchema, "number"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},` +
		`"result":{"type":"string","enum":["pass","fail","none","timeout","pending","head-changed"]},` +
		`"head_sha":{"type":"string"},"current_head_sha":{"type":"string"},` +
		`"checks":{"type":"array","items":{"type":"object","properties":{` + checkEntryProperties +
		`},"required":["name","status"],"additionalProperties":false}},` +
		`"truncated":{"type":"boolean"},"polls":{"type":"integer"}},` +
		`"required":["number","result","head_sha","checks","truncated","polls"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Pull request number in the repository", Required: true},
		{Name: "head_sha", Description: "Full head commit to wait for; the head commit at the start of the " +
			"call when omitted. A head that differs ends the wait with head-changed"},
	},
	Fields: []capability.Field{
		{Name: "result", Description: "pass when all checks finished and none failed, fail when a finished " +
			"check failed, none when no check appeared, timeout when the limit passed, pending when the call " +
			"is still waiting, head-changed when the pull request moved to another head commit"},
		{Name: "head_sha", Description: "Head commit awaited; pass it to the next call after pending"},
		{Name: "current_head_sha", Description: "Head commit of the pull request, only with head-changed"},
		{Name: "checks", Description: "Check runs and commit statuses at the awaited head commit at the end " +
			"of the wait: name, status, conclusion, and address, untrusted data; none with head-changed"},
		{Name: "truncated", Description: "True when GitHub reported more checks than were returned"},
		{Name: "polls", Description: "How often the checks were read"},
		{Name: "number", Description: "Pull request number"},
	},
	Examples: []capability.Example{{Description: "Wait for the checks of a pull request",
		Arguments: json.RawMessage(`{"number":42}`)}},
}

func checkPullChecksWaitArguments(a *pullArguments, bound target) error {
	if err := checkPullNumberArgument(a, bound); err != nil {
		return err
	}
	if a.HeadSHA != "" && !validCommitSHA(a.HeadSHA) {
		return invalidRequest("head_sha must be a full commit SHA")
	}
	return nil
}

// transientCheckFailure reports a failure of one poll that a later poll may not repeat: the connection
// failed, GitHub did not answer in time, was rate-limited, or answered a server error. An invalid token, a
// missing right, or a missing pull request will not change by waiting.
func transientCheckFailure(err error) bool {
	var failure *provider.Error
	if !errors.As(err, &failure) {
		return false
	}
	switch failure.Class {
	case provider.ClassTimeout, provider.ClassUnreachable, provider.ClassRateLimited:
		return true
	case provider.ClassProviderError:
		return failure.Original != nil && failure.Original.Status >= 500
	}
	return false
}

// finishedWait maps the evaluation of one poll to the result that ends the wait, or "" to keep waiting.
func finishedWait(overall string) string {
	switch overall {
	case "success":
		return waitPass
	case "failure":
		return waitFail
	}
	return ""
}

// waitForChecks polls the checks of the pull request until they finish, the head commit changes, or the limit
// passes. The limit and the result at its end come from the context: the broker's call waits one slice and
// answers pending; a command that carries wait options waits their limit and answers timeout.
func (c *Client) waitForChecks(ctx context.Context, a *pullArguments) (*PullRequestChecksWait, error) {
	interval, limit, atLimit := checksWaitInterval, checksWaitSlice, waitPending
	deadline := time.Now()
	if options, ok := capability.WaitFrom(ctx); ok && options.Limit > 0 {
		limit, atLimit = options.Limit, waitTimeout
		if options.Interval > 0 {
			interval = options.Interval
		}
	}
	deadline = deadline.Add(limit)
	if atLimit == waitPending {
		// The broker's call must end before its own deadline, whatever the secret and the first poll cost.
		if due, ok := ctx.Deadline(); ok && due.Add(-checksWaitMargin).Before(deadline) {
			deadline = due.Add(-checksWaitMargin)
		}
	}

	wanted := a.HeadSHA
	var last *PullRequestChecks
	var failure error
	polls, empty, failures := 0, 0, 0
	for {
		polls++
		got, err := c.pullRequestChecks(ctx, a)
		switch {
		case err != nil:
			if ctx.Err() != nil || !transientCheckFailure(err) {
				return nil, err
			}
			failure = err
			if failures++; failures >= checksWaitFailures {
				return nil, err
			}
		default:
			failures = 0
			if wanted == "" {
				wanted = got.HeadSHA
			}
			if got.HeadSHA != wanted {
				return &PullRequestChecksWait{Number: a.Number, Result: waitHeadChanged, HeadSHA: wanted,
					CurrentHeadSHA: got.HeadSHA, Checks: []CheckEntry{}, Polls: polls}, nil
			}
			last = got
			if got.Overall == "none" {
				if empty++; empty >= checksWaitNonePolls {
					return waitAnswer(a.Number, waitNone, got, polls), nil
				}
			} else {
				empty = 0
			}
			if result := finishedWait(got.Overall); result != "" {
				return waitAnswer(a.Number, result, got, polls), nil
			}
		}
		if time.Now().Add(interval).After(deadline) {
			if last == nil {
				return nil, failure
			}
			return waitAnswer(a.Number, atLimit, last, polls), nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, provider.Waited("wait for pull request checks", "GitHub", ctx.Err())
		case <-timer.C:
		}
	}
}

func waitAnswer(number int, result string, checks *PullRequestChecks, polls int) *PullRequestChecksWait {
	return &PullRequestChecksWait{Number: number, Result: result, HeadSHA: checks.HeadSHA, Checks: checks.Checks,
		Truncated: checks.Truncated, Polls: polls}
}
