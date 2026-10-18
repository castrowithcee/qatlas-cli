package infomaniakmail

// renamedTools maps former tool IDs to their current IDs.
var renamedTools = map[string]string{
	"infomaniakmail.attachments.get":  "infomaniak.mail.attachments.get",
	"infomaniakmail.drafts.create":    "infomaniak.mail.drafts.create",
	"infomaniakmail.drafts.delete":    "infomaniak.mail.drafts.delete",
	"infomaniakmail.drafts.send":      "infomaniak.mail.drafts.send",
	"infomaniakmail.drafts.update":    "infomaniak.mail.drafts.update",
	"infomaniakmail.folders.list":     "infomaniak.mail.folders.list",
	"infomaniakmail.messages.delete":  "infomaniak.mail.messages.delete",
	"infomaniakmail.messages.expunge": "infomaniak.mail.messages.expunge",
	"infomaniakmail.messages.flag":    "infomaniak.mail.messages.flag",
	"infomaniakmail.messages.get":     "infomaniak.mail.messages.get",
	"infomaniakmail.messages.list":    "infomaniak.mail.messages.list",
	"infomaniakmail.messages.move":    "infomaniak.mail.messages.move",
	"infomaniakmail.messages.send":    "infomaniak.mail.messages.send",
}
