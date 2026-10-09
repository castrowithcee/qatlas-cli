package dav

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-vcard"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	vcardVersion = "3.0"
	maxPhoneLen  = 32
)

var (
	phonePattern = regexp.MustCompile(`^[0-9+()./ -]+$`)
	// contactTypes is the fixed allow-list of type parameters; "other" writes no type.
	contactTypes = []string{"home", "work", "cell", "voice", "fax", "other"}
	// modeledFields are the vCard properties an update can reproduce; a card with any other property is refused.
	modeledFields = map[string]bool{"VERSION": true, "UID": true, "FN": true, "N": true, "EMAIL": true, "TEL": true,
		"ADR": true, "ORG": true, "TITLE": true, "BDAY": true, "NOTE": true, "URL": true, "PRODID": true, "REV": true}
)

// ContactInput is the structured content of a contact to write.
type ContactInput struct {
	Name           string          `json:"name"`
	StructuredName *ContactName    `json:"structured_name"`
	Emails         []TypedValue    `json:"emails"`
	Phones         []TypedValue    `json:"phones"`
	Addresses      []PostalAddress `json:"addresses"`
	Organization   string          `json:"organization"`
	Title          string          `json:"title"`
	Birthday       string          `json:"birthday"`
	Note           string          `json:"note"`
	URLs           []string        `json:"urls"`
}

// component checks one single-line text member; structured members may not hold the ";" separator.
func component(value string, structured bool) (string, bool) {
	text, ok := ValidText(value, MaxContactText, false)
	if !ok || (structured && strings.Contains(text, ";")) {
		return "", false
	}
	return text, true
}

// typeParams validates a type against the fixed allow-list; "other" and an empty type write none.
func typeParams(kind string) (vcard.Params, bool) {
	kind = strings.ToLower(kind)
	if kind == "" || kind == "other" {
		return vcard.Params{}, true
	}
	for _, allowed := range contactTypes {
		if kind == allowed {
			return vcard.Params{vcard.ParamType: {kind}}, true
		}
	}
	return nil, false
}

func validURL(value string) bool {
	text, ok := ValidText(value, MaxContactText, false)
	if !ok || text == "" || strings.ContainsAny(text, " \t") {
		return false
	}
	parsed, err := url.Parse(text)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" &&
		parsed.User == nil
}

