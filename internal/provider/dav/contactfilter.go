package dav

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The fixed value lists of the contact filters. Only these values ever reach a REPORT body.
var (
	ContactFilterProperties = []string{"FN", "N", "NICKNAME", "EMAIL", "TEL", "ADR", "LABEL", "ORG", "TITLE", "ROLE",
		"NOTE", "CATEGORIES", "UID", "URL", "BDAY", "CLASS", "MAILER", "TZ", "GEO", "SORT-STRING", "REV"}
	ContactFilterParameters = []string{"TYPE", "PREF", "VALUE"}
	ContactFilterCollations = []string{"i;unicode-casemap", "i;ascii-casemap", "i;octet"}
	ContactFilterMatchTypes = []string{"equals", "contains", "starts-with", "ends-with"}
	ContactFilterTests      = []string{"anyof", "allof"}
)

// ContactParamFilter narrows a property by one of its parameters: a text condition or defined=false.
type ContactParamFilter struct {
	Name      string  `json:"name"`
	Text      *string `json:"text"`
	MatchType string  `json:"match_type"`
	Negate    bool    `json:"negate"`
	Collation string  `json:"collation"`
	Defined   *bool   `json:"defined"`
}

// ContactPropertyFilter is one prop-filter of an addressbook-query.
type ContactPropertyFilter struct {
	Property  string              `json:"property"`
	Text      *string             `json:"text"`
	MatchType string              `json:"match_type"`
	Negate    bool                `json:"negate"`
	Collation string              `json:"collation"`
	Defined   *bool               `json:"defined"`
	Parameter *ContactParamFilter `json:"parameter"`
}

// ContactFilter is the optional narrowing of a contact list.
type ContactFilter struct {
	Properties []ContactPropertyFilter
	Test       string
}

// Zero reports whether the filter changes nothing about the plain query.
func (f ContactFilter) Zero() bool { return len(f.Properties) == 0 && f.Test == "" }

// UsesCollation reports whether any condition names a collation.
func (f ContactFilter) UsesCollation() bool {
	for _, p := range f.Properties {
		if p.Collation != "" || (p.Parameter != nil && p.Parameter.Collation != "") {
			return true
		}
	}
	return false
}

// ContactQueryBody validates the filter and builds the addressbook-query. Without a filter the body is
// AddressbookQueryBody. Every text is XML-escaped and every name comes from a fixed list. The server limit
// element is never sent: the list sorts all hits before it cuts.
func ContactQueryBody(op string, f ContactFilter) (string, error) {
	if !slices.Contains(append([]string{""}, ContactFilterTests...), f.Test) {
		return "", provider.Fail(op, "test is not offered")
	}
	if f.Zero() {
		return AddressbookQueryBody, nil
	}
	if len(f.Properties) == 0 {
		return "", provider.Fail(op, "test needs property_filters")
	}
	if len(f.Properties) > MaxPropertyFilters {
		return "", provider.Fail(op, fmt.Sprintf("property_filters takes at most %d entries", MaxPropertyFilters))
	}
	var filters bytes.Buffer
	for i := range f.Properties {
		if err := writeContactPropFilter(op, &filters, &f.Properties[i]); err != nil {
			return "", err
		}
	}
	test := ""
	if f.Test == "allof" {
		test = ` test="allof"`
	}
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<card:addressbook-query xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">` +
		`<d:prop><d:getetag/><card:address-data/></d:prop>` +
		`<card:filter` + test + `>` + filters.String() + `</card:filter></card:addressbook-query>`, nil
}

