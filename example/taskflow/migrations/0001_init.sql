-- Taskflow schema. This is the reference for swapping the in-memory store for
-- Postgres (see README "Swapping in Postgres"): implement internal/store against
-- these tables, keeping the same method set. docker-compose mounts this file into
-- postgres's docker-entrypoint-initdb.d, so `docker compose up` initialises it.

CREATE TABLE IF NOT EXISTS projects (
    id          TEXT PRIMARY KEY,
    key         TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    color       TEXT,
    status      TEXT NOT NULL DEFAULT 'active',
    -- v2 columns; the v1 API simply ignores them.
    description TEXT,
    owner_id    TEXT,
    task_count  INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tasks (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    title        TEXT NOT NULL,
    description  TEXT,
    status       TEXT NOT NULL DEFAULT 'todo',
    priority     INTEGER NOT NULL DEFAULT 2,
    points       INTEGER,
    progress_pct INTEGER NOT NULL DEFAULT 0,
    assignee_ids TEXT[] NOT NULL DEFAULT '{}',
    labels       TEXT[] NOT NULL DEFAULT '{}',
    due_at       TIMESTAMPTZ,
    search_text  TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS tasks_project_idx ON tasks (project_id);

CREATE TABLE IF NOT EXISTS comments (
    id         TEXT PRIMARY KEY,
    task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    author     TEXT NOT NULL,
    body       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS attachments (
    id         TEXT PRIMARY KEY,
    task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    filename   TEXT NOT NULL,
    size       INTEGER NOT NULL,
    mime_type  TEXT NOT NULL,
    caption    TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS api_tokens (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    scope       TEXT NOT NULL,
    last4       TEXT NOT NULL,
    secret_hash TEXT NOT NULL,   -- never store the raw secret
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
