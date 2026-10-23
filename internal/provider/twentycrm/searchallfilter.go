package twentycrm

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Bounds of the structured filter of the cross-object search. Twenty offers it only on the fields every
// object has: id, createdAt, and updatedAt; deletedAt is never filterable here.
const (
	searchAllConditionsMax = 5
	searchAllValuesMax     = 50
	searchAllExcludeMax    = 50

	errSearchAllFilter = "conditions must be 1 to 5 entries of a field, an operator, and a value that fits the field and operator"
	errSearchAllMatch  = "match needs conditions and must be all or any"
)

const searchAllConditionSchema = `{"type":"object","properties":{` +
	`"field":{"type":"string","enum":["id","createdAt","updatedAt"]},` +
	`"operator":{"type":"string","enum":["eq","neq","gt","gte","lt","lte","in","not_in","is"]},` +
	`"value":{"anyOf":[{"type":"string","minLength":1,"maxLength":40},` +
	`{"type":"array","minItems":1,"maxItems":50,"items":{"type":"string","minLength":1,"maxLength":40}}]}},` +
	`"required":["field","operator","value"],"additionalProperties":false}`

// searchAllCondition is one checked condition; Values are canonical (UUIDs lower case, instants in UTC,
// lists sorted without repeats) so that equal conditions read alike.
type searchAllCondition struct {
	Field    string
	Operator string
	List     bool
	Values   []string
}

type searchAllConditionArgument struct {
	Field    string          `json:"field"`
	Operator string          `json:"operator"`
	Value    json.RawMessage `json:"value"`
}

var (
	searchAllFields    = []string{"id", "createdAt", "updatedAt"}
	searchAllOperators = []string{"eq", "neq", "gt", "gte", "lt", "lte", "in", "not_in", "is"}
)

// newSearchAllFilter checks conditions and match and returns the normalized conditions, the GraphQL filter
// (nil without conditions), and the match that counts: a single condition reads alike with all and any.
func newSearchAllFilter(args []searchAllConditionArgument, match string) ([]searchAllCondition, map[string]any, string, error) {
	if args == nil {
		if match != "" {
			return nil, nil, "", invalidRequest(errSearchAllMatch)
		}
		return nil, nil, "", nil
	}
	if len(args) < 1 || len(args) > searchAllConditionsMax || (match != "" && match != "all" && match != "any") {
		return nil, nil, "", invalidRequest(errSearchAllFilter)
	}
	conditions := make([]searchAllCondition, 0, len(args))
	seen := map[string]bool{}
	for _, arg := range args {
		cond, ok := normalizeSearchAllCondition(arg)
		if !ok {
			return nil, nil, "", invalidRequest(errSearchAllFilter)
		}
		if key := searchAllConditionKey(cond); !seen[key] {
			seen[key] = true
			conditions = append(conditions, cond)
		}
	}
	sort.Slice(conditions, func(i, j int) bool {
		return searchAllConditionKey(conditions[i]) < searchAllConditionKey(conditions[j])
	})
	link := "and"
	if match == "any" && len(conditions) > 1 {
		link = "or"
	}
	parts := make([]any, len(conditions))
	for i, cond := range conditions {
		parts[i] = cond.filter()
	}
	return conditions, map[string]any{link: parts}, link, nil
}

func normalizeSearchAllCondition(arg searchAllConditionArgument) (searchAllCondition, bool) {
	if !containsName(searchAllFields, arg.Field) || !containsName(searchAllOperators, arg.Operator) {
		return searchAllCondition{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(arg.Value))
	var value any
	if decoder.Decode(&value) != nil || decoder.More() {
		return searchAllCondition{}, false
	}
	list := arg.Operator == "in" || arg.Operator == "not_in"
	var items []any
	if values, isList := value.([]any); isList {
		if !list || len(values) == 0 || len(values) > searchAllValuesMax {
			return searchAllCondition{}, false
		}
		items = values
	} else if list {
		return searchAllCondition{}, false
	} else {
		items = []any{value}
	}
	cond := searchAllCondition{Field: arg.Field, Operator: arg.Operator, List: list}
	seen := map[string]bool{}
	for _, item := range items {
		text, isText := item.(string)
		if !isText {
			return searchAllCondition{}, false
		}
		text, ok := canonicalSearchAllValue(arg.Field, arg.Operator, text)
		if !ok {
			return searchAllCondition{}, false
		}
		if !seen[text] {
			seen[text] = true
			cond.Values = append(cond.Values, text)
		}
	}
	sort.Strings(cond.Values)
	return cond, true
}

// canonicalSearchAllValue checks one value against the field and operator. The comparators gt to lte are
// offered by Twenty's UUID filter as well as by its date-time filter.
func canonicalSearchAllValue(field, operator, text string) (string, bool) {
	switch {
	case operator == "is":
		return text, text == "NULL" || text == "NOT_NULL"
	case field == "id":
		return strings.ToLower(text), validUUID(text)
	}
	if _, err := checkFilterDate(filterInstant, text); err != nil {
		return "", false
	}
	instant, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return "", false
	}
	return instant.UTC().Format(time.RFC3339Nano), true
}

func (c searchAllCondition) filter() map[string]any {
	var operand any = c.Values[0]
	if c.List {
		operand = c.Values
	}
	if c.Operator == "not_in" {
		return map[string]any{"not": map[string]any{c.Field: map[string]any{"in": operand}}}
	}
	return map[string]any{c.Field: map[string]any{c.Operator: operand}}
}

func searchAllConditionKey(c searchAllCondition) string {
	return c.Field + "\x00" + c.Operator + "\x00" + strings.Join(c.Values, "\x01")
}

// searchAllFilterKey is the cursor binding part of the normalized filter.
func searchAllFilterKey(conditions []searchAllCondition, link string, excluded []string) []string {
	keys := make([]string, len(conditions))
	for i, cond := range conditions {
		keys[i] = searchAllConditionKey(cond)
	}
	return []string{link, strings.Join(keys, "\x02"), strings.Join(excluded, "\x02")}
}

// splitExcluded removes the excluded names from the searched ones and returns the rest and the excluded
// names that were among the searched ones, which are the only ones sent to Twenty.
func splitExcluded(searched, excluded []string) (kept, sent []string) {
	for _, name := range searched {
		if containsName(excluded, name) {
			sent = append(sent, name)
		} else {
			kept = append(kept, name)
		}
	}
	return kept, sent
}
