package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const editedOK = `{"ok":true,"result":{"message_id":91,"date":1787220000,"caption":"secret",` +
	`"photo":[{"file_id":"small"},{"file_id":"NEWBIG"}],"location":{"latitude":1}}}`

var editTools = []capability.Descriptor{messagesEditCaption, messagesEditMedia, locationsEditLive, locationsStopLive}

func TestEditCaptionAndLiveBodiesAndResults(t *testing.T) {
	ctx := context.Background()
	lat, lon, acc := 52.52, 13.405, 12.5
	live, heading, radius := int64(foreverLivePeriod), int64(360), int64(1)
	client, bodies, paths := capture(t, "-1001", 200, editedOK)
	calls := []struct {
		run        func() (map[string]any, error)
		path, body string
	}{
		{func() (map[string]any, error) { return client.EditCaption(ctx, 91, "New", "HTML") },
			"editMessageCaption", `{"chat_id":"-1001","message_id":91,"caption":"New","parse_mode":"HTML"}`},
		{func() (map[string]any, error) { return client.EditCaption(ctx, 91, "", "HTML") },
			"editMessageCaption", `{"chat_id":"-1001","message_id":91,"caption":""}`},
		{func() (map[string]any, error) {
			return client.EditLiveLocation(ctx, 91, LiveLocationOptions{Latitude: &lat, Longitude: &lon})
		}, "editMessageLiveLocation", `{"chat_id":"-1001","message_id":91,"latitude":52.52,"longitude":13.405}`},
		{func() (map[string]any, error) {
			return client.EditLiveLocation(ctx, 91, LiveLocationOptions{Latitude: &lat, Longitude: &lon,
				HorizontalAccuracy: &acc, LivePeriod: &live, Heading: &heading, ProximityAlertRadius: &radius})
		}, "editMessageLiveLocation", `{"chat_id":"-1001","message_id":91,"latitude":52.52,"longitude":13.405,` +
			`"horizontal_accuracy":12.5,"live_period":2147483647,"heading":360,"proximity_alert_radius":1}`},
		{func() (map[string]any, error) { return client.StopLiveLocation(ctx, 91) },
			"stopMessageLiveLocation", `{"chat_id":"-1001","message_id":91}`},
	}
	for i, c := range calls {
		got, err := c.run()
		if err != nil || len(got) != 1 || got["message_id"] != int64(91) {
			t.Errorf("call %d = %v, %v", i, got, err)
		}
		if (*paths)[i] != c.path || (*bodies)[i] != c.body {
			t.Errorf("call %d = %s %s", i, (*paths)[i], (*bodies)[i])
		}
	}
}

func TestEditValuesRejectedBeforeIO(t *testing.T) {
	ctx := context.Background()
	f := func(v float64) *float64 { return &v }
	n := func(v int64) *int64 { return &v }
	client, bodies, _ := capture(t, "-1001", 200, editedOK)
	bad := []func() error{
		func() error { _, err := client.EditCaption(ctx, 0, "x", ""); return err },
		func() error { _, err := client.EditCaption(ctx, 1, strings.Repeat("x", 1025), ""); return err },
		func() error { _, err := client.EditCaption(ctx, 1, "x", "Markdown"); return err },
		func() error { _, err := client.StopLiveLocation(ctx, -1); return err },
		func() error {
			_, err := client.EditMedia(ctx, 1, mediaItem{kind: "audio", fileID: "x"}, "", "")
			return err
		},
		func() error {
			_, err := client.EditMedia(ctx, 1, mediaItem{kind: kindVideoNote, fileID: "x"}, "", "")
			return err
		},
		func() error {
			_, err := client.EditMedia(ctx, 0, mediaItem{kind: kindPhoto, fileID: "x"}, "", "")
			return err
		},
	}
	for _, o := range []LiveLocationOptions{
		{Latitude: f(91), Longitude: f(0)}, {Latitude: f(0)},
		{Latitude: f(0), Longitude: f(0), HorizontalAccuracy: f(1501)},
		{Latitude: f(0), Longitude: f(0), LivePeriod: n(59)},
		{Latitude: f(0), Longitude: f(0), LivePeriod: n(86401)},
		{Latitude: f(0), Longitude: f(0), Heading: n(0)}, {Latitude: f(0), Longitude: f(0), Heading: n(361)},
		{Latitude: f(0), Longitude: f(0), ProximityAlertRadius: n(0)},
		{Latitude: f(0), Longitude: f(0), ProximityAlertRadius: n(100001)},
	} {
		o := o
		bad = append(bad, func() error { _, err := client.EditLiveLocation(ctx, 1, o); return err })
	}
	bad = append(bad, func() error {
		_, err := client.EditLiveLocation(ctx, 0, LiveLocationOptions{Latitude: f(0), Longitude: f(0)})
		return err
	})
	for i, run := range bad {
		if run() == nil {
			t.Errorf("case %d was accepted", i)
		}
	}
	good := []func() error{
		func() error { _, err := client.EditCaption(ctx, 91, strings.Repeat("x", 1024), ""); return err },
		func() error {
			_, err := client.EditLiveLocation(ctx, 91, LiveLocationOptions{Latitude: f(-90), Longitude: f(180),
				HorizontalAccuracy: f(0), LivePeriod: n(60), Heading: n(1), ProximityAlertRadius: n(100000)})
			return err
		},
		func() error {
			_, err := client.EditLiveLocation(ctx, 91, LiveLocationOptions{Latitude: f(90), Longitude: f(-180),
				HorizontalAccuracy: f(1500), LivePeriod: n(86400), Heading: n(360), ProximityAlertRadius: n(1)})
			return err
		},
	}
	for i, run := range good {
		if err := run(); err != nil {
			t.Errorf("boundary %d = %v", i, err)
		}
	}
	if len(*bodies) != len(good) {
		t.Errorf("requests = %d, want %d (only the boundary cases)", len(*bodies), len(good))
	}
}

