CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    external_user_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS channel_accounts (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    channel_type TEXT NOT NULL,
    account_uid TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    avatar_url TEXT NOT NULL DEFAULT '',
    credential_ciphertext BLOB,
    credential_version INTEGER NOT NULL DEFAULT 0,
    last_bound_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_accounts_unique_user_channel_uid
ON channel_accounts(user_id, channel_type, account_uid);

CREATE TABLE IF NOT EXISTS channel_bindings (
    id TEXT PRIMARY KEY,
    bot_id TEXT NOT NULL DEFAULT '',
    user_id TEXT NOT NULL,
    channel_type TEXT NOT NULL,
    status TEXT NOT NULL,
    provider_binding_ref TEXT NOT NULL DEFAULT '',
    qr_code_payload TEXT NOT NULL DEFAULT '',
    expires_at DATETIME,
    error_message TEXT NOT NULL DEFAULT '',
    channel_account_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    finished_at DATETIME
);

CREATE TABLE IF NOT EXISTS bots (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    channel_type TEXT NOT NULL,
    channel_account_id TEXT NOT NULL DEFAULT '',
    connection_status TEXT NOT NULL,
    connection_error TEXT NOT NULL DEFAULT '',
    last_connected_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_bots_user_id ON bots(user_id);
