package matomo

import (
	"context"
	"encoding/json"
	"math"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	trackingSensitive = "matomo-tracking-data"
	maxTrackingURL    = 2048
	maxTrackingText   = 512
	maxTrackingShort  = 255
	maxDimensions     = 20
	maxTrackingMillis = 3600000
	// A visit or action time stamp is plausible between 2000-01-01 and 2100-01-01.
	minTrackingTime = 946684800
	maxTrackingTime = 4102444800
	trackingSent    = "sent to Matomo; this does not confirm that Matomo processed the request"
)

// trackingSentOnce words the unclear outcome of a request that may have arrived.
const trackingSentOnce = "; sent once and never repeated by Qatlas: a repeat may be counted twice, so read the " +
	"visit log with matomo.visits.list before repeating it"

var (
	trackingVisitorPattern = regexp.MustCompile(`^[0-9a-fA-F]{16}$`)
	pageViewPattern        = regexp.MustCompile(`^[A-Za-z0-9]{6}$`)
	languagePatternRe      = regexp.MustCompile(`^[A-Za-z]{2,8}([-_][A-Za-z0-9]{1,8}){0,3}$`)
	resolutionPattern      = regexp.MustCompile(`^[1-9][0-9]{2,4}x[1-9][0-9]{2,4}$`)
	dimensionPattern       = regexp.MustCompile(`^[1-9][0-9]{0,5}$`)
	countryPattern         = regexp.MustCompile(`^[A-Za-z]{2}$`)
	regionPattern          = regexp.MustCompile(`^[A-Za-z0-9]{1,3}$`)
)

// trackingClock is the clock that bounds timestamp; tests replace it.
var trackingClock = time.Now

// Matomo ignores a cdt that lies in the future or 20 years or more back and records the request at the time it
// arrives, so the tool refuses both instead.
const trackingTimeWindow = 20 * 365 * 86400

const trackingContextBaseSchema = `"url":{"type":"string","minLength":1,"maxLength":2048},` +
	`"referrer":{"type":"string","minLength":1,"maxLength":2048},` +
	`"pageTitle":{"type":"string","minLength":1,"maxLength":512},` +
	`"pageViewId":{"type":"string","pattern":"^[A-Za-z0-9]{6}$","maxLength":6},` +
	`"visitorId":{"type":"string","pattern":"^[0-9a-fA-F]{16}$","maxLength":16},` +
	`"userId":{"type":"string","minLength":1,"maxLength":255},` +
	`"overrideVisitorId":{"type":"string","pattern":"^[0-9a-fA-F]{16}$","maxLength":16},` +
	`"newVisit":{"type":"boolean"},` +
	`"language":{"type":"string","pattern":"^[A-Za-z]{2,8}([-_][A-Za-z0-9]{1,8}){0,3}$","maxLength":35},` +
	`"screenResolution":{"type":"string","pattern":"^[1-9][0-9]{2,4}x[1-9][0-9]{2,4}$","maxLength":11},` +
	`"userAgent":{"type":"string","minLength":1,"maxLength":512},` +
	`"hour":{"type":"integer","minimum":0,"maximum":23},"minute":{"type":"integer","minimum":0,"maximum":59},` +
	`"second":{"type":"integer","minimum":0,"maximum":59},` +
	`"campaignName":{"type":"string","minLength":1,"maxLength":255},` +
	`"campaignKeyword":{"type":"string","minLength":1,"maxLength":255},` +
	`"visitCount":{"type":"integer","minimum":1,"maximum":2147483647},` +
	`"firstVisitTime":{"type":"integer","minimum":946684800,"maximum":4102444800},` +
	`"previousVisitTime":{"type":"integer","minimum":946684800,"maximum":4102444800}`

const trackingOverrideSchema = `"clientIp":{"type":"string","minLength":2,"maxLength":45},` +
	`"timestamp":{"type":"integer","minimum":946684800,"maximum":4102444800},` +
	`"country":{"type":"string","pattern":"^[A-Za-z]{2}$","maxLength":2},` +
	`"region":{"type":"string","pattern":"^[A-Za-z0-9]{1,3}$","maxLength":3},` +
	`"city":{"type":"string","minLength":1,"maxLength":255},` +
	`"latitude":{"type":"number","minimum":-90,"maximum":90},"longitude":{"type":"number","minimum":-180,"maximum":180}`

