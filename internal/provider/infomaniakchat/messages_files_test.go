package infomaniakchat

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const fileOwnFree = "file00000000000000000f1f"

// sendFilesServer answers the checks of a send with files: the file infos are keyed by file ID, posts record
// the body of POST /api/v4/posts.
func sendFilesServer(t *testing.T, infos map[string]string, posted *[]string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == "/api/v4/channels/"+chanA:
			return jsonResponse(200, channelJSONOf(chanA, teamA, "Channel A")), nil
		case r.URL.Path == "/api/v4/users/me":
			return jsonResponse(200, `{"id":"`+selfID+`"}`), nil
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v4/files/"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v4/files/"), "/info")
			if info, ok := infos[id]; ok {
				return jsonResponse(200, info), nil
			}
		case r.Method == http.MethodPost && r.URL.Path == "/api/v4/posts":
			body, _ := readAll(r)
			*posted = append(*posted, body)
			return jsonResponse(201, postJSONOf("sent1", chanA, "", selfID, "", 1735689600000)), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func readAll(r *http.Request) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			return sb.String(), nil
		}
	}
}

func fileInfoFor(id, user, post, channel string) string {
	return `{"id":"` + id + `","user_id":"` + user + `","post_id":"` + post + `","channel_id":"` + channel +
		`","create_at":1,"delete_at":0,"name":"x","size":1}`
}

func TestMessagesSendWithFilesAttachesOwnUnattachedUploads(t *testing.T) {
	var calls []call
	var posted []string
	env := newEnvironment(t, &calls, sendFilesServer(t, map[string]string{
		fileOwnFree: fileInfoFor(fileOwnFree, selfID, "", chanA),
		fileInChanA: fileInfoFor(fileInChanA, selfID, "", ""),
	}, &posted))
	result, err := env.confirmed(messagesSend.ID, "channel",
		`{"channel_id":"`+chanA+`","file_ids":["`+fileOwnFree+`","`+fileInChanA+`"]}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var sent SentMessage
	if err := json.Unmarshal([]byte(result), &sent); err != nil || sent.ID != "sent1" {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(posted) != 1 || !strings.Contains(posted[0], `"file_ids":["`+fileOwnFree+`","`+fileInChanA+`"]`) {
		t.Fatalf("posted = %v", posted)
	}
}

func TestMessagesSendRefusesForeignAttachedAndForeignChannelFilesBeforePost(t *testing.T) {
	infos := map[string]string{
		fileOwnFree: fileInfoFor(fileOwnFree, selfID, "", chanA),
		fileInChanB: fileInfoFor(fileInChanB, otherID, "", chanA),
		fileInChanA: fileInfoFor(fileInChanA, selfID, postInChanA, chanA),
		fileInChanC: fileInfoFor(fileInChanC, selfID, "", chanC),
	}
	for name, id := range map[string]string{"foreign uploader": fileInChanB, "already attached": fileInChanA,
		"other channel": fileInChanC, "unknown": fileNoPost} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			var posted []string
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/api/v4/files/"+fileNoPost+"/info" {
					return jsonResponse(404, `{"message":"`+messageCanary+`"}`), nil
				}
				return sendFilesServer(t, infos, &posted)(r)
			})
			_, err := env.confirmed(messagesSend.ID, "channel",
				`{"channel_id":"`+chanA+`","text":"hi","file_ids":["`+fileOwnFree+`","`+id+`"]}`)
			if err == nil || (name != "unknown" && !isInvalidRequest(err)) {
				t.Fatalf("err = %v, want a refusal", err)
			}
			if len(posted) != 0 {
				t.Fatalf("posted = %v, want no post", posted)
			}
			if strings.Contains(err.Error(), id) || strings.Contains(err.Error(), messageCanary) {
				t.Fatalf("error names the file or leaks content: %v", err)
			}
		})
	}
}

func TestMessagesSendFileIDsAndTextShape(t *testing.T) {
	cases := map[string]string{
		"no text and no files":     `{"channel_id":"` + chanA + `"}`,
		"too many":                 `{"channel_id":"` + chanA + `","file_ids":["a","b","c","d","e","f","g","h","i","j","k"]}`,
		"duplicate":                `{"channel_id":"` + chanA + `","file_ids":["` + fileOwnFree + `","` + fileOwnFree + `"]}`,
		"malformed":                `{"channel_id":"` + chanA + `","file_ids":["../x"]}`,
		"empty list":               `{"channel_id":"` + chanA + `","file_ids":[]}`,
		"empty text without files": `{"channel_id":"` + chanA + `","text":""}`,
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request %s", r.URL.Path)
				return nil, nil
			})
			if _, err := env.confirmed(messagesSend.ID, "channel", args); err == nil {
				t.Fatal("err = nil, want a refusal")
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d", calls, *env.reads)
			}
		})
	}
}

func TestMessagesSendWithoutFilesMakesNoFileRequests(t *testing.T) {
	var calls []call
	var posted []string
	env := newEnvironment(t, &calls, sendFilesServer(t, nil, &posted))
	if _, err := env.confirmed(messagesSend.ID, "channel", `{"channel_id":"`+chanA+`","text":"hi"}`); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || strings.Contains(posted[0], "file_ids") {
		t.Fatalf("calls = %+v, posted = %v", calls, posted)
	}
}
