---
description: >
  Describes the AWS provider: configuration, the credential and optional role assumption, the account and region targets, global services, endpoints, how Qatlas shows up in CloudTrail, error shapes, limits, how the tools are generated from pinned models and curation tables, and the tools of the Security Token Service group.
type: knowledge
edit: shared
created: 2026-11-06
updated: 2026-11-07
---

# Amazon Web Services

The `aws` provider gives agents controlled, read-only access to one AWS account. Qatlas signs requests with
Signature Version 4 using the Go standard library; it uses no AWS SDK. This first package ships the foundation
(credential, role assumption, account and region binding, connection test, endpoint table, error mapping) and the
three Security Token Service (STS) tools. Further services build on it.

## Configuration

A connection of this provider needs a credential with these secret roles and targets.

| Secret role | Required | Meaning |
| --- | --- | --- |
| `access_key_id` | yes | Access key ID of an IAM user, or of temporary credentials (`AKIA...`, `ASIA...`). |
| `secret_access_key` | yes | The secret access key that belongs to it. |
| `session_token` | no | Session token of temporary credentials. Leave it unset for a long-term IAM user key. |
| `role_arn` | no | ARN of an IAM role of the bound account to assume, `arn:aws:iam::<account>:role/<name>`. Not a secret; it is a role because the configuration model keeps no other per-credential setting. |
| `external_id` | no | External ID the role's trust policy requires. Treated as a secret. Only valid together with `role_arn`. |

Every value is registered with the redactor. No tool, argument, result, error text, or log line carries a
credential. The service has no base URL of your own: leave `base_url` out, since every endpoint comes from a fixed
table and any other value is refused.

Use an IAM identity with the least permissions the tools you enable need. The STS tools in this package need no
permission except `sts:DecodeAuthorizationMessage` for the decode tool.

## Role assumption

With `role_arn` set, Qatlas assumes the role itself with `sts:AssumeRole`, using the access key. It requests a
session of 3600 seconds, which is the default maximum of every role and the limit for role chaining, so it is never
above what the role allows; a higher maximum duration configured on the role is not used. The session lives in
memory only. It is reused until five minutes before it expires and then replaced. One session is held per
credential set and region, because it is fetched from the regional STS endpoint of the region of the call. Each
new session is checked against the bound account again.

The session credentials are internal. They are never a tool, an argument, a result, a log line, or part of an error
message. `AssumeRole` is not a tool, and neither is any other STS action that returns a credential or a token
(`AssumeRoleWithSAML`, `AssumeRoleWithWebIdentity`, `AssumeRoot`, `GetDelegatedAccessToken`, `GetFederationToken`,
`GetSessionToken`, `GetWebIdentityToken`).

The role must live in the bound account: a `role_arn` of another account is refused before any request. Role
ARNs of the partitions `aws-cn` and `aws-us-gov` are refused.

## Targets

A connection binds exactly one account and a list of regions.

- `account/<12 digits>`: required, exactly one. Global services hang on it.
- `region/<name>`: required, at least one, at most 40, no repeats. The list is an allowlist. Only regions of the
  `aws` partition are accepted; `cn-*`, `us-gov-*` and the isolated partitions are refused.

Several accounts mean several connections. A regional tool takes an optional `region` argument. It may be left out
when the connection lists exactly one region; otherwise it must name one of the allowed regions. A region outside
the list is refused before any secret is read or any request is built.

### Account check

Before every call Qatlas proves with `sts:GetCallerIdentity` that the credential belongs to the bound account,
and refuses the call otherwise. With a role, the account of the assumed session counts. The refusal names no
account ID. The proof is remembered in memory for each credential set (access key ID, a digest of its secret and
token, and the bound account), because an access key belongs to one account for good, so the second request is paid
once and not on every call. Only a match is remembered; a refusal is checked again on the next call. The tool
`aws.sts.caller-identity.get` always asks AWS afresh and is its own proof.

## Global services and endpoints

Qatlas resolves the host of every request from a fixed table, never from an argument or from the configuration, and
sends only over HTTPS without following redirects.

| Service | Host | Signing region |
| --- | --- | --- |
| STS | `sts.<region>.amazonaws.com` | the region |
| IAM (global) | `iam.amazonaws.com` | `us-east-1` |
| Amazon S3 | `s3.<region>.amazonaws.com` | the region |
| SES v2 | `email.<region>.amazonaws.com` | the region |
| Route 53 (global) | `route53.amazonaws.com` | `us-east-1` |
| Account (global) | `account.us-east-1.amazonaws.com` | `us-east-1` |

Global services hang on the account and ignore the region allowlist; a connection reaches them with its account
target alone. Only STS is used in this package.

## CloudTrail

Every request carries `User-Agent: qatlas/<version>`. A role session is named `qatlas-<hash>`, where the hash is
the first 16 hex digits of the SHA-256 of the connection name; it is no secret and does not reveal the name. In
CloudTrail, look for the user agent, or for the assumed-role session name.

## Connection test

