package telegram

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// personalDataSensitivity classifies tools that send locations, places, or contact details.
const personalDataSensitivity = "telegram-personal-data"

// Limits of the Bot API for sendLocation. Telegram fixes no text limits for venues and contacts; the caps
// below keep the values bounded and are stricter than any display field.
const (
	maxHorizontalAccuracy = 1500
	minLivePeriod         = 60
	maxLivePeriod         = 86400
	// foreverLivePeriod keeps a live location updatable for as long as Telegram allows.
	foreverLivePeriod      = 0x7FFFFFFF
	minHeading             = 1
	maxHeading             = 360
	minProximityRadius     = 1
	maxProximityRadius     = 100000
	maxVenueTitleRunes     = 256
	maxVenueAddressRunes   = 512
	maxContactPhoneRunes   = 64
	maxContactNameRunes    = 64
	structuredResponseOver = defaultResponseBytes
)

// diceEmoji is the fixed list of sendDice emoji of the Bot API.
var diceEmoji = []string{"\U0001f3b2", "\U0001f3af", "\U0001f3c0", "⚽", "\U0001f3b3", "\U0001f3b0"}

func structuredRisk(sensitivity string) capability.Risk {
	return capability.Risk{
		Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: sensitivity,
	}
}

const (
	structuredCommonSchema = `"reply_to_message_id":{"type":"integer","minimum":1},` +
		`"message_thread_id":{"type":"integer","minimum":1},` +
		`"disable_notification":{"type":"boolean"},"protect_content":{"type":"boolean"}`
	structuredOutputSchema = `{"type":"object","properties":{"message_id":{"type":"integer"},` +
		`"date":{"type":"integer"}},"required":["message_id","date"],"additionalProperties":false}`
	coordinateSchema = `"latitude":{"type":"number","minimum":-90,"maximum":90},` +
		`"longitude":{"type":"number","minimum":-180,"maximum":180}`
)

var structuredCommonArguments = []capability.Argument{
	{Name: "reply_to_message_id", Description: "Message of the same chat to reply to"},
	{Name: "message_thread_id", Description: "Forum topic to send into"},
	{Name: "disable_notification", Description: "Send silently"},
	{Name: "protect_content", Description: "Forbid forwarding and saving of the message"},
}

var structuredFields = []capability.Field{
	{Name: "message_id", Description: "Telegram message identifier of the sent message"},
	{Name: "date", Description: "Telegram send time as Unix time"},
}

func structuredArguments(specific ...capability.Argument) []capability.Argument {
	args := append([]capability.Argument{chatArgument}, specific...)
	return append(args, structuredCommonArguments...)
}

var locationsSend = capability.Descriptor{
	ID:          Provider + ".locations.send",
	Version:     1,
	Title:       "Send a Telegram location",
	Description: "Send one location, optionally live, to a bound chat of an explicit Telegram connection",
	Tags:        []string{"telegram", "locations", "send"},
	Risk:        structuredRisk(personalDataSensitivity),
	Provider:    Provider,
	Group:       groupInteractions,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + coordinateSchema + `,` +
		`"horizontal_accuracy":{"type":"number","minimum":0,"maximum":1500},` +
		`"live_period":{"type":"integer","minimum":60,"maximum":2147483647},` +
		`"heading":{"type":"integer","minimum":1,"maximum":360},` +
		`"proximity_alert_radius":{"type":"integer","minimum":1,"maximum":100000},` +
		structuredCommonSchema + `},"required":["latitude","longitude"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(structuredOutputSchema),
	Arguments: structuredArguments(
		capability.Argument{Name: "latitude", Description: "Latitude from -90 through 90", Required: true},
		capability.Argument{Name: "longitude", Description: "Longitude from -180 through 180", Required: true},
		capability.Argument{Name: "horizontal_accuracy", Description: "Radius of uncertainty in meters, from 0 through 1500"},
		capability.Argument{Name: "live_period", Description: "Makes the location live: seconds from 60 through 86400, " +
			"or 2147483647 for as long as Telegram allows"},
		capability.Argument{Name: "heading", Description: "Live only: direction of movement in degrees, from 1 through 360"},
		capability.Argument{Name: "proximity_alert_radius", Description: "Live only: alert distance in meters, from 1 through 100000"},
	),
	Fields: structuredFields,
	Examples: []capability.Example{{
		Description: "Send a location to this connection's chat",
		Arguments:   json.RawMessage(`{"latitude":52.52,"longitude":13.405}`),
	}},
}

var venuesSend = capability.Descriptor{
	ID:          Provider + ".venues.send",
	Version:     1,
	Title:       "Send a Telegram venue",
	Description: "Send one venue with title and address to a bound chat of an explicit Telegram connection",
	Tags:        []string{"telegram", "venues", "send"},
	Risk:        structuredRisk(personalDataSensitivity),
	Provider:    Provider,
	Group:       groupInteractions,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + coordinateSchema + `,` +
		`"title":{"type":"string","minLength":1,"maxLength":256},"address":{"type":"string","minLength":1,"maxLength":512},` +
		structuredCommonSchema + `},"required":["latitude","longitude","title","address"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(structuredOutputSchema),
	Arguments: structuredArguments(
		capability.Argument{Name: "latitude", Description: "Latitude from -90 through 90", Required: true},
		capability.Argument{Name: "longitude", Description: "Longitude from -180 through 180", Required: true},
		capability.Argument{Name: "title", Description: "Name of the venue, from 1 through 256 characters", Required: true},
		capability.Argument{Name: "address", Description: "Address of the venue, from 1 through 512 characters", Required: true},
	),
	Fields: structuredFields,
	Examples: []capability.Example{{
		Description: "Send a venue to this connection's chat",
		Arguments:   json.RawMessage(`{"latitude":52.52,"longitude":13.405,"title":"Office","address":"Example Street 1"}`),
	}},
}

