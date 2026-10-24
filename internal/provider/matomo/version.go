package matomo

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// version is a Matomo release as major.minor.patch; a suffix such as -rc1 is ignored.
type version struct{ major, minor, patch int }

func (v version) less(o version) bool {
	if v.major != o.major {
		return v.major < o.major
	}
	if v.minor != o.minor {
		return v.minor < o.minor
	}
	return v.patch < o.patch
}

func (v version) String() string {
	return strconv.Itoa(v.major) + "." + strconv.Itoa(v.minor) + "." + strconv.Itoa(v.patch)
}

// parseVersion reads "5.14.2", "5.0", or "5.0.0-b1".
func parseVersion(raw string) (version, bool) {
	raw, _, _ = strings.Cut(strings.TrimSpace(raw), "-")
	parts := strings.Split(raw, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return version{}, false
	}
	var numbers [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 || n > 9999 || strings.HasPrefix(parts[i], "+") {
			return version{}, false
		}
		numbers[i] = n
	}
	return version{numbers[0], numbers[1], numbers[2]}, true
}

var (
	// minSupported is the oldest Matomo release any tool of this provider serves.
	minSupported = version{5, 0, 0}
	// headerSince is the first release that reads the token from an Authorization header.
	headerSince = version{5, 4, 0}
)

// instanceTTL is how long the version and the plugin states of one instance and token are trusted. A held
// value saves one request per call; an upgrade or plugin change shows after at most this long.
const instanceTTL = 10 * time.Minute

// now is the clock of the instance cache; tests replace it.
var now = time.Now

type pluginState struct {
	active  bool
	expires time.Time
}

type instanceInfo struct {
	version   version
	bodyToken bool
	expires   time.Time
	plugins   map[string]pluginState
}

// instances holds what is known per instance and token for the life of the process. Only a successful read
// is held; a failure is never remembered.
var instances = struct {
	mu    sync.Mutex
	known map[instanceKey]*instanceInfo
}{known: map[instanceKey]*instanceInfo{}}

// requirement states what a tool needs from the instance beyond Matomo 5.0: a later release and a plugin.
// Every tool of a later package declares its own; the zero value asks for Matomo 5.0 and the core plugins.
// Tool is the ID the refusal names.
type requirement struct {
	tool       string
	minVersion version
	plugin     string
}

func baseRequirement(tool string) requirement { return requirement{tool: tool} }

// unavailable is the refusal of a tool the instance cannot serve: an unsupported capability that names the
// version or the plugin the tool needs.
func (c *Client) unavailable(req requirement, reason string) error {
	message := "this Matomo instance cannot serve the tool: " + reason
	if req.tool != "" {
		message = req.tool + " is not available on this Matomo instance: " + reason
	}
	return &unavailableError{
		UnsupportedError: &capability.UnsupportedError{Connection: c.connection, Capability: req.tool},
		message:          message,
	}
}

// instance returns what is known about the instance: from the cache, or from one API.getMatomoVersion read
// when nothing fresh is held or force is set. The first read of an unknown instance decides how the token is
// sent: with the bearer header alone, and only when Matomo then refuses it as unauthenticated, once more with
// the token in the form; an answer that proves a release from 5.4.0 on after that fails closed, because such
// an instance reads the header and something on the way removes it.
func (c *Client) instance(ctx context.Context, op string, force bool) (instanceInfo, error) {
	if !force {
		if info, ok := c.cached(); ok {
			return info, nil
		}
	}
	bodyToken := false
	raw, err := c.readVersion(ctx, op, false)
	if refusedAccess(err) {
		raw, err = c.readVersion(ctx, op, true)
		bodyToken = err == nil
	}
	if err != nil {
		var answered *answeredError
		if errors.As(err, &answered) && answered.failure.Class == provider.ClassPermission {
			// This read needs a view right on some website; without a usable token Matomo answers the same.
			return instanceInfo{}, &provider.Error{Class: provider.ClassAuth, Op: op,
				Message: "Matomo rejected the token or the token has no view access to any website"}
		}
		return instanceInfo{}, err
	}
	parsed, ok := parseVersion(raw)
	if !ok {
		return instanceInfo{}, provider.InvalidResponse(op, "Matomo returned an invalid version")
	}
	if bodyToken && !parsed.less(headerSince) {
		return instanceInfo{}, &provider.Error{Class: provider.ClassAuth, Op: op,
			Message: "Matomo accepted the token only in the request body; check that the Authorization header reaches it"}
	}
	info := instanceInfo{version: parsed, bodyToken: parsed.less(headerSince), expires: now().Add(instanceTTL)}
	instances.mu.Lock()
	defer instances.mu.Unlock()
	if held, ok := instances.known[c.key]; ok {
		info.plugins = held.plugins
	}
	instances.known[c.key] = &info
	return info, nil
}

