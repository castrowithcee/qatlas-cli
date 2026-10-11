package cli

import (
	"strings"
	"testing"
)

func TestMCPIDScanner(t *testing.T) {
	long := strings.Repeat("a", maxMCPIDBytes)
	tests := []struct {
		name, in, want string
	}{
		{"id at start", `{"id":7,"method":"x","params":{"a":[1,2]}}`, `7`},
		{"id at end", `{"params":{"a":{"b":"}"}},"id":"abc"}`, `"abc"`},
		{"nested only", `{"params":{"id":5},"x":[{"id":6}]}`, ``},
		{"escapes and braces", `{"a\"}{":"\"id\":9}","b":"{\\","id":"q\"}"}`, `"q\"}"`},
		{"string value id is no key", `{"a":"id","b":"id","id":3}`, `3`},
		{"value id before colon-less key", `{"x":"id"}`, ``},
		{"duplicate last wins", `{"id":1,"id":"two"}`, `"two"`},
		{"duplicate last invalid", `{"id":1,"id":null}`, ``},
		{"duplicate last nested", `{"id":1,"id":{"a":1}}`, ``},
		{"number forms", `{"id":-1.5e+3 }`, `-1.5e+3`},
		{"null", `{"id":null}`, ``},
		{"bool", `{"id":true}`, ``},
		{"array id", `{"id":[1]}`, ``},
		{"array message", `[{"id":1}]`, ``},
		{"id too long", `{"id":"` + long + `"}`, ``},
		{"id at limit", `{"id":"` + long[:maxMCPIDBytes-2] + `"}`, `"` + long[:maxMCPIDBytes-2] + `"`},
		{"invalid escape", `{"id":"\x"}`, ``},
		{"whitespace", " {\n\"id\" :\t 12 }", `12`},
		{"truncated value", `{"id":"abc`, ``},
		{"content after object", `{"id":1}{"id":2}`, `1`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			whole := &mcpIDScanner{}
			whole.feed([]byte(tc.in))
			if got := string(whole.ID()); got != tc.want {
				t.Fatalf("whole = %q, want %q", got, tc.want)
			}
			bytewise := &mcpIDScanner{}
			for i := 0; i < len(tc.in); i++ {
				bytewise.feed([]byte{tc.in[i]})
			}
			if got := string(bytewise.ID()); got != tc.want {
				t.Fatalf("bytewise = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMCPOversizedMessageAnswersWithRequestID(t *testing.T) {
	filler := strings.Repeat("x", maxRequestBytes)
	valid := `{"jsonrpc":"2.0","id":2,"method":"server/discover","params":{` + mcpTestMeta + `}}`
	input := `{"jsonrpc":"2.0","method":"tools/call","params":{"id":99,"blob":"` + filler + `"},"id":"req-1"}` + "\n" +
		`{"params":{"id":5,"blob":"` + filler + `"}}` + "\n" +
		`{"id":42,"blob":"` + filler + `"}` + "\n" +
		valid + "\n" +
		`{"id":"eof","blob":"` + filler
	responses, _ := runMCP(t, fakeRegistry(t), input)
	for _, id := range []string{`"req-1"`, "42", `"eof"`} {
		resp, ok := responses[id]
		if !ok || resp.Error == nil || resp.Error.Code != mcpInvalidRequest ||
			!strings.Contains(resp.Error.Message, "pass large file content as local_path") {
			t.Fatalf("response %s = %+v", id, resp)
		}
	}
	if resp := responses["null"]; resp.Error == nil || resp.Error.Code != mcpParseError {
		t.Fatalf("nested-id response = %+v", resp)
	}
	if len(responses["2"].Result) == 0 {
		t.Fatal("valid message after oversized input was not served")
	}
}
