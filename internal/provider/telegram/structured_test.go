package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var structuredInvokers = map[string]invokeFunc{
	"locations": invokeLocationsSend, "venues": invokeVenuesSend, "contacts": invokeContactsSend, "dice": invokeDiceSend,
}

const structuredOK = `{"ok":true,"result":{"message_id":7,"date":1787220000,"text":"secret","location":{"latitude":1}}}`

func TestStructuredBodiesPathsAndResults(t *testing.T) {
	ctx := context.Background()
	lat, lon, acc := 52.52, 13.405, 12.5
	live, heading, radius := int64(foreverLivePeriod), int64(360), int64(1)
	zero := 0.0
	client, bodies, paths := capture(t, "-1001", 200, structuredOK)
	common := SendCommon{ReplyToMessageID: 3, MessageThreadID: 4, DisableNotification: true, ProtectContent: true}
	calls := []struct {
		run        func() (map[string]any, error)
		path, body string
	}{
		{func() (map[string]any, error) {
			return client.SendLocation(ctx, LocationOptions{Latitude: &lat, Longitude: &lon})
		}, "sendLocation", `{"chat_id":"-1001","latitude":52.52,"longitude":13.405}`},
		{func() (map[string]any, error) {
			return client.SendLocation(ctx, LocationOptions{Latitude: &lat, Longitude: &lon, HorizontalAccuracy: &acc,
				LivePeriod: &live, Heading: &heading, ProximityAlertRadius: &radius, SendCommon: common})
		}, "sendLocation", `{"chat_id":"-1001","latitude":52.52,"longitude":13.405,"horizontal_accuracy":12.5,` +
			`"live_period":2147483647,"heading":360,"proximity_alert_radius":1,"message_thread_id":4,` +
			`"disable_notification":true,"protect_content":true,"reply_parameters":{"message_id":3}}`},
		{func() (map[string]any, error) {
			return client.SendLocation(ctx, LocationOptions{Latitude: &zero, Longitude: &zero, HorizontalAccuracy: &zero})
		}, "sendLocation", `{"chat_id":"-1001","latitude":0,"longitude":0,"horizontal_accuracy":0}`},
		{func() (map[string]any, error) {
			return client.SendVenue(ctx, VenueOptions{Latitude: &lat, Longitude: &lon, Title: "Office", Address: "Street 1",
				SendCommon: common})
		}, "sendVenue", `{"chat_id":"-1001","latitude":52.52,"longitude":13.405,"title":"Office","address":"Street 1",` +
			`"message_thread_id":4,"disable_notification":true,"protect_content":true,"reply_parameters":{"message_id":3}}`},
		{func() (map[string]any, error) {
			return client.SendContact(ctx, ContactOptions{PhoneNumber: "+49170", FirstName: "Alex"})
		}, "sendContact", `{"chat_id":"-1001","phone_number":"+49170","first_name":"Alex"}`},
		{func() (map[string]any, error) {
			return client.SendContact(ctx, ContactOptions{PhoneNumber: "+49170", FirstName: "Alex", LastName: "Doe",
				SendCommon: SendCommon{ReplyToMessageID: 9}})
		}, "sendContact", `{"chat_id":"-1001","phone_number":"+49170","first_name":"Alex","last_name":"Doe",` +
			`"reply_parameters":{"message_id":9}}`},
		{func() (map[string]any, error) { return client.SendDice(ctx, DiceOptions{}) },
			"sendDice", `{"chat_id":"-1001"}`},
		{func() (map[string]any, error) { return client.SendDice(ctx, DiceOptions{Emoji: "\U0001f3af"}) },
			"sendDice", `{"chat_id":"-1001","emoji":"🎯"}`},
	}
	for i, c := range calls {
		got, err := c.run()
		if err != nil || len(got) != 2 || got["message_id"] != int64(7) || got["date"] != int64(1787220000) {
			t.Errorf("call %d = %v, %v", i, got, err)
		}
		if (*paths)[i] != c.path || (*bodies)[i] != c.body {
			t.Errorf("call %d = %s %s", i, (*paths)[i], (*bodies)[i])
		}
	}
}

