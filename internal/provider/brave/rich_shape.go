package brave

// The rich answer in Brave's own member names. The verticals are in the rich_shape_*.go files, one object
// per vertical; a result carries one of them.

// Limits of the lists of a rich answer: schedules, standings, and forecasts are longer than a result list,
// and a time series holds one entry per sample.
const (
	maxRichItems   = 100
	maxSeriesItems = 500
)

func richList(elem *shape) *shape { return listOf(maxRichItems, elem) }

func series(elem *shape) *shape { return listOf(maxSeriesItems, elem) }

var (
	richProviderShape = object(p("type", short), p("name", title), p("url", addr), p("long_name", title),
		p("img", addr))

	richResultShape = object(p("type", short), p("subtype", short), p("provider", richProviderShape),
		p("language", short),
		p("calculator", richCalculator), p("cryptocurrency", richCryptocurrency), p("currency", richCurrency),
		p("definitions", richDefinitions), p("formula1", richFormula1), p("news", richNews),
		p("packagetracker", richPackagetracker), p("american_football", richAmericanFootball),
		p("football", richFootball), p("baseball", richBaseball), p("basketball", richBasketball),
		p("ice_hockey", richIceHockey), p("cricket", richCricket), p("stock", richStock),
		p("unitconversion", richUnitconversion), p("unixtimestamp", richUnixtimestamp),
		p("weather", richWeather))

	richAnswerShape = object(p("type", short), p("results", richList(richResultShape)),
		p("response_callback_info", object(p("vertical", short), p("callback_key", short),
			p("callback_status", short), p("search_lang", short))))
)
