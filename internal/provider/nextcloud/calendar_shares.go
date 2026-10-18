package nextcloud

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Calendar sharing: one oc:share POST to a bound calendar of the identity after the read-only owner pre-check
// of the calendar management tools. Nextcloud ignores an unknown user or group without an error, so the
// answer says that the change was accepted, not that the recipient exists; calendar.calendars.list shows the result.

const calendarShareOut = `"calendar":{"type":"string"},"type":{"type":"string"},"id":{"type":"string"},` +
	`"accepted":{"type":"boolean"}`

var calendarsShare = capability.Descriptor{
	ID: Provider + ".calendar.calendars.share", Version: 1, Title: "Share a Nextcloud calendar with a user or group",
	Description: "Share one bound calendar that belongs to the identity with one existing user or group of the " +
		"instance for reading or for reading and writing; calling it again changes the access. A calendar shared " +
		"by someone else is refused, and no e-mail, team, or remote recipient and no public link can be set. " +
		"Nextcloud ignores an unknown user or group without an error, so check calendar.calendars.list. One read-only " +
		"pre-check of the calendar follows, then one POST; the request is never repeated after an unclear result",
	Tags: []string{"nextcloud", "calendar", "share"}, Provider: Provider,
	Risk: eventWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` + davShareTypeSchema + `,` +
		davShareIDSchema + `,"access":{"type":"string","enum":["read","read-write"]}},` +
		`"required":["calendar","type","id","access"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarShareOut + `,` +
		`"access":{"type":"string"}},"required":["calendar","type","id","access","accepted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{calendarArgument, davShareTypeArgument, davShareIDArgument,
		{Name: "access", Required: true, Description: "read or read-write"}},
	Fields: calendarShareFields,
	Examples: []capability.Example{{Description: "Let a group read a calendar",
		Arguments: json.RawMessage(`{"calendar":"projects","type":"groups","id":"staff","access":"read"}`)}},
	// Reachable only through a tools list, never through a profile.
	RequiresToolAllowList: true,
}

var calendarsUnshare = capability.Descriptor{
	ID: Provider + ".calendar.calendars.unshare", Version: 1, Title: "Revoke the share of a Nextcloud calendar",
	Description: "Revoke the share of one bound calendar that belongs to the identity for one user or group. A " +
		"calendar shared by someone else is refused. One read-only pre-check of the calendar follows, then one " +
		"POST; the request is never repeated after an unclear result",
	Tags: []string{"nextcloud", "calendar", "share"}, Provider: Provider,
	Risk: eventWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` + davShareTypeSchema + `,` +
		davShareIDSchema + `},"required":["calendar","type","id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarShareOut + `},` +
		`"required":["calendar","type","id","accepted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{calendarArgument, davShareTypeArgument, davShareIDArgument},
	Fields:    calendarShareFields[:4],
	Examples: []capability.Example{{Description: "Revoke the share of a user",
		Arguments: json.RawMessage(`{"calendar":"projects","type":"users","id":"bob"}`)}},
	// Reachable only through a tools list, never through a profile.
	RequiresToolAllowList: true,
}

var calendarShareFields = []capability.Field{
	{Name: "calendar", Description: "Id of the calendar"},
	{Name: "type", Description: "users or groups"},
	{Name: "id", Description: "ID of the user or group"},
	{Name: "accepted", Description: "True when Nextcloud accepted the change"},
	{Name: "access", Description: "read or read-write"},
}

// CalendarShareResult is the result of the calendar share and unshare tools.
type CalendarShareResult struct {
	Calendar string `json:"calendar"`
	Type     string `json:"type"`
	ID       string `json:"id"`
	Access   string `json:"access,omitempty"`
	Accepted bool   `json:"accepted"`
}

type calendarShareArguments struct {
	Calendar string `json:"calendar"`
	Type     string `json:"type"`
	ID       string `json:"id"`
	Access   string `json:"access"`
}

func invokeCalendarsShare(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return changeCalendarShare(ctx, "share calendar", false, resolved, secrets, red, raw)
}

func invokeCalendarsUnshare(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return changeCalendarShare(ctx, "unshare calendar", true, resolved, secrets, red, raw)
}

// changeCalendarShare checks everything locally decidable before any credential access, then reads the
// owner of the calendar once and sends the one POST.
func changeCalendarShare(ctx context.Context, op string, remove bool, resolved *config.Resolved,
	secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input calendarShareArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	href, err := davSharePrincipal(op, input.Type, input.ID)
	if err != nil {
		return nil, err
	}
	body := davShareRemoveBody(href)
	if remove {
		if input.Access != "" {
			return nil, providerError(op, "revoking a share takes no access")
		}
	} else {
		if input.Access != davShareRead && input.Access != davShareReadWrite {
			return nil, providerError(op, "access must be read or read-write")
		}
		body = davShareSetBody(href, input.Access == davShareReadWrite)
	}
	client, err := openCalendar(ctx, op, resolved, secrets, red, input.Calendar)
	if err != nil {
		return nil, err
	}
	if err := client.ownCalendar(ctx, op, input.Calendar); err != nil {
		return nil, err
	}
	if err := client.sendDavShare(ctx, op, client.calendarHome(input.Calendar), body, calendarUncertain); err != nil {
		return nil, err
	}
	return &CalendarShareResult{Calendar: input.Calendar, Type: input.Type, ID: input.ID, Access: input.Access,
		Accepted: true}, nil
}
