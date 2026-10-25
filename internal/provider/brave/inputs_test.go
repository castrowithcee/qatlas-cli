package brave

import (
	"net/http"
	"strings"
	"testing"
)

func TestCheckQuery(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		ok    bool
	}{
		{"plain", "open source", true},
		{"padded", "  open source  ", true},
		{"empty", "", false},
		{"blank", " \t ", false},
		{"limit", strings.Repeat("ä", 400), true},
		{"over", strings.Repeat("ä", 401), false},
		{"words", strings.TrimSpace(strings.Repeat("w ", 50)), true},
		{"too many words", strings.TrimSpace(strings.Repeat("w ", 51)), false},
		{"newline", "a\nb", false},
		{"nul", "a\x00b", false},
	} {
		got, err := checkQuery(tc.value, 400, 50)
		if (err == nil) != tc.ok || (err != nil && !isInvalidRequest(err)) {
			t.Errorf("%s: err = %v, want ok = %v", tc.name, err, tc.ok)
		}
		if err == nil && got != strings.TrimSpace(tc.value) {
			t.Errorf("%s: query = %q, want it trimmed", tc.name, got)
		}
	}
	// A tool with other limits passes its own.
	if _, err := checkQuery(strings.Repeat("a", 600), 600, 75); err != nil {
		t.Errorf("limit of 600: %v", err)
	}
}

func TestCheckFreshness(t *testing.T) {
	for value, ok := range map[string]bool{
		"": true, "pd": true, "pw": true, "pm": true, "py": true, "2022-04-01to2022-07-30": true,
		"2024-02-29to2024-02-29": true, "2024-02-29to2024-03-01": true,
		"2023-02-29to2023-03-01": false, "2022-07-30to2022-04-01": false, "PD": false, "pq": false,
		"2022-04-01": false, "2022-4-1to2022-7-30": false, "2022-04-01to": false, "2022-04-01 to 2022-07-30": false,
	} {
		if err := checkFreshness(value); (err == nil) != ok {
			t.Errorf("freshness %q: err = %v, want ok = %v", value, err, ok)
		}
	}
}

func TestEnumsMatchTheReference(t *testing.T) {
	if len(countries) != 38 || !oneOf(countries, "ALL") || !oneOf(countries, "GB") || oneOf(countries, "de") {
		t.Errorf("countries = %v", countries)
	}
	if len(searchLanguages) != 52 || !oneOf(searchLanguages, "zh-hant") || !oneOf(searchLanguages, "jp") ||
		!oneOf(searchLanguages, "el") || oneOf(searchLanguages, "EN") {
		t.Errorf("search languages = %v", searchLanguages)
	}
	if len(uiLanguages) != 39 || !oneOf(uiLanguages, "no-NO") || oneOf(uiLanguages, "en-us") {
		t.Errorf("ui languages = %v", uiLanguages)
	}
	if err := checkEnum("country", "", countries); err != nil {
		t.Errorf("an omitted value is refused: %v", err)
	}
	if err := checkEnum("country", "XX", countries); err == nil || strings.Contains(err.Error(), "XX") {
		t.Errorf("err = %v, want a refusal that does not quote the value", err)
	}
}

func TestCheckGoggles(t *testing.T) {
	for name, tc := range map[string]struct {
		values []string
		ok     bool
	}{
		"none":        {nil, true},
		"hosted":      {[]string{"https://raw.githubusercontent.com/brave/goggles-quickstart/main/goggles/a.goggle"}, true},
		"inline":      {[]string{"$boost=3,site=dev.to\n$discard,site=example.com"}, true},
		"mixed three": {[]string{"https://gist.githubusercontent.com/u/1/raw/a.goggle", "$discard", "/path/$boost"}, true},
		"four":        {[]string{"a", "b", "c", "d"}, false},
		"http":        {[]string{"http://example.com/a.goggle"}, false},
		"ftp":         {[]string{"ftp://example.com/a"}, false},
		"file":        {[]string{"file:///etc/passwd"}, false},
		"userinfo":    {[]string{"https://u:p@example.com/a"}, false},
		"no host":     {[]string{"https:///a"}, false},
		"space":       {[]string{"https://example.com/a b"}, false},
		"empty":       {[]string{""}, false},
		"blank":       {[]string{"  "}, false},
		"long inline": {[]string{strings.Repeat("a", 2049)}, false},
		"max inline":  {[]string{strings.Repeat("a", 2048)}, true},
		"control":     {[]string{"a\x00"}, false},
		"carriage":    {[]string{"a\rb"}, false},
	} {
		_, err := checkGoggles(tc.values)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok = %v", name, err, tc.ok)
		}
		if err != nil && (strings.Contains(err.Error(), "example.com") || !isInvalidRequest(err)) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func f(v float64) *float64 { return &v }

func TestLocation(t *testing.T) {
	good := location{Latitude: f(47.3769), Longitude: f(8.5417), City: "Zürich", State: "ZH", StateName: "Zürich",
		CountryCode: "CH", PostalCode: "8001"}
	if err := good.check(); err != nil {
		t.Fatalf("check = %v", err)
	}
	header := good.headers()
	for name, want := range map[string]string{"X-Loc-Lat": "47.3769", "X-Loc-Long": "8.5417", "X-Loc-City": "Zürich",
		"X-Loc-State": "ZH", "X-Loc-State-Name": "Zürich", "X-Loc-Country": "CH", "X-Loc-Postal-Code": "8001"} {
		if header.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, header.Get(name), want)
		}
	}
	if len(location{}.headers()) != 0 || (location{}).check() != nil {
		t.Error("an empty location must send nothing")
	}
	if got := (location{City: "Bern"}).headers(); len(got) != 1 || got.Get("X-Loc-City") != "Bern" {
		t.Errorf("city only = %v", got)
	}
	for name, l := range map[string]location{
		"lat only":      {Latitude: f(1)},
		"long only":     {Longitude: f(1)},
		"lat high":      {Latitude: f(90.01), Longitude: f(0)},
		"lat low":       {Latitude: f(-90.01), Longitude: f(0)},
		"long high":     {Latitude: f(0), Longitude: f(180.01)},
		"long low":      {Latitude: f(0), Longitude: f(-180.01)},
		"lower country": {CountryCode: "ch"},
		"long country":  {CountryCode: "CHE"},
		"city control":  {City: "a\nb"},
		"city padded":   {City: " Bern"},
		"city long":     {City: strings.Repeat("a", 101)},
		"postal long":   {PostalCode: strings.Repeat("1", 21)},
	} {
		if err := l.check(); err == nil || !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want a refusal", name, err)
		}
	}
	for _, l := range []location{{Latitude: f(90), Longitude: f(180)}, {Latitude: f(-90), Longitude: f(-180)}} {
		if err := l.check(); err != nil {
			t.Errorf("edge %v: %v", l, err)
		}
	}
}

func TestFlagsAndTextSendOnlyWhatIsSet(t *testing.T) {
	q := map[string][]string{}
	yes, no := true, false
	flag(q, "a", &yes)
	flag(q, "b", &no)
	flag(q, "c", nil)
	text(q, "d", "x")
	text(q, "e", "")
	if len(q) != 3 || q["a"][0] != "true" || q["b"][0] != "false" || q["d"][0] != "x" {
		t.Errorf("query = %v", q)
	}
	if noCache(nil) != nil || noCache(&no) != nil || noCache(&yes).Get("Cache-Control") != "no-cache" {
		t.Error("noCache")
	}
	if _, ok := http.Header(noCache(&yes))["Cache-Control"]; !ok {
		t.Error("header key is not canonical")
	}
}
