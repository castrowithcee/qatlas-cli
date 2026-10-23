package nextcloud

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func richContactCard() string {
	return vcardOf("u-r", "Ada Lovelace", "ORG:Engine;Analytical;Dept\r\nTITLE:Programmer\r\nBDAY:1815-12-10\r\nNOTE:note\r\n"+
		"URL:https://example.org/ada\r\nNICKNAME:Ada,Countess\r\nMAILER:Mailer\r\nTZ:+01:00\r\nGEO:47.37;8.54\r\nROLE:Pioneer\r\n"+
		"CLASS:PUBLIC\r\nSORT-STRING:Lovelace\r\nREV:20260101T000000Z\r\nPRODID:-//Example//EN\r\nNAME:Card\r\n"+
		"PROFILE:VCARD\r\nSOURCE:https://example.org/ada.vcf\r\nLABEL;TYPE=home:1 Main St\\nZurich\r\n"+
		"CATEGORIES:friends,math\r\nPHOTO;VALUE=uri:https://example.org/p.png\r\nLOGO;VALUE=uri:https://example.org/l.png\r\n"+
		"SOUND;VALUE=uri:https://example.org/s.ogg\r\nAGENT;VALUE=uri:https://example.org/a.vcf\r\n"+
		"KEY;VALUE=uri:https://example.org/k.asc\r\nKEY;ENCODING=b:S0VZLUNBTkFSWQ==\r\n"+
		"EMAIL;PREF=1;TYPE=home:ada@example.org\r\nTEL;PREF=2:+100\r\nADR;PREF=3;TYPE=home:;;1 Main St;Zurich;ZH;8000;CH\r\n"+
		"EMAIL;TYPE=work:ada"+"@"+"work.example\r\nTEL;TYPE=cell:+200\r\nADR;TYPE=work:;;2 Side St;Bern;BE;3000;CH\r\n"+
		"URL:https://example.org/second\r\n")
}

func TestContactsGetReportsEveryMemberInSchemaAndFields(t *testing.T) {
	card := richContactCard()
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookMultistatus(
			cardNode(contactHome+"contacts/ada.vcf", "e1", card))), nil
	})
	result, err := bookInvoke(t, []string{"addressbook/contacts"}, "nextcloud.contacts.contacts.get",
		`{"addressbook":"contacts","id":"ada.vcf"}`)
	if err != nil {
		t.Fatalf("get = %v", err)
	}
	if strings.Contains(string(result), "S0VZ") {
		t.Errorf("an embedded key was reported: %s", result)
	}
	for _, name := range []string{"PHOTO", "LOGO", "SOUND", "KEY", "AGENT"} {
		if strings.Contains((*calls)[0].body, `name="`+name+`"`) {
			t.Errorf("the request asks for %s", name)
		}
	}
	var out struct {
		Contact map[string]any `json:"contact"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
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
	for _, name := range []string{"photo_uri", "logo_uri", "sound_uri", "agent_uri", "key_uri"} {
		if _, ok := out.Contact[name]; ok {
			t.Errorf("%s was reported", name)
		}
		if _, ok := schema.Properties.Contact.Properties[name]; ok || fields[name] {
			t.Errorf("%s is in schema or fields", name)
		}
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
	if len(missing) > 0 || out.Contact["organization"] != "Engine, Analytical, Dept" ||
		len(out.Contact["organization_units"].([]any)) != 2 || len(out.Contact) < 23 {
		t.Errorf("missing %v; %d members: %v", missing, len(out.Contact), out.Contact)
	}
}

func TestContactsListReportsTheMembersOfGet(t *testing.T) {
	card := richContactCard()
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookMultistatus(
			cardNode(contactHome+"contacts/ada.vcf", "e1", card))), nil
	})
	listed, err := listContacts(t, `{"addressbook":"contacts"}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := bookInvoke(t, []string{"addressbook/contacts"}, "nextcloud.contacts.contacts.get",
		`{"addressbook":"contacts","id":"ada.vcf"}`)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Contacts []map[string]any `json:"contacts"`
	}
	var get struct {
		Contact map[string]any `json:"contact"`
	}
	if err := json.Unmarshal(listed, &list); err != nil || json.Unmarshal(got, &get) != nil || len(list.Contacts) != 1 {
		t.Fatalf("list = %s, get = %s, %v", listed, got, err)
	}
	entry := list.Contacts[0]
	if entry["email"] != "ada@example.org" {
		t.Errorf("email = %v", entry["email"])
	}
	checkOutputDeclared(t, contactsList, "contacts", listed)
	delete(entry, "email")
	if !reflect.DeepEqual(entry, get.Contact) {
		t.Errorf("list entry = %v, get = %v", entry, get.Contact)
	}
	for _, key := range []string{"structured_name", "emails", "phones", "addresses", "title", "birthday", "note", "urls"} {
		if _, ok := get.Contact[key]; !ok {
			t.Errorf("example carries no %s", key)
		}
	}
	for _, key := range []string{"photo_uri", "logo_uri", "sound_uri", "agent_uri", "key_uri"} {
		if _, ok := entry[key]; ok {
			t.Errorf("%s was reported", key)
		}
	}
}

func TestContactsListAsksForTheGetProperties(t *testing.T) {
	for name, args := range map[string]string{
		"plain":    `{"addressbook":"contacts"}`,
		"query":    `{"addressbook":"contacts","query":"ada"}`,
		"filtered": `{"addressbook":"contacts","property_filters":[{"property":"FN","text":"a"}]}`,
	} {
		calls := emptyBook(t)
		if _, err := listContacts(t, args); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body := (*calls)[0].body
		if !strings.Contains(body, `<card:address-data>`+contactGetProps+`</card:address-data>`) {
			t.Errorf("%s: body lacks the get properties: %s", name, body)
		}
		for _, binary := range []string{"PHOTO", "LOGO", "SOUND", "KEY", "AGENT"} {
			if strings.Contains(body, `name="`+binary+`"`) {
				t.Errorf("%s: the request asks for %s", name, binary)
			}
		}
	}
}
