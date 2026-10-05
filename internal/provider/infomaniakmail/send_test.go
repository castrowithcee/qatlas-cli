package infomaniakmail

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/mail"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/emersion/go-imap/v2"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// smtpServer is a small in-test submission server on a loopback port. It logs every command, whether TLS was
// on, every authentication, and every message it received.
type smtpServer struct {
	// behaviour switches
	noStartTLS bool   // does not offer STARTTLS and advertises AUTH in plain text
	rejectRcpt string // refuses this recipient with 550
	dataReply  string // reply to the end of DATA, default "250 queued"
	dropAfter  bool   // closes the connection after the end of DATA without a reply
	dropDuring bool   // closes the connection in the middle of DATA
	onlyLogin  bool   // offers AUTH LOGIN only
	password   string // the password the server accepts; default the mailbox password

	tlsConfig *tls.Config
	client    *tls.Config
	addr      string

	mu                 sync.Mutex
	commands           []string
	plainAuth, tlsAuth []string
	messages           [][]byte
	dials              atomic.Int32
	datas              atomic.Int32
	tlsSeen            atomic.Bool
}

func testCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: smtpHost}, DNSNames: []string{smtpHost},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

// newSMTPServer starts the server and points the package's SMTP seam at it. trusted decides whether the
// client trusts its certificate; an untrusted one must fail the handshake.
func newSMTPServer(t *testing.T, trusted bool, configure func(*smtpServer)) *smtpServer {
	t.Helper()
	cert, pool := testCertificate(t)
	s := &smtpServer{tlsConfig: &tls.Config{Certificates: []tls.Certificate{cert}}}
	s.password = passwordVal
	if configure != nil {
		configure(s)
	}
	s.client = tlsConfig()
	if trusted {
		s.client.RootCAs = pool
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.addr = listener.Addr().String()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	previous := dialSMTP
	dialSMTP = func(ctx context.Context) (net.Conn, *tls.Config, error) {
		s.dials.Add(1)
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.addr)
		return conn, s.client, err
	}
	t.Cleanup(func() { dialSMTP = previous })
	return s
}

func (s *smtpServer) log(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, line)
}

func (s *smtpServer) count(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.commands {
		if strings.HasPrefix(strings.ToUpper(c), prefix) {
			n++
		}
	}
	return n
}

func (s *smtpServer) wire() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.commands, "\n")
}

func (s *smtpServer) delivered() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.messages...)
}

func (s *smtpServer) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	var out net.Conn = conn
	reader := bufio.NewReader(conn)
	say := func(line string) { _, _ = out.Write([]byte(line + "\r\n")) }
	secure := false
	say("220 mail.infomaniak.com ESMTP ready")
	for {
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		raw, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line := strings.TrimRight(raw, "\r\n")
		verb, rest, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			s.log(line)
			say("250-mail.infomaniak.com")
			if !secure && !s.noStartTLS {
				say("250-STARTTLS")
			}
			mechanisms := "PLAIN LOGIN"
			if s.onlyLogin {
				mechanisms = "LOGIN"
			}
			if secure || s.noStartTLS {
				say("250-AUTH " + mechanisms)
			}
			say("250 8BITMIME")
		case "STARTTLS":
			s.log(line)
			say("220 go ahead")
			secured := tls.Server(conn, s.tlsConfig)
			if err := secured.Handshake(); err != nil {
				return
			}
			s.tlsSeen.Store(true)
			out, reader, secure = secured, bufio.NewReader(secured), true
		case "AUTH":
			mechanism, initial, _ := strings.Cut(rest, " ")
			s.log("AUTH " + mechanism)
			var user, password string
			if strings.EqualFold(mechanism, "PLAIN") {
				decoded, _ := base64.StdEncoding.DecodeString(initial)
				parts := strings.Split(string(decoded), "\x00")
				if len(parts) == 3 {
					user, password = parts[1], parts[2]
				}
			} else {
				say("334 VXNlcm5hbWU6")
				u, _ := reader.ReadString('\n')
				say("334 UGFzc3dvcmQ6")
				p, _ := reader.ReadString('\n')
				du, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(u))
				dp, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(p))
				user, password = string(du), string(dp)
			}
			s.mu.Lock()
			if secure {
				s.tlsAuth = append(s.tlsAuth, user+"\x00"+password)
			} else {
				s.plainAuth = append(s.plainAuth, user+"\x00"+password)
			}
			s.mu.Unlock()
			if user == mailbox && password == s.password {
				say("235 2.7.0 authenticated")
			} else {
				say("535 5.7.8 SERVER-TEXT-CANARY authentication failed")
			}
		case "MAIL":
			s.log(line)
			say("250 ok")
		case "RCPT":
			s.log(line)
			if s.rejectRcpt != "" && strings.Contains(rest, "<"+s.rejectRcpt+">") {
				say("550 5.1.1 SERVER-TEXT-CANARY no such user")
				continue
			}
			say("250 ok")
		case "DATA":
			s.log(line)
			s.datas.Add(1)
			say("354 end with <CRLF>.<CRLF>")
			var message bytes.Buffer
			for {
				l, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				l = strings.TrimPrefix(l, ".")
				message.WriteString(l)
				if s.dropDuring && message.Len() > 200 {
					return
				}
			}
			s.mu.Lock()
			s.messages = append(s.messages, message.Bytes())
			s.mu.Unlock()
			if s.dropAfter {
				return
			}
			reply := s.dataReply
			if reply == "" {
				reply = "250 2.0.0 queued"
			}
			say(reply)
		case "QUIT":
			s.log(line)
			say("221 bye")
			return
		default:
			s.log(line)
			say("502 not implemented")
		}
	}
}

