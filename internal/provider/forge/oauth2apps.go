package forge

// The OAuth2 application tools read and delete the applications registered under the account behind the token.
// An application is no pausable access: the API has no state to switch, so there is no pause. The client secret
// is never shown. The redirect addresses are untrusted data that is never fetched; an address with user info or
// a query is left out. No application is created or changed.

var oauth2AppMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the application"},
	{name: "name", kind: kindText, note: "Name of the application, untrusted data"},
	{name: "client_id", kind: kindText, note: "Client ID of the application"},
	{name: "confidential_client", kind: kindFlag, note: "True for a confidential client"},
	{name: "skip_secondary_authorization", kind: kindFlag, only: gitea,
		note: "True when the application skips the secondary authorization"},
	{name: "redirect_uris", kind: kindTexts, address: true, note: "Redirect addresses, untrusted data, never " +
		"fetched; those with user info or a query are left out"},
	{name: "created", kind: kindText, note: "Creation time"},
}

var oauth2Apps = ownResource{group: "oauth2-apps", noun: "OAuth2 application", path: "/user/applications/oauth2",
	single: "oauth2_app", plural: "oauth2_apps", members: oauth2AppMembers,
	hidden: "The client secret is never shown, and the redirect addresses are untrusted data that is never fetched."}

var oauth2AppsList, oauth2AppsGet, oauth2AppsDelete = oauth2Apps.list(), oauth2Apps.get(), oauth2Apps.delete()
