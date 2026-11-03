package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// maxCrashMerge is Qatlas's own limit on the crashes of one merge; the reference states none.
	maxCrashMerge = 100
	// crashIDsSchema allows no duplicates: a merge names each crash once.
	crashIDsSchema = `"idLogCrashes":{"type":"array","minItems":2,"maxItems":100,"uniqueItems":true,` +
		`"items":{"type":"integer","minimum":1,"maximum":2147483647}}`
)

var (
	crashSentOnceGet    = sentOnceFor("crash", "matomo.crashes.get")
	crashSentOnceGroups = sentOnceFor("crash group", "matomo.crash-groups.list")
)

// crashChangeRisk is the risk of every crash change: confirmed, open to the instance, and about the crash data of
// a website.
func crashChangeRisk(idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: capability.EffectUpdate, Idempotency: idempotency,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: crashesSensitive}
}

var crashesSetIgnored = capability.Descriptor{
	ID: Provider + ".crashes.set-ignored", Version: 1, Title: "Ignore or stop ignoring a Matomo crash",
	Description: "Set whether a crash of a website is ignored when tracking (CrashAnalytics.setIgnoreCrash); ignore is " +
		"true when omitted, as the reference defaults it. Before the change one read must show the crash for this " +
		"website, otherwise it is not found: to ignore a crash the read is the crash list, to stop ignoring it the " +
		"list of ignored crashes (CrashAnalytics.getIgnoredCrashes), which holds the crash only while it is ignored. " +
		"Whether an ignored crash stays in the crash list is not verified. Needs the premium plugin CrashAnalytics and, " +
		"expected, a write right on the website; idempotent in effect" + crashSentOnceGet,
	Tags: []string{"matomo", "crashes", "crash-analytics", "update"},
	Risk: crashChangeRisk(capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idLogCrashSchema + `,` +
		`"ignore":{"type":"boolean"}},"required":["idLogCrash"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idLogCrash":{"type":"integer"},"ignored":{"type":"boolean"}},` +
		`"required":["idSite","idLogCrash","ignored"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idLogCrash", Required: true, Description: "ID of the crash, from matomo.crashes.list"},
		{Name: "ignore", Description: "true to ignore the crash for future tracking, false to stop ignoring it; true when omitted"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the crash belongs to"},
		{Name: "idLogCrash", Description: "The crash that was changed"},
		{Name: "ignored", Description: "The state Matomo accepted: true for ignored, false for no longer ignored"}},
	Examples: []capability.Example{{Description: "Ignore crash 105 of website 1", Arguments: json.RawMessage(
		`{"idSite":1,"idLogCrash":105}`)}, {Description: "Stop ignoring crash 105", Arguments: json.RawMessage(
		`{"idSite":1,"idLogCrash":105,"ignore":false}`)}},
}

var crashesMerge = capability.Descriptor{
	ID: Provider + ".crashes.merge", Version: 1, Title: "Merge Matomo crashes",
	Description: "Merge two or more crashes of a website so that reports treat them as one crash " +
		"(CrashAnalytics.mergeCrashes). The IDs go to Matomo as one comma-separated list in the order given; Matomo " +
		"documents neither which crash becomes the parent nor what happens to a crash that is already merged. One read " +
		"of the crash list must show every crash for this website, otherwise the call is not found and nothing is " +
		"merged. Needs the premium plugin CrashAnalytics and, expected, a write right on the website; not idempotent" +
		crashSentOnceGroups,
	Tags: []string{"matomo", "crashes", "crash-analytics", "merge", "update"},
	Risk: crashChangeRisk(capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + crashIDsSchema + `},` +
		`"required":["idLogCrashes"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idLogCrashes":{"type":"array","items":{"type":"integer"}},"merged":{"type":"boolean"}},` +
		`"required":["idSite","idLogCrashes","merged"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idLogCrashes", Required: true, Description: "2 to 100 different crash IDs of the website to merge, from matomo.crashes.list"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the crashes belong to"},
		{Name: "idLogCrashes", Description: "The crashes that were asked to be merged, in the order given"},
		{Name: "merged", Description: "True once Matomo accepted the merge"}},
	Examples: []capability.Example{{Description: "Merge crashes 105 and 119 of website 1", Arguments: json.RawMessage(
		`{"idSite":1,"idLogCrashes":[105,119]}`)}},
}

var crashesUnmerge = capability.Descriptor{
	ID: Provider + ".crashes.unmerge", Version: 1, Title: "Unmerge a Matomo crash group",
	Description: "Unmerge a group of merged crashes of a website by the ID of its parent crash " +
		"(CrashAnalytics.unmergeCrashGroup). The group list of the website (CrashAnalytics.getCrashGroups) is read " +
		"first and must hold the group with a crash of this website, otherwise the call is not found; a crash that " +
		"is no parent of a group is not found either. Needs the premium plugin CrashAnalytics and, expected, a write " +
		"right on the website; idempotent in effect" + crashSentOnceGroups,
	Tags: []string{"matomo", "crashes", "crash-analytics", "unmerge", "update"},
	Risk: crashChangeRisk(capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idLogCrashSchema + `},` +
		`"required":["idLogCrash"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idLogCrash":{"type":"integer"},"unmerged":{"type":"boolean"}},` +
		`"required":["idSite","idLogCrash","unmerged"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idLogCrash", Required: true, Description: "ID of the parent crash of the group, as parent_idLogCrash of matomo.crash-groups.list"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the group belongs to"},
		{Name: "idLogCrash", Description: "The parent crash of the group that was unmerged"},
		{Name: "unmerged", Description: "True once Matomo accepted the change"}},
	Examples: []capability.Example{{Description: "Unmerge the group of parent crash 112 of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idLogCrash":112}`)}},
}