const trackingDimensionsSchema = `"dimensions":{"type":"object","maxProperties":20,"propertyNames":{"pattern":"^[1-9][0-9]{0,5}$"},` +
	`"additionalProperties":{"type":"string","maxLength":255}}`

const trackingContextSchema = trackingContextBaseSchema + `,` + trackingOverrideSchema + `,` + trackingDimensionsSchema

const trackingOutputSchema = `{"type":"object","properties":{"idSite":{"type":"integer"},"sent":{"type":"boolean"},` +
	`"processingConfirmed":{"type":"boolean"},"message":{"type":"string"}},` +
	`"required":["idSite","sent","processingConfirmed","message"],"additionalProperties":false}`

const trackingNote = "Sends one request to the tracking endpoint matomo.php of the website's Matomo and never " +
	"retries. Without clientIp, timestamp, country, region, city, latitude, and longitude the endpoint needs no " +
	"token and the tool reads no secret; the visitor is then the Qatlas host, whose address Matomo records, " +
	"and whose user agent is qatlas-cli unless userAgent is given. With any of those seven the connection's token " +
	"is read and sent as token_auth in the request body, and Matomo accepts the override only for a token with at " +
	"least the Write role for the website, unless the instance sets tracking_requests_require_authentication=0 " +
	"(then it accepts them from anyone); Matomo ignores a timestamp that is in the future or 20 years back, and " +
	"refuses one older than the raw data it keeps (delete_logs_older_than). " +
	"Success means only that Matomo accepted the request " +
	"(HTTP 204 or an image): it does not confirm processing, because Matomo answers many invalid parameters the " +
	"same way and a queue confirms before it processes; an unknown website, a missing or too weak token for an " +
	"override, and a timestamp that is too old are refused as HTTP 400 and switched-off " +
	"tracking as 503. It writes raw data (visits, actions, conversions) that no API call can undo and fakes visits " +
	"that reports then count; the overrides additionally forge the origin, time, and place of a visit; " +
	"visitorId, overrideVisitorId, and userId merge or take over visitor identities, and " +
	"userId, clientIp, userAgent, referrer, and the texts may carry personal data. Not idempotent: a repeated call is counted " +
	"twice. The website must be inside the connection's targets"

func trackingRisk() capability.Risk {
	return capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: trackingSensitive}
}

