-- +migrate Up

-- ─── PAT lookup by SHA-256 ────────────────────────────────────────────────────
-- PATs were stored only as bcrypt hashes, which cannot be looked up, so the
-- auth middleware bcrypt-compared EVERY unexpired PAT on each "pat_" request:
-- an unauthenticated CPU-exhaustion vector that grew with the number of PATs.
-- PATs are 256-bit random values, so an unsalted SHA-256 is sufficient (and
-- indexable). New tokens store token_sha256 only; legacy rows keep token_hash
-- until first use, when the middleware fills token_sha256 and clears it.
ALTER TABLE personal_access_tokens ADD COLUMN IF NOT EXISTS token_sha256 TEXT;
ALTER TABLE personal_access_tokens ALTER COLUMN token_hash DROP NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS personal_access_tokens_token_sha256_key
    ON personal_access_tokens (token_sha256);

-- +migrate Down

DROP INDEX IF EXISTS personal_access_tokens_token_sha256_key;
ALTER TABLE personal_access_tokens DROP COLUMN IF EXISTS token_sha256;
