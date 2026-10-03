package infomaniakmail

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The fixed submission endpoint: port 587 with mandatory STARTTLS.
const (
	smtpHost = imapHost
	smtpAddr = smtpHost + ":587"
)

const (
	// notSent ends the failure of every step that precedes the end of the DATA transfer: the server never got
	// the terminating dot, so it cannot have accepted the message.
	notSent = "; the message was not sent"
	// maybeSent ends the failure of the step that ends the DATA transfer when the answer is unclear.
	maybeSent = "; the message may have been sent, check the Sent folder and ask the recipients before sending it " +
		"again; it is never repeated automatically"
)

// dialSMTP opens the transport connection of the submission and returns the TLS configuration for the
// STARTTLS upgrade. It is the package's only SMTP seam: the default dials the fixed host in plain text, as
// STARTTLS requires, and verifies the certificate and host name afterwards. Only the package's own tests
// replace it, so no test ever reaches Infomaniak.
var dialSMTP = func(ctx context.Context) (net.Conn, *tls.Config, error) {
	conn, err := (&net.Dialer{Timeout: defaultTimeout}).DialContext(ctx, "tcp", smtpAddr)
	if err != nil {
		return nil, nil, err
	}
	return conn, tlsConfig(), nil
}

// outgoing is a message ready to be sent: the bytes without any Bcc header, the envelope sender, and every
// envelope recipient.
type outgoing struct {
	raw         []byte
	from        string
	rcpts       []string
	messageID   string
	attachments []DraftAttachment
	// draftKept marks a message that came from a draft, which stays where it is.
	draftKept bool
}

// timeout is the time the transfer may take: longer for a message that carries a large attachment.
func (m *outgoing) timeout() time.Duration {
	if len(m.raw) > draftTransferBytes {
		return transferTimeout
	}
	return defaultTimeout
}

// loginAuth is AUTH LOGIN, offered by servers that do not advertise PLAIN. Like smtp.PlainAuth it refuses to
// start on a connection without TLS.
type loginAuth struct{ user, password string }

func (a loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("the connection is not encrypted")
	}
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(challenge))) {
	case "username:":
		return []byte(a.user), nil
	case "password:":
		return []byte(a.password), nil
	}
	return nil, errors.New("unexpected challenge")
}

// smtpFailure normalises a failure of the submission. A reply code decides the class and the server's text is
// never copied; step says what was refused. suffix always ends the message.
func smtpFailure(op string, err error, step, suffix string) error {
	var reply *textproto.Error
	if errors.As(err, &reply) {
		var class provider.Class
		var message string
		switch {
		case reply.Code == 530 || reply.Code == 534 || reply.Code == 535 || reply.Code == 454:
			class, message = provider.ClassAuth, "Infomaniak Mail rejected the mailbox address or password"
		case reply.Code == 421:
			class, message = provider.ClassUnreachable, "Infomaniak Mail closed the connection"
		default:
			class, message = provider.ClassProviderError, "Infomaniak Mail refused "+step
		}
		return &provider.Error{Class: class, Op: op, Message: message + suffix}
	}
	failed := provider.Transport(op, "Infomaniak Mail", err)
	failed.Message += suffix
	return failed
}

// smtpSend submits the message over one connection: greeting, EHLO, STARTTLS (required), AUTH, MAIL FROM,
// RCPT TO for every recipient, and exactly one DATA transfer. It never retries. Every failure before the
// terminating dot of DATA means the message was not sent; a refusal by reply code after it means the server
// declined the message; any other failure after it leaves the outcome unknown and says so.
func (c *Client) smtpSend(ctx context.Context, op string, msg *outgoing, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, tlsCfg, err := dialSMTP(ctx)
	if err != nil {
		return smtpFailure(op, err, "the connection", notSent)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	defer context.AfterFunc(ctx, func() { _ = conn.Close() })()
	if tlsCfg == nil {
		tlsCfg = tlsConfig()
	}

	client, err := smtp.NewClient(conn, smtpHost)
	if err != nil {
		return smtpFailure(op, err, "the connection", notSent)
	}
	defer client.Close()
	if err := client.Hello("localhost"); err != nil {
		return smtpFailure(op, err, "the greeting", notSent)
	}
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return providerError(op, "Infomaniak Mail did not offer STARTTLS, so no credential was sent"+notSent)
	}
	if err := client.StartTLS(tlsCfg); err != nil {
		return smtpFailure(op, err, "STARTTLS", notSent)
	}
	ok, params := client.Extension("AUTH")
	if !ok {
		return providerError(op, "Infomaniak Mail offered no authentication"+notSent)
	}
	var auth smtp.Auth
	switch mechanisms := " " + strings.ToUpper(params) + " "; {
	case strings.Contains(mechanisms, " PLAIN "):
		auth = smtp.PlainAuth("", c.scope.mailbox, c.password, smtpHost)
	case strings.Contains(mechanisms, " LOGIN "):
		auth = loginAuth{user: c.scope.mailbox, password: c.password}
	default:
		return providerError(op, "Infomaniak Mail offers neither AUTH PLAIN nor AUTH LOGIN"+notSent)
	}
	if err := client.Auth(auth); err != nil {
		return smtpFailure(op, err, "the login", notSent)
	}
	if err := client.Mail(msg.from); err != nil {
		return smtpFailure(op, err, "the sender address", notSent)
	}
	for _, rcpt := range msg.rcpts {
		if err := client.Rcpt(rcpt); err != nil {
			return smtpFailure(op, err, "a recipient address", notSent)
		}
	}
	data, err := client.Data()
	if err != nil {
		return smtpFailure(op, err, "the message transfer", notSent)
	}
	if _, err := data.Write(msg.raw); err != nil {
		return smtpFailure(op, err, "the message transfer", notSent)
	}
	// From here on the terminating dot may have reached the server.
	if err := data.Close(); err != nil {
		var reply *textproto.Error
		if errors.As(err, &reply) && reply.Code >= 400 && reply.Code < 600 {
			if reply.Code == 421 {
				return smtpFailure(op, err, "the message", maybeSent)
			}
			return &provider.Error{Class: provider.ClassProviderError, Op: op,
				Message: "Infomaniak Mail refused the message after receiving it; the message was not sent"}
		}
		return smtpFailure(op, err, "the message", maybeSent)
	}
	_ = client.Quit()
	return nil
}
