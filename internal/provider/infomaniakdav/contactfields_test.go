package infomaniakdav

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func richContactCard() string {
	return vcardOf("UID:u-r", "FN:Ada Lovelace", "N:Lovelace;Ada;Augusta;Countess;", "ORG:Engine;Analytical;Dept",
		"TITLE:Programmer", "BDAY:1815-12-10", "NOTE:note", "URL:https://example.org/ada",
		"NICKNAME:Ada,Countess", "MAILER:Mailer", "TZ:+01:00", "GEO:47.37;8.54", "ROLE:Pioneer",
		"CLASS:PUBLIC", "SORT-STRING:Lovelace", "REV:20260101T000000Z", "PRODID:-//Example//EN", "NAME:Card",
		"PROFILE:VCARD", "SOURCE:https://example.org/ada.vcf", "LABEL;TYPE=home:1 Main St\\nZurich",
		"CATEGORIES:friends,math", "PHOTO;VALUE=uri:https://example.org/p.png", "LOGO;VALUE=uri:https://example.org/l.png",
		"SOUND;VALUE=uri:https://example.org/s.ogg", "AGENT;VALUE=uri:https://example.org/a.vcf",
		"KEY;VALUE=uri:https://example.org/k.asc", "KEY;ENCODING=b:S0VZLUNBTkFSWQ==", "PHOTO;ENCODING=b:UEhPVE8tQ0FOQVJZ",
		"EMAIL;PREF=1;TYPE=home:ada@example.org", "TEL;PREF=2:+100",
		"ADR;PREF=3;TYPE=home:;;1 Main St;Zurich;ZH;8000;CH")
}

func coreFor(t *testing.T) *application.Core {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"dav": {Provider: Provider}},
		Credentials: map[string]config.Credential{"dav": resolved().Secrets},
		Connections: map[string]config.Connection{"main": {Service: "dav", Credential: "dav",
			Effects: config.Effects(), Targets: []string{"addressbook/main"}}},
	}
	red := &redact.Redactor{}
	return application.New(reg, cfg, resolver(red), red)
}

func TestContactsGetReportsEveryMemberInSchemaAndFields(t *testing.T) {
	calls := serve(t, contactsFake(t, "", func(*http.Request) (*http.Response, error) {
		resp := xmlResponse(200, richContactCard())
		resp.Header.Set("ETag", `"v1"`)
		return resp, nil
	}))
	got, err := coreFor(t).Invoke(context.Background(), application.InvokeRequest{
		Operation: contactsGet.ID, Connection: "main", Arguments: json.RawMessage(`{"addressbook":"main","id":"a.vcf"}`),
	})
	if err != nil {
		t.Fatalf("get = %v", err)
	}
	if strings.Contains(string(got.Result), "CANARY") || strings.Contains(string(got.Result), "UEhPVE8") ||
		strings.Contains(string(got.Result), "S0VZ") {
		t.Errorf("binary data was reported: %s", got.Result)
	}
	var out struct {
		Contact map[string]any `json:"contact"`
	}
	if err := json.Unmarshal(got.Result, &out); err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Contact struct {
				Properties map[string]any `json:"properties"`
			} `json:"contact"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(contactsGet.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	fields := map[string]bool{}
	for _, f := range contactsGet.Fields {
		fields[f.Name] = true
	}
	var missing []string
	for key := range out.Contact {
		if _, ok := schema.Properties.Contact.Properties[key]; !ok {
			missing = append(missing, key+" (schema)")
		}
		if !fields[key] {
			missing = append(missing, key+" (fields)")
		}
	}
	for key := range schema.Properties.Contact.Properties {
		if !fields[key] {
			missing = append(missing, key+" (schema without field)")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 || len(out.Contact) < 33 {
		t.Errorf("missing %v; %d members: %v", missing, len(out.Contact), out.Contact)
	}
	if out.Contact["key_uri"] != "https://example.org/k.asc" || out.Contact["organization"] != "Engine, Analytical, Dept" ||
		len(out.Contact["organization_units"].([]any)) != 2 {
		t.Errorf("contact = %v", out.Contact)
	}
	if len(*calls) == 0 {
		t.Error("no request")
	}
}
