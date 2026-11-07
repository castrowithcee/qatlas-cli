package gen

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/httpbind"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/restjson"
)

func loadRest(t *testing.T, table Table) *Model {
	t.Helper()
	p, err := LoadPin(ModelDir)
	if err != nil {
		t.Fatal(err)
	}
	m, err := LoadTableModel(ModelDir, p, table)
	if err != nil {
		t.Fatal(err)
	}
	if m.Protocol != ProtocolRestJSON {
		t.Fatalf("%s speaks %s", table.Service, m.Protocol)
	}
	return m
}

func describeBinding(b *RestBinding) string {
	return b.Operation.Method + " " + b.Operation.URI + " code " + strconv.Itoa(b.Code)
}

// Every operation of both real models binds to the runtime, and the table of each lists exactly these
// operations in defer: nothing is a tool yet, nothing is missing.
func TestEveryOperationOfTheRestModelsBinds(t *testing.T) {
	for _, c := range []struct {
		table Table
		count int
	}{{accountTable, 18}, {sesv2Table, 116}} {
		m := loadRest(t, c.table)
		ops, err := m.Operations()
		if err != nil {
			t.Fatal(err)
		}
		if len(ops) != c.count || len(c.table.Defer) != c.count || len(c.table.Operations) != 0 || len(c.table.Exclude) != 0 {
			t.Errorf("%s: %d operations in the model, %d deferred, %d tools", c.table.Service, len(ops), len(c.table.Defer), len(c.table.Operations))
		}
		for _, op := range ops {
			b, err := m.BindRest(op)
			if err != nil {
				t.Errorf("%s: %v", c.table.Service, err)
				continue
			}
			if err := b.Operation.Validate(); err != nil {
				t.Errorf("%s %s: %v", c.table.Service, op, err)
			}
			if b.Code != 200 {
				t.Errorf("%s %s: success code %d", c.table.Service, op, b.Code)
			}
		}
	}
}

func TestProtocolIsReadFromTheService(t *testing.T) {
	if m := stsModel(t); m.Protocol != ProtocolQuery {
		t.Errorf("sts speaks %s", m.Protocol)
	}
	loadRest(t, accountTable)
	loadRest(t, sesv2Table)
	for name, traits := range map[string]string{
		"both":    `"aws.protocols#awsQuery":{},"aws.protocols#restJson1":{}`,
		"neither": `"aws.protocols#restXml":{}`,
	} {
		data := `{"smithy":"2.0","shapes":{"com.example#S":{"type":"service","version":"2020-01-01","traits":{` + traits + `}}}}`
		if _, err := LoadModel([]byte(data)); err == nil {
			t.Errorf("%s: the model was accepted", name)
		}
	}
	// A restJson1 model is not rendered as awsQuery tools.
	table := accountTable
	table.Operations = []Operation{{Operation: "GetPrimaryEmail", Object: "primary-email", Action: "get", Effect: "read",
		Idempotency: "safe", Title: "t", Description: "d", DataSensitivity: "s", TargetCheck: "none", PolicyCheck: "none"}}
	table.Defer = slices.DeleteFunc(slices.Clone(table.Defer), func(o Omission) bool { return o.Operation == "GetPrimaryEmail" })
	if _, err := Render(ModelDir, []Table{table}); err == nil || !strings.Contains(err.Error(), "GetPrimaryEmail") {
		t.Errorf("a restJson1 tool was rendered: %v", err)
	}
}

