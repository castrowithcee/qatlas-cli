package twentycrm

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	widgetTypeGraph = "GRAPH"

	chartAggregate = "AGGREGATE_CHART"
	chartBar       = "BAR_CHART"
	chartLine      = "LINE_CHART"
	chartPie       = "PIE_CHART"

	chartFiltersMax = 10

	errChartObject      = "object_id must name an active object of the workspace that is not a system object"
	errChartFilterField = "a filter field must be an active, non-system field of the object with a type that a filter " +
		"can be written for"
	errChartShape = "chart_type needs its own arguments: AGGREGATE_CHART takes none beyond aggregate field and " +
		"operation, BAR_CHART needs group_by_field_id and bar_layout, LINE_CHART and PIE_CHART need " +
		"group_by_field_id; secondary grouping is for BAR_CHART and LINE_CHART, bar_group_mode needs a secondary " +
		"grouping, color and display_legend are not for AGGREGATE_CHART, and a granularity needs a date field"
)

// The enums are those of the chart configuration of Twenty (twenty-server dtos and enums of page-layout-widget,
// twenty-shared AggregateOperations and ObjectRecordGroupByDateGranularity). Qatlas offers only the sort orders that
// do not depend on the type of the group field, and the colors of the dashboard tool of Twenty.
var (
	chartAggregateOperations = []string{"COUNT", "COUNT_UNIQUE_VALUES", "COUNT_EMPTY", "COUNT_NOT_EMPTY",
		"PERCENTAGE_EMPTY", "PERCENTAGE_NOT_EMPTY", "MIN", "MAX", "AVG", "SUM", "COUNT_TRUE", "COUNT_FALSE"}
	chartGranularities = []string{"DAY", "WEEK", "MONTH", "QUARTER", "YEAR", "DAY_OF_THE_WEEK", "MONTH_OF_THE_YEAR",
		"QUARTER_OF_THE_YEAR"}
	chartOrders       = []string{"FIELD_ASC", "FIELD_DESC", "VALUE_ASC", "VALUE_DESC"}
	chartBarLayouts   = []string{"VERTICAL", "HORIZONTAL"}
	chartBarGroupMode = []string{"STACKED", "GROUPED"}
	chartColors       = []string{"auto", "red", "ruby", "crimson", "tomato", "orange", "amber", "yellow", "lime", "grass",
		"green", "jade", "mint", "turquoise", "cyan", "sky", "blue", "iris", "violet", "purple", "plum", "pink", "bronze",
		"gold", "brown", "gray"}

	// chartGroupTypes are the scalar field types a chart can group by without a sub field.
	chartGroupTypes = []string{"TEXT", "SELECT", "MULTI_SELECT", "BOOLEAN", "NUMBER", "RATING", "DATE", "DATE_TIME"}
	chartDateTypes  = []string{"DATE", "DATE_TIME"}

	chartCountOperations   = []string{"COUNT", "COUNT_UNIQUE_VALUES", "COUNT_EMPTY", "COUNT_NOT_EMPTY", "PERCENTAGE_EMPTY", "PERCENTAGE_NOT_EMPTY"}
	chartNumberOperations  = []string{"MIN", "MAX", "AVG", "SUM"}
	chartRatingOperations  = []string{"MIN", "MAX", "AVG"}
	chartBooleanOperations = []string{"COUNT_TRUE", "COUNT_FALSE"}
)

// chartAggregateOperationsFor lists the operations Twenty offers for a field of the type (its aggregate menu of
// the record table); a date field takes only the counting operations because Twenty names the earliest and the
// latest date differently there.
func chartAggregateOperationsFor(fieldType string) []string {
	if !filterFieldSupported(fieldType) {
		return nil
	}
	operations := append([]string(nil), chartCountOperations...)
	switch fieldType {
	case "NUMBER", "CURRENCY":
		operations = append(operations, chartNumberOperations...)
	case "RATING":
		operations = append(operations, chartRatingOperations...)
	case "BOOLEAN":
		operations = append(operations, chartBooleanOperations...)
	}
	return operations
}

const chartFilterSchema = `{"type":"array","minItems":1,"maxItems":10,"items":{"type":"object","properties":{` +
	`"field_id":` + uuidSchema + `,"operand":` + `OPERANDS` + `,` + viewFilterValueProperties + `},` +
	`"required":["field_id","operand"],"additionalProperties":false}}`

