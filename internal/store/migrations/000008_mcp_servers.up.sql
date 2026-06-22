CREATE TABLE IF NOT EXISTS mcp_servers (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    server_type TEXT NOT NULL DEFAULT 'http',
    url         TEXT NOT NULL DEFAULT '',
    command     TEXT NOT NULL DEFAULT '',
    args_json   TEXT NOT NULL DEFAULT '[]',
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL,
    updated_at  DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS bot_mcp_servers (
    bot_id        TEXT NOT NULL,
    mcp_server_id TEXT NOT NULL,
    created_at    DATETIME NOT NULL,
    PRIMARY KEY (bot_id, mcp_server_id)
);
CREATE INDEX IF NOT EXISTS idx_bot_mcp_servers_bot ON bot_mcp_servers(bot_id);
