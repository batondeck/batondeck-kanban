-- +goose Up
-- The base schema: every in-scope area, derived from conductor's src/data/store.ts areas and
-- src/domain/types.ts entities. Conventions, the same on both engines so sqlc emits the same Go
-- types: times are bigint Unix milliseconds (*_at, ts); JSON is text; a boolean is bigint 0/1.
-- Everything a project owns is keyed by project_id first: a task id (T-n) is unique only within
-- its project (conductor rule 6, T-111).

-- --- projects ---
CREATE TABLE projects (
    id text PRIMARY KEY,
    name text NOT NULL,
    -- ProjectSettings as JSON (quotaEnabled and txnCap included: the contract carries them),
    -- minus the two columns below and minus transitionTable (the table is fixed).
    settings text NOT NULL DEFAULT '{}',
    review_enabled bigint NOT NULL DEFAULT 0 CHECK (review_enabled IN (0, 1)),
    -- RetryPolicy as JSON; NULL is the default policy.
    retry_policy text,
    created_by text NOT NULL,
    created_at bigint NOT NULL
);

CREATE TABLE project_members (
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    identity_id text NOT NULL,
    role text NOT NULL CHECK (role IN ('viewer', 'worker', 'master', 'member', 'admin')),
    added_by text NOT NULL,
    added_at bigint NOT NULL,
    PRIMARY KEY (project_id, identity_id)
);

-- One member's CapabilityGrant as JSON. Removing the member removes the grant.
CREATE TABLE capability_grants (
    project_id text NOT NULL,
    identity_id text NOT NULL,
    grant_json text NOT NULL,
    updated_by text NOT NULL,
    updated_at bigint NOT NULL,
    PRIMARY KEY (project_id, identity_id),
    FOREIGN KEY (project_id, identity_id) REFERENCES project_members(project_id, identity_id) ON DELETE CASCADE
);

-- The per-project task counter: T-n is last_task after the increment, and is never reused.
CREATE TABLE project_counters (
    project_id text PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    last_task bigint NOT NULL
);

-- --- boards / columns ---
CREATE TABLE boards (
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id text NOT NULL,
    name text NOT NULL,
    created_by text NOT NULL,
    created_at bigint NOT NULL,
    PRIMARY KEY (project_id, id)
);

CREATE TABLE columns (
    project_id text NOT NULL,
    board_id text NOT NULL,
    id text NOT NULL,
    name text NOT NULL,
    position bigint NOT NULL,
    status text NOT NULL CHECK (status IN ('BACKLOG', 'READY', 'IN_PROGRESS', 'BLOCKED', 'REVIEW', 'DONE')),
    wip_limit bigint,
    PRIMARY KEY (project_id, board_id, id),
    FOREIGN KEY (project_id, board_id) REFERENCES boards(project_id, id) ON DELETE CASCADE
);

-- --- tasks ---
CREATE TABLE tasks (
    project_id text NOT NULL,
    id text NOT NULL,
    board_id text NOT NULL,
    column_id text NOT NULL,
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    status text NOT NULL CHECK (status IN ('BACKLOG', 'READY', 'IN_PROGRESS', 'BLOCKED', 'REVIEW', 'DONE')),
    -- 'poison' marks a quarantined task; auto-unblock skips it.
    blocked_reason text,
    priority text NOT NULL DEFAULT 'normal' CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    assignee text,
    labels text NOT NULL DEFAULT '[]',
    required_capabilities text NOT NULL DEFAULT '[]',
    estimate double precision,
    due_at bigint,
    parent_task_id text,
    order_key text NOT NULL,
    custom_fields text NOT NULL DEFAULT '{}',
    -- Caches of the canonical dependencies edges, kept in the same transaction.
    blocked_by text NOT NULL DEFAULT '[]',
    blocks text NOT NULL DEFAULT '[]',
    deliverable text,
    reviewer text,
    review text,
    chars text,
    model_hint text,
    run_of text,
    sprint_ids text NOT NULL DEFAULT '[]',
    racing bigint NOT NULL DEFAULT 0 CHECK (racing IN (0, 1)),
    race_opened_at bigint,
    claim_attempts bigint NOT NULL DEFAULT 0,
    next_eligible_at bigint,
    score double precision NOT NULL DEFAULT 0,
    search_tokens text NOT NULL DEFAULT '',
    claim_owner text,
    claim_agent text,
    claim_agent_id text,
    lease_id text,
    lease_expires_at bigint,
    version bigint NOT NULL DEFAULT 1,
    created_by text NOT NULL,
    created_by_agent text,
    created_at bigint NOT NULL,
    updated_by text NOT NULL,
    updated_at bigint NOT NULL,
    -- search_tasks: the title, description and search tokens as one tsvector. 'simple' does no
    -- stemming, as SQLite's FTS5 default tokenizer does none.
    search tsvector GENERATED ALWAYS AS (to_tsvector('simple', title || ' ' || description || ' ' || search_tokens)) STORED,
    PRIMARY KEY (project_id, id),
    FOREIGN KEY (project_id, board_id) REFERENCES boards(project_id, id) ON DELETE CASCADE
);
-- The claim frontier: claim_next and next_task read the eligible tasks of one board and status,
-- best score first.
CREATE INDEX tasks_claim_frontier ON tasks(project_id, board_id, status, next_eligible_at, score DESC);
CREATE INDEX tasks_search ON tasks USING gin (search);

