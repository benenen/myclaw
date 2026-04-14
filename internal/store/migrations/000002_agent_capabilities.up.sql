CREATE TABLE IF NOT EXISTS agent_capabilities (
    id TEXT PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL,
    command TEXT NOT NULL,
    args_json TEXT NOT NULL DEFAULT '[]',
    supported_modes_json TEXT NOT NULL DEFAULT '[]',
    available INTEGER NOT NULL DEFAULT 0,
    detection_source TEXT NOT NULL DEFAULT '',
    last_detected_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

ALTER TABLE bots ADD COLUMN agent_capability_id TEXT NOT NULL DEFAULT '';
ALTER TABLE bots ADD COLUMN agent_mode TEXT NOT NULL DEFAULT '';
