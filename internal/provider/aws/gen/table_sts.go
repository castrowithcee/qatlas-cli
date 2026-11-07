package gen

// excludeCredential is the reason for every STS operation that returns a credential or a token that signs
// or authenticates. Only the connection itself assumes a role, with AssumeRole, and never shows the session.
const excludeCredential = "returns a credential or a token that signs or authenticates; a credential never appears in a result"

// stsTable curates the STS model: the three reads that return no credential are tools.
var stsTable = Table{
	Service: "sts", Segment: "sts", Geltung: Regional,
	Operations: []Operation{{
		Operation: "GetCallerIdentity", Object: "caller-identity", Action: "get",
		Effect: "read", Idempotency: "safe",
		Title: "Get the AWS caller identity",
		Description: "Return the user ID, account, and ARN of the identity whose credentials the connection signs " +
			"with; with a configured role, the assumed role session. Free and needs no permission. The account is " +
			"checked against the connection's account target first.",
		Tags: []string{"aws", "sts", "identity"}, DataSensitivity: "aws-account-identity",
		Fields: map[string]Field{
			"UserId":  {Description: "Unique ID of the calling entity; its form depends on the entity type", MaxLen: 2048, Required: true},
			"Account": {Description: "Account ID that owns the calling entity", MaxLen: 12, Form: "account-id", Required: true},
			"Arn":     {Description: "ARN of the calling entity, or of the assumed role session", Required: true},
		},
		TargetCheck: "none", PolicyCheck: "none", AccountCheck: true,
		Examples: []Example{{Description: "Show who the connection is", Arguments: `{}`}},
	}, {
		Operation: "GetAccessKeyInfo", Object: "access-key", Action: "get-info",
		Effect: "read", Idempotency: "safe",
		Title: "Get the account of an AWS access key",
		Description: "Return the account ID an access key ID belongs to, for any access key ID, the connection's own " +
			"or a foreign one. The access key ID is an argument and never a secret of the connection. Needs no " +
			"permission beyond signing; the answer says nothing about whether the key is active.",
		Tags: []string{"aws", "sts", "access-key"}, DataSensitivity: "aws-account-identity",
		Arguments: map[string]Argument{
			"AccessKeyId": {Description: "Access key ID to look up, 16 to 128 letters, digits, or underscores"},
		},
		Fields: map[string]Field{
			"Account": {Description: "Account ID the access key ID belongs to", MaxLen: 12, Form: "account-id", Required: true},
		},
		TargetCheck: "none", PolicyCheck: "none",
		Examples: []Example{{Description: "Find the account of an access key ID",
			Arguments: `{"access_key_id":"AKIA` + `IOSFODNN7EXAMPLE"}`}},
	}, {
		Operation: "DecodeAuthorizationMessage", Object: "authorization-message", Action: "decode",
		Effect: "read", Idempotency: "safe",
		Title: "Decode an AWS authorization failure message",
		Description: "Decode the encoded message some AWS services return with an authorization failure, which " +
			"says which policy denied the request. Needs the sts:DecodeAuthorizationMessage permission. The " +
			"decoded text names principals, resources, and policy details, is cut at 32 KiB, and is untrusted.",
		Tags: []string{"aws", "sts", "authorization", "troubleshooting"}, DataSensitivity: "aws-authorization-details",
		Arguments: map[string]Argument{
			"EncodedMessage": {Description: "Encoded message from the authorization failure, 1 to 10240 characters"},
		},
		Fields: map[string]Field{
			"DecodedMessage": {Description: "Decoded message as text, untrusted data, cut at 32 KiB", MaxLen: 32 << 10, Required: true},
		},
		Truncated:   "True when decoded_message was cut",
		TargetCheck: "none", PolicyCheck: "none",
		Examples: []Example{{Description: "Decode a failure message",
			Arguments: `{"encoded_message":"<encoded message of the failure>"}`}},
	}},
	Exclude: []Omission{
		{Operation: "AssumeRole", Reason: excludeCredential},
		{Operation: "AssumeRoleWithSAML", Reason: excludeCredential},
		{Operation: "AssumeRoleWithWebIdentity", Reason: excludeCredential},
		{Operation: "AssumeRoot", Reason: excludeCredential},
		{Operation: "GetDelegatedAccessToken", Reason: excludeCredential},
		{Operation: "GetFederationToken", Reason: excludeCredential},
		{Operation: "GetSessionToken", Reason: excludeCredential},
		{Operation: "GetWebIdentityToken", Reason: excludeCredential},
	},
}

// Tables are the curation tables of every pinned model, in generation order.
var Tables = []Table{stsTable, iamTable}