func trackingContextArguments() []capability.Argument {
	return []capability.Argument{
		idSiteArgument(),
		{Name: "url", Required: true, Description: "Address of the page the visitor is on, an http or https address up " +
			"to 2048 characters; passed on as text and never fetched"},
		{Name: "referrer", Description: "Address the visitor came from (urlref), an http or https address up to 2048 " +
			"characters; may carry personal data"},
		{Name: "pageTitle", Description: "Title of the page (action_name), up to 512 characters"},
		{Name: "pageViewId", Description: "Six letters or digits that tie the actions of one page view together (pv_id)"},
		{Name: "visitorId", Description: "Visitor ID (_id) of 16 hexadecimal characters; actions with the same ID " +
			"join one visitor, so a known ID merges into that visitor's history"},
		{Name: "userId", Description: "Your own ID of the signed-in user (uid), up to 255 characters; Matomo then " +
			"derives the visitor ID from it and joins visits across devices; personal data"},
		{Name: "overrideVisitorId", Description: "WARNING: forces the visitor ID (cid) of 16 hexadecimal characters " +
			"and overrides visitorId and userId, so the request joins or takes over that visitor and cannot be undone"},
		{Name: "newVisit", Description: "True forces a new visit (new_visit) instead of continuing the current one"},
		{Name: "language", Description: "Browser language, such as de or en-US (lang)"},
		{Name: "screenResolution", Description: "Screen resolution as WIDTHxHEIGHT, such as 1920x1080 (res)"},
		{Name: "userAgent", Description: "User agent of the visitor (ua), up to 512 characters; without it Matomo " +
			"sees qatlas-cli"},
		{Name: "hour", Description: "Local hour of the visitor, 0 to 23 (h); hour, minute, and second go together"},
		{Name: "minute", Description: "Local minute of the visitor, 0 to 59 (m)"},
		{Name: "second", Description: "Local second of the visitor, 0 to 59 (s)"},
		{Name: "campaignName", Description: "Campaign name of the visit (_rcn), up to 255 characters"},
		{Name: "campaignKeyword", Description: "Campaign keyword of the visit (_rck), up to 255 characters"},
		{Name: "visitCount", Description: "Number of visits of the visitor so far (_idvc), at least 1"},
		{Name: "firstVisitTime", Description: "Unix time in seconds of the visitor's first visit (_idts)"},
		{Name: "previousVisitTime", Description: "Unix time in seconds of the visitor's previous visit (_viewts)"},
		{Name: "clientIp", Description: "WARNING: overrides the visitor's IP address (cip), an IPv4 or IPv6 address; " +
			"personal data. Needs the token (role Write); setting any of the seven override arguments sends it"},
		{Name: "timestamp", Description: "Overrides the time of the request (cdt) as Unix time in seconds, not in the " +
			"future and less than 20 years back; Matomo refuses a time older than the raw data it keeps"},
		{Name: "country", Description: "Overrides the visitor's country (country) as a two-letter ISO 3166-1 code, " +
			"such as de; sent in lower case; needs the token"},
		{Name: "region", Description: "Overrides the visitor's region (region) as a code of 1 to 3 letters or digits, " +
			"such as BY; needs the token"},
		{Name: "city", Description: "Overrides the visitor's city (city), up to 255 characters; needs the token"},
		{Name: "latitude", Description: "Overrides the visitor's latitude (lat), -90 to 90 degrees; needs the token"},
		{Name: "longitude", Description: "Overrides the visitor's longitude (long), -180 to 180 degrees; needs the token"},
		{Name: "dimensions", Description: "Values of custom dimensions of the visit or the action, keyed by dimension " +
			"ID from matomo.dimensions.list as a number, at most 20, each up to 255 characters (dimension1, dimension2, ...)"},
	}
}

var trackingResultFields = []capability.Field{
	{Name: "idSite", Description: "The website the request named"},
	{Name: "sent", Description: "True once Matomo accepted the request with HTTP 204 or an image"},
	{Name: "processingConfirmed", Description: "Always false: acceptance does not prove that Matomo recorded the request"},
	{Name: "message", Description: "The same statement as text"},
}

var trackingRecordPageView = capability.Descriptor{
	ID: Provider + ".tracking.record-page-view", Version: 1, Title: "Record a page view in Matomo",
	Description: "Record one page view (rec=1 with url and action_name) of a website. " + trackingNote +
		"; optional performance timings in milliseconds belong to the page view" + trackingSentOnce,
	Tags: []string{"matomo", "tracking", "page-view", "create"}, Risk: trackingRisk(), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + trackingContextSchema + `,` +
		trackingPerformanceSchema + `},"required":["url"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(trackingOutputSchema),
	Arguments:    append(trackingContextArguments(), trackingPerformanceArguments()...),
	Fields:       trackingResultFields,
	Examples: []capability.Example{{Description: "Record a page view of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"url":"https://example.org/pricing","pageTitle":"Pricing"}`)}},
}

var trackingRecordEvent = capability.Descriptor{
	ID: Provider + ".tracking.record-event", Version: 1, Title: "Record an event in Matomo",
	Description: "Record one event (category, action, optional name and value) of a website. " + trackingNote +
		"; the event texts may carry personal data" + trackingSentOnce,
	Tags: []string{"matomo", "tracking", "event", "create"}, Risk: trackingRisk(), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + trackingContextSchema + `,` +
		`"category":{"type":"string","minLength":1,"maxLength":512},"action":{"type":"string","minLength":1,"maxLength":512},` +
		`"name":{"type":"string","minLength":1,"maxLength":512},"value":{"type":"number"}},` +
		`"required":["url","category","action"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(trackingOutputSchema),
	Arguments: append(trackingContextArguments(),
		capability.Argument{Name: "category", Required: true, Description: "Event category (e_c), up to 512 characters"},
		capability.Argument{Name: "action", Required: true, Description: "Event action (e_a), up to 512 characters"},
		capability.Argument{Name: "name", Description: "Event name (e_n), up to 512 characters"},
		capability.Argument{Name: "value", Description: "Numeric event value (e_v), a finite number"}),
	Fields: trackingResultFields,
	Examples: []capability.Example{{Description: "Record the event Video Play of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"url":"https://example.org/video","category":"Video","action":"Play","name":"Intro","value":12.5}`)}},
}

