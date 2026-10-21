package telegram

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

func TestResolveTargetURL(t *testing.T) {
	tests := []struct {
		name, url, target, label string
	}{
		{"username", "https://t.me/alerts_channel", "@alerts_channel", "alerts_channel"},
		{"username with message", "https://t.me/alerts_channel/42", "@alerts_channel", "alerts_channel"},
		{"username trailing slash", "https://t.me/alerts_channel/", "@alerts_channel", "alerts_channel"},
		{"username with query and fragment", "https://t.me/alerts_channel/42?single#x", "@alerts_channel", "alerts_channel"},
		{"host in upper case", "HTTPS://T.ME/Alerts_Channel", "@Alerts_Channel", "Alerts_Channel"},
		{"private chat", "https://t.me/c/1234567890/55", "-1001234567890", "-1001234567890"},
		{"private chat only", "https://t.me/c/1234567890", "-1001234567890", "-1001234567890"},
		{"private chat topic", "https://t.me/c/1234567890/7/55", "-1001234567890", "-1001234567890"},
		{"private chat topic zero", "https://t.me/c/1234567890/0/55", "-1001234567890", "-1001234567890"},
		{"private chat topic leading zero", "https://t.me/c/1234567890/07/55", "-1001234567890", "-1001234567890"},
		{"private chat topic too large", "https://t.me/c/1234567890/99999999999999999999/55", "-1001234567890", "-1001234567890"},
		{"bot api origin is ignored", "https://t.me/news_room", "@news_room", "news_room"},

		{"invalid username characters", "https://t.me/ab-cdef", "", ""},
		{"username too short", "https://t.me/abcd", "", ""},
		{"username too long", "https://t.me/abcdefghijklmnopqrstuvwxyz0123456", "", ""},
		{"username starts with digit", "https://t.me/1alerts", "", ""},
		{"username with letters in message", "https://t.me/alerts_channel/abc", "", ""},
		{"username with deep path", "https://t.me/alerts_channel/1/2", "", ""},
		{"escaped separator", "https://t.me/alerts%2Fchannel", "", ""},
		{"reserved joinchat", "https://t.me/joinchat/AAAAAEx", "", ""},
		{"reserved addstickers", "https://t.me/addstickers/my_pack", "", ""},
		{"reserved share", "https://t.me/share/url?url=x", "", ""},
		{"reserved proxy", "https://t.me/proxy?server=x", "", ""},
		{"reserved preview", "https://t.me/s/alerts_channel", "", ""},
		{"reserved iv", "https://t.me/iv?url=x", "", ""},
		{"invite link", "https://t.me/+AbCdEfGh1234", "", ""},
		{"c without id", "https://t.me/c", "", ""},
		{"c with name", "https://t.me/c/alerts_channel", "", ""},
		{"c with leading zero", "https://t.me/c/0123", "", ""},
		{"c id too long", "https://t.me/c/12345678901234", "", ""},
		{"c with signed id", "https://t.me/c/-1001/5", "", ""},
		{"c with too many segments", "https://t.me/c/1/2/3/4", "", ""},
		{"c with non-numeric message", "https://t.me/c/1001/x", "", ""},
		{"root", "https://t.me/", "", ""},
		{"foreign host", "https://example.com/alerts_channel", "", ""},
		{"lookalike host", "https://t.me.example.com/alerts_channel", "", ""},
		{"telegram.me", "https://telegram.me/alerts_channel", "", ""},
		{"subdomain", "https://web.t.me/alerts_channel", "", ""},
		{"trailing dot host", "https://t.me./alerts_channel", "", ""},
		{"http", "http://t.me/alerts_channel", "", ""},
		{"tg scheme", "tg://resolve?domain=alerts_channel", "", ""},
		{"port", "https://t.me:443/alerts_channel", "", ""},
		{"userinfo", (&url.URL{Scheme: "https", User: url.UserPassword("user", "pw"), Host: "t.me", Path: "/alerts_channel"}).String(), "", ""},
		{"empty", "", "", ""},
		{"broken", "https://t.me/%zz", "", ""},
		{"no scheme", "t.me/alerts_channel", "", ""},
	}
	// Only a forum message link, with three numbers after c, also names its topic; c/{id}/{n} is the chat alone.
	topics := map[string]string{"private chat topic": "-1001234567890/topics/7"}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveTargetURL(context.Background(), nil, nil, nil, "https://api.telegram.org", tc.url)
			if err != nil {
				t.Fatalf("error = %v, want none", err)
			}
			if tc.target == "" {
				if len(got) != 0 {
					t.Fatalf("entries = %v, want none", got)
				}
				return
			}
			want := []config.TargetEntry{{Target: tc.target, Name: tc.label, Kind: "chat"}}
			if topic := topics[tc.name]; topic != "" {
				want = append(want, config.TargetEntry{Target: topic, Name: topic, Kind: "topic", Path: tc.target})
			}
			if !slices.Equal(got, want) {
				t.Fatalf("entries = %+v, want %+v", got, want)
			}
			for _, entry := range got {
				if kind, _, err := parseTarget(entry.Target); err != nil || kind != entry.Kind {
					t.Errorf("resolved target %q is %q, %v", entry.Target, kind, err)
				}
			}
		})
	}
}

func TestResolvedTargetsAreValidForTheKind(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, target := range []string{"@alerts_channel", "-1001234567890"} {
		if !metadata.Target.ValidTarget("chat", target) {
			t.Errorf("%q is no valid chat target", target)
		}
	}
	if !metadata.Target.ValidTarget("topic", "-1001234567890/topics/7") {
		t.Error("the topic of a forum link is no valid topic target")
	}
}

func TestTelegramOffersNoList(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	if !reg.HasTargetBrowser(Provider) {
		t.Fatal("no target browser registered")
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, kind := range metadata.Target.Kinds {
		if !kind.Unlisted {
			t.Errorf("kind %s is listed, want unlisted", kind.Name)
		}
	}
	_, err := reg.ListTargets(context.Background(), resolvedWith("-1001"), nil, nil, config.TargetListRequest{Kind: "chat"})
	if !errors.Is(err, config.ErrTargetBrowseUnsupported) {
		t.Errorf("ListTargets error = %v, want unsupported", err)
	}
}
