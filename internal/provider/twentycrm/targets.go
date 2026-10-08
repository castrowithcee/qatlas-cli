package twentycrm

import (
	"errors"
	"regexp"
	"sort"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	objectPrefix  = "object/"
	companyObject = "company"
)

// objectNamePattern is the shape of the singular camelCase name Twenty gives an object. It is checked on
// every configured target and on every object argument before the name reaches anything else.
var objectNamePattern = regexp.MustCompile(`^[a-z][A-Za-z0-9]{0,62}$`)

// systemObjects are the objects no connection reaches, whether it has targets or not, and no target can
// name. They are the 33 standard objects Twenty marks isSystem: true in
// packages/twenty-server/src/engine/workspace-manager/twenty-standard-application/utils/object-metadata/
// create-standard-flat-object-metadata.util.ts (twentyhq/twenty, commit 46fc01c38374c2b489b0d4755719da2d1e5408ac),
// plus workflow, which Twenty leaves visible but which carries automations. The workspace document does not
// mark system objects, so this fixed list is the only line.
var systemObjects = map[string]bool{
	"agentChatThread": true, "agentChatThreadParticipant": true, "agentChatThreadTarget": true,
	"agentMessage": true, "agentMessagePart": true, "agentTurn": true, "attachment": true, "blocklist": true,
	"calendarChannelEventAssociation": true, "calendarEvent": true, "calendarEventParticipant": true,
	"calendarEventTarget": true, "callRecording": true, "campaignDelivery": true, "message": true,
	"messageCampaign": true, "messageChannelMessageAssociation": true,
	"messageChannelMessageAssociationMessageFolder": true, "messageList": true, "messageListMember": true,
	"messageParticipant": true, "messageSuppression": true, "messageThread": true, "messageThreadTarget": true,
	"noteTarget": true, "recordShare": true, "shortLink": true, "taskTarget": true, "timelineActivity": true,
	"workflowAutomatedTrigger": true, "workflowRun": true, "workflowVersion": true, "workspaceMember": true,
	"workflow": true,
}

// scope is the object boundary of one connection: no objects means every non-system object the API key
// reaches, otherwise exactly the listed ones.
type scope struct {
	objects []string
}

func (s scope) bound() bool { return len(s.objects) > 0 }

// allows reports whether the connection may reach an object by name, without looking at the workspace.
func (s scope) allows(name string) bool {
	if !objectNamePattern.MatchString(name) || systemObjects[name] {
		return false
	}
	if !s.bound() {
		return true
	}
	for _, bound := range s.objects {
		if bound == name {
			return true
		}
	}
	return false
}

const targetForm = "a Twenty target must be object/NAME with the camelCase singular name of an object, " +
	"for example object/person"

// parseTarget reads one object/NAME entry. No error quotes the value.
func parseTarget(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, objectPrefix) {
		return "", errors.New(targetForm)
	}
	name := strings.TrimPrefix(raw, objectPrefix)
	if !objectNamePattern.MatchString(name) {
		return "", errors.New(targetForm)
	}
	if systemObjects[name] {
		return "", errors.New("a Twenty target names a system object, which no connection reaches")
	}
	return name, nil
}

// parseScope reads the configured targets: none, or one or more object/NAME entries, none named twice.
func parseScope(values []string) (scope, error) {
	var bound scope
	seen := map[string]bool{}
	for _, raw := range values {
		name, err := parseTarget(raw)
		if err != nil {
			return scope{}, err
		}
		if seen[name] {
			return scope{}, errors.New("the Twenty object targets name an object more than once")
		}
		seen[name] = true
		bound.objects = append(bound.objects, name)
	}
	sort.Strings(bound.objects)
	return bound, nil
}

func validateTarget(raw string) error {
	_, err := parseTarget(raw)
	return err
}

func validateSet(values []string) error {
	_, err := parseScope(values)
	return err
}

// boundScope reads the connection's scope before any secret is resolved.
func boundScope(resolved *config.Resolved) (scope, error) {
	if resolved == nil {
		return scope{}, providerError("open", "no connection was selected")
	}
	bound, err := parseScope(provider.TargetsOf(resolved))
	if err != nil {
		return scope{}, providerError("open", err.Error())
	}
	return bound, nil
}

func invalidRequest(message string) error { return &provider.InvalidRequestError{Message: message} }

// errObjectUnavailable is the one refusal for an object a connection cannot reach: a malformed name, a
// system object, and an object outside the targets read alike and never name the object.
const errObjectUnavailable = "this object is not available through this connection"

// selectObject checks an object argument against the connection's targets and the system list before any
// secret is resolved and before any request is sent. Whether the object exists is known only after the
// workspace catalog was read; see catalog.resolve.
func selectObject(resolved *config.Resolved, name string) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if !bound.allows(name) {
		return invalidRequest(errObjectUnavailable)
	}
	return nil
}

// requireWorkspaceScope is the local gate of every workspace-wide tool. It admits only a connection without
// object targets and refuses any other one, before a secret is resolved and before any request is sent.
// The refusal names no target.
func requireWorkspaceScope(resolved *config.Resolved) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if bound.bound() {
		return invalidRequest("this connection has object targets, so it cannot use workspace-wide tools: " +
			"they are only available on a connection without targets")
	}
	return nil
}
