package infomaniakchat

import (
	"net/http"
	"strings"
	"testing"
)

// A failed kChat answer keeps Qatlas' own message free of provider text and carries id and message
// separately in Original; detailed_error and request_id are not taken over.
func TestStatusErrorCarriesOriginalIdAndMessage(t *testing.T) {
	body := `{"id":"api.channel.get.app_error","message":"Channel canary missing","detailed_error":"secret detail","request_id":"req123","status_code":400}`
	c := &Client{}
	failure := c.statusError("channels.get", jsonResponse(http.StatusBadRequest, body))
	if strings.Contains(failure.Error(), "canary") {
		t.Fatalf("Error() = %q, want no provider text", failure.Error())
	}
	o := failure.Original
	if o == nil || o.Status != 400 || len(o.Entries) != 1 ||
		o.Entries[0].Code != "api.channel.get.app_error" || o.Entries[0].Message != "Channel canary missing" {
		t.Fatalf("Original = %+v", o)
	}
	if e := o.Entries[0]; e.Type != "" || e.Path != "" {
		t.Fatalf("entry = %+v, want only code and message", e)
	}
}

// A redirect answer comes from another host's storage at most: only its status is kept.
func TestStatusErrorRedirectKeepsOnlyStatus(t *testing.T) {
	c := &Client{}
	failure := c.statusError("files.download", jsonResponse(http.StatusFound, `{"message":"storage canary"}`))
	if strings.Contains(failure.Error(), "canary") {
		t.Fatalf("Error() = %q", failure.Error())
	}
	if o := failure.Original; o == nil || o.Status != 302 || len(o.Entries) != 0 {
		t.Fatalf("Original = %+v, want status only", o)
	}
}
