package infomaniakdav

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/emersion/go-vcard"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const baseContact = `"addressbook":"main","name":"Ada Example"`

func invokeContactWrite(handler capability.Handler, args string) (any, error) {
	return handler(context.Background(), resolved("addressbook/main"), resolver(nil), nil, json.RawMessage(args))
}

func decodeVCard(t *testing.T, data string) vcard.Card {
	t.Helper()
	card, err := vcard.NewDecoder(strings.NewReader(data)).Decode()
	if err != nil {
		t.Fatalf("vcard = %v\n%s", err, data)
	}
	return card
}

func TestContactsCreateSendsOneConditionalPut(t *testing.T) {
	calls := serve(t, writeFake(t, func(r *http.Request) (*http.Response, error) { return empty(201, "n1"), nil }))
	out, err := invokeContactWrite(invokeContactsCreate, `{`+baseContact+`,"structured_name":{"family":"Example","given":"Ada"},`+
		`"emails":[{"value":"ada@example.org","type":"work"},{"value":"a2@example.org"}],`+
		`"phones":[{"value":"100","type":"cell"},{"value":"+1 (2) 3.4-5/6"}],`+
		`"addresses":[{"type":"home","street":"Street 1","locality":"Town"}],"organization":"Org","title":"Dev",`+
		`"birthday":"1990-02-03","note":"a\nb, c","urls":["https://example.org/a"]}`)
	if err != nil {
		t.Fatalf("create = %v", err)
	}
	result := out.(*ContactWriteResult)
	if !result.Created || result.ETag != "n1" || result.ID != result.UID+".vcf" || !validCollectionID(result.ID) {
		t.Fatalf("result = %+v", result)
	}
	puts := writesOf(calls)
	if len(puts) != 1 || puts[0].method != "PUT" || puts[0].path != "sync.infomaniak.com"+mainPath+result.ID ||
		puts[0].header.Get("If-None-Match") != "*" || puts[0].header.Get("If-Match") != "" ||
		puts[0].header.Get("Content-Type") != "text/vcard; charset=utf-8" || puts[0].auth != basic() {
		t.Fatalf("puts = %+v", puts)
	}
	card := decodeVCard(t, puts[0].body)
	if card.Value(vcard.FieldVersion) != "3.0" || card.Value(vcard.FieldUID) != result.UID ||
		card.Value(vcard.FieldFormattedName) != "Ada Example" || card.Name().GivenName != "Ada" ||
		len(card[vcard.FieldEmail]) != 2 || !card[vcard.FieldEmail][0].Params.HasType("work") ||
		len(card[vcard.FieldTelephone]) != 2 || card.Value(vcard.FieldBirthday) != "1990-02-03" ||
		card.Value(vcard.FieldNote) != "a\nb, c" || card.Value(vcard.FieldURL) != "https://example.org/a" ||
		card.Addresses()[0].Locality != "Town" || card.Value(vcard.FieldOrganization) != "Org" {
		t.Errorf("card = %s", puts[0].body)
	}
}

func TestContactsCreateEscapesArguments(t *testing.T) {
	calls := serve(t, writeFake(t, func(r *http.Request) (*http.Response, error) { return empty(201, ""), nil }))
	_, err := invokeContactWrite(invokeContactsCreate, `{"addressbook":"main","name":"Ada","note":"x\r\nEMAIL:evil@example.org\nTEL:200",`+
		`"title":"t, u\\v"}`)
	if err != nil {
		t.Fatal(err)
	}
	body := writesOf(calls)[0].body
	card := decodeVCard(t, body)
	if len(card[vcard.FieldEmail]) != 0 || len(card[vcard.FieldTelephone]) != 0 ||
		card.Value(vcard.FieldNote) != "x\nEMAIL:evil@example.org\nTEL:200" || card.Value(vcard.FieldTitle) != `t, u\v` {
		t.Errorf("card = %s", body)
	}
	for _, line := range strings.Split(body, "\r\n") {
		if strings.HasPrefix(line, "EMAIL") || strings.HasPrefix(line, "TEL") {
			t.Errorf("injected line %q", line)
		}
	}
}

