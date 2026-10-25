package brave

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const richKey = "86d06abffc884e9ea281a40f62e0a5a6"

// richEnvironment answers with body unless it is empty, then with a failed callback.
func richEnvironment(t *testing.T, body string) *environment {
	t.Helper()
	if body == "" {
		body = `{"type":"rich","response_callback_info":{"vertical":"weather","callback_key":"` + richKey +
			`","callback_status":"failure"}}`
	}
	return newEnvironment(t, &fakeBrave{answer: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "1, 15000")
		w.Header().Set("X-RateLimit-Policy", "1;w=1, 15000;w=2592000")
		w.Header().Set("X-RateLimit-Remaining", "1, 14523")
		w.Header().Set("X-RateLimit-Reset", "1, 1234567")
		fmt.Fprint(w, body)
	}})
}

func (e *environment) rich(t *testing.T, arguments string) map[string]any {
	t.Helper()
	result, err := e.invoke("brave.rich.get", "search", arguments)
	if err != nil {
		t.Fatalf("invoke %s = %v", arguments, err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("result %s: %v", result, err)
	}
	return got
}

func (e *environment) richKey(t *testing.T) map[string]any {
	t.Helper()
	return e.rich(t, `{"callback_key":"`+richKey+`"}`)
}

// richAnswer wraps the object of one vertical in the envelope Brave documents.
func richAnswer(vertical, object string) string {
	return `{"type":"rich","results":[{"type":"rich","subtype":"` + vertical + `","provider":{"type":"external",` +
		`"name":"Provider","url":"https://provider.example","long_name":"Provider Inc","img":"https://provider.example/i.png"},` +
		`"language":"en","` + vertical + `":` + object + `,"added_later":1}],"response_callback_info":{"vertical":"` +
		vertical + `","callback_key":"` + richKey + `","callback_status":"success","search_lang":"en"},"added_later":1}`
}

// richVerticals holds one fixture per vertical with documented members of every kind it has.
var richVerticals = map[string]string{
	"calculator": `{"expression":"2+2","answer":4}`,
	"cryptocurrency": `{"intent_type":"price","quote":{"id":"bitcoin","symbol":"btc","name":"Bitcoin",` +
		`"image":"https://c.example/btc.png","current_price":64000.5,"market_cap_rank":1,"ath_date":"2026-01-01"},` +
		`"timeseries":{"time_range":"1d","ts_price":[[1700000000,64000.5],[1700003600,64100]]},"vs_currency":"usd",` +
		`"vs_currencies":[{"label":"US Dollar","value":"usd"}],"amount":2,"currency":{"code":"USD","decimals":2}}`,
	"currency": `{"conversion":{"query":{"from_currency":{"full_name":"Euro","code":"EUR","decimals":2},` +
		`"amount":10},"info":{"rate":1.08,"timestamp":1700000000},"date":"2026-10-01","result":10.8},` +
		`"timeseries":{"time_range":"1m","ts_exchange_rate":[[1700000000,1.08]]},` +
		`"supported_currencies":[{"full_name":"Euro","code":"EUR","decimals":2}]}`,
	"definitions": `{"word":"run","language":"en","pronounciation":"ruhn","definitions":[{"part_of_speech":"verb",` +
		`"definitions":[{"text":"to move fast","example_uses":["run home"],"related_words":["sprint"],` +
		`"labels":[{"text":"informal","type":"register"}]}]}],"source_dict":"wiktionary",` +
		`"attribution_text":"from Wordnik","attribution_url":"https://wordnik.com","audio_available":false}`,
	"formula1": `{"default_view":"calendar","calendar":{"races":[{"id":1,"name":"Grand Prix","date":"2026-03-15"}],` +
		`"next_race":{"id":1,"competition":{"name":"Grand Prix","location":{"country":"Australia","city":"Melbourne"}},` +
		`"laps":{"current":0,"total":58},"distance":305.3,"date":"2026-03-15","status":"Scheduled"}}}`,
	"news": `{"is_topical":true,"topic_id":3,"topics":[{"index":0,"title":"Topic","score":0.5,"query":"topic"}],` +
		`"articles":[{"title":"News","url":"https://n.example/a","img":"https://n.example/i.jpg",` +
		`"meta_url":{"scheme":"https","netloc":"n.example","hostname":"n.example","favicon":"https://n.example/f.ico",` +
		`"path":"› a"},"publisher_name":"N","publish_time":1700000000,"is_live":false,"extra_snippets":["more"],` +
		`"bo_debug":{"rank":1}}]}`,
	"packagetracker": `{"matched":[{"tracking_number":"TRK123","tracking_url":"https://t.example/TRK123",` +
		`"description":"Parcel","courier":{"name":"Post","service":"Express","code":"post","domain":"post.example"}}]}`,
	"american_football": sportFixture("american_football", `{"id":"g1","score":{"home":21,"away":17},`+
		`"score_table":{"q1":{"home":7,"away":3}},"phase":"final"}`),
	"football": `{"sport":"football","type":"match","view":"match","content":{"league":{"id":"l1","name":"Premier"},` +
		`"id":"m1","start_time":1700000000,"teams":{"home":{"id":1,"name":"Home"},"away":{"id":2,"name":"Away"}},` +
		`"statistics":[{"home":55,"away":45}]}}`,
	"baseball":   sportFixture("baseball", `{"id":1,"score":{"home":3,"away":2}}`),
	"basketball": sportFixture("basketball", `{"id":1,"score":{"home":98,"away":91}}`),
	"ice_hockey": sportFixture("ice_hockey", `{"id":1,"score":{"home":4,"away":1}}`),
	"cricket": sportFixture("cricket", `{"id":"c1","score":{"home":"180/4","away":{"runs":150},"home_runs":"180",`+
		`"home_overs":"20"}}`),
	"stock": `{"asset_info":{"symbol":"ACME","name":"Acme Corp","exchange":"NYSE"},"time_range":"1d",` +
		`"quote":{"symbol":"ACME","latest_price":12.5,"volume":1000,"market_cap":123456789},` +
		`"timeseries":{"time_range":"1d","timeseries":[{"date":"2026-01-02","close":12.5,"volume":10.5}]},` +
		`"exchange_info":{"timezone":"America/New_York","open_time":"09:30","close_time":"16:00"}}`,
	"unitconversion": `{"amount":3.5,"from_unit":"km","to_unit":"mi","dimensionality":"length"}`,
	"unixtimestamp":  `{"conversion":{"intent_type":"to_date","input_ts":1700000000}}`,
	"weather": `{"location":{"id":2867714,"name":"Munich","coords":{"lat":48.1,"lon":11.5},"tzoffset":3600},` +
		`"current_time_iso":"2026-10-25T10:00:00Z","current_weather":{"ts":1700000000,"temp":12.5,` +
		`"wind":{"speed":3.2,"deg":270},"weather":{"id":803,"main":"Clouds","icon":"04d"}},` +
		`"daily":[{"ts":1700000000,"date_i18n":"Mon"}],"alerts":[{"sender":"Service","tags":["Wind"]}]}`,
}

func sportFixture(sport, game string) string {
	return `{"sport":"` + sport + `","type":"games","view":"games","content":{"type":"games","league":{"id":7,` +
		`"name":"League","logo":"https://s.example/l.png","has_standings":true},"game_times":[1700000000],` +
		`"game_time":1700000000,"date":"2026-10-25","date_kind":"today","games":[` + addTeams(game) + `]}}`
}

// addTeams adds the two teams every game of the sports fixtures has.
func addTeams(game string) string {
	return strings.TrimSuffix(game, "}") + `,"teams":{"home":{"id":1,"name":"Home","nickname":"H","location":"Town",` +
		`"logo":"https://s.example/h.png"},"away":{"id":2,"name":"Away"}},"start_time":1700000000}`
}

func TestRichGetShowsEveryVertical(t *testing.T) {
	if len(richVerticals) != 17 {
		t.Fatalf("%d fixtures, want one per vertical", len(richVerticals))
	}
	for vertical, object := range richVerticals {
		t.Run(vertical, func(t *testing.T) {
			got := richEnvironment(t, richAnswer(vertical, object)).richKey(t)
			var want any
			if err := json.Unmarshal([]byte(object), &want); err != nil {
				t.Fatal(err)
			}
			result := pick(t, got, "results", 0).(map[string]any)
			if !reflect.DeepEqual(result[vertical], want) {
				t.Errorf("%s = %v\nwant %v", vertical, result[vertical], want)
			}
			if got["type"] != "rich" || result["type"] != "rich" || result["subtype"] != vertical || result["language"] != "en" ||
				pick(t, result, "provider", "long_name") != "Provider Inc" ||
				pick(t, result, "provider", "type") != "external" || pick(t, result, "provider", "name") != "Provider" ||
				pick(t, result, "provider", "url") != "https://provider.example" ||
				pick(t, result, "provider", "img") != "https://provider.example/i.png" {
				t.Errorf("envelope = %v", result)
			}
			if _, ok := got["added_later"]; ok {
				t.Errorf("an unknown member is shown")
			}
			if _, ok := result["added_later"]; ok || len(result) != 5 {
				t.Errorf("result = %v, want type, subtype, provider, language, and the vertical", result)
			}
			if !reflect.DeepEqual(got["response_callback_info"], map[string]any{"vertical": vertical,
				"callback_key": richKey, "callback_status": "success", "search_lang": "en"}) {
				t.Errorf("response_callback_info = %v", got["response_callback_info"])
			}
			if pick(t, got, "quota", "windows", 1, "remaining") != float64(14523) {
				t.Errorf("quota = %v", got["quota"])
			}
		})
	}
}

func TestRichGetReportsAFailedCallback(t *testing.T) {
	got := richEnvironment(t, "").richKey(t)
	if pick(t, got, "response_callback_info", "callback_status") != "failure" || got["results"] != nil {
		t.Errorf("failure = %v", got)
	}
	for name, body := range map[string]string{
		"empty object": `{}`, "null": `null`, "null members": `{"type":null,"results":null,"response_callback_info":null}`,
		"empty results": `{"type":"rich","results":[]}`, "wrong types": `{"results":"x","response_callback_info":[1]}`,
		"sparse result": `{"results":[null,1,{"subtype":"weather","weather":null,"provider":{"url":"x"}}]}`,
	} {
		if got := richEnvironment(t, body).richKey(t); got == nil {
			t.Errorf("%s: no result", name)
		}
	}
}

// rowNode is a documented member of the answer, read from the reference rows in testdata.
type rowNode struct {
	typ  string
	kids map[string]*rowNode
	keys []string
}

func readReference(t *testing.T) (*rowNode, int) {
	t.Helper()
	raw, err := os.ReadFile("testdata/rich-fields.txt")
	if err != nil {
		t.Fatal(err)
	}
	root := &rowNode{kids: map[string]*rowNode{}}
	rows := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		path, typ, _ := strings.Cut(line, "\t")
		node := root
		parts := strings.Split(path, ".")
		for i, part := range parts {
			part = strings.TrimSuffix(part, "[]")
			child := node.kids[part]
			if child == nil {
				child = &rowNode{kids: map[string]*rowNode{}}
				node.kids[part] = child
				node.keys = append(node.keys, part)
			}
			if i == len(parts)-1 {
				child.typ = typ
			}
			node = child
		}
		rows++
	}
	return root, rows
}