func TestStructuredBoundaries(t *testing.T) {
	ctx := context.Background()
	f := func(v float64) *float64 { return &v }
	n := func(v int64) *int64 { return &v }
	long := func(k int) string { return strings.Repeat("x", k) }
	client, bodies, _ := capture(t, "-1001", 200, structuredOK)
	good := []func() error{
		func() error {
			_, err := client.SendLocation(ctx, LocationOptions{Latitude: f(-90), Longitude: f(-180)})
			return err
		},
		func() error {
			_, err := client.SendLocation(ctx, LocationOptions{Latitude: f(90), Longitude: f(180)})
			return err
		},
		func() error {
			_, err := client.SendLocation(ctx, LocationOptions{Latitude: f(1), Longitude: f(1), HorizontalAccuracy: f(1500)})
			return err
		},
		func() error {
			_, err := client.SendLocation(ctx, LocationOptions{Latitude: f(1), Longitude: f(1), LivePeriod: n(60)})
			return err
		},
		func() error {
			_, err := client.SendLocation(ctx, LocationOptions{Latitude: f(1), Longitude: f(1), LivePeriod: n(86400),
				Heading: n(1), ProximityAlertRadius: n(100000)})
			return err
		},
		func() error {
			_, err := client.SendVenue(ctx, VenueOptions{Latitude: f(1), Longitude: f(1), Title: long(256), Address: long(512)})
			return err
		},
		func() error {
			_, err := client.SendContact(ctx, ContactOptions{PhoneNumber: long(64), FirstName: long(64), LastName: long(64)})
			return err
		},
	}
	for i, run := range good {
		if err := run(); err != nil {
			t.Errorf("valid boundary %d = %v", i, err)
		}
	}
	if len(*bodies) != len(good) {
		t.Fatalf("requests = %d, want %d", len(*bodies), len(good))
	}
	for _, e := range diceEmoji {
		if _, err := client.SendDice(ctx, DiceOptions{Emoji: e}); err != nil {
			t.Errorf("dice %q = %v", e, err)
		}
	}
}

func TestStructuredValuesRejectedBeforeIO(t *testing.T) {
	long := func(k int) string { return `"` + strings.Repeat("x", k) + `"` }
	base := `"latitude":1,"longitude":1`
	cases := map[string][]string{
		"locations": {
			`{}`, `{"latitude":1}`, `{"longitude":1}`, `{"latitude":90.001,"longitude":1}`, `{"latitude":-90.001,"longitude":1}`,
			`{"latitude":1,"longitude":180.001}`, `{"latitude":1,"longitude":-180.001}`,
			`{` + base + `,"horizontal_accuracy":-0.1}`, `{` + base + `,"horizontal_accuracy":1500.1}`,
			`{` + base + `,"live_period":0}`, `{` + base + `,"live_period":59}`, `{` + base + `,"live_period":86401}`,
			`{` + base + `,"live_period":2147483646}`, `{` + base + `,"live_period":2147483648}`,
			`{` + base + `,"heading":90}`, `{` + base + `,"proximity_alert_radius":5}`,
			`{` + base + `,"live_period":60,"heading":0}`, `{` + base + `,"live_period":60,"heading":361}`,
			`{` + base + `,"live_period":60,"proximity_alert_radius":0}`,
			`{` + base + `,"live_period":60,"proximity_alert_radius":100001}`,
			`{` + base + `,"reply_to_message_id":-1}`, `{` + base + `,"message_thread_id":-1}`,
			`{` + base + `,"allow_paid_broadcast":true}`, `{` + base + `,"method":"sendMessage"}`,
			`{` + base + `,"latitude":"x"}`,
		},
		"venues": {
			`{` + base + `,"title":"t"}`, `{` + base + `,"address":"a"}`, `{` + base + `,"title":"","address":"a"}`,
			`{` + base + `,"title":"t","address":""}`, `{` + base + `,"title":` + long(257) + `,"address":"a"}`,
			`{` + base + `,"title":"t","address":` + long(513) + `}`, `{"latitude":91,"longitude":1,"title":"t","address":"a"}`,
			`{"latitude":1,"title":"t","address":"a"}`, `{` + base + `,"title":"t","address":"a","foursquare_id":"x"}`,
		},
		"contacts": {
			`{}`, `{"phone_number":"1"}`, `{"first_name":"a"}`, `{"phone_number":"","first_name":"a"}`,
			`{"phone_number":"1","first_name":""}`, `{"phone_number":` + long(65) + `,"first_name":"a"}`,
			`{"phone_number":"1","first_name":` + long(65) + `}`, `{"phone_number":"1","first_name":"a","last_name":` + long(65) + `}`,
			`{"phone_number":"1","first_name":"a","vcard":"BEGIN:VCARD"}`,
		},
		"dice": {`{"emoji":"x"}`, `{"emoji":"🎲🎲"}`, `{"emoji":"❤"}`, `{"emoji":"🏆"}`, `{"emoji":1}`,
			`{"reply_to_message_id":-1}`},
	}
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	for name, list := range cases {
		for _, args := range list {
			if _, err := structuredInvokers[name](context.Background(), resolvedWith("-1001"), resolver, nil,
				json.RawMessage(args)); err == nil {
				t.Errorf("%s accepted %s", name, args)
			}
			if resolutions != 0 {
				t.Fatalf("%s resolved the secret for %s", name, args)
			}
		}
	}
}