// restModel is a small restJson1 model with every binding trait, and the operations that must stop the
// generation.
const restModel = `{"smithy":"2.0","shapes":{
"com.example#Svc":{"type":"service","version":"2020-01-01","operations":[
 {"target":"com.example#Put"},{"target":"com.example#Make"},{"target":"com.example#Page"},
 {"target":"com.example#TwoPlaces"},{"target":"com.example#Streaming"},{"target":"com.example#GetBody"},
 {"target":"com.example#Redirect"},{"target":"com.example#Loop"},{"target":"com.example#NoLabel"},
 {"target":"com.example#BadFormat"},{"target":"com.example#Big"},{"target":"com.example#PagingBad"}],
 "resources":[{"target":"com.example#Res"}],
 "traits":{"aws.protocols#restJson1":{},"smithy.api#paginated":{"pageSize":"Max"}}},
"com.example#Res":{"type":"resource","operations":[{"target":"com.example#ViaResource"}]},
"com.example#ViaResource":{"type":"operation","input":{"target":"smithy.api#Unit"},"output":{"target":"smithy.api#Unit"},
 "traits":{"smithy.api#http":{"method":"POST","uri":"/via"}}},

"com.example#Put":{"type":"operation","input":{"target":"com.example#PutIn"},"output":{"target":"com.example#PutOut"},
 "traits":{"smithy.api#http":{"method":"PUT","uri":"/things/{Id}/{Path+}?fixed=1","code":201}}},
"com.example#PutIn":{"type":"structure","members":{
 "Id":{"target":"smithy.api#String","traits":{"smithy.api#httpLabel":{},"smithy.api#required":{}}},
 "Path":{"target":"smithy.api#String","traits":{"smithy.api#httpLabel":{},"smithy.api#required":{}}},
 "Mode":{"target":"com.example#Mode","traits":{"smithy.api#httpQuery":"mode"}},
 "Tags":{"target":"com.example#Strings","traits":{"smithy.api#httpQuery":"tag"}},
 "Extra":{"target":"com.example#StringMap","traits":{"smithy.api#httpQueryParams":{}}},
 "Trace":{"target":"smithy.api#String","traits":{"smithy.api#httpHeader":"X-Trace-Id"}},
 "Meta":{"target":"com.example#StringMap","traits":{"smithy.api#httpPrefixHeaders":"x-meta-"}},
 "When":{"target":"smithy.api#Timestamp","traits":{"smithy.api#httpHeader":"X-When","smithy.api#timestampFormat":"epoch-seconds"}},
 "Doc":{"target":"com.example#Doc","traits":{"smithy.api#httpPayload":{}}}}},
"com.example#PutOut":{"type":"structure","members":{
 "Location":{"target":"smithy.api#String","traits":{"smithy.api#httpHeader":"Location"}},
 "Status":{"target":"smithy.api#Integer","traits":{"smithy.api#httpResponseCode":{}}},
 "Item":{"target":"com.example#Doc","traits":{"smithy.api#httpPayload":{}}}}},
"com.example#Doc":{"type":"structure","members":{
 "Value":{"target":"smithy.api#String","traits":{"smithy.api#jsonName":"val"}},
 "Choice":{"target":"com.example#Choice"}}},
"com.example#Choice":{"type":"union","members":{"A":{"target":"smithy.api#String"},"B":{"target":"smithy.api#Integer"}}},
"com.example#Mode":{"type":"enum","members":{"X":{"target":"smithy.api#Unit","traits":{"smithy.api#enumValue":"x"}}}},
"com.example#Strings":{"type":"list","member":{"target":"smithy.api#String"}},
"com.example#StringMap":{"type":"map","key":{"target":"smithy.api#String"},"value":{"target":"smithy.api#String"}},

"com.example#Make":{"type":"operation","input":{"target":"com.example#MakeIn"},"output":{"target":"com.example#Empty"},
 "traits":{"smithy.api#http":{"method":"POST","uri":"/make"}}},
"com.example#MakeIn":{"type":"structure","members":{
 "ClientToken":{"target":"smithy.api#String","traits":{"smithy.api#idempotencyToken":{},"smithy.api#jsonName":"clientToken"}},
 "Stamp":{"target":"smithy.api#Timestamp","traits":{"smithy.api#timestampFormat":"date-time"}},
 "Any":{"target":"smithy.api#Document"},
 "Raw":{"target":"smithy.api#Blob"},
 "Short":{"target":"com.example#ShortName"},
 "Count":{"target":"smithy.api#Integer","traits":{"smithy.api#required":{}}}}},
"com.example#ShortName":{"type":"string","traits":{"smithy.api#length":{"max":10}}},
"com.example#Empty":{"type":"structure","members":{}},

"com.example#Page":{"type":"operation","input":{"target":"com.example#PageIn"},"output":{"target":"com.example#PageOut"},
 "traits":{"smithy.api#http":{"method":"POST","uri":"/page"},"smithy.api#paginated":{"inputToken":"Token","outputToken":"Next"}}},
"com.example#PageIn":{"type":"structure","members":{
 "Token":{"target":"smithy.api#String"},
 "Max":{"target":"com.example#MaxRange"},
 "Filter":{"target":"smithy.api#String"}}},
"com.example#MaxRange":{"type":"integer","traits":{"smithy.api#range":{"min":1,"max":25}}},
"com.example#PageOut":{"type":"structure","members":{"Next":{"target":"smithy.api#String"},"Things":{"target":"com.example#Strings"}}},

"com.example#TwoPlaces":{"type":"operation","input":{"target":"com.example#TwoPlacesIn"},"output":{"target":"smithy.api#Unit"},
 "traits":{"smithy.api#http":{"method":"POST","uri":"/two"}}},
"com.example#TwoPlacesIn":{"type":"structure","members":{"M":{"target":"smithy.api#String","traits":{"smithy.api#httpQuery":"m","smithy.api#httpHeader":"X-M"}}}},
"com.example#Streaming":{"type":"operation","input":{"target":"com.example#StreamingIn"},"output":{"target":"smithy.api#Unit"},
 "traits":{"smithy.api#http":{"method":"PUT","uri":"/stream"}}},
"com.example#StreamingIn":{"type":"structure","members":{"Data":{"target":"com.example#Stream","traits":{"smithy.api#httpPayload":{}}}}},
"com.example#Stream":{"type":"blob","traits":{"smithy.api#streaming":{}}},
"com.example#GetBody":{"type":"operation","input":{"target":"com.example#GetBodyIn"},"output":{"target":"smithy.api#Unit"},
 "traits":{"smithy.api#http":{"method":"GET","uri":"/get"}}},
"com.example#GetBodyIn":{"type":"structure","members":{"Filter":{"target":"smithy.api#String"}}},
"com.example#Redirect":{"type":"operation","input":{"target":"smithy.api#Unit"},"output":{"target":"smithy.api#Unit"},
 "traits":{"smithy.api#http":{"method":"POST","uri":"/r","code":301}}},
"com.example#Loop":{"type":"operation","input":{"target":"com.example#LoopIn"},"output":{"target":"smithy.api#Unit"},
 "traits":{"smithy.api#http":{"method":"POST","uri":"/loop"}}},
"com.example#LoopIn":{"type":"structure","members":{"Self":{"target":"com.example#LoopIn"}}},
"com.example#NoLabel":{"type":"operation","input":{"target":"smithy.api#Unit"},"output":{"target":"smithy.api#Unit"},
 "traits":{"smithy.api#http":{"method":"POST","uri":"/x/{Missing}"}}},
"com.example#BadFormat":{"type":"operation","input":{"target":"com.example#BadFormatIn"},"output":{"target":"smithy.api#Unit"},
 "traits":{"smithy.api#http":{"method":"POST","uri":"/f"}}},
"com.example#BadFormatIn":{"type":"structure","members":{"T":{"target":"smithy.api#Timestamp","traits":{"smithy.api#timestampFormat":"unix"}}}},
"com.example#Big":{"type":"operation","input":{"target":"com.example#BigIn"},"output":{"target":"smithy.api#Unit"},
 "traits":{"smithy.api#http":{"method":"POST","uri":"/b"}}},
"com.example#BigIn":{"type":"structure","members":{"N":{"target":"smithy.api#BigInteger"}}},
"com.example#PagingBad":{"type":"operation","input":{"target":"com.example#PageIn"},"output":{"target":"com.example#PageOut"},
 "traits":{"smithy.api#http":{"method":"POST","uri":"/pb"},"smithy.api#paginated":{"inputToken":"Nope","outputToken":"Next"}}}
}}`