// refusedAccess reports Matomo's own refusal of the caller as unauthenticated or without a view right, in
// an HTTP 200 answer. An HTTP 401 or 403 comes from something in front of Matomo and is not retried.
func refusedAccess(err error) bool {
	var answered *answeredError
	if !errors.As(err, &answered) {
		return false
	}
	return answered.failure.Class == provider.ClassAuth || answered.failure.Class == provider.ClassPermission
}

// readVersion performs the API.getMatomoVersion call, which answers {"value":"5.14.2"}.
func (c *Client) readVersion(ctx context.Context, op string, bodyToken bool) (string, error) {
	var raw json.RawMessage
	if err := c.send(ctx, op, "API.getMatomoVersion", nil, bodyToken, &raw); err != nil {
		var missing *missingError
		if errors.As(err, &missing) {
			return "", provider.InvalidResponse(op, "Matomo does not offer a version method")
		}
		return "", err
	}
	var value string
	if scalar(raw, &value) != nil {
		return "", provider.InvalidResponse(op, "Matomo returned an invalid version")
	}
	return value, nil
}

// scalar reads the value of a single-value answer, which Matomo wraps as {"value":...}; a bare scalar passes.
func scalar(raw json.RawMessage, out any) error {
	var wrapped struct {
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Value != nil {
		return json.Unmarshal(wrapped.Value, out)
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) cached() (instanceInfo, bool) {
	instances.mu.Lock()
	defer instances.mu.Unlock()
	held, ok := instances.known[c.key]
	if !ok || !now().Before(held.expires) {
		return instanceInfo{}, false
	}
	return *held, true
}

// ensure proves that the instance can serve a tool: Matomo 5.0 or later, the release the tool needs, and its
// plugin activated. With a known instance it sends nothing.
func (c *Client) ensure(ctx context.Context, op string, req requirement) (instanceInfo, error) {
	info, err := c.instance(ctx, op, false)
	if err != nil {
		return instanceInfo{}, err
	}
	if err := c.supports(info, req); err != nil {
		return instanceInfo{}, err
	}
	if req.plugin != "" {
		active, err := c.pluginActive(ctx, op, req, false)
		if err != nil {
			return instanceInfo{}, err
		}
		if !active {
			return instanceInfo{}, c.unavailable(req, "it needs the Matomo plugin "+req.plugin+" to be activated")
		}
	}
	return info, nil
}

// supports checks the release against Matomo 5.0 and the release the tool names.
func (c *Client) supports(info instanceInfo, req requirement) error {
	if info.version.less(minSupported) {
		return c.unavailable(req, "it needs Matomo "+minSupported.String()+" or newer")
	}
	if info.version.less(req.minVersion) {
		return c.unavailable(req, "it needs Matomo "+req.minVersion.String()+" or newer")
	}
	return nil
}

// pluginActive reports whether the plugin of the requirement is activated: from the cache, or from one
// API.isPluginActivated read when nothing fresh is held or force is set.
func (c *Client) pluginActive(ctx context.Context, op string, req requirement, force bool) (bool, error) {
	if !force {
		instances.mu.Lock()
		held, ok := instances.known[c.key]
		var state pluginState
		if ok {
			state, ok = held.plugins[req.plugin]
		}
		instances.mu.Unlock()
		if ok && now().Before(state.expires) {
			return state.active, nil
		}
	}
	var raw json.RawMessage
	params := url.Values{"pluginName": {req.plugin}}
	info, err := c.instance(ctx, op, false)
	if err != nil {
		return false, err
	}
	if err := c.refuseMissing(req, c.send(ctx, op, "API.isPluginActivated", params, info.bodyToken, &raw)); err != nil {
		return false, err
	}
	var active bool
	if scalar(raw, &active) != nil {
		return false, provider.InvalidResponse(op, "Matomo returned an invalid plugin state")
	}
	instances.mu.Lock()
	defer instances.mu.Unlock()
	if held, ok := instances.known[c.key]; ok {
		if held.plugins == nil {
			held.plugins = map[string]pluginState{}
		}
		held.plugins[req.plugin] = pluginState{active: active, expires: now().Add(instanceTTL)}
	}
	return active, nil
}
