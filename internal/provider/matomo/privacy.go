package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// dataSubjectsSensitive classifies the tools that find and export the data of one person.
const dataSubjectsSensitive = "matomo-data-subjects"

const (
	// maxFindRows is the filter_limit Matomo itself applies to findDataSubjects; a list that reaches it may be cut.
	maxFindRows = 401
	// maxExportVisits bounds the input list of an export at the most one find answers.
	maxExportVisits    = maxFindRows
	defaultExportBatch = 10
	maxExportBatch     = 25
	// maxExportBytes bounds the rows of one export call as they are returned; the rest is cut and flagged.
	maxExportBytes   = 256 << 10
	maxExportTables  = 100
	maxExportColumns = 200
	maxExportKey     = 64
	maxExportValue   = maxUrlLength
)

const personText = "Visitor IDs, IP addresses, user IDs, locations, and the names and addresses in the logs are " +
	"personal data and untrusted text; they are shortened and addresses are never fetched"

// findMembers are the columns findDataSubjects keeps of a visit. The icon members are paths Matomo
// reports; they are text that is never fetched.
var findMembers = []member{
	{"idVisit", "id_visit", kInt, "Visit ID; pass it with id_site to matomo.data-subjects.export"},
	{"idSite", "id_site", kInt, "Website ID of the visit"},
	{"siteName", "site_name", kText, "Website name, untrusted"},
	{"lastActionDateTime", "last_action_datetime", kText, "Date and time of the last action as Matomo reports it (UTC)"},
	{"visitorId", "visitor_id", kText, "Visitor ID, personal data"},
	{"visitIp", "visit_ip", kText, "IP address of the visitor, personal data; Matomo may anonymize it"},
	{"userId", "user_id", kText, "User ID the website assigned to the visitor, personal data, untrusted"},
	{"deviceType", "device_type", kText, "Device type, such as smartphone or desktop"},
	{"deviceModel", "device_model", kText, "Device model"},
	{"deviceTypeIcon", "device_type_icon", kText, "Icon path of the device type, untrusted text that is never fetched"},
	{"operatingSystem", "operating_system", kText, "Operating system with version"},
	{"operatingSystemIcon", "operating_system_icon", kText, "Icon path of the operating system, untrusted text that is never fetched"},
	{"browser", "browser", kText, "Browser with version"},
	{"browserFamilyDescription", "browser_family_description", kText, "Browser engine with its description"},
	{"browserIcon", "browser_icon", kText, "Icon path of the browser, untrusted text that is never fetched"},
	{"country", "country", kText, "Country of the visitor, personal data"},
	{"region", "region", kText, "Region of the visitor, personal data"},
	{"countryFlag", "country_flag", kText, "Icon path of the country flag, untrusted text that is never fetched"},
}

func findFields() []capability.Field {
	fields := []capability.Field{
		{Name: "visits", Description: "Visits of the person that the segment selects, on the website asked for, in " +
			"the order Matomo answers (newest first), cut to this batch"},
		{Name: "matomo_limit_reached", Description: "True when Matomo answered its maximum of 401 visits, so the list " +
			"may be incomplete; narrow the segment"},
	}
	for _, m := range findMembers {
		fields = append(fields, capability.Field{Name: "visits[]." + m.to, Description: m.doc})
	}
	return append(fields, pagingFields...)
}

