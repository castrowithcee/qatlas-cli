package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxCommerceItems = 100
	maxItemCategory  = 5
	// Matomo treats an amount above this as no amount at all, so a larger one is refused here.
	maxCommerceAmount = 1e12
	maxItemQuantity   = math.MaxInt32
)

const commerceItemsSchema = `"items":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"object","properties":{` +
	`"sku":{"type":"string","minLength":1,"maxLength":255},"name":{"type":"string","minLength":1,"maxLength":255},` +
	`"categories":{"type":"array","minItems":1,"maxItems":5,"items":{"type":"string","minLength":1,"maxLength":255}},` +
	`"price":{"type":"number","minimum":0,"maximum":1000000000000},` +
	`"quantity":{"type":"integer","minimum":1,"maximum":2147483647}},` +
	`"required":["sku"],"additionalProperties":false}}`

const commerceAmountSchema = `{"type":"number","minimum":0,"maximum":1000000000000}`

const commerceFraudNote = "; revenue data can be forged: a fabricated order or revenue distorts the e-commerce " +
	"and goal reports and cannot be undone through the API"

func commerceItemsArgument(required bool, purpose string) capability.Argument {
	return capability.Argument{Name: "items", Required: required, Description: purpose + " (ec_items), 1 to 100 " +
		"entries; each has sku (required), name, categories (at most 5), price (0 or more), and quantity (1 or more); " +
		"send the full list every time, because Matomo marks items missing from a later update as removed"}
}

func commerceAmountArgument(name, key, description string, required bool) capability.Argument {
	return capability.Argument{Name: name, Required: required, Description: description + " (" + key +
		"), a number from 0 to 1000000000000"}
}

var trackingRecordGoal = capability.Descriptor{
	ID: Provider + ".tracking.record-goal", Version: 1, Title: "Record a goal conversion in Matomo",
	Description: "Record one conversion of a goal (idgoal) of a website, optionally with revenue. Matomo looks the " +
		"goal up only within the named website and refuses an unknown goal ID as invalid. " + trackingNote +
		commerceFraudNote + trackingSentOnce,
	Tags: []string{"matomo", "tracking", "goal", "create"}, Risk: trackingRisk(), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + trackingContextSchema + `,` +
		`"goalId":{"type":"integer","minimum":1,"maximum":2147483647},"revenue":` + commerceAmountSchema + `},` +
		`"required":["url","goalId"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(trackingOutputSchema),
	Arguments: append(trackingContextArguments(),
		capability.Argument{Name: "goalId", Required: true, Description: "ID of the goal (idgoal) from " +
			"matomo.goals.list, 1 or more"},
		commerceAmountArgument("revenue", "revenue", "Revenue the conversion generated", false)),
	Fields: trackingResultFields,
	Examples: []capability.Example{{Description: "Record a conversion of goal 3 on website 1",
		Arguments: json.RawMessage(`{"idSite":1,"url":"https://example.org/thanks","goalId":3,"revenue":19.9}`)}},
}

var trackingRecordOrder = capability.Descriptor{
	ID: Provider + ".tracking.record-order", Version: 1, Title: "Record an e-commerce order in Matomo",
	Description: "Record one e-commerce order (idgoal=0 with the order ID ec_id, revenue, and optional items and " +
		"amounts) of a website. " + trackingNote + commerceFraudNote + "; Matomo normally refuses an order ID it " +
		"already holds for the website as invalid (HTTP 400) instead of counting it twice, but a queue confirms " +
		"before it checks" + trackingSentOnce,
	Tags: []string{"matomo", "tracking", "ecommerce", "order", "create"}, Risk: trackingRisk(), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + trackingContextSchema + `,` +
		`"orderId":{"type":"string","minLength":1,"maxLength":255},"revenue":` + commerceAmountSchema + `,` +
		`"subtotal":` + commerceAmountSchema + `,"tax":` + commerceAmountSchema + `,"shipping":` + commerceAmountSchema + `,` +
		`"discount":` + commerceAmountSchema + `,` + commerceItemsSchema + `},` +
		`"required":["url","orderId","revenue"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(trackingOutputSchema),
	Arguments: append(trackingContextArguments(),
		capability.Argument{Name: "orderId", Required: true, Description: "Unique ID of the order (ec_id), up to 255 characters"},
		commerceAmountArgument("revenue", "revenue", "Grand total of the order", true),
		commerceAmountArgument("subtotal", "ec_st", "Subtotal of the order without shipping", false),
		commerceAmountArgument("tax", "ec_tx", "Tax amount of the order", false),
		commerceAmountArgument("shipping", "ec_sh", "Shipping cost of the order", false),
		commerceAmountArgument("discount", "ec_dt", "Discount offered", false),
		commerceItemsArgument(false, "Items of the order")),
	Fields: trackingResultFields,
	Examples: []capability.Example{{Description: "Record an order of two items on website 1",
		Arguments: json.RawMessage(`{"idSite":1,"url":"https://example.org/checkout/done","orderId":"order-1001","revenue":59.8,` +
			`"subtotal":49.8,"tax":8,"shipping":10,"items":[{"sku":"SKU-1","name":"Mug","categories":["Kitchen"],"price":24.9,"quantity":2}]}`)}},
}