func TestContactsWritesRefuseBeforeAnyIO(t *testing.T) {
	calls := serve(t, writeFake(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected %s", r.Method)
		return empty(500, ""), nil
	}))
	many := strings.Repeat(`{"value":"a@example.org"},`, 20) + `{"value":"a@example.org"}`
	creates := map[string]string{
		"book not allowed":   `{"addressbook":"foreign","name":"n"}`,
		"book traversal":     `{"addressbook":"../main","name":"n"}`,
		"id argument":        `{"addressbook":"main","id":"x.vcf","name":"n"}`,
		"etag argument":      `{"addressbook":"main","etag":"e","name":"n"}`,
		"no name":            `{"addressbook":"main"}`,
		"name blank":         `{"addressbook":"main","name":"  "}`,
		"name newline":       `{"addressbook":"main","name":"a\nb"}`,
		"name control":       `{"addressbook":"main","name":"a\u0000b"}`,
		"name too long":      `{"addressbook":"main","name":"` + strings.Repeat("a", 257) + `"}`,
		"part semicolon":     `{` + baseContact + `,"structured_name":{"family":"a;b"}}`,
		"part control":       `{` + baseContact + `,"structured_name":{"given":"a\r\nb"}}`,
		"email invalid":      `{` + baseContact + `,"emails":[{"value":"nobody"}]}`,
		"email with name":    `{` + baseContact + `,"emails":[{"value":"A <a@example.org>"}]}`,
		"email injection":    `{` + baseContact + `,"emails":[{"value":"a@example.org\r\nX:y"}]}`,
		"email type":         `{` + baseContact + `,"emails":[{"value":"a@example.org","type":"work;X=1"}]}`,
		"email type unknown": `{` + baseContact + `,"emails":[{"value":"a@example.org","type":"pref"}]}`,
		"too many emails":    `{` + baseContact + `,"emails":[` + many + `]}`,
		"phone letters":      `{` + baseContact + `,"phones":[{"value":"call me"}]}`,
		"phone no digit":     `{` + baseContact + `,"phones":[{"value":"+-"}]}`,
		"phone newline":      `{` + baseContact + `,"phones":[{"value":"100\nTEL:200"}]}`,
		"phone too long":     `{` + baseContact + `,"phones":[{"value":"` + strings.Repeat("1", 33) + `"}]}`,
		"phone type":         `{` + baseContact + `,"phones":[{"value":"100","type":"x"}]}`,
		"address empty":      `{` + baseContact + `,"addresses":[{"type":"home"}]}`,
		"address semicolon":  `{` + baseContact + `,"addresses":[{"street":"a;b"}]}`,
		"address type":       `{` + baseContact + `,"addresses":[{"street":"a","type":"x"}]}`,
		"org semicolon":      `{` + baseContact + `,"organization":"a;b"}`,
		"title control":      `{` + baseContact + `,"title":"a\u0007"}`,
		"birthday bad":       `{` + baseContact + `,"birthday":"1990-02-30"}`,
		"birthday format":    `{` + baseContact + `,"birthday":"03.02.1990"}`,
		"note control":       `{` + baseContact + `,"note":"a\u0000"}`,
		"note too long":      `{` + baseContact + `,"note":"` + strings.Repeat("a", 4097) + `"}`,
		"url scheme":         `{` + baseContact + `,"urls":["javascript:alert(1)"]}`,
		"url file":           `{` + baseContact + `,"urls":["file:///etc/passwd"]}`,
		"url credentials":    `{` + baseContact + `,"urls":["https://u:p@example.org/"]}`,
		"url space":          `{` + baseContact + `,"urls":["https://example.org/a b"]}`,
		"url newline":        `{` + baseContact + `,"urls":["https://example.org/\nX:y"]}`,
		"url no host":        `{` + baseContact + `,"urls":["https:///a"]}`,
		"unknown argument":   `{` + baseContact + `,"vcard":"BEGIN:VCARD"}`,
		"photo":              `{` + baseContact + `,"photo":"x"}`,
	}
	for name, args := range creates {
		t.Run("create "+name, func(t *testing.T) {
			_, err := invokeContactsCreate(context.Background(), resolved("addressbook/main"), nil, nil, json.RawMessage(args))
			if err == nil || strings.Contains(err.Error(), "foreign") || strings.Contains(err.Error(), "evil") {
				t.Errorf("err = %v", err)
			}
		})
		if strings.Contains(args, `"addressbook":"foreign"`) || strings.Contains(args, `"id"`) ||
			strings.Contains(args, `"etag"`) || strings.Contains(args, "../main") {
			continue
		}
		t.Run("update "+name, func(t *testing.T) {
			args := strings.Replace(args, `"addressbook":"main"`, `"addressbook":"main","id":"x.vcf","etag":"e1"`, 1)
			if _, err := invokeContactsUpdate(context.Background(), resolved("addressbook/main"), nil, nil, json.RawMessage(args)); err == nil {
				t.Error("accepted")
			}
		})
	}
	targets := map[string]string{
		"book not allowed": `{"addressbook":"foreign","id":"x.vcf","etag":"e"}`,
		"id slash":         `{"addressbook":"main","id":"a/b.vcf","etag":"e"}`,
		"id dot dot":       `{"addressbook":"main","id":"..","etag":"e"}`,
		"id percent":       `{"addressbook":"main","id":"a%2Fb","etag":"e"}`,
		"etag missing":     `{"addressbook":"main","id":"x.vcf"}`,
		"etag star":        `{"addressbook":"main","id":"x.vcf","etag":"*"}`,
		"etag weak":        `{"addressbook":"main","id":"x.vcf","etag":"W/\"a\""}`,
		"etag control":     `{"addressbook":"main","id":"x.vcf","etag":"a\r\nb"}`,
		"free header":      `{"addressbook":"main","id":"x.vcf","etag":"e","method":"POST"}`,
	}
	for name, args := range targets {
		t.Run("delete "+name, func(t *testing.T) {
			if _, err := invokeContactsDelete(context.Background(), resolved("addressbook/main"), nil, nil, json.RawMessage(args)); err == nil {
				t.Error("accepted")
			}
		})
		if name == "etag missing" || name == "free header" || name == "book not allowed" {
			continue
		}
		t.Run("update target "+name, func(t *testing.T) {
			args := strings.TrimSuffix(args, "}") + `,"name":"n"}`
			if _, err := invokeContactsUpdate(context.Background(), resolved("addressbook/main"), nil, nil, json.RawMessage(args)); err == nil {
				t.Error("accepted")
			}
		})
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %v, want none", *calls)
	}
	if _, err := invokeContactsDelete(context.Background(), resolved("addressbook/main"), nil, nil,
		json.RawMessage(targets["book not allowed"])); classOf(err) != provider.ClassPermission {
		t.Errorf("allow-list err = %v", err)
	}
}

