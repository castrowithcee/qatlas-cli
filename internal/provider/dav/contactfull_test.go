package dav

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// rfcCard is the example of RFC 2426 section 5 with the addresses moved to a reserved domain.
const rfcCard = "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Mr. John Q. Public\\, Esq.\r\nN:Public;John;Quinlan;Mr.;Esq.\r\n" +
	"NICKNAME:Robbie\r\n" +
	"ORG:Bubba Gump Shrimp Co.\r\nTITLE:Shrimp Man\r\n" +
	"TEL;TYPE=WORK,VOICE:213-555-1234\r\n" +
	"ADR;TYPE=WORK:;;100 Waters Edge;Baytown;LA;30314;United States of America\r\n" +
	"LABEL;TYPE=WORK:100 Waters Edge\\nBaytown\\, LA 30314\\nUnited States of America\r\n" +
	"EMAIL;TYPE=PREF,INTERNET:jqpublic@example.com\r\nREV:20060215T175000Z\r\nUID:19950401-080045-40000F192713-0052\r\nEND:VCARD\r\n"

// withExtras adds properties to the card; an ORG among them replaces the one of the example.
func withExtras(extra ...string) string {
	card := rfcCard
	for _, line := range extra {
		if strings.HasPrefix(line, "ORG:") {
			card = strings.Replace(card, "ORG:Bubba Gump Shrimp Co.\r\n", "", 1)
		}
	}
	return strings.Replace(card, "REV:", strings.Join(extra, "\r\n")+"\r\nREV:", 1)
}

func TestParseContactReadsEveryRFC2426Property(t *testing.T) {
	card := withExtras(
		"NICKNAME:Rob,Bobby",
		"MAILER:ExampleMail",
		"TZ:-05:00",
		"GEO:37.386013;-122.082932",
		"ROLE:Executive",
		"CATEGORIES:TRAVEL AGENT,INTERNET",
		"CATEGORIES:VIP",
		"CLASS:CONFIDENTIAL",
		"SORT-STRING:Public",
		"PRODID:-//ONLINE DIRECTORY//EN",
		"NAME:Public card",
		"PROFILE:VCARD",
		"SOURCE:http://directory.example.org/jqpublic.vcf",
		"SOURCE:https://directory.example.org/jqpublic.vcf?token=secret",
		"PHOTO;VALUE=uri:http://www.example.org/photo.gif",
		"LOGO;VALUE=uri:https://www.example.org/logo.png",
		"SOUND;VALUE=uri:http://www.example.org/sound.ogg",
		"AGENT;VALUE=uri:https://www.example.org/agent.vcf",
		"KEY;VALUE=uri:https://www.example.org/key.asc",
		"ORG:Bubba Gump Shrimp Co.;Sales;Harbor",
		"EMAIL;PREF=1;TYPE=work:pref@example.com",
		"TEL;PREF=101:213-555-9999",
		"ADR;PREF=2;TYPE=home:;;2 Main St;Baytown;LA;30314;USA",
	)
	contact, err := ParseContact(card, true)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(contact)
	var out map[string]any
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"nicknames": []any{"Robbie", "Rob", "Bobby"}, "mailer": "ExampleMail", "timezone": "-05:00",
		"geo": "37.386013;-122.082932", "role": "Executive", "class": "CONFIDENTIAL", "sort_string": "Public",
		"revision": "20060215T175000Z", "product_id": "-//ONLINE DIRECTORY//EN", "name_source": "Public card",
		"profile": "VCARD", "source": []any{"http://directory.example.org/jqpublic.vcf"},
		"categories":         []any{"TRAVEL AGENT", "INTERNET", "VIP"},
		"organization":       "Bubba Gump Shrimp Co., Sales, Harbor",
		"organization_units": []any{"Sales", "Harbor"},
		"photo_uri":          "http://www.example.org/photo.gif", "logo_uri": "https://www.example.org/logo.png",
		"sound_uri": "http://www.example.org/sound.ogg", "agent_uri": "https://www.example.org/agent.vcf",
		"key_uri": "https://www.example.org/key.asc",
	}
	for key, value := range want {
		if !reflect.DeepEqual(out[key], value) {
			t.Errorf("%s = %#v, want %#v", key, out[key], value)
		}
	}
	labels, _ := out["labels"].([]any)
	if len(labels) != 1 || labels[0].(map[string]any)["type"] != "work" ||
		labels[0].(map[string]any)["value"] != "100 Waters Edge\nBaytown, LA 30314\nUnited States of America" {
		t.Errorf("labels = %#v", out["labels"])
	}
	if len(contact.Emails) != 2 || contact.Emails[0].Pref != 0 || contact.Emails[1].Pref != 1 ||
		contact.Emails[1].Value != "pref@example.com" || contact.Emails[0].Type != "pref,internet" {
		t.Errorf("emails = %+v", contact.Emails)
	}
	if len(contact.Phones) != 2 || contact.Phones[1].Pref != 0 {
		t.Errorf("phones = %+v", contact.Phones)
	}
	if len(contact.Addresses) != 2 || contact.Addresses[1].Pref != 2 {
		t.Errorf("addresses = %+v", contact.Addresses)
	}
}