// chartInputProperties are the chart members of the create input schema.
var chartInputProperties = `"chart_type":` + enumSchema(chartTypes) + `,"object_id":` + uuidSchema +
	`,"aggregate_field_id":` + uuidSchema + `,"aggregate_operation":` + enumSchema(chartAggregateOperations) +
	`,"group_by_field_id":` + uuidSchema + `,"group_by_date_granularity":` + enumSchema(chartGranularities) +
	`,"group_by_order":` + enumSchema(chartOrders) + `,"secondary_group_by_field_id":` + uuidSchema +
	`,"secondary_group_by_date_granularity":` + enumSchema(chartGranularities) +
	`,"secondary_group_by_order":` + enumSchema(chartOrders) + `,"bar_layout":` + enumSchema(chartBarLayouts) +
	`,"bar_group_mode":` + enumSchema(chartBarGroupMode) + `,"filters":` +
	strings.Replace(chartFilterSchema, "OPERANDS", enumSchema(filterOperands), 1) +
	`,"display_data_label":{"type":"boolean"},"display_legend":{"type":"boolean"},"color":` + enumSchema(chartColors)

// chartUpdateInputProperties are the chart members of the update input schema: the object stays that of the widget.
var chartUpdateInputProperties = strings.Replace(chartInputProperties, `"object_id":`+uuidSchema+`,`, "", 1)

// chartUpdateArguments are the chart arguments of an update, without the object.
var chartUpdateArguments = func() []capability.Argument {
	var arguments []capability.Argument
	for _, argument := range chartArguments {
		if argument.Name != "object_id" {
			arguments = append(arguments, argument)
		}
	}
	return arguments
}()

var chartArguments = []capability.Argument{
	{Name: "chart_type", Description: "Only for GRAPH and required there: AGGREGATE_CHART (one number), BAR_CHART, " +
		"LINE_CHART, or PIE_CHART"},
	{Name: "object_id", Description: "Only for GRAPH and required there: UUID of the active, non-system object whose " +
		"records feed the chart, as returned by twentycrm.metaobjects.list; every field below belongs to it"},
	{Name: "aggregate_field_id", Description: "Only for GRAPH and required there: UUID of the field to aggregate, as " +
		"returned by twentycrm.metaobjects.get; active, not a system field, and of a type a filter can be written for " +
		"(COUNT takes any such field)"},
	{Name: "aggregate_operation", Description: "Only for GRAPH and required there: one of " +
		strings.Join(chartAggregateOperations, ", ") + ". Every field type takes COUNT, COUNT_UNIQUE_VALUES, " +
		"COUNT_EMPTY, COUNT_NOT_EMPTY, PERCENTAGE_EMPTY, and PERCENTAGE_NOT_EMPTY; NUMBER and CURRENCY also MIN, MAX, " +
		"AVG, and SUM; RATING also MIN, MAX, and AVG; BOOLEAN also COUNT_TRUE and COUNT_FALSE"},
	{Name: "group_by_field_id", Description: "Required for BAR_CHART, LINE_CHART, and PIE_CHART, not for " +
		"AGGREGATE_CHART: UUID of the field that groups the chart (the primary axis of a bar or line chart, the slices " +
		"of a pie chart); type " + strings.Join(chartGroupTypes, ", ")},
	{Name: "group_by_date_granularity", Description: "Only with a DATE or DATE_TIME group field: one of " +
		strings.Join(chartGranularities, ", ") + "; DAY when omitted"},
	{Name: "group_by_order", Description: "Sort order of the groups: one of " + strings.Join(chartOrders, ", ")},
	{Name: "secondary_group_by_field_id", Description: "Only for BAR_CHART and LINE_CHART: UUID of a second grouping " +
		"field, other than the first, of the same types"},
	{Name: "secondary_group_by_date_granularity", Description: "Only with a DATE or DATE_TIME secondary group field; " +
		"the values of group_by_date_granularity"},
	{Name: "secondary_group_by_order", Description: "Only with a secondary group field: the values of group_by_order"},
	{Name: "bar_layout", Description: "Required for BAR_CHART, not for others: VERTICAL or HORIZONTAL"},
	{Name: "bar_group_mode", Description: "Only for BAR_CHART with a secondary group field: STACKED or GROUPED"},
	{Name: "filters", Description: "Up to 10 simple filters that restrict the records of the chart, combined with AND, " +
		"each with field_id, operand, and one value argument, checked like twentycrm.viewfilters.create: " + viewFilterNote},
	{Name: "display_data_label", Description: "Show the values on the chart, true or false; Twenty's default when omitted"},
	{Name: "display_legend", Description: "Only for BAR_CHART, LINE_CHART, and PIE_CHART: show the legend, true or false; " +
		"true when omitted"},
	{Name: "color", Description: "Only for BAR_CHART, LINE_CHART, and PIE_CHART: one of " + strings.Join(chartColors, ", ")},
}