func writeContactPropFilter(op string, out *bytes.Buffer, p *ContactPropertyFilter) error {
	if !slices.Contains(ContactFilterProperties, p.Property) {
		return provider.Fail(op, "property_filters names a property that is not offered")
	}
	if p.Text != nil && p.Defined != nil {
		return provider.Fail(op, "a property filter takes exactly one of text and defined")
	}
	if p.Text == nil && p.Defined == nil && p.Parameter == nil {
		return provider.Fail(op, "a property filter needs one of text and defined")
	}
	if p.Defined != nil && *p.Defined {
		return provider.Fail(op, "defined only takes false")
	}
	if p.Defined != nil && p.Parameter != nil {
		return provider.Fail(op, "a property that must be undefined cannot carry a parameter filter")
	}
	if p.Text == nil && (p.Negate || p.Collation != "" || p.MatchType != "") {
		return provider.Fail(op, "negate, collation, and match_type need a text")
	}
	var inner bytes.Buffer
	switch {
	case p.Text != nil:
		match, err := contactTextMatch(op, *p.Text, p.MatchType, p.Negate, p.Collation)
		if err != nil {
			return err
		}
		inner.WriteString(match)
	case p.Defined != nil:
		inner.WriteString(`<card:is-not-defined/>`)
	}
	if p.Parameter != nil {
		param, err := contactParamFilter(op, p.Parameter)
		if err != nil {
			return err
		}
		inner.WriteString(param)
	}
	fmt.Fprintf(out, `<card:prop-filter name="%s">%s</card:prop-filter>`, p.Property, inner.String())
	return nil
}

func contactParamFilter(op string, p *ContactParamFilter) (string, error) {
	if !slices.Contains(ContactFilterParameters, p.Name) {
		return "", provider.Fail(op, "parameter names a parameter that is not offered")
	}
	switch {
	case p.Text != nil && p.Defined != nil:
		return "", provider.Fail(op, "a parameter filter takes exactly one of text and defined")
	case p.Defined != nil:
		if *p.Defined || p.Negate || p.Collation != "" || p.MatchType != "" {
			return "", provider.Fail(op, "defined only takes false, without negate, collation, or match_type")
		}
		return `<card:param-filter name="` + p.Name + `"><card:is-not-defined/></card:param-filter>`, nil
	case p.Text != nil:
		match, err := contactTextMatch(op, *p.Text, p.MatchType, p.Negate, p.Collation)
		if err != nil {
			return "", err
		}
		return `<card:param-filter name="` + p.Name + `">` + match + `</card:param-filter>`, nil
	}
	return "", provider.Fail(op, "a parameter filter needs a text or defined")
}

// contactTextMatch builds a text-match. Attributes are only written when the caller chose a value, so the
// server defaults (contains, i;unicode-casemap) apply otherwise.
func contactTextMatch(op, text, matchType string, negate bool, collation string) (string, error) {
	if text == "" || len(text) > MaxFilterText {
		return "", provider.Fail(op, fmt.Sprintf("a filter text takes 1 to %d bytes", MaxFilterText))
	}
	if !utf8.ValidString(text) || strings.IndexFunc(text, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return "", provider.Fail(op, "a filter text must be valid text without control characters")
	}
	attrs := ""
	if collation != "" {
		if !slices.Contains(ContactFilterCollations, collation) {
			return "", provider.Fail(op, "collation is not offered")
		}
		attrs += ` collation="` + collation + `"`
	}
	if matchType != "" {
		if !slices.Contains(ContactFilterMatchTypes, matchType) {
			return "", provider.Fail(op, "match_type is not offered")
		}
		attrs += ` match-type="` + matchType + `"`
	}
	if negate {
		attrs += ` negate-condition="yes"`
	}
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(text))
	return `<card:text-match` + attrs + `>` + escaped.String() + `</card:text-match>`, nil
}

// ContactFilterSchema returns the JSON schema members of the filter arguments for a tool input schema.
func ContactFilterSchema() string {
	enum := func(values []string) string {
		raw, _ := json.Marshal(values)
		return `{"type":"string","enum":` + string(raw) + `}`
	}
	text := fmt.Sprintf(`{"type":"string","minLength":1,"maxLength":%d}`, MaxFilterText)
	condition := `"text":` + text + `,"match_type":` + enum(ContactFilterMatchTypes) + `,"negate":{"type":"boolean"},` +
		`"collation":` + enum(ContactFilterCollations) + `,"defined":{"type":"boolean","enum":[false]}`
	parameter := `{"type":"object","properties":{"name":` + enum(ContactFilterParameters) + `,` + condition +
		`},"required":["name"],"additionalProperties":false}`
	entry := `{"type":"object","properties":{"property":` + enum(ContactFilterProperties) + `,` + condition +
		`,"parameter":` + parameter + `},"required":["property"],"additionalProperties":false}`
	return `"property_filters":{"type":"array","maxItems":` + fmt.Sprint(MaxPropertyFilters) + `,"items":` + entry + `},` +
		`"test":` + enum(ContactFilterTests)
}
