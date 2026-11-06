package googlemaps

// The shape of a Place as Places API (New) documents it, revision 20261105 of its discovery document, with
// every member the documentation does not mark as deprecated. A member of a later revision is ignored until
// it is added here. Photo media is never fetched: a photo is shown by its resource name and its attributions.
var (
	localizedText = object(p("text", text), p("languageCode", short))
	dateShape     = object(p("year", integer), p("month", integer), p("day", integer))
	latLng        = object(p("latitude", decimal), p("longitude", decimal))
	moneyShape    = object(p("currencyCode", short), p("units", short), p("nanos", integer))
	contentBlock  = object(p("content", localizedText), p("referencedPlaces", list(short)))
	authorShape   = object(p("displayName", title), p("uri", addr), p("photoUri", addr))

	hoursPoint = object(p("date", dateShape), p("truncated", boolean), p("day", integer), p("hour", integer),
		p("minute", integer))
	openingHours = object(
		p("periods", list(object(p("open", hoursPoint), p("close", hoursPoint)))),
		p("weekdayDescriptions", list(title)), p("secondaryHoursType", short),
		p("specialDays", list(object(p("date", dateShape)))),
		p("nextOpenTime", short), p("nextCloseTime", short), p("openNow", boolean))

	transitIcon = object(p("url", addr), p("nameIncluded", boolean))
	transitLine = object(p("id", short), p("displayName", localizedText), p("shortDisplayName", localizedText),
		p("backgroundColor", short), p("textColor", short), p("icon", transitIcon), p("vehicleIcon", transitIcon),
		p("url", addr), p("vehicleType", short))
	transitAgency = object(p("displayName", localizedText), p("icon", transitIcon), p("url", addr),
		p("fareUrl", addr), p("lines", list(transitLine)))
	transitStop = object(p("id", short), p("displayName", localizedText), p("platformCode", localizedText),
		p("signageText", localizedText), p("stopCode", localizedText), p("location", latLng),
		p("wheelchairAccessibleEntrance", boolean))

	placeShape = object(
		// IDs Only.
		p("id", short), p("name", short), p("movedPlace", short), p("movedPlaceId", short),
		p("attributions", list(object(p("provider", title), p("providerUri", addr)))),
		p("consumerAlert", object(p("overview", text), p("languageCode", short), p("details", object(
			p("title", title), p("description", text),
			p("aboutLink", object(p("title", title), p("uri", addr))))))),
		p("photos", listOf(10, object(p("name", short), p("widthPx", integer), p("heightPx", integer),
			p("authorAttributions", list(authorShape)), p("flagContentUri", addr), p("googleMapsUri", addr)))),
		// Essentials.
		p("addressComponents", listOf(40, object(p("longText", title), p("shortText", title),
			p("types", list(short)), p("languageCode", short)))),
		p("addressDescriptor", object(
			p("landmarks", list(object(p("name", short), p("placeId", short), p("displayName", localizedText),
				p("types", list(short)), p("spatialRelationship", short), p("straightLineDistanceMeters", decimal),
				p("travelDistanceMeters", decimal)))),
			p("areas", list(object(p("name", short), p("placeId", short), p("displayName", localizedText),
				p("containment", short)))))),
		p("adrFormatAddress", text), p("formattedAddress", title), p("shortFormattedAddress", title),
		p("location", latLng),
		p("plusCode", object(p("globalCode", short), p("compoundCode", title))),
		p("postalAddress", object(p("revision", integer), p("regionCode", short), p("languageCode", short),
			p("postalCode", short), p("sortingCode", short), p("administrativeArea", title),
			p("locality", title), p("sublocality", title), p("addressLines", list(title)),
			p("recipients", list(title)), p("organization", title))),
		p("types", listOf(40, short)),
		p("viewport", object(p("low", latLng), p("high", latLng))),
		// Pro.
		p("accessibilityOptions", object(p("wheelchairAccessibleParking", boolean),
			p("wheelchairAccessibleEntrance", boolean), p("wheelchairAccessibleRestroom", boolean),
			p("wheelchairAccessibleSeating", boolean))),
		p("businessStatus", short),
		p("containingPlaces", list(object(p("name", short), p("id", short)))),
		p("displayName", localizedText),
		p("entrances", list(object(p("location", latLng), p("tags", list(short))))),
		p("googleMapsLinks", object(p("directionsUri", addr), p("placeUri", addr), p("writeAReviewUri", addr),
			p("reviewsUri", addr), p("photosUri", addr))),
		p("googleMapsTypeLabel", localizedText), p("googleMapsUri", addr),
		p("iconBackgroundColor", short), p("iconMaskBaseUri", addr),
		p("navigationPoints", list(object(p("displayName", localizedText), p("location", latLng),
			p("navigationPointToken", short), p("usages", list(short)), p("travelModes", list(short))))),
		p("openingDate", dateShape), p("primaryType", short), p("primaryTypeDisplayName", localizedText),
		p("pureServiceAreaBusiness", boolean),
		p("subDestinations", list(object(p("name", short), p("id", short)))),
		p("timeZone", object(p("id", short), p("version", short))), p("utcOffsetMinutes", integer),
		// Enterprise.
		p("currentOpeningHours", openingHours), p("currentSecondaryOpeningHours", list(openingHours)),
		p("regularOpeningHours", openingHours), p("regularSecondaryOpeningHours", list(openingHours)),
		p("internationalPhoneNumber", short), p("nationalPhoneNumber", short), p("priceLevel", short),
		p("priceRange", object(p("startPrice", moneyShape), p("endPrice", moneyShape))),
		p("rating", decimal), p("userRatingCount", integer), p("websiteUri", addr),
		p("transitStation", object(p("displayName", localizedText), p("agencies", list(transitAgency)),
			p("stops", list(transitStop)))),
		// Enterprise + Atmosphere.
		p("allowsDogs", boolean), p("curbsidePickup", boolean), p("delivery", boolean), p("dineIn", boolean),
		p("goodForChildren", boolean), p("goodForGroups", boolean), p("goodForWatchingSports", boolean),
		p("liveMusic", boolean), p("menuForChildren", boolean), p("outdoorSeating", boolean),
		p("reservable", boolean), p("restroom", boolean), p("servesBeer", boolean),
		p("servesBreakfast", boolean), p("servesBrunch", boolean), p("servesCocktails", boolean),
		p("servesCoffee", boolean), p("servesDessert", boolean), p("servesDinner", boolean),
		p("servesLunch", boolean), p("servesVegetarianFood", boolean), p("servesWine", boolean),
		p("takeout", boolean),
		p("editorialSummary", localizedText),
		p("evChargeAmenitySummary", object(p("overview", contentBlock), p("coffee", contentBlock),
			p("restaurant", contentBlock), p("store", contentBlock), p("flagContentUri", addr),
			p("disclosureText", localizedText))),
		p("evChargeOptions", object(p("connectorCount", integer), p("connectorAggregation", list(object(
			p("type", short), p("maxChargeRateKw", decimal), p("count", integer), p("availableCount", integer),
			p("outOfServiceCount", integer), p("availabilityLastUpdateTime", short)))))),
		p("fuelOptions", object(p("fuelPrices", list(object(p("type", short), p("price", moneyShape),
			p("updateTime", short)))))),
		p("generativeSummary", object(p("overview", localizedText), p("disclosureText", localizedText),
			p("overviewFlagContentUri", addr))),
		p("neighborhoodSummary", object(p("overview", contentBlock), p("description", contentBlock),
			p("flagContentUri", addr), p("disclosureText", localizedText))),
		p("parkingOptions", object(p("freeParkingLot", boolean), p("paidParkingLot", boolean),
			p("freeStreetParking", boolean), p("paidStreetParking", boolean), p("valetParking", boolean),
			p("freeGarageParking", boolean), p("paidGarageParking", boolean))),
		p("paymentOptions", object(p("acceptsCreditCards", boolean), p("acceptsDebitCards", boolean),
			p("acceptsCashOnly", boolean), p("acceptsNfc", boolean))),
		p("reviewSummary", object(p("text", localizedText), p("flagContentUri", addr),
			p("disclosureText", localizedText), p("reviewsUri", addr))),
		p("reviews", listOf(5, object(p("name", short), p("relativePublishTimeDescription", short),
			p("rating", decimal), p("text", localizedText), p("originalText", localizedText),
			p("authorAttribution", authorShape), p("publishTime", short), p("flagContentUri", addr),
			p("googleMapsUri", addr), p("visitDate", dateShape)))),
	)

	placeOutputSchema = placeShape.schema()
)
