package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func tagAdmin(t *testing.T, invoke capability.Handler, args string) (any, error) {
	t.Helper()
	red := &redact.Redactor{}
	return accountBound(invoke)(capability.WithConfirmed(context.Background()), accountConnection(), resolver(red), red, json.RawMessage(args))
}

func located(code int, location string) *http.Response {
	response := status(code)
	response.Header = http.Header{}
	if location != "" {
		response.Header.Set("Content-Location", location)
	}
	return response
}

func proppatchResult(inner string) *http.Response {
	return xmlResponse(http.StatusMultiStatus, multistatus(`<d:response><d:href>`+tagCatalog+`/7</d:href><d:propstat><d:prop>`+inner+
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`))
}

// tagAdminServer answers the pre-check read of tag 7 with tag and every mutation with change.
func tagAdminServer(t *testing.T, tag string, change func(*http.Request) (*http.Response, error)) (*[]call, *[]string) {
	t.Helper()
	var changes []string
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == methodPropfind && r.URL.Path == tagCatalog+"/7" {
			return xmlResponse(http.StatusMultiStatus, multistatus(tag)), nil
		}
		changes = append(changes, r.Method+" "+r.URL.Path)
		return change(r)
	})
	return calls, &changes
}

func TestSystemTagAdminToolsRefuseWithoutAccountBeforeSecretsAndIO(t *testing.T) {
	refuse(t)
	reads := 0
	red := &redact.Redactor{}
	secrets := secret.NewWith(func(string) string { reads++; return "x" }, nil, nil, red)
	for name, args := range map[string]string{
		"create": `{"name":"Invoice"}`, "update": `{"tag_id":"7","name":"x"}`, "delete": `{"tag_id":"7"}`,
	} {
		fn := map[string]capability.Handler{"create": invokeSystemTagsCreate, "update": invokeSystemTagsUpdate, "delete": invokeSystemTagsDelete}[name]
		_, err := accountBound(fn)(capability.WithConfirmed(context.Background()), localConnection("", ""), secrets, red, json.RawMessage(args))
		if err == nil || reads != 0 {
			t.Errorf("%s: err = %v, secret reads = %d", name, err, reads)
		}
	}
}

func TestSystemTagAdminRefusesInvalidInputBeforeIO(t *testing.T) {
	refuse(t)
	long := strings.Repeat("a", 65)
	for name, c := range map[string]struct {
		fn   capability.Handler
		args string
	}{
		"create without name":  {invokeSystemTagsCreate, `{}`},
		"create blank name":    {invokeSystemTagsCreate, `{"name":"  "}`},
		"create long name":     {invokeSystemTagsCreate, `{"name":"` + long + `"}`},
		"create control":       {invokeSystemTagsCreate, `{"name":"a\nb"}`},
		"create with color":    {invokeSystemTagsCreate, `{"name":"a","color":"ff0000"}`},
		"create with tag_id":   {invokeSystemTagsCreate, `{"name":"a","tag_id":"7"}`},
		"create unknown":       {invokeSystemTagsCreate, `{"name":"a","groups":"x"}`},
		"create string bool":   {invokeSystemTagsCreate, `{"name":"a","visible":"false"}`},
		"update without field": {invokeSystemTagsUpdate, `{"tag_id":"7"}`},
		"update bad id":        {invokeSystemTagsUpdate, `{"tag_id":"7/../8","name":"a"}`},
		"update missing id":    {invokeSystemTagsUpdate, `{"name":"a"}`},
		"update bad color":     {invokeSystemTagsUpdate, `{"tag_id":"7","color":"#ff0000"}`},
		"update short color":   {invokeSystemTagsUpdate, `{"tag_id":"7","color":"fff"}`},
		"update blank name":    {invokeSystemTagsUpdate, `{"tag_id":"7","name":""}`},
		"delete bad id":        {invokeSystemTagsDelete, `{"tag_id":"x"}`},
		"delete extra field":   {invokeSystemTagsDelete, `{"tag_id":"7","name":"a"}`},
		"delete missing id":    {invokeSystemTagsDelete, `{}`},
	} {
		if _, err := tagAdmin(t, c.fn, c.args); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestSystemTagsCreateSendsOneJSONPostAndReadsContentLocation(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return located(201, "/remote.php/dav/systemtags/42"), nil
	})
	got, err := tagAdmin(t, invokeSystemTagsCreate, `{"name":" Rechnung \"A\" <b> "}`)
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["created"] != true || m["tag_id"] != "42" || m["name"] != `Rechnung "A" <b>` {
		t.Errorf("result = %v", m)
	}
	if len(*calls) != 1 || (*calls)[0].method != http.MethodPost || (*calls)[0].url.Path != tagCatalog+"/" {
		t.Fatalf("calls = %+v", *calls)
	}
	want := `{"name":"Rechnung \"A\" \u003cb\u003e","userVisible":true,"userAssignable":true}`
	if (*calls)[0].body != want {
		t.Errorf("body = %s, want %s", (*calls)[0].body, want)
	}
	_, err = tagAdmin(t, invokeSystemTagsCreate, `{"name":"x","visible":false,"assignable":false}`)
	if err != nil || (*calls)[1].body != `{"name":"x","userVisible":false,"userAssignable":false}` {
		t.Errorf("err = %v, body = %s", err, (*calls)[1].body)
	}
}

func TestSystemTagsCreateWithoutUsableContentLocationReportsNoID(t *testing.T) {
	for name, location := range map[string]string{
		"absent": "", "foreign area": "/remote.php/dav/files/alice/42", "nested": "/remote.php/dav/systemtags/4/2",
		"not digits": "/remote.php/dav/systemtags/abc", "foreign host": "https://evil.example.invalid/remote.php/dav/systemtags/42",
	} {
		serve(t, func(*http.Request) (*http.Response, error) { return located(201, location), nil })
		got, err := tagAdmin(t, invokeSystemTagsCreate, `{"name":"x"}`)
		m, _ := got.(map[string]any)
		if err != nil || m["created"] != true || m["tag_id"] != nil || !strings.Contains(m["note"].(string), "systemtags.list") {
			t.Errorf("%s: got %v, %v", name, got, err)
		}
	}
}

func TestSystemTagsCreateRefusals(t *testing.T) {
	for code, want := range map[int]string{403: messageTagAdminDenied, 409: messageTagExists, 400: messageTagVisibility} {
		calls := serve(t, func(*http.Request) (*http.Response, error) { return status(code), nil })
		_, err := tagAdmin(t, invokeSystemTagsCreate, `{"name":"x"}`)
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "check systemtags.list") || len(*calls) != 1 {
			t.Errorf("%d: err = %v, calls = %d", code, err, len(*calls))
		}
	}
}

func TestSystemTagAdminUnclearOutcomesAreNeverRepeated(t *testing.T) {
	for name, fn := range map[string]func(*http.Request) (*http.Response, error){
		"5xx":     func(*http.Request) (*http.Response, error) { return status(500), nil },
		"timeout": func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
	} {
		for tool, c := range map[string]struct {
			fn   capability.Handler
			args string
		}{
			"create": {invokeSystemTagsCreate, `{"name":"x"}`}, "update": {invokeSystemTagsUpdate, `{"tag_id":"7","name":"x"}`},
			"delete": {invokeSystemTagsDelete, `{"tag_id":"7"}`},
		} {
			calls, changes := tagAdminServer(t, okTag, fn)
			_, err := tagAdmin(t, c.fn, c.args)
			if err == nil || !strings.Contains(err.Error(), "check systemtags.list before repeating") || len(*changes) != 1 {
				t.Errorf("%s %s: err = %v, changes = %v, calls = %d", tool, name, err, *changes, len(*calls))
			}
		}
	}
}

func TestSystemTagAdminRefusesARedirectClearly(t *testing.T) {
	for tool, c := range map[string]struct {
		fn   capability.Handler
		args string
	}{
		"create": {invokeSystemTagsCreate, `{"name":"x"}`}, "update": {invokeSystemTagsUpdate, `{"tag_id":"7","name":"x"}`},
		"delete": {invokeSystemTagsDelete, `{"tag_id":"7"}`},
	} {
		tagAdminServer(t, okTag, func(*http.Request) (*http.Response, error) {
			response := status(307)
			response.Header = http.Header{"Location": {"https://evil.example.invalid/x"}}
			return response, nil
		})
		_, err := tagAdmin(t, c.fn, c.args)
		if err == nil || !strings.Contains(err.Error(), messageRedirect) || strings.Contains(err.Error(), "evil") ||
			strings.Contains(err.Error(), "before repeating") {
			t.Errorf("%s: %v", tool, err)
		}
	}
}

func TestSystemTagsUpdateReadsThenSendsOneEscapedProppatch(t *testing.T) {
	var proppatch string
	calls, changes := tagAdminServer(t, okTag, func(r *http.Request) (*http.Response, error) {
		return proppatchResult(`<oc:display-name/>`), nil
	})
	got, err := tagAdmin(t, invokeSystemTagsUpdate,
		`{"tag_id":"7","name":"A & <B>","visible":true,"assignable":false,"color":"FF00aa"}`)
	if err != nil || got.(map[string]any)["updated"] != true || got.(map[string]any)["tag_id"] != "7" {
		t.Fatalf("got %v, %v", got, err)
	}
	proppatch = (*calls)[1].body
	if len(*calls) != 2 || (*calls)[0].method != methodPropfind || (*calls)[0].depth != depthSelf ||
		(*calls)[1].method != methodProppatch || len(*changes) != 1 || (*changes)[0] != "PROPPATCH "+tagCatalog+"/7" {
		t.Fatalf("calls = %+v", *calls)
	}
	want := xmlHeader + `<d:propertyupdate xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns" xmlns:nc="http://nextcloud.org/ns">` +
		`<d:set><d:prop><oc:display-name>A &amp; &lt;B&gt;</oc:display-name><oc:user-visible>true</oc:user-visible>` +
		`<oc:user-assignable>false</oc:user-assignable><nc:color>FF00aa</nc:color></d:prop></d:set></d:propertyupdate>`
	if proppatch != want {
		t.Errorf("body = %s", proppatch)
	}
}

func TestSystemTagsUpdateOnlyNamedFieldsAndRefusals(t *testing.T) {
	var proppatch string
	calls, _ := tagAdminServer(t, okTag, func(r *http.Request) (*http.Response, error) {
		return proppatchResult(`<oc:user-visible/>`), nil
	})
	_, err := tagAdmin(t, invokeSystemTagsUpdate, `{"tag_id":"7","visible":false}`)
	proppatch = (*calls)[len(*calls)-1].body
	if err != nil ||
		!strings.Contains(proppatch, `<d:prop><oc:user-visible>false</oc:user-visible></d:prop>`) || strings.Contains(proppatch, "display-name") {
		t.Errorf("body = %s, err = %v", proppatch, err)
	}
	for code, want := range map[int]string{403: messageTagAdminDenied, 409: messageTagExists} {
		tagAdminServer(t, okTag, func(*http.Request) (*http.Response, error) { return status(code), nil })
		if _, err := tagAdmin(t, invokeSystemTagsUpdate, `{"tag_id":"7","name":"x"}`); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%d: %v", code, err)
		}
	}
	tagAdminServer(t, okTag, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, multistatus(`<d:response><d:href>`+tagCatalog+`/7</d:href><d:propstat><d:prop><oc:display-name/></d:prop>`+
			`<d:status>HTTP/1.1 409 Conflict</d:status></d:propstat></d:response>`)), nil
	})
	if _, err := tagAdmin(t, invokeSystemTagsUpdate, `{"tag_id":"7","name":"x"}`); err == nil || !strings.Contains(err.Error(), messageTagRefused) {
		t.Errorf("refused property: %v", err)
	}
	tagAdminServer(t, okTag, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, multistatus(`<d:response><d:href>`+tagCatalog+`/8</d:href><d:propstat><d:prop><oc:display-name/></d:prop>`+
			`<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)), nil
	})
	if _, err := tagAdmin(t, invokeSystemTagsUpdate, `{"tag_id":"7","name":"x"}`); err == nil {
		t.Error("an answer for another tag was accepted")
	}
}

func TestSystemTagsDeleteReadsThenSendsOneDelete(t *testing.T) {
	calls, changes := tagAdminServer(t, okTag, func(*http.Request) (*http.Response, error) { return status(204), nil })
	got, err := tagAdmin(t, invokeSystemTagsDelete, `{"tag_id":"7"}`)
	if err != nil || got.(map[string]any)["deleted"] != true || len(*calls) != 2 || len(*changes) != 1 || (*changes)[0] != "DELETE "+tagCatalog+"/7" {
		t.Fatalf("got %v, %v, calls = %+v", got, err, *calls)
	}
}

func TestSystemTagsDeleteAndUpdateTreatInvisibleOrMissingTagsAsMissing(t *testing.T) {
	invisible := tagXML(tagCatalog+"/7", "7", "x", "false", "true", "true")
	for name, tag := range map[string]string{"invisible": invisible, "id mismatch": tagXML(tagCatalog+"/7", "8", "x", "true", "true", "true")} {
		for tool, c := range map[string]struct {
			fn   capability.Handler
			args string
		}{"update": {invokeSystemTagsUpdate, `{"tag_id":"7","name":"x"}`}, "delete": {invokeSystemTagsDelete, `{"tag_id":"7"}`}} {
			_, changes := tagAdminServer(t, tag, func(*http.Request) (*http.Response, error) { return status(204), nil })
			if _, err := tagAdmin(t, c.fn, c.args); err == nil || len(*changes) != 0 {
				t.Errorf("%s %s: err = %v, changes = %v", name, tool, err, *changes)
			}
		}
	}
	serve(t, func(*http.Request) (*http.Response, error) { return status(404), nil })
	if _, err := tagAdmin(t, invokeSystemTagsDelete, `{"tag_id":"7"}`); err == nil || !strings.Contains(err.Error(), messageTagMissing) {
		t.Errorf("missing tag: %v", err)
	}
}

func TestSystemTagsUnassignableTagCanStillBeAdministered(t *testing.T) {
	_, changes := tagAdminServer(t, tagXML(tagCatalog+"/7", "7", "x", "true", "false", "false"),
		func(*http.Request) (*http.Response, error) { return status(204), nil })
	if _, err := tagAdmin(t, invokeSystemTagsDelete, `{"tag_id":"7"}`); err != nil || len(*changes) != 1 {
		t.Errorf("err = %v, changes = %v", err, *changes)
	}
}

func TestSystemTagAdminRisks(t *testing.T) {
	for _, c := range []struct {
		d      capability.Descriptor
		effect capability.Effect
		allow  bool
	}{{systemtagsCreate, capability.EffectCreate, false}, {systemtagsUpdate, capability.EffectUpdate, false}, {systemtagsDelete, capability.EffectDelete, true}} {
		r := c.d.Risk
		if r.Effect != c.effect || r.Idempotency == "" || r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld ||
			r.DataSensitivity != dataSensitivity || c.d.RequiresToolAllowList != c.allow || strings.Count(c.d.ID, ".") != 2 {
			t.Errorf("%s = %+v", c.d.ID, c.d)
		}
	}
	if !strings.Contains(systemtagsDelete.Description, "every assignment") {
		t.Errorf("description = %q", systemtagsDelete.Description)
	}
	reg := registry(t)
	for _, d := range reg.Provider(Provider) {
		if strings.HasPrefix(d.ID, "nextcloud.systemtags.") && d.Group != "files" {
			t.Errorf("%s group = %q", d.ID, d.Group)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if id == systemtagsCreate.ID || id == systemtagsUpdate.ID || id == systemtagsDelete.ID {
				t.Errorf("profile %s holds %s", p.ID, id)
			}
		}
	}
}
