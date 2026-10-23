package nextcloud

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// Shared reading of the account, notification, activity, and search members beyond the flat ones. Provider
// text is untrusted: it is cleaned of control characters and bounded, and an address is never fetched.

const (
	maxAddressObjects  = 50
	maxActivityObjects = 50
	maxActivityPreview = 10
	maxHitAttributes   = 20
	maxAttributeKey    = 64
	maxProviderFilters = 20
	maxProviderTrigger = 10
	maxFilterType      = 32

	fieldObjects    = "objects"
	fieldAttributes = "attributes"
	fieldFilters    = "filters"

	filterCursorMark = "~"
)

// optText is a text member that may be absent or of another type; anything but a JSON string counts as absent.
type optText string

func (o *optText) UnmarshalJSON(data []byte) error {
	var value string
	if json.Unmarshal(data, &value) == nil {
		*o = optText(value)
	}
	return nil
}

// plainText cleans a provider text of control characters and cuts it to limit bytes.
func plainText(value string, limit int) (string, bool) {
	return cut(cleanText(value, 1<<20), limit)
}

// addressText cleans an address and cuts it to the bound. An address that leads to a public share or to a
// conversation by its token is left out, because it grants access without a sign-in.
func addressText(value string) (string, bool) {
	value = cleanText(value, 1<<20)
	if value == "" || richAccessAddress(value) {
		return "", false
	}
	return cut(value, maxHitLink)
}

// BackendCapabilities tells which account data the user backend can change.
type BackendCapabilities struct {
	SetDisplayName *bool `json:"set_display_name,omitempty"`
	SetPassword    *bool `json:"set_password,omitempty"`
}

// ContactDetails holds the contact and backend members of a user document.
type ContactDetails struct {
	Phone               string               `json:"phone,omitempty"`
	Address             string               `json:"address,omitempty"`
	Website             string               `json:"website,omitempty"`
	Twitter             string               `json:"twitter,omitempty"`
	Backend             string               `json:"backend,omitempty"`
	BackendCapabilities *BackendCapabilities `json:"backend_capabilities,omitempty"`
}

// rawContact lists the members ContactDetails reads. The storage location and the repeated display-name member
// are not decoded, so they cannot reach a result.
type rawContact struct {
	Phone               optText         `json:"phone"`
	Address             optText         `json:"address"`
	Website             optText         `json:"website"`
	Twitter             optText         `json:"twitter"`
	Backend             optText         `json:"backend"`
	BackendCapabilities json.RawMessage `json:"backendCapabilities"`
}

func contactOf(raw rawContact) (ContactDetails, bool) {
	var details ContactDetails
	var cuts [5]bool
	details.Phone, cuts[0] = plainText(string(raw.Phone), maxAccountText)
	details.Address, cuts[1] = plainText(string(raw.Address), maxAccountText)
	details.Website, cuts[2] = plainText(string(raw.Website), maxAccountText)
	details.Twitter, cuts[3] = plainText(string(raw.Twitter), maxAccountText)
	details.Backend, cuts[4] = plainText(string(raw.Backend), maxActionLabel)
	var capabilities struct {
		SetDisplayName optBool `json:"setDisplayName"`
		SetPassword    optBool `json:"setPassword"`
	}
	if len(raw.BackendCapabilities) > 0 && json.Unmarshal(raw.BackendCapabilities, &capabilities) == nil &&
		(capabilities.SetDisplayName.set || capabilities.SetPassword.set) {
		details.BackendCapabilities = &BackendCapabilities{SetDisplayName: capabilities.SetDisplayName.ptr(),
			SetPassword: capabilities.SetPassword.ptr()}
	}
	return details, cuts[0] || cuts[1] || cuts[2] || cuts[3] || cuts[4]
}

// quotaOf reads the quota member. The storage numbers are required; the free space and the quota setting are
// reported when they are whole numbers, and negative special values stay.
func quotaOf(raw json.RawMessage) *Quota {
	var base struct {
		Used     *int64   `json:"used"`
		Total    *int64   `json:"total"`
		Relative *float64 `json:"relative"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &base) != nil || base.Used == nil || base.Total == nil {
		return nil
	}
	quota := &Quota{Used: *base.Used, Total: *base.Total}
	if base.Relative != nil {
		quota.Relative = *base.Relative
	}
	var extra struct {
		Free  json.RawMessage `json:"free"`
		Quota json.RawMessage `json:"quota"`
	}
	_ = json.Unmarshal(raw, &extra)
	quota.Free = integer(string(extra.Free), math.MinInt64)
	quota.Quota = integer(string(extra.Quota), math.MinInt64)
	return quota
}

// enabledOf reads the enabled member, which arrives as a flag or a number.
func enabledOf(raw json.RawMessage) *bool {
	switch strings.Trim(strings.TrimSpace(string(raw)), `"`) {
	case "true", "1":
		value := true
		return &value
	case "false", "0":
		value := false
		return &value
	}
	return nil
}

// lastLoginOf converts the milliseconds of lastLogin; 0 means the user never signed in.
func lastLoginOf(raw json.Number) string {
	if millis, err := raw.Int64(); err == nil {
		return unixTime(json.Number(strconv.FormatInt(millis/1000, 10)))
	}
	return ""
}

// stringList reads a list of strings; a member of another shape is empty.
func stringList(raw json.RawMessage) []string {
	var values []string
	if json.Unmarshal(raw, &values) != nil {
		return nil
	}
	return values
}

// richObjectsOf maps the parameters of a rich string, an object of placeholder keys, with the shared rich
// object mapping. An empty parameter set arrives as an empty array and has no objects.
func richObjectsOf(raw json.RawMessage) ([]MessageObject, bool) {
	var parameters map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &parameters) != nil {
		return nil, false
	}
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var objects []MessageObject
	truncated := false
	for _, key := range keys {
		var members map[string]json.RawMessage
		if json.Unmarshal(parameters[key], &members) != nil {
			continue
		}
		if len(objects) == maxMessageParams {
			truncated = true
			break
		}
		objects = append(objects, richObjectOf(key, members))
	}
	return objects, truncated
}

// richStringOf reads a rich string that arrives as a template and a parameter object: a pair, or an object with
// the members 0 and 1.
func richStringOf(raw json.RawMessage) (string, json.RawMessage) {
	var pair []json.RawMessage
	if json.Unmarshal(raw, &pair) == nil && len(pair) == 2 {
		var template string
		_ = json.Unmarshal(pair[0], &template)
		return template, pair[1]
	}
	var named map[string]json.RawMessage
	if json.Unmarshal(raw, &named) == nil {
		var template string
		_ = json.Unmarshal(named["0"], &template)
		return template, named["1"]
	}
	return "", nil
}

// filterCursor puts the cursor of the server together with the filters of the request into one cursor.
func filterCursor(binding []byte, inner string) string {
	return filterCursorMark + provider.EncodeCursor(binding, inner)
}

// openFilterCursor returns the cursor of the server an outer cursor continues after. A cursor of the earlier
// format, which carries no filters, is valid only while the request sets no new filter; a bound cursor is
// valid only for the filters it was issued for.
func openFilterCursor(binding []byte, cursor string, filtered bool, plain func(string) bool, max int) (string, bool) {
	if cursor == "" {
		return "", true
	}
	if rest, ok := strings.CutPrefix(cursor, filterCursorMark); ok {
		inner, ok := provider.DecodeCursor(binding, rest, max)
		return inner, ok && plain(inner)
	}
	if filtered || !plain(cursor) {
		return "", false
	}
	return cursor, true
}