func syntheticRest(t *testing.T) *Model {
	t.Helper()
	m, err := LoadModel([]byte(restModel))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// Each trait of the model lands where the Smithy HTTP binding rules put it, and resource operations count.
func TestBindRestMapsTheTraits(t *testing.T) {
	m := syntheticRest(t)
	ops, _ := m.Operations()
	if !slices.Contains(ops, "ViaResource") || len(ops) != 13 {
		t.Errorf("operations = %v", ops)
	}
	b, err := m.BindRest("Put")
	if err != nil {
		t.Fatal(err)
	}
	if b.Operation.Method != "PUT" || b.Operation.URI != "/things/{Id}/{Path+}?fixed=1" || b.Code != 201 || b.Pager != nil {
		t.Errorf("binding = %s", describeBinding(b))
	}
	want := map[string]struct {
		loc      httpbind.Loc
		name     string
		required bool
	}{
		"Id": {httpbind.LocLabel, "Id", true}, "Path": {httpbind.LocLabel, "Path", true},
		"Mode": {httpbind.LocQuery, "mode", false}, "Tags": {httpbind.LocQuery, "tag", false},
		"Extra": {httpbind.LocQueryParams, "Extra", false}, "Trace": {httpbind.LocHeader, "X-Trace-Id", false},
		"Meta": {httpbind.LocPrefixHeaders, "x-meta-", false}, "When": {httpbind.LocHeader, "X-When", false},
		"Doc": {httpbind.LocPayload, "Doc", false},
	}
	for _, f := range b.Operation.Input.Fields {
		w := want[f.Key]
		if f.Loc != w.loc || f.Name != w.name || f.Required != w.required {
			t.Errorf("input %s = loc %d, name %q, required %t; want %+v", f.Key, f.Loc, f.Name, f.Required, w)
		}
	}
	if when := fieldByKey(b.Operation.Input, "When"); when.Shape.Format != httpbind.FormatEpochSeconds {
		t.Errorf("timestamp format = %d", when.Shape.Format)
	}
	doc := fieldByKey(b.Operation.Input, "Doc").Shape
	if doc.Fields[0].Name != "val" || doc.Fields[0].Key != "Value" || !doc.Fields[1].Shape.Union {
		t.Errorf("payload = %+v", doc)
	}
	if st := fieldByKey(b.Operation.Output, "Status"); st == nil || st.Loc != httpbind.LocResponseCode {
		t.Errorf("response code = %+v", st)
	}

	made, err := m.BindRest("Make")
	if err != nil {
		t.Fatal(err)
	}
	in := made.Operation.Input
	if f := fieldByKey(in, "ClientToken"); !f.IdempotencyToken || f.Name != "clientToken" || f.Loc != httpbind.LocBody {
		t.Errorf("token = %+v", f)
	}
	if fieldByKey(in, "Stamp").Shape.Format != httpbind.FormatDateTime || fieldByKey(in, "Any").Shape.Kind != httpbind.KindDocument ||
		fieldByKey(in, "Raw").Shape.Kind != httpbind.KindBlob || !fieldByKey(in, "Count").Required ||
		fieldByKey(in, "Short").Shape.MaxLen != 10 {
		t.Errorf("body members = %+v", in.Fields)
	}
}

// The paginated trait of the operation over the service's, with the paging members under their "~" keys.
func TestBindRestPaging(t *testing.T) {
	b, err := syntheticRest(t).BindRest("Page")
	if err != nil {
		t.Fatal(err)
	}
	if b.Pager == nil || *b.Pager != (httpbind.Pager{TokenIn: "Token", TokenOut: "~Next", SizeParam: "Max", MaxSize: 25}) {
		t.Fatalf("pager = %+v", b.Pager)
	}
	for _, key := range []string{"~Token", "~Max"} {
		if fieldByKey(b.Operation.Input, key) == nil {
			t.Errorf("input lacks %s", key)
		}
	}
	if fieldByKey(b.Operation.Output, "~Next") == nil || fieldByKey(b.Operation.Output, "Things") == nil {
		t.Errorf("output = %+v", b.Operation.Output.Fields)
	}
	page, err := b.Pager.Start([]byte("bound"), 10, "")
	if err != nil {
		t.Fatal(err)
	}
	args, err := httpbind.ApplyPage(b.Operation.Input, map[string]any{"Filter": "f"}, page)
	if err != nil {
		t.Fatal(err)
	}
	req, err := b.Operation.Encode(restjson.Codec{}, args, nil)
	if err != nil || (string(req.Body) != `{"Max":10,"Filter":"f"}` && string(req.Body) != `{"Filter":"f","Max":10}`) {
		t.Errorf("body = %s, err = %v", req.Body, err)
	}
}

// What the model leaves open or the runtime does not carry stops the binding and names the operation.
func TestBindRestRefusesWhatItCannotBind(t *testing.T) {
	m := syntheticRest(t)
	for op, want := range map[string]string{
		"TwoPlaces":  "more than one binding trait",
		"Streaming":  "smithy.api#streaming",
		"GetBody":    "cannot carry a body",
		"Redirect":   "not a 2xx status",
		"Loop":       "recursive",
		"NoLabel":    "label",
		"BadFormat":  "timestamp format",
		"Big":        "BigInteger",
		"PagingBad":  "paging input token Nope",
		"NotInModel": "not in the model",
	} {
		_, err := m.BindRest(op)
		if err == nil || !strings.Contains(err.Error(), op) || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
			t.Errorf("%s: %v, want an error naming the operation and %q", op, err, want)
		}
	}
	if _, err := stsModel(t).BindRest("GetCallerIdentity"); err == nil {
		t.Error("an awsQuery operation was bound as restJson1")
	}
}

