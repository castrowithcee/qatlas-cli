package brave

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The values Brave documents for the shared search parameters. The tools publish them as enums of their
// input schema and check them again before a key is read.
var (
	countries = []string{"AR", "AU", "AT", "BE", "BR", "CA", "CL", "DK", "FI", "FR", "DE", "GR", "HK", "IN",
		"ID", "IT", "JP", "KR", "MY", "MX", "NL", "NZ", "NO", "CN", "PL", "PT", "PH", "RU", "SA", "ZA", "ES",
		"SE", "CH", "TW", "TR", "GB", "US", "ALL"}
	searchLanguages = []string{"ar", "eu", "bn", "bg", "ca", "zh-hans", "zh-hant", "hr", "cs", "da", "nl", "en",
		"en-gb", "et", "fi", "fr", "gl", "de", "el", "gu", "he", "hi", "hu", "is", "it", "ja", "jp", "kn", "ko",
		"lv", "lt", "ms", "ml", "mr", "nb", "pl", "pt-br", "pt-pt", "pa", "ro", "ru", "sr", "sk", "sl", "es",
		"sv", "ta", "te", "th", "tr", "uk", "vi"}
	uiLanguages = []string{"es-AR", "en-AU", "de-AT", "nl-BE", "fr-BE", "pt-BR", "en-CA", "fr-CA", "es-CL",
		"da-DK", "fi-FI", "fr-FR", "de-DE", "el-GR", "zh-HK", "en-IN", "en-ID", "it-IT", "ja-JP", "ko-KR",
		"en-MY", "es-MX", "nl-NL", "en-NZ", "no-NO", "zh-CN", "pl-PL", "en-PH", "ru-RU", "en-ZA", "es-ES",
		"sv-SE", "fr-CH", "de-CH", "zh-TW", "tr-TR", "en-GB", "en-US", "es-US"}
	safeSearchModes = []string{"off", "moderate", "strict"}
)

// Bounds of the free-text arguments.
const (
	maxGoggles      = 3
	maxGoggleLength = 2048
	maxLocationText = 100
	maxPostalCode   = 20
)

// enumSchema renders the allowed values of an argument as a JSON schema string type.
func enumSchema(values []string) string {
	encoded, _ := json.Marshal(values)
	return `{"type":"string","enum":` + string(encoded) + `}`
}

// Argument schemas shared by the search tools. Backslashes are doubled because the patterns sit in JSON
// strings.
var (
	countrySchema    = enumSchema(countries)
	searchLangSchema = enumSchema(searchLanguages)
	uiLangSchema     = enumSchema(uiLanguages)
	safeSearchSchema = enumSchema(safeSearchModes)
)

const (
	booleanSchema   = `{"type":"boolean"}`
	freshnessSchema = `{"type":"string","minLength":2,"maxLength":22,` +
		`"pattern":"^(pd|pw|pm|py|[0-9]{4}-[0-9]{2}-[0-9]{2}to[0-9]{4}-[0-9]{2}-[0-9]{2})$",` +
		`"x-form":"pd, pw, pm, py, or YYYY-MM-DDtoYYYY-MM-DD"}`
	gogglesSchema = `{"type":"array","minItems":1,"maxItems":3,"uniqueItems":true,` +
		`"items":{"type":"string","minLength":1,"maxLength":2048}}`
	cursorSchema = `{"type":"string","minLength":1,"maxLength":256,"pattern":"^[A-Za-z0-9_-]+$"}`
)

// locationInput holds the properties of the location arguments, for a tool that accepts a location.
const locationInput = `"latitude":{"type":"number","minimum":-90,"maximum":90},` +
	`"longitude":{"type":"number","minimum":-180,"maximum":180},` +
	`"city":{"type":"string","minLength":1,"maxLength":100},` +
	`"state":{"type":"string","minLength":1,"maxLength":100},` +
	`"state_name":{"type":"string","minLength":1,"maxLength":100},` +
	`"country_code":{"type":"string","pattern":"^[A-Z]{2}$"},` +
	`"postal_code":{"type":"string","minLength":1,"maxLength":20}`