// chartArgs are the chart arguments of a widget create, as the agent gave them.
type chartArgs struct {
	ChartType                       string           `json:"chart_type"`
	ObjectID                        string           `json:"object_id"`
	AggregateFieldID                string           `json:"aggregate_field_id"`
	AggregateOperation              string           `json:"aggregate_operation"`
	GroupByFieldID                  string           `json:"group_by_field_id"`
	GroupByDateGranularity          string           `json:"group_by_date_granularity"`
	GroupByOrder                    string           `json:"group_by_order"`
	SecondaryGroupByFieldID         string           `json:"secondary_group_by_field_id"`
	SecondaryGroupByDateGranularity string           `json:"secondary_group_by_date_granularity"`
	SecondaryGroupByOrder           string           `json:"secondary_group_by_order"`
	BarLayout                       string           `json:"bar_layout"`
	BarGroupMode                    string           `json:"bar_group_mode"`
	Filters                         []chartFilterArg `json:"filters"`
	DisplayDataLabel                *bool            `json:"display_data_label"`
	DisplayLegend                   *bool            `json:"display_legend"`
	Color                           string           `json:"color"`
}

type chartFilterArg struct {
	FieldID string `json:"field_id"`
	Operand string `json:"operand"`
	FilterValue
}

// given reports whether any chart argument is present.
func (a chartArgs) given() bool {
	for _, text := range []string{a.ChartType, a.ObjectID, a.AggregateFieldID, a.AggregateOperation, a.GroupByFieldID,
		a.GroupByDateGranularity, a.GroupByOrder, a.SecondaryGroupByFieldID, a.SecondaryGroupByDateGranularity,
		a.SecondaryGroupByOrder, a.BarLayout, a.BarGroupMode, a.Color} {
		if text != "" {
			return true
		}
	}
	return a.Filters != nil || a.DisplayDataLabel != nil || a.DisplayLegend != nil
}

// chartConfiguration is the whole configuration of a chart; Qatlas builds every member. Time zone and first day of
// the week are never sent, so Twenty keeps its defaults.
type chartConfiguration struct {
	ConfigurationType string `json:"configurationType"`

	AggregateFieldMetadataID string `json:"aggregateFieldMetadataId"`
	AggregateOperation       string `json:"aggregateOperation"`

	// Bar and line charts group by a primary and an optional secondary axis, a pie chart by one field.
	PrimaryAxisGroupByFieldMetadataID   string `json:"primaryAxisGroupByFieldMetadataId,omitempty"`
	PrimaryAxisDateGranularity          string `json:"primaryAxisDateGranularity,omitempty"`
	PrimaryAxisOrderBy                  string `json:"primaryAxisOrderBy,omitempty"`
	SecondaryAxisGroupByFieldMetadataID string `json:"secondaryAxisGroupByFieldMetadataId,omitempty"`
	SecondaryAxisGroupByDateGranularity string `json:"secondaryAxisGroupByDateGranularity,omitempty"`
	SecondaryAxisOrderBy                string `json:"secondaryAxisOrderBy,omitempty"`
	GroupByFieldMetadataID              string `json:"groupByFieldMetadataId,omitempty"`
	DateGranularity                     string `json:"dateGranularity,omitempty"`
	OrderBy                             string `json:"orderBy,omitempty"`

	Layout    string `json:"layout,omitempty"`
	GroupMode string `json:"groupMode,omitempty"`
	Color     string `json:"color,omitempty"`

	DisplayDataLabel  bool  `json:"displayDataLabel"`
	DisplayLegend     *bool `json:"displayLegend,omitempty"`
	ShowCenterMetric  *bool `json:"showCenterMetric,omitempty"`
	HideEmptyCategory *bool `json:"hideEmptyCategory,omitempty"`

	Filter *chartFilter `json:"filter,omitempty"`
}

type chartFilter struct {
	RecordFilters      []chartRecordFilter      `json:"recordFilters"`
	RecordFilterGroups []chartRecordFilterGroup `json:"recordFilterGroups"`
}

