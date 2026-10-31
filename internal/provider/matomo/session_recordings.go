package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// sessionRecordingsSensitive covers the names and page rules people wrote into recording configurations.
	sessionRecordingsSensitive = "matomo-session-recordings"
	// recordedSessionsSensitive covers recorded visits: pages, location, device, visitor ID, and behavior.
	recordedSessionsSensitive = "matomo-session-recordings-personal-data"
	maxRecordedID             = 9007199254740991
)

const (
	idRecordedSessionSchema = `"idLogHsr":{"type":"integer","minimum":1,"maximum":9007199254740991}`
	recordingProps          = `"idSiteHsr":{"type":"integer"},"idSite":{"type":"integer"},"name":{"type":"string"},` +
		`"sample_rate":{"type":"string"},"sample_limit":{"type":"integer"},"match_page_rules":{"type":"array",` +
		`"items":{"type":"object","properties":{"attribute":{"type":"string"},"type":{"type":"string"},` +
		`"value":{"type":"string"},"inverted":{"type":"boolean"}},"additionalProperties":false}},` +
		`"min_session_time":{"type":"integer"},"requires_activity":{"type":"boolean"},` +
		`"capture_keystrokes":{"type":"boolean"},"created_date":{"type":"string"},"updated_date":{"type":"string"},` +
		`"status":{"type":"string"},"capture_manually":{"type":"boolean"},"created_date_pretty":{"type":"string"}`
	recordedSessionProps = `"label":{"type":"string"},"nb_pageviews":{"type":"integer"},"idVisit":{"type":"integer"},` +
		`"first_url":{"type":"string"},"last_url":{"type":"string"},"time_on_site":{"type":"string"},` +
		`"total_events":{"type":"integer"},"idLogHsr":{"type":"integer"},"idVisitor":{"type":"string"},` +
		`"location_country":{"type":"string"},"location_region":{"type":"string"},"location_city":{"type":"string"},` +
		`"config_os":{"type":"string"},"config_device_type":{"type":"integer"},"config_device_model":{"type":"string"},` +
		`"config_browser_name":{"type":"string"},"server_time":{"type":"string"},"sessionReplayUrl":{"type":"string"}`
)

