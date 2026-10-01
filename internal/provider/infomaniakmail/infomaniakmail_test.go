package infomaniakmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// No test reaches Infomaniak: every connection goes to an in-memory IMAP server on a local port through the
// package's dialer seam.
const (
	mailbox     = "box@example.com"
	passwordEnv = "TEST_INFOMANIAK_MAIL_PASSWORD"
	passwordVal = "canary0mail0password0canary"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type fixture struct {
	user  *imapmemserver.User
	wire  *lockedBuffer
	dials atomic.Int32
	reads atomic.Int32
	// password is what the environment returns for the connection's credential.
	password atomic.Value
}

type literal struct{ *bytes.Reader }

func (l literal) Size() int64 { return int64(l.Len()) }

// newFixture starts the in-memory server and points the package's dialer at it for one test.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{user: imapmemserver.NewUser(mailbox, passwordVal), wire: &lockedBuffer{}}
	f.password.Store(passwordVal)
	memory := imapmemserver.New()
	memory.AddUser(f.user)
	server := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memory.NewSession(), nil, nil
		},
		InsecureAuth: true,
		DebugWriter:  f.wire,
		Logger:       log.New(io.Discard, "", 0),
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	previous := dialIMAP
	dialIMAP = func(ctx context.Context) (net.Conn, error) {
		f.dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}
	t.Cleanup(func() { dialIMAP = previous })
	t.Cleanup(limiters.Replace(mailbox+"\x00"+passwordVal, freeLimiter()))
	return f
}

func freeLimiter() *ratelimit.Limiter {
	return ratelimit.New(0, time.Now, func(context.Context, time.Duration) error { return nil })
}

func (f *fixture) folder(t *testing.T, name string) {
	t.Helper()
	if err := f.user.Create(name, nil); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
}

// add appends one message and returns its UID.
func (f *fixture) add(t *testing.T, folder, from, subject string, when time.Time, flags ...imap.Flag) uint32 {
	t.Helper()
	raw := fmt.Sprintf("From: %s\r\nTo: Box <%s>\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%d@example.com>\r\n\r\nBODY-CANARY-%s\r\n",
		from, mailbox, subject, when.Format(time.RFC1123Z), when.UnixNano(), subject)
	data, err := f.user.Append(folder, literal{bytes.NewReader([]byte(raw))}, &imap.AppendOptions{Time: when, Flags: flags})
	if err != nil {
		t.Fatalf("append to %s: %v", folder, err)
	}
	return uint32(data.UID)
}

func registry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	return reg
}

var allPermissions = config.Permissions()

func coreConfig() *config.Config {
	credential := config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleMailPassword: passwordEnv}}
	connection := func(targets ...string) config.Connection {
		return config.Connection{Service: "mail", Credential: "mail-reader", Permissions: allPermissions,
			Targets: append([]string{"mailbox/" + mailbox}, targets...)}
	}
	return &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"mail": {Provider: Provider}},
		Credentials: map[string]config.Credential{"mail-reader": credential},
		Connections: map[string]config.Connection{
			"open":    connection(),
			"folders": connection("folder/Allowed", "folder/INBOX"),
			"allowed": connection("folder/Allowed"),
			"senders": connection("sender/alice@example.net"),
		},
	}
}

type environment struct {
	core *application.Core
	red  *redact.Redactor
	f    *fixture
}

func newEnvironment(t *testing.T) *environment {
	t.Helper()
	f := newFixture(t)
	red := &redact.Redactor{}
	resolver := secret.NewWith(func(name string) string {
		f.reads.Add(1)
		if name == passwordEnv {
			return f.password.Load().(string)
		}
		return ""
	}, nil, nil, red)
	return &environment{core: application.New(registry(t), coreConfig(), resolver, red), red: red, f: f}
}

func (e *environment) invoke(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: json.RawMessage(arguments),
	})
	return string(response.Result), err
}

func classOf(err error) provider.Class {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		return providerErr.Class
	}
	return ""
}

func isInvalidRequest(err error) bool {
	var invalid *application.InvalidRequestError
	return errors.As(err, &invalid)
}

