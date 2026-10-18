package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Bot API limits in Unicode characters.
const (
	maxBusinessFirstNameRunes = 64
	maxBusinessLastNameRunes  = 64
	maxBusinessUsernameRunes  = 32
	minBusinessUsernameRunes  = 5
	maxBusinessBioRunes       = 140
)

func businessProfileDescriptor(name, title, desc string, list bool, props, required string,
	args []capability.Argument, example string) capability.Descriptor {
	return capability.Descriptor{
		ID:          Provider + ".business." + name,
		Version:     1,
		Title:       title,
		Description: desc,
		Tags:        []string{"telegram", "business", "update"},
		Risk: capability.Risk{
			Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: businessDataSensitivity,
		},
		Provider: Provider, RequiresToolAllowList: list, Group: groupBusiness,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + businessConnectionSchema + `,` + props +
			`},"required":[` + required + `],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"set":{"type":"boolean"}},` +
			`"required":["set"],"additionalProperties":false}`),
		Arguments: append([]capability.Argument{{Name: "business", Description: businessArgument, Required: true}}, args...),
		Fields:    []capability.Field{{Name: "set", Description: "True when Telegram accepted the change"}},
		Examples:  []capability.Example{{Description: title, Arguments: json.RawMessage(example)}},
	}
}

var (
	businessSetName = businessProfileDescriptor("set-name", "Set the Telegram business account name",
		"Change the first and last name of the account of a bound business connection; Telegram checks the bot's rights",
		false,
		`"first_name":{"type":"string","minLength":1,"maxLength":64},"last_name":{"type":"string","maxLength":64}`,
		`"business","first_name"`,
		[]capability.Argument{
			{Name: "first_name", Description: "New first name, from 1 through 64 characters", Required: true},
			{Name: "last_name", Description: "New last name, up to 64 characters; absent or empty removes it"},
		}, `{"business":"<connection id>","first_name":"Ann","last_name":"Example"}`)
	businessSetUsername = businessProfileDescriptor("set-username", "Set the Telegram business account username",
		"Change or remove the username of the account of a bound business connection; the old username can be lost",
		true,
		`"username":{"type":"string","maxLength":32,"pattern":"^([A-Za-z0-9_]{5,32})?$"}`,
		`"business"`,
		[]capability.Argument{{Name: "username", Description: "New username without @, from 5 through 32 letters, " +
			"digits, or underscores; absent or empty removes it"}},
		`{"business":"<connection id>","username":"example_name"}`)
	businessSetBio = businessProfileDescriptor("set-bio", "Set the Telegram business account bio",
		"Change or remove the bio of the account of a bound business connection; Telegram checks the bot's rights",
		false,
		`"bio":{"type":"string","maxLength":140}`,
		`"business"`,
		[]capability.Argument{{Name: "bio", Description: "New bio, up to 140 characters; absent or empty removes it"}},
		`{"business":"<connection id>","bio":"Opening hours 9-17"}`)
)

func validBusinessUsername(s string) bool {
	if s == "" {
		return true
	}
	if len(s) < minBusinessUsernameRunes || len(s) > maxBusinessUsernameRunes {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_') {
			return false
		}
	}
	return true
}

// businessProfileCall decodes and checks the arguments, passes the business gate, and only then opens the client.
func businessProfileCall(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	httpClient *http.Client, op string, raw json.RawMessage, allowed []string,
	check func(args map[string]string) error, method string, payload func(args map[string]string) businessProfileBody) (any, error) {
	var arguments map[string]string
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	for key := range arguments {
		if key != "business" && !slices.Contains(allowed, key) {
			return nil, providerError(op, "an argument is not supported")
		}
	}
	if err := check(arguments); err != nil {
		return nil, err
	}
	id := arguments["business"]
	if err := scopeBusiness(op, resolved, id); err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.setBusinessProfile(ctx, op, id, method, payload(arguments))
}

// businessProfileBody leaves an empty field out, which makes Telegram remove that value.
type businessProfileBody struct {
	ID        string `json:"business_connection_id"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
	Bio       string `json:"bio,omitempty"`
}

// setBusinessProfile performs exactly one request without retrying an ambiguous result.
func (c *Client) setBusinessProfile(ctx context.Context, op, id, method string, body businessProfileBody) (map[string]any, error) {
	if !validBusinessID(id) || !c.set.hasBusiness(id) {
		return nil, providerError(op, "this connection does not bind that business connection")
	}
	body.ID = id
	if err := c.confirmed(ctx, spec{op: op, method: method, limit: defaultResponseBytes}, body); err != nil {
		return nil, err
	}
	return map[string]any{"set": true}, nil
}

func invokeBusinessSetName(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeBusinessSetNameWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeBusinessSetNameWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "set business account name"
	return businessProfileCall(ctx, resolved, secrets, red, httpClient, op, raw, []string{"first_name", "last_name"},
		func(a map[string]string) error {
			if err := checkRunes(op, "first_name", a["first_name"], 1, maxBusinessFirstNameRunes); err != nil {
				return err
			}
			return checkRunes(op, "last_name", a["last_name"], 0, maxBusinessLastNameRunes)
		}, "setBusinessAccountName",
		func(a map[string]string) businessProfileBody {
			return businessProfileBody{FirstName: a["first_name"], LastName: a["last_name"]}
		})
}

func invokeBusinessSetUsername(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeBusinessSetUsernameWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeBusinessSetUsernameWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "set business account username"
	return businessProfileCall(ctx, resolved, secrets, red, httpClient, op, raw, []string{"username"},
		func(a map[string]string) error {
			// Stricter than Telegram: only 5 through 32 letters, digits, or underscores.
			if !validBusinessUsername(a["username"]) {
				return providerError(op, "username must be empty or 5 through 32 letters, digits, or underscores")
			}
			return nil
		}, "setBusinessAccountUsername",
		func(a map[string]string) businessProfileBody { return businessProfileBody{Username: a["username"]} })
}

func invokeBusinessSetBio(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeBusinessSetBioWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeBusinessSetBioWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "set business account bio"
	return businessProfileCall(ctx, resolved, secrets, red, httpClient, op, raw, []string{"bio"},
		func(a map[string]string) error { return checkRunes(op, "bio", a["bio"], 0, maxBusinessBioRunes) },
		"setBusinessAccountBio",
		func(a map[string]string) businessProfileBody { return businessProfileBody{Bio: a["bio"]} })
}
