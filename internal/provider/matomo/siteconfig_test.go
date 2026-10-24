package matomo

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestTrackingCodeJavascriptSendsEveryDocumentedParameter(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getJavascriptTag": func(call) *http.Response { return ok(`"<script>var _paq=[];</script>"`) },
	}).handler(t))
	result, err := env.invoke("matomo.tracking-code.get", "general", `{"idSite":3,"kind":"javascript",`+
		`"piwikUrl":"https://stats.example.test/m/","mergeSubdomains":true,"groupPageTitlesByDomain":true,`+
		`"mergeAliasUrls":true,"visitorCustomVariables":[{"name":"a","value":"b"}],`+
		`"pageCustomVariables":[{"name":"c","value":"d"}],"customCampaignNameQueryParam":"cn",`+
		`"customCampaignKeywordParam":"ck","doNotTrack":true,"disableCookies":true,"trackNoScript":true,`+
		`"crossDomain":true,"forceMatomoEndpoint":true,"excludedQueryParams":["x","y"],`+
		`"excludedReferrers":["r.example.test"],"disableCampaignParameters":true}`)
	if err != nil || !strings.Contains(result, `"code":"\u003cscript\u003evar _paq=[];\u003c/script\u003e"`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	form := calls[len(calls)-1].form
	want := url.Values{"idSite": {"3"}, "piwikUrl": {"https://stats.example.test/m"}, "mergeSubdomains": {"1"},
		"groupPageTitlesByDomain": {"1"}, "mergeAliasUrls": {"1"}, "visitorCustomVariables[0][0]": {"a"},
		"visitorCustomVariables[0][1]": {"b"}, "pageCustomVariables[0][0]": {"c"}, "pageCustomVariables[0][1]": {"d"},
		"customCampaignNameQueryParam": {"cn"}, "customCampaignKeywordParam": {"ck"}, "doNotTrack": {"1"},
		"disableCookies": {"1"}, "trackNoScript": {"1"}, "crossDomain": {"1"}, "forceMatomoEndpoint": {"1"},
		"excludedQueryParams": {"x,y"}, "excludedReferrers": {"r.example.test"}, "disableCampaignParameters": {"1"}}
	for name, values := range want {
		if form.Get(name) != values[0] {
			t.Errorf("%s = %q, want %q", name, form.Get(name), values[0])
		}
	}
}

func TestTrackingCodeImageDefaultsToTheConnectionAddress(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getImageTrackingCode": func(call) *http.Response { return ok(`{"value":"&lt;img&gt;"}`) },
	}).handler(t))
	result, err := env.invoke("matomo.tracking-code.get", "open", `{"idSite":1,"kind":"image","actionName":"A","idGoal":2,"revenue":4.5}`)
	if err != nil || !strings.Contains(result, `"code":"\u0026lt;img\u0026gt;"`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	form := calls[len(calls)-1].form
	if form.Get("piwikUrl") != "analytics.example.test/matomo" || form.Get("actionName") != "A" ||
		form.Get("idGoal") != "2" || form.Get("revenue") != "4.5" {
		t.Fatalf("form = %v", form)
	}
}

func TestTrackingCodeRefusesBadInputBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, versionOnly("5.14.2").handler(t))
	userInfo := (&url.URL{Scheme: "https", User: url.UserPassword("u", "p"), Host: "h.example.test"}).String()
	for name, arguments := range map[string]string{
		"foreign website": `{"idSite":9,"kind":"javascript"}`,
		"unknown kind":    `{"idSite":2,"kind":"php"}`,
		"http address":    `{"idSite":2,"kind":"javascript","piwikUrl":"http://h.example.test"}`,
		"user info":       `{"idSite":2,"kind":"javascript","piwikUrl":"` + userInfo + `"}`,
		"query":           `{"idSite":2,"kind":"javascript","piwikUrl":"https://h.example.test/?a=1"}`,
		"quote":           `{"idSite":2,"kind":"javascript","piwikUrl":"https://h.example.test/\"x"}`,
		"image arg on js": `{"idSite":2,"kind":"javascript","actionName":"x"}`,
		"js arg on image": `{"idSite":2,"kind":"image","doNotTrack":true}`,
		"revenue alone":   `{"idSite":2,"kind":"image","revenue":1}`,
		"comma in list":   `{"idSite":2,"kind":"javascript","excludedReferrers":["a,b"]}`,
	} {
		if _, err := env.invoke("matomo.tracking-code.get", "one", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Errorf("secret reads = %d, requests = %d", *env.reads, len(calls))
	}
}

