package cli

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// nextcloudConfig configures two Nextcloud instances, two identities of the first one, and one identity
// bound to two root folders, next to a BookStack connection. No instance is ever contacted here: every
// request in this file is refused by the core before any provider I/O, so the tests need no server.
func nextcloudConfig(t *testing.T) string {
	t.Helper()
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	return writeConfig(t, `version: 2
services:
  cloud-main:
    provider: nextcloud
    base_url: https://cloud.example.invalid
  cloud-partner:
    provider: nextcloud
    base_url: https://partner.example.invalid/nextcloud
  wiki:
    provider: bookstack
    base_url: https://wiki.example.invalid
credentials:
  cloud-reader:
    type: env
    values:
      user-id: NEXTCLOUD_READER_USER
      app-password: NEXTCLOUD_READER_PASSWORD
  cloud-auditor:
    type: env
    values:
      user-id: NEXTCLOUD_AUDITOR_USER
      app-password: NEXTCLOUD_AUDITOR_PASSWORD
  partner-reader:
    type: env
    values:
      user-id: NEXTCLOUD_PARTNER_USER
      app-password: NEXTCLOUD_PARTNER_PASSWORD
  reader:
    type: env
    values:
      token-id: WIKI_TOKEN_ID
      token-secret: WIKI_TOKEN_SECRET
connections:
  files-reports:
    service: cloud-main
    credential: cloud-reader
    target: Reports
  files-archive:
    service: cloud-main
    credential: cloud-reader
    target: Archive/2026
  files-audit:
    service: cloud-main
    credential: cloud-auditor
    target: Audit
  files-partner:
    service: cloud-partner
    credential: partner-reader
    target: Shared/Qatlas
  wiki:
    service: wiki
    credential: reader
defaults: {}
`)
}

