package github

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The project filters beyond the status, type, repository, assignee, and label filters. Like those, each
// becomes quoted single terms of the project filter grammar, built only from values whose characters were
// checked, so no value can open a second term or a wildcard. GitHub's own filter reading stays the judge of
// the match; Qatlas does not verify these filters against the returned items.

const (
	maxFieldFilters    = 10
	maxFilterTextLen   = 256
	maxFieldNameLen    = 100
	maxFilterOffset    = 365
	filterDateLayout   = "2006-01-02"
	filterTodayKeyword = "@today"
)

var (
	filterKeywordPattern = regexp.MustCompile(`^@(current|next|previous|today|me)([+-][0-9]{1,3})?$`)
	filterNumberPattern  = regexp.MustCompile(`^-?[0-9]{1,15}(\.[0-9]{1,10})?$`)
	filterTodayPattern   = regexp.MustCompile(`^@today([+-][0-9]{1,3})?$`)
	parentIssuePattern   = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_-]{0,99})/([A-Za-z0-9._-]{1,100})#([1-9][0-9]{0,9})$`)
)

// FilterBound is one end of a field filter range, written as text: a number, a date, a keyword, or a title.
type FilterBound string

// FieldFilter selects items by the value of one project field: either any of Values, or the range From..To.
type FieldFilter struct {
	Field  string      `json:"field"`
	Values []string    `json:"values"`
	From   FilterBound `json:"from"`
	To     FilterBound `json:"to"`
	Negate bool        `json:"negate"`
}

// ItemFilters are the optional project filters of the item list that the first filters do not cover.
type ItemFilters struct {
	State        string        `json:"state"`
	Reason       string        `json:"reason"`
	Milestone    []string      `json:"milestone"`
	Reviewers    []string      `json:"reviewers"`
	IssueType    []string      `json:"issue_type"`
	ParentIssue  string        `json:"parent_issue"`
	UpdatedFrom  string        `json:"updated_from"`
	UpdatedTo    string        `json:"updated_to"`
	Has          []string      `json:"has"`
	No           []string      `json:"no"`
	FieldFilters []FieldFilter `json:"field_filters"`
	Text         string        `json:"text"`
}

// binding is the part of the cursor fingerprint these filters add: nothing when none is set, so a cursor
// made before they existed stays valid.
func (f *ItemFilters) binding() any {
	if f.State == "" && f.Reason == "" && len(f.Milestone) == 0 && len(f.Reviewers) == 0 && len(f.IssueType) == 0 &&
		f.ParentIssue == "" && f.UpdatedFrom == "" && f.UpdatedTo == "" && len(f.Has) == 0 && len(f.No) == 0 &&
		len(f.FieldFilters) == 0 && f.Text == "" {
		return nil
	}
	return struct {
		State, Reason, ParentIssue, UpdatedFrom, UpdatedTo, Text string
		Milestone, Reviewers, IssueType, Has, No                 []string
		FieldFilters                                             []FieldFilter
	}{f.State, f.Reason, strings.ToLower(f.ParentIssue), f.UpdatedFrom, f.UpdatedTo, f.Text,
		folded(f.Milestone), folded(f.Reviewers), folded(f.IssueType), folded(f.Has), folded(f.No), f.FieldFilters}
}

// check validates every filter before a credential is resolved.
func (f *ItemFilters) check() error {
	switch f.State {
	case "", "open", "closed", "merged":
	default:
		return invalidRequest("state must be open, closed, or merged")
	}
	switch f.Reason {
	case "", "completed", "not_planned", "reopened":
	default:
		return invalidRequest("reason must be completed, not_planned, or reopened")
	}
	for _, list := range []struct {
		name   string
		values []string
	}{{"milestone", f.Milestone}, {"issue_type", f.IssueType}} {
		if err := checkFilterList(list.name, list.values); err != nil {
			return err
		}
	}
	if len(f.Reviewers) > maxFilterValues {
		return invalidRequest(fmt.Sprintf("reviewers accepts at most %d values", maxFilterValues))
	}
	for _, reviewer := range f.Reviewers {
		if reviewer != "@me" && !validLogin(reviewer) {
			return invalidRequest("reviewers must be GitHub logins or @me")
		}
	}
	if f.ParentIssue != "" && !parentIssuePattern.MatchString(f.ParentIssue) {
		return invalidRequest("parent_issue must be OWNER/REPO#NUMBER")
	}
	for _, bound := range []struct{ name, value string }{{"updated_from", f.UpdatedFrom}, {"updated_to", f.UpdatedTo}} {
		if _, err := dateBound(bound.name, bound.value); err != nil {
			return err
		}
	}
	for _, list := range []struct {
		name   string
		values []string
	}{{"has", f.Has}, {"no", f.No}} {
		if len(list.values) > maxFilterValues {
			return invalidRequest(fmt.Sprintf("%s accepts at most %d values", list.name, maxFilterValues))
		}
		for _, name := range list.values {
			if !validFieldName(name) {
				return invalidRequest(list.name + " contains a field name a filter cannot carry")
			}
		}
	}
	if len(f.FieldFilters) > maxFieldFilters {
		return invalidRequest(fmt.Sprintf("field_filters accepts at most %d entries", maxFieldFilters))
	}
	for _, filter := range f.FieldFilters {
		if _, err := filter.term(); err != nil {
			return err
		}
	}
	if f.Text != "" && !safeFilterText(f.Text, maxFilterTextLen) {
		return invalidRequest("text contains a value a filter cannot carry")
	}
	return nil
}

