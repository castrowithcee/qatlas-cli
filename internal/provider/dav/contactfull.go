package dav

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/emersion/go-vcard"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// Bounds of the additional contact members.
const (
	maxContactNickname = 256
	maxContactShort    = 64
	maxContactClass    = 32
	maxContactLabel    = 1024
	maxContactCategory = 64
	maxContactAddress  = 1024
	maxContactPref     = 100
)

// ContactLabel is one LABEL (formatted delivery address) of a contact.
type ContactLabel struct {
	Type  string `json:"type,omitempty"`
	Value string `json:"value,omitempty"`
}

// ContactValueProperties are the members the full contact adds to the compact ones, as the properties of a
// JSON schema object without braces; ContactReferenceProperties are its address references.
const ContactValueProperties = `"nicknames":{"type":"array","items":{"type":"string"}},` +
	`"mailer":{"type":"string"},"timezone":{"type":"string"},"geo":{"type":"string"},"role":{"type":"string"},` +
	`"class":{"type":"string"},"sort_string":{"type":"string"},"revision":{"type":"string"},` +
	`"product_id":{"type":"string"},"name_source":{"type":"string"},"profile":{"type":"string"},` +
	`"source":{"type":"array","items":{"type":"string"}},` +
	`"labels":{"type":"array","items":{"type":"object","properties":{"type":{"type":"string"},` +
	`"value":{"type":"string"}},"additionalProperties":false}},` +
	`"categories":{"type":"array","items":{"type":"string"}},` +
	`"organization_units":{"type":"array","items":{"type":"string"}}`

// ContactReferenceProperties are the address references of photo, logo, sound, agent, and key.
const ContactReferenceProperties = `"photo_uri":{"type":"string"},"logo_uri":{"type":"string"},` +
	`"sound_uri":{"type":"string"},"agent_uri":{"type":"string"},"key_uri":{"type":"string"}`

// ContactFullProperties are all members the full contact adds.
const ContactFullProperties = ContactValueProperties + `,` + ContactReferenceProperties

// ContactValueFields describes ContactValueProperties for the capability descriptors.
var ContactValueFields = []capability.Field{
	{Name: "nicknames", Description: "At most 20 nicknames, untrusted data; only the get tool reports them"},
	{Name: "mailer", Description: "Mailer (MAILER), untrusted data; only the get tool reports it"},
	{Name: "timezone", Description: "Time zone (TZ) as the card states it, untrusted data; only the get tool reports it"},
	{Name: "geo", Description: "Position (GEO) as the card states it, \"latitude;longitude\", untrusted data; only the get tool reports it"},
	{Name: "role", Description: "Role (ROLE), untrusted data; only the get tool reports it"},
	{Name: "class", Description: "Classification (CLASS), untrusted data; only the get tool reports it"},
	{Name: "sort_string", Description: "Sort string (SORT-STRING), untrusted data; only the get tool reports it"},
	{Name: "revision", Description: "Revision (REV) as the card states it, untrusted data; only the get tool reports it"},
	{Name: "product_id", Description: "Product identifier (PRODID), untrusted data; only the get tool reports it"},
	{Name: "name_source", Description: "Source name (NAME), untrusted data; only the get tool reports it"},
	{Name: "profile", Description: "Profile (PROFILE), untrusted data; only the get tool reports it"},
	{Name: "source", Description: "At most 20 http or https source addresses (SOURCE), untrusted data, never followed; only the get tool reports them"},
	{Name: "labels", Description: "At most 20 delivery labels with type, untrusted data; only the get tool reports them"},
	{Name: "categories", Description: "At most 20 categories, untrusted data; only the get tool reports them"},
	{Name: "organization_units", Description: "At most 20 organizational units after the organization name, untrusted data; only the get tool reports them"},
}

// ContactReferenceFields describes ContactReferenceProperties for the capability descriptors.
var ContactReferenceFields = []capability.Field{
	{Name: "photo_uri", Description: "http or https address of the photo, untrusted data, never followed; only the get tool reports it"},
	{Name: "logo_uri", Description: "http or https address of the logo, untrusted data, never followed; only the get tool reports it"},
	{Name: "sound_uri", Description: "http or https address of the sound, untrusted data, never followed; only the get tool reports it"},
	{Name: "agent_uri", Description: "http or https address of the agent card, untrusted data, never followed; only the get tool reports it"},
	{Name: "key_uri", Description: "http or https address of the public key, untrusted data, never followed; only the get tool reports it"},
}

// ContactFullFields describes ContactFullProperties for the capability descriptors.
var ContactFullFields = append(append([]capability.Field{}, ContactValueFields...), ContactReferenceFields...)

