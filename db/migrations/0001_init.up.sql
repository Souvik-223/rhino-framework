CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL
);

CREATE TABLE accounts (
    id                TEXT PRIMARY KEY,
    user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label             TEXT NOT NULL,
    token_ciphertext  BYTEA NOT NULL,
    added_at          TIMESTAMPTZ NOT NULL,
    quota_limit       BIGINT,
    quota_usage       BIGINT,
    quota_checked_at  TIMESTAMPTZ,
    UNIQUE (user_id, label)
);
CREATE INDEX idx_accounts_user_id ON accounts (user_id);

CREATE TABLE virtual_files (
    id            BIGSERIAL PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    size          BIGINT NOT NULL,
    content_hash  TEXT NOT NULL,
    chunk_size    BIGINT NOT NULL,
    file_key      BYTEA NOT NULL,
    replicas      INTEGER NOT NULL DEFAULT 1,
    version       INTEGER NOT NULL DEFAULT 1,
    status        TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    modified_at   TIMESTAMPTZ NOT NULL,
    UNIQUE (user_id, name)
);
CREATE INDEX idx_virtual_files_user_id ON virtual_files (user_id);
-- text_pattern_ops keeps the "name LIKE prefix || '%'" query in
-- ListVirtualFiles index-able under a non-C locale.
CREATE INDEX idx_vf_user_name_pattern ON virtual_files (user_id, name text_pattern_ops);

CREATE TABLE chunks (
    id                BIGSERIAL PRIMARY KEY,
    user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    virtual_file_id   BIGINT NOT NULL REFERENCES virtual_files(id) ON DELETE CASCADE,
    idx               INTEGER NOT NULL,
    account_id        TEXT REFERENCES accounts(id) ON DELETE SET NULL,
    remote_file_id    TEXT NOT NULL,
    remote_folder_id  TEXT NOT NULL,
    plaintext_size    BIGINT NOT NULL,
    plaintext_sha256  TEXT NOT NULL,
    ciphertext_md5    TEXT NOT NULL,
    uploaded_at       TIMESTAMPTZ NOT NULL,
    UNIQUE (virtual_file_id, idx)
);
CREATE INDEX idx_chunks_user_id ON chunks (user_id);
CREATE INDEX idx_chunks_account_id ON chunks (account_id);

CREATE TABLE chunk_replicas (
    chunk_id       BIGINT NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
    account_id     TEXT NOT NULL REFERENCES accounts(id),
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    remote_file_id TEXT NOT NULL,
    PRIMARY KEY (chunk_id, account_id)
);
CREATE INDEX idx_chunk_replicas_user_id ON chunk_replicas (user_id);
