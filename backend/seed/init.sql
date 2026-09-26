CREATE USER launchday WITH PASSWORD 'launchday';

CREATE DATABASE student OWNER launchday;
CREATE DATABASE reference OWNER launchday;
CREATE DATABASE buggy OWNER launchday;

\connect student
CREATE TABLE items (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    available INT NOT NULL
);
CREATE TABLE reservations (
    id TEXT PRIMARY KEY,
    item_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    qty INT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO launchday;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO launchday;

\connect reference
CREATE TABLE items (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    available INT NOT NULL
);
CREATE TABLE reservations (
    id TEXT PRIMARY KEY,
    item_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    qty INT NOT NULL,
    status TEXT NOT NULL,
    idempotency_key TEXT,
    body_hash TEXT,
    mode TEXT NOT NULL DEFAULT 'live',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX reservations_user_idem_idx
    ON reservations (user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE TABLE reservation_events (
    id BIGSERIAL PRIMARY KEY,
    reservation_id TEXT NOT NULL,
    event TEXT NOT NULL,
    detail TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO launchday;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO launchday;

\connect buggy
CREATE TABLE items (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    available INT NOT NULL
);
CREATE TABLE reservations (
    id TEXT PRIMARY KEY,
    item_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    qty INT NOT NULL,
    status TEXT NOT NULL,
    idempotency_key TEXT,
    body_hash TEXT,
    mode TEXT NOT NULL DEFAULT 'live',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Intentionally NO unique constraint on (user_id, idempotency_key) — D1.
CREATE TABLE reservation_events (
    id BIGSERIAL PRIMARY KEY,
    reservation_id TEXT NOT NULL,
    event TEXT NOT NULL,
    detail TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO launchday;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO launchday;
