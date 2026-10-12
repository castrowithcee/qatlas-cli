package twentycrm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const chartGroupID = "00000000-0000-4000-8000-000000000001"

// fixedChartIDs makes the identifiers of the filter group and its filters predictable.
func fixedChartIDs(t *testing.T) {
	t.Helper()
	previous, n := newChartID, 0
	newChartID = func() string {
		n++
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
	}
	t.Cleanup(func() { newChartID = previous })
}

func chartArgsFor(chartType, field, operation, rest string) string {
	if rest != "" {
		rest = "," + rest
	}
	return widgetCreateArgs(layoutTabID, "GRAPH", `,"chart_type":"`+chartType+`","object_id":"`+metaObjID+
		`","aggregate_field_id":"`+field+`","aggregate_operation":"`+operation+`"`+rest)
}

func chartAnswer(chartType string) string {
	answer := widgetWire(wNewWidget, layoutTabID, "Notes", "GRAPH", metaObjID, wGrid)
	return strings.Replace(answer, `"configuration":{"configurationType":"GRAPH"`,
		`"configuration":{"configurationType":"`+chartType+`"`, 1)
}

func TestPageLayoutWidgetsCreateChartSendsOneCheckedConfiguration(t *testing.T) {
	filterRow := func(id, field, operand, value, kind string) string {
		return `{"id":"` + id + `","fieldMetadataId":"` + field + `","operand":"` + operand + `","value":` + value +
			`,"displayValue":` + value + `,"type":"` + kind + `","recordFilterGroupId":"` + chartGroupID +
			`","positionInRecordFilterGroup":0}`
	}
	filterOf := func(rows string) string {
		return `,"filter":{"recordFilters":[` + rows + `],"recordFilterGroups":[{"id":"` + chartGroupID +
			`","logicalOperator":"AND"}]}`
	}
	for name, tt := range map[string]struct {
		chartType, args, configuration string
	}{
		"aggregate": {chartAggregate, chartArgsFor(chartAggregate, flNumber, "SUM", ""),
			`{"configurationType":"AGGREGATE_CHART","aggregateFieldMetadataId":"` + flNumber +
				`","aggregateOperation":"SUM","displayDataLabel":true}`},
		"bar": {chartBar, chartArgsFor(chartBar, flText, "COUNT", `"group_by_field_id":"`+flSelect+`","bar_layout":"VERTICAL"`),
			`{"configurationType":"BAR_CHART","aggregateFieldMetadataId":"` + flText + `","aggregateOperation":"COUNT",` +
				`"primaryAxisGroupByFieldMetadataId":"` + flSelect + `","layout":"VERTICAL","displayDataLabel":false,` +
				`"displayLegend":true}`},
		"bar stacked with a filter": {chartBar, chartArgsFor(chartBar, flNumber, "AVG", `"group_by_field_id":"`+flDate+
			`","secondary_group_by_field_id":"`+flSelect+`","bar_layout":"HORIZONTAL","bar_group_mode":"STACKED",`+
			`"filters":[{"field_id":"`+flSelect+`","operand":"IS","options":["A"]}]`),
			`{"configurationType":"BAR_CHART","aggregateFieldMetadataId":"` + flNumber + `","aggregateOperation":"AVG",` +
				`"primaryAxisGroupByFieldMetadataId":"` + flDate + `","secondaryAxisGroupByFieldMetadataId":"` + flSelect +
				`","layout":"HORIZONTAL","groupMode":"STACKED","displayDataLabel":false,"displayLegend":true` +
				filterOf(filterRow("00000000-0000-4000-8000-000000000002", flSelect, "IS", `"[\"A\"]"`, "SELECT")) + `}`},
		"line": {chartLine, chartArgsFor(chartLine, flNumber, "SUM", `"group_by_field_id":"`+flDate+
			`","group_by_date_granularity":"MONTH","group_by_order":"FIELD_ASC","color":"blue","display_data_label":true,`+
			`"filters":[{"field_id":"`+flNumber+`","operand":"GREATER_THAN_OR_EQUAL","number":10}]`),
			`{"configurationType":"LINE_CHART","aggregateFieldMetadataId":"` + flNumber + `","aggregateOperation":"SUM",` +
				`"primaryAxisGroupByFieldMetadataId":"` + flDate + `","primaryAxisDateGranularity":"MONTH",` +
				`"primaryAxisOrderBy":"FIELD_ASC","color":"blue","displayDataLabel":true,"displayLegend":true` +
				filterOf(filterRow("00000000-0000-4000-8000-000000000002", flNumber, "GREATER_THAN_OR_EQUAL", `"10"`, "NUMBER")) + `}`},
		"pie": {chartPie, chartArgsFor(chartPie, flBool, "COUNT_TRUE", `"group_by_field_id":"`+flSelect+
			`","group_by_order":"VALUE_DESC","display_legend":false`),
			`{"configurationType":"PIE_CHART","aggregateFieldMetadataId":"` + flBool + `","aggregateOperation":"COUNT_TRUE",` +
				`"groupByFieldMetadataId":"` + flSelect + `","orderBy":"VALUE_DESC","displayDataLabel":true,` +
				`"displayLegend":false,"showCenterMetric":true,"hideEmptyCategory":false}`},
	} {
		t.Run(name, func(t *testing.T) {
			fixedChartIDs(t)
			calls := serveWidgets(t, http.StatusCreated, chartAnswer(tt.chartType))
			result, err := runColumn(invokePageLayoutWidgetsCreate, tt.args)
			if err != nil {
				t.Fatal(err)
			}
			out, _ := json.Marshal(result)
			want := `{"id":"` + wNewWidget + `","tab_id":"` + layoutTabID + `","type":"GRAPH","title":"Notes",` +
				`"position":{"layout_mode":"GRID","row":0,"column":1,"row_span":4,"column_span":6},"chart_type":"` +
				tt.chartType + `"}`
			if string(out) != want {
				t.Errorf("result = %s, want %s", out, want)
			}
			for _, canary := range []string{creatorCn, appCn, "applicationId", "universalIdentifier", "configuration"} {
				if strings.Contains(string(out), canary) {
					t.Errorf("%s reached the output: %s", canary, out)
				}
			}
			body := `{"pageLayoutTabId":"` + layoutTabID + `","title":"Notes","type":"GRAPH","objectMetadataId":"` + metaObjID +
				`","position":` + wGrid + `,"configuration":` + tt.configuration + `}`
			sent := columnWrites(calls)
			if len(sent) != 1 || sent[0].Method != http.MethodPost || sent[0].URI != pageLayoutWidgetsPath || sent[0].Body != body {
				t.Errorf("writes = %+v, want body %s", sent, body)
			}
			if len(*calls) != 4 || (*calls)[2].URI != metadataObjectsPath+"/"+metaObjID {
				t.Errorf("the tab, the layout, and the object are read first: %+v", *calls)
			}
		})
	}
}