// chartRecordFilter has the shape Twenty's own dashboard tool writes: the value as a string next to its display
// form, the field type, and the place in the root group.
type chartRecordFilter struct {
	ID                          string `json:"id"`
	FieldMetadataID             string `json:"fieldMetadataId"`
	Operand                     string `json:"operand"`
	Value                       string `json:"value"`
	DisplayValue                string `json:"displayValue"`
	Type                        string `json:"type"`
	RecordFilterGroupID         string `json:"recordFilterGroupId"`
	PositionInRecordFilterGroup int    `json:"positionInRecordFilterGroup"`
}

type chartRecordFilterGroup struct {
	ID              string `json:"id"`
	LogicalOperator string `json:"logicalOperator"`
}

// newChartID makes the identifiers of a filter and its group; tests replace it.
var newChartID = func() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// chartField returns the field of the object with the id when it is active, not a system field, and of an
// allowed type.
func chartField(object *MetaObject, id string, allowed func(string) bool) *MetaField {
	for i := range object.Fields {
		field := &object.Fields[i]
		if equalUUID(field.ID, id) {
			if !isTrue(field.IsActive) || isTrue(field.IsSystem) || !allowed(field.Type) {
				return nil
			}
			return field
		}
	}
	return nil
}

// checkChartAxis checks one grouping: its field, an optional granularity that needs a date field, and an order.
func checkChartAxis(object *MetaObject, role, fieldID, granularity, order string) (*MetaField, error) {
	if !validUUID(fieldID) {
		return nil, invalidRequest(role + "_field_id must be a UUID")
	}
	field := chartField(object, fieldID, func(t string) bool { return containsName(chartGroupTypes, t) })
	if field == nil {
		return nil, invalidRequest(role + "_field_id is not an active, non-system field of the object with a type " +
			"that can group a chart: " + strings.Join(chartGroupTypes, ", "))
	}
	if granularity != "" && !containsName(chartDateTypes, field.Type) {
		return nil, invalidRequest(role + "_date_granularity needs a DATE or DATE_TIME group field")
	}
	return field, nil
}

