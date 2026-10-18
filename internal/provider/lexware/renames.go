package lexware

// renamedTools maps former tool IDs to their current IDs.
var renamedTools = map[string]string{
	"lexware.creditnotes.create":        "lexware.credit-notes.create",
	"lexware.creditnotes.get":           "lexware.credit-notes.get",
	"lexware.creditnotes.issue":         "lexware.credit-notes.issue",
	"lexware.deliverynotes.create":      "lexware.delivery-notes.create",
	"lexware.deliverynotes.get":         "lexware.delivery-notes.get",
	"lexware.deliverynotes.issue":       "lexware.delivery-notes.issue",
	"lexware.downpaymentinvoices.get":   "lexware.down-payment-invoices.get",
	"lexware.eventsubscriptions.delete": "lexware.event-subscriptions.delete",
	"lexware.eventsubscriptions.get":    "lexware.event-subscriptions.get",
	"lexware.eventsubscriptions.list":   "lexware.event-subscriptions.list",
	"lexware.orderconfirmations.create": "lexware.order-confirmations.create",
	"lexware.orderconfirmations.get":    "lexware.order-confirmations.get",
	"lexware.orderconfirmations.issue":  "lexware.order-confirmations.issue",
	"lexware.paymentconditions.list":    "lexware.payment-conditions.list",
	"lexware.postingcategories.list":    "lexware.posting-categories.list",
	"lexware.printlayouts.list":         "lexware.print-layouts.list",
	"lexware.recurringtemplates.get":    "lexware.recurring-templates.get",
	"lexware.recurringtemplates.list":   "lexware.recurring-templates.list",
	"lexware.voucherlist.list":          "lexware.voucher-list.list",
}