// value builds a response member from the documented type: every address-like text is a safe address, so
// that the same text passes every kind of string.
func (n *rowNode) value() any {
	typ := strings.TrimSuffix(n.typ, "?")
	list := strings.HasSuffix(typ, "[]")
	typ = strings.TrimSuffix(typ, "[]")
	var one any
	switch {
	case len(n.kids) > 0:
		object := map[string]any{}
		for _, key := range n.keys {
			object[key] = n.kids[key].value()
		}
		one = object
	case typ == "string":
		one = "https://e.example/x"
	case typ == "int":
		one = float64(7)
	case typ == "number":
		one = 1.5
	case typ == "bool":
		one = true
	default:
		one = map[string]any{"k": []any{"v", 1.0}}
	}
	if list {
		return []any{one}
	}
	return one
}

// The reference documents 1169 members of the answer. A response that fills every one of them comes back
// unchanged, so no documented member is lost.
func TestRichGetShowsEveryDocumentedMember(t *testing.T) {
	root, rows := readReference(t)
	if rows != 1169 {
		t.Fatalf("%d documented members, the reference lists 1169", rows)
	}
	full := root.value().(map[string]any)
	encoded, _ := json.Marshal(full)
	got := richEnvironment(t, string(encoded)).richKey(t)
	delete(got, "quota")
	if !reflect.DeepEqual(got, full) {
		t.Errorf("the result differs from a response that fills every documented member")
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(richOutputSchema), &schema); err != nil {
		t.Fatalf("output schema: %v", err)
	}
	// Every member of the shape is documented, too: the shape adds nothing the reference lacks.
	if extra := undocumented(richAnswerShape, root); len(extra) > 0 {
		t.Errorf("members of the shape that the reference does not list: %v", extra)
	}
}

