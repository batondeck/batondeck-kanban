-- +goose Up
-- The base schema: every in-scope area, derived from conductor's src/data/store.ts areas and
-- src/domain/types.ts entities. Conventions, the same on both engines so sqlc emits the same Go
-- types: times are INTEGER Unix milliseconds (*_at, ts); JSON is TEXT; a boolean is INTEGER 0/1.
-- Everything a project owns is keyed by project_id first: a task id (T-n) is unique only within
-- its project (conductor rule 6, T-111).

-- --- projects ---
CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    -- ProjectSettings as JSON, minus the two columns below and minus transitionTable (the table
    -- is fixed), quotaEnabled and txnCap (metering is out of scope).
    settings TEXT NOT NULL DEFAULT '{}',
    review_enabled INTEGER NOT NULL DEFAULT 0 CHECK (review_enabled IN (0, 1)),
    -- RetryPolicy as JSON; NULL is the default policy.
    retry_policy TEXT,
    created_by TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE project_members (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    identity_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('viewer', 'worker', 'master', 'member', 'admin')),
    added_by TEXT NOT NULL,
    added_at INTEGER NOT NULL,
    PRIMARY KEY (project_id, identity_id)
);

-- One member's CapabilityGrant as JSON. Removing the member removes the grant.
CREATE TABLE capability_grants (
    project_id TEXT NOT NULL,
    identity_id TEXT NOT NULL,
    grant_json TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (project_id, identity_id),
    FOREIGN KEY (project_id, identity_id) REFERENCES project_members(project_id, identity_id) ON DELETE CASCADE
);

-- The per-project task counter: T-n is last_task after the increment, and is never reused.
CREATE TABLE project_counters (
    project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    last_task INTEGER NOT NULL
);

-- --- boards / columns ---
CREATE TABLE boards (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (project_id, id)
);

CREATE TABLE columns (
    project_id TEXT NOT NULL,
    board_id TEXT NOT NULL,
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    position INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('BACKLOG', 'READY', 'IN_PROGRESS', 'BLOCKED', 'REVIEW', 'DONE')),
    wip_limit INTEGER,
    PRIMARY KEY (project_id, board_id, id),
    FOREIGN KEY (project_id, board_id) REFERENCES boards(project_id, id) ON DELETE CASCADE
);

-- --- tasks ---
-- rid is the stable integer rowid the FTS5 index below is keyed on (an implicit rowid may be
-- renumbered by VACUUM). The task's key is (project_id, id).
CREATE TABLE tasks (
    rid INTEGER PRIMARY KEY,
    project_id TEXT NOT NULL,
    id TEXT NOT NULL,
    board_id TEXT NOT NULL,
    column_id TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('BACKLOG', 'READY', 'IN_PROGRESS', 'BLOCKED', 'REVIEW', 'DONE')),
    -- 'poison' marks a quarantined task; auto-unblock skips it.
    blocked_reason TEXT,
    priority TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    assignee TEXT,
    labels TEXT NOT NULL DEFAULT '[]',
    required_capabilities TEXT NOT NULL DEFAULT '[]',
    estimate REAL,
    due_at INTEGER,
    parent_task_id TEXT,
    order_key TEXT NOT NULL,
    custom_fields TEXT NOT NULL DEFAULT '{}',
    -- Caches of the canonical dependencies edges, kept in the same transaction.
    blocked_by TEXT NOT NULL DEFAULT '[]',
    blocks TEXT NOT NULL DEFAULT '[]',
    deliverable TEXT,
    reviewer TEXT,
    review TEXT,
    chars TEXT,
    model_hint TEXT,
    run_of TEXT,
    sprint_ids TEXT NOT NULL DEFAULT '[]',
    racing INTEGER NOT NULL DEFAULT 0 CHECK (racing IN (0, 1)),
    race_opened_at INTEGER,
    claim_attempts INTEGER NOT NULL DEFAULT 0,
    next_eligible_at INTEGER,
    score REAL NOT NULL DEFAULT 0,
    search_tokens TEXT NOT NULL DEFAULT '',
    claim_owner TEXT,
    claim_agent TEXT,
    claim_agent_id TEXT,
    lease_id TEXT,
    lease_expires_at INTEGER,
    version INTEGER NOT NULL DEFAULT 1,
    created_by TEXT NOT NULL,
    created_by_agent TEXT,
    created_at INTEGER NOT NULL,
    updated_by TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (project_id, id),
    FOREIGN KEY (project_id, board_id) REFERENCES boards(project_id, id) ON DELETE CASCADE
);
-- The claim frontier: claim_next and next_task read the eligible tasks of one board and status,
-- best score first.
CREATE INDEX tasks_claim_frontier ON tasks(project_id, board_id, status, next_eligible_at, score DESC);

