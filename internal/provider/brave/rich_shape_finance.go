package brave

// Rich vertical shapes of Brave's reference: cryptocurrency, currency, stock.
// any, an object without listed members, and a list of unspecified items are anyJSON.

var (
	richCryptocurrency = object(
		p("intent_type", short),
		p("quote", object(
			p("id", short), p("symbol", short), p("name", title), p("image", addr),
			p("current_price", decimal), p("market_cap", decimal), p("market_cap_rank", integer),
			p("fully_diluted_valuation", decimal), p("total_volume", decimal), p("high_24h", decimal),
			p("low_24h", decimal), p("price_change_24h", decimal), p("price_change_percentage_24h", decimal),
			p("market_cap_change_24h", decimal), p("market_cap_change_percentage_24h", decimal),
			p("circulating_supply", decimal), p("total_supply", decimal), p("max_supply", decimal),
			p("ath", decimal), p("ath_change_percentage", decimal), p("ath_date", short), p("atl", decimal),
			p("atl_change_percentage", decimal), p("atl_date", short), p("last_updated", short),
		)),
		p("timeseries", object(
			p("time_range", short), p("ts_price", series(anyJSON)), p("ts_market_cap", series(anyJSON)),
			p("ts_volume", series(anyJSON)),
		)),
		p("vs_currency", short), p("vs_currencies", richList(object(p("label", title), p("value", short)))),
		p("top100_crypto", richList(object(p("label", title), p("value", short), p("symbol", short)))),
		p("amount", anyJSON),
		p("currency", object(p("full_name", title), p("code", short), p("decimals", integer))),
	)
	richCurrency = object(
		p("conversion", object(
			p("query", object(
				p("from_currency", object(p("full_name", title), p("code", short), p("decimals", integer))),
				p("to_currency", object(p("full_name", title), p("code", short), p("decimals", integer))),
				p("amount", decimal),
			)),
			p("info", object(p("rate", decimal), p("timestamp", integer))), p("date", short),
			p("result", decimal), p("amount", decimal),
		)),
		p("timeseries", object(p("time_range", short), p("ts_exchange_rate", series(anyJSON)))),
		p("supported_currencies", richList(object(p("full_name", title), p("code", short), p("decimals", integer)))),
	)
	richStock = object(
		p("asset_info", object(
			p("symbol", short), p("name", title), p("category", short), p("currency", short),
			p("country", title), p("country_code", short), p("exchange", title), p("type", short),
			p("mic_code", short), p("score", decimal),
		)),
		p("time_range", short),
		p("quote", object(
			p("symbol", short), p("company_name", title), p("primary_exchange", title),
			p("market_cap", integer), p("pe_ratio", decimal), p("week_52_high", decimal),
			p("week_52_low", decimal), p("latest_price", decimal), p("open", decimal), p("close", decimal),
			p("high", decimal), p("low", decimal), p("change", decimal), p("change_percent", decimal),
			p("volume", integer), p("latest_update", integer), p("currency", short),
		)),
		p("timeseries", object(
			p("time_range", short),
			p("timeseries", richList(object(
				p("date", short), p("minute", short), p("label", title), p("timestamp", integer),
				p("high", decimal), p("low", decimal), p("close", decimal), p("previous_close", decimal),
				p("average", decimal), p("volume", decimal),
			))),
		)),
		p("exchange_info", object(p("timezone", short), p("open_time", short), p("close_time", short))),
	)
)
