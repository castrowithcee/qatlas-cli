package infomaniakmail

import (
	"errors"
	"net/textproto"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func originalOf(t *testing.T, err error) *provider.Original {
	t.Helper()
	var pe *provider.Error
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want a provider error", err)
	}
	return pe.Original
}

func wantEntry(t *testing.T, err error, want provider.OriginalEntry) {
	t.Helper()
	o := originalOf(t, err)
	if o == nil || o.Status != 0 || len(o.Entries) != 1 || o.Entries[0] != want {
		t.Fatalf("original = %+v, want one entry %+v with status 0", o, want)
	}
	if strings.Contains(err.Error(), "SERVER-TEXT-CANARY") {
		t.Errorf("Error() = %q carries the server text", err.Error())
	}
}

func TestIMAPFailureKeepsTheServerAnswer(t *testing.T) {
	no := &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeNonExistent, Text: "SERVER-TEXT-CANARY no such folder"}
	err := failure("op", no)
	if classOf(err) != provider.ClassNotFound {
		t.Fatalf("class = %q", classOf(err))
	}
	wantEntry(t, err, provider.OriginalEntry{Type: "NO", Code: "NONEXISTENT", Message: no.Text})

	// A new message with a suffix keeps the original.
	wantEntry(t, withSuffix("op", no, " suffix"), provider.OriginalEntry{Type: "NO", Code: "NONEXISTENT", Message: no.Text})
	wantEntry(t, changeFailure("op", no, " suffix"), provider.OriginalEntry{Type: "NO", Code: "NONEXISTENT", Message: no.Text})

	bye := &imap.Error{Type: imap.StatusResponseTypeBye, Text: "SERVER-TEXT-CANARY bye"}
	wantEntry(t, changeFailure("op", bye, " suffix"), provider.OriginalEntry{Type: "BYE", Message: bye.Text})
}

func TestIMAPLoginFailureKeepsTheServerAnswer(t *testing.T) {
	bad := &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeAuthenticationFailed, Text: "SERVER-TEXT-CANARY bad login"}
	err := loginFailure("op", bad)
	if classOf(err) != provider.ClassAuth {
		t.Fatalf("class = %q", classOf(err))
	}
	wantEntry(t, err, provider.OriginalEntry{Type: "NO", Code: "AUTHENTICATIONFAILED", Message: bad.Text})
	bye := &imap.Error{Type: imap.StatusResponseTypeBye, Text: "SERVER-TEXT-CANARY bye"}
	wantEntry(t, loginFailure("op", bye), provider.OriginalEntry{Type: "BYE", Message: bye.Text})
}

func TestTransportFailureHasNoOriginal(t *testing.T) {
	if o := originalOf(t, failure("op", errors.New("boom"))); o != nil {
		t.Errorf("original = %+v, want nil", o)
	}
	if o := originalOf(t, smtpFailure("op", errors.New("boom"), "x", "")); o != nil {
		t.Errorf("original = %+v, want nil", o)
	}
}

func TestSMTPFailureKeepsTheServerReply(t *testing.T) {
	reply := &textproto.Error{Code: 535, Msg: "5.7.8 SERVER-TEXT-CANARY authentication failed"}
	err := smtpFailure("op", reply, "the login", " suffix")
	if classOf(err) != provider.ClassAuth {
		t.Fatalf("class = %q", classOf(err))
	}
	wantEntry(t, err, provider.OriginalEntry{Code: "535", Message: reply.Msg})
}

func TestSMTPRefusalsKeepTheServerReply(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config func(*smtpServer)
		want   provider.OriginalEntry
	}{
		{"recipient", func(s *smtpServer) { s.rejectRcpt = "bad@example.net" },
			provider.OriginalEntry{Code: "550", Message: "5.1.1 SERVER-TEXT-CANARY no such user"}},
		{"end of data", func(s *smtpServer) { s.dataReply = "554 5.7.1 SERVER-TEXT-CANARY spam" },
			provider.OriginalEntry{Code: "554", Message: "5.7.1 SERVER-TEXT-CANARY spam"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := sendEnvironment(t, t.TempDir())
			newSMTPServer(t, true, tc.config)
			_, err := e.confirmed("infomaniak.mail.messages.send", "send", `{"to":["a@example.net","bad@example.net"]}`)
			wantEntry(t, err, tc.want)
		})
	}
}
