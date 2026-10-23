package github

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// outputSamples names, for every tool, a value of the Go type its handler returns. The test reads the JSON
// names of that type, so a member a tool can emit is checked without a call. A tool whose result is a
// generic map carries a literal with the members the handler sets; the three delete tools are the only ones.
var outputSamples = map[string]any{
	"github.accounts.me":                           Account{},
	"github.actions-permissions.get":               ActionsPermissionsRead{},
	"github.actions-permissions.update":            SettingsChange[ActionsPermissions]{},
	"github.blame.get":                             BlameResult{},
	"github.branches.create":                       BranchCreated{},
	"github.branches.list":                         BranchList{},
	"github.code-quality-findings.get":             CodeQualityFinding{},
	"github.code-scanning-alerts.get":              CodeScanningAlert{},
	"github.code-scanning-alerts.list":             CodeScanningAlertList{},
	"github.code.search":                           CodeSearchList{},
	"github.collaborators.list":                    CollaboratorList{},
	"github.comments.create":                       Comment{},
	"github.comments.delete":                       map[string]any{"deleted": true, "comment_id": int64(0)},
	"github.comments.list":                         CommentList{},
	"github.comments.update":                       Comment{},
	"github.commits.get":                           CommitDetail{},
	"github.commits.list":                          CommitList{},
	"github.commits.search":                        CommitSearchList{},
	"github.contents.delete":                       DeletedContents{},
	"github.contents.get":                          ContentsResult{},
	"github.contents.put":                          WrittenContents{},
	"github.copilot-assignments.create":            CopilotAssignment{},
	"github.copilot-reviews.request":               PullRequest{},
	"github.custom-properties.get":                 CustomPropertiesResult{},
	"github.custom-properties.set":                 CustomPropertiesResult{},
	"github.dependabot-alerts.get":                 DependabotAlert{},
	"github.dependabot-alerts.list":                DependabotAlertList{},
	"github.discussion-categories.list":            DiscussionCategoryList{},
	"github.discussion-comments.create":            DiscussionCommentWritten{},
	"github.discussion-comments.delete":            map[string]any{"deleted": true, "comment_id": ""},
	"github.discussion-comments.list":              DiscussionCommentList{},
	"github.discussion-comments.update":            DiscussionCommentWritten{},
	"github.discussions.create":                    DiscussionCreated{},
	"github.discussions.get":                       DiscussionGet{},
	"github.discussions.list":                      DiscussionList{},
	"github.files.push":                            FilesPushed{},
	"github.gists.create":                          Gist{},
	"github.gists.delete":                          GistDeleted{},
	"github.gists.get":                             GistDetail{},
	"github.gists.list":                            GistList{},
	"github.gists.update":                          Gist{},
	"github.global-advisories.get":                 GlobalAdvisory{},
	"github.global-advisories.list":                GlobalAdvisoryList{},
	"github.issue-dependencies.add":                IssueDependencyChange{},
	"github.issue-dependencies.list":               IssueDependencies{},
	"github.issue-dependencies.remove":             IssueDependencyChange{},
	"github.issue-fields.list":                     IssueFieldList{},
	"github.issue-fields.set":                      IssueFieldsChange{},
	"github.issue-types.list":                      IssueTypeList{},
	"github.issues.close":                          Issue{},
	"github.issues.create":                         Issue{},
	"github.issues.get":                            Issue{},
	"github.issues.list":                           IssueList{},
	"github.issues.reopen":                         Issue{},
	"github.issues.search":                         IssueSearchList{},
	"github.issues.update":                         Issue{},
	"github.labels.create":                         Label{},
	"github.labels.delete":                         map[string]any{"deleted": true, "name": ""},
	"github.labels.get":                            Label{},
	"github.labels.list":                           LabelList{},
	"github.labels.update":                         Label{},
	"github.milestones.list":                       MilestoneList{},
	"github.notifications.dismiss":                 NotificationDismissed{},
	"github.notifications.get":                     Notification{},
	"github.notifications.list":                    NotificationList{},
	"github.notifications.mark-all":                NotificationsMarked{},
	"github.organization-advisories.list":          RepositoryAdvisoryList{},
	"github.organizations.search":                  OrganizationSearchList{},
	"github.project-collaborators.update":          ChangedCollaborators{},
	"github.project-drafts.convert":                ConvertedDraft{},
	"github.project-drafts.create":                 Planning{},
	"github.project-drafts.update":                 Draft{},
	"github.project-field-options.delete":          RemovedEntries{},
	"github.project-fields.create":                 ChangedField{},
	"github.project-fields.delete":                 DeletedField{},
	"github.project-fields.list":                   FieldSchema{},
	"github.project-fields.update":                 ChangedField{},
	"github.project-issues.create":                 Planning{},
	"github.project-items.add":                     Planning{},
	"github.project-items.archive":                 Archived{},
	"github.project-items.delete":                  DeletedItem{},
	"github.project-items.get":                     Item{},
	"github.project-items.list":                    ItemList{},
	"github.project-items.move":                    MovedItem{},
	"github.project-items.unarchive":               Archived{},
	"github.project-items.update":                  Planning{},
	"github.project-iterations.replace":            RemovedEntries{},
	"github.project-status.create":                 ChangedStatusUpdate{},
	"github.project-status.delete":                 DeletedStatusUpdate{},
	"github.project-status.list":                   StatusUpdateList{},
	"github.project-status.update":                 ChangedStatusUpdate{},
	"github.project-teams.list":                    TeamList{},
	"github.project-templates.mark":                Templated{},
	"github.project-templates.unmark":              Templated{},
	"github.project-views.create":                  CreatedView{},
	"github.project-views.delete":                  DeletedView{},
	"github.project-views.list":                    ViewList{},
	"github.project-views.update":                  ChangedView{},
	"github.project-workflows.delete":              DeletedProjectWorkflow{},
	"github.project-workflows.list":                ProjectWorkflowList{},
	"github.projects.copy":                         Created{},
	"github.projects.create":                       Created{},
	"github.projects.delete":                       Deleted{},
	"github.projects.link":                         Linked{},
	"github.projects.link-team":                    TeamLink{},
	"github.projects.list":                         ProjectList{},
	"github.projects.unlink":                       Linked{},
	"github.projects.unlink-team":                  TeamLink{},
	"github.projects.update":                       ProjectState{},
	"github.pull-request-branches.update":          PullRequestBranchUpdate{},
	"github.pull-request-checks.list":              PullRequestChecks{},
	"github.pull-request-comments.create":          Comment{},
	"github.pull-request-comments.list":            CommentList{},
	"github.pull-request-commits.list":             PullRequestCommitList{},
	"github.pull-request-diffs.get":                PullRequestDiff{},
	"github.pull-request-files.list":               PullRequestFileList{},
	"github.pull-request-review-comments.list":     ReviewCommentList{},
	"github.pull-request-review-comments.reply":    ReviewComment{},
	"github.pull-request-review-threads.list":      ReviewThreadList{},
	"github.pull-request-review-threads.resolve":   ReviewThreadState{},
	"github.pull-request-review-threads.unresolve": ReviewThreadState{},
	"github.pull-request-reviewers.remove":         PullRequest{},
	"github.pull-request-reviewers.request":        PullRequest{},
	"github.pull-request-reviews.approve":          Review{},
	"github.pull-request-reviews.create":           Review{},
	"github.pull-request-reviews.list":             ReviewList{},
	"github.pull-requests.close":                   PullRequest{},
	"github.pull-requests.create":                  PullRequest{},
	"github.pull-requests.get":                     PullRequestDetail{},
	"github.pull-requests.list":                    PullRequestList{},
	"github.pull-requests.merge":                   PullRequestMerge{},
	"github.pull-requests.reopen":                  PullRequest{},
	"github.pull-requests.search":                  PullRequestSearchList{},
	"github.pull-requests.update":                  PullRequest{},
	"github.reactions.add":                         ReactionAdd{},
	"github.reactions.remove":                      ReactionRemove{},
	"github.release-assets.list":                   ReleaseAssetList{},
	"github.releases.create":                       Release{},
	"github.releases.delete":                       DeletedRelease{},
	"github.releases.get":                          Release{},
	"github.releases.list":                         ReleaseList{},
	"github.releases.update":                       Release{},
	"github.repositories.create":                   RepositoryCreated{},
	"github.repositories.delete":                   RepositoryDeleted{},
	"github.repositories.fork":                     RepositoryForked{},
	"github.repositories.list":                     RepositoryList{},
	"github.repositories.search":                   RepositorySearchList{},
	"github.repository-advisories.list":            RepositoryAdvisoryList{},
	"github.repository-settings.get":               RepositorySettings{},
	"github.repository-settings.update":            MergeSettings{},
	"github.repository-subscriptions.set":          RepositorySubscription{},
	"github.rulesets.create":                       Ruleset{},
	"github.rulesets.delete":                       RulesetDeleted{},
	"github.rulesets.get":                          Ruleset{},
	"github.rulesets.list":                         RulesetList{},
	"github.rulesets.update":                       Ruleset{},
	"github.secret-scanning-alerts.get":            SecretScanningAlert{},
	"github.secret-scanning-alerts.list":           SecretScanningAlertList{},
	"github.stars.add":                             StarChange{},
	"github.stars.list":                            StarredRepositoryList{},
	"github.stars.remove":                          StarChange{},
	"github.sub-issues.add":                        SubIssueChange{},
	"github.sub-issues.list":                       SubIssueList{},
	"github.sub-issues.remove":                     SubIssueChange{},
	"github.sub-issues.reprioritize":               SubIssueChange{},
	"github.tags.get":                              TagDetail{},
	"github.tags.list":                             TagList{},
	"github.team-members.list":                     TeamMemberList{},
	"github.teams.list":                            OrganizationTeamList{},
	"github.thread-subscriptions.set":              ThreadSubscription{},
	"github.trees.get":                             TreeResult{},
	"github.users.search":                          UserSearchList{},
	"github.workflow-artifacts.list":               ArtifactList{},
	"github.workflow-files.create":                 WorkflowFileChange{},
	"github.workflow-files.get":                    WorkflowFile{},
	"github.workflow-files.list":                   WorkflowFiles{},
	"github.workflow-files.update":                 WorkflowFileChange{},
	"github.workflow-jobs.get":                     WorkflowJob{},
	"github.workflow-jobs.list":                    JobList{},
	"github.workflow-jobs.log":                     JobLog{},
	"github.workflow-permissions.get":              WorkflowPermissions{},
	"github.workflow-permissions.update":           SettingsChange[WorkflowPermissions]{},
	"github.workflow-run-logs.delete":              RunLogsDeleted{},
	"github.workflow-runs.cancel":                  RunCancel{},
	"github.workflow-runs.get":                     WorkflowRun{},
	"github.workflow-runs.list":                    RunList{},
	"github.workflow-runs.rerun":                   RunChange{},
	"github.workflow-runs.rerun-failed":            RunChange{},
	"github.workflow-runs.usage":                   RunUsage{},
	"github.workflows.disable":                     WorkflowState{},
	"github.workflows.dispatch":                    Dispatch{},
	"github.workflows.enable":                      WorkflowState{},
	"github.workflows.get":                         Workflow{},
	"github.workflows.list":                        WorkflowList{},
}