// fillContactFull reads the properties only the full contact reports and returns the extended cut list.
func fillContactFull(contact *Contact, card vcard.Card, cut []string) []string {
	contact.Mailer = Clean(card.Value("MAILER"), MaxContactText)
	contact.Timezone = Clean(card.Value(vcard.FieldTimezone), maxContactShort)
	contact.Geo = Clean(card.Value(vcard.FieldGeolocation), maxContactShort)
	contact.Role = Clean(card.Value(vcard.FieldRole), MaxContactText)
	contact.Class = Clean(card.Value("CLASS"), maxContactClass)
	contact.SortString = Clean(card.Value("SORT-STRING"), MaxContactText)
	contact.Revision = Clean(card.Value(vcard.FieldRevision), maxContactShort)
	contact.ProductID = Clean(card.Value(vcard.FieldProductID), MaxContactText)
	contact.NameSource = Clean(card.Value("NAME"), MaxContactText)
	contact.Profile = Clean(card.Value("PROFILE"), maxContactClass)
	contact.Nicknames, cut = contactList(card[vcard.FieldNickname], "nicknames", maxContactNickname, cut)
	contact.Categories, cut = contactList(card[vcard.FieldCategories], "categories", maxContactCategory, cut)
	for _, field := range card[vcard.FieldSource] {
		if address := contactAddress(field.Value); address != "" {
			if len(contact.Source) >= MaxContactEntries {
				cut = appendOnce(cut, "source")
				break
			}
			contact.Source = append(contact.Source, address)
		}
	}
	for _, field := range card["LABEL"] {
		value := CleanText(field.Value, maxContactLabel, true)
		if value == "" || field.Params.Get("ENCODING") != "" {
			continue
		}
		if len(contact.Labels) >= MaxContactEntries {
			cut = appendOnce(cut, "labels")
			break
		}
		contact.Labels = append(contact.Labels, ContactLabel{Type: typeOf(field), Value: value})
	}
	// Components after the organization name; the first one stays in Organization alone.
	if parts := strings.Split(card.Value(vcard.FieldOrganization), ";"); len(parts) > 1 {
		for _, unit := range nonEmpty(parts[1:]...) {
			if len(contact.OrganizationUnits) >= MaxContactEntries {
				cut = appendOnce(cut, "organization_units")
				break
			}
			contact.OrganizationUnits = append(contact.OrganizationUnits, Clean(unit, MaxContactText))
		}
	}
	contact.PhotoURI = contactReference(card[vcard.FieldPhoto])
	contact.LogoURI = contactReference(card[vcard.FieldLogo])
	contact.SoundURI = contactReference(card[vcard.FieldSound])
	contact.AgentURI = contactReference(card["AGENT"])
	contact.KeyURI = contactReference(card[vcard.FieldKey])
	return cut
}

// contactList splits comma-separated text properties into a bounded list.
func contactList(fields []*vcard.Field, name string, max int, cut []string) ([]string, []string) {
	var out []string
	for _, field := range fields {
		for _, part := range strings.Split(field.Value, ",") {
			part = Clean(part, max)
			if part == "" {
				continue
			}
			if len(out) >= MaxContactEntries {
				return out, appendOnce(cut, name)
			}
			out = append(out, part)
		}
	}
	return out, cut
}

func appendOnce(list []string, name string) []string {
	for _, item := range list {
		if item == name {
			return list
		}
	}
	return append(list, name)
}

// contactReference returns the first usable address of a property that carries a value or a reference. Only
// VALUE=uri counts; embedded data (ENCODING) and an embedded card never do.
func contactReference(fields []*vcard.Field) string {
	for _, field := range fields {
		if field.Params.Get("ENCODING") != "" || !strings.EqualFold(field.Params.Get(vcard.ParamValue), "uri") {
			continue
		}
		if address := contactAddress(field.Value); address != "" {
			return address
		}
	}
	return ""
}

// contactAddress accepts a plain http or https address. One with user information, a query, or a fragment
// can carry an access token and is left out; so is one over the bound, rather than cut.
func contactAddress(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxContactAddress || !utf8.ValidString(value) ||
		strings.ContainsAny(value, "?#\\") || strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r)
	}) >= 0 {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Host == "" || strings.Contains(parsed.Host, "@") ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return value
}

// contactPref reads the PREF parameter of vCard 4.0, 1 to 100; anything else is left out.
func contactPref(field *vcard.Field) int {
	n, err := strconv.Atoi(strings.TrimSpace(field.Params.Get(vcard.ParamPreferred)))
	if err != nil || n < 1 || n > maxContactPref {
		return 0
	}
	return n
}