-- search_tasks: external-content FTS5 over the task's title, description and search tokens. rid
-- is UNINDEXED and read from tasks itself; it is the join column a query can name (sqlc v1.30.0
-- knows no rowid on a virtual table).
CREATE VIRTUAL TABLE tasks_fts USING fts5(title, description, search_tokens, rid UNINDEXED, content='tasks', content_rowid='rid');
-- The three triggers keep the index in step with tasks.
-- +goose StatementBegin
CREATE TRIGGER tasks_fts_ai AFTER INSERT ON tasks BEGIN
    INSERT INTO tasks_fts(rowid, title, description, search_tokens, rid) VALUES (new.rid, new.title, new.description, new.search_tokens, new.rid);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER tasks_fts_ad AFTER DELETE ON tasks BEGIN
    INSERT INTO tasks_fts(tasks_fts, rowid, title, description, search_tokens, rid) VALUES ('delete', old.rid, old.title, old.description, old.search_tokens, old.rid);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER tasks_fts_au AFTER UPDATE OF title, description, search_tokens ON tasks BEGIN
    INSERT INTO tasks_fts(tasks_fts, rowid, title, description, search_tokens, rid) VALUES ('delete', old.rid, old.title, old.description, old.search_tokens, old.rid);
    INSERT INTO tasks_fts(rowid, title, description, search_tokens, rid) VALUES (new.rid, new.title, new.description, new.search_tokens, new.rid);
END;
-- +goose StatementEnd

-- Task.artifacts, one row per artifact; a retraction is recorded on the row, never a delete.
CREATE TABLE artifacts (
    project_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('pr', 'branch', 'commit', 'file', 'doc', 'attachment')),
    url TEXT,
    ref TEXT,
    repo TEXT,
    title TEXT,
    added_by TEXT NOT NULL,
    added_by_agent TEXT,
    ts INTEGER NOT NULL,
    retracted_by TEXT,
    retracted_by_agent TEXT,
    retracted_at INTEGER,
    retracted_reason TEXT,
    PRIMARY KEY (project_id, task_id, id),
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);

-- Task.contextSummary, the task's rolling summary: one row per task that has one.
CREATE TABLE summaries (
    project_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    body TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (project_id, task_id),
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);

-- A run of a raced task: the run is a task whose run_of is the parent; this row holds its
-- ordinal among the parent's runs, which conductor kept only in the run.opened event.
CREATE TABLE runs (
    project_id TEXT NOT NULL,
    run_task_id TEXT NOT NULL,
    parent_task_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    opened_by TEXT NOT NULL,
    opened_at INTEGER NOT NULL,
    PRIMARY KEY (project_id, run_task_id),
    FOREIGN KEY (project_id, run_task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);

-- --- events ---
CREATE TABLE task_events (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    board_id TEXT,
    task_id TEXT,
    actor TEXT NOT NULL,
    agent TEXT,
    type TEXT NOT NULL,
    data TEXT NOT NULL DEFAULT '{}',
    ts INTEGER NOT NULL,
    PRIMARY KEY (project_id, id),
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);
-- The board feed, newest or oldest first, by cursor.
CREATE INDEX task_events_feed ON task_events(board_id, ts, id);

-- The bus reads the change log after a cursor (id); a watcher reads its own scope's rows.
CREATE TABLE change_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    token_scope TEXT NOT NULL,
    project_id TEXT,
    kind TEXT NOT NULL,
    payload TEXT NOT NULL DEFAULT '{}',
    ts INTEGER NOT NULL
);
CREATE INDEX change_log_scope ON change_log(token_scope, id);