// buildChartConfiguration checks the chart arguments against the object schema and builds the configuration the
// way the server validates it: aggregate field and operation, the grouping its type needs, and the filters. It
// never touches the network, so every refusal comes before a write.
func buildChartConfiguration(object *MetaObject, a chartArgs) (*chartConfiguration, error) {
	if !containsName(chartTypes, a.ChartType) {
		return nil, invalidRequest(errChartShape)
	}
	if !validUUID(a.AggregateFieldID) {
		return nil, invalidRequest("aggregate_field_id must be a UUID")
	}
	aggregate := chartField(object, a.AggregateFieldID, filterFieldSupported)
	if aggregate == nil {
		return nil, invalidRequest("aggregate_field_id is not an active, non-system field of the object with a type " +
			"a filter can be written for")
	}
	if !containsName(chartAggregateOperationsFor(aggregate.Type), a.AggregateOperation) {
		return nil, invalidRequest("the operation " + a.AggregateOperation + " is not offered for a field of type " +
			aggregate.Type + "; see the operations by field type in the argument aggregate_operation")
	}
	config := &chartConfiguration{ConfigurationType: a.ChartType, AggregateFieldMetadataID: strings.ToLower(aggregate.ID),
		AggregateOperation: a.AggregateOperation, DisplayDataLabel: a.ChartType == chartAggregate || a.ChartType == chartPie}
	if a.DisplayDataLabel != nil {
		config.DisplayDataLabel = *a.DisplayDataLabel
	}

	grouped := a.ChartType != chartAggregate
	secondary := a.ChartType == chartBar || a.ChartType == chartLine
	hasSecondary := a.SecondaryGroupByFieldID != ""
	switch {
	case !grouped && (a.GroupByFieldID != "" || a.GroupByDateGranularity != "" || a.GroupByOrder != "" ||
		hasSecondary || a.SecondaryGroupByDateGranularity != "" || a.SecondaryGroupByOrder != "" ||
		a.BarLayout != "" || a.BarGroupMode != "" || a.Color != "" || a.DisplayLegend != nil),
		grouped && a.GroupByFieldID == "",
		!secondary && (hasSecondary || a.SecondaryGroupByDateGranularity != "" || a.SecondaryGroupByOrder != ""),
		!hasSecondary && (a.SecondaryGroupByDateGranularity != "" || a.SecondaryGroupByOrder != "" || a.BarGroupMode != ""),
		a.ChartType == chartBar && a.BarLayout == "",
		a.ChartType != chartBar && (a.BarLayout != "" || a.BarGroupMode != ""):
		return nil, invalidRequest(errChartShape)
	}
	if a.Color != "" && !containsName(chartColors, a.Color) {
		return nil, invalidRequest("color must be one of " + strings.Join(chartColors, ", "))
	}

	if grouped {
		primary, err := checkChartAxis(object, "group_by", a.GroupByFieldID, a.GroupByDateGranularity, a.GroupByOrder)
		if err != nil {
			return nil, err
		}
		legend := true
		if a.DisplayLegend != nil {
			legend = *a.DisplayLegend
		}
		config.DisplayLegend, config.Color = &legend, a.Color
		primaryID := strings.ToLower(primary.ID)
		if a.ChartType == chartPie {
			config.GroupByFieldMetadataID, config.DateGranularity, config.OrderBy = primaryID, a.GroupByDateGranularity,
				a.GroupByOrder
			centre, empty := true, false
			config.ShowCenterMetric, config.HideEmptyCategory = &centre, &empty
		} else {
			config.PrimaryAxisGroupByFieldMetadataID, config.PrimaryAxisDateGranularity, config.PrimaryAxisOrderBy =
				primaryID, a.GroupByDateGranularity, a.GroupByOrder
		}
		if hasSecondary {
			second, err := checkChartAxis(object, "secondary_group_by", a.SecondaryGroupByFieldID,
				a.SecondaryGroupByDateGranularity, a.SecondaryGroupByOrder)
			if err != nil {
				return nil, err
			}
			if equalUUID(second.ID, primary.ID) {
				return nil, invalidRequest("secondary_group_by_field_id must differ from group_by_field_id")
			}
			config.SecondaryAxisGroupByFieldMetadataID = strings.ToLower(second.ID)
			config.SecondaryAxisGroupByDateGranularity, config.SecondaryAxisOrderBy = a.SecondaryGroupByDateGranularity,
				a.SecondaryGroupByOrder
		}
		config.Layout, config.GroupMode = a.BarLayout, a.BarGroupMode
	}

	filter, err := buildChartFilter(object, a.Filters)
	if err != nil {
		return nil, err
	}
	config.Filter = filter
	return config, nil
}

// buildChartFilter checks each filter like a view filter, by field type, operand, and value, and puts them in one
// AND group. Nothing is built for no filters.
func buildChartFilter(object *MetaObject, filters []chartFilterArg) (*chartFilter, error) {
	if len(filters) == 0 {
		return nil, nil
	}
	if len(filters) > chartFiltersMax {
		return nil, invalidRequest("filters holds at most " + strconv.Itoa(chartFiltersMax) + " filters")
	}
	group := chartRecordFilterGroup{ID: newChartID(), LogicalOperator: "AND"}
	result := &chartFilter{RecordFilterGroups: []chartRecordFilterGroup{group}}
	for i, filter := range filters {
		if !validUUID(filter.FieldID) {
			return nil, invalidRequest("a filter field_id must be a UUID")
		}
		field := filterFieldOf(object, filter.FieldID)
		if field == nil {
			return nil, invalidRequest(errChartFilterField)
		}
		if filter.Operand == "" {
			return nil, invalidRequest(errViewFilterNothing)
		}
		if filter.count() > 1 {
			return nil, invalidRequest("at most one value argument is allowed per filter")
		}
		stored, err := checkFilterValue(field.Type, field.Options, filter.Operand, filter.FilterValue)
		if err != nil {
			return nil, err
		}
		result.RecordFilters = append(result.RecordFilters, chartRecordFilter{ID: newChartID(),
			FieldMetadataID: strings.ToLower(field.ID), Operand: filter.Operand, Value: stored, DisplayValue: stored,
			Type: field.Type, RecordFilterGroupID: group.ID, PositionInRecordFilterGroup: i})
	}
	return result, nil
}

// chartConfigurationType reads only the type out of the configuration of an answer.
func chartConfigurationType(raw json.RawMessage) string {
	var configuration struct {
		ConfigurationType string `json:"configurationType"`
	}
	if json.Unmarshal(raw, &configuration) != nil || !containsName(chartTypes, configuration.ConfigurationType) {
		return ""
	}
	return configuration.ConfigurationType
}
