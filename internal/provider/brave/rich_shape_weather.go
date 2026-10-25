package brave

// Rich vertical shapes of Brave's reference: weather.
// any, an object without listed members, and a list of unspecified items are anyJSON.

var (
	richWeather = object(
		p("location", object(
			p("id", integer), p("name", title), p("country", title), p("state", title),
			p("coords", object(p("lat", decimal), p("lon", decimal))), p("population", integer),
			p("sunrise", integer), p("sunset", integer), p("tzoffset", integer),
			p("implicit_location", boolean),
		)),
		p("current_time_iso", short),
		p("current_weather", object(
			p("ts", integer), p("sunrise", integer), p("sunset", integer), p("temp", decimal),
			p("feels_like", decimal), p("pressure", integer), p("humidity", integer), p("dew_point", decimal),
			p("uvi", decimal), p("clouds", integer), p("visibility", integer),
			p("wind", object(p("speed", decimal), p("deg", integer), p("gust", decimal))),
			p("weather", object(p("id", integer), p("main", short), p("description", body), p("icon", short))),
			p("rain", integer), p("snow", integer),
		)),
		p("daily", richList(object(
			p("ts", integer), p("date_i18n", short), p("sunrise", integer), p("sunset", integer),
			p("moonrise", integer), p("moonset", integer),
			p("temperature", object(
				p("day", decimal), p("min", decimal), p("max", decimal), p("night", decimal),
				p("evening", decimal), p("morning", decimal),
			)),
			p("feels_like", object(
				p("day", decimal), p("night", decimal), p("evening", decimal), p("morning", decimal),
			)),
			p("pressure", integer), p("humidity", integer), p("dew_point", decimal),
			p("wind", object(p("speed", decimal), p("deg", integer), p("gust", decimal))),
			p("clouds", integer), p("pop", decimal), p("rain", decimal), p("uvi", decimal),
			p("weather", object(p("id", integer), p("main", short), p("description", body), p("icon", short))),
		))),
		p("hours3", richList(object(
			p("ts", integer),
			p("temperature", object(
				p("temp", decimal), p("feels_like", decimal), p("min", decimal), p("max", decimal),
			)),
			p("pressure", integer),
			p("weather", object(p("id", integer), p("main", short), p("description", body), p("icon", short))),
			p("wind", object(p("speed", decimal), p("deg", integer), p("gust", decimal))),
			p("humidity", integer), p("clouds", integer), p("pop", decimal), p("visibility", integer),
			p("rain", decimal), p("snow", decimal),
		))),
		p("alerts", richList(object(
			p("sender", title), p("event", title), p("ts_start", integer), p("ts_end", integer),
			p("start_relative_i18n", short), p("description", body), p("tags", richList(short)),
		))),
	)
)
