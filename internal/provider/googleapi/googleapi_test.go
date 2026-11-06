package googleapi

import "testing"

func TestStatusOf(t *testing.T) {
	cases := []struct {
		name, body, status, reason string
	}{
		{"status and reason", `{"error":{"status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.rpc.Help"},{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID"}]}}`, "PERMISSION_DENIED", "API_KEY_INVALID"},
		{"no ErrorInfo", `{"error":{"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.rpc.Help","reason":"x"}]}}`, "NOT_FOUND", ""},
		{"foreign body", `{"status":"OK"}`, "", ""},
		{"not json", `<html>`, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, reason := StatusOf([]byte(c.body))
			if status != c.status || reason != c.reason {
				t.Fatalf("StatusOf = %q, %q; want %q, %q", status, reason, c.status, c.reason)
			}
		})
	}
}