func TestTrackingCodeCutsLongCode(t *testing.T) {
	var calls []call
	long := strings.Repeat("a", maxCodeLength+10)
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getJavascriptTag": func(call) *http.Response { return ok(`"` + long + `"`) },
	}).handler(t))
	result, err := env.invoke("matomo.tracking-code.get", "open", `{"idSite":1,"kind":"javascript"}`)
	if err != nil || !strings.Contains(result, `"truncated":true`) || len(result) > maxCodeLength+200 {
		t.Fatalf("len = %d, err = %v", len(result), err)
	}
}

func TestSiteSettingsReturnsEveryField(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getSiteSettings": func(call) *http.Response {
			return ok(`[{"pluginName":"WebsiteMeasurable","title":"Website","settings":[{"name":"urls","title":"URLs",` +
				`"value":["https://a.example.test"],"defaultValue":[],"type":"array","uiControl":"textarea",` +
				`"uiControlAttributes":{"rows":3},"availableValues":{"a":"A"},"description":"d","inlineHelp":"h",` +
				`"introduction":"i","condition":"c","fullWidth":true,"component":"Comp",` +
				`"extraMetadata":{"compliancePolicyControlled":{},"idSite":1}}]}]`)
		},
	}).handler(t))
	result, err := env.invoke("matomo.site-settings.get", "open", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{`"plugin_name":"WebsiteMeasurable"`, `"value":["https://a.example.test"]`,
		`"type":"array"`, `"ui_control":"textarea"`, `"ui_control_attributes":{"rows":3}`,
		`"available_values":{"a":"A"}`, `"description":"d"`, `"inline_help":"h"`, `"introduction":"i"`,
		`"condition":"c"`, `"full_width":true`, `"component":"Comp"`, `"extra_metadata":{`} {
		if !strings.Contains(result, part) {
			t.Errorf("result lacks %s: %s", part, result)
		}
	}
}

func TestSiteSettingsBoundsLargeValuesAndRefusesForeignWebsite(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getSiteSettings": func(call) *http.Response {
			return ok(`[{"pluginName":"P","settings":[{"name":"n","value":"` + strings.Repeat("v", 20000) + `"}]}]`)
		},
		"SitesManager.getSiteSettings#": nil,
	}).handler(t))
	result, err := env.invoke("matomo.site-settings.get", "open", `{"idSite":1}`)
	if err != nil || len(result) > maxSettingValue+300 {
		t.Fatalf("len = %d, err = %v", len(result), err)
	}
	before := *env.reads
	if _, err := env.invoke("matomo.site-settings.get", "one", `{"idSite":1}`); !isInvalidRequest(err) || *env.reads != before {
		t.Errorf("err = %v", err)
	}
}

func TestSiteSettingsMapsPermissionAndMissingMethod(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getSiteSettings": func(call) *http.Response { return failure(permissionMsg) },
	}).handler(t))
	_, err := env.invoke("matomo.site-settings.get", "open", `{"idSite":1}`)
	if classOf(err) != "permission" || strings.Contains(err.Error(), bodyCanary) {
		t.Fatalf("err = %v", err)
	}
}