-- Task.artifacts, one row per artifact; a retraction is recorded on the row, never a delete.
CREATE TABLE artifacts (
    project_id text NOT NULL,
    task_id text NOT NULL,
    id text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('pr', 'branch', 'commit', 'file', 'doc', 'attachment')),
    url text,
    ref text,
    repo text,
    title text,
    added_by text NOT NULL,
    added_by_agent text,
    ts bigint NOT NULL,
    retracted_by text,
    retracted_by_agent text,
    retracted_at bigint,
    retracted_reason text,
    PRIMARY KEY (project_id, task_id, id),
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);

-- Task.contextSummary, the task's rolling summary: one row per task that has one.
CREATE TABLE summaries (
    project_id text NOT NULL,
    task_id text NOT NULL,
    body text NOT NULL,
    updated_by text NOT NULL,
    updated_at bigint NOT NULL,
    PRIMARY KEY (project_id, task_id),
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);

-- A run of a raced task: the run is a task whose run_of is the parent; this row holds its
-- ordinal among the parent's runs, which conductor kept only in the run.opened event.
CREATE TABLE runs (
    project_id text NOT NULL,
    run_task_id text NOT NULL,
    parent_task_id text NOT NULL,
    ordinal bigint NOT NULL,
    opened_by text NOT NULL,
    opened_at bigint NOT NULL,
    PRIMARY KEY (project_id, run_task_id),
    FOREIGN KEY (project_id, run_task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);

-- --- events ---
CREATE TABLE task_events (
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id text NOT NULL,
    board_id text,
    task_id text,
    actor text NOT NULL,
    agent text,
    type text NOT NULL,
    data text NOT NULL DEFAULT '{}',
    ts bigint NOT NULL,
    PRIMARY KEY (project_id, id),
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);
-- The board feed, newest or oldest first, by cursor.
CREATE INDEX task_events_feed ON task_events(board_id, ts, id);

-- The bus reads the change log after a cursor (id); a watcher reads its own scope's rows.
CREATE TABLE change_log (
    id bigserial PRIMARY KEY,
    token_scope text NOT NULL,
    project_id text,
    kind text NOT NULL,
    payload text NOT NULL DEFAULT '{}',
    ts bigint NOT NULL
);
CREATE INDEX change_log_scope ON change_log(token_scope, id);

-- --- dependencies (canonical edges; tasks.blocked_by and tasks.blocks are their caches) ---
-- blocker_id is conductor's fromTaskId, dependent_id its toTaskId.
CREATE TABLE dependencies (
    project_id text NOT NULL,
    id text NOT NULL,
    blocker_id text NOT NULL,
    dependent_id text NOT NULL,
    type text NOT NULL DEFAULT 'blocks' CHECK (type IN ('blocks', 'relates')),
    created_by text NOT NULL,
    ts bigint NOT NULL,
    PRIMARY KEY (project_id, id),
    FOREIGN KEY (project_id, blocker_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE,
    FOREIGN KEY (project_id, dependent_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);
CREATE INDEX dependencies_blocker ON dependencies(project_id, blocker_id);
CREATE INDEX dependencies_dependent ON dependencies(project_id, dependent_id);

-- --- context items ---
CREATE TABLE context_items (
    project_id text NOT NULL,
    task_id text NOT NULL,
    id text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('note', 'decision', 'field', 'link')),
    body text NOT NULL,
    author text NOT NULL,
    ts bigint NOT NULL,
    PRIMARY KEY (project_id, task_id, id),
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);

-- --- blobs ---
-- A blob is a file on local disk named by its content hash; attachments and memory point at it.
CREATE TABLE blobs (
    sha256 text PRIMARY KEY,
    bytes bigint NOT NULL,
    created_at bigint NOT NULL
);

-- --- memory (five scopes + TTL) ---
-- project_id is NULL only for agent_global memory, which belongs to an identity, not a project.
CREATE TABLE memory (
    id text PRIMARY KEY,
    project_id text REFERENCES projects(id) ON DELETE CASCADE,
    task_id text,
    scope text NOT NULL CHECK (scope IN ('task', 'agent', 'shared', 'project', 'agent_global')),
    key text NOT NULL,
    value text,
    blob_sha256 text REFERENCES blobs(sha256),
    author text NOT NULL,
    ts bigint NOT NULL,
    expires_at bigint,
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);

-- --- comments, follow-ups, notifications ---
CREATE TABLE comments (
    project_id text NOT NULL,
    task_id text NOT NULL,
    id text NOT NULL,
    author text NOT NULL,
    body text NOT NULL,
    parent_comment_id text,
    mentions text NOT NULL DEFAULT '[]',
    ts bigint NOT NULL,
    PRIMARY KEY (project_id, task_id, id),
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);

CREATE TABLE follow_ups (
    project_id text NOT NULL,
    task_id text NOT NULL,
    id text NOT NULL,
    body text NOT NULL,
    author text NOT NULL,
    target text,
    target_identity text,
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'acked')),
    reopened bigint NOT NULL DEFAULT 0 CHECK (reopened IN (0, 1)),
    ts bigint NOT NULL,
    PRIMARY KEY (project_id, task_id, id),
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);

