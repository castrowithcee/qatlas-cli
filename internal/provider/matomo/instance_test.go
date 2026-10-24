package matomo

import (
	"net/http"
	"strings"
	"testing"
)

func TestGlobalSettingsNeedTheInstanceTargetBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, versionOnly("5.14.2").handler(t))
	for _, connection := range []string{"open", "general", "one", "two"} {
		if _, err := env.invoke("matomo.global-settings.get", connection, `{}`); err == nil {
			t.Errorf("connection %s read global settings", connection)
		}
	}
	// A website outside the targets is refused before any request, without naming it.
	if _, err := env.invoke("matomo.global-settings.get", "mixed", `{"section":"excluded-query-parameters","idSite":1}`); !isInvalidRequest(err) {
		t.Errorf("idSite outside: err = %v", err)
	}
	if _, err := env.invoke("matomo.global-settings.get", "mixed", `{"section":"excluded-ips","idSite":2}`); !isInvalidRequest(err) {
		t.Errorf("idSite for a global section: err = %v", err)
	}
	if _, err := env.invoke("matomo.global-settings.get", "instance", `{"section":"nope"}`); !isInvalidRequest(err) {
		t.Errorf("unknown section: err = %v", err)
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Errorf("secret reads = %d, requests = %d", *env.reads, len(calls))
	}
	if ok, _ := reaches(globalSettingsGet.ID, []string{"site"}); ok {
		t.Error("reaches offers global settings without instance")
	}
}

func globalAnswers() answers {
	text := func(v string) func(call) *http.Response { return func(call) *http.Response { return ok(v) } }
	return with(versionOnly("5.14.2"), answers{
		"SitesManager.getExcludedIpsGlobal":              text(`"10.0.0.1,10.0.1.*"`),
		"SitesManager.getExcludedUserAgentsGlobal":       text(`"bot"`),
		"SitesManager.getExcludedReferrersGlobal":        text(`"spam.example.test"`),
		"SitesManager.getExcludedQueryParametersGlobal":  text(`"sid,token"`),
		"SitesManager.getExclusionTypeForQueryParams":    text(`"custom"`),
		"SitesManager.getSearchKeywordParametersGlobal":  text(`"q,s"`),
		"SitesManager.getSearchCategoryParametersGlobal": text(`false`),
		"SitesManager.getKeepURLFragmentsGlobal":         text(`true`),
		"SitesManager.getDefaultCurrency":                text(`"EUR"`),
		"SitesManager.getDefaultTimezone":                text(`"Europe/Paris"`),
	})
}