// The model lowers a cap, never raises it.
func TestBindRestCapsOnlyTighten(t *testing.T) {
	if got := tighter(newObject("max", json.Number("5")), 100); got != 5 {
		t.Errorf("tighter = %d", got)
	}
	if got := tighter(newObject("max", json.Number("5000")), 100); got != 0 {
		t.Errorf("a larger model cap raised the default: %d", got)
	}
}

func encodeWith(t *testing.T, b *RestBinding, args map[string]any) httpbind.Request {
	t.Helper()
	req, err := b.Operation.Encode(restjson.Codec{}, args, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func bindReal(t *testing.T, table Table, op string) *RestBinding {
	t.Helper()
	b, err := loadRest(t, table).BindRest(op)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// SES v2: a label in the path, a query with a list and timestamps, a label with a body, and an empty answer.
func TestSESv2OperationsEncodeAndDecode(t *testing.T) {
	get := encodeWith(t, bindReal(t, sesv2Table, "GetEmailIdentity"), map[string]any{"EmailIdentity": "news.example.org"})
	if get.Method != "GET" || get.Path != "/v2/email/identities/news.example.org" || len(get.Body) != 0 || get.RawQuery != "" {
		t.Errorf("GetEmailIdentity = %+v", get)
	}
	// A label cannot add path segments: the slashes are escaped, so the value stays one segment.
	if r := encodeWith(t, bindReal(t, sesv2Table, "GetEmailIdentity"), map[string]any{"EmailIdentity": "a/../b"}); r.RawPath != "/v2/email/identities/a%2F..%2Fb" {
		t.Errorf("a label with slashes: %s", r.RawPath)
	}

	list := encodeWith(t, bindReal(t, sesv2Table, "ListSuppressedDestinations"), map[string]any{
		"Reasons": []any{"BOUNCE", "COMPLAINT"}, "StartDate": "2026-01-02T03:04:05Z", "TenantName": "t1"})
	if list.Path != "/v2/email/suppression/addresses" ||
		list.RawQuery != "Reason=BOUNCE&Reason=COMPLAINT&StartDate=2026-01-02T03%3A04%3A05Z&TenantName=t1" {
		t.Errorf("ListSuppressedDestinations = %+v", list)
	}

	contacts := bindReal(t, sesv2Table, "ListContacts")
	req := encodeWith(t, contacts, map[string]any{"ContactListName": "list-1"})
	if req.Method != "POST" || req.Path != "/v2/email/contact-lists/list-1/contacts/list" || req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("ListContacts = %+v", req)
	}
	// The page size is bound but offered nowhere: the model gives it no range maximum.
	if contacts.Pager == nil || contacts.Pager.SizeParam != "" || contacts.UnboundedPageSize != "PageSize" || contacts.Pager.TokenIn != "NextToken" {
		t.Errorf("ListContacts pager = %+v, unbounded %q", contacts.Pager, contacts.UnboundedPageSize)
	}

	del := bindReal(t, sesv2Table, "DeleteEmailIdentity")
	for _, status := range []int{200, 204} {
		out, _, err := del.Operation.Decode(restjson.Codec{}, status, http.Header{}, nil)
		if err != nil || len(out) != 0 {
			t.Errorf("empty answer %d = %v, %v", status, out, err)
		}
	}
	ident := bindReal(t, sesv2Table, "GetEmailIdentity")
	out, _, err := ident.Operation.Decode(restjson.Codec{}, 200, http.Header{}, []byte(`{"IdentityType":"DOMAIN","VerifiedForSendingStatus":true,"Unknown":1}`))
	if err != nil || out["IdentityType"] != "DOMAIN" || out["VerifiedForSendingStatus"] != true {
		t.Errorf("GetEmailIdentity answer = %v, %v", out, err)
	}
}

// Account: every call is a POST with a JSON body; a pager with the model's page size maximum; date-time
// from the target shape's timestampFormat.
func TestAccountOperationsEncodeAndDecode(t *testing.T) {
	req := encodeWith(t, bindReal(t, accountTable, "StartPrimaryEmailUpdate"), map[string]any{"AccountId": "123456789012", "PrimaryEmail": "owner@example.org"})
	if req.Method != "POST" || req.Path != "/startPrimaryEmailUpdate" || string(req.Body) != `{"AccountId":"123456789012","PrimaryEmail":"owner@example.org"}` {
		t.Errorf("StartPrimaryEmailUpdate = %+v, body %s", req, req.Body)
	}
	regions := bindReal(t, accountTable, "ListRegions")
	if regions.Pager == nil || *regions.Pager != (httpbind.Pager{TokenIn: "NextToken", TokenOut: "~NextToken", SizeParam: "MaxResults", MaxSize: 50}) {
		t.Errorf("ListRegions pager = %+v", regions.Pager)
	}
	info := bindReal(t, accountTable, "GetAccountInformation")
	out, _, err := info.Operation.Decode(restjson.Codec{}, 200, http.Header{}, []byte(`{"AccountId":"123456789012","AccountCreatedDate":"2026-01-02T03:04:05Z","AccountState":"ACTIVE"}`))
	if err != nil || out["AccountState"] != "ACTIVE" || out["AccountCreatedDate"] == nil {
		t.Errorf("GetAccountInformation answer = %v, %v", out, err)
	}
	empty := bindReal(t, accountTable, "PutAccountName")
	if empty.Operation.Output != nil {
		t.Errorf("a Unit output has a shape: %+v", empty.Operation.Output)
	}
	if out, _, err := empty.Operation.Decode(restjson.Codec{}, 204, http.Header{}, nil); err != nil || len(out) != 0 {
		t.Errorf("empty answer = %v, %v", out, err)
	}
}

// Payload, headers, prefix headers, the response code and the idempotency token have no operation in the two
// models, so the synthetic one carries them.
func TestSyntheticRestOperationRoundTrip(t *testing.T) {
	m := syntheticRest(t)
	put, err := m.BindRest("Put")
	if err != nil {
		t.Fatal(err)
	}
	req := encodeWith(t, put, map[string]any{
		"Id": "one", "Path": "a/b c", "Mode": "x", "Tags": []any{"p", "q"}, "Extra": map[string]any{"k": "v"},
		"Trace": "t-1", "Meta": map[string]any{"a": "1"}, "When": "2026-01-02T03:04:05Z",
		"Doc": map[string]any{"Value": "hi", "Choice": map[string]any{"A": "x"}}})
	if req.Path != "/things/one/a/b c" || !strings.HasPrefix(req.RawQuery, "fixed=1&") || req.Header.Get("X-Trace-Id") != "t-1" ||
		req.Header.Get("X-Meta-A") != "1" || req.Header.Get("X-When") == "" || string(req.Body) != `{"val":"hi","Choice":{"A":"x"}}` {
		t.Errorf("request = %+v, body %s", req, req.Body)
	}
	out, _, err := put.Operation.Decode(restjson.Codec{}, 201, http.Header{"Location": {"/things/one"}}, []byte(`{"val":"ok"}`))
	if err != nil || out["Location"] != "/things/one" || out["Status"] != int64(201) || out["Item"] == nil {
		t.Errorf("answer = %v, %v", out, err)
	}
	made, err := m.BindRest("Make")
	if err != nil {
		t.Fatal(err)
	}
	r := encodeWith(t, made, map[string]any{"Count": float64(1)})
	if !r.Generated || r.Token == "" || !strings.Contains(string(r.Body), `"clientToken":"`+r.Token+`"`) {
		t.Errorf("token request = %+v, body %s", r, r.Body)
	}
	via, err := m.BindRest("ViaResource")
	if err != nil || via.Operation.Input != nil || via.Operation.Output != nil {
		t.Errorf("resource operation = %+v, %v", via, err)
	}
}
