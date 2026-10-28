package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
)

const (
	// MinVersion is the oldest release that can be listed or installed.
	MinVersion = "v0.30.0"
	// listedStable is how many of the newest stable releases the list shows.
	listedStable = 10
	// maxReleasePages bounds the pagination so a misbehaving server cannot keep the reader looping.
	maxReleasePages = 20
	signatureAsset  = "checksums.txt.sig"
)

// releasePageSize is a variable so a test can paginate with few releases.
var releasePageSize = 100

// TargetError reports a release that cannot be chosen, with the reason in words for the person.
type TargetError struct{ Reason string }

func (e *TargetError) Error() string { return e.Reason }

// Entry is one release a person can choose.
type Entry struct {
	Version    string
	Prerelease bool
	// Current marks the running version.
	Current bool
	// Older marks a version older than the running one.
	Older bool
}

// Target is a release chosen for installation, resolved and checked but not yet downloaded.
type Target struct {
	Version    string
	Prerelease bool
	// Current reports that Version is the running version; installing it changes nothing.
	Current bool
	// Older reports that Version is older than the running version: installing it is a rollback.
	Older bool
	// running is the running version, for the result.
	running string
	rel     release
}

// minimum is the parsed MinVersion.
func minimum() semver {
	v, err := parseSemver(MinVersion)
	if err != nil {
		panic(err)
	}
	return v
}

// eligible reports whether r is a release a person may choose: published, signed, a valid version, and not
// older than MinVersion.
func eligible(r release) (semver, bool) {
	if r.Draft {
		return semver{}, false
	}
	v, err := parseSemver(r.TagName)
	if err != nil || v.compare(minimum()) < 0 {
		return semver{}, false
	}
	return v, hasSignature(r)
}

func hasSignature(r release) bool {
	for _, a := range r.Assets {
		if a.Name == signatureAsset {
			return true
		}
	}
	return false
}

// List returns the releases a person can choose, newest first: the ten newest stable releases, every
// prerelease newer than the newest stable one, and the running version when it is such a release. Only
// signed releases from MinVersion on appear.
func (c *Client) List(ctx context.Context) ([]Entry, error) {
	current, err := c.currentVersion()
	if err != nil {
		return nil, err
	}
	all, err := c.releases(ctx)
	if err != nil {
		return nil, err
	}
	type item struct {
		entry Entry
		v     semver
	}
	var items []item
	for _, r := range all {
		v, ok := eligible(r)
		if !ok {
			continue
		}
		cmp := v.compare(current)
		items = append(items, item{Entry{Version: r.TagName, Prerelease: r.Prerelease, Current: cmp == 0, Older: cmp < 0}, v})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].v.compare(items[j].v) > 0 })
	var out []Entry
	stable := 0
	for _, it := range items {
		switch {
		case it.entry.Current:
		case it.entry.Prerelease:
			// A prerelease is only offered while it is newer than the newest stable release.
			if stable > 0 {
				continue
			}
		default:
			if stable >= listedStable {
				continue
			}
		}
		if !it.entry.Prerelease {
			stable++
		}
		out = append(out, it.entry)
	}
	return out, nil
}

// releases reads every page of the repository's release list.
func (c *Client) releases(ctx context.Context) ([]release, error) {
	var all []release
	for page := 1; page <= maxReleasePages; page++ {
		endpoint := c.baseURL() + "/releases?per_page=" + strconv.Itoa(releasePageSize) + "&page=" + strconv.Itoa(page)
		body, err := c.request(ctx, endpoint, maxMetadataBytes, "application/vnd.github+json", nil)
		if err != nil {
			return nil, fmt.Errorf("list releases: %w", err)
		}
		var batch []release
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, fmt.Errorf("decode release list: %w", err)
		}
		all = append(all, batch...)
		if len(batch) < releasePageSize {
			break
		}
	}
	return all, nil
}

// Target resolves tag to a release that can be installed. It refuses a tag that is not a signed release
// from MinVersion on. A version older than the running one is allowed and marked Older.
func (c *Client) Target(ctx context.Context, tag string) (Target, error) {
	current, err := c.currentVersion()
	if err != nil {
		return Target{}, err
	}
	v, err := parseSemver(tag)
	if err != nil {
		return Target{}, &TargetError{fmt.Sprintf("%q is not a release version: %v", tag, err)}
	}
	if v.compare(minimum()) < 0 {
		return Target{}, &TargetError{fmt.Sprintf("%s is older than %s, the oldest version qatlas installs", tag, MinVersion)}
	}
	body, err := c.request(ctx, c.baseURL()+"/releases/tags/"+url.PathEscape(tag), maxMetadataBytes, "application/vnd.github+json", nil)
	if err != nil {
		return Target{}, fmt.Errorf("look up release %s: %w", tag, err)
	}
	var r release
	if err := json.Unmarshal(body, &r); err != nil {
		return Target{}, fmt.Errorf("decode release %s: %w", tag, err)
	}
	if r.TagName != tag || r.Draft {
		return Target{}, &TargetError{fmt.Sprintf("release %s does not exist", tag)}
	}
	if !hasSignature(r) {
		return Target{}, &TargetError{fmt.Sprintf("release %s is not signed and cannot be chosen", tag)}
	}
	cmp := v.compare(current)
	return Target{Version: tag, Prerelease: r.Prerelease, Current: cmp == 0, Older: cmp < 0, running: c.Version, rel: r}, nil
}

// Prepare downloads and verifies the release t names without installing it. It refuses the running
// version, which has nothing to install.
func (c *Client) Prepare(ctx context.Context, t Target) (*Prepared, error) {
	if t.Current {
		return nil, &TargetError{fmt.Sprintf("%s is the running version", t.Version)}
	}
	return c.prepareRelease(ctx, Result{Current: t.running, Latest: t.Version, UpdateAvailable: true}, t.rel)
}

// UpdateTo installs the release t names, through the same download, verification, and replacement as
// Update. Nothing is changed when t is the running version. An older version is refused here: a rollback
// goes through Prepare, a check of the program against the configuration, and InstallPrepared.
func (c *Client) UpdateTo(ctx context.Context, t Target) (Result, error) {
	if t.Older {
		return Result{}, &TargetError{fmt.Sprintf("%s is older than the running version and is installed as a checked rollback", t.Version)}
	}
	result := Result{Current: t.running, Latest: t.Version, UpdateAvailable: !t.Current}
	if t.Current {
		return result, nil
	}
	return c.installRelease(ctx, result, t.rel)
}