// runNextcloudCLI drives the real command tree and the shipped registry with a resolver that counts
// secret lookups, so a run can prove it resolved no app password.
func runNextcloudCLI(t *testing.T, reads *atomic.Int32, input string, args ...string) (int, string, string) {
	t.Helper()
	redactor := &redact.Redactor{}
	lookup := func(string) string {
		if reads != nil {
			reads.Add(1)
		}
		return ""
	}
	var stdout, stderr bytes.Buffer
	options := &Options{
		Input: strings.NewReader(input), Redactor: redactor,
		Secrets: secret.NewWith(lookup, nil, nil, redactor),
	}
	code := run(newRootCommand(options, defaultRegistry()), options, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// The Nextcloud namespace publishes all its tools, with the connections that can
// run them and without contacting an instance.
func TestNextcloudToolsAreDiscoverable(t *testing.T) {
	path := nextcloudConfig(t)
	var reads atomic.Int32

	code, stdout, stderr := runNextcloudCLI(t, &reads, "", "tools", "nextcloud", "--all", "--query", "nextcloud", "--config", path)
	if code != exitOK || stderr != "" {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	for _, want := range []string{
		"tools[144]{id,title,effect,requires,confirm,reason}:", "nextcloud.account.activity.list,List Nextcloud activity,read,", "nextcloud.account.account.get,Get Nextcloud account data,read,", "nextcloud.account.status.get,Get the Nextcloud status,read,", "nextcloud.account.status.set,Set the Nextcloud status,update,\"\",true,", "nextcloud.account.status.clear,Clear the Nextcloud status message,update,\"\",true,", "nextcloud.account.notifications.list,List Nextcloud notifications,read,", "nextcloud.account.notifications.dismiss,Dismiss a Nextcloud notification,delete,\"notification_id:an id of account.notifications.list\",true,", "nextcloud.account.notifications.dismiss-all,Dismiss all Nextcloud notifications,delete,", "nextcloud.calendar.calendars.create,Create a Nextcloud calendar,create,", "nextcloud.calendar.calendars.update,Change a Nextcloud calendar,update,", "nextcloud.calendar.calendars.delete,Delete a Nextcloud calendar,delete,", "nextcloud.calendar.calendars.share,Share a Nextcloud calendar with a user or group,update,", "nextcloud.calendar.calendars.unshare,Revoke the share of a Nextcloud calendar,delete,", "nextcloud.contacts.address-books.share,Share a Nextcloud address book with a user or group,update,", "nextcloud.contacts.address-books.unshare,Revoke the share of a Nextcloud address book,delete,", "nextcloud.calendar.trash.list,List the Nextcloud calendar trash bin,read,", "nextcloud.calendar.trash.restore,Restore a Nextcloud calendar trash entry,update,id,true,", "nextcloud.calendar.trash.delete,Delete a Nextcloud calendar trash entry for good,delete,id,true,", "nextcloud.calendar.events.create,Create a Nextcloud calendar event,create,", "nextcloud.calendar.events.update,Replace a Nextcloud calendar event,update,", "nextcloud.calendar.events.delete,Delete a Nextcloud calendar event,delete,", "nextcloud.contacts.address-books.create,Create a Nextcloud address book,create,", "nextcloud.contacts.address-books.update,Change a Nextcloud address book,update,", "nextcloud.contacts.address-books.delete,Delete a Nextcloud address book,delete,", "nextcloud.contacts.address-books.list,List Nextcloud address books,read,", "nextcloud.contacts.contacts.list,List Nextcloud contacts,read,addressbook,", "nextcloud.contacts.contacts.get,Get a Nextcloud contact,read,addressbook; id,", "nextcloud.contacts.contacts.create,Create a Nextcloud contact,create,addressbook; name,true,", "nextcloud.contacts.contacts.update,Replace a Nextcloud contact,update,addressbook; id; etag; name,true,", "nextcloud.contacts.contacts.delete,Delete a Nextcloud contact,delete,addressbook; id; etag,true,", "nextcloud.calendar.calendars.list,List Nextcloud calendars,read,", "nextcloud.calendar.events.get,Get a Nextcloud calendar event,read,", "nextcloud.calendar.tasks.get,Get a Nextcloud calendar task,read,", "nextcloud.calendar.tasks.list,List Nextcloud calendar tasks,read,", "nextcloud.calendar.tasks.create,Create a Nextcloud calendar task,create,", "nextcloud.calendar.tasks.update,Replace a Nextcloud calendar task,update,", "nextcloud.calendar.tasks.delete,Delete a Nextcloud calendar task,delete,", "nextcloud.calendar.events.list,List Nextcloud calendar events,read,", "nextcloud.deck.boards.create,Create a Nextcloud Deck board,create,", "nextcloud.deck.boards.update,Change a Nextcloud Deck board,update,", "nextcloud.deck.boards.delete,Delete a Nextcloud Deck board,delete,", "nextcloud.files.comments.list,List the comments of a Nextcloud file,read,", "nextcloud.files.comments.create,Comment on a Nextcloud file,create,path; message,true,", "nextcloud.files.comments.update,Change a Nextcloud file comment,update,", "nextcloud.files.comments.delete,Delete a Nextcloud file comment,delete,", "nextcloud.deck.boards.get,Get a Nextcloud Deck board,read,", "nextcloud.deck.boards.list,List Nextcloud Deck boards,read,", "nextcloud.deck.cards.get,Get a Nextcloud Deck card,read,", "nextcloud.deck.cards.labels,Assign or remove a Nextcloud Deck card label,update,", "nextcloud.deck.cards.assignees,Assign or unassign a Nextcloud Deck card member,update,", "nextcloud.deck.attachments.list,List Nextcloud Deck card attachments,read,", "nextcloud.deck.attachments.get,Read or download a Nextcloud Deck card attachment,read,", "nextcloud.deck.stacks.list,List Nextcloud Deck stacks,read,", "nextcloud.files.favorites.list,List Nextcloud favorites,read,", "nextcloud.files.files.favorite,Mark or unmark a Nextcloud favorite,update,path; favorite,true,", "nextcloud.files.files.search,Search Nextcloud files,read,", "nextcloud.files.files.copy,Copy a Nextcloud file or folder,create,", "nextcloud.files.files.create,Create a Nextcloud file,create,", "nextcloud.files.files.delete,Delete a Nextcloud file,delete,", "nextcloud.files.files.get,Read or download a Nextcloud file,read,", "nextcloud.files.files.list,List Nextcloud files,read,", "nextcloud.files.files.move,Move or rename a Nextcloud file or folder,update,", "nextcloud.files.files.stat,Get Nextcloud file metadata,read,", "nextcloud.files.files.update,Update a Nextcloud file,update,", "nextcloud.files.folders.create,Create a Nextcloud folder,create,", "nextcloud.shares.incoming-shares.accept,Accept an internal Nextcloud share,update,share_id,true,", "nextcloud.shares.incoming-shares.decline,Decline a pending Nextcloud share,delete,\"share_id; origin:internal|federated\",true,", "nextcloud.shares.incoming-shares.pending,List pending incoming Nextcloud shares,read,", "nextcloud.notes.attachments.get,Read a Nextcloud note attachment,read,", "nextcloud.notes.attachments.upload,Upload a Nextcloud note attachment,create,\"id:a note id of notes.notes.list; local_path\",true,", "nextcloud.notes.notes.create,Create a Nextcloud note,create,title,true,", "nextcloud.notes.notes.update,Change a Nextcloud note,update,\"id:a note id of notes.notes.list; etag\",true,", "nextcloud.notes.notes.delete,Delete a Nextcloud note,delete,\"id:a note id of notes.notes.list\",true,", "nextcloud.notes.notes.get,Read a Nextcloud note,read,", "nextcloud.notes.notes.list,List Nextcloud notes,read,", "nextcloud.notes.settings.get,Get the Nextcloud Notes settings,read,", "nextcloud.shares.remote-shares.get,Get an accepted Nextcloud federated share,read,share_id,", "nextcloud.shares.remote-shares.list,List accepted Nextcloud federated shares,read,", "nextcloud.shares.remote-shares.remove,Remove an accepted Nextcloud federated share,delete,share_id,true,", "nextcloud.account.search.providers,List Nextcloud search providers,read,", "nextcloud.account.search.query,Search Nextcloud with one provider,read,", "nextcloud.shares.sharees.search,Search Nextcloud recipients,read,", "nextcloud.shares.shares.create,Share a Nextcloud item with a user or group,create,", "nextcloud.shares.shares.update,Change a Nextcloud share,update,share_id,true,", "nextcloud.shares.shares.delete,Revoke a Nextcloud share,delete,share_id,true,", "nextcloud.shares.shares.get,Get a Nextcloud share,read,", "nextcloud.shares.shares.list,List Nextcloud shares,read,", "nextcloud.files.trash.delete,Delete a Nextcloud trash item for good,delete,trash_id,true,", "nextcloud.files.trash.list,List the Nextcloud trash bin,read,", "nextcloud.files.trash.restore,Restore a Nextcloud trash item,update,trash_id,true,", "nextcloud.files.versions.get,Read or download a Nextcloud file version,read,", "nextcloud.files.versions.list,List Nextcloud file versions,read,", "nextcloud.files.versions.restore,Restore a Nextcloud file version,update,", "nextcloud.files.file-tags.add,Assign a Nextcloud system tag,update,\"path; tag_id:a tag_id of files.system-tags.list\",true,", "nextcloud.files.file-tags.list,List the system tags of a Nextcloud file,read,", "nextcloud.files.file-tags.remove,Remove a Nextcloud system tag from a file,update,\"path; tag_id:a tag_id of files.system-tags.list\",true,", "nextcloud.files.system-tags.list,List Nextcloud system tags,read,", "nextcloud.files.system-tags.create,Create a Nextcloud system tag,create,", "nextcloud.files.system-tags.update,Change a Nextcloud system tag,update,", "nextcloud.talk.files.share,Share a Nextcloud file in a Talk conversation,create,token; path,true,", "nextcloud.talk.messages.list,List Nextcloud Talk messages,read,token,", "nextcloud.talk.messages.send,Send a Nextcloud Talk message,create,token; message,true,", "nextcloud.talk.messages.edit,Edit a Nextcloud Talk message,update,token; message_id; message,true,", "nextcloud.talk.messages.delete,Delete a Nextcloud Talk message,delete,token; message_id,true,", "nextcloud.talk.reactions.set,React to a Nextcloud Talk message,update,token; message_id; reaction; add,true,", "nextcloud.talk.polls.get,Get a Nextcloud Talk poll,read,token; poll_id,", "nextcloud.talk.polls.create,Create a Nextcloud Talk poll,create,token; question; options,true,", "nextcloud.talk.polls.vote,Vote on a Nextcloud Talk poll,update,token; poll_id; option_ids,true,", "nextcloud.talk.polls.close,Close a Nextcloud Talk poll,delete,token; poll_id,true,", "nextcloud.talk.participants.list,List Nextcloud Talk participants,read,token,", "nextcloud.talk.participants.add,Add a Nextcloud Talk participant,update,\"token; participant; source:users|groups\",true,", "nextcloud.talk.participants.remove,Remove a Nextcloud Talk participant,delete,token; attendee_id,true,", "nextcloud.talk.participants.moderator,Set a Nextcloud Talk moderator,update,token; attendee_id; moderator,true,", "nextcloud.talk.rooms.get,Get a Nextcloud Talk conversation,read,token,", "nextcloud.talk.rooms.list,List Nextcloud Talk conversations,read,", "nextcloud.talk.rooms.create,Create a Nextcloud Talk conversation,create,\"kind:one-to-one|group\",true,", "nextcloud.talk.rooms.mark-read,Mark a Nextcloud Talk conversation as read,update,token,true,", "nextcloud.talk.rooms.update,Change a Nextcloud Talk conversation,update,token,true,", "nextcloud.talk.rooms.delete,Delete a Nextcloud Talk conversation,delete,token,true,", "nextcloud.files.system-tags.delete,Delete a Nextcloud system tag,delete,",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("tools output does not contain %q:\n%s", want, stdout)
		}
	}
	if got := toolIDs(t, string(runNextcloudJSON(t, "", "tools", "nextcloud", "--all", "--query", "nextcloud", "--config", path))); len(got) != 144 {
		t.Errorf("nextcloud tools = %v, want all one hundred and forty tools", got)
	}
	if reads.Load() != 0 {
		t.Errorf("secret lookups = %d, want 0", reads.Load())
	}
}

// One tool document carries the complete contract. The instance, the identity, and the root folder are
// not part of it: an agent can name neither them nor a URL, a method, or a depth through the contract.
func TestNextcloudToolContractKeepsInstanceIdentityAndRootOutOfTheArguments(t *testing.T) {
	path := nextcloudConfig(t)

	code, stdout, stderr := runNextcloudCLI(t, nil, "", "describe", "nextcloud.files.files.list", "--config", path)
	if code != exitOK || stderr != "" {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	for _, want := range []string{
		"id: nextcloud.files.files.list", "version: 1", "effect: read", "idempotency: safe",
		"confirmation: none", "path",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("tool contract does not contain %q:\n%s", want, stdout)
		}
	}

	document := runNextcloudJSON(t, "", "describe", "nextcloud.files.files.list", "--config", path)
	var described struct {
		Tool struct {
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tool"`
		Connections []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(document, &described); err != nil {
		t.Fatalf("tool document = %s: %v", document, err)
	}
	for _, forbidden := range []string{
		"base_url", "instance", "user", "password", "root", "href", "depth", "method", "url", "recursive",
	} {
		if strings.Contains(string(described.Tool.InputSchema), forbidden) {
			t.Errorf("the input schema offers %q: %s", forbidden, described.Tool.InputSchema)
		}
	}
	if len(described.Connections) != 4 {
		t.Errorf("connections = %v, want all four configured Nextcloud connections", described.Connections)
	}
}

// Everything the contract does not allow is refused before a connection is opened and before any secret
// is resolved, so no request can reach an instance. Four connections are never picked silently.
func TestNextcloudInvokeRefusalsHappenBeforeSecretsAndProviderIO(t *testing.T) {
	path := nextcloudConfig(t)

	tests := []struct {
		name  string
		input string
		args  []string
		code  string
	}{
		{
			name: "four connections without a default", input: `{}`,
			args: []string{"invoke", "nextcloud.files.files.list", "--config", path},
			code: "connection-ambiguous",
		},
		{
			name: "an instance as an argument", input: `{"base_url":"https://evil.example.invalid"}`,
			args: []string{"invoke", "nextcloud.files.files.list", "--connection", "files-reports", "--config", path},
			code: "invalid-request",
		},
		{
			name: "an identity as an argument", input: `{"user":"other"}`,
			args: []string{"invoke", "nextcloud.files.files.list", "--connection", "files-reports", "--config", path},
			code: "invalid-request",
		},
		{
			name: "a depth as an argument", input: `{"depth":"infinity"}`,
			args: []string{"invoke", "nextcloud.files.files.list", "--connection", "files-reports", "--config", path},
			code: "invalid-request",
		},
		{
			name: "a path that traverses the root", input: `{"path":"../Audit"}`,
			args: []string{"invoke", "nextcloud.files.files.list", "--connection", "files-reports", "--config", path},
			code: "invalid-request",
		},
		{
			name: "an absolute path", input: `{"path":"/remote.php/dav/files/other/Audit"}`,
			args: []string{"invoke", "nextcloud.files.files.stat", "--connection", "files-reports", "--config", path},
			code: "invalid-request",
		},
		{
			name: "an encoded separator", input: `{"path":"2026%2F..%2FAudit"}`,
			args: []string{"invoke", "nextcloud.files.files.stat", "--connection", "files-reports", "--config", path},
			code: "invalid-request",
		},
		{
			name: "a double encoded separator", input: `{"path":"2026%252F.."}`,
			args: []string{"invoke", "nextcloud.files.files.stat", "--connection", "files-reports", "--config", path},
			code: "invalid-request",
		},
		{
			name: "a backslash separator", input: `{"path":"2026\\..\\Audit"}`,
			args: []string{"invoke", "nextcloud.files.files.stat", "--connection", "files-reports", "--config", path},
			code: "invalid-request",
		},
		{
			name: "a connection of another provider", input: `{}`,
			args: []string{"invoke", "nextcloud.files.files.list", "--connection", "wiki", "--config", path},
			code: "unsupported-capability",
		},
		{
			name: "an unknown connection", input: `{}`,
			args: []string{"invoke", "nextcloud.files.files.list", "--connection", "absent", "--config", path},
			code: "unknown-connection",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reads atomic.Int32
			code, stdout, stderr := runNextcloudCLI(t, &reads, tt.input, tt.args...)
			if code != exitUsage || stdout != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if !strings.Contains(stderr, "qatlas: "+tt.code+":") {
				t.Errorf("stderr = %q, want the %s code", stderr, tt.code)
			}
			if reads.Load() != 0 {
				t.Errorf("secret lookups = %d, want none before the request was refused", reads.Load())
			}
		})
	}
}

// The MCP broker and the tool CLI publish the same Nextcloud contracts and the same error codes, because
// both use the application core.
func TestNextcloudMCPAndCLIShareTheCoreContracts(t *testing.T) {
	path := nextcloudConfig(t)
	options := &Options{Config: path, Redactor: &redact.Redactor{}}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"search","method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.search","arguments":{"provider":"nextcloud","query":"nextcloud","all":true}}}`,
		`{"jsonrpc":"2.0","id":"describe","method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.describe","arguments":{"operation":"nextcloud.files.files.list","version":1}}}`,
		`{"jsonrpc":"2.0","id":"invoke","method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.invoke","arguments":{"operation":"nextcloud.files.files.stat",` +
			`"connection":"files-reports","arguments":{"path":"../Audit"}}}}`,
		`{"jsonrpc":"2.0","id":"ambiguous","method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.invoke","arguments":{"operation":"nextcloud.files.files.list","arguments":{}}}}`,
	}, "\n") + "\n"

	responses, stderr := runMCPWithOptions(t, defaultRegistry(), input, options)
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}

	search := toolResultFrom(t, responses[`"search"`])
	var searched struct {
		Operations []application.SearchHit `json:"operations"`
		NextCursor string                  `json:"next_cursor"`
	}
	decodeRaw(t, search.Structured, &searched)
	// More than one page of tools: the query lists them instead of the groups, and a cursor reaches the rest.
	if len(searched.Operations) != 50 || searched.NextCursor == "" ||
		searched.Operations[0].ID != "nextcloud.account.account.get" || searched.Operations[1].ID != "nextcloud.account.activity.list" ||
		!sort.SliceIsSorted(searched.Operations, func(a, b int) bool { return searched.Operations[a].ID < searched.Operations[b].ID }) {
		t.Fatalf("search operations = %+v", searched.Operations)
	}

	describe := toolResultFrom(t, responses[`"describe"`])
	describedByCLI := runNextcloudJSON(t, "", "describe", "nextcloud.files.files.list", "--config", path, "--output", "json")
	assertMCPParity(t, describedByCLI, "tool", describe.Structured, "operation")
	assertMCPParity(t, describedByCLI, "connections", describe.Structured, "connections")

	invoke := toolResultFrom(t, responses[`"invoke"`])
	if !invoke.IsError {
		t.Fatalf("invoke = %+v, want the traversal refused", invoke)
	}
	mcpCode, _, _ := strings.Cut(invoke.Content[0].Text, ":")
	code, _, cliStderr := runNextcloudCLI(t, nil, `{"path":"../Audit"}`,
		"invoke", "nextcloud.files.files.stat", "--connection", "files-reports", "--config", path)
	cliCode, _, _ := strings.Cut(strings.TrimPrefix(strings.SplitN(cliStderr, "\n", 2)[0], "qatlas: "), ":")
	if code != exitUsage || mcpCode != cliCode || cliCode != "invalid-request" {
		t.Fatalf("MCP code=%q CLI code=%q exit=%d", mcpCode, cliCode, code)
	}

	// The broker does not pick one of the four roots either.
	ambiguous := toolResultFrom(t, responses[`"ambiguous"`])
	if !ambiguous.IsError || !strings.HasPrefix(ambiguous.Content[0].Text, "connection-ambiguous:") {
		t.Fatalf("ambiguous invoke = %+v, want several matching connections", ambiguous)
	}
}

func runNextcloudJSON(t *testing.T, input string, args ...string) []byte {
	t.Helper()
	if len(args) > 0 && args[len(args)-1] != "json" {
		args = append(args, "--output", "json")
	}
	code, stdout, stderr := runNextcloudCLI(t, nil, input, args...)
	if code != exitOK || stderr != "" {
		t.Fatalf("CLI %v exit=%d stderr=%q", args, code, stderr)
	}
	return []byte(stdout)
}

// With a service level the group overview stays the first answer and a group lists its tools.
func TestNextcloudToolsListGroupsBeforeTools(t *testing.T) {
	path := nextcloudConfig(t)
	code, stdout, stderr := runNextcloudCLI(t, nil, "", "tools", "nextcloud", "--all", "--config", path)
	if code != exitOK || !strings.Contains(stdout, "groups[9]{group,title,description,tools,offered}:\n") ||
		strings.Contains(stdout, "tools[") || !strings.Contains(stderr, "tools in 9 groups; 'qatlas tools nextcloud <group>'") {
		t.Fatalf("exit=%d stderr=%q stdout:\n%s", code, stderr, stdout)
	}
	code, stdout, stderr = runNextcloudCLI(t, nil, "", "tools", "nextcloud", "deck", "--all", "--config", path)
	if code != exitOK || stderr != "" || !strings.Contains(stdout, "nextcloud.deck.cards.get") ||
		strings.Contains(stdout, "nextcloud.talk.") {
		t.Fatalf("deck: exit=%d stderr=%q stdout:\n%s", code, stderr, stdout)
	}
}