// NewContactCard validates every argument without any I/O and returns the card without its UID. No message
// quotes a value.
func NewContactCard(op string, input ContactInput) (vcard.Card, error) {
	card := vcard.Card{}
	card.SetValue(vcard.FieldVersion, vcardVersion)
	name, ok := ValidText(input.Name, MaxContactText, false)
	if !ok || strings.TrimSpace(name) == "" {
		return nil, provider.Fail(op, "name is required, at most 256 bytes, without control characters or line breaks")
	}
	card.SetValue(vcard.FieldFormattedName, name)
	structured := ContactName{}
	if input.StructuredName != nil {
		structured = *input.StructuredName
	}
	parts := []*string{&structured.Family, &structured.Given, &structured.Additional, &structured.Prefix, &structured.Suffix}
	for _, part := range parts {
		if *part, ok = component(*part, true); !ok {
			return nil, provider.Fail(op, "structured_name parts must be at most 256 bytes without control characters or semicolons")
		}
	}
	card.SetName(&vcard.Name{FamilyName: structured.Family, GivenName: structured.Given,
		AdditionalName: structured.Additional, HonorificPrefix: structured.Prefix, HonorificSuffix: structured.Suffix})
	if len(input.Emails) > MaxContactEntries || len(input.Phones) > MaxContactEntries ||
		len(input.Addresses) > MaxContactEntries || len(input.URLs) > MaxContactEntries {
		return nil, provider.Fail(op, "a contact takes at most 20 e-mail addresses, phone numbers, addresses, and URLs each")
	}
	for _, entry := range input.Emails {
		params, typed := typeParams(entry.Type)
		if !typed || !ValidAddress(entry.Value) {
			return nil, provider.Fail(op, "each e-mail needs a plain e-mail address and a type from home, work, cell, voice, fax, other")
		}
		card.Add(vcard.FieldEmail, &vcard.Field{Value: entry.Value, Params: params})
	}
	for _, entry := range input.Phones {
		params, typed := typeParams(entry.Type)
		digit := strings.ContainsAny(entry.Value, "0123456789")
		if !typed || !digit || len(entry.Value) > maxPhoneLen || !phonePattern.MatchString(entry.Value) {
			return nil, provider.Fail(op, "each phone needs digits with + ( ) . / - and spaces, at most 32 bytes, and a type from home, work, cell, voice, fax, other")
		}
		card.Add(vcard.FieldTelephone, &vcard.Field{Value: entry.Value, Params: params})
	}
	for _, entry := range input.Addresses {
		params, typed := typeParams(entry.Type)
		fields := []*string{&entry.POBox, &entry.Extended, &entry.Street, &entry.Locality, &entry.Region,
			&entry.PostalCode, &entry.Country}
		empty := true
		for _, field := range fields {
			if *field, ok = component(*field, true); !ok {
				typed = false
			}
			empty = empty && *field == ""
		}
		if !typed || empty {
			return nil, provider.Fail(op, "each address needs a type from home, work, cell, voice, fax, other and at least one part of at most 256 bytes without control characters or semicolons")
		}
		address := vcard.Address{PostOfficeBox: entry.POBox, ExtendedAddress: entry.Extended, StreetAddress: entry.Street,
			Locality: entry.Locality, Region: entry.Region, PostalCode: entry.PostalCode, Country: entry.Country}
		card.AddAddress(&address)
		card[vcard.FieldAddress][len(card[vcard.FieldAddress])-1].Params = params
	}
	if input.Organization != "" {
		org, ok := component(input.Organization, true)
		if !ok {
			return nil, provider.Fail(op, "organization must be at most 256 bytes without control characters or semicolons")
		}
		card.SetValue(vcard.FieldOrganization, org)
	}
	if input.Title != "" {
		title, ok := component(input.Title, false)
		if !ok {
			return nil, provider.Fail(op, "title must be at most 256 bytes without control characters")
		}
		card.SetValue(vcard.FieldTitle, title)
	}
	if input.Birthday != "" {
		if _, err := time.Parse(dateLayout, input.Birthday); err != nil {
			return nil, provider.Fail(op, "birthday must be a date YYYY-MM-DD")
		}
		card.SetValue(vcard.FieldBirthday, input.Birthday)
	}
	if input.Note != "" {
		note, ok := ValidText(input.Note, MaxContactNote, true)
		if !ok {
			return nil, provider.Fail(op, "note must be at most 4096 bytes without control characters")
		}
		card.SetValue(vcard.FieldNote, note)
	}
	for _, entry := range input.URLs {
		if !validURL(entry) {
			return nil, provider.Fail(op, "each URL must be an http or https URL without credentials, at most 256 bytes")
		}
		card.Add(vcard.FieldURL, &vcard.Field{Value: entry})
	}
	if _, err := EncodeContact(op, card, "placeholder"); err != nil {
		return nil, err
	}
	return card, nil
}

// EncodeContact renders the card with its UID. The encoder escapes line breaks, backslashes, and commas, and
// every other value is free of control characters, so no argument can start a property of its own.
func EncodeContact(op string, card vcard.Card, uid string) (string, error) {
	card.SetValue(vcard.FieldUID, uid)
	var buf bytes.Buffer
	if err := vcard.NewEncoder(&buf).Encode(card); err != nil || buf.Len() > MaxEventBytes {
		return "", provider.Fail(op, "the contact could not be encoded within the size limit")
	}
	return buf.String(), nil
}

// StoredContactUID reads the UID of the stored card. It refuses a card that holds a group kind or any property
// Qatlas does not model, such as a photo, so an update never silently drops data.
func (s Server) StoredContactUID(op string, data []byte) (uid string, err error) {
	notValid := s.Name + " returned a contact that is not a valid vCard"
	defer func() {
		if recover() != nil {
			uid, err = "", provider.InvalidResponse(op, notValid)
		}
	}()
	card, decodeErr := vcard.NewDecoder(bytes.NewReader(data)).Decode()
	if decodeErr != nil {
		return "", provider.InvalidResponse(op, notValid)
	}
	for key := range card {
		if !modeledFields[strings.ToUpper(key)] {
			return "", provider.Fail(op, "this contact holds a photo, a group, or other properties that Qatlas does not write; change it in a contacts application")
		}
	}
	uid, ok := ValidText(card.Value(vcard.FieldUID), MaxContactText, false)
	if !ok || strings.TrimSpace(uid) == "" {
		return "", provider.InvalidResponse(op, s.Name+" returned a contact without a usable UID")
	}
	return uid, nil
}