-- One recipient's in-app feed.
CREATE TABLE notifications (
    identity_id text NOT NULL,
    id text NOT NULL,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    board_id text,
    type text NOT NULL CHECK (type IN ('mention', 'follow_up', 'review_request')),
    task_id text,
    actor text NOT NULL,
    data text NOT NULL DEFAULT '{}',
    read bigint NOT NULL DEFAULT 0 CHECK (read IN (0, 1)),
    ts bigint NOT NULL,
    PRIMARY KEY (identity_id, id)
);

-- --- sprints ---
CREATE TABLE sprints (
    project_id text NOT NULL,
    id text NOT NULL,
    board_id text NOT NULL,
    name text NOT NULL,
    goal text NOT NULL,
    acceptance text NOT NULL,
    status text NOT NULL CHECK (status IN ('PROPOSED', 'ACTIVE', 'CLOSED', 'ABANDONED')),
    proposal_task_id text,
    approved_by text,
    member_count bigint NOT NULL DEFAULT 0,
    checkpoint text,
    event_cursor text,
    version bigint NOT NULL DEFAULT 1,
    created_by text NOT NULL,
    created_at bigint NOT NULL,
    started_at bigint,
    closed_at bigint,
    updated_by text NOT NULL,
    updated_at bigint NOT NULL,
    PRIMARY KEY (project_id, id),
    FOREIGN KEY (project_id, board_id) REFERENCES boards(project_id, id) ON DELETE CASCADE
);

-- --- attachments ---
-- blob_sha256 is NULL while the upload is pending.
CREATE TABLE attachments (
    project_id text NOT NULL,
    task_id text NOT NULL,
    id text NOT NULL,
    blob_sha256 text REFERENCES blobs(sha256),
    mime_type text NOT NULL,
    bytes bigint NOT NULL,
    kind text NOT NULL CHECK (kind IN ('file', 'image', 'audio', 'video')),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready', 'failed')),
    created_by text NOT NULL,
    ts bigint NOT NULL,
    PRIMARY KEY (project_id, task_id, id),
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);