-- --- dependencies (canonical edges; tasks.blocked_by and tasks.blocks are their caches) ---
-- blocker_id is conductor's fromTaskId, dependent_id its toTaskId.
CREATE TABLE dependencies (
    project_id TEXT NOT NULL,
    id TEXT NOT NULL,
    blocker_id TEXT NOT NULL,
    dependent_id TEXT NOT NULL,
    type TEXT NOT NULL DEFAULT 'blocks' CHECK (type IN ('blocks', 'relates')),
    created_by TEXT NOT NULL,
    ts INTEGER NOT NULL,
    PRIMARY KEY (project_id, id),
    FOREIGN KEY (project_id, blocker_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE,
    FOREIGN KEY (project_id, dependent_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);
CREATE INDEX dependencies_blocker ON dependencies(project_id, blocker_id);
CREATE INDEX dependencies_dependent ON dependencies(project_id, dependent_id);

-- --- context items ---
CREATE TABLE context_items (
    project_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('note', 'decision', 'field', 'link')),
    body TEXT NOT NULL,
    author TEXT NOT NULL,
    ts INTEGER NOT NULL,
    PRIMARY KEY (project_id, task_id, id),
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);

-- --- blobs ---
-- A blob is a file on local disk named by its content hash; attachments and memory point at it.
CREATE TABLE blobs (
    sha256 TEXT PRIMARY KEY,
    bytes INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);

-- --- memory (five scopes + TTL) ---
-- project_id is NULL only for agent_global memory, which belongs to an identity, not a project.
CREATE TABLE memory (
    id TEXT PRIMARY KEY,
    project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
    task_id TEXT,
    scope TEXT NOT NULL CHECK (scope IN ('task', 'agent', 'shared', 'project', 'agent_global')),
    key TEXT NOT NULL,
    value TEXT,
    blob_sha256 TEXT REFERENCES blobs(sha256),
    author TEXT NOT NULL,
    ts INTEGER NOT NULL,
    expires_at INTEGER,
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);

-- --- comments, follow-ups, notifications ---
CREATE TABLE comments (
    project_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    id TEXT NOT NULL,
    author TEXT NOT NULL,
    body TEXT NOT NULL,
    parent_comment_id TEXT,
    mentions TEXT NOT NULL DEFAULT '[]',
    ts INTEGER NOT NULL,
    PRIMARY KEY (project_id, task_id, id),
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);

CREATE TABLE follow_ups (
    project_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    id TEXT NOT NULL,
    body TEXT NOT NULL,
    author TEXT NOT NULL,
    target TEXT,
    target_identity TEXT,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'acked')),
    reopened INTEGER NOT NULL DEFAULT 0 CHECK (reopened IN (0, 1)),
    ts INTEGER NOT NULL,
    PRIMARY KEY (project_id, task_id, id),
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);

-- One recipient's in-app feed.
CREATE TABLE notifications (
    identity_id TEXT NOT NULL,
    id TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    board_id TEXT,
    type TEXT NOT NULL CHECK (type IN ('mention', 'follow_up', 'review_request')),
    task_id TEXT,
    actor TEXT NOT NULL,
    data TEXT NOT NULL DEFAULT '{}',
    read INTEGER NOT NULL DEFAULT 0 CHECK (read IN (0, 1)),
    ts INTEGER NOT NULL,
    PRIMARY KEY (identity_id, id)
);