// undocumented lists the members of a shape that the reference tree lacks.
func undocumented(s *shape, node *rowNode) []string {
	for s.kind == kArr {
		s = s.elem
	}
	if s.kind != kObj {
		return nil
	}
	var out []string
	for _, property := range s.props {
		child, ok := node.kids[property.name]
		if !ok {
			out = append(out, property.name)
			continue
		}
		for _, name := range undocumented(property.shape, child) {
			out = append(out, property.name+"."+name)
		}
	}
	return out
}

func TestRichGetSendsOnlyTheKey(t *testing.T) {
	f := &fakeBrave{answer: func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"type":"rich"}`) }}
	env := newEnvironment(t, f)
	if _, err := env.invoke("brave.rich.get", "search", `{"callback_key":"`+richKey+`"}`); err != nil {
		t.Fatalf("invoke = %v", err)
	}
	r := f.recorded()[0]
	if r.method != http.MethodGet || r.path != "/res/v1/web/rich" ||
		!reflect.DeepEqual(r.query, url.Values{"callback_key": {richKey}}) ||
		r.header.Get("X-Subscription-Token") != tokenValue || r.header.Get("Accept") != "application/json" ||
		r.header.Get("User-Agent") != "qatlas-cli" || r.header.Get("Cache-Control") != "" ||
		strings.Contains(r.rawQuery, tokenValue) {
		t.Errorf("request = %+v", r)
	}
	if _, err := env.invoke("brave.rich.get", "search", `{"callback_key":"`+richKey+`","no_cache":true}`); err != nil {
		t.Fatal(err)
	}
	if r = f.recorded()[1]; r.header.Get("Cache-Control") != "no-cache" || r.query.Get("callback_key") != richKey {
		t.Errorf("no_cache request = %+v", r)
	}
	if _, err := env.invoke("brave.rich.get", "search", `{"callback_key":"`+richKey+`","no_cache":false}`); err != nil {
		t.Fatal(err)
	}
	if r = f.recorded()[2]; r.header.Get("Cache-Control") != "" {
		t.Errorf("no_cache false sent a header: %+v", r)
	}
	// The edges of a callback key.
	for _, key := range []string{"a", strings.Repeat("A", 128), "a-b_C9"} {
		if _, err := env.invoke("brave.rich.get", "search", `{"callback_key":"`+key+`"}`); err != nil {
			t.Errorf("key %q: %v", key, err)
		}
	}
}

func TestRichGetRefusesInvalidArgumentsBeforeTheKey(t *testing.T) {
	for name, arguments := range map[string]string{
		"missing":          `{}`,
		"empty":            `{"callback_key":""}`,
		"null":             `{"callback_key":null}`,
		"too long":         `{"callback_key":"` + strings.Repeat("a", 129) + `"}`,
		"address":          `{"callback_key":"https://rich.example/key"}`,
		"query injection":  `{"callback_key":"a&b=c"}`,
		"slash":            `{"callback_key":"a/b"}`,
		"path traversal":   `{"callback_key":"../x"}`,
		"percent":          `{"callback_key":"a%20b"}`,
		"space":            `{"callback_key":"a b"}`,
		"padding":          `{"callback_key":" abc"}`,
		"newline":          `{"callback_key":"abc\n"}`,
		"control":          `{"callback_key":"a\u0000b"}`,
		"non ascii letter": `{"callback_key":"ä"}`,
		"number":           `{"callback_key":12}`,
		"unknown argument": `{"callback_key":"a","vertical":"weather"}`,
		"url":              `{"callback_key":"a","url":"https://x.example"}`,
		"no_cache text":    `{"callback_key":"a","no_cache":"yes"}`,
	} {
		f := &fakeBrave{}
		env := newEnvironment(t, f)
		_, err := env.invoke("brave.rich.get", "search", arguments)
		if !isInvalidRequest(err) || *env.reads != 0 || len(f.recorded()) != 0 {
			t.Errorf("%s: err = %v, reads = %d, requests = %d, want invalid-request before the key",
				name, err, *env.reads, len(f.recorded()))
		}
	}
	// A refused key never echoes the value.
	_, err := newEnvironment(t, &fakeBrave{}).invoke("brave.rich.get", "search", `{"callback_key":"bad key canary"}`)
	if err == nil || strings.Contains(err.Error(), "canary") {
		t.Errorf("err = %v, want a refusal that does not quote the key", err)
	}
}

// An expired or unknown callback key ends with whatever status Brave gives; none of them carries Brave's
// text or the callback key.
func TestRichGetStatusesMapToClassesWithoutProviderText(t *testing.T) {
	for _, tc := range []struct {
		status int
		class  provider.Class
	}{
		{http.StatusBadRequest, provider.ClassInvalidResponse},
		{http.StatusUnauthorized, provider.ClassAuth},
		{http.StatusPaymentRequired, provider.ClassPermission},
		{http.StatusForbidden, provider.ClassPermission},
		{http.StatusNotFound, provider.ClassNotFound},
		{http.StatusUnprocessableEntity, provider.ClassInvalidResponse},
		{http.StatusTooManyRequests, provider.ClassRateLimited},
		{http.StatusInternalServerError, provider.ClassProviderError},
		{http.StatusBadGateway, provider.ClassUnreachable},
		{http.StatusServiceUnavailable, provider.ClassUnreachable},
		{http.StatusGatewayTimeout, provider.ClassTimeout},
	} {
		env := newEnvironment(t, &fakeBrave{answer: reply(tc.status, nil, errorBody(tc.status))})
		_, err := env.invoke("brave.rich.get", "search", `{"callback_key":"expired-key-canary"}`)
		if classOf(err) != tc.class {
			t.Errorf("HTTP %d: class = %q (%v), want %q", tc.status, classOf(err), err, tc.class)
			continue
		}
		for _, leak := range []string{detailCanary, tokenValue, "expired-key-canary", "api.search.brave.com", "VALIDATION"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("HTTP %d: error %q leaks %q", tc.status, err, leak)
			}
		}
	}
}

func TestRichGetAnswersThatCannotBeReadAreInvalid(t *testing.T) {
	var big strings.Builder
	big.WriteString(`{"type":"`)
	big.WriteString(strings.Repeat("a", maxResponseBytes))
	big.WriteString(`"}`)
	for name, answer := range map[string]func(http.ResponseWriter, *http.Request){
		"not json":  reply(200, nil, "<html>maintenance</html>"),
		"empty":     reply(200, nil, ""),
		"an array":  reply(200, nil, `[1,2]`),
		"too large": reply(200, nil, big.String()),
	} {
		env := newEnvironment(t, &fakeBrave{answer: answer})
		_, err := env.invoke("brave.rich.get", "search", `{"callback_key":"`+richKey+`"}`)
		if classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s: err = %v, want invalid-provider-response", name, err)
		}
	}
}