var trackingRecordSiteSearch = capability.Descriptor{
	ID: Provider + ".tracking.record-site-search", Version: 1, Title: "Record an internal search in Matomo",
	Description: "Record one internal site search (keyword, optional category and result count) of a website. " +
		trackingNote + "; the keyword is what a visitor typed and may carry personal data" + trackingSentOnce,
	Tags: []string{"matomo", "tracking", "site-search", "create"}, Risk: trackingRisk(), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + trackingContextSchema + `,` +
		`"keyword":{"type":"string","minLength":1,"maxLength":512},"category":{"type":"string","minLength":1,"maxLength":512},` +
		`"resultCount":{"type":"integer","minimum":0,"maximum":2147483647}},` +
		`"required":["url","keyword"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(trackingOutputSchema),
	Arguments: append(trackingContextArguments(),
		capability.Argument{Name: "keyword", Required: true, Description: "Search keyword (search), up to 512 characters"},
		capability.Argument{Name: "category", Description: "Search category (search_cat), up to 512 characters"},
		capability.Argument{Name: "resultCount", Description: "Number of results shown (search_count), 0 or more"}),
	Fields: trackingResultFields,
	Examples: []capability.Example{{Description: "Record a search without results on website 1",
		Arguments: json.RawMessage(`{"idSite":1,"url":"https://example.org/search","keyword":"invoice","resultCount":0}`)}},
}

var trackingRecordLink = capability.Descriptor{
	ID: Provider + ".tracking.record-link", Version: 1, Title: "Record a download or outlink in Matomo",
	Description: "Record one download (kind download) or one click on an outgoing link (kind outlink) of a website. " +
		trackingNote + "; url is the page the visitor was on, target the file or address that was used" + trackingSentOnce,
	Tags: []string{"matomo", "tracking", "download", "outlink", "create"}, Risk: trackingRisk(), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + trackingContextSchema + `,` +
		`"kind":{"type":"string","enum":["download","outlink"]},"target":{"type":"string","minLength":1,"maxLength":2048}},` +
		`"required":["url","kind","target"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(trackingOutputSchema),
	Arguments: append(trackingContextArguments(),
		capability.Argument{Name: "kind", Required: true, Description: "download sends the target as download, " +
			"outlink as link"},
		capability.Argument{Name: "target", Required: true, Description: "Address of the downloaded file or the " +
			"outgoing link, an http or https address up to 2048 characters; passed on as text and never fetched"}),
	Fields: trackingResultFields,
	Examples: []capability.Example{{Description: "Record the download of a PDF on website 1",
		Arguments: json.RawMessage(`{"idSite":1,"url":"https://example.org/docs","kind":"download","target":"https://example.org/files/guide.pdf"}`)}},
}

const trackingPerformanceSchema = `"performance":{"type":"object","properties":{` +
	`"network":{"type":"integer","minimum":0,"maximum":3600000},"server":{"type":"integer","minimum":0,"maximum":3600000},` +
	`"transfer":{"type":"integer","minimum":0,"maximum":3600000},"domProcessing":{"type":"integer","minimum":0,"maximum":3600000},` +
	`"domCompletion":{"type":"integer","minimum":0,"maximum":3600000},"onLoad":{"type":"integer","minimum":0,"maximum":3600000}},` +
	`"minProperties":1,"additionalProperties":false},` +
	`"generationTime":{"type":"integer","minimum":0,"maximum":3600000}`