-- --- identities, agent sessions, agent profiles and skill stats ---
CREATE TABLE identities (
    id text PRIMARY KEY,
    kind text NOT NULL,
    display text NOT NULL,
    capabilities text NOT NULL DEFAULT '[]',
    max_ticket_tokens bigint,
    token_policy text CHECK (token_policy IN ('off', 'warn', 'enforce')),
    -- Identity.starredAssets, a JSON array (get_my_settings / set_my_settings).
    starred_assets text NOT NULL DEFAULT '[]'
);

-- MCP connection telemetry, one row per (identity, agent).
CREATE TABLE agent_sessions (
    identity_id text NOT NULL,
    agent_id text NOT NULL,
    agent_name text NOT NULL,
    kind text NOT NULL,
    client_name text,
    client_version text,
    oauth_client_id text,
    first_seen_at bigint NOT NULL,
    last_seen_at bigint NOT NULL,
    live_at bigint,
    wake_attached_at bigint,
    wake_bound bigint NOT NULL DEFAULT 0 CHECK (wake_bound IN (0, 1)),
    session_count bigint NOT NULL DEFAULT 0,
    request_count bigint NOT NULL DEFAULT 0,
    tool_counts text NOT NULL DEFAULT '{}',
    last_tool text,
    last_project_id text,
    revoked_at bigint,
    revoked_by text,
    PRIMARY KEY (identity_id, agent_id)
);

CREATE TABLE agent_profiles (
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    agent_key text NOT NULL,
    capabilities text NOT NULL DEFAULT '[]',
    tags text NOT NULL DEFAULT '[]',
    max_concurrency bigint,
    cost_class text CHECK (cost_class IN ('low', 'standard', 'premium')),
    models text NOT NULL DEFAULT '[]',
    max_effort text CHECK (max_effort IN ('low', 'medium', 'high', 'xhigh')),
    created_by text NOT NULL,
    created_at bigint NOT NULL,
    updated_at bigint NOT NULL,
    PRIMARY KEY (project_id, agent_key)
);

CREATE TABLE skill_stats (
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    agent_key text NOT NULL,
    capability text NOT NULL,
    completed bigint NOT NULL DEFAULT 0,
    blocked bigint NOT NULL DEFAULT 0,
    total_ms bigint NOT NULL DEFAULT 0,
    samples bigint NOT NULL DEFAULT 0,
    updated_at bigint NOT NULL,
    PRIMARY KEY (project_id, agent_key, capability)
);

-- A /wake listener or a Claude Code session inbox socket registered for doorbells.
CREATE TABLE wake_sessions (
    id text PRIMARY KEY,
    identity_id text NOT NULL,
    agent_id text NOT NULL,
    socket_path text,
    socket_token_sealed bytea,
    last_cursor bigint,
    created_at bigint NOT NULL,
    last_seen_at bigint NOT NULL,
    expires_at bigint NOT NULL
);

-- --- local users, browser sessions, API tokens ---
CREATE TABLE users (
    id text PRIMARY KEY,
    email text NOT NULL UNIQUE,
    display_name text NOT NULL,
    -- argon2id, in its encoded form.
    password_hash text NOT NULL,
    created_at bigint NOT NULL,
    disabled_at bigint
);

-- A browser session; the cookie carries the secret, this row its hash.
CREATE TABLE sessions (
    id_hash text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token text NOT NULL,
    created_at bigint NOT NULL,
    last_seen_at bigint NOT NULL,
    expires_at bigint NOT NULL
);