var contactsSend = capability.Descriptor{
	ID:          Provider + ".contacts.send",
	Version:     1,
	Title:       "Send a Telegram contact",
	Description: "Send one phone contact to a bound chat of an explicit Telegram connection",
	Tags:        []string{"telegram", "contacts", "send"},
	Risk:        structuredRisk(personalDataSensitivity),
	Provider:    Provider,
	Group:       groupInteractions,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"phone_number":{"type":"string","minLength":1,"maxLength":64},` +
		`"first_name":{"type":"string","minLength":1,"maxLength":64},` +
		`"last_name":{"type":"string","minLength":1,"maxLength":64},` +
		structuredCommonSchema + `},"required":["phone_number","first_name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(structuredOutputSchema),
	Arguments: structuredArguments(
		capability.Argument{Name: "phone_number", Description: "Phone number, from 1 through 64 characters", Required: true},
		capability.Argument{Name: "first_name", Description: "First name, from 1 through 64 characters", Required: true},
		capability.Argument{Name: "last_name", Description: "Last name, from 1 through 64 characters"},
	),
	Fields: structuredFields,
	Examples: []capability.Example{{
		Description: "Send a contact to this connection's chat",
		Arguments:   json.RawMessage(`{"phone_number":"030 1234567","first_name":"Alex","last_name":"Example"}`),
	}},
}