func trackingPerformanceArguments() []capability.Argument {
	return []capability.Argument{
		{Name: "performance", Description: "Page load timings in milliseconds, each 0 to 3600000, any of network " +
			"(pf_net), server (pf_srv), transfer (pf_tfr), domProcessing (pf_dm1), domCompletion (pf_dm2), and onLoad (pf_onl)"},
		{Name: "generationTime", Description: "Time the server needed to generate the page in milliseconds (gt_ms), 0 to 3600000"},
	}
}

// TrackingSent is the answer of every tracking tool.
type TrackingSent struct {
	IDSite              int    `json:"idSite"`
	Sent                bool   `json:"sent"`
	ProcessingConfirmed bool   `json:"processingConfirmed"`
	Message             string `json:"message"`
}

// trackingContext holds the arguments every tracking tool shares.
type trackingContext struct {
	IDSite            *int              `json:"idSite"`
	URL               *string           `json:"url"`
	Referrer          *string           `json:"referrer"`
	PageTitle         *string           `json:"pageTitle"`
	PageViewID        *string           `json:"pageViewId"`
	VisitorID         *string           `json:"visitorId"`
	UserID            *string           `json:"userId"`
	OverrideVisitorID *string           `json:"overrideVisitorId"`
	NewVisit          *bool             `json:"newVisit"`
	Language          *string           `json:"language"`
	ScreenResolution  *string           `json:"screenResolution"`
	UserAgent         *string           `json:"userAgent"`
	Hour              *int              `json:"hour"`
	Minute            *int              `json:"minute"`
	Second            *int              `json:"second"`
	CampaignName      *string           `json:"campaignName"`
	CampaignKeyword   *string           `json:"campaignKeyword"`
	VisitCount        *int              `json:"visitCount"`
	FirstVisitTime    *int64            `json:"firstVisitTime"`
	PreviousVisitTime *int64            `json:"previousVisitTime"`
	Dimensions        map[string]string `json:"dimensions"`
	ClientIP          *string           `json:"clientIp"`
	Timestamp         *int64            `json:"timestamp"`
	Country           *string           `json:"country"`
	Region            *string           `json:"region"`
	City              *string           `json:"city"`
	Latitude          *float64          `json:"latitude"`
	Longitude         *float64          `json:"longitude"`
}

// trackingText validates an optional text and stores it. A blank text is refused: Matomo ignores it silently.
func trackingText(form url.Values, key, argument string, given *string, limit int) error {
	if given == nil {
		return nil
	}
	if !formText(*given) || strings.TrimSpace(*given) == "" || utf8.RuneCountInString(*given) > limit {
		return invalidArgument(argument + " must be a text of 1 to " + strconv.Itoa(limit) + " characters")
	}
	form.Set(key, *given)
	return nil
}

// trackingAddress validates an http or https address and stores it as text.
func trackingAddress(form url.Values, key, argument string, given *string, required bool) error {
	if given == nil {
		if required {
			return invalidArgument(argument + " is required")
		}
		return nil
	}
	parsed, err := url.Parse(*given)
	if !formText(*given) || utf8.RuneCountInString(*given) > maxTrackingURL || err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
		strings.ContainsAny(*given, " \t\r\n") {
		return invalidArgument(argument + " must be an http or https address of at most 2048 characters")
	}
	form.Set(key, *given)
	return nil
}

func trackingPattern(form url.Values, key, argument string, given *string, pattern *regexp.Regexp) error {
	if given == nil {
		return nil
	}
	if !pattern.MatchString(*given) {
		return invalidArgument(argument + " does not have the required form")
	}
	form.Set(key, *given)
	return nil
}

func trackingRange(form url.Values, key, argument string, given *int64, low, high int64) error {
	if given == nil {
		return nil
	}
	if *given < low || *given > high {
		return invalidArgument(argument + " is outside its allowed range")
	}
	form.Set(key, strconv.FormatInt(*given, 10))
	return nil
}