// unemittedMembers lists, per tool, the members of a Go type shared with another tool that this tool never
// sets, so its schema rightly omits them: a list leaves out what only the detail read adds, and a change
// sets what only its counterpart sets.
var unemittedMembers = map[string][]string{
	"github.code-scanning-alerts.list": {"alerts[].linked_pull_requests", "alerts[].linked_pull_requests_truncated"},
	"github.gists.create":              {"files[].binary", "files[].content", "files[].content_truncated"},
	"github.gists.update":              {"files[].binary", "files[].content", "files[].content_truncated"},
	"github.gists.get":                 {"forks", "forks_truncated", "history", "history_truncated"},
	"github.gists.list":                {"gists[].files[].binary", "gists[].files[].content", "gists[].files[].content_truncated"},
	"github.issue-dependencies.add":    {"removed"},
	"github.sub-issues.add":            {"removed"},
	"github.sub-issues.reprioritize":   {"removed", "sub_issues_summary"},
	"github.project-items.list":        {"items[].body"},
}

// checkOutput reports every member of the output of sample that the output schema or the fields lack, at any
// depth of the schema, and every top-level schema member without a field. Fields name top-level members; a
// field may also name a member of the entries of a top-level list, which documents that entry member.
// Members in unemitted are shared-type members the tool never sets; each must still be found, so a stale
// entry fails.
func checkOutput(sample any, rawSchema json.RawMessage, fields []capability.Field, unemitted []string) []string {
	var schema map[string]any
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		return []string{"output schema unreadable: " + err.Error()}
	}
	var findings []string
	properties, _ := schema["properties"].(map[string]any)
	named := map[string]bool{}
	for _, field := range fields {
		named[field.Name] = true
		if _, ok := properties[field.Name]; !ok && !isEntryMember(properties, field.Name) {
			findings = append(findings, field.Name+": field without schema member")
		}
	}
	for name := range properties {
		if !named[name] {
			findings = append(findings, name+": schema member without field")
		}
	}
	var missing []string
	if value := reflect.ValueOf(sample); value.Kind() == reflect.Map {
		for _, key := range value.MapKeys() {
			if _, ok := properties[key.String()]; !ok {
				missing = append(missing, key.String())
			}
		}
	} else {
		missing = outputMembers(reflect.TypeOf(sample), schema, "", 0)
	}
	skipped := map[string]bool{}
	for _, path := range unemitted {
		skipped[path] = true
	}
	for _, path := range missing {
		if skipped[path] {
			delete(skipped, path)
			continue
		}
		findings = append(findings, path+": not in schema")
		if top, _, _ := strings.Cut(path, "."); !named[strings.TrimSuffix(top, "[]")] {
			findings = append(findings, path+": top-level member not in fields")
		}
	}
	for path := range skipped {
		findings = append(findings, path+": listed as unemitted but in the schema or not a member")
	}
	sort.Strings(findings)
	return findings
}