func TestRichGetTextAndListsAreBounded(t *testing.T) {
	long := func(n int) string { return strings.Repeat("ä", n) }
	var games, points []string
	for i := 0; i < 150; i++ {
		games = append(games, fmt.Sprintf(`{"id":%d}`, i))
	}
	for i := 0; i < 600; i++ {
		points = append(points, fmt.Sprintf(`[%d,1.5]`, i))
	}
	deep := `{"a":{"b":{"c":{"d":{"e":{"f":{"g":{"h":1}}}}}}}}`
	shallow := `{"a":{"b":{"c":{"d":{"e":1}}}}}`
	body := `{"type":"` + long(500) + `","results":[{"subtype":"` + long(500) + `","provider":{"name":"` + long(900) + `",` +
		`"url":"javascript:alert(1)","img":"` + userinfoURL("e.example", "/i.png") + `","long_name":"` + long(900) + `"},` +
		`"calculator":{"expression":"` + long(5000) + `","answer":` + shallow + `},` +
		`"definitions":{"word":"` + long(900) + `","attribution_text":"` + long(5000) + `","attribution_url":"data:text/html,x",` +
		`"definitions":[{"definitions":[{"example_uses":[` + strings.TrimSuffix(strings.Repeat(`"e",`, 150), ",") + `]}]}]},` +
		`"cricket":{"content":{"games":[` + strings.Join(games, ",") + `]}},` +
		`"cryptocurrency":{"timeseries":{"ts_price":[` + strings.Join(points, ",") + `]},"amount":"file:///etc/passwd"},` +
		`"packagetracker":{"matched":[{"tracking_url":"/relative","description":"` + long(5000) + `"}]},` +
		`"news":{"articles":[{"url":"https://e.example/` + strings.Repeat("p", 3000) + `","img":"https://e.example/a b.png",` +
		`"title":"` + long(900) + `","publish_time":1.9,"is_live":"yes","bo_debug":{"ok":"https://e.example/","bad":"javascript:x","deep":` + deep + `}}]}}]}`
	got := richEnvironment(t, body).richKey(t)
	runes := func(s any) int { return len([]rune(s.(string))) }
	result := pick(t, got, "results", 0).(map[string]any)
	if runes(got["type"]) != 201 || runes(result["subtype"]) != 201 || runes(pick(t, result, "provider", "name")) != 301 ||
		runes(pick(t, result, "provider", "long_name")) != 301 ||
		runes(pick(t, result, "calculator", "expression")) != 1501 ||
		runes(pick(t, result, "definitions", "word")) != 301 ||
		runes(pick(t, result, "definitions", "attribution_text")) != 1501 ||
		runes(pick(t, result, "packagetracker", "matched", 0, "description")) != 1501 ||
		runes(pick(t, result, "news", "articles", 0, "title")) != 301 {
		t.Errorf("text lengths are not cut")
	}
	if !strings.HasSuffix(got["type"].(string), "…") {
		t.Errorf("a cut text carries no mark")
	}
	// Lists: schedules at 100, a time series at 500, a list of texts inside a result at 100.
	if len(pick(t, result, "cricket", "content", "games").([]any)) != 100 ||
		len(pick(t, result, "cryptocurrency", "timeseries", "ts_price").([]any)) != 500 ||
		len(pick(t, result, "definitions", "definitions", 0, "definitions", 0, "example_uses").([]any)) != 100 {
		t.Errorf("lists are not cut")
	}
	// Addresses that are not safe are left out, a text in an address place included.
	provider := result["provider"].(map[string]any)
	article := pick(t, result, "news", "articles", 0).(map[string]any)
	if provider["url"] != nil || provider["img"] != nil || pick(t, result, "definitions", "attribution_url") != nil ||
		pick(t, result, "packagetracker", "matched", 0, "tracking_url") != nil || article["url"] != nil || article["img"] != nil {
		t.Errorf("an unsafe address is shown: %v / %v", provider, article)
	}
	// A member of another type than documented is left out, a number is made whole.
	if article["is_live"] != nil || article["publish_time"] != float64(1) {
		t.Errorf("types: %v %v", article["is_live"], article["publish_time"])
	}
	// Open members are bounded as JSON: depth (a chain cut at the limit ends empty and disappears with its
	// parents), text, and addresses.
	if !reflect.DeepEqual(pick(t, result, "calculator", "answer"), map[string]any{"a": map[string]any{"b": map[string]any{
		"c": map[string]any{"d": map[string]any{"e": float64(1)}}}}}) ||
		pick(t, result, "cryptocurrency", "amount") != nil ||
		!reflect.DeepEqual(article["bo_debug"], map[string]any{"ok": "https://e.example/"}) {
		t.Errorf("open members = %v / %v / %v", pick(t, result, "calculator", "answer"),
			pick(t, result, "cryptocurrency", "amount"), article["bo_debug"])
	}
}