The test assumes the role if one is configured, calls `sts:GetCallerIdentity` at the STS endpoint of the first
listed region, and checks that the account is the bound one. It is free and needs no permission. **It proves the
credential and the account only.** It proves neither the permissions for any other service nor that the other
listed regions are enabled for the account.

A credential of another account is reported as a test error that says so, since that refusal has no class of its
own. Every other failure is reported by its class.

## Errors

AWS answers `awsQuery` calls with `<ErrorResponse><Error><Type><Code><Message>`. Qatlas keeps the code and type
tokens and never shows the provider's `Message`.

| Answer | Class |
| --- | --- |
| `Throttling`, `ThrottlingException`, `RequestLimitExceeded`, HTTP 429 | `rate-limited` |
| `AccessDenied`, HTTP 403 | `permission` |
| `InvalidClientTokenId`, `SignatureDoesNotMatch`, `ExpiredToken`, HTTP 401 | `auth` |
| HTTP 5xx | `provider-error` |
| unreadable or oversized body | `invalid-provider-response` |
| deadline | `timeout` |

Reads are repeated automatically, at most twice: a rate limit only when AWS sends a `Retry-After` of at most ten
seconds, and a server failure once more after a short pause. A rate limit without `Retry-After` is reported and not
repeated. A call has 50 seconds in total, each request 15 seconds. Requests are spaced by 250 ms per connection and
service.

## Limits

- No live test has been run against AWS. Behaviour for roles that require MFA, role chaining, and the role
  maximum is documented by AWS only and has not been verified.
- A role that requires multi-factor authentication cannot be assumed. AWS answers it with the same `AccessDenied` as
  a wrong trust policy, so the message names both causes.
- Role chaining (an access key that itself carries a session token and assumes a role) is limited to one hour by
  AWS; the requested 3600 seconds fit.
- Identity Center, Roles Anywhere, and `credential_process` are not supported. Partitions `aws-cn` and
  `aws-us-gov` are not supported.
- Everything AWS returns, including the decoded authorization message, is untrusted data.

## How the tools are built

Every AWS tool is generated from the AWS service model and a curation table, not written by hand. Each tool is one
fixed model operation; there is no general call.

- **Model pin.** The models come from the AWS repository `api-models-aws` at one pinned commit. The repository keeps
  an extract of each model, not the upstream file: the documentation and example traits are removed, nothing else
  changes. The pin file beside the extracts names the commit, the day the files were retrieved, and the SHA-256 of
  every upstream file and every extract. The upstream `LICENSE` and `NOTICE` (Apache-2.0) lie beside them. A test
  checks the extracts against the pin file without network access.
- **Curation table.** One table per service decides which operations become tools and how: the tool ID, effect,
  confirmation, `listed-only`, idempotency, title and descriptions, renames for readability, members hidden for a
  security reason, and the target check and client-side policy check that run before the call. Every operation that
  is not a read requires confirmation.
- **Completeness.** Every operation of a model is a tool, in `exclude`, or in `defer`, each with a reason; a test
  fails otherwise, so a new operation of an updated model never becomes a tool or disappears silently. `exclude`
  means never a tool, such as every operation that returns a credential; a test proves that none of them is one.
  `defer` means not yet.
- **`listed-only`.** Such a tool is offered only by a connection whose tools list names it
  (`requires_tool_allow_list`).
- **From the model.** Arguments, fields and their limits come from the model. Deprecated members are left out. A
  member the model marks sensitive, or whose name says it holds a credential, stops the generation unless the table
  hides it. A paged operation takes `page_size` (where the service has a page size) and `cursor` and returns
  `next_cursor` and `has_more`. An operation with an idempotency token generates the token, accepts `client_token`
  to repeat a request after a timeout, and returns the token it sent.

A model update is a deliberate change. Set the commit, the retrieval day and the upstream hashes in
`internal/provider/aws/gen/model/pin.json`, download the upstream files into a directory laid out like
`api-models-aws`, and run `go run ./gen/awsgen extract <directory>` in `internal/provider/aws`; it refuses a file
whose hash differs and writes the extracts and their hashes. Then run `go generate ./internal/provider/aws/...` and
commit extracts, pin file and generated files together. A test fails when the generated files do not match the
extracts and the tables.

## Tools

Group `sts`. All tools are reads and need no confirmation. The recommended profile `read` selects all three.
`region` is optional with exactly one allowed region.

| Tool | AWS action | Arguments | Result |
| --- | --- | --- | --- |
| `aws.sts.caller-identity.get` | `GetCallerIdentity` | `region` | `region`, `user_id`, `account`, `arn` |
| `aws.sts.access-key.get-info` | `GetAccessKeyInfo` | `access_key_id` (required), `region` | `region`, `account` |
| `aws.sts.authorization-message.decode` | `DecodeAuthorizationMessage` | `encoded_message` (required, up to 10240 characters), `region` | `region`, `decoded_message`, `truncated` |

`access_key_id` is an argument, not a secret of the connection: it names the key to look up, which may be a foreign
one, and the answer is its account. The decoded message is cut at 32 KiB, and `truncated` says so. It needs the
permission `sts:DecodeAuthorizationMessage`.
