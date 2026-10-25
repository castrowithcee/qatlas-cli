package brave

// Rich vertical shapes of Brave's reference: Formula 1.
// any, an object without listed members, and a list of unspecified items are anyJSON.

var (
	richFormula1 = object(
		p("default_view", short),
		p("calendar", object(
			p("races", richList(object(p("id", integer), p("name", title), p("date", short)))),
			p("next_race", object(
				p("id", integer),
				p("competition", object(
					p("id", integer), p("name", title),
					p("location", object(p("country", title), p("city", title))),
				)),
				p("circuit", object(p("id", integer), p("name", title), p("image", addr))),
				p("season", integer), p("type", short),
				p("laps", object(p("current", integer), p("total", integer))),
				p("fastest_lap", object(p("driver", object(p("id", integer))), p("time", short))),
				p("distance", anyJSON), p("date", short), p("status", short),
			)),
			p("previous_race", object(
				p("id", integer),
				p("competition", object(
					p("id", integer), p("name", title),
					p("location", object(p("country", title), p("city", title))),
				)),
				p("circuit", object(p("id", integer), p("name", title), p("image", addr))),
				p("season", integer), p("type", short),
				p("laps", object(p("current", integer), p("total", integer))),
				p("fastest_lap", object(p("driver", object(p("id", integer))), p("time", short))),
				p("distance", anyJSON), p("date", short), p("status", short),
			)),
		)),
		p("next_race", object(
			p("event", object(
				p("id", integer),
				p("competition", object(
					p("id", integer), p("name", title),
					p("location", object(p("country", title), p("city", title))),
				)),
				p("circuit", object(p("id", integer), p("name", title), p("image", addr))),
				p("season", integer), p("type", short),
				p("laps", object(p("current", integer), p("total", integer))),
				p("fastest_lap", object(p("driver", object(p("id", integer))), p("time", short))),
				p("distance", anyJSON), p("date", short), p("status", short),
			)),
			p("standings", richList(object(
				p("position", integer),
				p("driver", object(
					p("id", integer), p("name", title), p("abbr", short), p("number", integer),
					p("image", addr),
				)),
				p("team", object(p("id", integer), p("name", title), p("logo", addr))), p("points", decimal),
				p("wins", integer), p("behind", integer), p("season", integer), p("time", short),
				p("laps", integer), p("grid", short), p("gap", short),
			))),
			p("qualifying", richList(object(
				p("position", integer),
				p("driver", object(
					p("id", integer), p("name", title), p("abbr", short), p("number", integer),
					p("image", addr),
				)),
				p("team", object(p("id", integer), p("name", title), p("logo", addr))), p("points", decimal),
				p("wins", integer), p("behind", integer), p("season", integer), p("time", short),
				p("laps", integer), p("grid", short), p("gap", short),
			))),
			p("sprint_shootout", richList(object(
				p("position", integer),
				p("driver", object(
					p("id", integer), p("name", title), p("abbr", short), p("number", integer),
					p("image", addr),
				)),
				p("team", object(p("id", integer), p("name", title), p("logo", addr))), p("points", decimal),
				p("wins", integer), p("behind", integer), p("season", integer), p("time", short),
				p("laps", integer), p("grid", short), p("gap", short),
			))),
			p("sprint_event", object(
				p("id", integer),
				p("competition", object(
					p("id", integer), p("name", title),
					p("location", object(p("country", title), p("city", title))),
				)),
				p("circuit", object(p("id", integer), p("name", title), p("image", addr))),
				p("season", integer), p("type", short),
				p("laps", object(p("current", integer), p("total", integer))),
				p("fastest_lap", object(p("driver", object(p("id", integer))), p("time", short))),
				p("distance", anyJSON), p("date", short), p("status", short),
			)),
			p("sprint_standings", richList(object(
				p("position", integer),
				p("driver", object(
					p("id", integer), p("name", title), p("abbr", short), p("number", integer),
					p("image", addr),
				)),
				p("team", object(p("id", integer), p("name", title), p("logo", addr))), p("points", decimal),
				p("wins", integer), p("behind", integer), p("season", integer), p("time", short),
				p("laps", integer), p("grid", short), p("gap", short),
			))),
			p("weekend_events", richList(object(p("type", short), p("date", short), p("status", short)))),
		)),
		p("previous_race", object(
			p("event", object(
				p("id", integer),
				p("competition", object(
					p("id", integer), p("name", title),
					p("location", object(p("country", title), p("city", title))),
				)),
				p("circuit", object(p("id", integer), p("name", title), p("image", addr))),
				p("season", integer), p("type", short),
				p("laps", object(p("current", integer), p("total", integer))),
				p("fastest_lap", object(p("driver", object(p("id", integer))), p("time", short))),
				p("distance", anyJSON), p("date", short), p("status", short),
			)),
			p("standings", richList(object(
				p("position", integer),
				p("driver", object(
					p("id", integer), p("name", title), p("abbr", short), p("number", integer),
					p("image", addr),
				)),
				p("team", object(p("id", integer), p("name", title), p("logo", addr))), p("points", decimal),
				p("wins", integer), p("behind", integer), p("season", integer), p("time", short),
				p("laps", integer), p("grid", short), p("gap", short),
			))),
			p("qualifying", richList(object(
				p("position", integer),
				p("driver", object(
					p("id", integer), p("name", title), p("abbr", short), p("number", integer),
					p("image", addr),
				)),
				p("team", object(p("id", integer), p("name", title), p("logo", addr))), p("points", decimal),
				p("wins", integer), p("behind", integer), p("season", integer), p("time", short),
				p("laps", integer), p("grid", short), p("gap", short),
			))),
			p("sprint_shootout", richList(object(
				p("position", integer),
				p("driver", object(
					p("id", integer), p("name", title), p("abbr", short), p("number", integer),
					p("image", addr),
				)),
				p("team", object(p("id", integer), p("name", title), p("logo", addr))), p("points", decimal),
				p("wins", integer), p("behind", integer), p("season", integer), p("time", short),
				p("laps", integer), p("grid", short), p("gap", short),
			))),
			p("sprint_event", object(
				p("id", integer),
				p("competition", object(
					p("id", integer), p("name", title),
					p("location", object(p("country", title), p("city", title))),
				)),
				p("circuit", object(p("id", integer), p("name", title), p("image", addr))),
				p("season", integer), p("type", short),
				p("laps", object(p("current", integer), p("total", integer))),
				p("fastest_lap", object(p("driver", object(p("id", integer))), p("time", short))),
				p("distance", anyJSON), p("date", short), p("status", short),
			)),
			p("sprint_standings", richList(object(
				p("position", integer),
				p("driver", object(
					p("id", integer), p("name", title), p("abbr", short), p("number", integer),
					p("image", addr),
				)),
				p("team", object(p("id", integer), p("name", title), p("logo", addr))), p("points", decimal),
				p("wins", integer), p("behind", integer), p("season", integer), p("time", short),
				p("laps", integer), p("grid", short), p("gap", short),
			))),
			p("weekend_events", richList(object(p("type", short), p("date", short), p("status", short)))),
		)),
		p("live_race", object(
			p("event", object(
				p("id", integer),
				p("competition", object(
					p("id", integer), p("name", title),
					p("location", object(p("country", title), p("city", title))),
				)),
				p("circuit", object(p("id", integer), p("name", title), p("image", addr))),
				p("season", integer), p("type", short),
				p("laps", object(p("current", integer), p("total", integer))),
				p("fastest_lap", object(p("driver", object(p("id", integer))), p("time", short))),
				p("distance", anyJSON), p("date", short), p("status", short),
			)),
			p("standings", richList(object(
				p("position", integer),
				p("driver", object(
					p("id", integer), p("name", title), p("abbr", short), p("number", integer),
					p("image", addr),
				)),
				p("team", object(p("id", integer), p("name", title), p("logo", addr))), p("points", decimal),
				p("wins", integer), p("behind", integer), p("season", integer), p("time", short),
				p("laps", integer), p("grid", short), p("gap", short),
			))),
			p("qualifying", richList(object(
				p("position", integer),
				p("driver", object(
					p("id", integer), p("name", title), p("abbr", short), p("number", integer),
					p("image", addr),
				)),
				p("team", object(p("id", integer), p("name", title), p("logo", addr))), p("points", decimal),
				p("wins", integer), p("behind", integer), p("season", integer), p("time", short),
				p("laps", integer), p("grid", short), p("gap", short),
			))),
			p("sprint_shootout", richList(object(
				p("position", integer),
				p("driver", object(
					p("id", integer), p("name", title), p("abbr", short), p("number", integer),
					p("image", addr),
				)),
				p("team", object(p("id", integer), p("name", title), p("logo", addr))), p("points", decimal),
				p("wins", integer), p("behind", integer), p("season", integer), p("time", short),
				p("laps", integer), p("grid", short), p("gap", short),
			))),
			p("sprint_event", object(
				p("id", integer),
				p("competition", object(
					p("id", integer), p("name", title),
					p("location", object(p("country", title), p("city", title))),
				)),
				p("circuit", object(p("id", integer), p("name", title), p("image", addr))),
				p("season", integer), p("type", short),
				p("laps", object(p("current", integer), p("total", integer))),
				p("fastest_lap", object(p("driver", object(p("id", integer))), p("time", short))),
				p("distance", anyJSON), p("date", short), p("status", short),
			)),
			p("sprint_standings", richList(object(
				p("position", integer),
				p("driver", object(
					p("id", integer), p("name", title), p("abbr", short), p("number", integer),
					p("image", addr),
				)),
				p("team", object(p("id", integer), p("name", title), p("logo", addr))), p("points", decimal),
				p("wins", integer), p("behind", integer), p("season", integer), p("time", short),
				p("laps", integer), p("grid", short), p("gap", short),
			))),
			p("weekend_events", richList(object(p("type", short), p("date", short), p("status", short)))),
		)),
		p("drivers", richList(object(
			p("position", integer),
			p("driver", object(
				p("id", integer), p("name", title), p("abbr", short), p("number", integer), p("image", addr),
			)),
			p("team", object(p("id", integer), p("name", title), p("logo", addr))), p("points", decimal),
			p("wins", integer), p("behind", integer), p("season", integer), p("time", short),
			p("laps", integer), p("grid", short), p("gap", short),
		))),
		p("constructors", richList(object(
			p("position", integer),
			p("driver", object(
				p("id", integer), p("name", title), p("abbr", short), p("number", integer), p("image", addr),
			)),
			p("team", object(p("id", integer), p("name", title), p("logo", addr))), p("points", decimal),
			p("wins", integer), p("behind", integer), p("season", integer), p("time", short),
			p("laps", integer), p("grid", short), p("gap", short),
		))),
	)
)