func TestRichGetBetweenTheKeyAndTheResult(t *testing.T) {
	body := richAnswer("calculator", `{"expression":"Bearer `+tokenValue+`"}`)
	result, err := richEnvironment(t, body).invoke("brave.rich.get", "search", `{"callback_key":"`+richKey+`"}`)
	if err != nil || strings.Contains(result, tokenValue) {
		t.Errorf("result %s, err %v: the key reached the output", result, err)
	}
}

func TestRichGetIsListedAndDescribesItsCost(t *testing.T) {
	descriptor := richGet
	if descriptor.ID != "brave.rich.get" || descriptor.Risk != readRisk {
		t.Fatalf("descriptor = %s / %+v", descriptor.ID, descriptor.Risk)
	}
	for _, want := range []string{"Search plan", "billed", "0.005", "brave.web.search", "enable_rich_callback",
		"rich.hint.callback_key", "attribution", "terms of use", "untrusted", "lifetime"} {
		if !strings.Contains(descriptor.Description, want) {
			t.Errorf("description lacks %q: %s", want, descriptor.Description)
		}
	}
	var input struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(descriptor.InputSchema, &input); err != nil {
		t.Fatal(err)
	}
	named := map[string]bool{}
	for _, argument := range descriptor.Arguments {
		named[argument.Name] = true
		if _, ok := input.Properties[argument.Name]; !ok {
			t.Errorf("argument %s is not in the input schema", argument.Name)
		}
	}
	if len(input.Properties) != 2 || !named["callback_key"] || !named["no_cache"] ||
		!reflect.DeepEqual(input.Required, []string{"callback_key"}) {
		t.Errorf("arguments = %v, required %v, want callback_key and no_cache", named, input.Required)
	}
	for _, field := range descriptor.Fields {
		if field.Description == "" {
			t.Errorf("field %s has no description", field.Name)
		}
	}
	for _, vertical := range []string{"calculator", "cryptocurrency", "currency", "definitions", "formula1", "news",
		"packagetracker", "american_football", "football", "baseball", "basketball", "ice_hockey", "cricket", "stock",
		"unitconversion", "unixtimestamp", "weather"} {
		if !strings.Contains(fieldText(descriptor.Fields, "results"), vertical) {
			t.Errorf("results does not name the vertical %s", vertical)
		}
	}
}

func fieldText(fields []capability.Field, name string) string {
	for _, field := range fields {
		if field.Name == name {
			return field.Description
		}
	}
	return ""
}
