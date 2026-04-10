CREATE TABLE IF NOT EXISTS repositories (
    id            BIGSERIAL PRIMARY KEY,
    owner         VARCHAR(255) NOT NULL,
    name          VARCHAR(255) NOT NULL,
    last_seen_tag VARCHAR(255) NOT NULL DEFAULT '',
    checked_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(owner, name)
);

CREATE TABLE IF NOT EXISTS subscriptions (
    id                BIGSERIAL PRIMARY KEY,
    email             VARCHAR(255) NOT NULL,
    repository_id     BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    confirmed         BOOLEAN NOT NULL DEFAULT FALSE,
    confirm_token     VARCHAR(64) NOT NULL UNIQUE,
    unsubscribe_token VARCHAR(64) NOT NULL UNIQUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(email, repository_id)
);

CREATE INDEX idx_subscriptions_email ON subscriptions(email);
CREATE INDEX idx_subscriptions_confirmed ON subscriptions(repository_id) WHERE confirmed = TRUE;