-- An API token (conductor's CliToken): the secret's hash, never the secret.
CREATE TABLE tokens (
    id text PRIMARY KEY,
    identity_id text NOT NULL,
    subject text NOT NULL,
    secret_hash text NOT NULL,
    label text NOT NULL,
    permissions text NOT NULL DEFAULT '[]',
    created_at bigint NOT NULL,
    expires_at bigint NOT NULL,
    last_used_at bigint,
    revoked_at bigint
);

-- --- OAuth 2.1 authorization server (opaque tokens) ---
-- A dynamically registered client (RFC 7591); last_used_at lets unused registrations be collected.
CREATE TABLE oauth_clients (
    client_id text PRIMARY KEY,
    client_name text NOT NULL DEFAULT '',
    redirect_uris text NOT NULL,
    grant_types text NOT NULL DEFAULT '["authorization_code","refresh_token"]',
    response_types text NOT NULL DEFAULT '["code"]',
    token_endpoint_auth_method text NOT NULL DEFAULT 'none',
    scope text NOT NULL DEFAULT '',
    created_at bigint NOT NULL,
    last_used_at bigint
);

-- An authorization code, by its hash; PKCE S256 only; used once.
CREATE TABLE oauth_codes (
    code_hash text PRIMARY KEY,
    client_id text NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    identity_id text NOT NULL,
    redirect_uri text NOT NULL,
    code_challenge text NOT NULL,
    code_challenge_method text NOT NULL DEFAULT 'S256' CHECK (code_challenge_method IN ('S256')),
    scope text NOT NULL DEFAULT '',
    resource text NOT NULL DEFAULT '',
    created_at bigint NOT NULL,
    expires_at bigint NOT NULL,
    used_at bigint
);

-- A refresh-token family: reuse of a rotated refresh token revokes the whole family.
CREATE TABLE oauth_refresh_families (
    id text PRIMARY KEY,
    client_id text NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    identity_id text NOT NULL,
    scope text NOT NULL DEFAULT '',
    resource text NOT NULL DEFAULT '',
    created_at bigint NOT NULL,
    revoked_at bigint,
    revoked_reason text
);

-- An access or refresh token, by its hash, bound to its family and its resource (RFC 8707).
CREATE TABLE oauth_tokens (
    token_hash text PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('access', 'refresh')),
    family_id text NOT NULL REFERENCES oauth_refresh_families(id) ON DELETE CASCADE,
    client_id text NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    identity_id text NOT NULL,
    scope text NOT NULL DEFAULT '',
    resource text NOT NULL DEFAULT '',
    created_at bigint NOT NULL,
    expires_at bigint NOT NULL,
    used_at bigint,
    revoked_at bigint
);

-- --- audit, analytics, idempotency ---
-- One row per sensitive mutation: conductor's AUDIT_LOG_FIELDS minus tenantId.
CREATE TABLE audit_log (
    id bigserial PRIMARY KEY,
    ts bigint NOT NULL,
    action text NOT NULL,
    actor text NOT NULL,
    agent text NOT NULL DEFAULT '',
    subject text NOT NULL DEFAULT '',
    project_id text NOT NULL DEFAULT '',
    result text NOT NULL,
    error_code text NOT NULL DEFAULT '',
    data text NOT NULL DEFAULT '{}'
);

-- One row per MCP tool call (conductor's structured `tool` log line).
CREATE TABLE tool_calls (
    id bigserial PRIMARY KEY,
    ts bigint NOT NULL,
    tool text NOT NULL,
    identity text NOT NULL,
    agent text NOT NULL DEFAULT '',
    outcome text NOT NULL CHECK (outcome IN ('ok', 'error')),
    code text NOT NULL,
    latency_ms bigint NOT NULL,
    project_id text NOT NULL DEFAULT ''
);

-- A creating mutation's result, replayed for the same (project, identity, key) until expires_at.
CREATE TABLE idempotency_keys (
    project_id text NOT NULL,
    identity text NOT NULL,
    key text NOT NULL,
    result text NOT NULL,
    created_at bigint NOT NULL,
    expires_at bigint NOT NULL,
    PRIMARY KEY (project_id, identity, key)
);

-- --- chronicle ---
CREATE TABLE chronicle_pages (
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('adr', 'topic', 'feature', 'diagram', 'contract')),
    slug text NOT NULL,
    title text NOT NULL,
    head_version_id text NOT NULL,
    source_path text,
    last_derived_object text,
    conflict text,
    topics text NOT NULL DEFAULT '[]',
    coverage text CHECK (coverage IN ('full', 'partial')),
    superseded_by text NOT NULL DEFAULT '[]',
    updated_by text,
    deleting bigint NOT NULL DEFAULT 0 CHECK (deleting IN (0, 1)),
    created_at bigint NOT NULL,
    updated_at bigint NOT NULL,
    PRIMARY KEY (project_id, id)
);

