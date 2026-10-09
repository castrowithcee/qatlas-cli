package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func idRange(n int) []int64 {
	ids := make([]int64, n)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	return ids
}

func TestDeleteManyBodyDeduplicatesInOrder(t *testing.T) {
	client, bodies, paths := capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	got, err := client.DeleteMessages(context.Background(), []int64{9, 3, 9, 5, 3})
	if err != nil || got["deleted"] != true || len(got) != 1 {
		t.Fatalf("result = %v, %v", got, err)
	}
	if (*bodies)[0] != `{"chat_id":"-1001","message_ids":[9,3,5]}` || (*paths)[0] != "deleteMessages" {
		t.Errorf("request = %s %s", (*paths)[0], (*bodies)[0])
	}
}

func TestDeleteManyIDBounds(t *testing.T) {
	over := append(idRange(100), 1, 2, 3)
	cases := []struct {
		name string
		ids  []int64
		ok   bool
	}{
		{"empty", nil, false},
		{"one", []int64{1}, true},
		{"hundred", idRange(100), true},
		{"hundred and one", idRange(101), false},
		{"zero", []int64{1, 0}, false},
		{"negative", []int64{-4}, false},
		{"duplicates reduce to hundred", over, true},
	}
	for _, c := range cases {
		client, bodies, _ := capture(t, "-1001", 200, `{"ok":true,"result":true}`)
		_, err := client.DeleteMessages(context.Background(), c.ids)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v", c.name, err)
		}
		if want := map[bool]int{true: 1, false: 0}[c.ok]; len(*bodies) != want {
			t.Errorf("%s: requests = %d, want %d", c.name, len(*bodies), want)
		}
	}
}

func TestDeleteManyHandlerRejectsBeforeSecret(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	long := make([]string, 101)
	for i := range long {
		long[i] = fmt.Sprint(i + 1)
	}
	for _, args := range []string{
		`{}`, `{"message_ids":[]}`, `{"message_ids":[0]}`, `{"message_ids":[-1]}`, `{"message_ids":[1],"extra":1}`,
		`{"message_ids":[` + strings.Join(long, ",") + `]}`,
		`{"chat":"-2002","message_ids":[1]}`,
	} {
		_, err := invokeMessagesDeleteMany(context.Background(), resolvedWith("-1001"), resolver, nil, json.RawMessage(args))
		if err == nil || strings.Contains(err.Error(), "-2002") {
			t.Errorf("%s: err = %v", args, err)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestDeleteManySingleRequestAndUncertaintyAfterFailure(t *testing.T) {
	for _, status := range []int{500, 502} {
		client, bodies, _ := capture(t, "-1001", status, `{"ok":false,"description":"secret text"}`)
		_, err := client.DeleteMessages(context.Background(), []int64{1, 2})
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "secret text") {
			t.Errorf("status %d err = %v", status, err)
		}
		if len(*bodies) != 1 {
			t.Errorf("requests = %d, want 1", len(*bodies))
		}
	}
	calls := 0
	client, _ := telegramClient(t, "-1001", roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, &timeoutError{}
	}))
	if _, err := client.DeleteMessages(context.Background(), []int64{1}); err == nil ||
		!strings.Contains(err.Error(), "may have taken effect") || calls != 1 {
		t.Errorf("timeout err = %v calls = %d", err, calls)
	}
	client, _, _ = capture(t, "-1001", 200, `{"ok":true,"result":{"x":1}}`)
	if _, err := client.DeleteMessages(context.Background(), []int64{1}); err == nil ||
		!strings.Contains(err.Error(), "may have taken effect") {
		t.Errorf("non-true result err = %v", err)
	}
}

func TestDeleteManyIsInNoProfile(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == messagesDeleteMany.ID {
				t.Errorf("profile %s contains deletemany", profile.ID)
			}
		}
	}
	if !messagesDeleteMany.RequiresToolAllowList {
		t.Error("deletemany must require a tools list")
	}
}