func TestGlobalSettingsReadAllAndOneSection(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, globalAnswers().handler(t))
	result, err := env.invoke("matomo.global-settings.get", "instance", `{}`)
	for _, want := range []string{`"excluded_ips":"10.0.0.1,10.0.1.*"`, `"excluded_user_agents":"bot"`,
		`"excluded_referrers":"spam.example.test"`, `"excluded_query_parameters":"sid,token"`,
		`"query_parameter_exclusion_type":"custom"`, `"search_keyword_parameters":"q,s"`,
		`"search_category_parameters":""`, `"keep_url_fragments":true`, `"default_currency":"EUR"`,
		`"default_timezone":"Europe/Paris"`} {
		if err != nil || !strings.Contains(result, want) {
			t.Fatalf("missing %s in %s, err = %v", want, result, err)
		}
	}
	before := len(calls)
	result, err = env.invoke("matomo.global-settings.get", "mixed", `{"section":"excluded-query-parameters","idSite":2}`)
	if err != nil || result != `{"excluded_query_parameters":"sid,token"}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := calls[len(calls)-1].form; got.Get("idSite") != "2" || got.Get("method") != "SitesManager.getExcludedQueryParametersGlobal" {
		t.Errorf("form = %v", got)
	}
	if len(calls)-before != 1 {
		t.Errorf("a section made %d requests", len(calls)-before)
	}
}

func TestGlobalSettingsPermissionErrorKeepsProviderText(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getExcludedIpsGlobal": func(call) *http.Response { return failure(bodyCanary) },
	}).handler(t))
	_, err := env.invoke("matomo.global-settings.get", "instance", `{"section":"excluded-ips"}`)
	if err == nil || strings.Contains(err.Error(), bodyCanary) {
		t.Fatalf("err = %v", err)
	}
}

func referenceAnswers() answers {
	text := func(v string) func(call) *http.Response { return func(call) *http.Response { return ok(v) } }
	return with(versionOnly("5.14.2"), answers{
		"SitesManager.getCurrencyList":          text(`{"USD":"US Dollar ($)","EUR":"Euro (€)","CHF":"Franc (CHF)"}`),
		"SitesManager.getCurrencySymbols":       text(`{"USD":"$","EUR":"€"}`),
		"SitesManager.getTimezonesList":         text(`{"Europe":{"Europe/Paris":"France","Europe/Berlin":"Germany"},"UTC":{"UTC":"UTC"}}`),
		"SitesManager.getTimezoneName":          text(`"Japan"`),
		"SitesManager.isTimezoneSupportEnabled": text(`true`),
		"API.getAvailableMeasurableTypes": text(`[{"id":"website","name":"Website","description":"d","longDescription":"l",` +
			`"howToSetupUrl":"https://help.example.test/w","settings":[{"name":"urls","value":1}]}]`),
		"UserCountry.getCountryCodeMapping": text(`{"de":"Germany","fr":"France"}`),
		"API.isPluginActivated":             text(`true`),
	})
}

func TestReferenceDataKinds(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, referenceAnswers().handler(t))
	cases := map[string]struct{ arguments, want string }{
		"currencies":       {`{"kind":"currencies"}`, `"entries":[{"id":"CHF","name":"Franc (CHF)"},{"id":"EUR","name":"Euro (€)"},{"id":"USD","name":"US Dollar ($)"}]`},
		"currency-symbols": {`{"kind":"currency-symbols"}`, `{"id":"EUR","name":"€"}`},
		"timezones":        {`{"kind":"timezones"}`, `{"group":"Europe","id":"Europe/Berlin","name":"Germany"}`},
		"timezone-name":    {`{"kind":"timezone-name","timezone":"Asia/Tokyo","countryCode":"JP"}`, `"timezone_name":"Japan"`},
		"timezone-support": {`{"kind":"timezone-support"}`, `"timezone_support":true`},
		"measurable-types": {`{"kind":"measurable-types"}`, `"how_to_setup_url":"https://help.example.test/w"`},
		"country-codes":    {`{"kind":"country-codes"}`, `{"id":"de","name":"Germany"}`},
	}
	for kind, c := range cases {
		result, err := env.invoke("matomo.reference-data.list", "open", c.arguments)
		if err != nil || !strings.Contains(result, c.want) || !strings.Contains(result, `"has_more":false`) {
			t.Errorf("%s: result = %s, err = %v", kind, result, err)
		}
	}
	seen := false
	for _, c := range calls {
		seen = seen || c.form.Get("method") == "UserCountry.getCountryCodeMapping"
	}
	if !seen {
		t.Error("country codes were not read from UserCountry")
	}
}

func TestReferenceDataPagesAndValidates(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, referenceAnswers().handler(t))
	first := listOf2(t, env, `{"kind":"currencies","limit":2}`)
	if !strings.Contains(first, `"has_more":true`) || !strings.Contains(first, `"next_cursor"`) {
		t.Fatalf("first = %s", first)
	}
	for _, bad := range []string{`{"kind":"nope"}`, `{"kind":"currencies","timezone":"UTC"}`, `{"kind":"timezone-name"}`,
		`{"kind":"timezone-name","timezone":"UTC","countryCode":"jp"}`} {
		if _, err := env.invoke("matomo.reference-data.list", "open", bad); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
	reads := *env.reads
	if _, err := env.invoke("matomo.reference-data.list", "instance", `{"kind":"currencies"}`); err == nil {
		t.Error("instance-only connection read reference data")
	}
	if *env.reads != reads {
		t.Error("secret read before target refusal")
	}
}

func listOf2(t *testing.T, env *environment, arguments string) string {
	t.Helper()
	result, err := env.invoke("matomo.reference-data.list", "open", arguments)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestIPRangesExpand(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getIpsForRange": func(c call) *http.Response {
			if c.form.Get("ipRange") == "10.0.0.0/8" {
				return ok(`["10.0.0.0","10.255.255.255"]`)
			}
			return ok(`false`)
		},
	}).handler(t))
	result, err := env.invoke("matomo.ip-ranges.expand", "open", `{"ipRange":"10.0.0.0/8"}`)
	if err != nil || result != `{"high":"10.255.255.255","ip_range":"10.0.0.0/8","low":"10.0.0.0"}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if _, err := env.invoke("matomo.ip-ranges.expand", "open", `{"ipRange":"10.0.0.5/33"}`); !isInvalidRequest(err) {
		t.Errorf("bad prefix: err = %v", err)
	}
	for _, bad := range []string{`1.2.*.*`, `1.2.3.4-1.2.3.9`, `fe80::1%eth0`, `host.example.test`, strings.Repeat("1", 70)} {
		if _, err := env.invoke("matomo.ip-ranges.expand", "open", `{"ipRange":"`+bad+`"}`); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
	n := len(calls)
	if _, err := env.invoke("matomo.ip-ranges.expand", "open", `{"ipRange":"192.168.0.0/16"}`); !isInvalidRequest(err) {
		t.Errorf("rejected by Matomo: err = %v", err)
	}
	if len(calls) == n {
		t.Error("no request for a valid range")
	}
}