func TestEditHandlersRejectBeforeSecretAndIO(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	invokes := map[string]invokeFunc{
		"editcaption": invokeMessagesEditCaption, "editmedia": invokeMessagesEditMedia,
		"editlive": invokeLocationsEditLive, "stoplive": invokeLocationsStopLive,
	}
	valid := map[string]string{
		"editcaption": `"message_id":1,"caption":"x"`,
		"editmedia":   `"message_id":1,"type":"photo","file_ref":"x"`,
		"editlive":    `"message_id":1,"latitude":1,"longitude":1`,
		"stoplive":    `"message_id":1`,
	}
	for name, invoke := range invokes {
		cases := map[string]string{
			"foreign chat":  `{"chat":"-2002",` + valid[name] + `}`,
			"inline id":     `{` + valid[name] + `,"inline_message_id":"abc"}`,
			"unknown field": `{` + valid[name] + `,"reply_markup":{}}`,
			"zero message":  `{` + strings.Replace(valid[name], `"message_id":1`, `"message_id":0`, 1) + `}`,
		}
		for what, args := range cases {
			_, err := invoke(context.Background(), resolvedWith("-1001"), resolver, nil, json.RawMessage(args))
			if err == nil || strings.Contains(err.Error(), "-2002") {
				t.Errorf("%s %s err = %v", name, what, err)
			}
		}
		// Two bound chats without a chat argument are ambiguous.
		if _, err := invoke(context.Background(), resolvedWith("-1001", "-1002"), resolver, nil,
			json.RawMessage(`{`+valid[name]+`}`)); err == nil {
			t.Errorf("%s accepted a missing chat with two bound chats", name)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestEditSingleRequestAndUncertainty(t *testing.T) {
	ctx := context.Background()
	lat := 1.0
	ops := map[string]func(*Client) error{
		"caption": func(c *Client) error { _, err := c.EditCaption(ctx, 91, "x", ""); return err },
		"live": func(c *Client) error {
			_, err := c.EditLiveLocation(ctx, 91, LiveLocationOptions{Latitude: &lat, Longitude: &lat})
			return err
		},
		"stop": func(c *Client) error { _, err := c.StopLiveLocation(ctx, 91); return err },
		"media-ref": func(c *Client) error {
			_, err := c.EditMedia(ctx, 91, mediaItem{kind: kindPhoto, fileID: "ID"}, "", "")
			return err
		},
		"media-upload": func(c *Client) error {
			_, err := c.EditMedia(ctx, 91, mediaItem{kind: kindPhoto, name: "file.jpg", data: []byte("x")}, "", "")
			return err
		},
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
		// An unreadable result, true, or another message is never taken as success.
		for _, payload := range []string{`{"ok":true,"result":"garbage"}`, `{"ok":true,"result":true}`,
			`{"ok":true,"result":{"message_id":5,"date":1}}`} {
			client, _, _ = capture(t, "-1001", 200, payload)
			if err := op(client); err == nil || !strings.Contains(err.Error(), "may have taken effect") {
				t.Errorf("%s invalid result %s err = %v", name, payload, err)
			}
		}
	}
}

func TestEditMediaUploadsLocalFileAsMultipart(t *testing.T) {
	e := newMediaEnv(t)
	e.answer = func(*http.Request) (*http.Response, error) { return response(200, editedOK), nil }
	path := e.write(t, "private-name.JPG", 12)
	got, err := invokeMessagesEditMediaWith(capability.WithConfirmed(context.Background()), e.resolved, e.secrets, e.red,
		json.RawMessage(`{"message_id":91,"type":"photo","local_path":`+quote(path)+`,"caption":"New","parse_mode":"HTML"}`),
		e.client())
	if err != nil {
		t.Fatal(err)
	}
	result := got.(map[string]any)
	if len(result) != 2 || result["message_id"] != int64(91) || result["file_ref"] != e.ref(mediaChat, "NEWBIG") {
		t.Errorf("result = %v", result)
	}
	if len(e.requests) != 1 || e.requests[0].path != "POST /bot"+testToken+"/editMessageMedia" {
		t.Fatalf("requests = %+v", e.requests)
	}
	fields, files := parts(t, e.requests[0])
	if fields["chat_id"] != mediaChat || fields["message_id"] != "91" ||
		fields["media"] != `{"type":"photo","media":"attach://file0","caption":"New","parse_mode":"HTML"}` || len(fields) != 3 {
		t.Errorf("fields = %v", fields)
	}
	if len(files) != 1 || files["file0"][0] != "file.JPG" || files["file0"][1] != strings.Repeat("x", 12) {
		t.Errorf("files = %v", files)
	}
	if strings.Contains(string(e.requests[0].body), e.dir) || strings.Contains(string(e.requests[0].body), "private-name") {
		t.Error("the local path or name reached Telegram")
	}
}

func TestEditMediaSendsFileRefAsJSON(t *testing.T) {
	e := newMediaEnv(t)
	e.answer = func(*http.Request) (*http.Response, error) { return response(200, editedOK), nil }
	got, err := invokeMessagesEditMediaWith(capability.WithConfirmed(context.Background()), e.resolved, e.secrets, e.red,
		json.RawMessage(`{"message_id":91,"type":"document","file_ref":`+quote(e.ref(mediaChat, "DOC"))+`}`), e.client())
	if err != nil {
		t.Fatal(err)
	}
	if got.(map[string]any)["message_id"] != int64(91) {
		t.Errorf("result = %v", got)
	}
	r := e.requests[0]
	if len(e.requests) != 1 || r.path != "POST /bot"+testToken+"/editMessageMedia" || r.contentType != "application/json" ||
		string(r.body) != `{"chat_id":"-1001","message_id":91,"media":{"type":"document","media":"DOC"}}` {
		t.Errorf("request = %+v %s", r, r.body)
	}
}

func TestEditMediaRejectsBeforeSecretAndIO(t *testing.T) {
	e := newMediaEnv(t, mediaChat, "-1002")
	big := e.write(t, "big.jpg", maxPhotoBytes+1)
	small := e.write(t, "small.jpg", 12)
	cases := map[string]string{
		"audio type":     `{"message_id":1,"type":"audio","local_path":` + quote(small) + `}`,
		"video note":     `{"message_id":1,"type":"videonote","local_path":` + quote(small) + `}`,
		"raw file id":    `{"message_id":1,"type":"photo","file_ref":"AgACAgIAAxkBAAI"}`,
		"foreign ref":    `{"message_id":1,"type":"photo","file_ref":` + quote(e.ref("-1002", "X")) + `,"chat":"` + mediaChat + `"}`,
		"both sources":   `{"message_id":1,"type":"photo","file_ref":` + quote(e.ref(mediaChat, "X")) + `,"local_path":` + quote(small) + `}`,
		"no source":      `{"message_id":1,"type":"photo","chat":"` + mediaChat + `"}`,
		"oversize photo": `{"message_id":1,"type":"photo","local_path":` + quote(big) + `,"chat":"` + mediaChat + `"}`,
		"outside":        `{"message_id":1,"type":"photo","local_path":"/etc/hostname","chat":"` + mediaChat + `"}`,
		"foreign chat":   `{"message_id":1,"type":"photo","local_path":` + quote(small) + `,"chat":"-9999"}`,
		"inline":         `{"message_id":1,"type":"photo","local_path":` + quote(small) + `,"inline_message_id":"a","chat":"` + mediaChat + `"}`,
		"long caption":   `{"message_id":1,"type":"photo","local_path":` + quote(small) + `,"chat":"` + mediaChat + `","caption":"` + strings.Repeat("x", 1025) + `"}`,
	}
	for name, args := range cases {
		if _, err := invokeMessagesEditMediaWith(capability.WithConfirmed(context.Background()), e.resolved, e.secrets,
			e.red, json.RawMessage(args), e.client()); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if e.lookups != 0 || len(e.requests) != 0 {
		t.Errorf("secret lookups = %d, requests = %d, want 0", e.lookups, len(e.requests))
	}
}

func TestEditToolsRiskGroupAndProfiles(t *testing.T) {
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
			for _, d := range editTools {
				if tool == d.ID {
					t.Errorf("profile %s contains %s", profile.ID, tool)
				}
			}
		}
	}
	want := map[string]struct {
		group            string
		idempotency      capability.Idempotency
		sensitivity      string
		allowList, files bool
	}{
		"telegram.messages.editcaption": {groupMessages, capability.IdempotencyUnknown, dataSensitivity, false, false},
		"telegram.messages.editmedia":   {groupMessages, capability.IdempotencyUnknown, dataSensitivity, false, true},
		"telegram.locations.editlive":   {groupInteractions, capability.IdempotencyUnknown, personalDataSensitivity, false, false},
		"telegram.locations.stoplive":   {groupInteractions, capability.IdempotencyIdempotent, personalDataSensitivity, true, false},
	}
	for _, d := range editTools {
		w := want[d.ID]
		r := d.Risk
		if d.Group != w.group || r.Effect != capability.EffectUpdate || r.Idempotency != w.idempotency ||
			r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld || r.DataSensitivity != w.sensitivity ||
			d.RequiresToolAllowList != w.allowList || (d.LocalFiles == config.LocalFilesRead) != w.files {
			t.Errorf("descriptor %s = %+v", d.ID, d)
		}
		if strings.Contains(string(d.InputSchema), "inline_message_id") || strings.Contains(string(d.InputSchema), "reply_markup") {
			t.Errorf("%s offers an inline or keyboard argument", d.ID)
		}
	}
}

func TestEditSchemaLimitsMatchChecks(t *testing.T) {
	type prop struct {
		Type      string   `json:"type"`
		Min       *float64 `json:"minimum"`
		Max       *float64 `json:"maximum"`
		MaxLength *int     `json:"maxLength"`
		Enum      []string `json:"enum"`
	}
	read := func(d capability.Descriptor) map[string]prop {
		var schema struct {
			Properties map[string]prop `json:"properties"`
		}
		if err := json.Unmarshal(d.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		return schema.Properties
	}
	for _, d := range []capability.Descriptor{messagesEditCaption, messagesEditMedia} {
		if got := read(d)["caption"].MaxLength; got == nil || *got != maxCaptionRunes {
			t.Errorf("%s caption maxLength = %v, want %d", d.ID, got, maxCaptionRunes)
		}
		if !strings.Contains(argumentText(d, "caption"), "1024") {
			t.Errorf("%s caption description does not state 1024", d.ID)
		}
	}
	if got := read(messagesEditMedia)["type"].Enum; strings.Join(got, ",") != strings.Join(editMediaKinds, ",") {
		t.Errorf("type enum = %v, want %v", got, editMediaKinds)
	}
	live := read(locationsEditLive)
	for _, c := range []struct {
		name     string
		min, max float64
		text     string
	}{
		{"horizontal_accuracy", 0, maxHorizontalAccuracy, "from 0 through 1500"},
		{"live_period", minLivePeriod, foreverLivePeriod, "60 through 86400, or 2147483647"},
		{"heading", minHeading, maxHeading, "from 1 through 360"},
		{"proximity_alert_radius", minProximityRadius, maxProximityRadius, "from 1 through 100000"},
		{"latitude", -90, 90, "-90 through 90"},
		{"longitude", -180, 180, "-180 through 180"},
	} {
		p := live[c.name]
		if p.Min == nil || p.Max == nil || *p.Min != c.min || *p.Max != c.max {
			t.Errorf("%s schema = %+v, want %v..%v", c.name, p, c.min, c.max)
		}
		if !strings.Contains(argumentText(locationsEditLive, c.name), c.text) {
			t.Errorf("%s description does not state %q", c.name, c.text)
		}
	}
}

func argumentText(d capability.Descriptor, name string) string {
	for _, a := range d.Arguments {
		if a.Name == name {
			return a.Description
		}
	}
	return ""
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

func (e *mediaEnv) client() *http.Client {
	c := newHTTPClient()
	c.Transport = e.transport()
	return c
}
