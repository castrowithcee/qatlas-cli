package telegram

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const forChannelsSchema = `"for_channels":{"type":"boolean"}`

const forChannelsDescription = "true for the default rights in channels, false for groups and supergroups"

// defaultRightsOutputSchema lists the booleans of ChatAdministratorRights, which equal those of promoteChatMember.
func defaultRightsOutputSchema() string {
	props, required := "", ""
	for i, name := range promoteRightNames {
		if i > 0 {
			props += ","
			required += ","
		}
		props += `"` + name + `":{"type":"boolean"}`
		required += `"` + name + `"`
	}
	return `{"type":"object","properties":{"rights":{"type":"object","properties":{` + props + `},"required":[` +
		required + `],"additionalProperties":false}},"required":["rights"],"additionalProperties":false}`
}

func defaultRightsExample() string {
	out := `{"for_channels":false,"rights":{`
	for i, name := range promoteRightNames {
		if i > 0 {
			out += ","
		}
		out += `"` + name + `":false`
	}
	return out + `}}`
}

var defaultAdminRightsGet = capability.Descriptor{
	ID:      Provider + ".defaultadminrights.get",
	Version: 1,
	Title:   "Get the Telegram bot default administrator rights",
	Description: "Read the administrator rights the bot requests by default when it is added as administrator to " +
		"groups or channels. Requires the bot target",
	Tags: []string{"telegram", "bot", "administrator", "rights", "read"},
	Risk: capability.Risk{
		Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataSensitivity,
	},
	Provider: Provider,
	Group:    groupBot,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + forChannelsSchema +
		`},"required":["for_channels"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(defaultRightsOutputSchema()),
	Arguments:    []capability.Argument{{Name: "for_channels", Required: true, Description: forChannelsDescription}},
	Fields: []capability.Field{{Name: "rights",
		Description: "Every administrator right as a boolean; a right Telegram does not report is false"}},
	Examples: []capability.Example{{Description: "Read the default rights for groups", Arguments: json.RawMessage(`{"for_channels":false}`)}},
}

var defaultAdminRightsSet = capability.Descriptor{
	ID:      Provider + ".defaultadminrights.set",
	Version: 1,
	Title:   "Set the Telegram bot default administrator rights",
	Description: "Replace the administrator rights the bot requests by default when it is added as administrator " +
		"to groups or channels; every right must be given explicitly. The rights are only suggested to the user " +
		"who adds the bot. Requires the bot target",
	Tags:     []string{"telegram", "bot", "administrator", "rights"},
	Risk:     botSettingRisk(capability.EffectUpdate),
	Provider: Provider, RequiresToolAllowList: true,
	Group: groupBot,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + forChannelsSchema + `,` + promoteRightsSchema() +
		`},"required":["for_channels","rights"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"}},` +
		`"required":["updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "for_channels", Required: true, Description: forChannelsDescription},
		{Name: "rights", Required: true, Description: "Object with every ChatAdministratorRights boolean of the Bot API, none omitted"},
	},
	Fields:   []capability.Field{{Name: "updated", Description: "True when Telegram accepted the rights"}},
	Examples: []capability.Example{{Description: "Request no rights for groups", Arguments: json.RawMessage(defaultRightsExample())}},
}

func invokeDefaultAdminRightsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get default administrator rights"
	var arguments struct {
		ForChannels *bool `json:"for_channels"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	open, err := openBot(ctx, resolved, secrets, red, op)
	if err != nil {
		return nil, err
	}
	if arguments.ForChannels == nil {
		return nil, providerError(op, "for_channels is required")
	}
	client, err := open()
	if err != nil {
		return nil, err
	}
	return client.GetDefaultAdminRights(ctx, *arguments.ForChannels)
}

func invokeDefaultAdminRightsSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set default administrator rights"
	var arguments struct {
		ForChannels *bool            `json:"for_channels"`
		Rights      map[string]*bool `json:"rights"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	open, err := openBot(ctx, resolved, secrets, red, op)
	if err != nil {
		return nil, err
	}
	if arguments.ForChannels == nil {
		return nil, providerError(op, "for_channels is required")
	}
	rights, ok := completeRights(arguments.Rights)
	if !ok {
		return nil, providerError(op, "rights must set every right explicitly")
	}
	client, err := open()
	if err != nil {
		return nil, err
	}
	return client.SetDefaultAdminRights(ctx, *arguments.ForChannels, rights)
}

// GetDefaultAdminRights reads getMyDefaultAdministratorRights; only the known booleans are decoded.
func (c *Client) GetDefaultAdminRights(ctx context.Context, forChannels bool) (map[string]any, error) {
	const op = "get default administrator rights"
	raw, err := c.call(ctx, spec{op: op, method: "getMyDefaultAdministratorRights", limit: defaultResponseBytes,
		readOnly: true}, struct {
		ForChannels bool `json:"for_channels"`
	}{forChannels})
	if err != nil {
		return nil, err
	}
	var in map[string]json.RawMessage
	if json.Unmarshal(raw, &in) != nil || in == nil {
		return nil, invalidResponse(op)
	}
	rights := make(map[string]bool, len(promoteRightNames))
	for _, name := range promoteRightNames {
		value := false
		if v, present := in[name]; present && json.Unmarshal(v, &value) != nil {
			return nil, invalidResponse(op)
		}
		rights[name] = value
	}
	return map[string]any{"rights": rights}, nil
}

// SetDefaultAdminRights performs exactly one setMyDefaultAdministratorRights request with every right explicit.
func (c *Client) SetDefaultAdminRights(ctx context.Context, forChannels bool, rights map[string]bool) (map[string]any, error) {
	const op = "set default administrator rights"
	if len(rights) != len(promoteRightNames) {
		return nil, providerError(op, "rights must set every right explicitly")
	}
	body := make(map[string]bool, len(promoteRightNames))
	for _, name := range promoteRightNames {
		v, ok := rights[name]
		if !ok {
			return nil, providerError(op, "rights must set every right explicitly")
		}
		body[name] = v
	}
	err := c.confirmed(ctx, spec{op: op, method: "setMyDefaultAdministratorRights", limit: defaultResponseBytes},
		struct {
			Rights      map[string]bool `json:"rights"`
			ForChannels bool            `json:"for_channels"`
		}{body, forChannels})
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": true}, nil
}
