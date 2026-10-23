package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	documentedProfilePhotos = `{"total_count":3,"photos":[[` +
		`{"file_id":"p1","file_unique_id":"u1","width":10,"height":20,"file_size":30},` +
		`{"file_id":"p2","file_unique_id":"u2","width":40,"height":50,"file_size":60}]]}`
	documentedProfileAudios = `{"total_count":1,"audios":[{"file_id":"a1","file_unique_id":"ua","duration":4,` +
		`"performer":"Pf","title":"Tt","file_name":"f.mp3","mime_type":"audio/mpeg","file_size":99,` +
		`"thumbnail":{"file_id":"t1","file_unique_id":"ut","width":5,"height":6,"file_size":7}}]}`
	documentedBoosts = `{"boosts":[` +
		`{"boost_id":"b1","add_date":1,"expiration_date":2,"source":{"source":"premium","user":USER}},` +
		`{"boost_id":"b2","add_date":3,"expiration_date":4,"source":{"source":"gift_code","user":USER}},` +
		`{"boost_id":"b3","add_date":5,"expiration_date":6,"source":{"source":"giveaway","giveaway_message_id":9,` +
		`"user":USER,"prize_star_count":50,"is_unclaimed":true}}]}`
)

func TestProfileDataIsPrintedInFull(t *testing.T) {
	ref := func(id string) string { return signRef(testToken, refFile, "-1001", id) }
	cases := []struct {
		tool, member string
		want         string
	}{
		{"telegram.users.profile-photos", documentedProfilePhotos, fmt.Sprintf(
			`{"total_count":3,"photos":[{"file_ref":%q,"width":40,"height":50,"file_size":60,"sizes":[`+
				`{"file_ref":%q,"file_unique_id":"u1","width":10,"height":20,"file_size":30},`+
				`{"file_ref":%q,"file_unique_id":"u2","width":40,"height":50,"file_size":60}]}]}`,
			ref("p2"), ref("p1"), ref("p2"))},
		{"telegram.users.profile-audios", documentedProfileAudios, fmt.Sprintf(
			`{"total_count":1,"audios":[{"file_ref":%q,"file_unique_id":"ua","duration":4,"title":"Tt","performer":"Pf",`+
				`"file_name":"f.mp3","mime_type":"audio/mpeg","file_size":99,"thumbnail":{"file_ref":%q,`+
				`"file_unique_id":"ut","width":5,"height":6,"file_size":7}}]}`, ref("a1"), ref("t1"))},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			_, got := invokeOutputCase(t, outputCase{c.tool, []string{"-1001"}, `{"user_id":7}`,
				map[string]string{"getChatMember": memberAnswer,
					map[string]string{"telegram.users.profile-photos": "getUserProfilePhotos",
						"telegram.users.profile-audios": "getUserProfileAudios"}[c.tool]: c.member}, nil})
			var want map[string]any
			if err := json.Unmarshal([]byte(c.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("output = %v\nwant %v", got, want)
			}
		})
	}
	t.Run("telegram.users.chat-boosts", func(t *testing.T) {
		_, got := invokeOutputCase(t, outputCase{"telegram.users.chat-boosts", []string{"-1001"}, `{"user_id":7}`,
			map[string]string{"getChatMember": memberAnswer, "getUserChatBoosts": documentedBoosts}, nil})
		var user map[string]any
		_ = json.Unmarshal([]byte(documentedUser), &user)
		boosts, _ := got["boosts"].([]any)
		if len(boosts) != 3 {
			t.Fatalf("boosts = %v", got)
		}
		wantInfo := []map[string]any{
			{"source": "premium", "user": user},
			{"source": "gift_code", "user": user},
			{"source": "giveaway", "user": user, "giveaway_message_id": 9.0, "prize_star_count": 50.0, "is_unclaimed": true},
		}
		for i, b := range boosts {
			boost := b.(map[string]any)
			if boost["source"] != wantInfo[i]["source"] || !reflect.DeepEqual(boost["source_info"], wantInfo[i]) {
				t.Errorf("boost %d = %v", i, boost)
			}
		}
	})
}

// profileTransport answers getChatMember with a member and every other method with result, and records requests.
func profileTransport(calls *[]recorded, result string) roundTripFunc {
	return func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		*calls = append(*calls, recorded{name, string(data)})
		if name == "getChatMember" {
			return response(200, `{"ok":true,"result":`+memberAnswer+`}`), nil
		}
		return response(200, `{"ok":true,"result":`+result+`}`), nil
	}
}

// runTool invokes a registered tool against a fake Bot API and returns its output, error, and the requests.
func runTool(t *testing.T, tool, args, result string, tokenAccessed *bool) (map[string]any, error, []recorded) {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	_, handler, ok := reg.Lookup(tool)
	if !ok {
		t.Fatalf("%s is not registered", tool)
	}
	var calls []recorded
	previous := http.DefaultTransport
	http.DefaultTransport = profileTransport(&calls, result)
	t.Cleanup(func() { http.DefaultTransport = previous })
	red := &redact.Redactor{}
	secrets := secret.NewWith(func(string) string { *tokenAccessed = true; return testToken }, nil, nil, red)
	value, err := handler(context.Background(), resolvedWith("-1001"), secrets, red, json.RawMessage(args))
	if err != nil {
		return nil, err, calls
	}
	encoded, _ := json.Marshal(value)
	var out map[string]any
	_ = json.Unmarshal(encoded, &out)
	return out, nil, calls
}

