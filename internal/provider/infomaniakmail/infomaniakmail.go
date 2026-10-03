// Package infomaniakmail implements read-only access to exactly one Infomaniak mailbox over IMAP. It lists
// the folders of the mailbox and the envelope data of its messages (UID, date, From, To, Subject, flags, and
// size), reads one message with a bounded text part and the metadata of its attachments, and reads one
// attachment, inline up to a fixed size or into a local file the connection releases for writing. It changes
// no flag, moves nothing, and sends nothing.
//
// The connection always goes to the fixed host mail.infomaniak.com on port 993 with implicit TLS and
// certificate verification. No host, port, or TLS switch comes from configuration or from an argument; only
// the package's own tests replace the dialer, see dialIMAP. The login is the mailbox address of the
// connection's mailbox/ADDRESS target together with a mailbox password the person created for it; the
// Infomaniak REST mail API plays no part here.
//
// A connection binds exactly one mailbox, plus optional allow-lists of folders (folder/NAME) and sender
// addresses (sender/ADDRESS). Without a folder list every folder of the mailbox is reachable; a folder
// outside a configured list is refused before any secret is read and before any network access, and the
// refusal never names the folder that is allowed. The sender list is applied locally to the parsed From
// address of every message; an IMAP SEARCH on its own is never trusted for it.
//
// A folder is opened with EXAMINE, never SELECT, so a read cannot change a message's \Seen flag. Envelopes
// are fetched with ENVELOPE and BODYSTRUCTURE, and content only with BODY.PEEK[part], none of which ever
// marks a message seen. A part is addressed only by a part number taken from the message's own
// BODYSTRUCTURE, never by a section that comes from an argument. A UID is meaningful only together
// with its folder and the folder's UIDVALIDITY: every listing names both, and a later request that refers to
// a UID must repeat the UIDVALIDITY, which must still match.
//
// SEARCH uses fixed, typed criteria only (a since and before date, unread, and one validated sender address).
// No free search string and no raw IMAP command ever comes from an argument. The UID window and the number
// of results are capped.
//
// Everything a mailbox answers with is untrusted data from third parties. Every string is cleaned of control
// characters and capped, and the data carries its own sensitivity class. IMAP response texts never reach an
// error message.
package infomaniakmail

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "infomaniakmail"

// roleMailPassword is the single secret role: the mailbox password created in Infomaniak for the mailbox.
const roleMailPassword = "password"

// The fixed IMAP endpoint (https://www.infomaniak.com/en/support/faq/2427/sync-your-emails-across-all-your-
// devices): implicit TLS on port 993.
const (
	imapHost = "mail.infomaniak.com"
	imapAddr = imapHost + ":993"
	// defaultURL is the service endpoint a configuration may name. It is informational only: the IMAP host is
	// never derived from it.
	defaultURL = "https://" + imapHost
)

// Data sensitivity classes. Folder names are the mailbox's own structure; message envelopes, bodies, and
// attachments are third-party content and untrusted.
const (
	foldersSensitivity  = "infomaniak-mail-folders"
	messagesSensitivity = "infomaniak-mail-messages"
)

const (
	defaultTimeout = 30 * time.Second
	// transferTimeout replaces defaultTimeout for an attachment written to a local file.
	transferTimeout = 30 * time.Minute
	maxPassword     = 1024
)

// limiters holds the budget of every mailbox login this process has used. Infomaniak documents no request
// budget for IMAP, so there is no proactive spacing; every operation opens exactly one connection and a
// failed login is never retried.
var limiters = ratelimit.NewRegistry(0)

// dialIMAP opens the transport connection. It is the package's only network seam: the default dials the
// fixed host with implicit TLS, certificate and host name verification, and TLS 1.2 or newer. Only the
// package's own tests replace it, so no test ever reaches Infomaniak.
var dialIMAP = func(ctx context.Context) (net.Conn, error) {
	dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: defaultTimeout}, Config: tlsConfig()}
	return dialer.DialContext(ctx, "tcp", imapAddr)
}

func tlsConfig() *tls.Config {
	return &tls.Config{ServerName: imapHost, MinVersion: tls.VersionTLS12}
}

// Client binds one mailbox login to the folder and sender scope of its connection.
type Client struct {
	scope    scope
	password string
	limiter  *ratelimit.Limiter
}