// sendEnvironment has INBOX, Drafts (\Drafts), and Sent (\Sent), and connections that offer the sending tools.
func sendEnvironment(t *testing.T, dir string) *environment {
	t.Helper()
	e := newEnvironmentWith(t, func(cfg *config.Config) {
		sending := func(targets ...string) config.Connection {
			c := cfg.Connections["open"]
			c.Targets = append([]string{"mailbox/" + mailbox}, targets...)
			c.Tools = []string{messagesSend.ID, draftsSend.ID, draftsCreate.ID}
			c.Files = config.Files{Read: []string{dir}}
			return c
		}
		cfg.Connections["send"] = sending()
		cfg.Connections["sendnosent"] = sending("folder/INBOX", "folder/Drafts")
		cfg.Connections["sendsenders"] = sending("sender/alice@example.net")
		notools := sending()
		notools.Tools = nil
		cfg.Connections["sendnotools"] = notools
		nofiles := sending()
		nofiles.Files = config.Files{}
		cfg.Connections["sendnofiles"] = nofiles
	})
	for _, name := range []string{"INBOX", "Drafts", "Sent"} {
		e.f.folder(t, name)
	}
	markSpecialUse(t, e.f, "Drafts", imap.MailboxAttrDrafts)
	markSpecialUse(t, e.f, "Sent", imap.MailboxAttrSent)
	return e
}

// stored returns the raw messages of a folder of the in-memory server.
func stored(t *testing.T, f *fixture, name string) [][]byte {
	t.Helper()
	box := reflect.ValueOf(f.user).Elem().FieldByName("mailboxes").MapIndex(reflect.ValueOf(name))
	if !box.IsValid() {
		t.Fatalf("no folder %s", name)
	}
	messages := box.Elem().FieldByName("l")
	var out [][]byte
	for i := 0; i < messages.Len(); i++ {
		buf := messages.Index(i).Elem().FieldByName("buf")
		out = append(out, append([]byte(nil), reflect.NewAt(buf.Type(), unsafe.Pointer(buf.UnsafeAddr())).Elem().Bytes()...))
	}
	return out
}

func sendMessage(t *testing.T, e *environment, connection, arguments string) SendResult {
	t.Helper()
	result, err := e.confirmed("infomaniakmail.messages.send", connection, arguments)
	if err != nil {
		t.Fatalf("messages.send %s: %v", arguments, err)
	}
	var got SendResult
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("decode %q: %v", result, err)
	}
	return got
}

func headerOf(t *testing.T, raw []byte) mail.Header {
	t.Helper()
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	return message.Header
}

// hasBccLine reports whether any line of raw starts with a Bcc field name. It
// checks lines instead of the whole text, because random MIME boundaries may
// contain "bcc".
func hasBccLine(raw []byte) bool {
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			return true
		}
	}
	return false
}

func TestHasBccLineIgnoresBoundariesAndFindsTheField(t *testing.T) {
	boundary := rawDraft(mailbox, "", `multipart/mixed; boundary="bcc74c1b"`,
		"--bcc74c1b\r\nContent-Type: text/plain\r\n\r\nx\r\n--bcc74c1b--\r\n")
	if hasBccLine([]byte(boundary)) {
		t.Errorf("a boundary with bcc counts as a Bcc field")
	}
	for _, field := range []string{"Bcc: a@example.net\r\n", "BCC:a@example.net\r\n"} {
		if !hasBccLine([]byte(rawDraft(mailbox, field, "text/plain", "x\r\n"))) {
			t.Errorf("missed %q", field)
		}
	}
}