func TestPageLayoutWidgetsCreateChartRefusesWithoutAWritingRequest(t *testing.T) {
	bar := func(rest string) string {
		return chartArgsFor(chartBar, flNumber, "SUM", `"group_by_field_id":"`+flSelect+`","bar_layout":"VERTICAL"`+rest)
	}
	line := func(rest string) string {
		return chartArgsFor(chartLine, flNumber, "SUM", `"group_by_field_id":"`+flSelect+`"`+rest)
	}
	pie := func(rest string) string {
		return chartArgsFor(chartPie, flNumber, "SUM", `"group_by_field_id":"`+flSelect+`"`+rest)
	}
	aggregate := func(rest string) string { return chartArgsFor(chartAggregate, flNumber, "SUM", rest) }
	filter := func(field, rest string) string {
		return `,"filters":[{"field_id":"` + field + `",` + rest + `}]`
	}
	many := make([]string, 0, chartFiltersMax+1)
	for i := 0; i <= chartFiltersMax; i++ {
		many = append(many, `{"field_id":"`+flText+`","operand":"IS_EMPTY"}`)
	}
	for name, tt := range map[string]struct {
		args  string
		class provider.Class
		text  string
	}{
		"aggregate with a group":      {aggregate(`"group_by_field_id":"` + flSelect + `"`), "", "chart_type needs"},
		"aggregate with a color":      {aggregate(`"color":"blue"`), "", "chart_type needs"},
		"aggregate with a legend":     {aggregate(`"display_legend":true`), "", "chart_type needs"},
		"aggregate with a layout":     {aggregate(`"bar_layout":"VERTICAL"`), "", "chart_type needs"},
		"bar without a group":         {chartArgsFor(chartBar, flNumber, "SUM", `"bar_layout":"VERTICAL"`), "", "chart_type needs"},
		"bar without a layout":        {chartArgsFor(chartBar, flNumber, "SUM", `"group_by_field_id":"`+flSelect+`"`), "", "chart_type needs"},
		"bar mode without second":     {bar(`,"bar_group_mode":"STACKED"`), "", "chart_type needs"},
		"line without a group":        {chartArgsFor(chartLine, flNumber, "SUM", ""), "", "chart_type needs"},
		"line with a layout":          {line(`,"bar_layout":"VERTICAL"`), "", "chart_type needs"},
		"pie without a group":         {chartArgsFor(chartPie, flNumber, "SUM", ""), "", "chart_type needs"},
		"pie with a second group":     {pie(`,"secondary_group_by_field_id":"` + flText + `"`), "", "chart_type needs"},
		"second order without group":  {bar(`,"secondary_group_by_order":"FIELD_ASC"`), "", "chart_type needs"},
		"second equals first":         {bar(`,"secondary_group_by_field_id":"` + flSelect + `"`), "", "must differ"},
		"granularity on text":         {chartArgsFor(chartBar, flNumber, "SUM", `"group_by_field_id":"`+flText+`","group_by_date_granularity":"DAY","bar_layout":"VERTICAL"`), "", "DATE or DATE_TIME"},
		"unknown color":               {line(`,"color":"chartreuse"`), "", "color"},
		"unknown chart type":          {chartArgsFor("GAUGE_CHART", flNumber, "SUM", ""), "", "chart_type"},
		"chart type missing":          {widgetCreateArgs(layoutTabID, "GRAPH", `,"object_id":"`+metaObjID+`"`), "", "type"},
		"sum on text":                 {chartArgsFor(chartAggregate, flText, "SUM", ""), "", "not offered for a field of type TEXT"},
		"true count on number":        {chartArgsFor(chartAggregate, flNumber, "COUNT_TRUE", ""), "", "not offered"},
		"min on a date":               {chartArgsFor(chartAggregate, flDate, "MIN", ""), "", "not offered"},
		"unknown operation":           {chartArgsFor(chartAggregate, flNumber, "MEDIAN", ""), "", "not offered"},
		"aggregate on a relation":     {chartArgsFor(chartAggregate, flRel, "COUNT", ""), "", "aggregate_field_id"},
		"aggregate of another object": {chartArgsFor(chartAggregate, flOther, "COUNT", ""), "", "aggregate_field_id"},
		"inactive aggregate field":    {chartArgsFor(chartAggregate, flOff, "COUNT", ""), "", "aggregate_field_id"},
		"system aggregate field":      {chartArgsFor(chartAggregate, flSystem, "COUNT", ""), "", "aggregate_field_id"},
		"group of another object":     {chartArgsFor(chartPie, flNumber, "SUM", `"group_by_field_id":"`+flOther+`"`), "", "group_by_field_id"},
		"group by a relation":         {chartArgsFor(chartPie, flNumber, "SUM", `"group_by_field_id":"`+flRel+`"`), "", "group_by_field_id"},
		"second of another object":    {bar(`,"secondary_group_by_field_id":"` + flOther + `"`), "", "secondary_group_by_field_id"},
		"filter of another object":    {aggregate(filter(flOther, `"operand":"IS_EMPTY"`)[1:]), "", "a filter field"},
		"filter on a system field":    {aggregate(filter(flSystem, `"operand":"IS_EMPTY"`)[1:]), "", "a filter field"},
		"operand not for the type":    {aggregate(filter(flNumber, `"operand":"CONTAINS","text":"x"`)[1:]), "", "not supported for a field of type NUMBER"},
		"value not for the operand":   {aggregate(filter(flText, `"operand":"CONTAINS","number":1`)[1:]), "", "takes exactly one value"},
		"option not in the schema":    {aggregate(filter(flSelect, `"operand":"IS","options":["Z"]`)[1:]), "", "options of this field"},
		"two values":                  {aggregate(filter(flText, `"operand":"CONTAINS","text":"x","flag":true`)[1:]), "", "at most one value"},
		"too many filters":            {aggregate(`"filters":[` + strings.Join(many, ",") + `]`), "", "at most"},
		"object not a uuid":           {strings.Replace(aggregate(""), metaObjID, "nope", 1), "", "object_id"},
		"unknown object":              {strings.Replace(aggregate(""), metaObjID, metaOtherID, 1), provider.ClassNotFound, ""},
		"system object":               {strings.Replace(aggregate(""), metaObjID, wObjectSystem, 1), "", "object_id"},
		"chart with markdown":         {aggregate(`"markdown":"x"`), "", "type"},
		"chart with a view":           {aggregate(`"view_id":"` + colView + `"`), "", "type"},
		"text with chart arguments":   {widgetCreateArgs(layoutTabID, "STANDALONE_RICH_TEXT", `,"markdown":"x","chart_type":"PIE_CHART"`), "", "type"},
		"table with a chart color":    {widgetCreateArgs(layoutTabID, "RECORD_TABLE", `,"view_id":"`+colView+`","color":"blue"`), "", "type"},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveWidgets(t, http.StatusCreated, chartAnswer(chartAggregate))
			_, err := runColumn(invokePageLayoutWidgetsCreate, tt.args)
			if err == nil || len(columnWrites(calls)) != 0 {
				t.Fatalf("err = %v, writes = %+v", err, columnWrites(calls))
			}
			if tt.class != "" && classOf(err) != tt.class {
				t.Errorf("class = %q, err = %v", classOf(err), err)
			}
			if tt.class == "" && (!asInvalidOK(err) || !strings.Contains(err.Error(), tt.text)) {
				t.Errorf("err = %v, want invalid-request with %q", err, tt.text)
			}
			if strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("provider text in the error: %v", err)
			}
		})
	}
}