func TestConsentManagerDetect(t *testing.T) {
	var calls []call
	answer := `{"name":"Cookiebot","url":"https://help.example.test/cb","isConnected":true}`
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.detectConsentManager": func(call) *http.Response { return ok(answer) },
	}).handler(t))
	result, err := env.invoke("matomo.consent-manager.detect", "open", `{"idSite":1,"timeOut":5}`)
	if err != nil || !strings.Contains(result, `"detected":true`) || !strings.Contains(result, `"name":"Cookiebot"`) ||
		!strings.Contains(result, `"is_connected":true`) || !strings.Contains(result, `"url":"https://help.example.test/cb"`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := calls[len(calls)-1].form; got.Get("timeOut") != "5" || got.Get("idSite") != "1" || got.Has("url") {
		t.Fatalf("form = %v", got)
	}
	for _, none := range []string{`null`, `[]`, `{"value":null}`} {
		answer = none
		result, err := env.invoke("matomo.consent-manager.detect", "open", `{"idSite":1}`)
		if err != nil || result != `{"detected":false,"idSite":1}` {
			t.Errorf("answer %s: result = %s, err = %v", none, result, err)
		}
		if calls[len(calls)-1].form.Get("timeOut") != "20" {
			t.Errorf("default timeOut = %q", calls[len(calls)-1].form.Get("timeOut"))
		}
	}
	if _, err := env.invoke("matomo.consent-manager.detect", "open", `{"idSite":1,"timeOut":60}`); !isInvalidRequest(err) {
		t.Errorf("timeOut 60: err = %v", err)
	}
}

func TestSiteGroupsNeedTheInstanceTargetBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, versionOnly("5.14.2").handler(t))
	for _, connection := range []string{"open", "general", "one", "two"} {
		if _, err := env.invoke("matomo.site-groups.list", connection, `{}`); err == nil {
			t.Errorf("connection %s listed groups", connection)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Errorf("secret reads = %d, requests = %d", *env.reads, len(calls))
	}
	if ok, forms := reaches(siteGroupsList.ID, []string{"site"}); ok || len(forms) != 1 || forms[0] != "instance" {
		t.Errorf("reaches without instance = %v %v", ok, forms)
	}
	if ok, _ := reaches(siteGroupsList.ID, []string{"instance"}); !ok {
		t.Error("reaches refuses instance")
	}
}

func TestSiteGroupsListPagesNamesAndFiltersSitesByTargets(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getSitesGroups":    func(call) *http.Response { return ok(`["","Shops","Blogs"]`) },
		"SitesManager.getSitesFromGroup": func(call) *http.Response { return ok(siteList("1", "2", "3")) },
	}).handler(t))
	result, err := env.invoke("matomo.site-groups.list", "instance", `{"limit":2}`)
	if err != nil || strings.Contains(result, `"sites"`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if !strings.Contains(result, `"groups":["","Shops"]`) || !strings.Contains(result, `"has_more":true`) {
		t.Fatalf("result = %s", result)
	}
	// Only website 2 is bound besides the instance, so the rows of the group shrink to it.
	result, err = env.invoke("matomo.site-groups.list", "mixed", `{"group":"Shops"}`)
	if err != nil || strings.Contains(result, `"id":1,`) || strings.Contains(result, `"id":3,`) ||
		!strings.Contains(result, `"id":2,`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := calls[len(calls)-1].form.Get("group"); got != "Shops" {
		t.Errorf("group = %q", got)
	}
	// An instance-only binding reaches no website, so no row of a group is shown.
	result, err = env.invoke("matomo.site-groups.list", "instance", `{"group":""}`)
	if err != nil || strings.Contains(result, `"id":`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}

func TestSiteGroupsRequireSuperUser(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"SitesManager.getSitesGroups": func(call) *http.Response { return failure(bodyCanary) },
	}).handler(t))
	_, err := env.invoke("matomo.site-groups.list", "instance", `{}`)
	if err == nil || strings.Contains(err.Error(), bodyCanary) {
		t.Fatalf("err = %v", err)
	}
}