func TestMessagesSendSubmitsOnceOverStartTLSAndStoresTheCopy(t *testing.T) {
	e := sendEnvironment(t, t.TempDir())
	server := newSMTPServer(t, true, nil)
	note := []byte("inline attachment content")
	args := fmt.Sprintf(`{"to":["a@example.net","B@example.net"],"cc":["c@example.net"],"bcc":["secret-bcc@example.net","a@example.net"],`+
		`"subject":"Grüße","body":"Hallo\nBODY-CANARY","attachments":[{"name":"note.txt","content_base64":%q}]}`,
		base64.StdEncoding.EncodeToString(note))
	got := sendMessage(t, e, "send", args)

	if got.Recipients != 4 || got.MessageID == "" || !got.CopyStored || got.SentFolder != "Sent" || got.SentUID == 0 ||
		got.SentUIDValidity == 0 || len(got.Attachments) != 1 || got.Attachments[0].Name != "note.txt" ||
		got.Attachments[0].Size != int64(len(note)) || got.Note != "" || got.DraftKept {
		t.Fatalf("result = %+v", got)
	}
	raw, _ := json.Marshal(got)
	for _, leaked := range []string{"BODY-CANARY", "secret-bcc", passwordVal} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("result carries %q: %s", leaked, raw)
		}
	}
	if server.dials.Load() != 1 || server.datas.Load() != 1 || !server.tlsSeen.Load() {
		t.Errorf("dials = %d, DATA = %d, TLS = %t, want one connection, one DATA, TLS", server.dials.Load(), server.datas.Load(), server.tlsSeen.Load())
	}
	// STARTTLS comes first; the credential travels only inside TLS, once, with the mailbox and its password.
	wire := server.wire()
	if strings.Index(wire, "STARTTLS") > strings.Index(wire, "AUTH") || strings.Index(wire, "AUTH") > strings.Index(wire, "MAIL FROM") ||
		len(server.plainAuth) != 0 || len(server.tlsAuth) != 1 || server.tlsAuth[0] != mailbox+"\x00"+passwordVal {
		t.Errorf("wire:\n%s\nplain %q tls %q", wire, server.plainAuth, server.tlsAuth)
	}
	if !strings.Contains(wire, "MAIL FROM:<"+mailbox+">") || server.count("RCPT") != 4 {
		t.Errorf("envelope:\n%s", wire)
	}
	for _, want := range []string{"<a@example.net>", "<B@example.net>", "<c@example.net>", "<secret-bcc@example.net>"} {
		if !strings.Contains(wire, "RCPT TO:"+want) {
			t.Errorf("no RCPT for %s:\n%s", want, wire)
		}
	}

	messages := server.delivered()
	if len(messages) != 1 {
		t.Fatalf("delivered %d messages", len(messages))
	}
	header := headerOf(t, messages[0])
	if header.Get("From") != mailbox || header.Get("Message-ID") != got.MessageID || header.Get("Bcc") != "" ||
		hasBccLine(messages[0]) || strings.Contains(string(messages[0]), "secret-bcc") {
		t.Errorf("sent header = %v\n%s", header, messages[0])
	}
	// The copy is the sent message, flagged \Seen, and holds no Bcc either.
	copies := stored(t, e.f, "Sent")
	if len(copies) != 1 || !bytes.Equal(copies[0], messages[0]) {
		t.Fatalf("copy differs from the sent message:\n%q\n%q", copies, messages)
	}
	if flags := flagsOf(t, e, "Sent", got.SentUID); len(flags) != 1 || !hasFlag(flags, imap.FlagSeen) {
		t.Errorf("copy flags = %v, want only \\Seen", flags)
	}
	if len(stored(t, e.f, "Drafts")) != 0 || len(stored(t, e.f, "INBOX")) != 0 {
		t.Errorf("another folder changed")
	}
}

func TestMessagesSendReplyHeaders(t *testing.T) {
	e := sendEnvironment(t, t.TempDir())
	server := newSMTPServer(t, true, nil)
	sendMessage(t, e, "send", `{"to":["a@example.net"],"in_reply_to":"<two@example.net>"}`)
	sendMessage(t, e, "send", `{"to":["a@example.net"],"in_reply_to":"<two@example.net>","references":["<one@example.net>","<two@example.net>"]}`)
	messages := server.delivered()
	if len(messages) != 2 {
		t.Fatalf("delivered %d", len(messages))
	}
	if h := headerOf(t, messages[0]); h.Get("In-Reply-To") != "<two@example.net>" || h.Get("References") != "<two@example.net>" {
		t.Errorf("reply header = %v", h)
	}
	if h := headerOf(t, messages[1]); h.Get("In-Reply-To") != "<two@example.net>" ||
		strings.Join(strings.Fields(h.Get("References")), " ") != "<one@example.net> <two@example.net>" {
		t.Errorf("reply header = %v", h)
	}
	plain := sendMessage(t, e, "send", `{"to":["a@example.net"]}`)
	if h := headerOf(t, server.delivered()[2]); h.Get("In-Reply-To") != "" || h.Get("References") != "" || plain.MessageID == "" {
		t.Errorf("a new message carries reply headers: %v", h)
	}
}