func TestPageLayoutWidgetsCreateChartAnswerMustCarryTheRequestedChart(t *testing.T) {
	for name, answer := range map[string]string{
		"other chart":    chartAnswer(chartPie),
		"no chart":       widgetWire(wNewWidget, layoutTabID, "Notes", "GRAPH", metaObjID, wGrid),
		"other object":   strings.Replace(chartAnswer(chartAggregate), metaObjID, metaOtherID, 1),
		"not a graph":    widgetWire(wNewWidget, layoutTabID, "Notes", "STANDALONE_RICH_TEXT", metaObjID, wGrid),
		"another widget": widgetWire(wNewWidget, wTabOther, "Notes", "GRAPH", metaObjID, wGrid),
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveWidgets(t, http.StatusCreated, answer)
			_, err := runColumn(invokePageLayoutWidgetsCreate, chartArgsFor(chartAggregate, flNumber, "SUM", ""))
			if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "before repeating") ||
				len(columnWrites(calls)) != 1 {
				t.Errorf("err = %v, writes = %d", err, len(columnWrites(calls)))
			}
		})
	}
}

func TestPageLayoutWidgetsCreateChartNeedsAConnectionWithoutObjectTargets(t *testing.T) {
	refuse(t)
	_, err := invokePageLayoutWidgetsCreate(context.Background(), targetConnection("object/person"), countingResolver(t),
		&redact.Redactor{}, json.RawMessage(chartArgsFor(chartAggregate, flNumber, "SUM", "")))
	if !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
		t.Errorf("err = %v", err)
	}
}

