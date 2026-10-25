package brave

// The shapes of the web search answer, one per object of Brave's reference. A member that Brave marks as
// deprecated (the restaurant of a result, replaced by location) is not listed, and a member whose type the
// reference leaves open is anyJSON.

var (
	thumbnailShape = object(p("src", addr), p("alt", title), p("height", integer), p("width", integer),
		p("bg_color", short), p("original", addr), p("logo", boolean), p("duplicated", boolean), p("theme", short))
	profileShape = object(p("name", title), p("url", addr), p("long_name", title), p("img", addr))
	// metaURLShape is the address breakdown where Brave documents its members.
	metaURLShape = object(p("scheme", short), p("netloc", title), p("hostname", title), p("favicon", addr),
		p("path", title))
	ratingShape = object(p("ratingValue", decimal), p("bestRating", decimal), p("reviewCount", integer),
		p("profile", profileShape), p("is_tripadvisor", boolean))
	iconShape = object(p("href", addr), p("sizes", short), p("rel", short), p("type", short), p("ext", short))

	// pageProps are the members every kind of page result has.
	pageProps = []prop{p("title", title), p("url", addr), p("is_source_local", boolean),
		p("is_source_both", boolean), p("description", body), p("page_age", short), p("page_fetched", short),
		p("fetched_content_timestamp", integer), p("profile", profileShape), p("language", short),
		p("family_friendly", boolean)}

	// thingShape is a person or organization named in structured data.
	thingProps = []prop{p("type", short), p("name", title), p("url", addr), p("thumbnail", thumbnailShape),
		p("email", short)}
	thingShape        = object(thingProps...)
	contactPointShape = object(p("type", short), p("name", title), p("url", addr), p("thumbnail", thumbnailShape),
		p("telephone", short), p("email", short))
	organizationProps = []prop{p("type", short), p("name", title), p("url", addr), p("thumbnail", thumbnailShape),
		p("contact_points", list(contactPointShape))}
)

// videoDataShape is the video member of a result; views is a text in a web result and a number in a video
// result, which also has no thumbnail inside the video.
func videoDataShape(views *shape, thumbnail bool) *shape {
	props := []prop{p("duration", short), p("views", views), p("creator", title), p("publisher", title)}
	if thumbnail {
		props = append(props, p("thumbnail", thumbnailShape))
	}
	return object(append(props, p("tags", list(short)), p("author", profileShape),
		p("requires_subscription", boolean))...)
}

