package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The lock tools lock and unlock the conversation of an issue or pull request (one index space) inside the
// connection's targets. A change first proves by the evidence read that the canonical repository of the index
// lies inside the targets and then sends exactly one request. The instance documents only 204 and no answer for
// a state that already holds, so both are declared idempotent. Forgejo has the operation from 17; Gitea has it
// at its minimum.

const maxLockReason = 255

var lockSince = map[string]version{forgejo.id: {17, 0, 0}, gitea.id: gitea.minimum}

var lockReasonArgument = capability.Argument{Name: "reason",
	Description: "Why the conversation is locked, plain text of at most 255 characters without control characters"}

type lockCall struct {
	Repository string  `json:"repository"`
	Number     int64   `json:"number"`
	Reason     *string `json:"reason"`
}

func checkLockReason(reason *string) error {
	if reason == nil {
		return nil
	}
	if utf8.RuneCountInString(*reason) > maxLockReason || !utf8.ValidString(*reason) {
		return invalidArgument("reason must be valid text of at most 255 characters")
	}
	for _, r := range *reason {
		if unicode.IsControl(r) {
			return invalidArgument("reason must not contain control characters")
		}
	}
	return nil
}

// lockChange builds the lock or the unlock of one issue or pull request.
func lockChange(locks bool) *tool {
	action, title, done := "unlock", "Unlock", "unlocked"
	if locks {
		action, title, done = "lock", "Lock", "locked"
	}
	return &tool{
		group: "issue-locks", action: action, since: lockSince,
		describe: func(p *product) capability.Descriptor {
			properties, required := `"repository":`+repoSchema+`,"number":`+issueSchemaNumber, []string{"number"}
			arguments := []capability.Argument{repositoryArgument, numberArgument}
			example := `{"repository":"example/project","number":7`
			if locks {
				properties += `,"reason":{"type":"string","maxLength":255}`
				arguments = append(arguments, lockReasonArgument)
				example += `,"reason":"resolved"`
			}
			return capability.Descriptor{
				Title: title + " a " + p.name + " issue or pull request",
				Description: title + " the conversation of an issue or pull request of a repository inside the " +
					"connection's targets; it is read first and its canonical repository must lie inside the " +
					"targets. A repeat reaches the same state.",
				Tags:        []string{p.id, "issue-locks", action},
				Risk:        changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+issueSensitivity),
				InputSchema: inputSchema(properties, required...),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},"` + done +
					`":{"type":"boolean"}},"required":["number","` + done + `"],"additionalProperties":false}`),
				Arguments: arguments,
				Fields: []capability.Field{
					{Name: "number", Description: "The issue or pull request the call acted on"},
					{Name: done, Description: "True once the instance accepted the change"}},
				Examples: []capability.Example{{Description: title + " issue 7",
					Arguments: json.RawMessage(example + `}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a lockCall
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			repo, err := allowed.chooseRepository(a.Repository)
			if err != nil {
				return nil, err
			}
			if a.Number < 1 {
				return nil, invalidArgument("number must be the index of an issue or pull request")
			}
			if !locks && a.Reason != nil {
				return nil, invalidArgument("reason is not an argument of unlock")
			}
			if err := checkLockReason(a.Reason); err != nil {
				return nil, err
			}
			method, op := http.MethodDelete, "unlock issue"
			var body any
			if locks {
				// The reason is named differently by the two products.
				field := map[string]string{forgejo.id: "reason", gitea.id: "lock_reason"}[p.id]
				method, op = http.MethodPut, "lock issue"
				payload := map[string]any{}
				if a.Reason != nil && *a.Reason != "" {
					payload[field] = *a.Reason
				}
				body = payload
			}
			path := issuePath(repo, a.Number) + "/lock"
			return func(ctx context.Context, c *Client) (any, error) {
				if err := readIndex(ctx, c, allowed, op, repo, a.Number); err != nil {
					return nil, err
				}
				if err := c.change(ctx, op, method, path, body, nil); err != nil {
					return nil, err
				}
				return map[string]any{"number": a.Number, done: true}, nil
			}, nil
		},
	}
}

var (
	issueLocksLock   = lockChange(true)
	issueLocksUnlock = lockChange(false)
)