func TestMessagesSendRefusesInjectionAndOpenRelayBeforeAnyConnection(t *testing.T) {
	e := sendEnvironment(t, t.TempDir())
	server := newSMTPServer(t, true, nil)
	dials, reads := e.f.dials.Load(), e.f.reads.Load()
	many := make([]string, 51)
	for i := range many {
		many[i] = fmt.Sprintf(`"u%d@example.net"`, i)
	}
	for name, args := range map[string]string{
		"newline in to":        `{"to":["a@example.net>\r\nRCPT TO:<x@example.org"]}`,
		"display name":         `{"to":["Eve <e@example.net>"]}`,
		"newline in subject":   `{"to":["a@example.net"],"subject":"x\r\nBcc: e@example.net"}`,
		"newline in reply id":  `{"to":["a@example.net"],"in_reply_to":"<a@example.net>\r\nBcc: e@example.net"}`,
		"reply id without <>":  `{"to":["a@example.net"],"in_reply_to":"a@example.net"}`,
		"reply id with space":  `{"to":["a@example.net"],"in_reply_to":"<a b@example.net>"}`,
		"references alone":     `{"to":["a@example.net"],"references":["<a@example.net>"]}`,
		"bad reference":        `{"to":["a@example.net"],"in_reply_to":"<a@example.net>","references":["<x>"]}`,
		"too many references":  `{"to":["a@example.net"],"in_reply_to":"<a@example.net>","references":[` + strings.Repeat(`"<a@example.net>",`, 20) + `"<a@example.net>"]}`,
		"too many recipients":  `{"to":[` + strings.Join(many, ",") + `]}`,
		"no recipient":         `{"to":[]}`,
		"free from":            `{"to":["a@example.net"],"from":"boss@example.net"}`,
		"free header":          `{"to":["a@example.net"],"headers":{"X-A":"b"}}`,
		"free host":            `{"to":["a@example.net"],"host":"evil.example"}`,
		"envelope from":        `{"to":["a@example.net"],"envelope_from":"x@example.net"}`,
		"bad attachment":       `{"to":["a@example.net"],"attachments":[{"name":"a.txt","content_base64":"!!"}]}`,
		"attachment path name": `{"to":["a@example.net"],"attachments":[{"name":"../a.txt","content_base64":"QQ=="}]}`,
	} {
		if _, err := e.confirmed("infomaniakmail.messages.send", "send", args); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if server.dials.Load() != 0 || e.f.dials.Load() != dials || e.f.reads.Load() != reads {
		t.Errorf("a refused call reached the network or read the secret: smtp %d imap %d reads %d", server.dials.Load(),
			e.f.dials.Load()-dials, e.f.reads.Load()-reads)
	}
}

func TestSendToolsNeedConfirmationToolsListAndFileRelease(t *testing.T) {
	e := sendEnvironment(t, t.TempDir())
	server := newSMTPServer(t, true, nil)
	e.f.addRaw(t, "Drafts", header(mailbox, "d")+"\r\nbody\r\n", imap.FlagDraft)
	validity := validityOf(t, e, "Drafts")
	cases := map[string]string{
		"infomaniakmail.messages.send": `{"to":["a@example.net"]}`,
		"infomaniakmail.drafts.send":   refArgs("Drafts", 1, validity, ""),
	}
	for operation, args := range cases {
		if _, err := e.invoke(operation, "send", args); err == nil {
			t.Errorf("%s ran without confirmation", operation)
		}
		for _, connection := range []string{"sendnotools", "open"} {
			if _, err := e.confirmed(operation, connection, args); err == nil {
				t.Errorf("%s ran through %s without a tools list", operation, connection)
			}
		}
	}
	if _, err := e.confirmed("infomaniakmail.messages.send", "sendnofiles", cases["infomaniakmail.messages.send"]); err == nil {
		t.Errorf("messages.send ran without a file release")
	}
	if server.dials.Load() != 0 {
		t.Errorf("a refused call reached SMTP")
	}
}

func TestSendNeedsOneAllowedSentFolderBeforeSending(t *testing.T) {
	for _, tc := range []struct {
		name       string
		connection string
		setup      func(*testing.T, *environment)
	}{
		{"outside the folder targets", "sendnosent", nil},
		{"none marked", "send", func(t *testing.T, e *environment) { markSpecialUse(t, e.f, "Sent", imap.MailboxAttrArchive) }},
		{"two marked", "send", func(t *testing.T, e *environment) {
			e.f.folder(t, "More")
			markSpecialUse(t, e.f, "More", imap.MailboxAttrSent)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := sendEnvironment(t, t.TempDir())
			server := newSMTPServer(t, true, nil)
			if tc.setup != nil {
				tc.setup(t, e)
			}
			e.f.addRaw(t, "Drafts", header(mailbox, "d")+"\r\nbody\r\n", imap.FlagDraft)
			validity := validityOf(t, e, "Drafts")
			_, err := e.confirmed("infomaniakmail.messages.send", tc.connection, `{"to":["a@example.net"]}`)
			if !isInvalidRequest(err) {
				t.Fatalf("messages.send err = %v, want an invalid request", err)
			}
			_, err = e.confirmed("infomaniakmail.drafts.send", tc.connection, refArgs("Drafts", 1, validity, ""))
			if !isInvalidRequest(err) {
				t.Fatalf("drafts.send err = %v, want an invalid request", err)
			}
			if server.dials.Load() != 0 {
				t.Errorf("SMTP was contacted")
			}
		})
	}
}

// Without STARTTLS, or with a certificate that does not verify, the client stops before AUTH and sends no
// credential, no sender, and no message.
func TestSMTPRequiresVerifiedSTARTTLSBeforeAuth(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trusted bool
		config  func(*smtpServer)
		class   provider.Class
		want    string
	}{
		{"no STARTTLS", true, func(s *smtpServer) { s.noStartTLS = true }, provider.ClassProviderError, "did not offer STARTTLS"},
		{"untrusted certificate", false, nil, provider.ClassTLS, "TLS connection could not be established"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := sendEnvironment(t, t.TempDir())
			server := newSMTPServer(t, tc.trusted, tc.config)
			_, err := e.confirmed("infomaniakmail.messages.send", "send", `{"to":["a@example.net"]}`)
			if err == nil || classOf(err) != tc.class || !strings.Contains(err.Error(), tc.want) ||
				!strings.Contains(err.Error(), "the message was not sent") {
				t.Fatalf("err = %v (class %q), want %q and not sent", err, classOf(err), tc.want)
			}
			if server.count("AUTH") != 0 || len(server.plainAuth)+len(server.tlsAuth) != 0 || server.count("MAIL") != 0 ||
				server.datas.Load() != 0 {
				t.Errorf("the client went on:\n%s", server.wire())
			}
			if len(stored(t, e.f, "Sent")) != 0 {
				t.Errorf("a copy was stored")
			}
		})
	}
}

