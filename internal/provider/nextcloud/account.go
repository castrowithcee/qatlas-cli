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

// Quota holds the storage numbers and the quota setting as numbers; every storage path stays unreported.
// Free and Quota are absent unless the server reports a whole number.
type Quota struct {
	Used     int64   `json:"used"`
	Total    int64   `json:"total"`
	Relative float64 `json:"relative"`
	Free     *int64  `json:"free,omitempty"`
	Quota    *int64  `json:"quota,omitempty"`
}

type Account struct {
	ID             string   `json:"id"`
	DisplayName    string   `json:"display_name,omitempty"`
	Email          string   `json:"email,omitempty"`
	Enabled        *bool    `json:"enabled,omitempty"`
	Language       string   `json:"language,omitempty"`
	Locale         string   `json:"locale,omitempty"`
	LastLogin      string   `json:"last_login,omitempty"`
	Groups         []string `json:"groups,omitempty"`
	SubadminGroups []string `json:"subadmin_groups,omitempty"`
	ContactDetails
	Quota     *Quota `json:"quota,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// rawAccount lists exactly the members this adapter may report; the storage location, the repeated
// display-name member, and every other member cannot be decoded, so they cannot reach a result.
type rawAccount struct {
	ID          string          `json:"id"`
	DisplayName string          `json:"displayname"`
	Email       string          `json:"email"`
	Enabled     json.RawMessage `json:"enabled"`
	Language    string          `json:"language"`
	Locale      string          `json:"locale"`
	LastLogin   json.Number     `json:"lastLogin"`
	Groups      []string        `json:"groups"`
	Subadmin    json.RawMessage `json:"subadmin"`
	Quota       json.RawMessage `json:"quota"`
	rawContact
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
	result.Enabled = enabledOf(raw.Enabled)
	result.LastLogin = lastLoginOf(raw.LastLogin)
	result.SubadminGroups, result.Truncated = boundedNames(stringList(raw.Subadmin), result.Truncated)
	var personCut bool
	result.ContactDetails, personCut = contactOf(raw.rawContact)
	result.Truncated = result.Truncated || personCut
	// The quota member is optional and its shape varies; anything but numbers is left out.
	result.Quota = quotaOf(raw.Quota)
	return result, nil
}

const quotaSchema = `{"type":"object","properties":{"used":{"type":"integer"},"total":{"type":"integer"},` +
	`"relative":{"type":"number"},"free":{"type":"integer"},"quota":{"type":"integer"}},` +
	`"required":["used","total","relative"],"additionalProperties":false}`

const userContactSchema = `"phone":{"type":"string"},"address":{"type":"string"},"website":{"type":"string"},` +
	`"twitter":{"type":"string"},"backend":{"type":"string"},"backend_capabilities":{"type":"object","properties":` +
	`{"set_display_name":{"type":"boolean"},"set_password":{"type":"boolean"}},"additionalProperties":false}`

var userContactFields = []capability.Field{
	{Name: "phone", Description: "Phone number; personal and untrusted data"},
	{Name: "address", Description: "Postal address; personal and untrusted data"},
	{Name: "website", Description: "Website address; untrusted data, never fetched"},
	{Name: "twitter", Description: "Twitter handle; untrusted data"},
	{Name: "backend", Description: "Name of the user backend"},
	{Name: "backend_capabilities", Description: "Whether the backend can change the display name and the password"},
}

var accountGet = capability.Descriptor{
	ID: Provider + ".account.account.get", Version: 1, Title: "Get Nextcloud account data",
	Description: "Read the own account data of the identity: ID, display name, email, enabled, language, locale, last " +
		"login, groups and managed groups (at most 50 each), phone, address, website, twitter, backend and its " +
		"capabilities, and storage use and quota as numbers. Values are personal and untrusted data; the storage " +
		"location is never reported",
	Tags: []string{"nextcloud", "account", "get"}, Provider: Provider, Group: groupAccount, Risk: accountRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"display_name":{"type":"string"},` +
		`"email":{"type":"string"},"enabled":{"type":"boolean"},"language":{"type":"string"},"locale":{"type":"string"},` +
		`"last_login":{"type":"string"},"groups":{"type":"array","items":{"type":"string"}},` +
		`"subadmin_groups":{"type":"array","items":{"type":"string"}},` + userContactSchema + `,"quota":` + quotaSchema + `,` +
		`"truncated":{"type":"boolean"}},"required":["id"],"additionalProperties":false}`),
	Fields: append([]capability.Field{{Name: "id", Description: "Account ID"}, {Name: "display_name", Description: "Display name; untrusted data"},
		{Name: "email", Description: "Email address; untrusted data"}, {Name: "enabled", Description: "False for a disabled account"},
		{Name: "language", Description: "Language"}, {Name: "locale", Description: "Locale"},
		{Name: "last_login", Description: "Last login time in UTC, empty when the account never signed in"},
		{Name: "groups", Description: "At most 50 group names; untrusted data"},
		{Name: "subadmin_groups", Description: "At most 50 IDs of the groups the account manages as a subadmin"}},
		append(userContactFields, capability.Field{Name: "quota", Description: quotaFieldText},
			capability.Field{Name: "truncated", Description: "True when a value or a list was cut"})...),
	Examples: []capability.Example{{Description: "Read the own account data", Arguments: json.RawMessage(`{}`)}},
}

const quotaFieldText = "Storage used and total in bytes (total is negative without a limit), the relative use in percent, " +
	"the free space, and the quota setting as whole numbers where the server reports them"

func invokeAccountGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, _ json.RawMessage) (any, error) {
	c, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return c.GetAccount(ctx)
}