const storedCard = "UID:stored-1\r\nFN:Old\r\nN:Example;Old;;;\r\nEMAIL:old@example.org\r\nREV:20260101T000000Z"

func contactUpdateFake(t *testing.T, stored, etag string, put func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	return updateFake(t, stored, etag, put)
}

func TestContactsUpdateKeepsUIDAndSendsOneIfMatchPut(t *testing.T) {
	calls := serve(t, contactUpdateFake(t, vcardOf(storedCard), "e1", func(r *http.Request) (*http.Response, error) {
		return empty(204, "e2"), nil
	}))
	out, err := invokeContactWrite(invokeContactsUpdate, `{`+baseContact+`,"id":"x.vcf","etag":"\"e1\"","phones":[{"value":"200"}]}`)
	if err != nil {
		t.Fatalf("update = %v", err)
	}
	if result := out.(*ContactWriteResult); !result.Updated || result.UID != "stored-1" || result.ETag != "e2" || result.ID != "x.vcf" {
		t.Errorf("result = %+v", result)
	}
	puts := writesOf(calls)
	if len(puts) != 1 || puts[0].header.Get("If-Match") != `"e1"` || puts[0].header.Get("If-None-Match") != "" ||
		puts[0].path != "sync.infomaniak.com"+mainPath+"x.vcf" {
		t.Fatalf("puts = %+v", puts)
	}
	card := decodeVCard(t, puts[0].body)
	if card.Value(vcard.FieldUID) != "stored-1" || strings.Contains(puts[0].body, "old@example.org") ||
		card.Value(vcard.FieldTelephone) != "200" {
		t.Errorf("card = %s", puts[0].body)
	}
}

