package matomo

import (
	"context"
	"encoding/json"
	"math"
	"net/url"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// Matomo stores the status in a SMALLINT, the byte count and the time in INT columns, and the source in a
	// VARCHAR(50). It reports success to the tracker even when the insert fails, so the limits are enforced here.
	maxBotStatus = math.MaxUint16
	maxBotNumber = math.MaxUint32
	maxBotSource = 50
	botSentOnce  = "; sent once and never repeated by Qatlas: a repeat is counted twice, so check the bot reports of " +
		"the website before repeating it"
	botSent = "sent to Matomo; this does not confirm that Matomo processed the request, because Matomo stores " +
		"only requests whose user agent matches its list of AI chatbots and answers every other request the same way"
)

// firstBotRecording is the first release the tool accepts for recording bot requests.
var firstBotRecording = version{5, 7, 0}

const trackingBotSchema = `"userAgent":{"type":"string","minLength":1,"maxLength":512},` +
	`"url":{"type":"string","minLength":1,"maxLength":2048},"download":{"type":"string","minLength":1,"maxLength":2048},` +
	`"httpStatus":{"type":"integer","minimum":0,"maximum":65535},` +
	`"bytes":{"type":"integer","minimum":0,"maximum":4294967295},"responseTime":{"type":"integer","minimum":0,"maximum":4294967295},` +
	`"source":{"type":"string","minLength":1,"maxLength":50},` +
	`"timestamp":{"type":"integer","minimum":946684800,"maximum":4102444800}`

var trackingRecordBotRequest = capability.Descriptor{
	ID: Provider + ".tracking.record-bot-request", Version: 1, Title: "Record a request of an AI chatbot in Matomo",
	Description: "Record one request that an AI chatbot made to a website and that the website's server saw, which " +
		"Matomo cannot see through JavaScript (matomo.php with recMode=1, ua, and url or download, optionally " +
		"http_status, bw_bytes, pf_srv, source, and cdt). Matomo stores it as a bot request, not as a visit, and " +
		"only when the user agent matches a pattern of Matomo's own list of AI chatbots, which changes with the " +
		"Matomo release (in 5.14 for example ChatGPT-User, Claude-User, Perplexity-User, MistralAI-User, " +
		"Gemini-Deep-Research, and Google-NotebookLM); any other user agent is accepted and not stored. Matomo " +
		"stores the website, the time, the chatbot name and type, and the given status, bytes, response time, " +
		"source, and address, but no visitor ID, IP address, or user ID. It needs Matomo 5.7.0 or newer with the " +
		"BotTracking plugin activated; the tool checks both through the reporting API of the instance with the " +
		"connection's token, sent as a bearer header, and sends no tracking request when either is missing. The " +
		"tracking request itself is one POST to matomo.php that never retries and carries no token unless " +
		"timestamp is set: then the token is sent as token_auth in the request body, because Matomo accepts a " +
		"time more than 24 hours back only with a token that has at least the Write role for the website " +
		"(otherwise HTTP 400), and it then invalidates the archived reports of that past day. Matomo ignores a " +
		"timestamp in the future or 20 years back. httpStatus is 0 to 65535, bytes and responseTime (in " +
		"milliseconds) are 0 to 4294967295, source is at most 50 characters, and userAgent and the address are " +
		"never fetched; the tool refuses a value outside these limits because Matomo drops a record whose " +
		"values do not fit and still reports success. Success means only that Matomo accepted the request " +
		"(HTTP 204 or an image); an unknown website is refused as HTTP 400 and switched-off tracking as 503. " +
		"Forged bot requests distort the bot reports and no API call can undo them. Not idempotent: a repeated " +
		"call is counted twice. The website must be inside the connection's targets" + botSentOnce,
	Tags: []string{"matomo", "tracking", "bots", "ai", "chatbots", "create"}, Risk: trackingRisk(), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + trackingBotSchema + `},` +
		`"required":["userAgent"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(trackingOutputSchema),
	Arguments: []capability.Argument{
		idSiteArgument(),
		{Name: "userAgent", Required: true, Description: "Full user agent string the chatbot sent (ua), up to 512 " +
			"characters; Matomo stores the request only when it matches its list of AI chatbots"},
		{Name: "url", Description: "Address of the page the chatbot requested (url), an http or https address up to " +
			"2048 characters; passed on as text and never fetched. Exactly one of url and download is required"},
		{Name: "download", Description: "Address of the file the chatbot requested (download), an http or https " +
			"address up to 2048 characters; passed on as text and never fetched. Exactly one of url and download is required"},
		{Name: "httpStatus", Description: "HTTP status code the server answered (http_status), 0 to 65535"},
		{Name: "bytes", Description: "Bytes sent to the chatbot (bw_bytes), 0 to 4294967295"},
		{Name: "responseTime", Description: "Milliseconds the server needed to answer (pf_srv), 0 to 4294967295"},
		{Name: "source", Description: "Label of the system that observed the request (source), such as Cloudflare, up to 50 characters"},
		{Name: "timestamp", Description: "Time of the request (cdt) as Unix time in seconds, not in the future and less " +
			"than 20 years back. Only with it the token is sent, in the request body; Matomo refuses a time more than " +
			"24 hours back without a token of at least the Write role, and invalidates the archives of a past day"},
	},
	Fields: trackingResultFields,
	Examples: []capability.Example{{Description: "Record a ChatGPT-User request for a page of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"userAgent":"Mozilla/5.0 (compatible; ChatGPT-User/1.0)",` +
			`"url":"https://example.org/docs/intro","httpStatus":200,"bytes":18324,"responseTime":142,"source":"nginx"}`)}},
}

