package matomo

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const maxRecordingMinSessionTime = 999999999

var (
	recordingSentOnceList = sentOnceFor("recording", "matomo.session-recordings.list")
	recordingSentOnceGet  = sentOnceFor("recording", "matomo.session-recordings.get")
)

// recordingChangeRisk is the risk of every session recording change: confirmed, open to the instance, and about the
// recording configuration of a website. No change deletes anything.
func recordingChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: sessionRecordingsSensitive}
}

const (
	recordingSettingsSchema = `"name":{"type":"string","minLength":1,"maxLength":255},"matchPageRules":` + matchRuleSchema + `,` +
		`"sampleLimit":{"type":"integer","minimum":1,"maximum":999999999},` +
		`"sampleRate":{"type":"number","minimum":0.1,"maximum":100},` +
		`"minSessionTime":{"type":"integer","minimum":0,"maximum":999999999},` +
		`"requiresActivity":{"type":"boolean"},"captureKeystrokes":{"type":"boolean"},` +
		`"description":{"type":"string","maxLength":255},"autoRepeat":{"type":"boolean"}`
	recordingSettingsHint = "Optional settings that are not given are not sent, so Matomo applies its own defaults " +
		"(by its signature: no page rules, sampleLimit 1000, sampleRate 10, minSessionTime 0, requiresActivity and " +
		"captureKeystrokes on, autoRepeat off, description empty)"
	keystrokesHint = "Recording captures what visitors type into forms (Matomo's default is on; it leaves password " +
		"fields out)"
)

func recordingSettingArguments(replaces bool) []capability.Argument {
	given := "; Matomo's default when omitted"
	if replaces {
		given = "; reset to Matomo's default when omitted"
	}
	return []capability.Argument{
		{Name: "name", Required: true, Description: "Name of the session recording, 1 to 255 characters"},
		{Name: "matchPageRules", Description: "1 to 20 page rules that select the pages that are recorded; each " +
			"holds attribute and type (lowercase letters and underscores, values from matomo.heatmap-options.list) and " +
			"value, and optionally inverted" + given},
		{Name: "sampleLimit", Description: "Number of sessions after which recording ends, 1 to 999999999" + given},
		{Name: "sampleRate", Description: "Share of visits that are recorded in percent, 0.1 to 100 with at most one " +
			"decimal" + given},
		{Name: "minSessionTime", Description: "Minimum time in seconds a visit must last to be kept, 0 to 999999999" + given},
		{Name: "requiresActivity", Description: "True to record a visit only after the visitor was active" + given},
		{Name: "captureKeystrokes", Description: "True: " + keystrokesHint + ". False turns that off" + given},
		{Name: "description", Description: "Description of the recording, up to 255 characters; only the legacy " +
			"reference lists it, and a Matomo that does not know it may refuse it" + given},
		{Name: "autoRepeat", Description: "True to repeat the recording automatically; only the legacy reference " +
			"lists it, and a Matomo that does not know it may refuse it" + given}}
}

var sessionRecordingsAdd = capability.Descriptor{
	ID: Provider + ".session-recordings.add", Version: 1, Title: "Create a Matomo session recording",
	Description: "Create a session recording configuration on a website with a name and optional settings. " +
		recordingSettingsHint + ". captureKeystrokes: " + keystrokesHint + ". A repeated call creates a second " +
		"recording. Needs the premium plugin HeatmapSessionRecording and, expected, a write right on the website" +
		recordingSentOnceList,
	Tags: []string{"matomo", "session-recordings", "behavior", "create"}, Risk: recordingChangeRisk(capability.EffectCreate,
		capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + recordingSettingsSchema + `},` +
		`"required":["name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idSiteHsr":{"type":"integer"},"created":{"type":"boolean"}},"required":["idSite","idSiteHsr","created"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument()}, recordingSettingArguments(false)...),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the recording belongs to"},
		{Name: "idSiteHsr", Description: "ID of the new recording, used as idSiteHsr by the other recording tools"},
		{Name: "created", Description: "True once Matomo created the recording"}},
	Examples: []capability.Example{{Description: "Recording of the checkout pages of website 1 without typed text",
		Arguments: json.RawMessage(`{"idSite":1,"name":"Checkout","matchPageRules":[{"attribute":"path",` +
			`"type":"contains","value":"checkout"}],"sampleRate":10,"captureKeystrokes":false}`)}},
}

var sessionRecordingsUpdate = capability.Descriptor{
	ID: Provider + ".session-recordings.update", Version: 1, Title: "Replace the configuration of a Matomo session recording",
	Description: "Replace the configuration of a session recording of a website: name is required, and " +
		recordingSettingsHint + ", so a setting left out may reset a value the recording had; read the recording " +
		"with matomo.session-recordings.get and pass every setting to keep. captureKeystrokes: " + keystrokesHint +
		". Whether Matomo resets omitted settings is not verified. The recording is read first and must belong to " +
		"the website, otherwise it is not found. Needs the premium plugin HeatmapSessionRecording and, expected, a " +
		"write right on the website; idempotent in effect" + recordingSentOnceGet,
	Tags: []string{"matomo", "session-recordings", "behavior", "update"}, Risk: recordingChangeRisk(capability.EffectUpdate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idHeatmapSchema + `,` +
		recordingSettingsSchema + `},"required":["idSiteHsr","name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idSiteHsr":{"type":"integer"},"saved":{"type":"boolean"}},"required":["idSite","idSiteHsr","saved"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "idSiteHsr", Required: true, Description: "ID of the recording to replace, from matomo.session-recordings.list"}},
		recordingSettingArguments(true)...),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the recording belongs to"},
		{Name: "idSiteHsr", Description: "The recording that was replaced"},
		{Name: "saved", Description: "True once Matomo accepted the configuration"}},
	Examples: []capability.Example{{Description: "Rename recording 13 of website 1 and keep its settings",
		Arguments: json.RawMessage(`{"idSite":1,"idSiteHsr":13,"name":"Home","matchPageRules":[{"attribute":"url",` +
			`"type":"equals_simple","value":"https://example.org/"}],"sampleLimit":250,"sampleRate":100,` +
			`"captureKeystrokes":true}`)}},
}