// values validates the shared arguments and returns the form fields they stand for.
func (c *trackingContext) values() (url.Values, error) {
	form := url.Values{}
	if err := trackingAddress(form, "url", "url", c.URL, true); err != nil {
		return nil, err
	}
	if err := trackingAddress(form, "urlref", "referrer", c.Referrer, false); err != nil {
		return nil, err
	}
	for _, step := range []error{
		trackingText(form, "action_name", "pageTitle", c.PageTitle, maxTrackingText),
		trackingText(form, "ua", "userAgent", c.UserAgent, maxTrackingText),
		trackingText(form, "uid", "userId", c.UserID, maxTrackingShort),
		trackingText(form, "_rcn", "campaignName", c.CampaignName, maxTrackingShort),
		trackingText(form, "_rck", "campaignKeyword", c.CampaignKeyword, maxTrackingShort),
		trackingPattern(form, "pv_id", "pageViewId", c.PageViewID, pageViewPattern),
		trackingPattern(form, "_id", "visitorId", c.VisitorID, trackingVisitorPattern),
		trackingPattern(form, "cid", "overrideVisitorId", c.OverrideVisitorID, trackingVisitorPattern),
		trackingPattern(form, "lang", "language", c.Language, languagePatternRe),
		trackingPattern(form, "res", "screenResolution", c.ScreenResolution, resolutionPattern),
		trackingRange(form, "_idvc", "visitCount", asInt64(c.VisitCount), 1, math.MaxInt32),
		trackingRange(form, "_idts", "firstVisitTime", c.FirstVisitTime, minTrackingTime, maxTrackingTime),
		trackingRange(form, "_viewts", "previousVisitTime", c.PreviousVisitTime, minTrackingTime, maxTrackingTime),
	} {
		if step != nil {
			return nil, step
		}
	}
	if c.NewVisit != nil && *c.NewVisit {
		form.Set("new_visit", "1")
	}
	if c.Hour != nil || c.Minute != nil || c.Second != nil {
		if c.Hour == nil || c.Minute == nil || c.Second == nil || *c.Hour < 0 || *c.Hour > 23 ||
			*c.Minute < 0 || *c.Minute > 59 || *c.Second < 0 || *c.Second > 59 {
			return nil, invalidArgument("hour, minute, and second go together and must be a valid local time")
		}
		form.Set("h", strconv.Itoa(*c.Hour))
		form.Set("m", strconv.Itoa(*c.Minute))
		form.Set("s", strconv.Itoa(*c.Second))
	}
	if len(c.Dimensions) > maxDimensions {
		return nil, invalidArgument("dimensions holds at most 20 values")
	}
	ids := make([]string, 0, len(c.Dimensions))
	for id := range c.Dimensions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		value := c.Dimensions[id]
		if !dimensionPattern.MatchString(id) || !formText(value) || utf8.RuneCountInString(value) > maxTrackingShort {
			return nil, invalidArgument("a dimension needs a numeric ID and a text of at most 255 characters")
		}
		form.Set("dimension"+id, value)
	}
	return form, nil
}

func asInt64(given *int) *int64 {
	if given == nil {
		return nil
	}
	value := int64(*given)
	return &value
}

// trackingRequired validates a text the tool cannot do without.
func trackingRequired(form url.Values, key, argument string, given *string, limit int) error {
	if given == nil {
		return invalidArgument(argument + " is required")
	}
	return trackingText(form, key, argument, given, limit)
}

// trackingTimestamp validates the time override and stores it as cdt.
func trackingTimestamp(form url.Values, given *int64) error {
	if given == nil {
		return nil
	}
	now := trackingClock().Unix()
	if *given < minTrackingTime || *given > now || now-*given >= trackingTimeWindow {
		return invalidArgument("timestamp must be Unix time in seconds, not in the future and less than 20 years back")
	}
	form.Set("cdt", strconv.FormatInt(*given, 10))
	return nil
}

