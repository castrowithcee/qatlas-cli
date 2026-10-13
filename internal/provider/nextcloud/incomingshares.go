package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	incomingInternal  = "internal"
	incomingFederated = "federated"
	uncertainIncoming = "; the share may have changed, check incomingshares.pending before repeating"
)

type incomingShare struct {
	ID         string `json:"id"`
	Origin     string `json:"origin"`
	Sender     string `json:"sender"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Time       string `json:"time,omitempty"`
	ServerHost string `json:"server_host,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

type incomingResult struct {
	Shares    []incomingShare `json:"shares"`
	Count     int             `json:"count"`
	Truncated bool            `json:"truncated,omitempty"`
}

type remotePending struct {
	ID     flexString `json:"id"`
	Owner  string     `json:"owner"`
	Name   string     `json:"name"`
	Type   string     `json:"type"`
	Remote string     `json:"remote"`
}

func incomingText(value string) (string, bool) {
	cut := bounded(value)
	return cut, cut != value
}

func incomingHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Scheme != "https" && u.Scheme != "http" {
		return ""
	}
	return bounded(u.Hostname())
}

// pendingShares reads one pending list. The internal list carries no status and no time other than stime,
// so every entry is reported, including offers declined earlier; the federated list carries no share time
// (mtime is a file time), so none is reported.
func (c *Client) pendingShares(ctx context.Context, origin string) ([]incomingShare, error) {
	const op = "list pending shares"
	var suffix []string
	switch origin {
	case incomingInternal:
		suffix = []string{"shares", "pending"}
	case incomingFederated:
		suffix = []string{"remote_shares", "pending"}
	default:
		return nil, providerError(op, "the share origin is unusable")
	}
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsSharing, suffix: suffix})
	if err != nil {
		return nil, err
	}
	result := []incomingShare{}
	if origin == incomingInternal {
		var items []rawShare
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, invalidResponse(op, "the pending share list could not be read")
		}
		for _, item := range items {
			id := string(item.ID)
			if checkShareIDFor(op, id) != nil || item.UIDOwner == c.user || item.UIDInitiator == c.user ||
				item.ShareType != "0" && item.ShareType != "1" {
				continue
			}
			sender, a := incomingText(item.UIDOwner)
			target := item.FileTarget
			if target == "" {
				target = item.Path
			}
			name, b := incomingText(path.Base(target))
			if name == "." || name == "/" {
				continue
			}
			kind := item.ItemType
			if kind != "file" && kind != "folder" {
				kind = "unknown"
			}
			result = append(result, incomingShare{ID: id, Origin: origin, Sender: sender, Name: name, Type: kind,
				Time: incomingTime(item.STime), Truncated: a || b})
		}
	} else {
		var items []remotePending
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, invalidResponse(op, "the pending share list could not be read")
		}
		for _, item := range items {
			id := string(item.ID)
			if checkShareIDFor(op, id) != nil {
				continue
			}
			sender, a := incomingText(item.Owner)
			name, b := incomingText(path.Base(item.Name))
			if name == "." || name == "/" {
				continue
			}
			kind := item.Type
			if kind != "file" && kind != "folder" {
				kind = "unknown"
			}
			result = append(result, incomingShare{ID: id, Origin: origin, Sender: sender, Name: name, Type: kind,
				ServerHost: incomingHost(item.Remote), Truncated: a || b})
		}
	}
	return result, nil
}