func TestRegisterPublishesMetadataAndTools(t *testing.T) {
	reg := registry(t)
	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || metadata.Name != "Infomaniak Mail" || len(metadata.SecretRoles) != 1 ||
		metadata.SecretRoles[0].Name != "password" || !metadata.Target.Required || !metadata.Target.Multiple ||
		len(metadata.Target.Kinds) != 3 {
		t.Fatalf("metadata = %+v", metadata)
	}
	if len(metadata.Tools) != 2 || len(metadata.Profiles) != 1 || !metadata.Profiles[0].Recommended ||
		metadata.Profiles[0].ID != "read" {
		t.Fatalf("tools = %+v, profiles = %+v", metadata.Tools, metadata.Profiles)
	}
	for _, descriptor := range reg.Provider(Provider) {
		if descriptor.Risk.Effect != capability.EffectRead || descriptor.Risk.Confirmation != capability.ConfirmationNone {
			t.Errorf("%s risk = %+v, want a read without confirmation", descriptor.ID, descriptor.Risk)
		}
	}
	if messagesList.Risk.DataSensitivity != "infomaniak-mail-messages" {
		t.Errorf("messages sensitivity = %q", messagesList.Risk.DataSensitivity)
	}
}

// The endpoint is fixed: implicit TLS to the one Infomaniak host with verification, and no configuration can
// name another host.
func TestEndpointIsFixed(t *testing.T) {
	if imapAddr != "mail.infomaniak.com:993" {
		t.Errorf("imapAddr = %q", imapAddr)
	}
	cfg := tlsConfig()
	if cfg.ServerName != "mail.infomaniak.com" || cfg.InsecureSkipVerify || cfg.MinVersion < 0x0303 {
		t.Errorf("tls config = %+v", cfg)
	}
	for raw, ok := range map[string]bool{
		defaultURL: true, defaultURL + "/": true, "https://evil.example": false, "http://mail.infomaniak.com": false,
		"https://mail.infomaniak.com:143": false, "https://mail.infomaniak.com.evil.example": false,
	} {
		if err := validBaseURL(raw); (err == nil) != ok {
			t.Errorf("validBaseURL(%q) = %v, want ok=%t", raw, err, ok)
		}
	}
}

func TestTestConnection(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	resolved := &config.Resolved{
		Name: "open", Provider: Provider, Credential: "mail-reader",
		Secrets: coreConfig().Credentials["mail-reader"], Targets: []string{"mailbox/" + mailbox},
	}
	secrets := secret.NewWith(func(name string) string {
		if name == passwordEnv {
			return e.f.password.Load().(string)
		}
		return ""
	}, nil, nil, e.red)

	class, err := TestConnection(context.Background(), resolved, secrets, e.red)
	if err != nil || class != provider.ClassOK {
		t.Fatalf("TestConnection() = %q, %v, want ok", class, err)
	}
	if wire := e.f.wire.String(); !strings.Contains(wire, "LOGIN") || !strings.Contains(wire, "LIST") {
		t.Errorf("wire = %q, want LOGIN and LIST", wire)
	}

	e.f.password.Store("wrong-password-value")
	class, err = TestConnection(context.Background(), resolved, secrets, e.red)
	if err != nil || class != provider.ClassAuth {
		t.Errorf("wrong password: TestConnection() = %q, %v, want auth", class, err)
	}

	e.f.password.Store(passwordVal)
	dialIMAP = func(context.Context) (net.Conn, error) { return nil, &net.DNSError{Err: "no such host", Name: "x"} }
	class, err = TestConnection(context.Background(), resolved, secrets, e.red)
	if err != nil || class != provider.ClassUnreachable {
		t.Errorf("unreachable: TestConnection() = %q, %v, want unreachable", class, err)
	}
}

// A wrong password is an auth failure that carries neither the server's text nor the password.
func TestWrongPasswordIsAnAuthErrorWithoutProviderText(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	e.f.password.Store("wrong-password-value")
	_, err := e.invoke("infomaniakmail.folders.list", "open", `{}`)
	if classOf(err) != provider.ClassAuth {
		t.Fatalf("error = %v, want class auth", err)
	}
	text := e.red.Error(err)
	for _, forbidden := range []string{"imap", "wrong-password-value", passwordVal, "[", "AUTHENTICATION"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("error %q contains %q", text, forbidden)
		}
	}
}

func TestSecretIsRegisteredWithTheRedactor(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	if _, err := e.invoke("infomaniakmail.folders.list", "open", `{}`); err != nil {
		t.Fatalf("folders.list: %v", err)
	}
	if got := e.red.Apply("x " + passwordVal + " y"); strings.Contains(got, passwordVal) {
		t.Errorf("redactor leaves the password in %q", got)
	}
}

func TestCleanBoundsAndSanitisesStrings(t *testing.T) {
	got := clean("a\x00b\tc\nd e\xffz", 100)
	if strings.ContainsAny(got, "\x00\t\n ") || !strings.Contains(got, "a b c d e") {
		t.Errorf("clean() = %q", got)
	}
	if got := clean(strings.Repeat("é", 500), 10); len([]rune(got)) != 10 {
		t.Errorf("clean() kept %d runes, want 10", len([]rune(got)))
	}
}