// overrides validates the arguments that need the token and returns the form fields they stand for. They stay
// apart from values so that only trackingAuth can carry them to the body.
func (c *trackingContext) overrides() (url.Values, error) {
	form := url.Values{}
	if c.ClientIP != nil {
		address, err := netip.ParseAddr(*c.ClientIP)
		if err != nil || address.Zone() != "" || len(*c.ClientIP) > 45 {
			return nil, invalidArgument("clientIp must be an IPv4 or IPv6 address")
		}
		form.Set("cip", address.String())
	}
	if err := trackingTimestamp(form, c.Timestamp); err != nil {
		return nil, err
	}
	if c.Country != nil {
		if !countryPattern.MatchString(*c.Country) {
			return nil, invalidArgument("country must be a two-letter code")
		}
		form.Set("country", strings.ToLower(*c.Country))
	}
	if err := trackingPattern(form, "region", "region", c.Region, regionPattern); err != nil {
		return nil, err
	}
	if err := trackingText(form, "city", "city", c.City, maxTrackingShort); err != nil {
		return nil, err
	}
	for _, coordinate := range []struct {
		key, name string
		given     *float64
		limit     float64
	}{{"lat", "latitude", c.Latitude, 90}, {"long", "longitude", c.Longitude, 180}} {
		if coordinate.given == nil {
			continue
		}
		if math.IsNaN(*coordinate.given) || *coordinate.given < -coordinate.limit || *coordinate.given > coordinate.limit {
			return nil, invalidArgument(coordinate.name + " must be a finite number within its range")
		}
		form.Set(coordinate.key, strconv.FormatFloat(*coordinate.given, 'f', -1, 64))
	}
	return form, nil
}