var trackingRecordCartUpdate = capability.Descriptor{
	ID: Provider + ".tracking.record-cart-update", Version: 1, Title: "Record a cart update in Matomo",
	Description: "Record the current content of a shopping cart (idgoal=0 without an order ID, items, and the cart " +
		"total as revenue) of a website. " + trackingNote + commerceFraudNote + trackingSentOnce,
	Tags: []string{"matomo", "tracking", "ecommerce", "cart", "create"}, Risk: trackingRisk(), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + trackingContextSchema + `,` +
		`"revenue":` + commerceAmountSchema + `,` + commerceItemsSchema + `},` +
		`"required":["url","revenue","items"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(trackingOutputSchema),
	Arguments: append(trackingContextArguments(),
		commerceAmountArgument("revenue", "revenue", "Total of the cart", true),
		commerceItemsArgument(true, "Items now in the cart")),
	Fields: trackingResultFields,
	Examples: []capability.Example{{Description: "Record a cart with one item on website 1",
		Arguments: json.RawMessage(`{"idSite":1,"url":"https://example.org/cart","revenue":24.9,` +
			`"items":[{"sku":"SKU-1","name":"Mug","price":24.9,"quantity":1}]}`)}},
}

type commerceItem struct {
	SKU        *string  `json:"sku"`
	Name       *string  `json:"name"`
	Categories []string `json:"categories"`
	Price      *float64 `json:"price"`
	Quantity   *int64   `json:"quantity"`
}

// commerceAmount validates a money value and stores it.
func commerceAmount(form url.Values, key, argument string, given *float64, required bool) error {
	if given == nil {
		if required {
			return invalidArgument(argument + " is required")
		}
		return nil
	}
	if math.IsNaN(*given) || math.IsInf(*given, 0) || *given < 0 || *given > maxCommerceAmount {
		return invalidArgument(argument + " must be a finite number from 0 to 1000000000000")
	}
	form.Set(key, strconv.FormatFloat(*given, 'f', -1, 64))
	return nil
}

func commerceItemText(given string, argument string) error {
	if !formText(given) || strings.TrimSpace(given) == "" || utf8.RuneCountInString(given) > maxTrackingShort {
		return invalidArgument(argument + " must be a text of 1 to 255 characters")
	}
	return nil
}

// commerceItems stores the items as the JSON array of arrays Matomo reads: sku, name, category, price, quantity.
func commerceItems(form url.Values, items []commerceItem, required bool) error {
	if items == nil {
		if required {
			return invalidArgument("items is required")
		}
		return nil
	}
	if len(items) == 0 || len(items) > maxCommerceItems {
		return invalidArgument("items holds 1 to 100 entries")
	}
	rows := make([][]any, 0, len(items))
	for _, item := range items {
		if item.SKU == nil {
			return invalidArgument("every item needs a sku")
		}
		if err := commerceItemText(*item.SKU, "sku"); err != nil {
			return err
		}
		name := ""
		if item.Name != nil {
			if err := commerceItemText(*item.Name, "name"); err != nil {
				return err
			}
			name = *item.Name
		}
		if len(item.Categories) > maxItemCategory {
			return invalidArgument("an item has at most 5 categories")
		}
		for _, category := range item.Categories {
			if err := commerceItemText(category, "a category"); err != nil {
				return err
			}
		}
		// Matomo reads a single category as text and several as a list.
		var category any = ""
		switch len(item.Categories) {
		case 0:
		case 1:
			category = item.Categories[0]
		default:
			category = item.Categories
		}
		price := 0.0
		if item.Price != nil {
			if math.IsNaN(*item.Price) || math.IsInf(*item.Price, 0) || *item.Price < 0 || *item.Price > maxCommerceAmount {
				return invalidArgument("price must be a finite number from 0 to 1000000000000")
			}
			price = *item.Price
		}
		quantity := int64(1)
		if item.Quantity != nil {
			if *item.Quantity < 1 || *item.Quantity > maxItemQuantity {
				return invalidArgument("quantity must be a whole number of 1 or more")
			}
			quantity = *item.Quantity
		}
		rows = append(rows, []any{*item.SKU, name, category, price, quantity})
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(rows); err != nil {
		return invalidArgument("items could not be encoded")
	}
	form.Set("ec_items", strings.TrimSpace(out.String()))
	return nil
}

func invokeTrackingRecordGoal(ctx context.Context, resolved *config.Resolved, _ *secret.Resolver,
	_ *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		trackingContext
		GoalID  *int64   `json:"goalId"`
		Revenue *float64 `json:"revenue"`
	}
	return record(ctx, resolved, "record goal", raw, &args, func() *trackingContext { return &args.trackingContext },
		func(form url.Values) error {
			if args.GoalID == nil || *args.GoalID < 1 || *args.GoalID > math.MaxInt32 {
				return invalidArgument("goalId must be a whole number of 1 or more")
			}
			form.Set("idgoal", strconv.FormatInt(*args.GoalID, 10))
			return commerceAmount(form, "revenue", "revenue", args.Revenue, false)
		})
}

func invokeTrackingRecordOrder(ctx context.Context, resolved *config.Resolved, _ *secret.Resolver,
	_ *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		trackingContext
		OrderID  *string        `json:"orderId"`
		Revenue  *float64       `json:"revenue"`
		Subtotal *float64       `json:"subtotal"`
		Tax      *float64       `json:"tax"`
		Shipping *float64       `json:"shipping"`
		Discount *float64       `json:"discount"`
		Items    []commerceItem `json:"items"`
	}
	return record(ctx, resolved, "record order", raw, &args, func() *trackingContext { return &args.trackingContext },
		func(form url.Values) error {
			form.Set("idgoal", "0")
			if err := trackingRequired(form, "ec_id", "orderId", args.OrderID, maxTrackingShort); err != nil {
				return err
			}
			for _, step := range []error{
				commerceAmount(form, "revenue", "revenue", args.Revenue, true),
				commerceAmount(form, "ec_st", "subtotal", args.Subtotal, false),
				commerceAmount(form, "ec_tx", "tax", args.Tax, false),
				commerceAmount(form, "ec_sh", "shipping", args.Shipping, false),
				commerceAmount(form, "ec_dt", "discount", args.Discount, false),
				commerceItems(form, args.Items, false),
			} {
				if step != nil {
					return step
				}
			}
			return nil
		})
}

func invokeTrackingRecordCartUpdate(ctx context.Context, resolved *config.Resolved, _ *secret.Resolver,
	_ *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		trackingContext
		Revenue *float64       `json:"revenue"`
		Items   []commerceItem `json:"items"`
	}
	return record(ctx, resolved, "record cart update", raw, &args, func() *trackingContext { return &args.trackingContext },
		func(form url.Values) error {
			// Without an order ID Matomo reads the request as a cart update.
			form.Set("idgoal", "0")
			if err := commerceAmount(form, "revenue", "revenue", args.Revenue, true); err != nil {
				return err
			}
			return commerceItems(form, args.Items, true)
		})
}

func trackingCommerceOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: trackingRecordGoal, Handler: capability.Handler(invokeTrackingRecordGoal)},
		{Descriptor: trackingRecordOrder, Handler: capability.Handler(invokeTrackingRecordOrder)},
		{Descriptor: trackingRecordCartUpdate, Handler: capability.Handler(invokeTrackingRecordCartUpdate)},
	}
}