var periodDateSchema = `"period":{"type":"string","enum":["day","week","month","year","range"]},` +
	`"date":{"type":"string","minLength":1,"maxLength":32},` +
	`"segment":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxSegmentLength) + `}`

func sessionRecordingFields(prefix string) []capability.Field {
	return []capability.Field{
		{Name: prefix + "idSiteHsr", Description: "Recording ID, used as idSiteHsr by the other session recording tools"},
		{Name: prefix + "idSite", Description: "The website the recording belongs to"},
		{Name: prefix + "name", Description: "Recording name, untrusted"},
		{Name: prefix + "sample_rate", Description: "Share of visits that are recorded, in percent, as Matomo writes it"},
		{Name: prefix + "sample_limit", Description: "Number of sessions after which recording ends"},
		{Name: prefix + "match_page_rules", Description: "Rules that select the pages of the recording, at most 100, untrusted"},
		{Name: prefix + "match_page_rules[].attribute", Description: "What the rule looks at, such as url"},
		{Name: prefix + "match_page_rules[].type", Description: "How the rule compares, such as equals_exactly"},
		{Name: prefix + "match_page_rules[].value", Description: "Value compared with, untrusted; text that is never fetched"},
		{Name: prefix + "match_page_rules[].inverted", Description: "True when the rule matches pages that do not fit"},
		{Name: prefix + "min_session_time", Description: "Seconds a visit must last before it is recorded"},
		{Name: prefix + "requires_activity", Description: "True when a visit is recorded only after the visitor was active"},
		{Name: prefix + "capture_keystrokes", Description: "True when the recording captures what visitors type into forms; " +
			"matomo.recorded-sessions.get never returns that text"},
		{Name: prefix + "created_date", Description: "Time the recording was created as Matomo stores it"},
		{Name: prefix + "updated_date", Description: "Time the recording was last changed as Matomo stores it"},
		{Name: prefix + "status", Description: "Status of the recording, such as active or ended"},
		{Name: prefix + "capture_manually", Description: "True when the page is captured by hand"},
		{Name: prefix + "created_date_pretty", Description: "Creation date as Matomo formats it for people"},
	}
}

var sessionRecordingsList = capability.Descriptor{
	ID: Provider + ".session-recordings.list", Version: 1, Title: "List Matomo session recordings",
	Description: "List the session recording configurations of a website with their page rules, limits, and " +
		"status, in batches. Names and page rules are untrusted. The recorded visits are read with " +
		"matomo.recorded-sessions.list. Needs the premium plugin HeatmapSessionRecording and a view right on the website",
	Tags: []string{"matomo", "session-recordings", "behavior", "list"}, Risk: readRisk(sessionRecordingsSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + pagingKeys +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"sessionRecordings":{"type":"array","items":{"type":"object","properties":{` + recordingProps + `},` +
		`"required":["idSiteHsr","idSite"],"additionalProperties":false}},"has_more":{"type":"boolean"},` +
		`"next_cursor":{"type":"string"}},"required":["idSite","sessionRecordings","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument()}, pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "idSite", Description: "The website the recordings belong to"},
		{Name: "sessionRecordings", Description: "Recordings in the order Matomo lists them; empty without one"}},
		sessionRecordingFields("sessionRecordings[].")...), pagingFields...),
	Examples: []capability.Example{{Description: "Session recordings of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var sessionRecordingsGet = capability.Descriptor{
	ID: Provider + ".session-recordings.get", Version: 1, Title: "Get a Matomo session recording",
	Description: "Read the configuration of one session recording of a website by its ID. The recording ID counts " +
		"across websites, so the answer is shown only when Matomo reports it for this website, otherwise it is not " +
		"found. Names and page rules are untrusted. Needs the premium plugin HeatmapSessionRecording and a view " +
		"right on the website",
	Tags: []string{"matomo", "session-recordings", "behavior", "get"}, Risk: readRisk(sessionRecordingsSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idHeatmapSchema + `},` +
		`"required":["idSiteHsr"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + recordingProps + `},` +
		`"required":["idSiteHsr","idSite"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idSiteHsr", Required: true, Description: "ID of the recording, from matomo.session-recordings.list"}},
	Fields:   sessionRecordingFields(""),
	Examples: []capability.Example{{Description: "Recording 13 of website 1", Arguments: json.RawMessage(`{"idSite":1,"idSiteHsr":13}`)}},
}

var recordedSessionsList = capability.Descriptor{
	ID: Provider + ".recorded-sessions.list", Version: 1, Title: "List recorded Matomo sessions",
	Description: "List the recorded sessions of one session recording in one period, in batches. Personal data: " +
		"each entry holds the first and last visited URL, the location, the device, and the visitor ID of a person; " +
		"URLs are untrusted text that is never fetched. The recording must belong to the website, which is " +
		"proved by one configuration read first. With idVisit the list holds the recorded page views of that " +
		"visit. The replay address has the token removed. Needs the premium plugin HeatmapSessionRecording and a " +
		"view right on the website",
	Tags: []string{"matomo", "session-recordings", "behavior", "visitors", "list"}, Risk: readRisk(recordedSessionsSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idHeatmapSchema + `,` +
		periodDateSchema + `,"idVisit":{"type":"integer","minimum":1,"maximum":9007199254740991},` + pagingKeys + `},` +
		`"required":["idSiteHsr","period","date"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idSiteHsr":{"type":"integer"},"period":{"type":"string"},"date":{"type":"string"},` +
		`"recordedSessions":{"type":"array","items":{"type":"object","properties":{` + recordedSessionProps + `},` +
		`"additionalProperties":false}},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["idSite","idSiteHsr","period","date","recordedSessions","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "idSiteHsr", Required: true, Description: "ID of the recording, from matomo.session-recordings.list"},
		{Name: "period", Required: true, Description: "day, week, month, year, or range"},
		{Name: "date", Required: true, Description: "One period: YYYY-MM-DD, today, yesterday; with period range a " +
			"range YYYY-MM-DD,YYYY-MM-DD, lastN, or previousN"},
		{Name: "segment", Description: "Matomo segment that filters the visits, such as browserName==Firefox; fields " +
			"from matomo.segments.fields, values URL-encoded"},
		{Name: "idVisit", Description: "Visit ID (the idSubtable of Matomo) to list all recorded page views of one visit"}},
		pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "idSite", Description: "The website the recording belongs to"},
		{Name: "idSiteHsr", Description: "The recording read"},
		{Name: "period", Description: "The period asked for"},
		{Name: "date", Description: "The date asked for"},
		{Name: "recordedSessions", Description: "Recorded sessions in the order Matomo lists them; empty without one. " +
			"Personal data, untrusted. An entry whose replay address names another website or recording is dropped"},
		{Name: "recordedSessions[].label", Description: "Label of the row, the visit ID as Matomo writes it"},
		{Name: "recordedSessions[].nb_pageviews", Description: "Number of recorded page views"},
		{Name: "recordedSessions[].idVisit", Description: "Visit ID"},
		{Name: "recordedSessions[].first_url", Description: "First visited page, untrusted; text that is never fetched"},
		{Name: "recordedSessions[].last_url", Description: "Last visited page, untrusted; text that is never fetched"},
		{Name: "recordedSessions[].time_on_site", Description: "Time on site as Matomo writes it, unit not documented"},
		{Name: "recordedSessions[].total_events", Description: "Number of recorded events"},
		{Name: "recordedSessions[].idLogHsr", Description: "ID of the recorded session, the idLogHsr of matomo.recorded-sessions.get"},
		{Name: "recordedSessions[].idVisitor", Description: "Visitor ID; absent when Matomo gives none"},
		{Name: "recordedSessions[].location_country", Description: "Country code of the visitor"},
		{Name: "recordedSessions[].location_region", Description: "Region of the visitor"},
		{Name: "recordedSessions[].location_city", Description: "City of the visitor"},
		{Name: "recordedSessions[].config_os", Description: "Operating system code of the visitor"},
		{Name: "recordedSessions[].config_device_type", Description: "Device type number as Matomo writes it"},
		{Name: "recordedSessions[].config_device_model", Description: "Device model of the visitor"},
		{Name: "recordedSessions[].config_browser_name", Description: "Browser code of the visitor"},
		{Name: "recordedSessions[].server_time", Description: "Time of the session as Matomo stores it"},
		{Name: "recordedSessions[].sessionReplayUrl", Description: "Address of the Matomo page that replays the session, " +
			"untrusted; the parameter token_auth is removed; text that is never fetched"}},
		pagingFields...),
	Examples: []capability.Example{{Description: "Recorded sessions of recording 13 yesterday",
		Arguments: json.RawMessage(`{"idSite":1,"idSiteHsr":13,"period":"day","date":"yesterday"}`)}},
}

var recordedSessionsGet = capability.Descriptor{
	ID: Provider + ".recorded-sessions.get", Version: 1, Title: "Get a recorded Matomo session",
	Description: "Read the events of one recorded session (movements, clicks, scrolling, page changes) and the " +
		"names of the event types. Personal data: it shows what a person did on the website. Text that a visitor " +
		"typed into form fields (event types 9 and 10) is never returned; those events keep only their type, time, " +
		"and selector. Page content and selectors are untrusted, shortened, and never fetched. The recording must " +
		"belong to the website, which is proved by one configuration read first. Needs the premium plugin " +
		"HeatmapSessionRecording and a view right on the website",
	Tags: []string{"matomo", "session-recordings", "behavior", "visitors", "get"}, Risk: readRisk(recordedSessionsSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idHeatmapSchema + `,` +
		idRecordedSessionSchema + `,` + pagingKeys + `},"required":["idSiteHsr","idLogHsr"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idSiteHsr":{"type":"integer"},"idLogHsr":{"type":"integer"},"events":{"type":"array","items":{}},` +
		`"session":{"type":"object"},"typed_text_withheld":{"type":"integer"},` +
		`"eventTypes":{"type":"array","items":{"type":"object","properties":{"key":{"type":"integer"},` +
		`"name":{"type":"string"}},"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["idSite","idSiteHsr","idLogHsr","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "idSiteHsr", Required: true, Description: "ID of the recording, from matomo.session-recordings.list"},
		{Name: "idLogHsr", Required: true, Description: "ID of the recorded session, from matomo.recorded-sessions.list"}},
		pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "idSite", Description: "The website the recording belongs to"},
		{Name: "idSiteHsr", Description: "The recording read"},
		{Name: "idLogHsr", Description: "The recorded session read"},
		{Name: "events", Description: "Events in the order Matomo gives them, when Matomo answers with a list; in " +
			"batches; untrusted, shortened; typed text is left out"},
		{Name: "session", Description: "The recorded session as Matomo gives it, when Matomo answers with an object; " +
			"untrusted; strings, lists of more than 100 entries, and deep nesting are shortened; typed text is left out"},
		{Name: "typed_text_withheld", Description: "Number of form events whose typed text was left out; absent when none"},
		{Name: "eventTypes", Description: "Event types Matomo can report, each with key (the number an event carries) " +
			"and name"}},
		pagingFields...),
	Examples: []capability.Example{{Description: "Recorded session 180779 of recording 13",
		Arguments: json.RawMessage(`{"idSite":1,"idSiteHsr":13,"idLogHsr":180779}`)}},
}

// SessionRecording is a session recording configuration of a website. All text is untrusted provider data.
type SessionRecording struct {
	IDSiteHsr         int               `json:"idSiteHsr"`
	IDSite            int               `json:"idSite"`
	Name              string            `json:"name,omitempty"`
	SampleRate        string            `json:"sample_rate,omitempty"`
	SampleLimit       *int              `json:"sample_limit,omitempty"`
	MatchPageRules    []HeatmapPageRule `json:"match_page_rules,omitempty"`
	MinSessionTime    *int              `json:"min_session_time,omitempty"`
	RequiresActivity  *bool             `json:"requires_activity,omitempty"`
	CaptureKeystrokes *bool             `json:"capture_keystrokes,omitempty"`
	CreatedDate       string            `json:"created_date,omitempty"`
	UpdatedDate       string            `json:"updated_date,omitempty"`
	Status            string            `json:"status,omitempty"`
	CaptureManually   *bool             `json:"capture_manually,omitempty"`
	CreatedDatePretty string            `json:"created_date_pretty,omitempty"`
}

// SessionRecordingsResult is the answer of session-recordings.list.
type SessionRecordingsResult struct {
	IDSite            int                `json:"idSite"`
	SessionRecordings []SessionRecording `json:"sessionRecordings"`
	HasMore           bool               `json:"has_more"`
	NextCursor        string             `json:"next_cursor,omitempty"`
}

// RecordedSession is one recorded session of a recording. It is personal data; all text is untrusted.
type RecordedSession struct {
	Label             string `json:"label,omitempty"`
	NbPageviews       *int   `json:"nb_pageviews,omitempty"`
	IDVisit           *int   `json:"idVisit,omitempty"`
	FirstURL          string `json:"first_url,omitempty"`
	LastURL           string `json:"last_url,omitempty"`
	TimeOnSite        string `json:"time_on_site,omitempty"`
	TotalEvents       *int   `json:"total_events,omitempty"`
	IDLogHsr          *int   `json:"idLogHsr,omitempty"`
	IDVisitor         string `json:"idVisitor,omitempty"`
	LocationCountry   string `json:"location_country,omitempty"`
	LocationRegion    string `json:"location_region,omitempty"`
	LocationCity      string `json:"location_city,omitempty"`
	ConfigOS          string `json:"config_os,omitempty"`
	ConfigDeviceType  *int   `json:"config_device_type,omitempty"`
	ConfigDeviceModel string `json:"config_device_model,omitempty"`
	ConfigBrowserName string `json:"config_browser_name,omitempty"`
	ServerTime        string `json:"server_time,omitempty"`
	SessionReplayURL  string `json:"sessionReplayUrl,omitempty"`
}

// RecordedSessionsResult is the answer of recorded-sessions.list.
type RecordedSessionsResult struct {
	IDSite           int               `json:"idSite"`
	IDSiteHsr        int               `json:"idSiteHsr"`
	Period           string            `json:"period"`
	Date             string            `json:"date"`
	RecordedSessions []RecordedSession `json:"recordedSessions"`
	HasMore          bool              `json:"has_more"`
	NextCursor       string            `json:"next_cursor,omitempty"`
}

// EventType is an event type of a recorded session.
type EventType struct {
	Key  *int   `json:"key,omitempty"`
	Name string `json:"name,omitempty"`
}

// RecordedSessionResult is the answer of recorded-sessions.get.
type RecordedSessionResult struct {
	IDSite            int         `json:"idSite"`
	IDSiteHsr         int         `json:"idSiteHsr"`
	IDLogHsr          int         `json:"idLogHsr"`
	Events            []any       `json:"events,omitempty"`
	Session           any         `json:"session,omitempty"`
	TypedTextWithheld int         `json:"typed_text_withheld,omitempty"`
	EventTypes        []EventType `json:"eventTypes,omitempty"`
	HasMore           bool        `json:"has_more"`
	NextCursor        string      `json:"next_cursor,omitempty"`
}

// sessionRecordingJSON is a recording as HeatmapSessionRecording reports it. Members are read loosely.
type sessionRecordingJSON struct {
	ID                *loose          `json:"idsitehsr"`
	IDSite            *loose          `json:"idsite"`
	Name              *loose          `json:"name"`
	SampleRate        *loose          `json:"sample_rate"`
	SampleLimit       *loose          `json:"sample_limit"`
	MatchPageRules    json.RawMessage `json:"match_page_rules"`
	MinSessionTime    *loose          `json:"min_session_time"`
	RequiresActivity  *loose          `json:"requires_activity"`
	CaptureKeystrokes *loose          `json:"capture_keystrokes"`
	CreatedDate       *loose          `json:"created_date"`
	UpdatedDate       *loose          `json:"updated_date"`
	Status            *loose          `json:"status"`
	CaptureManually   *loose          `json:"capture_manually"`
	CreatedDatePretty *loose          `json:"created_date_pretty"`
}

// recording converts a row. found is false for a row of another website; ok is false for a row without a
// usable recording ID or without the website that proves where it belongs.
func (r sessionRecordingJSON) recording(site int) (recording SessionRecording, found, ok bool) {
	id, row := r.ID.number(), r.IDSite.number()
	if id == nil || *id < 1 || row == nil {
		return SessionRecording{}, false, false
	}
	if *row != site {
		return SessionRecording{}, false, true
	}
	var rules []HeatmapPageRule
	if rows, valid := readRows[heatmapRuleJSON](r.MatchPageRules); valid {
		for i := range rows {
			if len(rules) < maxHeatmapRules {
				rules = append(rules, rows[i].rule())
			}
		}
	}
	return SessionRecording{IDSiteHsr: *id, IDSite: site, Name: r.Name.str(maxStringLength),
		SampleRate: r.SampleRate.str(maxStringLength), SampleLimit: r.SampleLimit.number(), MatchPageRules: rules,
		MinSessionTime: r.MinSessionTime.number(), RequiresActivity: r.RequiresActivity.flag(),
		CaptureKeystrokes: r.CaptureKeystrokes.flag(), CreatedDate: r.CreatedDate.str(maxStringLength),
		UpdatedDate: r.UpdatedDate.str(maxStringLength), Status: r.Status.str(maxStringLength),
		CaptureManually: r.CaptureManually.flag(), CreatedDatePretty: r.CreatedDatePretty.str(maxStringLength)}, true, true
}

func recordingNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op,
		Message: "Matomo has no such session recording for this website inside the targets of this connection"}
}

// fetchSessionRecording is the one read that both proves a recording belongs to the website and delivers it:
// the answer carries idsite, and a recording of another website, or one without it, is never shown. A recording
// ID is global in Matomo, so the ID alone proves nothing.
func (c *Client) fetchSessionRecording(ctx context.Context, op string, req requirement, site, id int) (SessionRecording, error) {
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idSiteHsr": {strconv.Itoa(id)}}
	var answer json.RawMessage
	if err := c.call(ctx, op, req, "HeatmapSessionRecording.getSessionRecording", params, &answer); err != nil {
		return SessionRecording{}, err
	}
	trimmed := bytes.TrimSpace(answer)
	switch string(trimmed) {
	case "null", "false", "[]", "{}", "":
		return SessionRecording{}, recordingNotFound(op)
	}
	var row sessionRecordingJSON
	if !bytes.HasPrefix(trimmed, []byte("{")) || json.Unmarshal(trimmed, &row) != nil {
		return SessionRecording{}, provider.InvalidResponse(op, "Matomo returned an invalid session recording")
	}
	recording, belongs, ok := row.recording(site)
	if !ok {
		return SessionRecording{}, provider.InvalidResponse(op, "Matomo returned an invalid session recording")
	}
	if !belongs {
		return SessionRecording{}, recordingNotFound(op)
	}
	if recording.IDSiteHsr != id {
		return SessionRecording{}, provider.InvalidResponse(op, "Matomo returned another session recording than asked for")
	}
	return recording, nil
}

func checkRecordingID(id *int) error {
	if id == nil || *id < 1 || *id > maxHeatmapID {
		return invalidArgument("idSiteHsr must be a positive integer recording ID from matomo.session-recordings.list")
	}
	return nil
}

func checkRecordedID(name string, id *int, source string) error {
	if id == nil || *id < 1 || *id > maxRecordedID {
		return invalidArgument(name + " must be a positive integer from " + source)
	}
	return nil
}

func invokeSessionRecordingsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list session recordings"
	var args struct {
		IDSite *int   `json:"idSite"`
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	paging, err := startPage(provider.CursorBinding(sessionRecordingsList.ID, scopeBinding(resolved), site), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, heatmapsRequirement(sessionRecordingsList.ID), "HeatmapSessionRecording.getSessionRecordings",
		url.Values{"idSite": {strconv.Itoa(site)}}, &answer); err != nil {
		return nil, err
	}
	rows, valid := readRows[sessionRecordingJSON](answer)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid session recording list")
	}
	recordings := []SessionRecording{}
	for i := range rows {
		if recording, belongs, usable := rows[i].recording(site); usable && belongs {
			recordings = append(recordings, recording)
		}
	}
	batch, next := slice(paging, recordings)
	return &SessionRecordingsResult{IDSite: site, SessionRecordings: batch, HasMore: next != "", NextCursor: next}, nil
}

func invokeSessionRecordingsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get session recording"
	var args struct {
		IDSite    *int `json:"idSite"`
		IDSiteHsr *int `json:"idSiteHsr"`
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
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	recording, err := client.fetchSessionRecording(ctx, op, heatmapsRequirement(sessionRecordingsGet.ID), site, *args.IDSiteHsr)
	if err != nil {
		return nil, err
	}
	return &recording, nil
}

type recordedSessionJSON struct {
	Label             *loose `json:"label"`
	NbPageviews       *loose `json:"nb_pageviews"`
	IDVisit           *loose `json:"idvisit"`
	FirstURL          *loose `json:"first_url"`
	LastURL           *loose `json:"last_url"`
	TimeOnSite        *loose `json:"time_on_site"`
	TotalEvents       *loose `json:"total_events"`
	IDLogHsr          *loose `json:"idloghsr"`
	IDVisitor         *loose `json:"idvisitor"`
	LocationCountry   *loose `json:"location_country"`
	LocationRegion    *loose `json:"location_region"`
	LocationCity      *loose `json:"location_city"`
	ConfigOS          *loose `json:"config_os"`
	ConfigDeviceType  *loose `json:"config_device_type"`
	ConfigDeviceModel *loose `json:"config_device_model"`
	ConfigBrowserName *loose `json:"config_browser_name"`
	ServerTime        *loose `json:"server_time"`
	SessionReplayURL  *loose `json:"sessionReplayUrl"`
}

// replayBelongs checks the website and recording a replay address names. An address that names neither, or
// cannot be parsed, proves nothing against the row and does not drop it: the proof is the read of the recording.
func replayBelongs(address string, site, recording int) bool {
	parsed, err := url.Parse(address)
	if err != nil {
		return true
	}
	query := parsed.Query()
	for name, want := range map[string]int{"idSite": site, "idSiteHsr": recording} {
		for _, got := range query[name] {
			if got != strconv.Itoa(want) {
				return false
			}
		}
	}
	return true
}

// visitorID drops the placeholders Matomo writes for a visit without a visitor ID.
func visitorID(l *loose) string {
	switch id := l.str(maxStringLength); id {
	case "0", "false":
		return ""
	default:
		return id
	}
}

// session converts a row; ok is false for a row whose replay address names another website or recording.
func (r recordedSessionJSON) session(site, recording int, token string) (RecordedSession, bool) {
	address := r.SessionReplayURL.str(maxUrlLength * 2)
	if address != "" && !replayBelongs(address, site, recording) {
		return RecordedSession{}, false
	}
	// The visited pages come from the tracked site, which may have put a token in its own addresses.
	page := func(l *loose) string {
		text := l.str(maxUrlLength * 2)
		// An address without a token stays as Matomo wrote it instead of being normalized by the parser.
		if strings.Contains(strings.ToLower(text), "token") || token != "" && strings.Contains(text, token) {
			text = withoutToken(text, token)
		}
		return boundString(text, maxUrlLength)
	}
	replay := ""
	if address != "" {
		replay = boundString(withoutToken(address, token), maxUrlLength)
	}
	return RecordedSession{Label: r.Label.str(maxStringLength), NbPageviews: r.NbPageviews.number(),
		IDVisit: r.IDVisit.number(), FirstURL: page(r.FirstURL), LastURL: page(r.LastURL),
		TimeOnSite: r.TimeOnSite.str(maxStringLength), TotalEvents: r.TotalEvents.number(), IDLogHsr: r.IDLogHsr.number(),
		IDVisitor: visitorID(r.IDVisitor), LocationCountry: r.LocationCountry.str(maxStringLength),
		LocationRegion: r.LocationRegion.str(maxStringLength), LocationCity: r.LocationCity.str(maxStringLength),
		ConfigOS: r.ConfigOS.str(maxStringLength), ConfigDeviceType: r.ConfigDeviceType.number(),
		ConfigDeviceModel: r.ConfigDeviceModel.str(maxStringLength), ConfigBrowserName: r.ConfigBrowserName.str(maxStringLength),
		ServerTime: r.ServerTime.str(maxStringLength), SessionReplayURL: replay}, true
}

func invokeRecordedSessionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list recorded sessions"
	var args struct {
		IDSite    *int   `json:"idSite"`
		IDSiteHsr *int   `json:"idSiteHsr"`
		Period    string `json:"period"`
		Date      string `json:"date"`
		Segment   string `json:"segment"`
		IDVisit   *int   `json:"idVisit"`
		Limit     int    `json:"limit"`
		Cursor    string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkRecordingID(args.IDSiteHsr); err != nil {
		return nil, err
	}
	visit := 0
	if args.IDVisit != nil {
		if err := checkRecordedID("idVisit", args.IDVisit, "matomo.recorded-sessions.list"); err != nil {
			return nil, err
		}
		visit = *args.IDVisit
	}
	if err := singlePeriod(args.Period, args.Date); err != nil {
		return nil, err
	}
	if args.Segment != "" && !validSegmentExpression(args.Segment) {
		return nil, invalidArgument("segment must be terms of field, operator, and URL-encoded value " +
			"joined by ; or , (fields: matomo.segments.fields)")
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	paging, err := startPage(provider.CursorBinding(recordedSessionsList.ID, scopeBinding(resolved), site, *args.IDSiteHsr,
		args.Period, args.Date, args.Segment, visit), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := heatmapsRequirement(recordedSessionsList.ID)
	// The recording ID counts across websites: this read binds it to the website before any session is read.
	if _, err := client.fetchSessionRecording(ctx, op, req, site, *args.IDSiteHsr); err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idSiteHsr": {strconv.Itoa(*args.IDSiteHsr)},
		"period": {args.Period}, "date": {args.Date}}
	if args.Segment != "" {
		params.Set("segment", args.Segment)
	}
	if visit != 0 {
		params.Set("idSubtable", strconv.Itoa(visit))
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, req, "HeatmapSessionRecording.getRecordedSessions", params, &answer); err != nil {
		return nil, err
	}
	rows, valid := readRows[recordedSessionJSON](answer)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid recorded session list")
	}
	sessions := []RecordedSession{}
	for i := range rows {
		if one, belongs := rows[i].session(site, *args.IDSiteHsr, client.token); belongs {
			sessions = append(sessions, one)
		}
	}
	batch, next := slice(paging, sessions)
	return &RecordedSessionsResult{IDSite: site, IDSiteHsr: *args.IDSiteHsr, Period: args.Period, Date: args.Date,
		RecordedSessions: batch, HasMore: next != "", NextCursor: next}, nil
}

// typedTextKeys are the members an event of form input keeps; everything else of such an event, which is where
// Matomo puts what the visitor typed or chose, is dropped.
var typedTextKeys = map[string]bool{"event_type": true, "eventtype": true, "time_since_load": true,
	"selector": true, "idloghsr": true}

// withholdTypedText removes what visitors typed from a recorded session: events of the types Form Text Change
// (9) and Form Value Change (10) keep only their type, time, and selector. An event object whose type cannot be
// read as a number loses its text and value members instead, because the form of the events is not documented.
// The count tells how many events were cut.
func withholdTypedText(value any, count *int) any {
	switch typed := value.(type) {
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = withholdTypedText(item, count)
		}
		return out
	case map[string]any:
		kind, event := "", false
		for key, member := range typed {
			switch strings.ToLower(key) {
			case "event_type", "eventtype":
				if text := jsonScalar(member); text != "" {
					if _, err := strconv.Atoi(text); err == nil {
						kind = text
					}
				}
			case "time_since_load", "selector", "idloghsr":
				event = true
			}
		}
		switch {
		case kind == "9" || kind == "10":
			kept := map[string]any{}
			for name, member := range typed {
				if typedTextKeys[strings.ToLower(name)] {
					kept[name] = member
				}
			}
			*count++
			return kept
		case kind == "" && event:
			kept := make(map[string]any, len(typed))
			cut := false
			for name, member := range typed {
				if lower := strings.ToLower(name); lower == "text" || lower == "value" {
					cut = true
					continue
				}
				kept[name] = withholdTypedText(member, count)
			}
			if cut {
				*count++
			}
			return kept
		}
		out := make(map[string]any, len(typed))
		for key, member := range typed {
			out[key] = withholdTypedText(member, count)
		}
		return out
	}
	return value
}

func jsonScalar(value any) string {
	switch typed := value.(type) {
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case string:
		return strings.TrimSpace(typed)
	}
	return ""
}

type eventTypeJSON struct {
	Key  *loose `json:"key"`
	Name *loose `json:"name"`
}

// sessionNamesOther reports whether an object answer names another website or recording than the one read.
func sessionNamesOther(object map[string]any, site, recording, logID int) bool {
	for key, member := range object {
		want := 0
		switch strings.ToLower(key) {
		case "idsite":
			want = site
		case "idsitehsr":
			want = recording
		case "idloghsr":
			want = logID
		default:
			continue
		}
		if got := jsonScalar(member); got != "" && got != strconv.Itoa(want) {
			return true
		}
	}
	return false
}

func invokeRecordedSessionsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get recorded session"
	var args struct {
		IDSite    *int   `json:"idSite"`
		IDSiteHsr *int   `json:"idSiteHsr"`
		IDLogHsr  *int   `json:"idLogHsr"`
		Limit     int    `json:"limit"`
		Cursor    string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkRecordingID(args.IDSiteHsr); err != nil {
		return nil, err
	}
	if err := checkRecordedID("idLogHsr", args.IDLogHsr, "matomo.recorded-sessions.list"); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	paging, err := startPage(provider.CursorBinding(recordedSessionsGet.ID, scopeBinding(resolved), site, *args.IDSiteHsr,
		*args.IDLogHsr), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := heatmapsRequirement(recordedSessionsGet.ID)
	// The recording ID counts across websites: this read binds it to the website before any session is read.
	if _, err := client.fetchSessionRecording(ctx, op, req, site, *args.IDSiteHsr); err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idSiteHsr": {strconv.Itoa(*args.IDSiteHsr)},
		"idLogHsr": {strconv.Itoa(*args.IDLogHsr)}}
	var answer json.RawMessage
	if err := client.call(ctx, op, req, "HeatmapSessionRecording.getRecordedSession", params, &answer); err != nil {
		return nil, err
	}
	var decoded any
	trimmed := bytes.TrimSpace(answer)
	if string(trimmed) == "null" || string(trimmed) == "false" || len(trimmed) == 0 || json.Unmarshal(trimmed, &decoded) != nil {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid recorded session")
	}
	result := &RecordedSessionResult{IDSite: site, IDSiteHsr: *args.IDSiteHsr, IDLogHsr: *args.IDLogHsr}
	withheld := 0
	switch typed := decoded.(type) {
	case []any:
		events := make([]any, len(typed))
		for i := range typed {
			events[i] = boundedValue(withholdTypedText(typed[i], &withheld), 0)
		}
		batch, next := slice(paging, events)
		result.Events, result.HasMore, result.NextCursor = batch, next != "", next
	case map[string]any:
		if sessionNamesOther(typed, site, *args.IDSiteHsr, *args.IDLogHsr) {
			return nil, provider.InvalidResponse(op, "Matomo returned another recorded session than asked for")
		}
		result.Session = boundedValue(withholdTypedText(typed, &withheld), 0)
	default:
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid recorded session")
	}
	result.TypedTextWithheld = withheld
	var types json.RawMessage
	if err := client.call(ctx, op, req, "HeatmapSessionRecording.getEventTypes", url.Values{}, &types); err != nil {
		return nil, err
	}
	rows, valid := readRows[eventTypeJSON](types)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned invalid event types")
	}
	for _, row := range rows {
		if len(result.EventTypes) == maxResultEntries {
			break
		}
		result.EventTypes = append(result.EventTypes, EventType{Key: row.Key.number(), Name: row.Name.str(maxStringLength)})
	}
	return result, nil
}

func sessionRecordingsOperations() []capability.Operation {
	return append([]capability.Operation{
		{Descriptor: sessionRecordingsList, Handler: capability.Handler(invokeSessionRecordingsList)},
		{Descriptor: sessionRecordingsGet, Handler: capability.Handler(invokeSessionRecordingsGet)},
		{Descriptor: recordedSessionsList, Handler: capability.Handler(invokeRecordedSessionsList)},
		{Descriptor: recordedSessionsGet, Handler: capability.Handler(invokeRecordedSessionsGet)},
	}, sessionRecordingChangeOperations()...)
}