var (
	deepNewsShape = object(join(pageProps, []prop{p("meta_url", anyJSON), p("source", title), p("breaking", boolean),
		p("is_live", boolean), p("thumbnail", thumbnailShape), p("age", short),
		p("extra_snippets", listOf(maxExtraSnips, snippet)), p("icons", listOf(maxIcons, iconShape))})...)
	deepVideoShape = object(join(pageProps, []prop{p("type", short), p("video", videoDataShape(short, true)),
		p("meta_url", metaURLShape), p("thumbnail", thumbnailShape), p("age", short), p("publisher", title)})...)
	deepImageShape = object(p("thumbnail", thumbnailShape), p("url", addr), p("properties", object(
		p("url", addr), p("resized", addr), p("placeholder", addr), p("height", integer), p("width", integer),
		p("format", short), p("content_size", short))))
	deepResultsShape = object(p("news", list(deepNewsShape)),
		p("buttons", list(object(p("type", short), p("title", title), p("url", addr)))),
		p("videos", list(deepVideoShape)), p("images", list(deepImageShape)))

	pictureShape = thumbnailShape
	reviewShape  = object(p("title", title), p("description", body), p("date", short), p("rating", ratingShape),
		p("author", thingShape), p("review_url", addr), p("language", short))

	// placeProps are the members of a place, a location in a result and an entry of locations alike.
	placeProps = join(pageProps, []prop{p("type", short), p("provider_url", addr), p("coordinates", anyJSON),
		p("zoom_level", integer), p("thumbnail", thumbnailShape),
		p("postal_address", object(p("type", short), p("country", short), p("postalCode", short),
			p("streetAddress", title), p("addressRegion", title), p("addressLocality", title),
			p("displayAddress", title))),
		p("opening_hours", object(
			p("current_day", list(object(p("abbr_name", short), p("full_name", short), p("opens", short),
				p("closes", short)))), p("days", anyJSON))),
		p("contact", object(p("email", short), p("telephone", short))), p("price_range", short),
		p("rating", ratingShape), p("distance", object(p("value", decimal), p("units", short))),
		p("profiles", list(object(p("type", short), p("name", title), p("url", addr), p("long_name", title),
			p("img", addr)))),
		p("reviews", object(p("results", list(reviewShape)), p("viewMoreUrl", addr),
			p("reviews_in_foreign_language", boolean))),
		p("pictures", object(p("viewMoreUrl", addr), p("results", list(pictureShape)))),
		p("action", object(p("type", short), p("url", addr))), p("serves_cuisine", list(short)),
		p("categories", list(short)), p("icon_category", short), p("timezone", short),
		p("timezone_offset", integer)})
	locationShape = object(join(placeProps, []prop{p("id", short), p("results", list(object(
		join(pageProps, []prop{p("meta_url", metaURLShape)})...)))})...)

	movieShape = object(p("name", title), p("description", body), p("url", addr), p("thumbnail", thumbnailShape),
		p("release", short), p("directors", list(thingShape)), p("actors", list(thingShape)),
		p("rating", ratingShape), p("duration", short), p("genre", list(short)), p("query", short))
	faqItemShape = object(p("question", body), p("answer", body), p("title", title), p("url", addr),
		p("meta_url", metaURLShape))
	productProps = []prop{p("type", short), p("name", title), p("url", addr), p("category", short),
		p("price", short), p("thumbnail", thumbnailShape), p("description", body),
		p("offers", list(object(p("url", addr), p("priceCurrency", short), p("price", short)))),
		p("rating", ratingShape), p("gtin", short), p("gtin8", short), p("gtin12", short), p("gtin13", short),
		p("gtin14", short)}

	// resultProps are the members of a web result and of a discussion result alike.
	resultProps = join(pageProps, []prop{p("type", short), p("subtype", short), p("is_live", boolean),
		p("deep_results", deepResultsShape), p("schemas", anyJSON), p("meta_url", anyJSON),
		p("thumbnail", thumbnailShape), p("age", short), p("location", locationShape),
		p("video", videoDataShape(short, true)), p("movie", movieShape), p("faq", object(p("items", list(faqItemShape)))),
		p("qa", object(p("question", body), p("answer", object(p("text", body), p("author", title),
			p("upvoteCount", integer), p("downvoteCount", integer))))),
		p("book", object(p("title", title), p("author", list(thingShape)), p("date", short),
			p("price", object(p("price", short), p("priceCurrency", short))), p("pages", integer),
			p("publisher", thingShape), p("rating", ratingShape))),
		p("rating", ratingShape),
		p("article", object(p("author", list(thingShape)), p("date", short),
			p("publisher", object(organizationProps...)), p("thumbnail", thumbnailShape),
			p("isAccessibleForFree", boolean))),
		p("product", anyJSON), p("product_cluster", list(object(productProps...))), p("cluster_type", short),
		p("cluster", list(object(pageProps...))),
		p("creative_work", object(p("name", title), p("rating", ratingShape), p("thumbnail", thumbnailShape))),
		p("music_recording", object(p("name", title), p("rating", ratingShape), p("thumbnail", thumbnailShape))),
		p("review", object(p("type", short), p("name", title), p("thumbnail", thumbnailShape),
			p("description", body), p("rating", ratingShape))),
		p("recipe", object(p("title", title), p("description", body), p("thumbnail", thumbnailShape),
			p("url", addr), p("domain", title), p("favicon", addr), p("time", short), p("prep_time", short),
			p("cook_time", short), p("ingredients", body), p("instructions", list(object(p("text", body),
				p("name", title), p("url", addr), p("image", list(addr))))), p("servings", integer),
			p("calories", integer), p("publisher", title), p("rating", ratingShape), p("recipeCategory", short),
			p("recipeCuisine", short), p("video", videoDataShape(short, true)))),
		p("software", object(p("name", title), p("author", title), p("version", short),
			p("codeRepository", addr), p("homepage", addr), p("datePublished", short), p("is_npm", boolean),
			p("is_pypi", boolean), p("stars", integer), p("forks", integer), p("programmingLanguage", short))),
		p("organization", object(organizationProps...)), p("content_type", short),
		p("extra_snippets", listOf(maxExtraSnips, snippet)), p("icons", listOf(maxIcons, iconShape))})

	webResultShape        = object(resultProps...)
	discussionResultShape = object(join(resultProps, []prop{p("data", object(p("forum_name", title),
		p("num_answers", integer), p("score", short), p("title", title), p("question", body),
		p("top_comment", body)))})...)

	infoboxResultShape = object(join(pageProps, []prop{p("type", short), p("position", integer),
		p("label", title), p("category", short), p("long_desc", body), p("thumbnail", thumbnailShape),
		p("attributes", list(listOf(maxListItems, body))), p("profiles", anyJSON), p("website_url", addr),
		p("ratings", list(ratingShape)),
		p("providers", list(object(p("type", short), p("name", title), p("url", addr), p("long_name", title),
			p("img", addr)))),
		p("distance", object(p("value", decimal), p("units", short))), p("images", list(thumbnailShape)),
		p("movie", movieShape), p("subtype", short), p("found_in_urls", list(addr))})...)

	newsResultShape = object(join(pageProps, []prop{p("meta_url", anyJSON), p("source", title),
		p("breaking", boolean), p("is_live", boolean), p("thumbnail", thumbnailShape), p("age", short),
		p("extra_snippets", listOf(maxExtraSnips, snippet)), p("icons", listOf(maxIcons, iconShape))})...)
	videoResultShape = object(p("type", short), p("url", addr), p("title", title), p("description", body),
		p("age", short), p("page_age", short), p("page_fetched", short), p("fetched_content_timestamp", integer),
		p("video", videoDataShape(integer, false)), p("meta_url", metaURLShape), p("thumbnail", object(
			p("src", addr), p("original", addr))))
	faqResultShape = object(p("question", body), p("answer", body), p("title", title), p("url", addr),
		p("meta_url", metaURLShape))
	mixedEntryShape = object(p("type", short), p("index", integer), p("all", boolean))

	// webAnswerShape is the answer of the endpoint as the tool shows it, in Brave's own member names.
	webAnswerShape = object(
		p("type", short),
		p("query", object(p("original", body), p("show_strict_warning", boolean), p("altered", body),
			p("cleaned", body), p("safesearch", boolean), p("is_navigational", boolean), p("is_geolocal", boolean),
			p("local_decision", short), p("local_locations_idx", integer), p("is_trending", boolean),
			p("is_news_breaking", boolean), p("ask_for_location", boolean),
			p("language", object(p("main", short))), p("spellcheck_off", boolean), p("country", short),
			p("bad_results", boolean), p("should_fallback", boolean), p("lat", short), p("long", short),
			p("postal_code", short), p("city", title), p("header_country", short),
			p("more_results_available", boolean), p("state", title), p("custom_location_label", title),
			p("reddit_cluster", short), p("summary_key", short),
			p("search_operators", object(p("applied", boolean), p("cleaned_query", body),
				p("sites", listOf(maxSites, title)))),
			p("related_queries", list(body)))),
		p("discussions", object(p("type", short), p("results", list(discussionResultShape)),
			p("mutated_by_goggles", boolean))),
		p("faq", object(p("type", short), p("results", list(faqResultShape)))),
		p("infobox", object(p("type", short), p("results", list(infoboxResultShape)))),
		p("locations", object(p("type", short), p("results", list(locationShape)), p("provider", anyJSON))),
		p("mixed", object(p("type", short), p("main", listOf(maxMixedItems, mixedEntryShape)),
			p("top", listOf(maxMixedItems, mixedEntryShape)), p("side", listOf(maxMixedItems, mixedEntryShape)))),
		p("news", object(p("type", short), p("results", list(newsResultShape)), p("mutated_by_goggles", boolean))),
		p("videos", object(p("type", short), p("results", list(videoResultShape)),
			p("mutated_by_goggles", boolean))),
		p("web", object(p("type", short), p("results", list(webResultShape)), p("family_friendly", boolean))),
		p("summarizer", object(p("type", short), p("key", short))),
		p("rich", object(p("type", short), p("hint", object(p("vertical", short), p("callback_key", short))))),
	)
)