-- A version's blocks live in chronicle_blobs under blocks_object.
CREATE TABLE chronicle_versions (
    project_id text NOT NULL,
    page_id text NOT NULL,
    id text NOT NULL,
    parent_version_id text,
    source text NOT NULL CHECK (source IN ('sweep', 'human')),
    author text NOT NULL,
    agent text,
    ts bigint NOT NULL,
    blocks_object text NOT NULL,
    block_count bigint NOT NULL,
    PRIMARY KEY (project_id, page_id, id),
    FOREIGN KEY (project_id, page_id) REFERENCES chronicle_pages(project_id, id) ON DELETE CASCADE
);

-- Content-addressed chronicle objects (block lists, conflict copies, merge bases).
CREATE TABLE chronicle_blobs (
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    object_key text NOT NULL,
    body bytea NOT NULL,
    created_at bigint NOT NULL,
    PRIMARY KEY (project_id, object_key)
);

-- --- on-call integrations and webhook deliveries ---
-- id is unguessable and unique on its own: POST /hooks/{integrationId} resolves the project from it.
CREATE TABLE integrations (
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id text NOT NULL UNIQUE,
    name text NOT NULL,
    source text NOT NULL,
    enabled bigint NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    board_id text NOT NULL,
    column_id text,
    -- IntegrationAuth as JSON; its secrets are sealed with the keyring before they get here.
    auth text NOT NULL,
    mapping text NOT NULL DEFAULT '{}',
    routing text NOT NULL,
    lifecycle text NOT NULL DEFAULT 'correlate' CHECK (lifecycle IN ('correlate', 'autoclose', 'per_event')),
    policy text NOT NULL DEFAULT '{}',
    limits text NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_by text NOT NULL,
    created_at bigint NOT NULL,
    updated_at bigint NOT NULL,
    deleted_at bigint,
    PRIMARY KEY (project_id, id)
);

-- An alert fingerprint's correlation to its incident ticket (conductor AlertState). task_id has
-- no foreign key: the row outlives the ticket, and a warm fingerprint may point at a deleted one.
CREATE TABLE alert_states (
    project_id text NOT NULL,
    integration_id text NOT NULL,
    fingerprint_hash text NOT NULL,
    fingerprint text NOT NULL,
    task_id text NOT NULL,
    occurrences bigint NOT NULL DEFAULT 1,
    first_at bigint NOT NULL,
    last_at bigint NOT NULL,
    alert_status text NOT NULL CHECK (alert_status IN ('firing', 'resolved')),
    expires_at bigint,
    PRIMARY KEY (project_id, integration_id, fingerprint_hash),
    FOREIGN KEY (project_id, integration_id) REFERENCES integrations(project_id, id) ON DELETE CASCADE
);

-- id is sha256(delivery_id), hex.
CREATE TABLE deliveries (
    project_id text NOT NULL,
    integration_id text NOT NULL,
    id text NOT NULL,
    delivery_id text NOT NULL,
    ts bigint NOT NULL,
    kind text NOT NULL CHECK (kind IN ('firing', 'resolved', 'test', 'rejected')),
    result text NOT NULL CHECK (result IN ('created', 'appended', 'annotated', 'closed', 'recorded', 'rejected', 'rate_limited', 'coalesced')),
    task_id text,
    error text,
    normalized_event text,
    expires_at bigint NOT NULL,
    PRIMARY KEY (project_id, integration_id, id),
    FOREIGN KEY (project_id, integration_id) REFERENCES integrations(project_id, id) ON DELETE CASCADE
);

-- The raw body of a rejected delivery, kept for diagnosis until expires_at.
CREATE TABLE delivery_raw_bodies (
    project_id text NOT NULL,
    integration_id text NOT NULL,
    delivery_id text NOT NULL,
    body bytea NOT NULL,
    expires_at bigint NOT NULL,
    PRIMARY KEY (project_id, integration_id, delivery_id),
    FOREIGN KEY (project_id, integration_id, delivery_id) REFERENCES deliveries(project_id, integration_id, id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE delivery_raw_bodies;
DROP TABLE deliveries;
DROP TABLE alert_states;
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
DROP TABLE tasks;
DROP TABLE columns;
DROP TABLE boards;
DROP TABLE project_counters;
DROP TABLE capability_grants;
DROP TABLE project_members;
DROP TABLE projects;
