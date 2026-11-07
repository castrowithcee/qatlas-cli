package aws

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/sigv4"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// The secret roles of a credential. role_arn and external_id are roles although role_arn is no secret: the
// configuration model keeps no other per-credential setting, and a role may stay empty (SecretRole.Optional).
const (
	roleAccessKeyID     = "access_key_id"
	roleSecretAccessKey = "secret_access_key"
	roleSessionToken    = "session_token"
	roleRoleARN         = "role_arn"
	roleExternalID      = "external_id"
)

// Forms the roles must have before any request is built, so an obviously unusable value never reaches AWS.
// They follow the STS model: access key IDs are [\w]{16,128}, external IDs [\w+=,.@:/-]{2,1224}.
var (
	accessKeyIDForm = regexp.MustCompile(`^[A-Za-z0-9_]{16,128}$`)
	// RE2 repeats at most 1000 times, so the 2 to 1224 length is checked apart from the characters.
	externalIDForm = regexp.MustCompile(`^[A-Za-z0-9_+=,.@:/-]+$`)
	// roleARNForm accepts IAM role ARNs of the aws partition only; aws-cn and aws-us-gov are not supported.
	roleARNForm = regexp.MustCompile(`^arn:aws:iam::([0-9]{12}):role/[A-Za-z0-9_+=,.@/-]{1,576}$`)
)

// credentials are the values of one connection's credential: the access key that signs and, when roleARN is
// set, the role the broker assumes with it.
type credentials struct {
	key        sigv4.Credentials
	roleARN    string
	externalID string
}

// roleAccount is the account the role ARN names, or empty without a role.
func (c credentials) roleAccount() string {
	if m := roleARNForm.FindStringSubmatch(c.roleARN); m != nil {
		return m[1]
	}
	return ""
}

// resolveCredentials reads the roles of the connection's credential and registers every value with the
// redactor. The required roles come first; the optional ones are absent without error only when every stage
// of the secret cascade answered that it holds nothing.
func resolveCredentials(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (credentials, error) {
	const op = "open"
	if secrets == nil {
		return credentials{}, provider.Fail(op, "no credential resolver was configured")
	}
	var c credentials
	required := func(role string) (string, error) {
		value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, role)
		return value.Secret, err
	}
	optional := func(role string) (string, error) {
		value, found, err := secrets.ResolveOptional(ctx, resolved.Credential, resolved.Secrets, role)
		if err != nil || !found {
			return "", err
		}
		return value.Secret, nil
	}

	var err error
	if c.key.AccessKeyID, err = required(roleAccessKeyID); err != nil {
		return credentials{}, err
	}
	if c.key.SecretAccessKey, err = required(roleSecretAccessKey); err != nil {
		return credentials{}, err
	}
	if c.key.SessionToken, err = optional(roleSessionToken); err != nil {
		return credentials{}, err
	}
	if c.roleARN, err = optional(roleRoleARN); err != nil {
		return credentials{}, err
	}
	if c.externalID, err = optional(roleExternalID); err != nil {
		return credentials{}, err
	}
	if red != nil {
		red.Add(c.key.AccessKeyID, c.key.SecretAccessKey, c.key.SessionToken, c.externalID)
	}
	if err := c.check(); err != nil {
		return credentials{}, &provider.Error{Class: provider.ClassAuth, Op: op, Message: err.Error()}
	}
	return c, nil
}

// check refuses a credential whose values cannot be right. No message quotes a value.
func (c credentials) check() error {
	switch {
	case !accessKeyIDForm.MatchString(c.key.AccessKeyID):
		return errors.New("the AWS access key ID is unusable")
	case !printable(c.key.SecretAccessKey, 16, 256):
		return errors.New("the AWS secret access key is unusable")
	case c.key.SessionToken != "" && !provider.ValidHeaderToken(c.key.SessionToken):
		return errors.New("the AWS session token is unusable")
	case c.roleARN == "" && c.externalID != "":
		return errors.New("the AWS external ID is set but no role ARN is; set role_arn or remove external_id")
	case c.roleARN != "" && !roleARNForm.MatchString(c.roleARN):
		return errors.New("the AWS role ARN must be an IAM role ARN of the aws partition")
	case c.externalID != "" && (len(c.externalID) < 2 || len(c.externalID) > 1224 || !externalIDForm.MatchString(c.externalID)):
		return errors.New("the AWS external ID is unusable")
	}
	return nil
}

// printable reports whether value is printable ASCII without blanks and between min and max bytes long.
func printable(value string, min, max int) bool {
	if len(value) < min || len(value) > max {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

// fingerprint identifies a set of values without keeping them: the SHA-256 of the length-prefixed parts. It
// is the key of the in-memory session and account caches, so neither holds a second copy of a secret.
func fingerprint(parts ...string) string {
	h := sha256.New()
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		h.Write(length[:])
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// sessionName is the RoleSessionName of a connection: a fixed prefix and a hash of the connection name,
// within the characters AWS allows. It is no secret and shows up in CloudTrail as the session of this
// connection without revealing its name.
func sessionName(connection string) string {
	sum := sha256.Sum256([]byte(connection))
	return "qatlas-" + hex.EncodeToString(sum[:8])
}

// agentFor builds the User-Agent of every AWS request from the build version. Anything outside the
// characters of a version is dropped, so a version string can never reach a header unchecked.
func agentFor(version string) string {
	var b strings.Builder
	for _, r := range version {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._+-", r) {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 || b.Len() > 64 {
		return "qatlas/unknown"
	}
	return "qatlas/" + b.String()
}