// isEntryMember reports whether name is a member of the entries of a top-level list of properties.
func isEntryMember(properties map[string]any, name string) bool {
	for _, property := range properties {
		list, _ := property.(map[string]any)
		items, _ := list["items"].(map[string]any)
		members, _ := items["properties"].(map[string]any)
		if _, ok := members[name]; ok {
			return true
		}
	}
	return false
}

var timeType = reflect.TypeOf(time.Time{})

// outputMembers returns the path of every member of t that schema lacks. A free-form value (any, raw JSON,
// a map of such) ends the descent: its members are not a Go type. A schema node of an object without
// properties counts as having none, so a typed member below it is reported rather than skipped.
func outputMembers(t reflect.Type, schema map[string]any, path string, depth int) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if depth > 16 || t == timeType {
		return nil
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		items, ok := schema["items"].(map[string]any)
		if !ok {
			if isTyped(t.Elem()) {
				return []string{path + "[]: no items schema"}
			}
			return nil
		}
		return outputMembers(t.Elem(), items, path+"[]", depth+1)
	case reflect.Map:
		if !isTyped(t.Elem()) {
			return nil
		}
		values, ok := schema["additionalProperties"].(map[string]any)
		if !ok {
			return []string{path + "{}: no value schema"}
		}
		return outputMembers(t.Elem(), values, path+"{}", depth+1)
	case reflect.Struct:
		if t.Implements(reflect.TypeOf((*json.Marshaler)(nil)).Elem()) {
			return []string{path + ": custom marshaler hides the members"}
		}
		properties, _ := schema["properties"].(map[string]any)
		var missing []string
		for name, member := range jsonMembers(t) {
			child := joinPath(path, name)
			sub, ok := properties[name].(map[string]any)
			if !ok {
				missing = append(missing, child)
				continue
			}
			missing = append(missing, outputMembers(member, sub, child, depth+1)...)
		}
		return missing
	}
	return nil
}