func TestProfileLimitReachesTheRequestAndIsBounded(t *testing.T) {
	tests := []struct {
		tool, method, limit string
		sent                string
		ok                  bool
	}{
		{"telegram.users.profile-photos", "getUserProfilePhotos", `,"limit":100`, "100", true},
		{"telegram.users.profile-audios", "getUserProfileAudios", `,"limit":100`, "100", true},
		{"telegram.users.profile-photos", "getUserProfilePhotos", ``, "20", true},
		{"telegram.users.profile-audios", "getUserProfileAudios", ``, "20", true},
		{"telegram.users.profile-photos", "getUserProfilePhotos", `,"limit":101`, "", false},
		{"telegram.users.profile-audios", "getUserProfileAudios", `,"limit":101`, "", false},
		{"telegram.users.personal-chat-messages", "getUserPersonalChatMessages", `,"limit":21`, "", false},
		{"telegram.users.personal-chat-messages", "getUserPersonalChatMessages", ``, "20", true},
	}
	for _, tc := range tests {
		t.Run(tc.tool+tc.limit, func(t *testing.T) {
			accessed := false
			result := `{"total_count":0}`
			if strings.HasSuffix(tc.tool, "messages") {
				result = `[]`
			}
			_, err, calls := runTool(t, tc.tool, `{"user_id":7`+tc.limit+`}`, result, &accessed)
			if !tc.ok {
				if err == nil || accessed || len(calls) != 0 {
					t.Fatalf("err = %v, accessed = %v, calls = %+v", err, accessed, calls)
				}
				return
			}
			if err != nil || len(calls) != 2 || calls[0].method != "getChatMember" || calls[1].method != tc.method ||
				!strings.Contains(calls[1].body, `"limit":`+tc.sent) {
				t.Fatalf("err = %v, calls = %+v", err, calls)
			}
		})
	}
}

func TestChatBoostsPrintAtMostOneHundred(t *testing.T) {
	var boosts []string
	for i := 0; i < 150; i++ {
		boosts = append(boosts, fmt.Sprintf(`{"boost_id":"b%d","add_date":1,"expiration_date":2,"source":{"source":"premium"}}`, i))
	}
	accessed := false
	out, err, calls := runTool(t, "telegram.users.chat-boosts", `{"user_id":7}`,
		`{"boosts":[`+strings.Join(boosts, ",")+`]}`, &accessed)
	if err != nil || len(calls) != 2 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	if list, _ := out["boosts"].([]any); len(list) != 100 || out["truncated"] != true {
		t.Errorf("boosts = %d, truncated = %v", len(list), out["truncated"])
	}
	out, _, _ = runTool(t, "telegram.users.chat-boosts", `{"user_id":7}`, `{"boosts":[`+strings.Join(boosts[:100], ",")+`]}`, &accessed)
	if _, ok := out["truncated"]; ok {
		t.Error("100 boosts are not truncated")
	}
}

func TestProfileToolsNameTheirResponseLimit(t *testing.T) {
	if profileResponseMax != 512<<10 {
		t.Fatalf("profileResponseMax = %d", profileResponseMax)
	}
	for _, d := range []capability.Descriptor{usersProfilePhotos, usersProfileAudios, usersChatBoosts} {
		if !strings.Contains(d.Description, "512 KiB") {
			t.Errorf("%s does not name its response limit", d.ID)
		}
	}
}

// A page of 100 photos with the five sizes Telegram sends must fit the limit with room to spare.
func TestHundredProfilePhotosFitTheResponseLimit(t *testing.T) {
	var photos []string
	for i := 0; i < 100; i++ {
		var sizes []string
		for j := 0; j < 5; j++ {
			sizes = append(sizes, fmt.Sprintf(`{"file_id":"%s","file_unique_id":"%s","width":%d,"height":%d,"file_size":%d}`,
				strings.Repeat("F", 90)+fmt.Sprint(i, j), strings.Repeat("U", 20)+fmt.Sprint(i, j), 100*j, 100*j, 1000*j))
		}
		photos = append(photos, "["+strings.Join(sizes, ",")+"]")
	}
	result := `{"total_count":100,"photos":[` + strings.Join(photos, ",") + `]}`
	accessed := false
	out, err, _ := runTool(t, "telegram.users.profile-photos", `{"user_id":7,"limit":100}`, result, &accessed)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(out)
	t.Logf("input %d bytes, output %d bytes", len(result), len(encoded))
	if list, _ := out["photos"].([]any); len(list) != 100 || len(encoded) >= profileResponseMax {
		t.Errorf("photos = %d, output %d bytes", len(list), len(encoded))
	}
	if len(result) <= defaultResponseBytes {
		t.Errorf("the sample must exceed the former limit of %d bytes, is %d", defaultResponseBytes, len(result))
	}
}