// Open resolves the mailbox password of one selected connection and returns a client for its scope.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	const op = "open"
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if secrets == nil {
		return nil, providerError(op, "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleMailPassword)
	if err != nil {
		return nil, err
	}
	if !validPassword(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: op, Message: "the mailbox password is unusable"}
	}
	if red != nil {
		red.Add(value.Secret)
	}
	return &Client{
		scope: bound, password: value.Secret, limiter: limiters.For(bound.mailbox + "\x00" + value.Secret),
	}, nil
}

// validPassword keeps an obviously unusable value out of the LOGIN command. The real check is Infomaniak's.
func validPassword(value string) bool {
	if value == "" || len(value) > maxPassword || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// boundScope reads the connection's scope, wrapping a parse failure as a provider error; both a missing
// connection and a malformed target are configuration problems that exist before any secret is resolved.
func boundScope(resolved *config.Resolved) (scope, error) {
	if resolved == nil {
		return scope{}, providerError("open", "no connection was selected")
	}
	bound, err := scopeOf(resolved)
	if err != nil {
		return scope{}, providerError("open", err.Error())
	}
	return bound, nil
}

// session is one logged-in IMAP connection. close ends it and must always be called.
type session struct {
	client *imapclient.Client
	conn   net.Conn
	close  func()
}

// abort drops the transport at once. It ends a transfer that stopped reading a literal, which a regular
// logout could not get past.
func (s *session) abort() { _ = s.conn.Close() }

// connect opens the connection and logs in. One operation uses one connection, closed when it ends.
func (c *Client) connect(ctx context.Context, op string) (*session, error) {
	return c.connectWithin(ctx, op, defaultTimeout)
}

// connectWithin is connect with the time the whole operation may take, which a large attachment transfer
// raises above the default.
func (c *Client) connectWithin(ctx context.Context, op string, timeout time.Duration) (*session, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, provider.Waited(op, "Infomaniak Mail", err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	conn, err := dialIMAP(ctx)
	if err != nil {
		cancel()
		return nil, provider.Transport(op, "Infomaniak Mail", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	client := imapclient.New(conn, &imapclient.Options{})
	closer := func() {
		stop()
		_ = client.Logout().Wait()
		_ = client.Close()
		cancel()
	}
	if err := client.Login(c.scope.mailbox, c.password).Wait(); err != nil {
		closer()
		return nil, loginFailure(op, err)
	}
	return &session{client: client, conn: conn, close: closer}, nil
}

// loginFailure normalises a failed LOGIN. A tagged NO or BAD from the server is a rejected login whatever
// its text says; anything else is a transport failure. The server's own text is never copied.
func loginFailure(op string, err error) error {
	var imapErr *imap.Error
	if errors.As(err, &imapErr) {
		if imapErr.Type == imap.StatusResponseTypeBye {
			return &provider.Error{Class: provider.ClassUnreachable, Op: op, Message: "Infomaniak Mail closed the connection"}
		}
		return &provider.Error{Class: provider.ClassAuth, Op: op,
			Message: "Infomaniak Mail rejected the mailbox address or password"}
	}
	return provider.Transport(op, "Infomaniak Mail", err)
}

// failure normalises a failure after login to a stable class without ever copying the server's text.
func failure(op string, err error) error {
	var imapErr *imap.Error
	if !errors.As(err, &imapErr) {
		return provider.Transport(op, "Infomaniak Mail", err)
	}
	switch {
	case imapErr.Type == imap.StatusResponseTypeBye:
		return &provider.Error{Class: provider.ClassUnreachable, Op: op, Message: "Infomaniak Mail closed the connection"}
	case imapErr.Code == imap.ResponseCodeNonExistent:
		return &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "Infomaniak Mail does not hold this folder or does not show it to this mailbox"}
	case imapErr.Code == imap.ResponseCodeNoPerm, imapErr.Code == imap.ResponseCodeAuthorizationFailed:
		return &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "this mailbox may not perform this operation"}
	case imapErr.Code == imap.ResponseCodeAuthenticationFailed, imapErr.Code == imap.ResponseCodeExpired:
		return &provider.Error{Class: provider.ClassAuth, Op: op,
			Message: "Infomaniak Mail rejected the mailbox address or password"}
	case imapErr.Code == imap.ResponseCodeUnavailable:
		return &provider.Error{Class: provider.ClassUnreachable, Op: op, Message: "Infomaniak Mail is unavailable"}
	}
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: "Infomaniak Mail rejected the operation"}
}

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

func invalidResponse(op, message string) error {
	return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
}

// invalidRequest refuses a request the connection's own configuration decides against. No message ever
// quotes the refused value or names what the connection does allow.
func invalidRequest(message string) error {
	return &application.InvalidRequestError{Message: message}
}

// clean makes one string from the mailbox safe to return: valid UTF-8, no control or line-separator
// character, and at most max runes.
func clean(value string, max int) string {
	value = strings.ToValidUTF8(value, "�")
	var builder strings.Builder
	count := 0
	for _, r := range value {
		if count >= max {
			break
		}
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			r = ' '
		}
		builder.WriteRune(r)
		count++
	}
	return builder.String()
}