// trackingToken resolves the token for a request with overrides and registers it for redaction before use.
func trackingToken(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op string) (string, error) {
	if secrets == nil {
		return "", provider.Fail(op, "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleToken)
	if err != nil {
		return "", err
	}
	if !provider.ValidHeaderToken(value.Secret) {
		return "", &provider.Error{Class: provider.ClassAuth, Op: op, Message: "the Matomo token is unusable"}
	}
	if red != nil {
		red.Add(value.Secret)
	}
	return value.Secret, nil
}

// record validates the arguments, settles the website before any I/O, reads the token only when an override
// needs it, and sends the one request.
func record(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op string, raw json.RawMessage, args any, shared func() *trackingContext, extend func(url.Values) error) (any, error) {
	if err := decodeArguments(raw, args); err != nil {
		return nil, err
	}
	common := shared()
	site, err := selectSite(resolved, common.IDSite)
	if err != nil {
		return nil, err
	}
	form, err := common.values()
	if err != nil {
		return nil, err
	}
	overrides, err := common.overrides()
	if err != nil {
		return nil, err
	}
	if err := extend(form); err != nil {
		return nil, err
	}
	sender, err := openTracking(resolved)
	if err != nil {
		return nil, err
	}
	var auth *trackingAuth
	if len(overrides) > 0 {
		token, err := trackingToken(ctx, resolved, secrets, red, op)
		if err != nil {
			return nil, err
		}
		auth = &trackingAuth{token: token, overrides: overrides}
	}
	if err := uncertainChange(sender.send(ctx, op, site, form, auth), trackingSentOnce); err != nil {
		return nil, err
	}
	return &TrackingSent{IDSite: site, Sent: true, Message: trackingSent}, nil
}

func pageViewEntry() (any, *trackingContext, func(url.Values) error) {
	var args struct {
		trackingContext
		Performance    map[string]*int `json:"performance"`
		GenerationTime *int            `json:"generationTime"`
	}
	return &args, &args.trackingContext, func(form url.Values) error {
		timings := map[string]string{"network": "pf_net", "server": "pf_srv", "transfer": "pf_tfr",
			"domProcessing": "pf_dm1", "domCompletion": "pf_dm2", "onLoad": "pf_onl"}
		if len(args.Performance) > len(timings) {
			return invalidArgument("performance names only the six timings")
		}
		for name, value := range args.Performance {
			key, known := timings[name]
			if !known || value == nil || *value < 0 || *value > maxTrackingMillis {
				return invalidArgument("performance holds only the six timings, each 0 to 3600000 milliseconds")
			}
			form.Set(key, strconv.Itoa(*value))
		}
		if args.GenerationTime != nil {
			if *args.GenerationTime < 0 || *args.GenerationTime > maxTrackingMillis {
				return invalidArgument("generationTime must be 0 to 3600000 milliseconds")
			}
			form.Set("gt_ms", strconv.Itoa(*args.GenerationTime))
		}
		return nil
	}
}

func invokeTrackingRecordPageView(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	args, shared, extend := pageViewEntry()
	return record(ctx, resolved, secrets, red, "record page view", raw, args, func() *trackingContext { return shared }, extend)
}

func eventEntry() (any, *trackingContext, func(url.Values) error) {
	var args struct {
		trackingContext
		Category *string  `json:"category"`
		Action   *string  `json:"action"`
		Name     *string  `json:"name"`
		Value    *float64 `json:"value"`
	}
	return &args, &args.trackingContext, func(form url.Values) error {
		if err := trackingRequired(form, "e_c", "category", args.Category, maxTrackingText); err != nil {
			return err
		}
		if err := trackingRequired(form, "e_a", "action", args.Action, maxTrackingText); err != nil {
			return err
		}
		if err := trackingText(form, "e_n", "name", args.Name, maxTrackingText); err != nil {
			return err
		}
		if args.Value != nil {
			if math.IsNaN(*args.Value) || math.IsInf(*args.Value, 0) {
				return invalidArgument("value must be a finite number")
			}
			form.Set("e_v", strconv.FormatFloat(*args.Value, 'f', -1, 64))
		}
		return nil
	}
}

func invokeTrackingRecordEvent(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	args, shared, extend := eventEntry()
	return record(ctx, resolved, secrets, red, "record event", raw, args, func() *trackingContext { return shared }, extend)
}

func siteSearchEntry() (any, *trackingContext, func(url.Values) error) {
	var args struct {
		trackingContext
		Keyword     *string `json:"keyword"`
		Category    *string `json:"category"`
		ResultCount *int    `json:"resultCount"`
	}
	return &args, &args.trackingContext, func(form url.Values) error {
		if err := trackingRequired(form, "search", "keyword", args.Keyword, maxTrackingText); err != nil {
			return err
		}
		if err := trackingText(form, "search_cat", "category", args.Category, maxTrackingText); err != nil {
			return err
		}
		if args.ResultCount != nil {
			if *args.ResultCount < 0 || *args.ResultCount > math.MaxInt32 {
				return invalidArgument("resultCount must be 0 or more")
			}
			form.Set("search_count", strconv.Itoa(*args.ResultCount))
		}
		return nil
	}
}

func invokeTrackingRecordSiteSearch(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	args, shared, extend := siteSearchEntry()
	return record(ctx, resolved, secrets, red, "record site search", raw, args, func() *trackingContext { return shared }, extend)
}

func linkEntry() (any, *trackingContext, func(url.Values) error) {
	var args struct {
		trackingContext
		Kind   *string `json:"kind"`
		Target *string `json:"target"`
	}
	return &args, &args.trackingContext, func(form url.Values) error {
		if args.Kind == nil || (*args.Kind != "download" && *args.Kind != "outlink") {
			return invalidArgument("kind must be download or outlink")
		}
		key := "download"
		if *args.Kind == "outlink" {
			key = "link"
		}
		return trackingAddress(form, key, "target", args.Target, true)
	}
}

func invokeTrackingRecordLink(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	args, shared, extend := linkEntry()
	return record(ctx, resolved, secrets, red, "record link", raw, args, func() *trackingContext { return shared }, extend)
}

func trackingOperations() []capability.Operation {
	return append([]capability.Operation{
		{Descriptor: trackingRecordPageView, Handler: capability.Handler(invokeTrackingRecordPageView)},
		{Descriptor: trackingRecordEvent, Handler: capability.Handler(invokeTrackingRecordEvent)},
		{Descriptor: trackingRecordSiteSearch, Handler: capability.Handler(invokeTrackingRecordSiteSearch)},
		{Descriptor: trackingRecordLink, Handler: capability.Handler(invokeTrackingRecordLink)},
		{Descriptor: trackingRecordBotRequest, Handler: capability.Handler(invokeTrackingRecordBotRequest)},
	}, append(append(trackingCommerceOperations(), trackingContentOperations()...), trackingBatchOperations()...)...)
}