func TestPageLayoutWidgetsCreateChartInputSchemaHoldsTheSubset(t *testing.T) {
	for name, tt := range map[string]struct {
		args string
		ok   bool
	}{
		"valid line chart": {chartArgsFor(chartLine, flNumber, "SUM", `"group_by_field_id":"`+flDate+
			`","color":"blue","filters":[{"field_id":"`+flText+`","operand":"CONTAINS","text":"x"}]`), true},
		"option beyond the subset":    {chartArgsFor(chartLine, flNumber, "SUM", `"group_by_field_id":"`+flDate+`","legend_position":"top"`), false},
		"color beyond the list":       {chartArgsFor(chartLine, flNumber, "SUM", `"group_by_field_id":"`+flDate+`","color":"#ff0000"`), false},
		"order beyond the list":       {chartArgsFor(chartLine, flNumber, "SUM", `"group_by_field_id":"`+flDate+`","group_by_order":"MANUAL"`), false},
		"configuration passthrough":   {chartArgsFor(chartLine, flNumber, "SUM", `"configuration":{"filter":{}}`), false},
		"filter with another member":  {chartArgsFor(chartLine, flNumber, "SUM", `"filters":[{"field_id":"`+flText+`","operand":"IS_EMPTY","value":"x"}]`), false},
		"filter without an operand":   {chartArgsFor(chartLine, flNumber, "SUM", `"filters":[{"field_id":"`+flText+`"}]`), false},
		"empty filter list":           {chartArgsFor(chartLine, flNumber, "SUM", `"filters":[]`), false},
		"unknown aggregate operation": {chartArgsFor(chartLine, flNumber, "MEDIAN", ""), false},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveWidgets(t, http.StatusCreated, chartAnswer(chartLine))
			cfg := coreConfig()
			all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete}
			cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
				Permissions: all, Tools: []string{pagelayoutwidgetsCreate.ID}}
			stubLimiter(t, internalKey)
			red := &redact.Redactor{}
			core := application.New(registry(t), cfg, resolver(red), red)
			_, err := core.Invoke(context.Background(), application.InvokeRequest{Operation: pagelayoutwidgetsCreate.ID,
				Connection: "crm-internal", Arguments: json.RawMessage(tt.args), Confirmed: true})
			if tt.ok && (err != nil || len(columnWrites(calls)) != 1) {
				t.Errorf("err = %v, writes = %+v", err, columnWrites(calls))
			}
			if !tt.ok && (err == nil || len(*calls) != 0) {
				t.Errorf("err = %v, requests = %+v", err, *calls)
			}
		})
	}
}