-- --- sprints ---
CREATE TABLE sprints (
    project_id TEXT NOT NULL,
    id TEXT NOT NULL,
    board_id TEXT NOT NULL,
    name TEXT NOT NULL,
    goal TEXT NOT NULL,
    acceptance TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('PROPOSED', 'ACTIVE', 'CLOSED', 'ABANDONED')),
    proposal_task_id TEXT,
    approved_by TEXT,
    member_count INTEGER NOT NULL DEFAULT 0,
    checkpoint TEXT,
    event_cursor TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_by TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    started_at INTEGER,
    closed_at INTEGER,
    updated_by TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (project_id, id),
    FOREIGN KEY (project_id, board_id) REFERENCES boards(project_id, id) ON DELETE CASCADE
);

-- --- attachments ---
-- blob_sha256 is NULL while the upload is pending.
CREATE TABLE attachments (
    project_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    id TEXT NOT NULL,
    blob_sha256 TEXT REFERENCES blobs(sha256),
    mime_type TEXT NOT NULL,
    bytes INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('file', 'image', 'audio', 'video')),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready', 'failed')),
    created_by TEXT NOT NULL,
    ts INTEGER NOT NULL,
    PRIMARY KEY (project_id, task_id, id),
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);

-- --- identities, agent sessions, agent profiles and skill stats ---
CREATE TABLE identities (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    display TEXT NOT NULL,
    capabilities TEXT NOT NULL DEFAULT '[]',
    max_ticket_tokens INTEGER,
    token_policy TEXT CHECK (token_policy IN ('off', 'warn', 'enforce'))
);

-- MCP connection telemetry, one row per (identity, agent).
CREATE TABLE agent_sessions (
    identity_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    kind TEXT NOT NULL,
    client_name TEXT,
    client_version TEXT,
    oauth_client_id TEXT,
    first_seen_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    live_at INTEGER,
    wake_attached_at INTEGER,
    wake_bound INTEGER NOT NULL DEFAULT 0 CHECK (wake_bound IN (0, 1)),
    session_count INTEGER NOT NULL DEFAULT 0,
    request_count INTEGER NOT NULL DEFAULT 0,
    tool_counts TEXT NOT NULL DEFAULT '{}',
    last_tool TEXT,
    last_project_id TEXT,
    revoked_at INTEGER,
    revoked_by TEXT,
    PRIMARY KEY (identity_id, agent_id)
);

CREATE TABLE agent_profiles (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    agent_key TEXT NOT NULL,
    capabilities TEXT NOT NULL DEFAULT '[]',
    tags TEXT NOT NULL DEFAULT '[]',
    max_concurrency INTEGER,
    cost_class TEXT CHECK (cost_class IN ('low', 'standard', 'premium')),
    models TEXT NOT NULL DEFAULT '[]',
    max_effort TEXT CHECK (max_effort IN ('low', 'medium', 'high', 'xhigh')),
    created_by TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (project_id, agent_key)
);

CREATE TABLE skill_stats (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    agent_key TEXT NOT NULL,
    capability TEXT NOT NULL,
    completed INTEGER NOT NULL DEFAULT 0,
    blocked INTEGER NOT NULL DEFAULT 0,
    total_ms INTEGER NOT NULL DEFAULT 0,
    samples INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (project_id, agent_key, capability)
);

-- A /wake listener or a Claude Code session inbox socket registered for doorbells.
CREATE TABLE wake_sessions (
    id TEXT PRIMARY KEY,
    identity_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    socket_path TEXT,
    socket_token_sealed BLOB,
    last_cursor INTEGER,
    created_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);

-- --- local users, browser sessions, API tokens ---
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    -- argon2id, in its encoded form.
    password_hash TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    disabled_at INTEGER
);

-- A browser session; the cookie carries the secret, this row its hash.
CREATE TABLE sessions (
    id_hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);

