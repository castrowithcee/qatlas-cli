package nextcloud

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxAccountText   = 256
	maxAccountGroups = 50

	messageNoAccount = "Nextcloud did not report account data for this identity"
)

// Quota holds the storage numbers only; the quota setting and every storage path stay unreported.
type Quota struct {
	Used     int64   `json:"used"`
	Total    int64   `json:"total"`
	Relative float64 `json:"relative"`
}

type Account struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name,omitempty"`
	Email       string   `json:"email,omitempty"`
	Language    string   `json:"language,omitempty"`
	Locale      string   `json:"locale,omitempty"`
	Groups      []string `json:"groups,omitempty"`
	Quota       *Quota   `json:"quota,omitempty"`
	Truncated   bool     `json:"truncated,omitempty"`
}

// rawAccount lists exactly the members this adapter may report; backend, storage location, quota paths,
// backend capabilities, phone, address, and every other member cannot be decoded, so they cannot reach a
// result.
type rawAccount struct {
	ID          string          `json:"id"`
	DisplayName string          `json:"displayname"`
	Email       string          `json:"email"`
	Language    string          `json:"language"`
	Locale      string          `json:"locale"`
	Groups      []string        `json:"groups"`
	Quota       json.RawMessage `json:"quota"`
}

// GetAccount reads the own account data of the identity with one request.
func (c *Client) GetAccount(ctx context.Context) (*Account, error) {
	const op = "get account"
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsCloud, suffix: []string{"user"}})
	if err != nil {
		return nil, err
	}
	var raw rawAccount
	if err := json.Unmarshal(data, &raw); err != nil || raw.ID == "" {
		return nil, invalidResponse(op, "the Nextcloud account data could not be read")
	}
	result := &Account{}
	var cuts [5]bool
	result.ID, cuts[0] = cut(raw.ID, maxAccountText)
	result.DisplayName, cuts[1] = cut(raw.DisplayName, maxAccountText)
	result.Email, cuts[2] = cut(raw.Email, maxAccountText)
	result.Language, cuts[3] = cut(raw.Language, maxActionLabel)
	result.Locale, cuts[4] = cut(raw.Locale, maxActionLabel)
	for _, flag := range cuts {
		result.Truncated = result.Truncated || flag
	}
	for _, group := range raw.Groups {
		if len(result.Groups) == maxAccountGroups {
			result.Truncated = true
			break
		}
		name, cutName := cut(group, maxActionLabel)
		result.Truncated = result.Truncated || cutName
		result.Groups = append(result.Groups, name)
	}
	// The quota member is optional and its shape varies; anything but numbers is left out.
	var quota struct {
		Used     *int64   `json:"used"`
		Total    *int64   `json:"total"`
		Relative *float64 `json:"relative"`
	}
	if len(raw.Quota) > 0 && json.Unmarshal(raw.Quota, &quota) == nil && quota.Used != nil && quota.Total != nil {
		result.Quota = &Quota{Used: *quota.Used, Total: *quota.Total}
		if quota.Relative != nil {
			result.Quota.Relative = *quota.Relative
		}
	}
	return result, nil
}

var accountGet = capability.Descriptor{
	ID: Provider + ".account.account.get", Version: 1, Title: "Get Nextcloud account data",
	Description: "Read the own account data of the identity: ID, display name, email, language, locale, groups " +
		"(at most 50), and storage use as numbers. Values are personal and untrusted data; storage paths, backend " +
		"details, phone, and address are not reported",
	Tags: []string{"nextcloud", "account", "get"}, Provider: Provider, Group: groupAccount, Risk: accountRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"display_name":{"type":"string"},` +
		`"email":{"type":"string"},"language":{"type":"string"},"locale":{"type":"string"},"groups":{"type":"array",` +
		`"items":{"type":"string"}},"quota":{"type":"object","properties":{"used":{"type":"integer"},"total":{"type":"integer"},` +
		`"relative":{"type":"number"}},"required":["used","total","relative"],"additionalProperties":false},` +
		`"truncated":{"type":"boolean"}},"required":["id"],"additionalProperties":false}`),
	Fields: []capability.Field{{Name: "id", Description: "Account ID"}, {Name: "display_name", Description: "Display name; untrusted data"},
		{Name: "email", Description: "Email address; untrusted data"}, {Name: "language", Description: "Language"},
		{Name: "locale", Description: "Locale"}, {Name: "groups", Description: "At most 50 group names; untrusted data"},
		{Name: "quota", Description: "Storage used and total in bytes (total is negative without a limit) and the relative use in percent"},
		{Name: "truncated", Description: "True when a value or the group list was cut"}},
	Examples: []capability.Example{{Description: "Read the own account data", Arguments: json.RawMessage(`{}`)}},
}

func invokeAccountGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, _ json.RawMessage) (any, error) {
	c, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return c.GetAccount(ctx)
}