var diceSend = capability.Descriptor{
	ID:          Provider + ".dice.send",
	Version:     1,
	Title:       "Send a Telegram dice",
	Description: "Send one animated dice emoji with a random value to a bound chat of an explicit Telegram connection",
	Tags:        []string{"telegram", "dice", "send"},
	Risk:        structuredRisk(dataSensitivity),
	Provider:    Provider,
	Group:       groupInteractions,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` +
		`"emoji":{"type":"string","enum":["🎲","🎯","🏀","⚽","🎳","🎰"]},` +
		structuredCommonSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(structuredOutputSchema),
	Arguments: structuredArguments(
		capability.Argument{Name: "emoji", Description: "One of 🎲 (default), 🎯, 🏀, ⚽, 🎳, 🎰"},
	),
	Fields: structuredFields,
	Examples: []capability.Example{{
		Description: "Roll a dice in this connection's chat",
		Arguments:   json.RawMessage(`{"emoji":"🎯"}`),
	}},
}

// SendCommon holds the optional arguments every structured send shares.
type SendCommon struct {
	ReplyToMessageID    int64 `json:"reply_to_message_id"`
	MessageThreadID     int64 `json:"message_thread_id"`
	DisableNotification bool  `json:"disable_notification"`
	ProtectContent      bool  `json:"protect_content"`
}

func (o SendCommon) validate(op string) error {
	if o.ReplyToMessageID < 0 || o.MessageThreadID < 0 {
		return providerError(op, "the reply and topic identifiers must be positive")
	}
	return nil
}

// commonBody is embedded last into each request body so its fields are flattened.
type commonBody struct {
	MessageThreadID     int64            `json:"message_thread_id,omitempty"`
	DisableNotification bool             `json:"disable_notification,omitempty"`
	ProtectContent      bool             `json:"protect_content,omitempty"`
	ReplyParameters     *replyParameters `json:"reply_parameters,omitempty"`
}

func (o SendCommon) body() commonBody {
	b := commonBody{MessageThreadID: o.MessageThreadID, DisableNotification: o.DisableNotification,
		ProtectContent: o.ProtectContent}
	if o.ReplyToMessageID > 0 {
		b.ReplyParameters = &replyParameters{MessageID: o.ReplyToMessageID}
	}
	return b
}

func validCoordinates(op string, latitude, longitude *float64) error {
	if latitude == nil || longitude == nil || *latitude < -90 || *latitude > 90 || *longitude < -180 || *longitude > 180 {
		return providerError(op, "latitude must be from -90 through 90 and longitude from -180 through 180")
	}
	return nil
}

func boundedText(s string, max int) bool {
	n := utf8.RuneCountInString(s)
	return utf8.ValidString(s) && n >= 1 && n <= max
}

// LocationOptions are the options of one sendLocation request.
type LocationOptions struct {
	Latitude             *float64 `json:"latitude"`
	Longitude            *float64 `json:"longitude"`
	HorizontalAccuracy   *float64 `json:"horizontal_accuracy"`
	LivePeriod           *int64   `json:"live_period"`
	Heading              *int64   `json:"heading"`
	ProximityAlertRadius *int64   `json:"proximity_alert_radius"`
	SendCommon
}

func (o LocationOptions) validate() error {
	const op = "send location"
	if err := validCoordinates(op, o.Latitude, o.Longitude); err != nil {
		return err
	}
	if err := validAccuracyAndPeriod(op, o.HorizontalAccuracy, o.LivePeriod); err != nil {
		return err
	}
	if o.LivePeriod == nil && (o.Heading != nil || o.ProximityAlertRadius != nil) {
		return providerError(op, "heading and proximity_alert_radius need a live_period")
	}
	if err := validHeadingAndRadius(op, o.Heading, o.ProximityAlertRadius); err != nil {
		return err
	}
	return o.SendCommon.validate(op)
}

func validAccuracyAndPeriod(op string, accuracy *float64, period *int64) error {
	if accuracy != nil && (*accuracy < 0 || *accuracy > maxHorizontalAccuracy) {
		return providerError(op, "horizontal_accuracy must be from 0 through 1500 meters")
	}
	if period != nil && (*period < minLivePeriod || (*period > maxLivePeriod && *period != foreverLivePeriod)) {
		return providerError(op, "live_period must be from 60 through 86400 seconds or 2147483647")
	}
	return nil
}

func validHeadingAndRadius(op string, heading, radius *int64) error {
	if heading != nil && (*heading < minHeading || *heading > maxHeading) {
		return providerError(op, "heading must be from 1 through 360 degrees")
	}
	if radius != nil && (*radius < minProximityRadius || *radius > maxProximityRadius) {
		return providerError(op, "proximity_alert_radius must be from 1 through 100000 meters")
	}
	return nil
}

// VenueOptions are the options of one sendVenue request.
type VenueOptions struct {
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
	Title     string   `json:"title"`
	Address   string   `json:"address"`
	SendCommon
}

func (o VenueOptions) validate() error {
	const op = "send venue"
	if err := validCoordinates(op, o.Latitude, o.Longitude); err != nil {
		return err
	}
	if !boundedText(o.Title, maxVenueTitleRunes) || !boundedText(o.Address, maxVenueAddressRunes) {
		return providerError(op, "title must be from 1 through 256 and address from 1 through 512 characters")
	}
	return o.SendCommon.validate(op)
}

// ContactOptions are the options of one sendContact request.
type ContactOptions struct {
	PhoneNumber string `json:"phone_number"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	SendCommon
}

func (o ContactOptions) validate() error {
	const op = "send contact"
	if !boundedText(o.PhoneNumber, maxContactPhoneRunes) || !boundedText(o.FirstName, maxContactNameRunes) ||
		(o.LastName != "" && !boundedText(o.LastName, maxContactNameRunes)) {
		return providerError(op, "phone_number and first_name are required, and each value is at most 64 characters")
	}
	return o.SendCommon.validate(op)
}

// DiceOptions are the options of one sendDice request. An empty emoji lets Telegram pick its default.
type DiceOptions struct {
	Emoji string `json:"emoji"`
	SendCommon
}