// botBounds validates a number against the column Matomo stores it in and stores it.
func botBounds(form url.Values, key, argument string, given *int64, high int64) error {
	if given == nil {
		return nil
	}
	return trackingRange(form, key, argument, given, 0, high)
}

func invokeTrackingRecordBotRequest(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "record bot request"
	var args struct {
		IDSite       *int    `json:"idSite"`
		UserAgent    *string `json:"userAgent"`
		URL          *string `json:"url"`
		Download     *string `json:"download"`
		HTTPStatus   *int64  `json:"httpStatus"`
		Bytes        *int64  `json:"bytes"`
		ResponseTime *int64  `json:"responseTime"`
		Source       *string `json:"source"`
		Timestamp    *int64  `json:"timestamp"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if (args.URL == nil) == (args.Download == nil) {
		return nil, invalidArgument("exactly one of url and download is required")
	}
	form := url.Values{"recMode": {"1"}}
	if err := trackingRequired(form, "ua", "userAgent", args.UserAgent, maxTrackingText); err != nil {
		return nil, err
	}
	for _, step := range []error{
		trackingAddress(form, "url", "url", args.URL, false),
		trackingAddress(form, "download", "download", args.Download, false),
		botBounds(form, "http_status", "httpStatus", args.HTTPStatus, maxBotStatus),
		botBounds(form, "bw_bytes", "bytes", args.Bytes, maxBotNumber),
		botBounds(form, "pf_srv", "responseTime", args.ResponseTime, maxBotNumber),
		trackingText(form, "source", "source", args.Source, maxBotSource),
	} {
		if step != nil {
			return nil, step
		}
	}
	stamp := url.Values{}
	if err := trackingTimestamp(stamp, args.Timestamp); err != nil {
		return nil, err
	}
	sender, err := openTracking(resolved)
	if err != nil {
		return nil, err
	}
	// The instance check reads the token for the reporting API; the same token then serves the body of a
	// request with a timestamp, so it is resolved and registered for redaction once.
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if _, err := client.ensure(ctx, op, requirement{tool: trackingRecordBotRequest.ID, plugin: botPlugin,
		minVersion: firstBotRecording}); err != nil {
		return nil, err
	}
	var auth *trackingAuth
	if len(stamp) > 0 {
		auth = &trackingAuth{token: client.token, overrides: stamp}
	}
	if err := uncertainChange(sender.send(ctx, op, site, form, auth), botSentOnce); err != nil {
		return nil, err
	}
	return &TrackingSent{IDSite: site, Sent: true, Message: botSent}, nil
}