func TestStructuredRejectUnboundChatBeforeSecret(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	args := map[string]string{
		"locations": `{"chat":"-2002","latitude":1,"longitude":1}`,
		"venues":    `{"chat":"-2002","latitude":1,"longitude":1,"title":"t","address":"a"}`,
		"contacts":  `{"chat":"-2002","phone_number":"1","first_name":"a"}`,
		"dice":      `{"chat":"-2002"}`,
	}
	for name, invoke := range structuredInvokers {
		_, err := invoke(context.Background(), resolvedWith("-1001"), resolver, nil, json.RawMessage(args[name]))
		if err == nil || strings.Contains(err.Error(), "-2002") {
			t.Errorf("%s err = %v", name, err)
		}
		// Without a chat argument two bound chats are ambiguous.
		noChat := strings.Replace(args[name], `"chat":"-2002",`, "", 1)
		noChat = strings.Replace(noChat, `{"chat":"-2002"}`, `{}`, 1)
		if _, err := invoke(context.Background(), resolvedWith("-1001", "-1002"), resolver, nil,
			json.RawMessage(noChat)); err == nil {
			t.Errorf("%s accepted a missing chat with two bound chats", name)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestStructuredSingleRequestAndUncertainty(t *testing.T) {
	ctx := context.Background()
	lat := 1.0
	ops := map[string]func(*Client) error{
		"location": func(c *Client) error {
			_, err := c.SendLocation(ctx, LocationOptions{Latitude: &lat, Longitude: &lat})
			return err
		},
		"venue": func(c *Client) error {
			_, err := c.SendVenue(ctx, VenueOptions{Latitude: &lat, Longitude: &lat, Title: "t", Address: "a"})
			return err
		},
		"contact": func(c *Client) error {
			_, err := c.SendContact(ctx, ContactOptions{PhoneNumber: "1", FirstName: "a"})
			return err
		},
		"dice": func(c *Client) error { _, err := c.SendDice(ctx, DiceOptions{}); return err },
	}
	for name, op := range ops {
		for _, status := range []int{500, 502} {
			client, bodies, _ := capture(t, "-1001", status, `{"ok":false,"description":"secret text"}`)
			err := op(client)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "secret text") {
				t.Errorf("%s status %d err = %v", name, status, err)
			}
			if len(*bodies) != 1 {
				t.Errorf("%s requests = %d, want 1", name, len(*bodies))
			}
		}
		calls := 0
		client, _ := telegramClient(t, "-1001", roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, &timeoutError{}
		}))
		if err := op(client); err == nil || !strings.Contains(err.Error(), "may have taken effect") || calls != 1 {
			t.Errorf("%s timeout err = %v calls = %d", name, err, calls)
		}
		for _, payload := range []string{`{"ok":true,"result":"garbage"}`, `{"ok":true,"result":{"message_id":0,"date":1}}`} {
			client, _, _ = capture(t, "-1001", 200, payload)
			if err := op(client); err == nil || !strings.Contains(err.Error(), "may have taken effect") {
				t.Errorf("%s invalid result err = %v", name, err)
			}
		}
	}
}

func TestStructuredRiskGroupAndProfiles(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			for _, d := range []capability.Descriptor{locationsSend, venuesSend, contactsSend, diceSend} {
				if tool == d.ID {
					t.Errorf("profile %s contains %s", profile.ID, tool)
				}
			}
		}
	}
	for _, d := range []capability.Descriptor{locationsSend, venuesSend, contactsSend, diceSend} {
		want := personalDataSensitivity
		if d.ID == diceSend.ID {
			want = dataSensitivity
		}
		r := d.Risk
		if d.Group != groupInteractions || r.Effect != capability.EffectCreate ||
			r.Idempotency != capability.IdempotencyNonIdempotent || r.Confirmation != capability.ConfirmationRequired ||
			!r.OpenWorld || r.DataSensitivity != want || d.RequiresToolAllowList {
			t.Errorf("descriptor %s = %+v", d.ID, d)
		}
		if len(d.Fields) != 2 || string(d.OutputSchema) != structuredOutputSchema {
			t.Errorf("descriptor %s output = %v %s", d.ID, d.Fields, d.OutputSchema)
		}
	}
}

func TestStructuredSchemaLengthsMatchChecks(t *testing.T) {
	for _, c := range []struct {
		d     capability.Descriptor
		field string
		max   int
	}{
		{venuesSend, "title", maxVenueTitleRunes}, {venuesSend, "address", maxVenueAddressRunes},
		{contactsSend, "phone_number", maxContactPhoneRunes}, {contactsSend, "first_name", maxContactNameRunes},
		{contactsSend, "last_name", maxContactNameRunes},
	} {
		var schema struct {
			Properties map[string]struct {
				MaxLength int `json:"maxLength"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(c.d.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		if got := schema.Properties[c.field].MaxLength; got != c.max {
			t.Errorf("%s %s maxLength = %d, want %d", c.d.ID, c.field, got, c.max)
		}
	}
}