func incomingTime(value json.Number) string {
	seconds, err := value.Int64()
	if err != nil || seconds <= 0 {
		return ""
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

func (c *Client) listIncoming(ctx context.Context) (*incomingResult, error) {
	result := &incomingResult{Shares: []incomingShare{}}
	for _, origin := range []string{incomingInternal, incomingFederated} {
		shares, err := c.pendingShares(ctx, origin)
		if err != nil {
			return nil, err
		}
		for _, share := range shares {
			if len(result.Shares) == maxShareEntries {
				result.Truncated = true
				break
			}
			result.Shares = append(result.Shares, share)
		}
	}
	result.Count = len(result.Shares)
	return result, nil
}

func (c *Client) changeIncoming(ctx context.Context, id, origin string, accept bool) (any, error) {
	op := "decline pending share"
	if accept {
		op = "accept pending share"
	}
	shares, err := c.pendingShares(ctx, origin)
	if err != nil {
		return nil, err
	}
	// Numeric IDs of the two origins are independent. Accepting reads only the internal list, so an ID that is not
	// pending there, a federated one included, is refused before any change and without naming a foreign target.
	found := false
	for _, share := range shares {
		if share.ID == id {
			found = true
			break
		}
	}
	if !found {
		return nil, &provider.Error{Class: provider.ClassNotFound, Op: op, Message: "this connection does not hold this pending share"}
	}
	method := http.MethodDelete
	var suffix []string
	if origin == incomingInternal {
		suffix = []string{"shares", id}
		if accept {
			method = http.MethodPost
			suffix = []string{"shares", "pending", id}
		}
	} else {
		suffix = []string{"remote_shares", "pending", id}
	}
	if _, err := c.ocsSend(ctx, op, method, ocsRequest{app: ocsSharing, suffix: suffix}, nil, uncertainIncoming); err != nil {
		return nil, tagAdminError(op, err, shareManageErrors)
	}
	return map[string]any{"id": id, "origin": origin, "changed": true}, nil
}

const incomingEntrySchema = `{"type":"object","properties":{"id":{"type":"string"},"origin":{"type":"string","enum":["internal","federated"]},"sender":{"type":"string"},"name":{"type":"string"},"type":{"type":"string"},"time":{"type":"string"},"server_host":{"type":"string"},"truncated":{"type":"boolean"}},"required":["id","origin","sender","name","type"],"additionalProperties":false}`
const incomingChangedSchema = `{"type":"object","properties":{"id":{"type":"string"},"origin":{"type":"string"},"changed":{"type":"boolean"}},"required":["id","origin","changed"],"additionalProperties":false}`

var incomingPending = capability.Descriptor{
	ID: Provider + ".incomingshares.pending", Version: 1, Title: "List pending incoming Nextcloud shares",
	Description: "List pending internal and federated shares offered to the account identity; provider text is untrusted",
	Tags:        []string{"nextcloud", "shares", "incoming", "pending"}, Provider: Provider, Group: groupShares, Risk: sharesRisk,
	InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"shares":{"type":"array","items":` + incomingEntrySchema + `},"count":{"type":"integer"},"truncated":{"type":"boolean"}},"required":["shares","count"],"additionalProperties":false}`),
	Fields:       []capability.Field{{Name: "shares", Description: "Pending internal and federated offers; provider text is untrusted"}, {Name: "count", Description: "Number of offers"}, {Name: "truncated", Description: "True when the bounded list was cut"}},
}

var incomingAccept = capability.Descriptor{
	ID: Provider + ".incomingshares.accept", Version: 1, Title: "Accept an internal Nextcloud share",
	Description: "Accept one pending internal share offered to the account identity; federated shares cannot be accepted",
	Tags:        []string{"nextcloud", "shares", "incoming", "accept"}, Provider: Provider, Group: groupShares,
	Risk:         shareManageRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent),
	InputSchema:  json.RawMessage(`{"type":"object","properties":{"share_id":` + shareIDSchema + `},"required":["share_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(incomingChangedSchema),
	Arguments:    []capability.Argument{{Name: "share_id", Required: true, Description: "ID of an internal offer from incomingshares.pending"}},
	Fields:       []capability.Field{{Name: "id", Description: "Accepted share ID"}, {Name: "origin", Description: "internal"}, {Name: "changed", Description: "True when Nextcloud accepted the offer"}},
	Examples:     []capability.Example{{Description: "Accept an internal offer", Arguments: json.RawMessage(`{"share_id":"42"}`)}},
}

var incomingDecline = capability.Descriptor{
	ID: Provider + ".incomingshares.decline", Version: 1, Title: "Decline a pending Nextcloud share",
	Description: "Decline one pending internal or federated offer of the account identity",
	Tags:        []string{"nextcloud", "shares", "incoming", "decline"}, Provider: Provider, Group: groupShares,
	Risk: shareManageRisk(capability.EffectDelete, capability.IdempotencyNonIdempotent), RequiresToolAllowList: true,
	InputSchema:  json.RawMessage(`{"type":"object","properties":{"share_id":` + shareIDSchema + `,"origin":{"type":"string","enum":["internal","federated"]}},"required":["share_id","origin"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(incomingChangedSchema),
	Arguments:    []capability.Argument{{Name: "share_id", Required: true, Description: "ID from incomingshares.pending"}, {Name: "origin", Required: true, Description: "internal or federated as reported by incomingshares.pending"}},
	Fields:       []capability.Field{{Name: "id", Description: "Declined share ID"}, {Name: "origin", Description: "Origin of the declined offer"}, {Name: "changed", Description: "True when Nextcloud declined the offer"}},
	Examples:     []capability.Example{{Description: "Decline a federated offer", Arguments: json.RawMessage(`{"share_id":"42","origin":"federated"}`)}},
}

func incomingArgs(raw json.RawMessage, decline bool) (string, string, error) {
	var input struct {
		ShareID string `json:"share_id"`
		Origin  string `json:"origin"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", "", providerError("pending share", "the validated arguments could not be read")
	}
	if err := checkShareIDFor("pending share", input.ShareID); err != nil {
		return "", "", err
	}
	if !decline {
		return input.ShareID, incomingInternal, nil
	}
	if input.Origin != incomingInternal && input.Origin != incomingFederated {
		return "", "", providerError("pending share", "the origin must be internal or federated")
	}
	return input.ShareID, input.Origin, nil
}

func invokeIncomingPending(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, _ json.RawMessage) (any, error) {
	c, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return c.listIncoming(ctx)
}
func invokeIncomingAccept(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, origin, err := incomingArgs(raw, false)
	if err != nil {
		return nil, err
	}
	c, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return c.changeIncoming(ctx, id, origin, true)
}
func invokeIncomingDecline(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, origin, err := incomingArgs(raw, true)
	if err != nil {
		return nil, err
	}
	c, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return c.changeIncoming(ctx, id, origin, false)
}
