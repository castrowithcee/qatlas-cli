package brave

// Rich vertical shapes of Brave's reference: team sports and cricket.
// any, an object without listed members, and a list of unspecified items are anyJSON.

var (
	richAmericanFootball = object(
		p("sport", short), p("type", short), p("view", short),
		p("content", object(
			p("type", short),
			p("league", object(
				p("id", anyJSON), p("name", title), p("logo", addr), p("season", short), p("round", short),
				p("has_standings", boolean),
			)),
			p("game_times", richList(integer)), p("game_time", integer), p("date", short),
			p("date_kind", short),
			p("games", richList(object(
				p("id", anyJSON),
				p("teams", object(
					p("home", object(
						p("id", anyJSON), p("nickname", title), p("location", title), p("name", title),
						p("logo", addr),
					)),
					p("away", object(
						p("id", anyJSON), p("nickname", title), p("location", title), p("name", title),
						p("logo", addr),
					)),
				)),
				p("score", object(p("home", integer), p("away", integer))), p("start_time", integer),
				p("status", object(p("code", short), p("elapsed", short))),
				p("video", object(p("duration", short), p("url", addr), p("thumbnail", addr), p("type", short))),
				p("score_table", object(
					p("q1", object(p("home", integer), p("away", integer))),
					p("q2", object(p("home", integer), p("away", integer))),
					p("q3", object(p("home", integer), p("away", integer))),
					p("q4", object(p("home", integer), p("away", integer))),
					p("ot", object(p("home", integer), p("away", integer))),
				)),
				p("phase", short),
			))),
		)),
	)
	richFootball = object(
		p("sport", short), p("type", short), p("view", short),
		p("content", object(
			p("type", short),
			p("league", object(
				p("id", anyJSON), p("name", title), p("logo", addr), p("season", short), p("round", short),
				p("has_standings", boolean),
			)),
			p("id", anyJSON), p("start_time", integer),
			p("teams", object(
				p("home", object(
					p("id", anyJSON), p("nickname", title), p("location", title), p("name", title),
					p("logo", addr),
				)),
				p("away", object(
					p("id", anyJSON), p("nickname", title), p("location", title), p("name", title),
					p("logo", addr),
				)),
			)),
			p("status", object(p("code", short), p("elapsed", integer), p("extra", integer))),
			p("score", object(p("home", integer), p("away", integer))),
			p("ps_score", object(p("home", integer), p("away", integer))),
			p("statistics", richList(object(p("name", title), p("home", anyJSON), p("away", anyJSON)))),
			p("events", richList(object(
				p("team_id", integer), p("type", short), p("detail", body), p("player_name", title),
				p("assist_name", title), p("elapsed", integer), p("extra", integer),
			))),
			p("lineups", object(
				p("home", object(
					p("formation", short),
					p("startXI", richList(object(
						p("name", title), p("number", integer), p("pos", short), p("grid", short),
						p("photo", addr),
					))),
					p("substitutes", richList(object(
						p("name", title), p("number", integer), p("pos", short), p("grid", short),
						p("photo", addr),
					))),
					p("coach", object(p("name", title), p("photo", addr))),
				)),
				p("away", object(
					p("formation", short),
					p("startXI", richList(object(
						p("name", title), p("number", integer), p("pos", short), p("grid", short),
						p("photo", addr),
					))),
					p("substitutes", richList(object(
						p("name", title), p("number", integer), p("pos", short), p("grid", short),
						p("photo", addr),
					))),
					p("coach", object(p("name", title), p("photo", addr))),
				)),
			)),
			p("aggregate_score", object(p("scores", anyJSON))),
		)),
	)
	richBaseball = object(
		p("sport", short), p("type", short), p("view", short),
		p("content", object(
			p("type", short),
			p("league", object(
				p("id", anyJSON), p("name", title), p("logo", addr), p("season", short), p("round", short),
				p("has_standings", boolean),
			)),
			p("game_times", richList(integer)), p("game_time", integer), p("date", short),
			p("date_kind", short),
			p("games", richList(object(
				p("id", anyJSON),
				p("teams", object(
					p("home", object(
						p("id", anyJSON), p("nickname", title), p("location", title), p("name", title),
						p("logo", addr),
					)),
					p("away", object(
						p("id", anyJSON), p("nickname", title), p("location", title), p("name", title),
						p("logo", addr),
					)),
				)),
				p("score", object(p("home", integer), p("away", integer))), p("start_time", integer),
				p("status", object(p("code", short))),
				p("video", object(p("duration", short), p("url", addr), p("thumbnail", addr), p("type", short))),
				p("score_table", object(
					p("innings", richList(object(p("home", integer), p("away", integer)))),
					p("extra", object(p("home", integer), p("away", integer))),
					p("hits", object(p("home", integer), p("away", integer))),
					p("errors", object(p("home", integer), p("away", integer))),
				)),
			))),
		)),
	)
	richBasketball = object(
		p("sport", short), p("type", short), p("view", short),
		p("content", object(
			p("type", short),
			p("league", object(
				p("id", anyJSON), p("name", title), p("logo", addr), p("season", short), p("round", short),
				p("has_standings", boolean),
			)),
			p("game_times", richList(integer)), p("game_time", integer), p("date", short),
			p("date_kind", short),
			p("games", richList(object(
				p("id", anyJSON),
				p("teams", object(
					p("home", object(
						p("id", anyJSON), p("nickname", title), p("location", title), p("name", title),
						p("logo", addr),
					)),
					p("away", object(
						p("id", anyJSON), p("nickname", title), p("location", title), p("name", title),
						p("logo", addr),
					)),
				)),
				p("score", object(p("home", integer), p("away", integer))), p("start_time", integer),
				p("status", object(p("code", short))),
				p("video", object(p("duration", short), p("url", addr), p("thumbnail", addr), p("type", short))),
				p("score_table", object(
					p("q1", object(p("home", integer), p("away", integer))),
					p("q2", object(p("home", integer), p("away", integer))),
					p("q3", object(p("home", integer), p("away", integer))),
					p("q4", object(p("home", integer), p("away", integer))),
					p("ot", object(p("home", integer), p("away", integer))),
				)),
			))),
		)),
	)
	richIceHockey = object(
		p("sport", short), p("type", short), p("view", short),
		p("content", object(
			p("type", short),
			p("league", object(
				p("id", anyJSON), p("name", title), p("logo", addr), p("season", short), p("round", short),
				p("has_standings", boolean),
			)),
			p("game_times", richList(integer)), p("game_time", integer), p("date", short),
			p("date_kind", short),
			p("games", richList(object(
				p("id", anyJSON),
				p("teams", object(
					p("home", object(
						p("id", anyJSON), p("nickname", title), p("location", title), p("name", title),
						p("logo", addr),
					)),
					p("away", object(
						p("id", anyJSON), p("nickname", title), p("location", title), p("name", title),
						p("logo", addr),
					)),
				)),
				p("score", object(p("home", integer), p("away", integer))), p("start_time", integer),
				p("status", object(p("code", short), p("elapsed", integer))),
				p("video", object(p("duration", short), p("url", addr), p("thumbnail", addr), p("type", short))),
				p("score_table", object(
					p("p1", object(p("home", integer), p("away", integer))),
					p("p2", object(p("home", integer), p("away", integer))),
					p("p3", object(p("home", integer), p("away", integer))),
					p("ot", object(p("home", integer), p("away", integer))),
					p("so", object(p("home", integer), p("away", integer))),
				)),
				p("league_id", integer),
			))),
		)),
	)
	richCricket = object(
		p("sport", short), p("type", short), p("view", short),
		p("content", object(
			p("type", short),
			p("league", object(
				p("id", anyJSON), p("name", title), p("logo", addr), p("season", short), p("round", short),
				p("has_standings", boolean),
			)),
			p("game_times", richList(integer)), p("game_time", integer), p("date", short),
			p("date_kind", short),
			p("games", richList(object(
				p("id", anyJSON),
				p("teams", object(
					p("home", object(
						p("id", anyJSON), p("nickname", title), p("location", title), p("name", title),
						p("logo", addr),
					)),
					p("away", object(
						p("id", anyJSON), p("nickname", title), p("location", title), p("name", title),
						p("logo", addr),
					)),
				)),
				p("score", object(
					p("home", anyJSON), p("away", anyJSON), p("home_runs", short), p("home_overs", short),
					p("away_runs", short), p("away_overs", short),
				)),
				p("start_time", integer), p("status", object(p("code", short))),
				p("video", object(p("duration", short), p("url", addr), p("thumbnail", addr), p("type", short))),
			))),
		)),
	)
)