// CrashIgnoreSet is the answer of crashes.set-ignored.
type CrashIgnoreSet struct {
	IDSite     int  `json:"idSite"`
	IDLogCrash int  `json:"idLogCrash"`
	Ignored    bool `json:"ignored"`
}

// CrashesMerged is the answer of crashes.merge.
type CrashesMerged struct {
	IDSite       int   `json:"idSite"`
	IDLogCrashes []int `json:"idLogCrashes"`
	Merged       bool  `json:"merged"`
}

// CrashGroupUnmerged is the answer of crashes.unmerge.
type CrashGroupUnmerged struct {
	IDSite     int  `json:"idSite"`
	IDLogCrash int  `json:"idLogCrash"`
	Unmerged   bool `json:"unmerged"`
}

func checkCrashID(id *int) error {
	if id == nil || *id < 1 || *id > maxCrashID {
		return invalidArgument("idLogCrash must be a positive integer crash ID from matomo.crashes.list")
	}
	return nil
}

// proveCrashes ties every ID to the website with the one read of method: an ID counts across websites, so a row
// must name the website. The rows of getAllCrashes and getIgnoredCrashes are assumed to share their form.
func (c *Client) proveCrashes(ctx context.Context, op string, req requirement, method string, site int, ids []int) error {
	params := url.Values{"idSite": {strconv.Itoa(site)}}
	if method == "CrashAnalytics.getAllCrashes" {
		crashWindow(params, maxCrashRows)
	}
	var answer json.RawMessage
	if err := c.call(ctx, op, req, method, params, &answer); err != nil {
		return err
	}
	rows, valid := readRows[crashProofJSON](unwrapValue(answer))
	if !valid {
		return provider.InvalidResponse(op, "Matomo returned an invalid crash list")
	}
	missing := map[int]bool{}
	for _, id := range ids {
		missing[id] = true
	}
	for _, row := range rows {
		found, owner := row.ID.number(), row.IDSite.number()
		if found != nil && owner != nil && *owner == site {
			delete(missing, *found)
		}
	}
	if len(missing) > 0 {
		return crashNotFound(op)
	}
	return nil
}

// proveCrashGroup shows the group of a parent crash for the website: the key of the group list is the parent ID
// and at least one crash of the group must name the website.
func (c *Client) proveCrashGroup(ctx context.Context, op string, req requirement, site, parent int) error {
	var answer json.RawMessage
	if err := c.call(ctx, op, req, "CrashAnalytics.getCrashGroups", url.Values{"idSite": {strconv.Itoa(site)}}, &answer); err != nil {
		return err
	}
	trimmed := unwrapValue(answer)
	switch string(trimmed) {
	case "[]", "null", "false", "":
		return crashNotFound(op)
	}
	var keyed map[string]json.RawMessage
	if json.Unmarshal(trimmed, &keyed) != nil {
		return provider.InvalidResponse(op, "Matomo returned an invalid crash group list")
	}
	members, found := keyed[strconv.Itoa(parent)]
	if !found {
		return crashNotFound(op)
	}
	rows, valid := groupMembers(members)
	if !valid {
		return provider.InvalidResponse(op, "Matomo returned an invalid crash group")
	}
	for i := range rows {
		if rows[i].inSite(site) {
			return nil
		}
	}
	return crashNotFound(op)
}

