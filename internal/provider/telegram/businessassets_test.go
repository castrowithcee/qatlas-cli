package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

func (e *mediaEnv) businessTool(tool string, arguments any) (any, error) {
	raw, _ := json.Marshal(arguments)
	ctx := capability.WithConfirmed(context.Background())
	switch tool {
	case "setphoto":
		return invokeBusinessSetPhotoWith(ctx, e.resolved, e.secrets, e.red, raw, e.client())
	case "removephoto":
		return invokeBusinessRemovePhotoWith(ctx, e.resolved, e.secrets, e.red, raw, e.client())
	case "gifts":
		return invokeBusinessGiftsWith(ctx, e.resolved, e.secrets, e.red, raw, e.client())
	}
	return invokeBusinessStarBalanceWith(ctx, e.resolved, e.secrets, e.red, raw, e.client())
}

func TestBusinessSetPhotoSendsExactNeutralMultipart(t *testing.T) {
	e := newMediaEnv(t, "business/b1")
	e.answer = func(*http.Request) (*http.Response, error) { return response(200, `{"ok":true,"result":true}`), nil }
	path := e.write(t, "private-name.JPG", 12)
	out, err := e.businessTool("setphoto", map[string]any{"business": "b1", "local_path": path, "is_public": true})
	if err != nil || out.(map[string]any)["updated"] != true {
		t.Fatalf("out = %v, %v", out, err)
	}
	if len(e.requests) != 1 || e.requests[0].path != "POST /bot"+testToken+"/setBusinessAccountProfilePhoto" {
		t.Fatalf("requests = %+v", e.requests)
	}
	fields, files := parts(t, e.requests[0])
	if len(fields) != 3 || fields["business_connection_id"] != "b1" || fields["is_public"] != "true" ||
		fields["photo"] != `{"type":"static","photo":"attach://profile"}` || len(files) != 1 ||
		files["profile"] != [2]string{"file.JPG", strings.Repeat("x", 12)} {
		t.Errorf("fields = %v files = %v", fields, files)
	}
	if body := string(e.requests[0].body); strings.Contains(body, e.dir) || strings.Contains(body, "private-name") {
		t.Error("path or file name reached the request")
	}
}

func TestBusinessSetPhotoRefusesBeforeSecretAndRead(t *testing.T) {
	e := newMediaEnv(t, "business/b1")
	good := e.write(t, "ok.jpg", 4)
	other := newMediaEnv(t, "business/b2")
	other.write(t, "ok.jpg", 4)
	for name, arguments := range map[string]map[string]any{
		"no business":     {"local_path": good},
		"foreign":         {"business": "zz", "local_path": good},
		"outside release": {"business": "b1", "local_path": t.TempDir() + "/a.jpg"},
		"too large":       {"business": "b1", "local_path": e.write(t, "big.jpg", maxPhotoBytes+1)},
		"missing path":    {"business": "b1"},
		"file ref":        {"business": "b1", "file_ref": "x"},
		"photo":           {"business": "b1", "local_path": good, "photo": "x"},
		"animated":        {"business": "b1", "local_path": e.write(t, "a.mp4", 4)},
		"url":             {"business": "b1", "local_path": "https://example.test/a.jpg"},
	} {
		if _, err := e.businessTool("setphoto", arguments); err == nil || strings.Contains(err.Error(), e.dir) {
			t.Errorf("%s err = %v", name, err)
		}
	}
	// A connection without a read release refuses before any secret access.
	e.resolved.Files.Read = nil
	if _, err := e.businessTool("setphoto", map[string]any{"business": "b1", "local_path": good}); err == nil {
		t.Error("accepted without read release")
	}
	if e.lookups != 0 || len(e.requests) != 0 {
		t.Errorf("lookups = %d requests = %d, want 0", e.lookups, len(e.requests))
	}
}

func TestBusinessReadsAndRemoveRefuseBeforeSecret(t *testing.T) {
	e := newMediaEnv(t, "business/b1")
	for _, tool := range []string{"removephoto", "gifts", "starbalance"} {
		for name, arguments := range map[string]map[string]any{
			"missing":   {},
			"foreign":   {"business": "zz"},
			"no target": {"business": "b2"},
			"unknown":   {"business": "b1", "exclude_unique": true},
			"photo":     {"business": "b1", "photo": "x"},
		} {
			if _, err := e.businessTool(tool, arguments); err == nil {
				t.Errorf("%s %s accepted", tool, name)
			}
		}
	}
	for name, arguments := range map[string]map[string]any{
		"zero":     {"business": "b1", "limit": 0},
		"over":     {"business": "b1", "limit": 101},
		"long off": {"business": "b1", "offset": strings.Repeat("a", maxGiftsOffset+1)},
	} {
		if _, err := e.businessTool("gifts", arguments); err == nil {
			t.Errorf("gifts %s accepted", name)
		}
	}
	if e.lookups != 0 || len(e.requests) != 0 {
		t.Errorf("lookups = %d requests = %d, want 0", e.lookups, len(e.requests))
	}
}