// TestConnection logs in and lists the INBOX name, the smallest read that proves the address, the password,
// and the LIST command.
func TestConnection(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (provider.Class, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return "", err
	}
	const op = "test connection"
	conn, err := client.connect(ctx, op)
	if err == nil {
		defer conn.close()
		_, err = conn.client.List("", "INBOX", nil).Collect()
		if err != nil {
			err = failure(op, err)
		}
	}
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return provider.ClassProviderError, nil
	}
	return provider.ClassOK, nil
}

// validBaseURL accepts only the fixed service endpoint. It exists so a configuration cannot suggest another
// host; the IMAP host is never taken from it.
func validBaseURL(raw string) error {
	if !strings.EqualFold(strings.TrimRight(strings.TrimSpace(raw), "/"), defaultURL) {
		return errors.New("an Infomaniak Mail service connects to a fixed Infomaniak host; leave base_url out or " +
			"set it to " + defaultURL)
	}
	return nil
}

// Register adds Infomaniak Mail metadata, its connection test, and its read operations.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Infomaniak Mail", DefaultBaseURL: defaultURL, ValidateBaseURL: validBaseURL,
		DefaultPermissions: []config.Permission{config.PermissionRead},
		Description:        "Infomaniak mailbox over IMAP, folders, message envelopes, bodies, and attachments read for one mailbox",
		SecretRoles: []config.SecretRole{{
			Name: roleMailPassword,
			Description: "Mailbox password created in the Infomaniak Manager for this mailbox; the login name is " +
				"the mailbox address of the connection's mailbox target",
		}},
		Target: config.TargetMetadata{
			Label:    "mailbox, folders, and senders",
			Required: true,
			Multiple: true,
			Description: "exactly one mailbox/ADDRESS target, which is also the IMAP login name, plus an optional, " +
				"repeatable allow-list of folder/NAME targets and an optional, repeatable allow-list of " +
				"sender/ADDRESS targets; without a folder target every folder of the mailbox is reachable, and " +
				"without a sender target every sender. A folder outside the list is refused before any secret " +
				"is read; the sender list is applied locally to the parsed From address of every message",
			Kinds: []config.TargetKind{{
				Name:        "mailbox",
				Description: "the one mailbox this connection logs in to; required, exactly one",
				Forms:       []string{"mailbox/ADDRESS"},
			}, {
				Name: "folder",
				Description: "one folder of the mailbox, by its exact name; optional, repeatable; no wildcards " +
					"and no subfolders implied",
				Forms: []string{"folder/NAME"},
			}, {
				Name: "sender",
				Description: "one sender address whose messages are listed; optional, repeatable; compared " +
					"against the From address of each message, case-insensitively",
				Forms: []string{"sender/ADDRESS"},
			}},
			Validate:    validateTarget,
			ValidateSet: func(values []string) error { _, err := parseScope(values); return err },
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read folders, messages, and attachments", Recommended: true,
			Description: "lists the folders of the bound mailbox and the envelopes of its messages, reads one " +
				"message with a bounded text and its attachment list, and reads one attachment inline or into a " +
				"released local file; changes nothing in the mailbox, not even a Seen flag",
			Tools: []string{foldersList.ID, messagesList.ID, messagesGet.ID, attachmentsGet.ID},
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: foldersList, Handler: capability.Handler(invokeFoldersList)},
		capability.Operation{Descriptor: messagesList, Handler: capability.Handler(invokeMessagesList)},
		capability.Operation{Descriptor: messagesGet, Handler: capability.Handler(invokeMessagesGet)},
		capability.Operation{Descriptor: attachmentsGet, Handler: capability.Handler(invokeAttachmentsGet)},
	)
}

var mailReadRisk = func(sensitivity string) capability.Risk {
	return capability.Risk{
		Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: sensitivity,
	}
}