func TestSMTPUsesAuthLoginWhenPlainIsNotOffered(t *testing.T) {
	e := sendEnvironment(t, t.TempDir())
	server := newSMTPServer(t, true, func(s *smtpServer) { s.onlyLogin = true })
	sendMessage(t, e, "send", `{"to":["a@example.net"]}`)
	if len(server.tlsAuth) != 1 || server.tlsAuth[0] != mailbox+"\x00"+passwordVal || !strings.Contains(server.wire(), "AUTH LOGIN") {
		t.Errorf("wire:\n%s\nauth %q", server.wire(), server.tlsAuth)
	}
}

func TestSMTPEndpointIsFixed(t *testing.T) {
	if smtpAddr != "mail.infomaniak.com:587" || smtpHost != "mail.infomaniak.com" {
		t.Errorf("smtpAddr = %q", smtpAddr)
	}
}

// Errors carry neither the server's text nor the password.
func TestSendErrorsCarryNoServerTextOrPassword(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config func(*smtpServer)
		class  provider.Class
	}{
		{"rejected recipient", func(s *smtpServer) { s.rejectRcpt = "bad@example.net" }, provider.ClassProviderError},
		{"rejected message", func(s *smtpServer) { s.dataReply = "554 5.7.1 SERVER-TEXT-CANARY spam" }, provider.ClassProviderError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := sendEnvironment(t, t.TempDir())
			newSMTPServer(t, true, tc.config)
			_, err := e.confirmed("infomaniakmail.messages.send", "send", `{"to":["a@example.net","bad@example.net"]}`)
			if err == nil || classOf(err) != tc.class {
				t.Fatalf("err = %v", err)
			}
			text := e.red.Error(err)
			for _, forbidden := range []string{"CANARY", passwordVal, "5.7.1", "5.1.1", "554", "550"} {
				if strings.Contains(text, forbidden) {
					t.Errorf("error %q contains %q", text, forbidden)
				}
			}
		})
	}
}

// The SMTP login can fail on its own: the IMAP login succeeded with the same password.
func TestSMTPLoginRefusalIsAnAuthErrorWithoutServerText(t *testing.T) {
	e := sendEnvironment(t, t.TempDir())
	server := newSMTPServer(t, true, func(s *smtpServer) { s.password = passwordVal + "0other" })
	_, err := e.confirmed("infomaniakmail.messages.send", "send", `{"to":["a@example.net"]}`)
	if classOf(err) != provider.ClassAuth || !strings.Contains(err.Error(), "the message was not sent") {
		t.Fatalf("err = %v, want class auth and not sent", err)
	}
	text := e.red.Error(err)
	for _, forbidden := range []string{"CANARY", passwordVal, "535", "5.7.8"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("error %q contains %q", text, forbidden)
		}
	}
	if server.count("MAIL") != 0 || server.datas.Load() != 0 || server.dials.Load() != 1 {
		t.Errorf("wire:\n%s", server.wire())
	}
}

