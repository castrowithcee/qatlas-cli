package dav

import (
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// Bounds of the collection properties shared by the DAV providers.
const (
	// MaxSyncTokenLength caps a reported sync token in bytes.
	MaxSyncTokenLength = 512

	maxPropertyLabel    = 64
	maxPrivilegesListed = 32
	maxFormatsListed    = 8
	maxCollationsListed = 16
)

// CollectionFormat is one supported data format of a collection.
type CollectionFormat struct {
	ContentType string `json:"content_type,omitempty"`
	Version     string `json:"version,omitempty"`
}

// CollectionProperties are the standardized properties of a calendar or address book that every DAV provider
// reports under the same names, types, and bounds. Every member is optional.
type CollectionProperties struct {
	CalendarData            []CollectionFormat `json:"calendar_data,omitempty"`
	AddressData             []CollectionFormat `json:"address_data,omitempty"`
	MaxResourceSize         *uint64            `json:"max_resource_size,omitempty"`
	MinDateTime             string             `json:"min_date_time,omitempty"`
	MaxDateTime             string             `json:"max_date_time,omitempty"`
	MaxInstances            *uint64            `json:"max_instances,omitempty"`
	MaxAttendeesPerInstance *uint64            `json:"max_attendees_per_instance,omitempty"`
	Timezone                string             `json:"timezone,omitempty"`
	Collations              []string           `json:"collations,omitempty"`
	Privileges              []string           `json:"privileges,omitempty"`
	SyncToken               string             `json:"sync_token,omitempty"`
}

// CollectionPropertiesOf reads the properties of one collection from a parsed node. A property that is
// absent or unreadable is left out, never guessed. Calendar-only members stay empty for an address book.
func CollectionPropertiesOf(r *Resource, calendars bool) CollectionProperties {
	p := CollectionProperties{
		Collations:      Names(r.Collations, maxCollationsListed),
		Privileges:      Names(r.Privileges, maxPrivilegesListed),
		MaxResourceSize: numberOf(r.Text[KeyMaxSize]),
		SyncToken:       Clean(r.Text[KeySyncToken], MaxSyncTokenLength),
	}
	if !calendars {
		p.AddressData = formatsOf(r.AddressData)
		return p
	}
	p.CalendarData = formatsOf(r.CalendarData)
	p.MinDateTime = stampOf(r.Text[KeyMinDate])
	p.MaxDateTime = stampOf(r.Text[KeyMaxDate])
	p.MaxInstances = numberOf(r.Text[KeyMaxInstances])
	p.MaxAttendeesPerInstance = numberOf(r.Text[KeyMaxAttendees])
	p.Timezone = TimezoneID(r.Text[KeyTimezone])
	return p
}

// Names cleans a list of provider names and keeps at most limit non-empty ones.
func Names(values []string, limit int) []string {
	var out []string
	for _, value := range values {
		if value = Clean(value, maxPropertyLabel); value != "" && len(out) < limit {
			out = append(out, value)
		}
	}
	return out
}

func formatsOf(values []DataFormat) []CollectionFormat {
	var out []CollectionFormat
	for _, value := range values {
		format := CollectionFormat{ContentType: Clean(value.ContentType, maxPropertyLabel),
			Version: Clean(value.Version, maxPropertyLabel)}
		if format != (CollectionFormat{}) && len(out) < maxFormatsListed {
			out = append(out, format)
		}
	}
	return out
}

// numberOf accepts only plain decimal digits that fit an unsigned 64-bit integer.
func numberOf(value string) *uint64 {
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

// stampOf rewrites the CalDAV UTC form (20060102T150405Z) to RFC 3339; anything else is dropped.
func stampOf(value string) string {
	t, err := time.Parse(utcStampLayout, value)
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// TimezoneID returns the TZID of the first VTIMEZONE in a calendar-timezone value. The value may be cut at
// the parser's text limit, so a TZID line counts only when a complete line follows it: a cut line never
// yields a partial name.
func TimezoneID(value string) string {
	value = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(value)
	var lines []string
	for _, line := range strings.Split(value, "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if len(lines) > 0 {
				lines[len(lines)-1] += line[1:]
			}
			continue
		}
		lines = append(lines, line)
	}
	inZone := false
	for i, line := range lines {
		upper := strings.ToUpper(line)
		switch {
		case upper == "BEGIN:VTIMEZONE":
			inZone = true
		case upper == "END:VTIMEZONE":
			return ""
		case inZone && (strings.HasPrefix(upper, "TZID:") || strings.HasPrefix(upper, "TZID;")):
			colon := strings.Index(line, ":")
			if i == len(lines)-1 || colon < 0 {
				return ""
			}
			return Clean(line[colon+1:], maxPropertyLabel)
		}
	}
	return ""
}

const (
	propertyStringList = `{"type":"array","items":{"type":"string"}}`
	propertyFormatList = `{"type":"array","items":{"type":"object","properties":{"content_type":{"type":"string"},` +
		`"version":{"type":"string"}},"additionalProperties":false}}`
	propertyCommon = `"max_resource_size":{"type":"integer","minimum":0},"collations":` + propertyStringList +
		`,"privileges":` + propertyStringList + `,"sync_token":{"type":"string"}`
)

// CalendarPropertiesSchema and AddressbookPropertiesSchema are the JSON Schema members, without braces, of the
// properties CollectionPropertiesOf reports.
const (
	CalendarPropertiesSchema = `"calendar_data":` + propertyFormatList + `,` + propertyCommon + `,` +
		`"min_date_time":{"type":"string"},"max_date_time":{"type":"string"},` +
		`"max_instances":{"type":"integer","minimum":0},` +
		`"max_attendees_per_instance":{"type":"integer","minimum":0},"timezone":{"type":"string"}`
	AddressbookPropertiesSchema = `"address_data":` + propertyFormatList + `,` + propertyCommon
)

// CalendarPropertyFields and AddressbookPropertyFields describe the properties for the capability descriptors.
var (
	commonPropertyFields = []capability.Field{
		{Name: "max_resource_size", Description: "Largest accepted object in bytes"},
		{Name: "collations", Description: "Supported text collations, at most 16"},
		{Name: "privileges", Description: "Privileges of the bound identity in the DAV: namespace, at most 32"},
		{Name: "sync_token", Description: "Sync token of the collection, untrusted data"},
	}
	CalendarPropertyFields = append([]capability.Field{
		{Name: "calendar_data", Description: "Supported data formats as content_type and version, at most 8"},
		{Name: "min_date_time", Description: "Earliest accepted date as RFC 3339 in UTC"},
		{Name: "max_date_time", Description: "Latest accepted date as RFC 3339 in UTC"},
		{Name: "max_instances", Description: "Most instances of one recurring event"},
		{Name: "max_attendees_per_instance", Description: "Most attendees of one event instance"},
		{Name: "timezone", Description: "TZID of the default time zone, untrusted data"},
	}, commonPropertyFields...)
	AddressbookPropertyFields = append([]capability.Field{
		{Name: "address_data", Description: "Supported data formats as content_type and version, at most 8"},
	}, commonPropertyFields...)
)