var dataSubjectsFind = capability.Descriptor{
	ID: Provider + ".data-subjects.find", Version: 1, Title: "Find the visits of a person in Matomo",
	Description: "Find the visits of one person on one website by a segment (PrivacyManager.findDataSubjects), for a " +
		"data subject request. The segment decides who is found; the answer is the visit IDs with reduced visit data, at " +
		"most 401 as Matomo caps it, in batches. It needs an admin right on the website and the plugin Live; Matomo " +
		"answers an empty list, not an error, for a website whose visits log or visitor profile is disabled. " + personText,
	Tags:     []string{"matomo", "gdpr", "privacy", "data-subjects", "visitors", "find", "personal-data"},
	Risk:     readRisk(dataSubjectsSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + siteKey + `,"segment":{"type":"string",` +
		`"minLength":1,"maxLength":2048},` + pagingKeys + `},"required":["segment"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"visits":{"type":"array","items":{"type":"object"}},` +
		`"matomo_limit_reached":{"type":"boolean"},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["visits","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "idSite", Description: "Website ID from matomo.sites.list; optional when the connection binds exactly one " +
			"website; must lie inside the connection's targets; one website per call", Target: "site"},
		{Name: "segment", Required: true, Description: "Matomo segment that selects the person, such as " +
			"visitorId==0123456789abcdef or userId==alice@example.test; AND is ; and OR is ,; 1 to 2048 characters " +
			"without control characters"},
	}, pagingArguments...),
	Fields: findFields(),
	Examples: []capability.Example{
		{Description: "Find the visits of a visitor ID on website 1",
			Arguments: json.RawMessage(`{"idSite":1,"segment":"visitorId==0123456789abcdef"}`)},
	},
}

// FindResult is the answer of data-subjects.find.
type FindResult struct {
	Visits             []map[string]any `json:"visits"`
	MatomoLimitReached bool             `json:"matomo_limit_reached,omitempty"`
	HasMore            bool             `json:"has_more"`
	NextCursor         string           `json:"next_cursor,omitempty"`
}

func invokeDataSubjectsFind(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "find data subjects"
	var args struct {
		IDSite  *int    `json:"idSite"`
		Segment *string `json:"segment"`
		Limit   int     `json:"limit"`
		Cursor  string  `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if args.Segment == nil || !validText(*args.Segment, maxSegment) {
		return nil, invalidArgument("segment must hold 1 to 2048 bytes without control characters")
	}
	paging, err := startPage(provider.CursorBinding(dataSubjectsFind.ID, id, *args.Segment), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// Live is required here: without it Matomo answers an empty list that looks like "no data".
	req := requirement{tool: dataSubjectsFind.ID, plugin: "Live"}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "segment": {*args.Segment}}
	var listed []row
	if err := client.callLive(ctx, op, req, "PrivacyManager.findDataSubjects", params, &listed); err != nil {
		return nil, err
	}
	kept := make([]map[string]any, 0, len(listed))
	for _, one := range listed {
		// A visit of another website, or without the IDs an export needs, is dropped row by row.
		site, visit := one.integer("idSite"), one.integer("idVisit")
		if site == nil || visit == nil || *site != id || *visit < 1 {
			continue
		}
		out := map[string]any{}
		readMembers(one, findMembers, out)
		kept = append(kept, out)
	}
	batch, next := slice(paging, kept)
	if batch == nil {
		batch = []map[string]any{}
	}
	return &FindResult{Visits: batch, MatomoLimitReached: len(listed) >= maxFindRows, HasMore: next != "", NextCursor: next}, nil
}

const visitIDKeys = `"idSite":{"type":"integer","minimum":1,"maximum":999999999},` +
	`"idVisit":{"type":"integer","minimum":1,"maximum":999999999}`

var dataSubjectsExport = capability.Descriptor{
	ID: Provider + ".data-subjects.export", Version: 1, Title: "Export the data of visits from Matomo",
	Description: "Export everything Matomo holds about the visits that matomo.data-subjects.find returned " +
		"(PrivacyManager.exportDataSubjects): the rows of each log table of the visits, for a data subject request. At " +
		"most 25 visits (10 by default) are exported per call; next_cursor continues the same list. It needs an admin " +
		"right on every website of the list. The answer is cut at 256 KiB (truncated), values are shortened, and " +
		"columns that hold an access value are left out. " + personText,
	Tags: []string{"matomo", "gdpr", "privacy", "data-subjects", "visitors", "export", "personal-data"},
	Risk: readRisk(dataSubjectsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"visits":{"type":"array","minItems":1,"maxItems":401,` +
		`"items":{"type":"object","properties":{` + visitIDKeys + `},"required":["idSite","idVisit"],` +
		`"additionalProperties":false}},"limit":{"type":"integer","minimum":1,"maximum":25},` +
		`"cursor":{"type":"string","minLength":1,"maxLength":128,"pattern":"^[A-Za-z0-9_-]+$"}},` +
		`"required":["visits"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"visits":{"type":"array","items":{"type":"object",` +
		`"properties":{"id_site":{"type":"integer"},"id_visit":{"type":"integer"}},"additionalProperties":false}},` +
		`"tables":{"type":"array","items":{"type":"object"}},"truncated":{"type":"boolean"},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["visits","tables","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "visits", Required: true, Description: "The visits to export, 1 to 401 entries of idSite and idVisit " +
			"(id_site and id_visit of matomo.data-subjects.find); every idSite must lie inside the connection's targets"},
		{Name: "limit", Description: "Visits exported per call, from 1 through 25; 10 when omitted; a continuation " +
			"keeps the batch size of its first batch"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous call with the same visits; the first batch " +
			"when omitted"},
	},
	Fields: []capability.Field{
		{Name: "visits", Description: "The visits exported by this call"},
		{Name: "visits[].id_site", Description: "Website ID"},
		{Name: "visits[].id_visit", Description: "Visit ID"},
		{Name: "tables", Description: "One entry per log table (and per plugin export) that holds rows of these " +
			"visits, ordered by name; personal data"},
		{Name: "tables[].name", Description: "Name of the log table or export, such as log_visit or " +
			"log_link_visit_action, untrusted"},
		{Name: "tables[].row_count", Description: "Rows Matomo returned for the table, inside the websites asked for"},
		{Name: "tables[].rows", Description: "The rows with the columns Matomo returns, in the order of the table; " +
			"values are numbers, flags, or text shortened to 2048 bytes, untrusted; nested values become text; " +
			"columns that name a token, password, secret, or API key, and values that carry one, are left out"},
		{Name: "tables[].truncated", Description: "True when rows of the table were left out to keep the answer " +
			"within 256 KiB; export fewer visits per call"},
		{Name: "truncated", Description: "True when any table was cut"},
		{Name: "next_cursor", Description: "Cursor of the following batch of the same list, absent when has_more is false"},
		{Name: "has_more", Description: "True when visits of the list remain to be exported"},
	},
	Examples: []capability.Example{
		{Description: "Export two visits found on website 1",
			Arguments: json.RawMessage(`{"visits":[{"idSite":1,"idVisit":4711},{"idSite":1,"idVisit":4712}]}`)},
	},
}

// visitKey identifies one visit of an export.
// The fields are exported so that the cursor binding, which hashes the list as JSON, covers them.
type visitKey struct{ Site, Visit int }

// ExportVisit is one visit of the batch.
type ExportVisit struct {
	IDSite  int `json:"id_site"`
	IDVisit int `json:"id_visit"`
}

// ExportTable is the rows of one log table.
type ExportTable struct {
	Name      string           `json:"name"`
	RowCount  int              `json:"row_count"`
	Rows      []map[string]any `json:"rows"`
	Truncated bool             `json:"truncated,omitempty"`
}

// ExportResult is the answer of data-subjects.export.
type ExportResult struct {
	Visits     []ExportVisit `json:"visits"`
	Tables     []ExportTable `json:"tables"`
	Truncated  bool          `json:"truncated,omitempty"`
	HasMore    bool          `json:"has_more"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

func invokeDataSubjectsExport(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "export data subjects"
	var args struct {
		Visits []struct {
			IDSite  *int `json:"idSite"`
			IDVisit *int `json:"idVisit"`
		} `json:"visits"`
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	if len(args.Visits) < 1 || len(args.Visits) > maxExportVisits {
		return nil, invalidArgument("visits must hold 1 to 401 entries")
	}
	visits := make([]visitKey, 0, len(args.Visits))
	for _, one := range args.Visits {
		if one.IDSite == nil || one.IDVisit == nil || !validID(*one.IDSite) || !validID(*one.IDVisit) {
			return nil, invalidArgument("every visit needs a positive integer idSite and idVisit")
		}
		// Every website of the list is checked before a secret is read, and none is named in the refusal.
		if !bound.allows(*one.IDSite) {
			return nil, invalidArgument("every idSite must lie inside the targets of this connection")
		}
		key := visitKey{*one.IDSite, *one.IDVisit}
		if slices.Contains(visits, key) {
			return nil, invalidArgument("visits must not repeat a visit")
		}
		visits = append(visits, key)
	}
	limit := args.Limit
	if limit == 0 && args.Cursor == "" {
		limit = defaultExportBatch
	}
	if limit > maxExportBatch {
		return nil, invalidArgument("limit must be between 1 and " + strconv.Itoa(maxExportBatch))
	}
	binding := provider.CursorBinding(dataSubjectsExport.ID, bound.allSites, sortedSites(bound), visits)
	paging, err := startPage(binding, limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	if paging.limit > maxExportBatch {
		return nil, invalidArgument("cursor is not a next_cursor of this list; start the list again without cursor")
	}
	batch, next := slice(paging, visits)
	if len(batch) == 0 {
		return nil, invalidArgument("cursor is not a next_cursor of this list; start the list again without cursor")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	listed := make([]ExportVisit, 0, len(batch))
	for i, one := range batch {
		prefix := "visits[" + strconv.Itoa(i) + "]"
		params.Set(prefix+"[idsite]", strconv.Itoa(one.Site))
		params.Set(prefix+"[idvisit]", strconv.Itoa(one.Visit))
		listed = append(listed, ExportVisit{IDSite: one.Site, IDVisit: one.Visit})
	}
	var answer json.RawMessage
	req := requirement{tool: dataSubjectsExport.ID, plugin: "PrivacyManager"}
	if err := client.call(ctx, op, req, "PrivacyManager.exportDataSubjects", params, &answer); err != nil {
		return nil, err
	}
	tables, truncated, err := exportTables(op, answer, batch)
	if err != nil {
		return nil, err
	}
	return &ExportResult{Visits: listed, Tables: tables, Truncated: truncated, HasMore: next != "", NextCursor: next}, nil
}

func validID(id int) bool { return id >= 1 && len(strconv.Itoa(id)) <= maxIDDigits }

// accessValue matches the text of a value that carries an access value: a URL parameter such as token_auth=,
// or a nested member that is named like one.
var accessValue = regexp.MustCompile(`(?i)\b(token_auth|access_token|auth_token|api_?key|password|passwd)=|` +
	`"[^"]*(token|passw|secret|api_?key)[^"]*"\s*:`)

// accessColumn matches the name of a column that holds an access value.
var accessColumn = regexp.MustCompile(`(?i)token|passw|secret|api_?key`)

// exportValue reads one value of an export row: numbers and flags stay, text is shortened, nested values become
// text. A value that carries an access value, and an empty one, is left out.
func exportValue(raw json.RawMessage) (any, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false
	}
	if accessValue.Match(trimmed) {
		return nil, false
	}
	switch trimmed[0] {
	case '"':
		var text string
		if json.Unmarshal(trimmed, &text) != nil {
			return nil, false
		}
		return boundString(text, maxExportValue), true
	case '{', '[':
		return boundString(string(trimmed), maxExportValue), true
	}
	if len(trimmed) > 64 {
		return nil, false
	}
	return json.RawMessage(trimmed), true
}

// exportRow converts one row of an export. A row that is no object (a plugin may export plain texts) becomes a
// row with the single member value.
func exportRow(raw json.RawMessage) (map[string]any, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		value, ok := exportValue(trimmed)
		if !ok {
			return nil, false
		}
		return map[string]any{"value": value}, true
	}
	var members row
	if json.Unmarshal(trimmed, &members) != nil {
		return nil, false
	}
	keys := make([]string, 0, len(members))
	for key := range members {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	out := map[string]any{}
	for _, key := range keys {
		if accessColumn.MatchString(key) {
			continue
		}
		if len(out) >= maxExportColumns {
			break
		}
		if value, ok := exportValue(members[key]); ok {
			out[boundString(key, maxExportKey)] = value
		}
	}
	return out, len(out) > 0
}

// exportTables reads the answer of exportDataSubjects: an object from table name to rows, or an empty list.
// Rows of another website or visit than the batch are dropped. The rows are returned up to maxExportBytes; a
// table that does not fit is cut and flagged.
func exportTables(op string, answer json.RawMessage, batch []visitKey) ([]ExportTable, bool, error) {
	trimmed := bytes.TrimSpace(answer)
	if bytes.Equal(trimmed, []byte("[]")) || bytes.Equal(trimmed, []byte("null")) {
		return []ExportTable{}, false, nil
	}
	var named map[string]json.RawMessage
	if json.Unmarshal(trimmed, &named) != nil {
		return nil, false, provider.InvalidResponse(op, "Matomo returned an invalid export")
	}
	names := make([]string, 0, len(named))
	for name := range named {
		names = append(names, name)
	}
	slices.Sort(names)
	var truncated bool
	if len(names) > maxExportTables {
		names, truncated = names[:maxExportTables], true
	}
	used := 0
	tables := make([]ExportTable, 0, len(names))
	for _, name := range names {
		var rows []json.RawMessage
		if json.Unmarshal(named[name], &rows) != nil {
			return nil, false, provider.InvalidResponse(op, "Matomo returned an invalid export")
		}
		table := ExportTable{Name: bounded(name), Rows: []map[string]any{}}
		for _, one := range rows {
			if !inBatch(one, batch) {
				continue
			}
			table.RowCount++
			converted, ok := exportRow(one)
			if !ok {
				continue
			}
			size, _ := json.Marshal(converted)
			if table.Truncated || used+len(size) > maxExportBytes {
				table.Truncated = true
				continue
			}
			used += len(size)
			table.Rows = append(table.Rows, converted)
		}
		truncated = truncated || table.Truncated
		tables = append(tables, table)
	}
	return tables, truncated, nil
}

// inBatch keeps a row that names no website, or names a visit of the batch.
func inBatch(raw json.RawMessage, batch []visitKey) bool {
	var members row
	if json.Unmarshal(bytes.TrimSpace(raw), &members) != nil {
		return true
	}
	site := members.integer("idsite")
	if site == nil {
		return true
	}
	visit := members.integer("idvisit")
	return slices.ContainsFunc(batch, func(one visitKey) bool {
		return one.Site == *site && (visit == nil || one.Visit == *visit)
	})
}
