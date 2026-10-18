package n8n

// renamedTools maps former tool IDs to their current IDs.
var renamedTools = map[string]string{
	"n8n.datacolumns.add":        "n8n.data-columns.add",
	"n8n.datacolumns.delete":     "n8n.data-columns.delete",
	"n8n.datacolumns.list":       "n8n.data-columns.list",
	"n8n.datacolumns.update":     "n8n.data-columns.update",
	"n8n.datarows.delete":        "n8n.data-rows.delete",
	"n8n.datarows.insert":        "n8n.data-rows.insert",
	"n8n.datarows.list":          "n8n.data-rows.list",
	"n8n.datarows.update":        "n8n.data-rows.update",
	"n8n.datarows.upsert":        "n8n.data-rows.upsert",
	"n8n.datatables.create":      "n8n.data-tables.create",
	"n8n.datatables.delete":      "n8n.data-tables.delete",
	"n8n.datatables.get":         "n8n.data-tables.get",
	"n8n.datatables.list":        "n8n.data-tables.list",
	"n8n.datatables.rename":      "n8n.data-tables.rename",
	"n8n.projectmembers.add":     "n8n.project-members.add",
	"n8n.projectmembers.list":    "n8n.project-members.list",
	"n8n.projectmembers.remove":  "n8n.project-members.remove",
	"n8n.projectmembers.setrole": "n8n.project-members.set-role",
	"n8n.sourcecontrol.pull":     "n8n.source-control.pull",
	"n8n.sourcecontrol.push":     "n8n.source-control.push",
	"n8n.sourcecontrol.status":   "n8n.source-control.status",
	"n8n.users.setrole":          "n8n.users.set-role",
}
