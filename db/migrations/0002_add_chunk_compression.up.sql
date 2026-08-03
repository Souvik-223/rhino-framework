ALTER TABLE chunks ADD COLUMN compression_algo TEXT NOT NULL DEFAULT 'none';
ALTER TABLE chunks ADD COLUMN compressed_size BIGINT;

UPDATE chunks SET compressed_size = plaintext_size WHERE compressed_size IS NULL;

ALTER TABLE chunks ALTER COLUMN compressed_size SET NOT NULL;
