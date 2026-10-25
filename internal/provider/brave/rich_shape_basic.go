package brave

// Rich vertical shapes of Brave's reference: calculator, definitions, package tracking, unit conversion, Unix time, news.
// any, an object without listed members, and a list of unspecified items are anyJSON.

var (
	richCalculator  = object(p("expression", body), p("answer", anyJSON))
	richDefinitions = object(
		p("word", title), p("language", short), p("pronounciation", short),
		p("definitions", richList(object(
			p("part_of_speech", short),
			p("definitions", richList(object(
				p("text", body), p("example_uses", richList(short)), p("related_words", richList(short)),
				p("labels", richList(object(p("text", body), p("type", short)))),
			))),
		))),
		p("source_dict", short), p("attribution_text", body), p("attribution_url", addr),
		p("audio_available", boolean),
	)
	richPackagetracker = object(
		p("matched", richList(object(
			p("tracking_number", short), p("tracking_url", addr), p("description", body),
			p("courier", object(p("name", title), p("service", title), p("code", short), p("domain", title))),
		))),
	)
	richUnitconversion = object(
		p("amount", decimal), p("from_unit", short), p("to_unit", short), p("dimensionality", short),
	)
	richUnixtimestamp = object(p("conversion", object(p("intent_type", short), p("input_ts", integer))))
	richNews          = object(
		p("is_topical", boolean), p("topic_id", integer),
		p("topics", richList(object(p("index", integer), p("title", title), p("score", decimal), p("query", title)))),
		p("articles", richList(object(
			p("title", title), p("img", addr), p("img_small", addr), p("url", addr),
			p("meta_url", object(
				p("scheme", short), p("netloc", title), p("hostname", title), p("favicon", addr),
				p("path", title),
			)),
			p("publisher_name", title), p("publish_time", integer), p("page_age", short),
			p("is_live", boolean), p("description", body), p("extra_snippets", richList(short)),
			p("bo_debug", anyJSON),
		))),
	)
)
