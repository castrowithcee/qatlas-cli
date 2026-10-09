package dav

import (
	"errors"
	"strings"

	"github.com/emersion/go-vcard"
)

// Bounds of the contact model. Every string and every multi-valued property is capped; a photo or other
// binary property is never reported.
const (
	MaxContactText     = 256
	MaxContactNote     = 4096
	MaxContactEntries  = 20
	maxContactType     = 64
	maxContactBirthday = 32
)

// TypedValue is an e-mail address or phone number with its type parameter.
type TypedValue struct {
	Value string `json:"value,omitempty"`
	Type  string `json:"type,omitempty"`
}

// ContactName is the structured name (N) of a contact.
type ContactName struct {
	Family     string `json:"family,omitempty"`
	Given      string `json:"given,omitempty"`
	Additional string `json:"additional,omitempty"`
	Prefix     string `json:"prefix,omitempty"`
	Suffix     string `json:"suffix,omitempty"`
}

// PostalAddress is one ADR of a contact.
type PostalAddress struct {
	Type       string `json:"type,omitempty"`
	POBox      string `json:"po_box,omitempty"`
	Extended   string `json:"extended,omitempty"`
	Street     string `json:"street,omitempty"`
	Locality   string `json:"locality,omitempty"`
	Region     string `json:"region,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	Country    string `json:"country,omitempty"`
}

// Contact is one contact. The list tool reports only the compact members.
type Contact struct {
	ID             string          `json:"id"`
	UID            string          `json:"uid,omitempty"`
	Name           string          `json:"name,omitempty"`
	StructuredName *ContactName    `json:"structured_name,omitempty"`
	Email          string          `json:"email,omitempty"`
	Emails         []TypedValue    `json:"emails,omitempty"`
	Phones         []TypedValue    `json:"phones,omitempty"`
	Addresses      []PostalAddress `json:"addresses,omitempty"`
	Organization   string          `json:"organization,omitempty"`
	Title          string          `json:"title,omitempty"`
	Birthday       string          `json:"birthday,omitempty"`
	Note           string          `json:"note,omitempty"`
	URLs           []string        `json:"urls,omitempty"`
	Truncated      []string        `json:"truncated,omitempty"`
	ETag           string          `json:"etag,omitempty"`
}

var errNoContact = errors.New("no contact")

// ParseContact reads one vCard. With full unset, only the compact members are filled. A photo or other
// binary property is never read.
func ParseContact(data string, full bool) (contact *Contact, err error) {
	defer func() {
		if recover() != nil {
			contact, err = nil, errNoContact
		}
	}()
	card, err := vcard.NewDecoder(strings.NewReader(data)).Decode()
	if err != nil {
		return nil, err
	}
	contact = &Contact{
		UID:          Clean(card.Value(vcard.FieldUID), MaxContactText),
		Name:         Clean(card.PreferredValue(vcard.FieldFormattedName), MaxContactText),
		Organization: orgOf(card.Value(vcard.FieldOrganization)),
	}
	name := card.Name()
	if contact.Name == "" && name != nil {
		contact.Name = Clean(strings.Join(nonEmpty(name.HonorificPrefix, name.GivenName, name.AdditionalName,
			name.FamilyName, name.HonorificSuffix), " "), MaxContactText)
	}
	if !full {
		if field := card.Get(vcard.FieldEmail); field != nil {
			contact.Email = Clean(field.Value, MaxContactText)
		}
		return contact, nil
	}
	var cut []string
	if name != nil {
		contact.StructuredName = &ContactName{
			Family: Clean(name.FamilyName, MaxContactText), Given: Clean(name.GivenName, MaxContactText),
			Additional: Clean(name.AdditionalName, MaxContactText),
			Prefix:     Clean(name.HonorificPrefix, MaxContactText), Suffix: Clean(name.HonorificSuffix, MaxContactText),
		}
	}
	contact.Title = Clean(card.Value(vcard.FieldTitle), MaxContactText)
	contact.Birthday = Clean(card.Value(vcard.FieldBirthday), maxContactBirthday)
	contact.Note = CleanText(card.Value(vcard.FieldNote), MaxContactNote, true)
	if len(strings.TrimSpace(card.Value(vcard.FieldNote))) > MaxContactNote {
		cut = append(cut, "note")
	}
	fields := card[vcard.FieldEmail]
	contact.Emails, cut = typedOf(fields, "emails", cut)
	contact.Phones, cut = typedOf(card[vcard.FieldTelephone], "phones", cut)
	addresses := card.Addresses()
	for i, address := range addresses {
		if i >= MaxContactEntries {
			cut = append(cut, "addresses")
			break
		}
		contact.Addresses = append(contact.Addresses, PostalAddress{
			Type: typeOf(address.Field), POBox: Clean(address.PostOfficeBox, MaxContactText),
			Extended: Clean(address.ExtendedAddress, MaxContactText), Street: Clean(address.StreetAddress, MaxContactText),
			Locality: Clean(address.Locality, MaxContactText), Region: Clean(address.Region, MaxContactText),
			PostalCode: Clean(address.PostalCode, MaxContactText), Country: Clean(address.Country, MaxContactText),
		})
	}
	for i, field := range card[vcard.FieldURL] {
		if i >= MaxContactEntries {
			cut = append(cut, "urls")
			break
		}
		contact.URLs = append(contact.URLs, Clean(field.Value, MaxContactText))
	}
	contact.Truncated = cut
	return contact, nil
}

func typedOf(fields []*vcard.Field, name string, cut []string) ([]TypedValue, []string) {
	var out []TypedValue
	for i, field := range fields {
		if i >= MaxContactEntries {
			return out, append(cut, name)
		}
		out = append(out, TypedValue{Value: Clean(field.Value, MaxContactText), Type: typeOf(field)})
	}
	return out, cut
}

func typeOf(field *vcard.Field) string {
	if field == nil {
		return ""
	}
	return Clean(strings.Join(field.Params.Types(), ","), maxContactType)
}

func orgOf(value string) string {
	return Clean(strings.Join(nonEmpty(strings.Split(value, ";")...), ", "), MaxContactText)
}

func nonEmpty(values ...string) []string {
	var out []string
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}