// validFieldName accepts the name of a project field as the filter key it becomes: no separator, quote,
// wildcard, control character, padding, or leading minus, which would negate the term.
func validFieldName(name string) bool {
	return safeFilterText(name, maxFieldNameLen) && !strings.ContainsAny(name, ":@") && !strings.HasPrefix(name, "-")
}

// fieldKey writes a field name as GitHub's filter key: spaces become hyphens.
func fieldKey(name string) string { return strings.ReplaceAll(name, " ", "-") }

// offset reads the signed day or step count of a keyword and checks its range.
func offset(text string) (string, error) {
	if text == "" {
		return "", nil
	}
	n, _ := strconv.Atoi(text)
	if n < -maxFilterOffset || n > maxFilterOffset {
		return "", invalidRequest(fmt.Sprintf("an offset ranges from -%d to %d", maxFilterOffset, maxFilterOffset))
	}
	return text, nil
}

// dateBound reads a date YYYY-MM-DD or @today with an offset in days; empty reads as no bound.
func dateBound(name, value string) (string, error) {
	switch {
	case value == "":
		return "", nil
	case filterTodayPattern.MatchString(value):
		step, err := offset(strings.TrimPrefix(value, filterTodayKeyword))
		if err != nil {
			return "", err
		}
		if step == "" {
			return filterTodayKeyword, nil
		}
		return filterTodayKeyword + step + "d", nil
	}
	if _, err := time.Parse(filterDateLayout, value); err != nil {
		return "", invalidRequest(name + " must be a date YYYY-MM-DD or @today with an offset in days")
	}
	return value, nil
}

// bound reads one end of a field range: a keyword with an optional offset, a number, a date, or the text of
// an iteration title.
func (b FilterBound) bound() (string, error) {
	value := string(b)
	switch {
	case value == "":
		return "*", nil
	case filterKeywordPattern.MatchString(value):
		match := filterKeywordPattern.FindStringSubmatch(value)
		step, err := offset(match[2])
		if err != nil {
			return "", err
		}
		switch {
		case match[1] == "me" && step != "":
			return "", invalidRequest("@me takes no offset")
		case match[1] == "today" && step != "":
			return "@today" + step + "d", nil
		}
		return "@" + match[1] + step, nil
	case filterNumberPattern.MatchString(value):
		return value, nil
	}
	if _, err := time.Parse(filterDateLayout, value); err == nil {
		return value, nil
	}
	if strings.HasPrefix(value, "@") || !safeFilterText(value, maxFilterTextLen) {
		return "", invalidRequest("a range bound is a number, a date, a keyword, or an iteration title a filter can carry")
	}
	return quoted(value), nil
}

// term builds the one term of a field filter.
func (f FieldFilter) term() (string, error) {
	if !validFieldName(f.Field) {
		return "", invalidRequest("field_filters needs a field name a filter can carry")
	}
	ranged := f.From != "" || f.To != ""
	switch {
	case ranged == (len(f.Values) > 0):
		return "", invalidRequest("a field filter takes either values or from and to")
	case len(f.Values) > maxFilterValues:
		return "", invalidRequest(fmt.Sprintf("values accepts at most %d entries", maxFilterValues))
	}
	for _, value := range f.Values {
		if !safeFilterText(value, maxFilterTextLen) {
			return "", invalidRequest("values contains a value a filter cannot carry")
		}
	}
	term := fieldKey(f.Field) + ":"
	if f.Negate {
		term = "-" + term
	}
	if !ranged {
		return term + quotedList(f.Values), nil
	}
	from, err := f.From.bound()
	if err != nil {
		return "", err
	}
	to, err := f.To.bound()
	if err != nil {
		return "", err
	}
	return term + from + ".." + to, nil
}

