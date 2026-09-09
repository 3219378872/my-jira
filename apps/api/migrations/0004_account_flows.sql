ALTER TABLE users ADD COLUMN password_set boolean NOT NULL DEFAULT true;
ALTER TABLE instances ADD COLUMN settings jsonb NOT NULL DEFAULT '{}';
CREATE TABLE auth_challenges (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz,
  user_id uuid REFERENCES users(id), email text NOT NULL, purpose text NOT NULL,
  token_hash text NOT NULL, expires_at timestamptz NOT NULL, consumed_at timestamptz,
  attempts bigint NOT NULL DEFAULT 0, metadata jsonb NOT NULL DEFAULT '{}'
);
CREATE UNIQUE INDEX auth_challenges_token ON auth_challenges(token_hash);
CREATE INDEX auth_challenges_expiry ON auth_challenges(expires_at);
CREATE TABLE oauth_accounts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz,
  user_id uuid NOT NULL REFERENCES users(id), provider text NOT NULL, subject text NOT NULL,
  email text NOT NULL, metadata jsonb NOT NULL DEFAULT '{}'
);
CREATE UNIQUE INDEX oauth_accounts_identity_active ON oauth_accounts(provider,subject) WHERE deleted_at IS NULL;
CREATE INDEX oauth_accounts_user ON oauth_accounts(user_id) WHERE deleted_at IS NULL;
CREATE TABLE auth_rate_limits (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz,
  key text NOT NULL UNIQUE, window_started timestamptz NOT NULL DEFAULT now(), attempts bigint NOT NULL DEFAULT 0
);