func oneOf(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// checkEnum refuses an optional argument that is set to something else than a documented value.
func checkEnum(name, value string, allowed []string) error {
	if value != "" && !oneOf(allowed, value) {
		return badRequest(name + " is not a value Brave documents")
	}
	return nil
}

// checkQuery validates a search query: not empty, free of control characters, and within the character and
// word limits of its endpoint. It returns the query without surrounding space.
func checkQuery(value string, maxRunes, maxWords int) (string, error) {
	query := strings.TrimSpace(value)
	switch {
	case query == "":
		return "", badRequest("q must not be empty")
	case utf8.RuneCountInString(query) > maxRunes:
		return "", badRequest("q is longer than " + strconv.Itoa(maxRunes) + " characters")
	case len(strings.Fields(query)) > maxWords:
		return "", badRequest("q has more than " + strconv.Itoa(maxWords) + " words")
	case hasControl(query, false):
		return "", badRequest("q must not contain control characters")
	}
	return query, nil
}

// hasControl reports a control character; a line break and a tab are allowed where lines is true.
func hasControl(value string, lines bool) bool {
	for _, r := range value {
		if unicode.IsControl(r) && !(lines && (r == '\n' || r == '\t')) {
			return true
		}
	}
	return false
}

var freshnessPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}to[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// checkFreshness accepts the four ranges Brave names and a custom range of two real calendar dates in
// order.
func checkFreshness(value string) error {
	switch value {
	case "", "pd", "pw", "pm", "py":
		return nil
	}
	if freshnessPattern.MatchString(value) {
		from, to, _ := strings.Cut(value, "to")
		start, startErr := time.Parse(time.DateOnly, from)
		end, endErr := time.Parse(time.DateOnly, to)
		if startErr == nil && endErr == nil && !end.Before(start) {
			return nil
		}
	}
	return badRequest("freshness must be pd, pw, pm, py, or a range of two dates in order as YYYY-MM-DDtoYYYY-MM-DD")
}

var schemePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// checkGoggles validates the goggles of a request: up to three values, each a hosted https address or
// inline rules. Brave fetches a hosted address itself; Qatlas never does. It returns the values to send.
func checkGoggles(values []string) ([]string, error) {
	if len(values) > maxGoggles {
		return nil, badRequest("goggles takes at most 3 values")
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > maxGoggleLength {
			return nil, badRequest("a goggles value must not be empty or longer than 2048 characters")
		}
		if schemePattern.MatchString(value) {
			if err := checkGoggleAddress(value); err != nil {
				return nil, err
			}
			continue
		}
		if hasControl(value, true) {
			return nil, badRequest("inline goggles must not contain control characters")
		}
	}
	return values, nil
}

// checkGoggleAddress accepts an https address with a host, without user information, spaces, or control
// characters.
func checkGoggleAddress(value string) error {
	address, err := url.Parse(value)
	if err != nil || address.Scheme != "https" || address.Host == "" || address.User != nil ||
		address.Hostname() == "" || strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return badRequest("a goggles address must be an https address without user information")
	}
	return nil
}

// location is the optional location of the user, which Brave reads from request headers to rank local
// results. Qatlas never derives one: the caller states it.
type location struct {
	Latitude    *float64 `json:"latitude"`
	Longitude   *float64 `json:"longitude"`
	City        string   `json:"city"`
	State       string   `json:"state"`
	StateName   string   `json:"state_name"`
	CountryCode string   `json:"country_code"`
	PostalCode  string   `json:"postal_code"`
}

var countryCodePattern = regexp.MustCompile(`^[A-Z]{2}$`)

// check validates the location. Latitude and longitude come as a pair.
func (l location) check() error {
	if (l.Latitude == nil) != (l.Longitude == nil) {
		return badRequest("latitude and longitude must be given together")
	}
	if l.Latitude != nil && (*l.Latitude < -90 || *l.Latitude > 90) {
		return badRequest("latitude must be between -90 and 90")
	}
	if l.Longitude != nil && (*l.Longitude < -180 || *l.Longitude > 180) {
		return badRequest("longitude must be between -180 and 180")
	}
	for _, field := range []struct{ name, value string }{
		{"city", l.City}, {"state", l.State}, {"state_name", l.StateName},
	} {
		if err := checkLocationText(field.name, field.value, maxLocationText); err != nil {
			return err
		}
	}
	if err := checkLocationText("postal_code", l.PostalCode, maxPostalCode); err != nil {
		return err
	}
	if l.CountryCode != "" && !countryCodePattern.MatchString(l.CountryCode) {
		return badRequest("country_code must be two upper-case letters")
	}
	return nil
}

func checkLocationText(name, value string, maxRunes int) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value || utf8.RuneCountInString(value) > maxRunes || hasControl(value, false) {
		return badRequest(name + " must be plain text of at most " + strconv.Itoa(maxRunes) +
			" characters without padding")
	}
	return nil
}

// headers returns the request headers that carry the location; empty fields send nothing.
func (l location) headers() http.Header {
	header := http.Header{}
	set := func(name, value string) {
		if value != "" {
			header.Set(name, value)
		}
	}
	if l.Latitude != nil && l.Longitude != nil {
		set("X-Loc-Lat", strconv.FormatFloat(*l.Latitude, 'f', -1, 64))
		set("X-Loc-Long", strconv.FormatFloat(*l.Longitude, 'f', -1, 64))
	}
	set("X-Loc-City", l.City)
	set("X-Loc-State", l.State)
	set("X-Loc-State-Name", l.StateName)
	set("X-Loc-Country", l.CountryCode)
	set("X-Loc-Postal-Code", l.PostalCode)
	return header
}

// flag adds an optional boolean query parameter only when the caller set it.
func flag(query url.Values, name string, value *bool) {
	if value != nil {
		query.Set(name, strconv.FormatBool(*value))
	}
}

// text adds an optional text query parameter only when it is set.
func text(query url.Values, name, value string) {
	if value != "" {
		query.Set(name, value)
	}
}

// noCache returns the header that asks Brave to skip its cache, or none.
func noCache(set *bool) http.Header {
	if set != nil && *set {
		return http.Header{"Cache-Control": {"no-cache"}}
	}
	return nil
}
