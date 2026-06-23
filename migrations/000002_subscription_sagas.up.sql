CREATE TABLE IF NOT EXISTS subscription_sagas (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    state           VARCHAR(20) NOT NULL DEFAULT 'pending',
    email           VARCHAR(255) NOT NULL,
    repo_full_name  VARCHAR(511) NOT NULL,
    confirm_token   VARCHAR(64) NOT NULL,
    subscription_id BIGINT REFERENCES subscriptions(id) ON DELETE SET NULL,
    attempts        INT NOT NULL DEFAULT 0,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_subscription_sagas_stale_pending ON subscription_sagas(updated_at) WHERE state = 'pending';