// SessionRecordingCreated is the answer of session-recordings.add.
type SessionRecordingCreated struct {
	IDSite    int  `json:"idSite"`
	IDSiteHsr int  `json:"idSiteHsr"`
	Created   bool `json:"created"`
}

// SessionRecordingSaved is the answer of session-recordings.update.
type SessionRecordingSaved struct {
	IDSite    int  `json:"idSite"`
	IDSiteHsr int  `json:"idSiteHsr"`
	Saved     bool `json:"saved"`
}

// recordingSettings holds the settings of add and update. Pointers tell a setting that is not given, which is
// not sent, from one that is.
type recordingSettings struct {
	Name              string           `json:"name"`
	MatchPageRules    []matchRuleInput `json:"matchPageRules"`
	SampleLimit       *int             `json:"sampleLimit"`
	SampleRate        *float64         `json:"sampleRate"`
	MinSessionTime    *int             `json:"minSessionTime"`
	RequiresActivity  *bool            `json:"requiresActivity"`
	CaptureKeystrokes *bool            `json:"captureKeystrokes"`
	Description       *string          `json:"description"`
	AutoRepeat        *bool            `json:"autoRepeat"`
}

// params checks the settings the way the descriptor promises and adds only those that are given. Matomo stays the
// authority: it checks again, and its answer decides.
func (s recordingSettings) params(params url.Values) error {
	if strings.TrimSpace(s.Name) == "" || utf8.RuneCountInString(s.Name) > maxHeatmapName {
		return invalidArgument("name must hold 1 to 255 characters")
	}
	params.Set("name", s.Name)
	// The reference defaults the rules to none, so they are optional; a given list is checked like a heatmap's.
	if s.MatchPageRules != nil {
		if err := matchRuleParams(params, s.MatchPageRules); err != nil {
			return err
		}
	}
	if s.SampleLimit != nil {
		if *s.SampleLimit < 1 || *s.SampleLimit > maxHeatmapSampleLimit {
			return invalidArgument("sampleLimit must be between 1 and 999999999")
		}
		params.Set("sampleLimit", strconv.Itoa(*s.SampleLimit))
	}
	if s.SampleRate != nil {
		rate := *s.SampleRate
		if math.IsNaN(rate) || rate < 0.1 || rate > 100 || math.Abs(rate*10-math.Round(rate*10)) > 1e-9 {
			return invalidArgument("sampleRate must be between 0.1 and 100 with at most one decimal")
		}
		params.Set("sampleRate", strconv.FormatFloat(math.Round(rate*10)/10, 'f', -1, 64))
	}
	if s.MinSessionTime != nil {
		if *s.MinSessionTime < 0 || *s.MinSessionTime > maxRecordingMinSessionTime {
			return invalidArgument("minSessionTime must be between 0 and 999999999")
		}
		params.Set("minSessionTime", strconv.Itoa(*s.MinSessionTime))
	}
	if s.Description != nil {
		if utf8.RuneCountInString(*s.Description) > maxHeatmapDescription {
			return invalidArgument("description is longer than allowed")
		}
		params.Set("description", *s.Description)
	}
	for key, value := range map[string]*bool{"requiresActivity": s.RequiresActivity,
		"captureKeystrokes": s.CaptureKeystrokes, "autoRepeat": s.AutoRepeat} {
		if value != nil {
			params.Set(key, flagValue(value))
		}
	}
	return nil
}

func invokeSessionRecordingsAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add session recording"
	var args struct {
		IDSite *int `json:"idSite"`
		recordingSettings
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}}
	if err := args.params(params); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, heatmapsRequirement(sessionRecordingsAdd.ID),
		"HeatmapSessionRecording.addSessionRecording", params, recordingSentOnceList, &answer); err != nil {
		return nil, err
	}
	var id int
	if scalar(answer, &id) != nil || id < 1 {
		return nil, unclearChange(op, "Matomo returned no ID of the session recording", recordingSentOnceList)
	}
	return &SessionRecordingCreated{IDSite: site, IDSiteHsr: id, Created: true}, nil
}

func invokeSessionRecordingsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update session recording"
	var args struct {
		IDSite    *int `json:"idSite"`
		IDSiteHsr *int `json:"idSiteHsr"`
		recordingSettings
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkRecordingID(args.IDSiteHsr); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idSiteHsr": {strconv.Itoa(*args.IDSiteHsr)}}
	if err := args.params(params); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := heatmapsRequirement(sessionRecordingsUpdate.ID)
	// The recording ID counts across websites: one read must show it on this website before it is replaced.
	if _, err := client.fetchSessionRecording(ctx, op, req, site, *args.IDSiteHsr); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "HeatmapSessionRecording.updateSessionRecording", params,
		recordingSentOnceGet, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the session recording", recordingSentOnceGet)
	}
	return &SessionRecordingSaved{IDSite: site, IDSiteHsr: *args.IDSiteHsr, Saved: true}, nil
}

func sessionRecordingChangeOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: sessionRecordingsAdd, Handler: capability.Handler(invokeSessionRecordingsAdd)},
		{Descriptor: sessionRecordingsUpdate, Handler: capability.Handler(invokeSessionRecordingsUpdate)},
	}
}