func TestBusinessRemovePhotoAndStarBalanceBodies(t *testing.T) {
	e := newMediaEnv(t, "business/b1")
	e.answer = func(*http.Request) (*http.Response, error) { return response(200, `{"ok":true,"result":true}`), nil }
	out, err := e.businessTool("removephoto", map[string]any{"business": "b1", "is_public": false})
	if err != nil || out.(map[string]any)["deleted"] != true {
		t.Fatalf("out = %v, %v", out, err)
	}
	e.answer = func(*http.Request) (*http.Response, error) {
		return response(200, `{"ok":true,"result":{"amount":5,"nanostar_amount":7,"extra":1}}`), nil
	}
	out, err = e.businessTool("starbalance", map[string]any{"business": "b1"})
	if got, _ := json.Marshal(out); err != nil || string(got) != `{"amount":5,"nanostar_amount":7}` {
		t.Fatalf("out = %s, %v", got, err)
	}
	if len(e.requests) != 2 || e.requests[0].path != "POST /bot"+testToken+"/removeBusinessAccountProfilePhoto" ||
		string(e.requests[0].body) != `{"business_connection_id":"b1","is_public":false}` ||
		e.requests[1].path != "POST /bot"+testToken+"/getBusinessAccountStarBalance" ||
		string(e.requests[1].body) != `{"business_connection_id":"b1"}` {
		t.Errorf("requests = %+v", e.requests)
	}
}

func TestBusinessGiftsSendsOnlyPagingAndFiltersOutput(t *testing.T) {
	e := newMediaEnv(t, "business/b1")
	e.answer = func(*http.Request) (*http.Response, error) {
		return response(200, `{"ok":true,"result":{"total_count":2,"next_offset":"n2","gifts":[`+
			`{"type":"regular","owned_gift_id":"OWNED1","send_date":10,"is_saved":true,"text":"hi","sender_user":{"id":3,"first_name":"Bo","last_name":"X"},`+
			`"can_be_upgraded":true,"convert_star_count":9,"prepaid_upgrade_star_count":4,"gift":{"id":"G1","star_count":50,"upgrade_star_count":99}},`+
			`{"type":"unique","owned_gift_id":"OWNED2","send_date":11,"transfer_star_count":25,"can_be_transferred":true,`+
			`"gift":{"gift_id":"G2","base_name":"Cat","number":8,"name":"CatSLUG"}}]}}`), nil
	}
	out, err := e.businessTool("gifts", map[string]any{"business": "b1", "limit": 2, "offset": "n1"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(out)
	want := `{"total_count":2,"next_offset":"n2","gifts":[{"type":"regular","gift_id":"G1","send_date":10,"is_saved":true,` +
		`"text":"hi","sender":{"id":3,"is_bot":false,"first_name":"Bo","last_name":"X"},"star_count":50,"convert_star_count":9},` +
		`{"type":"unique","gift_id":"G2","name":"Cat","number":8,"send_date":11,"is_saved":false}]}`
	if string(got) != want {
		t.Errorf("out = %s\nwant %s", got, want)
	}
	if strings.Contains(string(got), "OWNED") {
		t.Error("owned gift id leaked")
	}
	if len(e.requests) != 1 || e.requests[0].path != "POST /bot"+testToken+"/getBusinessAccountGifts" ||
		string(e.requests[0].body) != `{"business_connection_id":"b1","offset":"n1","limit":2}` {
		t.Errorf("requests = %+v", e.requests)
	}
}

func TestBusinessAssetMutationsSendOneRequestAndReportUncertainty(t *testing.T) {
	ops := map[string]func(*Client) error{
		"setphoto": func(c *Client) error {
			_, err := c.SetBusinessPhoto(context.Background(), "b1", mediaItem{name: "file.jpg", data: []byte("x")}, nil)
			return err
		},
		"removephoto": func(c *Client) error { _, err := c.RemoveBusinessPhoto(context.Background(), "b1", nil); return err },
	}
	for name, op := range ops {
		for _, status := range []int{500, 502} {
			client, bodies, _ := capture(t, "business/b1", status, `{"ok":false,"description":"secret text"}`)
			err := op(client)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "secret text") {
				t.Errorf("%s status %d err = %v", name, status, err)
			}
			if len(*bodies) != 1 {
				t.Errorf("%s requests = %d, want 1", name, len(*bodies))
			}
		}
		calls := 0
		client, _ := telegramClient(t, "business/b1", roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, &timeoutError{}
		}))
		if err := op(client); err == nil || !strings.Contains(err.Error(), "may have taken effect") || calls != 1 {
			t.Errorf("%s timeout err = %v calls = %d", name, err, calls)
		}
	}
}

func TestBusinessAssetDescriptors(t *testing.T) {
	if businessSetPhoto.LocalFiles == "" || businessSetPhoto.RequiresToolAllowList || !businessRemovePhoto.RequiresToolAllowList ||
		businessGifts.RequiresToolAllowList || businessStarBalance.RequiresToolAllowList ||
		businessGifts.Risk.Effect != capability.EffectRead || businessStarBalance.Risk.Effect != capability.EffectRead {
		t.Error("business asset tool flags wrong")
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if strings.HasPrefix(tool, "telegram.business.") && tool != "telegram.business.get" {
				t.Logf("profile %s contains %s", profile.ID, tool)
				if tool == businessSetPhoto.ID || tool == businessRemovePhoto.ID || tool == businessGifts.ID || tool == businessStarBalance.ID {
					t.Errorf("profile %s contains %s", profile.ID, tool)
				}
			}
		}
	}
}
