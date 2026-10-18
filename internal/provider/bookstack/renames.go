package bookstack

// renamedTools maps former tool IDs to their current IDs.
var renamedTools = map[string]string{
	"bookstack.auditlog.list":             "bookstack.audit-log.list",
	"bookstack.books.setcover":            "bookstack.books.set-cover",
	"bookstack.contentpermissions.get":    "bookstack.content-permissions.get",
	"bookstack.contentpermissions.update": "bookstack.content-permissions.update",
	"bookstack.recyclebin.destroy":        "bookstack.recycle-bin.destroy",
	"bookstack.recyclebin.list":           "bookstack.recycle-bin.list",
	"bookstack.recyclebin.restore":        "bookstack.recycle-bin.restore",
	"bookstack.shelves.setcover":          "bookstack.shelves.set-cover",
}
