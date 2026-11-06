// Package aws implements controlled access to one Amazon Web Services account through the AWS REST APIs,
// signed with Signature Version 4 and the standard library alone. This package holds the provider: its
// metadata, the credential and the role assumption, the account and region binding, the connection test,
// and the Security Token Service tools. The signature, the endpoint and segment tables, the error mapping,
// and the awsQuery protocol are packages of their own below it, shared by every service that follows.
//
// A connection binds one access key to exactly one account and a non-empty allowlist of regions. Before
// every call the broker proves with GetCallerIdentity that the credential belongs to the bound account, and
// it refuses the call otherwise. With a configured role it assumes the role itself, keeps the session in
// memory until shortly before it expires, and never shows it: no tool, argument, result, log line, or error
// carries a credential of the connection or of its session.
//
// No tool takes a service, an action, a host, a path, a header, or a body: every tool is one fixed AWS
// operation, and every host comes from the endpoint table. Everything AWS sends back is untrusted data.
package aws

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "aws"

// dataSensitivity classifies results as account and identity data of the configured AWS account.
const dataSensitivity = "aws-account-identity"

// consoleOrigin is the only base URL of an AWS service. It is documentary: the endpoints come from a fixed
// table and no request is ever sent to it.
const consoleOrigin = "https://aws.amazon.com"

func validBaseURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host != "aws.amazon.com" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		return errors.New("an AWS service takes no base URL of its own; leave base_url out, since every " +
			"endpoint comes from a fixed table")
	}
	return nil
}

var toolGroups = []config.ToolGroup{{
	ID: groupSTS, Title: "Security Token Service",
	Description: "Identity of the calling credential, the account of an access key, and decoded authorization failures",
}}

var targetKinds = []config.TargetKind{{
	Name: kindAccount, Unlisted: true, Forms: []string{formAccount},
	Description: "the one account this connection is bound to, as a 12-digit ID; required, exactly one. Global " +
		"services hang on it",
}, {
	Name: kindRegion, Unlisted: true, Forms: []string{formRegion},
	Description: "a region of the aws partition the connection may call, such as eu-central-1; required, at " +
		"least one, and may be listed more than once. Regions of aws-cn and aws-us-gov are not supported",
}}

// TestConnection runs the smallest safe authenticated read: the role assumption when a role is configured,
// then GetCallerIdentity, whose account must be the bound one. It proves the credential and the account
// only. It does not prove permissions for any other service and does not prove that the other regions of
// the allowlist are enabled for the account; it calls the first one.
func TestConnection(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (provider.Class, error) {
	const op = "test connection"
	ctx, cancel := context.WithTimeout(ctx, invokeBudget)
	defer cancel()
	client, err := open(ctx, resolved, secrets, red)
	if err != nil {
		return testClass(err)
	}
	region := client.binding.regions[0]
	creds, err := client.effective(ctx, region)
	if err != nil {
		return testClass(err)
	}
	if _, failure := client.identity(ctx, op, region, creds); failure != nil {
		return testClass(failure)
	}
	return provider.ClassOK, nil
}

// testClass turns a provider failure into the class of a connection test. The refusal of another account
// has no class of its own and its explanation would be the generic one, so it is returned as an error that
// says what happened. Any non-provider error is returned as well.
func testClass(err error) (provider.Class, error) {
	var failed *provider.Error
	if errors.As(err, &failed) && failed.Message != mismatchMessage {
		return failed.Class, nil
	}
	return "", err
}

// Register adds the provider metadata, its connection test, and its tools. version is the build version of
// Qatlas; it becomes the User-Agent of every AWS request.
func Register(reg *capability.Registry, version string) error {
	userAgent = agentFor(version)
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Amazon Web Services", DefaultBaseURL: consoleOrigin, ValidateBaseURL: validBaseURL,
		Description:    "Amazon Web Services account: read-only identity checks through STS, bound to one account and its regions",
		DefaultEffects: []config.Effect{config.EffectRead},
		Groups:         toolGroups, ServiceLevel: true, DocPage: "aws.md",
		SecretRoles: []config.SecretRole{{
			Name: roleAccessKeyID,
			Description: "Access key ID of an IAM user or of temporary credentials (AKIA... or ASIA...). Use a key " +
				"with the least permissions the tools need",
		}, {
			Name:        roleSecretAccessKey,
			Description: "Secret access key that belongs to the access key ID",
		}, {
			Name:        roleSessionToken,
			Description: "Session token of temporary credentials; leave it empty for the long-term key of an IAM user",
			Optional:    true,
		}, {
			Name: roleRoleARN,
			Description: "ARN of an IAM role of the bound account to assume with the access key, such as " +
				"arn:aws:iam::123456789012:role/Name; when set the broker assumes the role itself and signs " +
				"with its session. Roles that require multi-factor authentication are not supported. Not a secret",
			Optional: true,
		}, {
			Name: roleExternalID,
			Description: "External ID the role's trust policy requires, sent with the role assumption; treated " +
				"as a secret. Only with role_arn",
			Optional: true,
		}},
		Target: config.TargetMetadata{
			Label:    "targets",
			Required: true,
			Multiple: true,
			Description: "exactly one account as account/ACCOUNT_ID and at least one region as region/NAME; " +
				"every call is checked against the account and a regional tool only calls an allowed region",
			Kinds:       targetKinds,
			Validate:    validateTarget,
			ValidateSet: validateSet,
			Reaches:     reaches,
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Identity (STS)", Recommended: true,
			Description: "reads the identity of the credential, the account of an access key, and decoded " +
				"authorization failures; changes nothing",
			Tools: []string{callerIdentityGet.ID, accessKeyGetInfo.ID, authorizationMessageDecode.ID},
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider, stsTools()...)
}
