package manifest

const schema = `
CREATE TABLE IF NOT EXISTS accounts (
	id                TEXT PRIMARY KEY,
	label             TEXT UNIQUE NOT NULL,
	token_path        TEXT NOT NULL,
	added_at          DATETIME NOT NULL,
	quota_limit       INTEGER,
	quota_usage       INTEGER,
	quota_checked_at  DATETIME
);

CREATE TABLE IF NOT EXISTS virtual_files (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	name          TEXT UNIQUE NOT NULL,
	size          INTEGER NOT NULL,
	content_hash  TEXT NOT NULL,
	chunk_size    INTEGER NOT NULL,
	file_key      BLOB NOT NULL,
	replicas      INTEGER NOT NULL DEFAULT 1,
	version       INTEGER NOT NULL DEFAULT 1,
	status        TEXT NOT NULL,
	created_at    DATETIME NOT NULL,
	modified_at   DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS chunks (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	virtual_file_id   INTEGER NOT NULL REFERENCES virtual_files(id) ON DELETE CASCADE,
	idx               INTEGER NOT NULL,
	account_id        TEXT NOT NULL REFERENCES accounts(id),
	remote_file_id    TEXT NOT NULL,
	remote_folder_id  TEXT NOT NULL,
	plaintext_size    INTEGER NOT NULL,
	plaintext_sha256  TEXT NOT NULL,
	ciphertext_md5    TEXT NOT NULL,
	uploaded_at       DATETIME NOT NULL,
	UNIQUE(virtual_file_id, idx)
);

CREATE TABLE IF NOT EXISTS chunk_replicas (
	chunk_id       INTEGER NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
	account_id     TEXT NOT NULL REFERENCES accounts(id),
	remote_file_id TEXT NOT NULL,
	PRIMARY KEY (chunk_id, account_id)
);
`