// isTyped reports whether a value of t has members of its own that the schema must describe.
func isTyped(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == timeType {
		return false
	}
	switch t.Kind() {
	case reflect.Struct:
		return true
	case reflect.Slice, reflect.Array, reflect.Map:
		return isTyped(t.Elem())
	}
	return false
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// jsonMembers returns the JSON name and type of every member encoding/json emits for struct t, including
// those of an embedded struct without a name of its own.
func jsonMembers(t reflect.Type) map[string]reflect.Type {
	members := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if tag == "-" || !field.IsExported() && !field.Anonymous {
			continue
		}
		if field.Anonymous && name == "" {
			embedded := field.Type
			for embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				for inner, innerType := range jsonMembers(embedded) {
					members[inner] = innerType
				}
				continue
			}
		}
		if !field.IsExported() {
			continue
		}
		if name == "" {
			name = field.Name
		}
		members[name] = field.Type
	}
	return members
}

func TestEveryOutputMemberIsInSchemaAndFields(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, descriptor := range reg.All() {
		registered[descriptor.ID] = true
		sample, ok := outputSamples[descriptor.ID]
		if !ok {
			t.Errorf("%s has no entry in outputSamples", descriptor.ID)
			continue
		}
		if findings := checkOutput(sample, descriptor.OutputSchema, descriptor.Fields, unemittedMembers[descriptor.ID]); len(findings) > 0 {
			t.Errorf("%s: %s", descriptor.ID, strings.Join(findings, "; "))
		}
	}
	for id := range outputSamples {
		if !registered[id] {
			t.Errorf("outputSamples names %s, which no tool carries", id)
		}
	}
}

type extraNested struct {
	Known   string `json:"known"`
	Hidden  string `json:"hidden"`
	private string
}

type extraOutput struct {
	Name    string        `json:"name"`
	Extra   string        `json:"extra"`
	Items   []extraNested `json:"items"`
	Skipped string        `json:"-"`
}

func TestCheckOutputReportsAnUndeclaredMember(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"items":{"type":"array",` +
		`"items":{"type":"object","properties":{"known":{"type":"string"}},"additionalProperties":false}}},` +
		`"additionalProperties":false}`)
	fields := []capability.Field{{Name: "name"}, {Name: "items"}}
	got := strings.Join(checkOutput(extraOutput{}, schema, fields, nil), "; ")
	for _, want := range []string{"extra: not in schema", "extra: top-level member not in fields",
		"items[].hidden: not in schema"} {
		if !strings.Contains(got, want) {
			t.Errorf("findings lack %q: %s", want, got)
		}
	}
	for _, unwanted := range []string{"name:", "known", "Skipped", "private"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("findings carry %q: %s", unwanted, got)
		}
	}
	if findings := checkOutput(extraOutput{}, schema, append(fields, capability.Field{Name: "gone"}), nil); !strings.Contains(
		strings.Join(findings, ";"), "gone: field without schema member") {
		t.Errorf("a field without schema member is not reported: %v", findings)
	}
}