func TestContactsUpdateRefusesWithoutPut(t *testing.T) {
	cases := map[string]struct{ stored, etag string }{
		"photo":        {vcardOf(storedCard, "PHOTO;ENCODING=b;TYPE=JPEG:AAAA"), "e1"},
		"group":        {vcardOf(storedCard, "KIND:group"), "e1"},
		"extension":    {vcardOf(storedCard, "X-CUSTOM:1"), "e1"},
		"no uid":       {vcardOf("FN:Old"), "e1"},
		"not a card":   {"garbage", "e1"},
		"etag differs": {vcardOf(storedCard), "e9"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			calls := serve(t, contactUpdateFake(t, c.stored, c.etag, func(*http.Request) (*http.Response, error) {
				t.Error("PUT sent")
				return empty(204, ""), nil
			}))
			_, err := invokeContactWrite(invokeContactsUpdate, `{`+baseContact+`,"id":"x.vcf","etag":"e1"}`)
			if err == nil || strings.Contains(err.Error(), "AAAA") || len(writesOf(calls)) != 0 {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestContactsPreconditionFailed(t *testing.T) {
	calls := serve(t, contactUpdateFake(t, vcardOf(storedCard), "e1", func(*http.Request) (*http.Response, error) {
		return xmlResponse(412, bodyCanary), nil
	}))
	_, err := invokeContactWrite(invokeContactsUpdate, `{`+baseContact+`,"id":"x.vcf","etag":"e1"}`)
	if err == nil || !strings.Contains(err.Error(), "precondition failed") || strings.Contains(err.Error(), bodyCanary) ||
		strings.Contains(err.Error(), "may have taken effect") || len(writesOf(calls)) != 1 {
		t.Errorf("update err = %v", err)
	}
	calls = serve(t, writeFake(t, func(*http.Request) (*http.Response, error) { return xmlResponse(412, bodyCanary), nil }))
	_, err = invokeContactWrite(invokeContactsDelete, `{"addressbook":"main","id":"x.vcf","etag":"e1"}`)
	if err == nil || !strings.Contains(err.Error(), "precondition failed") || len(writesOf(calls)) != 1 {
		t.Errorf("delete err = %v", err)
	}
	if _, err = invokeContactWrite(invokeContactsCreate, `{`+baseContact+`}`); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("create err = %v", err)
	}
}

func TestContactsDeleteSendsOneIfMatchDelete(t *testing.T) {
	calls := serve(t, writeFake(t, func(*http.Request) (*http.Response, error) { return empty(204, ""), nil }))
	out, err := invokeContactWrite(invokeContactsDelete, `{"addressbook":"main","id":"x.vcf","etag":"e1"}`)
	if result, _ := out.(*ContactWriteResult); err != nil || result == nil || !result.Deleted {
		t.Fatalf("delete = %+v, %v", out, err)
	}
	dels := writesOf(calls)
	if len(dels) != 1 || dels[0].method != "DELETE" || dels[0].header.Get("If-Match") != `"e1"` ||
		dels[0].path != "sync.infomaniak.com"+mainPath+"x.vcf" || dels[0].body != "" {
		t.Errorf("deletes = %+v", dels)
	}
}

func TestContactsWritesAreNeverRepeatedAfterUnclearResult(t *testing.T) {
	failures := map[string]func(*http.Request) (*http.Response, error){
		"500":     func(*http.Request) (*http.Response, error) { return xmlResponse(500, bodyCanary), nil },
		"503":     func(*http.Request) (*http.Response, error) { return xmlResponse(503, bodyCanary), nil },
		"timeout": func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"reset":   func(*http.Request) (*http.Response, error) { return nil, syscall.ECONNRESET },
		"unknown": func(*http.Request) (*http.Response, error) { return nil, errors.New(bodyCanary) },
		"odd 2xx": func(*http.Request) (*http.Response, error) { return xmlResponse(202, bodyCanary), nil },
	}
	run := map[string]string{
		"create": `create`, "update": `update`, "delete": `delete`,
	}
	for fname, failure := range failures {
		for rname := range run {
			t.Run(rname+" "+fname, func(t *testing.T) {
				calls := serve(t, contactUpdateFake(t, vcardOf(storedCard), "e1", failure))
				var err error
				switch rname {
				case "create":
					_, err = invokeContactWrite(invokeContactsCreate, `{`+baseContact+`}`)
				case "update":
					_, err = invokeContactWrite(invokeContactsUpdate, `{`+baseContact+`,"id":"x.vcf","etag":"e1"}`)
				default:
					_, err = invokeContactWrite(invokeContactsDelete, `{"addressbook":"main","id":"x.vcf","etag":"e1"}`)
				}
				if err == nil || !strings.Contains(err.Error(), "may have taken effect") ||
					strings.Contains(err.Error(), bodyCanary) || len(writesOf(calls)) != 1 {
					t.Errorf("err = %v, writes = %d", err, len(writesOf(calls)))
				}
			})
		}
	}
}

func TestContactsWritesThroughCoreNeedConfirmationAndToolList(t *testing.T) {
	calls := serve(t, writeFake(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "DELETE" {
			return empty(204, ""), nil
		}
		return empty(201, "n"), nil
	}))
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	connection := func(tools []string) *application.Core {
		cfg := &config.Config{
			Version:     1,
			Services:    map[string]config.Service{"dav": {Provider: Provider}},
			Credentials: map[string]config.Credential{"dav": resolved().Secrets},
			Connections: map[string]config.Connection{"main": {Service: "dav", Credential: "dav",
				Permissions: config.Permissions(), Tools: tools, Targets: []string{"addressbook/main"}}},
		}
		red := &redact.Redactor{}
		return application.New(reg, cfg, resolver(red), red)
	}
	core := connection(nil)
	request := application.InvokeRequest{Operation: contactsCreate.ID, Connection: "main",
		Arguments: json.RawMessage(`{` + baseContact + `}`)}
	if _, err := core.Invoke(context.Background(), request); err == nil || len(*calls) != 0 {
		t.Fatalf("unconfirmed create: err = %v, requests = %d", err, len(*calls))
	}
	request.Confirmed = true
	if _, err := core.Invoke(context.Background(), request); err != nil || len(writesOf(calls)) != 1 {
		t.Fatalf("confirmed create: err = %v", err)
	}
	before := len(*calls)
	del := application.InvokeRequest{Operation: contactsDelete.ID, Connection: "main", Confirmed: true,
		Arguments: json.RawMessage(`{"addressbook":"main","id":"x.vcf","etag":"e1"}`)}
	if _, err := core.Invoke(context.Background(), del); err == nil || len(*calls) != before {
		t.Fatalf("delete without a tools list: err = %v", err)
	}
	listed := connection([]string{contactsDelete.ID})
	if _, err := listed.Invoke(context.Background(), del); err != nil || len(writesOf(calls)) != 2 {
		t.Fatalf("delete with a tools list: err = %v", err)
	}
}