-- An API token (conductor's CliToken): the secret's hash, never the secret.
CREATE TABLE tokens (
    id TEXT PRIMARY KEY,
    identity_id TEXT NOT NULL,
    subject TEXT NOT NULL,
    secret_hash TEXT NOT NULL,
    label TEXT NOT NULL,
    permissions TEXT NOT NULL DEFAULT '[]',
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    last_used_at INTEGER,
    revoked_at INTEGER
);

-- --- OAuth 2.1 authorization server (opaque tokens) ---
-- A dynamically registered client (RFC 7591); last_used_at lets unused registrations be collected.
CREATE TABLE oauth_clients (
    client_id TEXT PRIMARY KEY,
    client_name TEXT NOT NULL DEFAULT '',
    redirect_uris TEXT NOT NULL,
    grant_types TEXT NOT NULL DEFAULT '["authorization_code","refresh_token"]',
    response_types TEXT NOT NULL DEFAULT '["code"]',
    token_endpoint_auth_method TEXT NOT NULL DEFAULT 'none',
    scope TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    last_used_at INTEGER
);

-- An authorization code, by its hash; PKCE S256 only; used once.
CREATE TABLE oauth_codes (
    code_hash TEXT PRIMARY KEY,
    client_id TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    identity_id TEXT NOT NULL,
    redirect_uri TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    code_challenge_method TEXT NOT NULL DEFAULT 'S256' CHECK (code_challenge_method IN ('S256')),
    scope TEXT NOT NULL DEFAULT '',
    resource TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at INTEGER
);

-- A refresh-token family: reuse of a rotated refresh token revokes the whole family.
CREATE TABLE oauth_refresh_families (
    id TEXT PRIMARY KEY,
    client_id TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    identity_id TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT '',
    resource TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    revoked_at INTEGER,
    revoked_reason TEXT
);

-- An access or refresh token, by its hash, bound to its family and its resource (RFC 8707).
CREATE TABLE oauth_tokens (
    token_hash TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('access', 'refresh')),
    family_id TEXT NOT NULL REFERENCES oauth_refresh_families(id) ON DELETE CASCADE,
    client_id TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    identity_id TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT '',
    resource TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at INTEGER,
    revoked_at INTEGER
);

-- --- audit, analytics, idempotency ---
-- One row per sensitive mutation: conductor's AUDIT_LOG_FIELDS minus tenantId.
CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts INTEGER NOT NULL,
    action TEXT NOT NULL,
    actor TEXT NOT NULL,
    agent TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    result TEXT NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    data TEXT NOT NULL DEFAULT '{}'
);

