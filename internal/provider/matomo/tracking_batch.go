package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxBatchEntries = 100
	// maxBatchBody bounds the JSON body of one bulk request in bytes.
	maxBatchBody = 512 * 1024
	// maxBatchAnswer bounds what is read of the answer; a genuine one is a few dozen bytes.
	maxBatchAnswer = 4096
)

// trackingBatchSentOnce words the unclear outcome of a bulk request that may have arrived.
const trackingBatchSentOnce = "; sent once and never repeated by Qatlas: a repeat counts the entries Matomo already " +
	"recorded twice, so read the visit log with matomo.visits.list before repeating any of them"

// batchKinds maps the type of an entry to the builder of the single tool of the same name. Chatbot requests are
// not part of the bulk.
var batchKinds = map[string]func() (any, *trackingContext, func(url.Values) error){
	"page-view": pageViewEntry, "event": eventEntry, "site-search": siteSearchEntry, "link": linkEntry,
	"goal": goalEntry, "order": orderEntry, "cart-update": cartUpdateEntry,
	"content-impression": contentImpressionEntry, "content-interaction": contentInteractionEntry,
}

// batchEntrySchema is the union of the arguments of the nine single tools without their overrides. The type
// decides which of them an entry may carry; the handler enforces that with the rules of the single tool.
const batchEntrySchema = `{"type":"object","properties":{` +
	`"type":{"type":"string","enum":["page-view","event","site-search","link","goal","order","cart-update",` +
	`"content-impression","content-interaction"]},` + idSiteSchema + `,` + trackingContextBaseSchema + `,` +
	trackingDimensionsSchema + `,` + trackingPerformanceSchema + `,` +
	`"category":{"type":"string","minLength":1,"maxLength":512},"action":{"type":"string","minLength":1,"maxLength":512},` +
	`"name":{"type":"string","minLength":1,"maxLength":512},"value":{"type":"number"},` +
	`"keyword":{"type":"string","minLength":1,"maxLength":512},` +
	`"resultCount":{"type":"integer","minimum":0,"maximum":2147483647},` +
	`"kind":{"type":"string","enum":["download","outlink"]},"target":{"type":"string","minLength":1,"maxLength":2048},` +
	`"goalId":{"type":"integer","minimum":1,"maximum":2147483647},"revenue":` + commerceAmountSchema + `,` +
	`"orderId":{"type":"string","minLength":1,"maxLength":255},"subtotal":` + commerceAmountSchema + `,` +
	`"tax":` + commerceAmountSchema + `,"shipping":` + commerceAmountSchema + `,"discount":` + commerceAmountSchema + `,` +
	commerceItemsSchema + `,` +
	`"interaction":{"type":"string","minLength":1,"maxLength":512},` + contentPropertiesSchema + `},` +
	`"required":["type","url"],"additionalProperties":false}`

const trackingBatchOutputSchema = `{"type":"object","properties":{"idSite":{"type":"integer"},"sent":{"type":"boolean"},` +
	`"processingConfirmed":{"type":"boolean"},"entries":{"type":"integer"},"tracked":{"type":"integer"},` +
	`"invalid":{"type":"integer"},"message":{"type":"string"}},` +
	`"required":["idSite","sent","processingConfirmed","entries","tracked","invalid","message"],"additionalProperties":false}`

