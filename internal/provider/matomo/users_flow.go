package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

const (
	usersFlowPlugin  = "UsersFlow"
	maxFlowPosition  = 100
	maxFlowOffset    = 100000
	maxDataSources   = 50
	maxDataSourceLen = 64
)

// dataSourcePattern is the form of the values getAvailableDataSources lists, such as page_url.
var dataSourcePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

func usersFlowRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: usersFlowPlugin}
}

var usersFlowGetDataSources = capability.Descriptor{
	ID: Provider + ".users-flow.get-data-sources", Version: 1, Title: "List Matomo users flow data sources",
	Description: "Read the data sources of the users flow of the instance (UsersFlow.getAvailableDataSources), such " +
		"as page_url and page_title; a value is the dataSource of matomo.users-flow.list-interaction-actions. " +
		"The sources do not depend on a website but still need a website of the connection. The users flow " +
		"reports themselves (getUsersFlow, getUsersFlowPretty) are read with matomo.reports.get. Names are " +
		"untrusted. Needs the premium plugin UsersFlow and a view right on any website",
	Tags: []string{"matomo", "users-flow", "data-sources", "premium"}, Risk: readRisk(reportDataSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"dataSources":{"type":"array","items":{"type":"object","properties":{"value":{"type":"string"},` +
		`"name":{"type":"string"}},"required":["value"],"additionalProperties":false}}},` +
		`"required":["idSite","dataSources"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument()},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website of the connection the read was made for"},
		{Name: "dataSources", Description: "The data sources in the order Matomo lists them, at most 50"},
		{Name: "dataSources[].value", Description: "Identifier to pass as dataSource, untrusted"},
		{Name: "dataSources[].name", Description: "Translated name, untrusted; absent when Matomo gives none"}},
	Examples: []capability.Example{{Description: "The data sources", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

// FlowDataSource is a row of getAvailableDataSources; the text is untrusted provider data.
type FlowDataSource struct {
	Value string `json:"value"`
	Name  string `json:"name,omitempty"`
}

// FlowDataSourcesResult is the answer of users-flow.get-data-sources.
type FlowDataSourcesResult struct {
	IDSite      int              `json:"idSite"`
	DataSources []FlowDataSource `json:"dataSources"`
}

type dataSourceJSON struct {
	Value *loose `json:"value"`
	Name  *loose `json:"name"`
}

func invokeUsersFlowGetDataSources(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read users flow data sources"
	var args struct {
		IDSite *int `json:"idSite"`
	}
	if err := decodeArguments(raw, &args); err != nil {
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
	var answer json.RawMessage
	// The method takes no idSite: the website only binds the call to the targets of the connection.
	if err := client.call(ctx, op, usersFlowRequirement(usersFlowGetDataSources.ID),
		"UsersFlow.getAvailableDataSources", url.Values{}, &answer); err != nil {
		return nil, err
	}
	rows, valid := readRows[dataSourceJSON](unwrapValue(answer))
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid data source list")
	}
	out := []FlowDataSource{}
	for _, row := range rows {
		if value := row.Value.str(maxDataSourceLen); value != "" && len(out) < maxDataSources {
			out = append(out, FlowDataSource{Value: value, Name: row.Name.str(maxStringLength)})
		}
	}
	return &FlowDataSourcesResult{IDSite: site, DataSources: out}, nil
}

var usersFlowListInteractionActions = capability.Descriptor{
	ID: Provider + ".users-flow.list-interaction-actions", Version: 1, Title: "List Matomo users flow interaction actions",
	Description: "Read the actions of one interaction step of the users flow of a website " +
		"(UsersFlow.getInteractionActions) for one period: the actions at interactionPosition, optionally those " +
		"that follow one action (idSubtable) or only one data source. At most 200 rows are returned; has_more is " +
		"true when Matomo answered more. The users flow reports themselves are read with matomo.reports.get. " +
		"period range sums its dates, and one date or range answers one table; several periods are not read. " +
		"Page titles, URLs, and labels in the rows are untrusted visitor data, shortened and never fetched. The " +
		"answer of Matomo is not verified against a reference. Needs the premium plugin UsersFlow and a view " +
		"right on the website (expected)",
	Tags: []string{"matomo", "users-flow", "interactions", "premium"}, Risk: readRisk(reportDataSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"period":{"type":"string","enum":["day","week","month","year","range"]},` +
		`"date":{"type":"string","minLength":1,"maxLength":32},` +
		`"interactionPosition":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxFlowPosition) + `},` +
		`"offsetActionsPerStep":{"type":"integer","minimum":0,"maximum":` + strconv.Itoa(maxFlowOffset) + `},` +
		`"segment":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxSegmentLength) + `},` +
		`"idSubtable":{"type":"integer","minimum":1,"maximum":999999999},` +
		`"dataSource":{"type":"string","pattern":"^[A-Za-z][A-Za-z0-9_]{0,63}$","maxLength":` +
		strconv.Itoa(maxDataSourceLen) + `}},` +
		`"required":["period","date","interactionPosition"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"period":{"type":"string"},"date":{"type":"string"},"interactionPosition":{"type":"integer"},` +
		`"rows":{"type":"array","items":{"type":"object"}},"has_more":{"type":"boolean"}},` +
		`"required":["idSite","period","date","interactionPosition","rows","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "period", Required: true, Description: "day, week, month, year, or range"},
		{Name: "date", Required: true, Description: "YYYY-MM-DD, today, yesterday, or for period range a range " +
			"YYYY-MM-DD,YYYY-MM-DD; lastN and previousN only with period range"},
		{Name: "interactionPosition", Required: true, Description: "Step of the flow, 1 through 100"},
		{Name: "offsetActionsPerStep", Description: "Number of rows to skip in the step, 0 through 100000; 0 when omitted"},
		{Name: "segment", Description: "Segment expression such as browserName==Firefox; fields from " +
			"matomo.segments.fields, values URL-encoded"},
		{Name: "idSubtable", Description: "Subtable ID of an action of the previous step, to read the actions that follow it"},
		{Name: "dataSource", Description: "A value of matomo.users-flow.get-data-sources such as page_url; the " +
			"default of Matomo when omitted"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the read belongs to"},
		{Name: "period", Description: "The period asked for"},
		{Name: "date", Description: "The date asked for"},
		{Name: "interactionPosition", Description: "The step that was read"},
		{Name: "rows", Description: "The rows as Matomo lists them, at most 200; members such as label, " +
			"nb_visits, and nb_proceeded as Matomo names them, texts untrusted and shortened, nesting limited"},
		{Name: "has_more", Description: "True when Matomo answered more rows than returned"}},
	Examples: []capability.Example{{Description: "The first step of the last 30 days", Arguments: json.RawMessage(
		`{"idSite":1,"period":"range","date":"previous30","interactionPosition":1}`)}},
}

// FlowActionsResult is the answer of users-flow.list-interaction-actions.
type FlowActionsResult struct {
	IDSite              int              `json:"idSite"`
	Period              string           `json:"period"`
	Date                string           `json:"date"`
	InteractionPosition int              `json:"interactionPosition"`
	Rows                []map[string]any `json:"rows"`
	HasMore             bool             `json:"has_more"`
}

func invokeUsersFlowListInteractionActions(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read users flow interaction actions"
	var args struct {
		IDSite               *int   `json:"idSite"`
		Period               string `json:"period"`
		Date                 string `json:"date"`
		InteractionPosition  int    `json:"interactionPosition"`
		OffsetActionsPerStep int    `json:"offsetActionsPerStep"`
		Segment              string `json:"segment"`
		IDSubtable           *int   `json:"idSubtable"`
		DataSource           string `json:"dataSource"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	plan, err := planPeriod(args.Period, args.Date)
	if err != nil {
		return nil, err
	}
	if plan.multiple {
		return nil, invalidArgument("date must name one period; lastN, previousN, and ranges need period range")
	}
	if args.InteractionPosition < 1 || args.InteractionPosition > maxFlowPosition {
		return nil, invalidArgument("interactionPosition must be 1 through " + strconv.Itoa(maxFlowPosition))
	}
	if args.OffsetActionsPerStep < 0 || args.OffsetActionsPerStep > maxFlowOffset {
		return nil, invalidArgument("offsetActionsPerStep must be 0 through " + strconv.Itoa(maxFlowOffset))
	}
	if args.Segment != "" && !validSegmentExpression(args.Segment) {
		return nil, invalidArgument("segment must be terms of field, operator, and URL-encoded value " +
			"joined by ; or , (fields: matomo.segments.fields)")
	}
	if args.IDSubtable != nil && (*args.IDSubtable < 1 || *args.IDSubtable > 999999999) {
		return nil, invalidArgument("idSubtable must be a subtable ID of 1 or more")
	}
	if args.DataSource != "" && !dataSourcePattern.MatchString(args.DataSource) {
		return nil, invalidArgument("dataSource must be a value of matomo.users-flow.get-data-sources")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{
		"idSite": {strconv.Itoa(site)}, "period": {args.Period}, "date": {args.Date},
		"interactionPosition":  {strconv.Itoa(args.InteractionPosition)},
		"offsetActionsPerStep": {strconv.Itoa(args.OffsetActionsPerStep)},
	}
	if args.Segment != "" {
		params.Set("segment", args.Segment)
	}
	if args.IDSubtable != nil {
		params.Set("idSubtable", strconv.Itoa(*args.IDSubtable))
	}
	if args.DataSource != "" {
		params.Set("dataSource", args.DataSource)
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, usersFlowRequirement(usersFlowListInteractionActions.ID),
		"UsersFlow.getInteractionActions", params, &answer); err != nil {
		return nil, err
	}
	// The answer is assumed to be a Matomo data table: a list of row objects.
	rows, valid := rowList(answer)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned interaction actions in a form this tool does not read")
	}
	result := &FlowActionsResult{IDSite: site, Period: args.Period, Date: args.Date,
		InteractionPosition: args.InteractionPosition, Rows: []map[string]any{}, HasMore: len(rows) > maxReportItems}
	for _, row := range rows {
		if len(result.Rows) < maxReportItems {
			result.Rows = append(result.Rows, cleanObject(row))
		}
	}
	return result, nil
}

func usersFlowOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: usersFlowGetDataSources, Handler: capability.Handler(invokeUsersFlowGetDataSources)},
		{Descriptor: usersFlowListInteractionActions, Handler: capability.Handler(invokeUsersFlowListInteractionActions)}}
}