func TestSendNeverRepeatsAfterTheEndOfData(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config func(*smtpServer)
		want   []string
		unsent bool
	}{
		{"connection lost after the end of DATA", func(s *smtpServer) { s.dropAfter = true },
			[]string{"may have been sent", "never repeated automatically"}, false},
		{"declined after the end of DATA", func(s *smtpServer) { s.dataReply = "554 5.7.1 no" },
			[]string{"refused the message after receiving it", "was not sent"}, true},
		{"temporary refusal after the end of DATA", func(s *smtpServer) { s.dataReply = "451 4.3.0 later" },
			[]string{"was not sent"}, true},
		{"refused recipient before DATA", func(s *smtpServer) { s.rejectRcpt = "a@example.net" },
			[]string{"the message was not sent"}, true},
		{"connection lost during DATA", func(s *smtpServer) { s.dropDuring = true },
			nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := sendEnvironment(t, t.TempDir())
			server := newSMTPServer(t, true, tc.config)
			body := strings.Repeat("line of text\n", 400)
			_, err := e.confirmed("infomaniakmail.messages.send", "send",
				fmt.Sprintf(`{"to":["a@example.net"],"body":%s}`, jsonString(body)))
			if err == nil {
				t.Fatal("no error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want %q", err, want)
				}
			}
			if tc.unsent && strings.Contains(err.Error(), "may have been sent") {
				t.Errorf("a clear refusal is reported as unclear: %v", err)
			}
			if server.dials.Load() != 1 || server.datas.Load() > 1 {
				t.Errorf("SMTP connections = %d, DATA transfers = %d, want one connection and at most one DATA",
					server.dials.Load(), server.datas.Load())
			}
			if tc.name != "refused recipient before DATA" && server.datas.Load() != 1 {
				t.Errorf("DATA transfers = %d, want exactly one", server.datas.Load())
			}
			if len(stored(t, e.f, "Sent")) != 0 {
				t.Errorf("a copy was stored for a message that is not known to be sent")
			}
		})
	}
}

func TestSendReportsAFailedCopyAndDoesNotRepeat(t *testing.T) {
	e := sendEnvironment(t, t.TempDir())
	server := newSMTPServer(t, true, nil)
	var writes atomic.Int32
	real := dialIMAP
	dialIMAP = func(ctx context.Context) (net.Conn, error) {
		conn, err := real(ctx)
		if err != nil {
			return nil, err
		}
		return &dropAfter{Conn: conn, match: regexp.MustCompile(`T\d+ APPEND `), writes: &writes}, nil
	}
	t.Cleanup(func() { dialIMAP = real })
	got := sendMessage(t, e, "send", `{"to":["a@example.net"]}`)
	if got.CopyStored || got.SentFolder != "" || !strings.Contains(got.Note, "was sent") ||
		!strings.Contains(got.Note, "do not send the message again") || got.MessageID == "" {
		t.Errorf("result = %+v", got)
	}
	if server.datas.Load() != 1 || writes.Load() != 1 || len(server.delivered()) != 1 {
		t.Errorf("DATA = %d, APPEND = %d, delivered = %d, want 1, 1, 1", server.datas.Load(), writes.Load(), len(server.delivered()))
	}
}

func TestSendRespectsTheSenderTargets(t *testing.T) {
	e := sendEnvironment(t, t.TempDir())
	server := newSMTPServer(t, true, nil)
	e.f.addRaw(t, "Drafts", header(mailbox, "d")+"\r\nbody\r\n", imap.FlagDraft)
	validity := validityOf(t, e, "Drafts")
	if _, err := e.confirmed("infomaniakmail.messages.send", "sendsenders", `{"to":["a@example.net"]}`); !isInvalidRequest(err) {
		t.Errorf("messages.send err = %v, want an invalid request", err)
	}
	if _, err := e.confirmed("infomaniakmail.drafts.send", "sendsenders", refArgs("Drafts", 1, validity, "")); !isInvalidRequest(err) {
		t.Errorf("drafts.send err = %v, want an invalid request", err)
	}
	if server.dials.Load() != 0 {
		t.Errorf("SMTP was contacted")
	}
}

func rawDraft(from, extraHeaders, contentType, body string) string {
	return fmt.Sprintf("From: %s\r\nTo: a@example.net\r\nSubject: s\r\nDate: %s\r\nMessage-ID: <d1@example.com>\r\n%sMIME-Version: 1.0\r\nContent-Type: %s\r\nContent-Transfer-Encoding: 7bit\r\n\r\n%s",
		from, base.Format(time.RFC1123Z), extraHeaders, contentType, body)
}

