package gen

// deferAccount is the reason for every account operation: the table is a raw scaffold, and the service package that
// curates the tools moves each operation it takes into operations or exclude.
const deferAccount = "not curated yet; the Account service package decides effect, confirmation and account target first"

// accountTable curates the account model. It starts as a scaffold: no operation is a tool yet.
var accountTable = Table{
	Service: "account", Segment: "account", Geltung: Global,
	Defer: []Omission{
		{Operation: "AcceptPrimaryEmailUpdate", Reason: deferAccount},
		{Operation: "DeleteAlternateContact", Reason: deferAccount},
		{Operation: "DisableRegion", Reason: deferAccount},
		{Operation: "EnableRegion", Reason: deferAccount},
		{Operation: "GetAccountInformation", Reason: deferAccount},
		{Operation: "GetAlternateContact", Reason: deferAccount},
		{Operation: "GetContactInformation", Reason: deferAccount},
		{Operation: "GetGovCloudAccountInformation", Reason: deferAccount},
		{Operation: "GetPrimaryEmail", Reason: deferAccount},
		{Operation: "GetPrimaryEmailUpdateStatus", Reason: deferAccount},
		{Operation: "GetRegionOptStatus", Reason: deferAccount},
		{Operation: "ListRegions", Reason: deferAccount},
		{Operation: "PutAccountName", Reason: deferAccount},
		{Operation: "PutAlternateContact", Reason: deferAccount},
		{Operation: "PutContactInformation", Reason: deferAccount},
		{Operation: "SendPhoneNumberVerification", Reason: deferAccount},
		{Operation: "StartPrimaryEmailUpdate", Reason: deferAccount},
		{Operation: "VerifyPhoneNumber", Reason: deferAccount},
	},
}
