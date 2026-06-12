CREATE TABLE IF NOT EXISTS registered_agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL DEFAULT 'local',
    bot_id TEXT NOT NULL DEFAULT '',
    endpoint TEXT NOT NULL DEFAULT '',
    auth_token TEXT NOT NULL DEFAULT '',
    health TEXT NOT NULL DEFAULT 'healthy',
    last_heartbeat_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

ALTER TABLE bots ADD COLUMN role TEXT NOT NULL DEFAULT '';