func TestDraftsSendSendsACheckedDraftWithoutBccAndKeepsIt(t *testing.T) {
	dir := t.TempDir()
	e := sendEnvironment(t, dir)
	server := newSMTPServer(t, true, nil)
	file := []byte("attachment bytes \x00\x01")
	created, err := e.confirmed("infomaniakmail.drafts.create", "send", fmt.Sprintf(
		`{"to":["a@example.net"],"cc":["c@example.net"],"bcc":["secret-bcc@example.net"],"subject":"Grüße","body":"Hallo\nBODY-CANARY",`+
			`"in_reply_to":"<x@example.net>","attachments":[{"name":"f.bin","content_base64":%q}]}`, base64.StdEncoding.EncodeToString(file)))
	if err == nil {
		t.Fatalf("drafts.create accepted reply fields: %s", created)
	}
	created, err = e.confirmed("infomaniakmail.drafts.create", "send", fmt.Sprintf(
		`{"to":["a@example.net"],"cc":["c@example.net"],"bcc":["secret-bcc@example.net"],"subject":"Grüße","body":"Hallo\nBODY-CANARY",`+
			`"attachments":[{"name":"f.bin","content_base64":%q}]}`, base64.StdEncoding.EncodeToString(file)))
	if err != nil {
		t.Fatal(err)
	}
	var draft DraftResult
	_ = json.Unmarshal([]byte(created), &draft)
	if !bytes.Contains(stored(t, e.f, "Drafts")[0], []byte("Bcc:")) {
		t.Fatalf("the draft holds no Bcc header")
	}

	result, err := e.confirmed("infomaniakmail.drafts.send", "send", refArgs("Drafts", draft.UID, draft.UIDValidity, ""))
	if err != nil {
		t.Fatalf("drafts.send: %v", err)
	}
	var got SendResult
	_ = json.Unmarshal([]byte(result), &got)
	if got.Recipients != 3 || !got.CopyStored || got.SentFolder != "Sent" || !got.DraftKept || len(got.Attachments) != 1 ||
		got.Attachments[0].Name != "f.bin" || got.Attachments[0].Size != int64(len(file)) || got.MessageID == "" {
		t.Fatalf("result = %+v", got)
	}
	if strings.Contains(result, "BODY-CANARY") || strings.Contains(result, "secret-bcc") {
		t.Errorf("result carries content: %s", result)
	}
	if server.datas.Load() != 1 || server.count("RCPT") != 3 || !strings.Contains(server.wire(), "RCPT TO:<secret-bcc@example.net>") ||
		!strings.Contains(server.wire(), "MAIL FROM:<"+mailbox+">") {
		t.Errorf("wire:\n%s", server.wire())
	}
	sent := server.delivered()[0]
	header := headerOf(t, sent)
	if header.Get("Bcc") != "" || hasBccLine(sent) || strings.Contains(string(sent), "secret-bcc") ||
		header.Get("From") != mailbox || header.Get("Message-ID") != got.MessageID {
		t.Errorf("sent message:\n%s", sent)
	}
	if copies := stored(t, e.f, "Sent"); len(copies) != 1 || !bytes.Equal(copies[0], sent) {
		t.Errorf("copy differs from the sent message")
	}
	if drafts := stored(t, e.f, "Drafts"); len(drafts) != 1 || !bytes.Contains(drafts[0], []byte("Bcc:")) {
		t.Errorf("the draft was changed or removed")
	}
	if flags := flagsOf(t, e, "Drafts", draft.UID); !hasFlag(flags, imap.FlagDraft) {
		t.Errorf("draft flags = %v", flags)
	}
}

func TestDraftsSendRefusesWhatItCannotValidate(t *testing.T) {
	good := "text/plain; charset=utf-8"
	for _, tc := range []struct {
		name  string
		raw   string
		flags []imap.Flag
		class provider.Class
		bad   bool // invalid request instead of not-found
	}{
		{"not a draft", rawDraft(mailbox, "", good, "body\r\n"), nil, provider.ClassNotFound, false},
		{"foreign From", rawDraft("boss@example.net", "", good, "body\r\n"), []imap.Flag{imap.FlagDraft}, "", true},
		{"From with display name", rawDraft("Box <"+mailbox+">", "", good, "body\r\n"), []imap.Flag{imap.FlagDraft}, "", true},
		{"unknown header", rawDraft(mailbox, "X-Mailer: other\r\n", good, "body\r\n"), []imap.Flag{imap.FlagDraft}, "", true},
		{"Reply-To header", rawDraft(mailbox, "Reply-To: e@example.net\r\n", good, "body\r\n"), []imap.Flag{imap.FlagDraft}, "", true},
		{"repeated header", rawDraft(mailbox, "Subject: again\r\n", good, "body\r\n"), []imap.Flag{imap.FlagDraft}, "", true},
		{"recipient with display name", strings.Replace(rawDraft(mailbox, "", good, "b\r\n"), "To: a@example.net", "To: Eve <a@example.net>", 1), []imap.Flag{imap.FlagDraft}, "", true},
		{"html", rawDraft(mailbox, "", "text/html", "<b>x</b>\r\n"), []imap.Flag{imap.FlagDraft}, "", true},
		{"8-bit body", rawDraft(mailbox, "", good, "Grüße\r\n"), []imap.Flag{imap.FlagDraft}, "", true},
		{"nested multipart", rawDraft(mailbox, "", `multipart/mixed; boundary="B"`,
			"--B\r\nContent-Type: multipart/alternative; boundary=\"C\"\r\n\r\n--C\r\nContent-Type: text/plain\r\n\r\nx\r\n--C--\r\n--B--\r\n"), []imap.Flag{imap.FlagDraft}, "", true},
		{"no text part", rawDraft(mailbox, "", `multipart/mixed; boundary="B"`,
			"--B\r\nContent-Type: application/pdf; name=a.pdf\r\nContent-Disposition: attachment; filename=a.pdf\r\nContent-Transfer-Encoding: base64\r\n\r\nQQ==\r\n--B--\r\n"), []imap.Flag{imap.FlagDraft}, "", true},
		{"bad reply id", rawDraft(mailbox, "In-Reply-To: <a b>\r\n", good, "body\r\n"), []imap.Flag{imap.FlagDraft}, "", true},
		{"bad Message-ID", strings.Replace(rawDraft(mailbox, "", good, "b\r\n"), "<d1@example.com>", "<d1>", 1), []imap.Flag{imap.FlagDraft}, "", true},
		{"only Bcc", strings.Replace(rawDraft(mailbox, "", good, "b\r\n"), "To: a@example.net", "Bcc: a@example.net", 1), []imap.Flag{imap.FlagDraft}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := sendEnvironment(t, t.TempDir())
			server := newSMTPServer(t, true, nil)
			uid := e.f.addRaw(t, "Drafts", tc.raw, tc.flags...)
			validity := validityOf(t, e, "Drafts")
			_, err := e.confirmed("infomaniakmail.drafts.send", "send", refArgs("Drafts", uid, validity, ""))
			if err == nil {
				t.Fatal("the draft was sent")
			}
			if tc.bad && !isInvalidRequest(err) || !tc.bad && classOf(err) != tc.class {
				t.Errorf("err = %v (class %q)", err, classOf(err))
			}
			if server.dials.Load() != 0 || len(stored(t, e.f, "Sent")) != 0 {
				t.Errorf("a refused draft reached SMTP or the Sent folder")
			}
		})
	}
}