func (o DiceOptions) validate() error {
	const op = "send dice"
	if o.Emoji != "" {
		known := false
		for _, e := range diceEmoji {
			known = known || e == o.Emoji
		}
		if !known {
			return providerError(op, "the emoji must be one of the dice emoji Telegram lists")
		}
	}
	return o.SendCommon.validate(op)
}

func invokeLocationsSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat string `json:"chat"`
		LocationOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("send location", "the validated arguments could not be read")
	}
	if err := arguments.LocationOptions.validate(); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.SendLocation(ctx, arguments.LocationOptions)
}

func invokeVenuesSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat string `json:"chat"`
		VenueOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("send venue", "the validated arguments could not be read")
	}
	if err := arguments.VenueOptions.validate(); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.SendVenue(ctx, arguments.VenueOptions)
}

func invokeContactsSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat string `json:"chat"`
		ContactOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("send contact", "the validated arguments could not be read")
	}
	if err := arguments.ContactOptions.validate(); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.SendContact(ctx, arguments.ContactOptions)
}

func invokeDiceSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Chat string `json:"chat"`
		DiceOptions
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError("send dice", "the validated arguments could not be read")
	}
	if err := arguments.DiceOptions.validate(); err != nil {
		return nil, err
	}
	client, err := openChat(ctx, resolved, secrets, red, arguments.Chat)
	if err != nil {
		return nil, err
	}
	return client.SendDice(ctx, arguments.DiceOptions)
}

// sendStructured performs exactly one request and reads only message_id and date of the sent message.
func (c *Client) sendStructured(ctx context.Context, op, method string, body any) (map[string]any, error) {
	raw, err := c.call(ctx, spec{op: op, method: method, limit: structuredResponseOver}, body)
	if err != nil {
		return nil, err
	}
	var message struct {
		MessageID int64 `json:"message_id"`
		Date      int64 `json:"date"`
	}
	if json.Unmarshal(raw, &message) != nil || message.MessageID <= 0 || message.Date <= 0 {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	return map[string]any{"message_id": message.MessageID, "date": message.Date}, nil
}

// SendLocation performs exactly one sendLocation request without retrying an ambiguous result.
func (c *Client) SendLocation(ctx context.Context, o LocationOptions) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError("send location", "no chat was selected")
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	return c.sendStructured(ctx, "send location", "sendLocation", struct {
		ChatID               string   `json:"chat_id"`
		Latitude             float64  `json:"latitude"`
		Longitude            float64  `json:"longitude"`
		HorizontalAccuracy   *float64 `json:"horizontal_accuracy,omitempty"`
		LivePeriod           *int64   `json:"live_period,omitempty"`
		Heading              *int64   `json:"heading,omitempty"`
		ProximityAlertRadius *int64   `json:"proximity_alert_radius,omitempty"`
		commonBody
	}{c.target, *o.Latitude, *o.Longitude, o.HorizontalAccuracy, o.LivePeriod, o.Heading, o.ProximityAlertRadius,
		o.SendCommon.body()})
}

// SendVenue performs exactly one sendVenue request without retrying an ambiguous result.
func (c *Client) SendVenue(ctx context.Context, o VenueOptions) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError("send venue", "no chat was selected")
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	return c.sendStructured(ctx, "send venue", "sendVenue", struct {
		ChatID    string  `json:"chat_id"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Title     string  `json:"title"`
		Address   string  `json:"address"`
		commonBody
	}{c.target, *o.Latitude, *o.Longitude, o.Title, o.Address, o.SendCommon.body()})
}

// SendContact performs exactly one sendContact request without retrying an ambiguous result.
func (c *Client) SendContact(ctx context.Context, o ContactOptions) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError("send contact", "no chat was selected")
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	return c.sendStructured(ctx, "send contact", "sendContact", struct {
		ChatID      string `json:"chat_id"`
		PhoneNumber string `json:"phone_number"`
		FirstName   string `json:"first_name"`
		LastName    string `json:"last_name,omitempty"`
		commonBody
	}{c.target, o.PhoneNumber, o.FirstName, o.LastName, o.SendCommon.body()})
}

// SendDice performs exactly one sendDice request without retrying an ambiguous result.
func (c *Client) SendDice(ctx context.Context, o DiceOptions) (map[string]any, error) {
	if c.target == "" {
		return nil, providerError("send dice", "no chat was selected")
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	return c.sendStructured(ctx, "send dice", "sendDice", struct {
		ChatID string `json:"chat_id"`
		Emoji  string `json:"emoji,omitempty"`
		commonBody
	}{c.target, o.Emoji, o.SendCommon.body()})
}