func TestParseContactNeverReportsBinaryKeysAgentCardsOrTokenAddresses(t *testing.T) {
	card := withExtras(
		"PHOTO;ENCODING=b;TYPE=JPEG:UEhPVE8tQ0FOQVJZ",
		"LOGO;ENCODING=b;TYPE=PNG:TE9HTy1DQU5BUlk=",
		"SOUND;ENCODING=b;TYPE=BASIC:U09VTkQtQ0FOQVJZ",
		"KEY;ENCODING=b;TYPE=PGP:S0VZLUNBTkFSWQ==",
		"AGENT:BEGIN:VCARD\\nFN:AGENT-CANARY\\nEND:VCARD\\n",
		"SOURCE:http://user:pw@example.org/a.vcf",
		"SOURCE:http://directory.example.org/a.vcf#frag",
		"SOURCE:ftp://directory.example.org/a.vcf",
		"SOURCE:http://directory.example.org/"+strings.Repeat("a", 1100),
		"X-CUSTOM:X-CANARY",
	)
	contact, err := ParseContact(card, true)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(contact)
	for _, canary := range []string{"UEhPVE8", "TE9HTy1", "U09VTkQ", "S0VZ", "AGENT-CANARY", "user", "frag", "ftp:", "X-CANARY", "aaaa"} {
		if strings.Contains(string(got), canary) {
			t.Errorf("%q was reported: %s", canary, got)
		}
	}
	if contact.PhotoURI != "" || contact.LogoURI != "" || contact.SoundURI != "" || contact.KeyURI != "" ||
		contact.AgentURI != "" || len(contact.Source) != 0 {
		t.Errorf("contact = %+v", contact)
	}
	// An embedded photo marked as a URI value still carries data, not an address.
	embedded, err := ParseContact(withExtras("PHOTO;VALUE=uri;ENCODING=b:aHR0cDovL2V4YW1wbGUub3JnLw=="), true)
	if err != nil || embedded.PhotoURI != "" {
		t.Errorf("embedded = %+v, err = %v", embedded, err)
	}
}

func TestParseContactCapsTheNewLists(t *testing.T) {
	var lines []string
	for i := 0; i < 25; i++ {
		lines = append(lines, "SOURCE:http://directory.example.org/"+strings.Repeat("a", i+1),
			"LABEL:label "+strings.Repeat("b", i+1))
	}
	nicknames := strings.TrimSuffix(strings.Repeat("n,", 25), ",")
	lines = append(lines, "NICKNAME:"+nicknames, "CATEGORIES:"+strings.Repeat("c,", 24)+"c",
		"ORG:Co"+strings.Repeat(";u", 25))
	contact, err := ParseContact(withExtras(lines...), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(contact.Source) != MaxContactEntries || len(contact.Labels) != MaxContactEntries ||
		len(contact.Nicknames) != MaxContactEntries || len(contact.Categories) != MaxContactEntries ||
		len(contact.OrganizationUnits) != MaxContactEntries {
		t.Errorf("lists = %d %d %d %d %d", len(contact.Source), len(contact.Labels), len(contact.Nicknames),
			len(contact.Categories), len(contact.OrganizationUnits))
	}
	if got := strings.Join(contact.Truncated, ","); got != "nicknames,categories,source,labels,organization_units" {
		t.Errorf("truncated = %s", got)
	}
}

func TestParseContactCompactIsUnchangedByTheNewProperties(t *testing.T) {
	plain, err := ParseContact(rfcCard, false)
	if err != nil {
		t.Fatal(err)
	}
	rich, err := ParseContact(withExtras("ROLE:Executive", "NICKNAME:Rob", "PHOTO;VALUE=uri:http://www.example.org/p.gif",
		"ORG:Bubba Gump Shrimp Co.;Sales"), false)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(plain)
	if want := `{"id":"","uid":"19950401-080045-40000F192713-0052","name":"Mr. John Q. Public, Esq.",` +
		`"email":"jqpublic@example.com","organization":"Bubba Gump Shrimp Co."}`; string(a) != want {
		t.Errorf("compact = %s", a)
	}
	b, _ := json.Marshal(rich)
	if want := `{"id":"","uid":"19950401-080045-40000F192713-0052","name":"Mr. John Q. Public, Esq.",` +
		`"email":"jqpublic@example.com","organization":"Bubba Gump Shrimp Co., Sales"}`; string(b) != want {
		t.Errorf("compact with extras = %s", b)
	}
}