var trackingRecordBatch = capability.Descriptor{
	ID: Provider + ".tracking.record-batch", Version: 1, Title: "Record several tracking entries of a website in Matomo",
	Description: "Record 1 to 100 entries of one website with a single bulk request (JSON body with requests) to " +
		"matomo.php; it needs the BulkTracking plugin of Matomo. Each entry has a type (page-view, event, " +
		"site-search, link, goal, order, cart-update, content-impression, or content-interaction) and the " +
		"arguments of the tool of the same name, checked by the same rules; the idSite of the call applies to " +
		"every entry, and an entry that names another website refuses the whole call. An entry cannot carry " +
		"clientIp, timestamp, country, region, city, latitude, or longitude, and requests of AI chatbots are no " +
		"entries, so the call needs no token, reads no secret, and sends neither token_auth nor an " +
		"Authorization header; the visitor of every entry without userAgent is the Qatlas host with the user " +
		"agent qatlas-cli. The request is sent once and never retries. Matomo answers with the number of entries " +
		"it tracked and the number it rejected as invalid; it skips an invalid entry and records the others, so " +
		"a call can succeed in part, and it can abort on an exception in the middle (HTTP 500) after it " +
		"recorded the entries before it, which stay unless the instance rolls a bulk back. tracked also counts a visit that Matomo excludes, and " +
		"the answer does not confirm processing, because a queue confirms before it processes; an answer of " +
		"tracked 0 and invalid 0 for sent entries can mean switched-off tracking. The confirmation covers the " +
		"number of entries and the website, not which of them Matomo accepts. It writes raw data (visits, " +
		"actions, conversions) that no API call can undo and fakes visits, orders, and revenue that reports " +
		"then count; visitorId, overrideVisitorId, and userId merge or take over visitor identities, and " +
		"userId, userAgent, referrer, and the texts may carry personal data. Not idempotent: a repeated call " +
		"counts every entry twice. The website must be inside the connection's targets" + trackingBatchSentOnce,
	Tags: []string{"matomo", "tracking", "bulk", "create"}, Risk: trackingRisk(), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"entries":{"type":"array","minItems":1,"maxItems":100,"items":` + batchEntrySchema + `}},` +
		`"required":["entries"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(trackingBatchOutputSchema),
	Arguments: []capability.Argument{
		idSiteArgument(),
		{Name: "entries", Required: true, Description: "1 to 100 entries, each an object with type and the " +
			"arguments of the single tool of that name (for a link entry, kind is download or outlink); url is " +
			"required; the override arguments, token_auth, and chatbot requests are not accepted; an entry may " +
			"repeat idSite but not name another website"},
	},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the entries were sent for"},
		{Name: "sent", Description: "True once Matomo answered the bulk request with a plausible count"},
		{Name: "processingConfirmed", Description: "Always false: the counts do not prove that Matomo recorded the entries"},
		{Name: "entries", Description: "Number of entries sent"},
		{Name: "tracked", Description: "Number of entries Matomo counted as tracked, including excluded visits"},
		{Name: "invalid", Description: "Number of entries Matomo rejected as invalid; it does not name them without a Super User token"},
		{Name: "message", Description: "The same statement as text"},
	},
	Examples: []capability.Example{{Description: "Record a page view and an event of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"entries":[{"type":"page-view","url":"https://example.org/pricing","pageTitle":"Pricing"},` +
			`{"type":"event","url":"https://example.org/video","category":"Video","action":"Play"}]}`)}},
}

// TrackingBatchSent is the answer of the bulk tracking tool.
type TrackingBatchSent struct {
	IDSite              int    `json:"idSite"`
	Sent                bool   `json:"sent"`
	ProcessingConfirmed bool   `json:"processingConfirmed"`
	Entries             int    `json:"entries"`
	Tracked             int    `json:"tracked"`
	Invalid             int    `json:"invalid"`
	Message             string `json:"message"`
}

// batchEntryForm validates one entry with the rules of the single tool its type names and returns its form
// fields. The overrides are refused, never validated, so no entry can reach the token path.
func batchEntryForm(index int, raw json.RawMessage, site int) (url.Values, error) {
	label := "entry " + strconv.Itoa(index+1)
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, invalidArgument(label + " must be an object")
	}
	var kind string
	if given, found := fields["type"]; !found || json.Unmarshal(given, &kind) != nil {
		return nil, invalidArgument(label + " needs a type")
	}
	build, known := batchKinds[kind]
	if !known {
		return nil, invalidArgument(label + " has a type that is not one of the nine entry types")
	}
	delete(fields, "type")
	data, err := json.Marshal(fields)
	if err != nil {
		return nil, invalidArgument(label + " could not be read")
	}
	args, shared, extend := build()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(args) != nil {
		return nil, invalidArgument(label + " holds an argument its type does not accept or one of the wrong kind")
	}
	if shared.ClientIP != nil || shared.Timestamp != nil || shared.Country != nil || shared.Region != nil ||
		shared.City != nil || shared.Latitude != nil || shared.Longitude != nil {
		return nil, invalidArgument(label + " carries an override (clientIp, timestamp, country, region, city, " +
			"latitude, or longitude), which needs the token and is not part of the bulk")
	}
	if shared.IDSite != nil && *shared.IDSite != site {
		return nil, invalidArgument(label + " names another website than idSite")
	}
	form, err := shared.values()
	if err == nil {
		err = extend(form)
	}
	if err != nil {
		var invalid *provider.InvalidRequestError
		if errors.As(err, &invalid) {
			return nil, invalidArgument(label + ": " + invalid.Message)
		}
		return nil, err
	}
	return form, nil
}

