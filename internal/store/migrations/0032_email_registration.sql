ALTER TABLE users ADD COLUMN email TEXT;
CREATE UNIQUE INDEX users_email_unique ON users (lower(email)) WHERE email IS NOT NULL;
CREATE TABLE mail_accounts (
 id BIGSERIAL PRIMARY KEY, email TEXT NOT NULL, client_id TEXT NOT NULL, refresh_token TEXT NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT true, sort_order INTEGER NOT NULL DEFAULT 0,
 checked_at TIMESTAMPTZ, last_status TEXT NOT NULL DEFAULT '', last_error TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX mail_accounts_email_unique ON mail_accounts (lower(email));
CREATE TABLE registration_emails (
 email TEXT PRIMARY KEY, code_hash TEXT NOT NULL DEFAULT '', expires_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 attempts INTEGER NOT NULL DEFAULT 0, last_sent_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 pending_hash TEXT NOT NULL DEFAULT '', pending_at TIMESTAMPTZ, send_status TEXT NOT NULL DEFAULT ''
);

ALTER TABLE mail_accounts ADD COLUMN config_version BIGINT NOT NULL DEFAULT 1;