// terms returns the filter terms in a fixed order. The filters were checked, so a term cannot fail here.
func (f *ItemFilters) terms() []string {
	var terms []string
	if f.State != "" {
		terms = append(terms, "is:"+f.State)
	}
	if f.Reason != "" {
		terms = append(terms, "reason:"+quoted(strings.ReplaceAll(f.Reason, "_", " ")))
	}
	if len(f.Milestone) > 0 {
		terms = append(terms, "milestone:"+quotedList(f.Milestone))
	}
	if len(f.Reviewers) > 0 {
		terms = append(terms, "reviewers:"+strings.Join(f.Reviewers, ","))
	}
	if len(f.IssueType) > 0 {
		terms = append(terms, "type:"+quotedList(f.IssueType))
	}
	if f.ParentIssue != "" {
		terms = append(terms, "parent-issue:"+f.ParentIssue)
	}
	if f.UpdatedFrom != "" || f.UpdatedTo != "" {
		from, _ := dateBound("updated_from", f.UpdatedFrom)
		to, _ := dateBound("updated_to", f.UpdatedTo)
		for _, end := range []*string{&from, &to} {
			if *end == "" {
				*end = "*"
			}
		}
		terms = append(terms, "updated:"+from+".."+to)
	}
	for _, name := range f.Has {
		terms = append(terms, "has:"+fieldKey(name))
	}
	for _, name := range f.No {
		terms = append(terms, "no:"+fieldKey(name))
	}
	for _, filter := range f.FieldFilters {
		term, _ := filter.term()
		terms = append(terms, term)
	}
	if f.Text != "" {
		terms = append(terms, quoted(f.Text))
	}
	return terms
}

// ---- Descriptor parts

const (
	filterNameSchema = `{"type":"string","minLength":1,"maxLength":100,"pattern":"` + filterValuePattern + `"}`
	filterTextSchema = `{"type":"string","minLength":1,"maxLength":256,"pattern":"` + filterValuePattern + `"}`
	filterDateSchema = `{"type":"string","maxLength":16,"pattern":"^([0-9]{4}-[0-9]{2}-[0-9]{2}|@today([+-][0-9]{1,3})?)$"}`
	filterRangeEnd   = `{"type":"string","maxLength":256}`
)

var itemFilterInput = `"state":{"type":"string","enum":["open","closed","merged"]},` +
	`"reason":{"type":"string","enum":["completed","not_planned","reopened"]},` +
	`"milestone":` + filterListSchema + `,` +
	`"reviewers":{"type":"array","maxItems":10,"items":{"type":"string","maxLength":100,"pattern":"^(@me|[A-Za-z0-9][A-Za-z0-9_-]{0,99})$"}},` +
	`"issue_type":` + filterListSchema + `,` +
	`"parent_issue":{"type":"string","maxLength":170,"pattern":"^[A-Za-z0-9][A-Za-z0-9_-]{0,99}/[A-Za-z0-9._-]{1,100}#[1-9][0-9]{0,9}$"},` +
	`"updated_from":` + filterDateSchema + `,"updated_to":` + filterDateSchema + `,` +
	`"has":{"type":"array","maxItems":10,"items":` + filterNameSchema + `},` +
	`"no":{"type":"array","maxItems":10,"items":` + filterNameSchema + `},` +
	`"field_filters":{"type":"array","maxItems":10,"items":{"type":"object","properties":{` +
	`"field":` + filterNameSchema + `,"values":{"type":"array","maxItems":10,"items":` + filterTextSchema + `},` +
	`"from":` + filterRangeEnd + `,"to":` + filterRangeEnd + `,"negate":{"type":"boolean"}},` +
	`"required":["field"],"additionalProperties":false}},` +
	`"text":` + filterTextSchema

var itemFilterArguments = []capability.Argument{
	{Name: "state", Description: "Return only items in this state: open, closed, or merged"},
	{Name: "reason", Description: "Return only items closed for this reason: completed, not_planned, or reopened"},
	{Name: "milestone", Description: "Return only items in one of these milestones, by title"},
	{Name: "reviewers", Description: "Return only pull requests with one of these reviewers, by login or @me"},
	{Name: "issue_type", Description: "Return only issues of one of these issue types, by name"},
	{Name: "parent_issue", Description: "Return only sub-issues of this issue, as OWNER/REPO#NUMBER"},
	{Name: "updated_from", Description: "Return only items updated on or after this date, as YYYY-MM-DD or @today " +
		"with an offset of -365 to 365 days such as @today-7"},
	{Name: "updated_to", Description: "Return only items updated on or before this date, written like updated_from"},
	{Name: "has", Description: "Return only items that have a value in each of these fields, by field name"},
	{Name: "no", Description: "Return only items that have no value in each of these fields, by field name"},
	{Name: "field_filters", Description: "Up to 10 filters on project fields, each with field and either values " +
		"(any of up to 10 texts) or from and to (a number, a date YYYY-MM-DD, an iteration title, or @current, " +
		"@next, @previous, @today, @me with an optional integer offset; a missing end is open), and negate"},
	{Name: "text", Description: "Free text searched in the titles and text fields, up to 256 characters"},
}