func invokeTrackingRecordBatch(ctx context.Context, resolved *config.Resolved, _ *secret.Resolver,
	_ *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "record batch"
	var args struct {
		IDSite  *int              `json:"idSite"`
		Entries []json.RawMessage `json:"entries"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if len(args.Entries) == 0 || len(args.Entries) > maxBatchEntries {
		return nil, invalidArgument("entries holds 1 to 100 entries")
	}
	forms := make([]url.Values, 0, len(args.Entries))
	for index, entry := range args.Entries {
		form, err := batchEntryForm(index, entry, site)
		if err != nil {
			return nil, err
		}
		forms = append(forms, form)
	}
	sender, err := openTracking(resolved)
	if err != nil {
		return nil, err
	}
	answer, err := sender.sendBatch(ctx, op, site, forms)
	if err := uncertainChange(err, trackingBatchSentOnce); err != nil {
		return nil, err
	}
	message := "sent to Matomo in one request with " + strconv.Itoa(len(forms)) + " entries; Matomo reports " +
		strconv.Itoa(answer.tracked) + " tracked and " + strconv.Itoa(answer.invalid) + " invalid; this does not " +
		"confirm that Matomo processed them, because tracked also counts excluded visits and a queue confirms " +
		"before it processes"
	if answer.invalid > 0 {
		message += "; Matomo does not say which entries were invalid"
	}
	if missing := len(forms) - answer.tracked - answer.invalid; missing > 0 {
		message += "; " + strconv.Itoa(missing) + " entries were neither tracked nor rejected, for example " +
			"because tracking is switched off or a database lock timed out"
	}
	return &TrackingBatchSent{IDSite: site, Sent: true, Entries: len(forms), Tracked: answer.tracked,
		Invalid: answer.invalid, Message: message}, nil
}

// batchAnswer is what Matomo's BulkTracking plugin answers. invalid_indices and message are left unread: the
// first appears only for a Super User token, which the bulk never sends, the second only in debug mode.
type batchAnswer struct {
	Status  string `json:"status"`
	Tracked *int   `json:"tracked"`
	Invalid *int   `json:"invalid"`
}

type batchCounts struct{ tracked, invalid int }

// batchCountsOf returns the counts of an answer that is plausible for the number of entries sent: both present,
// neither negative nor above it, and together not above it, because a rejected entry is not counted as tracked.
func batchCountsOf(answer batchAnswer, entries int) (batchCounts, bool) {
	if answer.Tracked == nil || answer.Invalid == nil || *answer.Tracked < 0 || *answer.Invalid < 0 ||
		*answer.Tracked > entries || *answer.Invalid > entries || *answer.Tracked+*answer.Invalid > entries {
		return batchCounts{}, false
	}
	return batchCounts{tracked: *answer.Tracked, invalid: *answer.Invalid}, true
}

// sendBatch posts the entries once as a JSON body to matomo.php. The entries are query strings, the form the
// plugin documents. Every entry is checked against the never-sent parameters like a single request, and the
// request carries no token, no Authorization header, and no query. It never retries.
func (s *trackingSender) sendBatch(ctx context.Context, op string, site int, entries []url.Values) (batchCounts, error) {
	requests := make([]string, 0, len(entries))
	for _, params := range entries {
		form, err := checkedForm(op, params)
		if err != nil {
			return batchCounts{}, err
		}
		stamp(form, site)
		requests = append(requests, "?"+form.Encode())
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(struct {
		Requests []string `json:"requests"`
	}{requests}); err != nil {
		return batchCounts{}, provider.Fail(op, "the request could not be built")
	}
	// Encode ends the document with a newline.
	body.Truncate(body.Len() - 1)
	if body.Len() > maxBatchBody {
		return batchCounts{}, invalidArgument("the entries together exceed 524288 bytes")
	}
	if err := s.limiter.Wait(ctx); err != nil {
		return batchCounts{}, provider.Waited(op, subject, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.origin+"/matomo.php", &body)
	if err != nil {
		return batchCounts{}, provider.Fail(op, "the request could not be built")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "qatlas-cli")
	response, err := s.http.Do(req)
	if err != nil {
		return batchCounts{}, provider.Transport(op, subject, err)
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxBatchAnswer+1))
	readable := readErr == nil && len(data) <= maxBatchAnswer
	var answer batchAnswer
	parsed := readable && json.Unmarshal(data, &answer) == nil
	counts, plausible := batchCountsOf(answer, len(entries))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		// A 204 means Matomo took send_image for a request to answer nothing, which the bulk never sends.
		if response.StatusCode != http.StatusOK || !parsed || answer.Status != "success" || !plausible {
			return batchCounts{}, provider.InvalidResponse(op, "the Matomo tracker returned an answer that is not a "+
				"bulk answer; the BulkTracking plugin may be inactive or a proxy answered")
		}
		return counts, nil
	}
	if parsed && plausible && answer.Status == "error" {
		return batchCounts{}, &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "the Matomo tracker aborted the bulk request after it tracked " + strconv.Itoa(counts.tracked) +
				" and rejected " + strconv.Itoa(counts.invalid) + " of " + strconv.Itoa(len(entries)) +
				" entries; the entries it tracked stay recorded unless the instance rolls a bulk back"}
	}
	if response.StatusCode == http.StatusBadRequest {
		// Matomo answers 400 with its start page when the BulkTracking plugin is not active and the body is no
		// request; a bulk it understands reports an invalid entry in the count instead.
		return batchCounts{}, &provider.InvalidRequestError{Message: "the Matomo tracker refused the bulk request as " +
			"invalid: the BulkTracking plugin may be inactive or the request was not understood; nothing was recorded"}
	}
	return batchCounts{}, s.failure(op, response, false)
}

func trackingBatchOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: trackingRecordBatch, Handler: capability.Handler(invokeTrackingRecordBatch)},
	}
}