func TestDraftsSendIsBoundToTheDraftsFolderAndUIDValidity(t *testing.T) {
	e := sendEnvironment(t, t.TempDir())
	server := newSMTPServer(t, true, nil)
	inbox := e.f.addRaw(t, "INBOX", rawDraft(mailbox, "", "text/plain", "body\r\n"), imap.FlagDraft)
	draft := e.f.addRaw(t, "Drafts", rawDraft(mailbox, "", "text/plain", "body\r\n"), imap.FlagDraft)
	validity := validityOf(t, e, "Drafts")
	if _, err := e.confirmed("infomaniakmail.drafts.send", "send", refArgs("INBOX", inbox, validityOf(t, e, "INBOX"), "")); !isInvalidRequest(err) {
		t.Errorf("a draft flagged message in INBOX: err = %v", err)
	}
	if _, err := e.confirmed("infomaniakmail.drafts.send", "send", refArgs("Drafts", draft, validity+1, "")); !isInvalidRequest(err) {
		t.Errorf("a stale uidvalidity: err = %v", err)
	}
	if _, err := e.confirmed("infomaniakmail.drafts.send", "send", refArgs("Drafts", draft+5, validity, "")); classOf(err) != provider.ClassNotFound {
		t.Errorf("a missing uid: err = %v", err)
	}
	if server.dials.Load() != 0 {
		t.Errorf("SMTP was contacted")
	}
	if _, err := e.confirmed("infomaniakmail.drafts.send", "send", refArgs("Drafts", draft, validity, "")); err != nil {
		t.Errorf("the plain text draft: %v", err)
	}
}

func TestDraftsSendNeverRepeatsAfterTheEndOfData(t *testing.T) {
	e := sendEnvironment(t, t.TempDir())
	server := newSMTPServer(t, true, func(s *smtpServer) { s.dropAfter = true })
	uid := e.f.addRaw(t, "Drafts", rawDraft(mailbox, "", "text/plain", "body\r\n"), imap.FlagDraft)
	validity := validityOf(t, e, "Drafts")
	_, err := e.confirmed("infomaniakmail.drafts.send", "send", refArgs("Drafts", uid, validity, ""))
	if err == nil || !strings.Contains(err.Error(), "may have been sent") {
		t.Fatalf("err = %v", err)
	}
	if server.dials.Load() != 1 || server.datas.Load() != 1 || len(stored(t, e.f, "Sent")) != 0 || len(stored(t, e.f, "Drafts")) != 1 {
		t.Errorf("dials %d, DATA %d", server.dials.Load(), server.datas.Load())
	}
}

func TestMessagesGetOffersTheMessageIDForAReply(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	good := e.f.addRaw(t, "INBOX", "From: alice@example.net\r\nSubject: s\r\nMessage-ID: <parent.1@example.net>\r\n\r\nx\r\n")
	bad := e.f.addRaw(t, "INBOX", "From: alice@example.net\r\nSubject: s\r\nMessage-ID: not an id\r\n\r\nx\r\n")
	validity := validityOf(t, e, "INBOX")
	if got := getMessage(t, e, "open", getArgs("INBOX", good, validity)); got.MessageID != "<parent.1@example.net>" {
		t.Errorf("message_id = %q", got.MessageID)
	}
	if got := getMessage(t, e, "open", getArgs("INBOX", bad, validity)); got.MessageID != "" {
		t.Errorf("message_id = %q, want none for a malformed one", got.MessageID)
	}
}
