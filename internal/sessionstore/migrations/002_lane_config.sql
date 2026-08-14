CREATE TABLE IF NOT EXISTS lane_config (
	session_id TEXT NOT NULL,
	lane TEXT NOT NULL,
	config TEXT NOT NULL,
	PRIMARY KEY (session_id, lane)
) WITHOUT ROWID;
