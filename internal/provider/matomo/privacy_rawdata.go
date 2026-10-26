package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// rawDataSensitive classifies the tools that anonymize or purge the stored logs of the instance.
const rawDataSensitive = "matomo-raw-data"

const (
	maxAnonymizeSites   = maxTargets
	maxAnonymizeColumns = 50
	anonymizeDatePart   = `\d{4}-\d{2}-\d{2}`
	anonymizeColumns    = `{"type":"array","maxItems":50,"items":{"type":"string","pattern":"^[A-Za-z][A-Za-z0-9_]{0,63}$"}}`
)

var (
	// columnName is the shape of a log table column; Matomo checks the name against the columns it can anonymize.
	columnName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	// anonymizeDate is one day or a range of two days; relative dates are not accepted.
	anonymizeDate = regexp.MustCompile(`^(` + anonymizeDatePart + `)(?:,(` + anonymizeDatePart + `))?$`)
)

const rawDataSentOnce = "; sent once and never repeated by Qatlas: after an unclear result the job may have run or " +
	"been scheduled, so check the logs and the anonymization settings in Matomo before repeating it"

var rawDataAnonymize = capability.Descriptor{
	ID: Provider + ".raw-data.anonymize", Version: 1, Title: "Anonymize stored visit logs in Matomo",
	Description: "Schedule the anonymization of stored visit logs of listed websites for one day or a date range " +
		"(PrivacyManager.anonymizeSomeRawData): IP addresses, locations, user IDs, and chosen log columns are " +
		"replaced for good. It cannot be undone. Matomo only schedules the job; its scheduled task runs it later, so " +
		"scheduled means accepted, not done. It needs Super User rights, a connection whose targets name the " +
		"instance, and websites inside its site targets. Qatlas sends only the explicit website IDs, never all " +
		"websites. At least one anonymize choice or column is required. Matomo asks for a password confirmation: the " +
		"credential needs the secret role " + rolePasswordConfirmation + ", which no argument carries. Sent once and " +
		"never repeated; offered only where a connection's tools list names it",
	Tags: []string{"matomo", "gdpr", "privacy", "anonymize", "raw-data", "instance", "personal-data"},
	Risk: eraseRisk(capability.EffectUpdate, rawDataSensitive), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"idSites":{"type":"array","minItems":1,"maxItems":` +
		strconv.Itoa(maxAnonymizeSites) + `,"items":{"type":"integer","minimum":1,"maximum":999999999}},` +
		`"date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}(,\\d{4}-\\d{2}-\\d{2})?$","maxLength":21},` +
		`"anonymizeIp":{"type":"boolean"},"anonymizeLocation":{"type":"boolean"},"anonymizeUserId":{"type":"boolean"},` +
		`"unsetVisitColumns":` + anonymizeColumns + `,"unsetLinkVisitActionColumns":` + anonymizeColumns + `},` +
		`"required":["idSites","date"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSites":{"type":"array","items":{"type":"integer"}},` +
		`"date":{"type":"string"},"scheduled":{"type":"boolean"}},` +
		`"required":["idSites","date","scheduled"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "idSites", Required: true, Description: "Website IDs whose logs are anonymized, 1 to 100 distinct IDs; " +
			"all must lie inside the connection's site targets; one foreign website refuses the whole call; all is not accepted"},
		{Name: "date", Required: true, Description: "One day as YYYY-MM-DD or a range as YYYY-MM-DD,YYYY-MM-DD; " +
			"relative dates are not accepted"},
		{Name: "anonymizeIp", Description: "True to anonymize visitor IP addresses; false when omitted"},
		{Name: "anonymizeLocation", Description: "True to anonymize stored locations; false when omitted"},
		{Name: "anonymizeUserId", Description: "True to anonymize stored user IDs; false when omitted"},
		{Name: "unsetVisitColumns", Description: "Columns of the visit log to clear, up to 50 names; Matomo refuses " +
			"a column it cannot anonymize"},
		{Name: "unsetLinkVisitActionColumns", Description: "Columns of the action log to clear, up to 50 names; " +
			"Matomo refuses a column it cannot anonymize"},
	},
	Fields: []capability.Field{
		{Name: "idSites", Description: "The websites the job was scheduled for, in ascending order"},
		{Name: "date", Description: "The day or date range of the job"},
		{Name: "scheduled", Description: "True once Matomo accepted the job; the anonymization itself runs later"},
	},
	Examples: []capability.Example{{Description: "Anonymize IP addresses and user IDs of January 2026 on website 1",
		Arguments: json.RawMessage(`{"idSites":[1],"date":"2026-01-01,2026-01-31","anonymizeIp":true,"anonymizeUserId":true}`)}},
}

// RawDataAnonymized is the answer of raw-data.anonymize.
type RawDataAnonymized struct {
	IDSites   []int  `json:"idSites"`
	Date      string `json:"date"`
	Scheduled bool   `json:"scheduled"`
}

// checkAnonymizeDate accepts a real day or an ordered range of two real days.
func checkAnonymizeDate(date string) bool {
	parts := anonymizeDate.FindStringSubmatch(date)
	if parts == nil {
		return false
	}
	start, err := time.Parse(dateLayout, parts[1])
	if err != nil {
		return false
	}
	if parts[2] == "" {
		return true
	}
	end, err := time.Parse(dateLayout, parts[2])
	return err == nil && !end.Before(start)
}

func checkColumns(names []string) bool {
	if len(names) > maxAnonymizeColumns {
		return false
	}
	for _, name := range names {
		if !columnName.MatchString(name) {
			return false
		}
	}
	return true
}

func invokeRawDataAnonymize(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "anonymize raw data"
	var args struct {
		IDSites                     []int    `json:"idSites"`
		Date                        *string  `json:"date"`
		AnonymizeIP                 *bool    `json:"anonymizeIp"`
		AnonymizeLocation           *bool    `json:"anonymizeLocation"`
		AnonymizeUserID             *bool    `json:"anonymizeUserId"`
		UnsetVisitColumns           []string `json:"unsetVisitColumns"`
		UnsetLinkVisitActionColumns []string `json:"unsetLinkVisitActionColumns"`
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
	if len(args.IDSites) < 1 || len(args.IDSites) > maxAnonymizeSites {
		return nil, invalidArgument("idSites must hold 1 to 100 website IDs")
	}
	sites := make([]int, 0, len(args.IDSites))
	for _, id := range args.IDSites {
		if !validID(id) {
			return nil, invalidArgument("every idSites entry must be a positive integer")
		}
		// The whole list is checked before a secret is read; one foreign website refuses it and is not named.
		if !bound.allows(id) {
			return nil, invalidArgument("every idSites entry must lie inside the targets of this connection")
		}
		if slices.Contains(sites, id) {
			return nil, invalidArgument("idSites must not repeat a website")
		}
		sites = append(sites, id)
	}
	slices.Sort(sites)
	if !bound.instance {
		return nil, invalidArgument("this tool needs a connection bound to the instance")
	}
	if args.Date == nil || !checkAnonymizeDate(*args.Date) {
		return nil, invalidArgument("date must be YYYY-MM-DD or YYYY-MM-DD,YYYY-MM-DD with the first day not after the second")
	}
	if !checkColumns(args.UnsetVisitColumns) || !checkColumns(args.UnsetLinkVisitActionColumns) {
		return nil, invalidArgument("each column list holds up to 50 column names of letters, digits, and underscores")
	}
	chosen := args.AnonymizeIP != nil && *args.AnonymizeIP || args.AnonymizeLocation != nil && *args.AnonymizeLocation ||
		args.AnonymizeUserID != nil && *args.AnonymizeUserID ||
		len(args.UnsetVisitColumns) > 0 || len(args.UnsetLinkVisitActionColumns) > 0
	if !chosen {
		return nil, invalidArgument("choose at least one of anonymizeIp, anonymizeLocation, anonymizeUserId, or a column")
	}
	ids := make([]string, 0, len(sites))
	for _, id := range sites {
		ids = append(ids, strconv.Itoa(id))
	}
	params := url.Values{"idSites": {strings.Join(ids, ",")}, "date": {*args.Date},
		"anonymizeIp": {flagValue(args.AnonymizeIP)}, "anonymizeLocation": {flagValue(args.AnonymizeLocation)},
		"anonymizeUserId": {flagValue(args.AnonymizeUserID)}}
	for _, name := range args.UnsetVisitColumns {
		params.Add("unsetVisitColumns[]", name)
	}
	for _, name := range args.UnsetLinkVisitActionColumns {
		params.Add("unsetLinkVisitActionColumns[]", name)
	}
	// The token is opened first, then the password role; a role that is not set ends the call before any request.
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := addPasswordConfirmation(ctx, resolved, secrets, red, params); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	req := requirement{tool: rawDataAnonymize.ID, plugin: "PrivacyManager"}
	if err := client.change(ctx, op, req, "PrivacyManager.anonymizeSomeRawData", params, rawDataSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the job", rawDataSentOnce)
	}
	return &RawDataAnonymized{IDSites: sites, Date: *args.Date, Scheduled: true}, nil
}

var dataPurgeRun = capability.Descriptor{
	ID: Provider + ".data-purge.run", Version: 1, Title: "Run the Matomo data purge now",
	Description: "Delete old raw logs and report data now, by the retention periods configured in Matomo " +
		"(PrivacyManager.executeDataPurge); what is deleted cannot be restored. It takes no argument and affects " +
		"every website of the instance. It needs Super User rights and a connection whose targets name the " +
		"instance; Matomo refuses it when its administrators disabled purge settings. Matomo asks for a password " +
		"confirmation: the credential needs the secret role " + rolePasswordConfirmation + ", which no argument " +
		"carries. Sent once and never repeated; offered only where a connection's tools list names it",
	Tags: []string{"matomo", "gdpr", "privacy", "purge", "retention", "instance"},
	Risk: eraseRisk(capability.EffectExecute, rawDataSensitive), Provider: Provider, RequiresToolAllowList: true,
	InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"executed":{"type":"boolean"}},"required":["executed"],"additionalProperties":false}`),
	Fields:       []capability.Field{{Name: "executed", Description: "True once Matomo ran the purge"}},
	Examples:     []capability.Example{{Description: "Run the purge", Arguments: json.RawMessage(`{}`)}},
}

// DataPurged is the answer of data-purge.run.
type DataPurged struct {
	Executed bool `json:"executed"`
}

func invokeDataPurgeRun(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "run data purge"
	var args struct{}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	params := url.Values{}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := addPasswordConfirmation(ctx, resolved, secrets, red, params); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	req := requirement{tool: dataPurgeRun.ID, plugin: "PrivacyManager"}
	if err := client.change(ctx, op, req, "PrivacyManager.executeDataPurge", params, rawDataSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the purge", rawDataSentOnce)
	}
	return &DataPurged{Executed: true}, nil
}