-- One row per MCP tool call (conductor's structured `tool` log line).
CREATE TABLE tool_calls (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts INTEGER NOT NULL,
    tool TEXT NOT NULL,
    identity TEXT NOT NULL,
    agent TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL CHECK (outcome IN ('ok', 'error')),
    code TEXT NOT NULL,
    latency_ms INTEGER NOT NULL,
    project_id TEXT NOT NULL DEFAULT ''
);

-- A creating mutation's result, replayed for the same (project, identity, key) until expires_at.
CREATE TABLE idempotency_keys (
    project_id TEXT NOT NULL,
    identity TEXT NOT NULL,
    key TEXT NOT NULL,
    result TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    PRIMARY KEY (project_id, identity, key)
);

-- --- chronicle ---
CREATE TABLE chronicle_pages (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('adr', 'topic', 'feature', 'diagram', 'contract')),
    slug TEXT NOT NULL,
    title TEXT NOT NULL,
    head_version_id TEXT NOT NULL,
    source_path TEXT,
    last_derived_object TEXT,
    conflict TEXT,
    topics TEXT NOT NULL DEFAULT '[]',
    coverage TEXT CHECK (coverage IN ('full', 'partial')),
    superseded_by TEXT NOT NULL DEFAULT '[]',
    updated_by TEXT,
    deleting INTEGER NOT NULL DEFAULT 0 CHECK (deleting IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (project_id, id)
);

-- A version's blocks live in chronicle_blobs under blocks_object.
CREATE TABLE chronicle_versions (
    project_id TEXT NOT NULL,
    page_id TEXT NOT NULL,
    id TEXT NOT NULL,
    parent_version_id TEXT,
    source TEXT NOT NULL CHECK (source IN ('sweep', 'human')),
    author TEXT NOT NULL,
    agent TEXT,
    ts INTEGER NOT NULL,
    blocks_object TEXT NOT NULL,
    block_count INTEGER NOT NULL,
    PRIMARY KEY (project_id, page_id, id),
    FOREIGN KEY (project_id, page_id) REFERENCES chronicle_pages(project_id, id) ON DELETE CASCADE
);

-- Content-addressed chronicle objects (block lists, conflict copies, merge bases).
CREATE TABLE chronicle_blobs (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    object_key TEXT NOT NULL,
    body BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (project_id, object_key)
);

-- --- on-call integrations and webhook deliveries ---
-- id is unguessable and unique on its own: POST /hooks/{integrationId} resolves the project from it.
CREATE TABLE integrations (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    source TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    board_id TEXT NOT NULL,
    column_id TEXT,
    -- IntegrationAuth as JSON; its secrets are sealed with the keyring before they get here.
    auth TEXT NOT NULL,
    mapping TEXT NOT NULL DEFAULT '{}',
    routing TEXT NOT NULL,
    lifecycle TEXT NOT NULL DEFAULT 'correlate' CHECK (lifecycle IN ('correlate', 'autoclose', 'per_event')),
    policy TEXT NOT NULL DEFAULT '{}',
    limits TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    created_by TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    deleted_at INTEGER,
    PRIMARY KEY (project_id, id)
);

-- id is sha256(delivery_id), hex.
CREATE TABLE deliveries (
    project_id TEXT NOT NULL,
    integration_id TEXT NOT NULL,
    id TEXT NOT NULL,
    delivery_id TEXT NOT NULL,
    ts INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('firing', 'resolved', 'test', 'rejected')),
    result TEXT NOT NULL CHECK (result IN ('created', 'appended', 'annotated', 'closed', 'recorded', 'rejected', 'rate_limited', 'coalesced')),
    task_id TEXT,
    error TEXT,
    normalized_event TEXT,
    expires_at INTEGER NOT NULL,
    PRIMARY KEY (project_id, integration_id, id),
    FOREIGN KEY (project_id, integration_id) REFERENCES integrations(project_id, id) ON DELETE CASCADE
);

-- The raw body of a rejected delivery, kept for diagnosis until expires_at.
CREATE TABLE delivery_raw_bodies (
    project_id TEXT NOT NULL,
    integration_id TEXT NOT NULL,
    delivery_id TEXT NOT NULL,
    body BLOB NOT NULL,
    expires_at INTEGER NOT NULL,
    PRIMARY KEY (project_id, integration_id, delivery_id),
    FOREIGN KEY (project_id, integration_id, delivery_id) REFERENCES deliveries(project_id, integration_id, id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE delivery_raw_bodies;
DROP TABLE deliveries;
DROP TABLE integrations;
DROP TABLE chronicle_blobs;
DROP TABLE chronicle_versions;
DROP TABLE chronicle_pages;
DROP TABLE idempotency_keys;
DROP TABLE tool_calls;
DROP TABLE audit_log;
DROP TABLE oauth_tokens;
DROP TABLE oauth_refresh_families;
DROP TABLE oauth_codes;
DROP TABLE oauth_clients;
DROP TABLE tokens;
DROP TABLE sessions;
DROP TABLE users;
DROP TABLE wake_sessions;
DROP TABLE skill_stats;
DROP TABLE agent_profiles;
DROP TABLE agent_sessions;
DROP TABLE identities;
DROP TABLE attachments;
DROP TABLE sprints;
DROP TABLE notifications;
DROP TABLE follow_ups;
DROP TABLE comments;
DROP TABLE memory;
DROP TABLE blobs;
DROP TABLE context_items;
DROP TABLE dependencies;
DROP TABLE change_log;
DROP TABLE task_events;
DROP TABLE runs;
DROP TABLE summaries;
DROP TABLE artifacts;
DROP TABLE tasks_fts;
DROP TABLE tasks;
DROP TABLE columns;
DROP TABLE boards;
DROP TABLE project_counters;
DROP TABLE capability_grants;
DROP TABLE project_members;
DROP TABLE projects;
