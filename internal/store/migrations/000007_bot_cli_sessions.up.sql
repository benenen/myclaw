CREATE TABLE IF NOT EXISTS bot_cli_sessions (
    bot_id     TEXT NOT NULL,
    cli_type   TEXT NOT NULL,
    session_id TEXT NOT NULL DEFAULT '',
    work_dir   TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (bot_id, cli_type)
);