func invokeCrashesSetIgnored(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set crash ignored"
	var args struct {
		IDSite     *int  `json:"idSite"`
		IDLogCrash *int  `json:"idLogCrash"`
		Ignore     *bool `json:"ignore"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkCrashID(args.IDLogCrash); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	ignore := args.Ignore == nil || *args.Ignore
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := crashesRequirement(crashesSetIgnored.ID)
	id := *args.IDLogCrash
	// A crash that is ignored may be missing from the crash list, so stopping to ignore it is proven by the list of
	// ignored crashes instead.
	method := "CrashAnalytics.getAllCrashes"
	if !ignore {
		method = "CrashAnalytics.getIgnoredCrashes"
	}
	if err := client.proveCrashes(ctx, op, req, method, site, []int{id}); err != nil {
		return nil, err
	}
	flag := "0"
	if ignore {
		flag = "1"
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idLogCrash": {strconv.Itoa(id)}, "ignore": {flag}}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "CrashAnalytics.setIgnoreCrash", params, crashSentOnceGet, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the crash", crashSentOnceGet)
	}
	return &CrashIgnoreSet{IDSite: site, IDLogCrash: id, Ignored: ignore}, nil
}

func invokeCrashesMerge(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "merge crashes"
	var args struct {
		IDSite       *int  `json:"idSite"`
		IDLogCrashes []int `json:"idLogCrashes"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if len(args.IDLogCrashes) < 2 || len(args.IDLogCrashes) > maxCrashMerge {
		return nil, invalidArgument("idLogCrashes must hold 2 to 100 crash IDs")
	}
	seen := make([]int, 0, len(args.IDLogCrashes))
	texts := make([]string, 0, len(args.IDLogCrashes))
	for i := range args.IDLogCrashes {
		id := args.IDLogCrashes[i]
		if err := checkCrashID(&id); err != nil {
			return nil, invalidArgument("idLogCrashes must hold positive integer crash IDs from matomo.crashes.list")
		}
		if slices.Contains(seen, id) {
			return nil, invalidArgument("idLogCrashes must name each crash once")
		}
		seen = append(seen, id)
		texts = append(texts, strconv.Itoa(id))
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := crashesRequirement(crashesMerge.ID)
	if err := client.proveCrashes(ctx, op, req, "CrashAnalytics.getAllCrashes", site, seen); err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idLogCrashes": {strings.Join(texts, ",")}}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "CrashAnalytics.mergeCrashes", params, crashSentOnceGroups, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the merge", crashSentOnceGroups)
	}
	return &CrashesMerged{IDSite: site, IDLogCrashes: seen, Merged: true}, nil
}

func invokeCrashesUnmerge(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "unmerge crash group"
	var args struct {
		IDSite     *int `json:"idSite"`
		IDLogCrash *int `json:"idLogCrash"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkCrashID(args.IDLogCrash); err != nil {
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
	req := crashesRequirement(crashesUnmerge.ID)
	id := *args.IDLogCrash
	if err := client.proveCrashGroup(ctx, op, req, site, id); err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idLogCrash": {strconv.Itoa(id)}}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "CrashAnalytics.unmergeCrashGroup", params, crashSentOnceGroups, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the crash group", crashSentOnceGroups)
	}
	return &CrashGroupUnmerged{IDSite: site, IDLogCrash: id, Unmerged: true}, nil
}

func crashChangeOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: crashesSetIgnored, Handler: capability.Handler(invokeCrashesSetIgnored)},
		{Descriptor: crashesMerge, Handler: capability.Handler(invokeCrashesMerge)},
		{Descriptor: crashesUnmerge, Handler: capability.Handler(invokeCrashesUnmerge)},
	}
}
