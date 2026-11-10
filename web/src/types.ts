export interface BillingStatus {
  provider: string;
  planId: string;
  status: 'active' | 'trialing' | 'past_due' | 'canceled' | 'none';
  currentPeriodEnd: string | null;
  customerId: string | null;
  subscriptionId: string | null;
  entitlements: Record<string, number | boolean | string[]>;
  usage: { ops: number };
  /** False when no payment provider is configured — the page shows the free plan + a disabled state. */
  billingEnabled: boolean;
  publishableKey: string;
}

/** The five states (ADR-0005), in board order. A cancelled task is DONE with the `cancelled` label and
 *  a poisoned one BLOCKED with the `poisoned` label (ADR-0001): neither is a state. */
export const STATUSES = ['TODO', 'IN_PROGRESS', 'BLOCKED', 'REVIEW', 'DONE'] as const;
export type Status = (typeof STATUSES)[number];
export type Priority = 'low' | 'normal' | 'high' | 'urgent';
export type Role = 'viewer' | 'worker' | 'master' | 'member' | 'admin';

export interface Claim {
  owner: string;
  /** Display name of the agent holding the lease (preferred over owner in the UI); null for human claims. */
  agent?: string | null;
  // No lease id: the server never sends it here, only in the claim's own answer (see
  // screens/task/leases.ts, which keeps it for this browser's claims).
  expiresAt: string;
}
export interface Task {
  id: string;
  projectId: string;
  boardId: string;
  columnId: string;
  title: string;
  description: string;
  status: Status;
  priority: Priority;
  assignee: string | null;
  labels: string[];
  requiredCapabilities: string[];
  estimate: number | null;
  dueAt: string | null;
  customFields: Record<string, string | number | boolean>;
  parentTaskId: string | null;
  order: string;
  blockedBy: string[];
  blocks: string[];
  contextSummary: string;
  version: number;
  claim: Claim | null;
  createdBy: string;
  /** Display name of the agent that created the task; null/absent for human-created tasks. */
  createdByAgent?: string | null;
  /** Failed claims so far (the retry policy's count); absent at zero. Poison is BLOCKED with the
   *  `poisoned` label once it reaches the policy's poisonAfterAttempts (ADR-0001). */
  claimAttempts?: number;
  /** ISO creation time. On the wire since the core's `TaskSchema`; the SPA simply never mirrored it,
   *  which is why every time-series in Analytics had to bucket on `updatedAt` or not exist. */
  createdAt: string;
  updatedAt: string;
  /** Runs (Phase 2): parent task id when this task is a run child; absent for normal tasks. */
  runOf?: string | null;
  /** Sprints this ticket belongs to (T-257). Optional for back-compat with pre-field tasks; length
   *  ≤ 1 under SPRINT_EXCLUSIVE (the deployment default), but always an array on the wire. */
  sprintIds?: string[];
  /** Runs: true on a PARENT while a race is open (the parent is non-claimable while racing). */
  racing?: boolean;
  /** Runs: ISO time the current race opened. */
  raceOpenedAt?: string | null;
  /** Advisory model/effort sizing set by the creating agent; the working agent should honor it. */
  modelHint?: { model: string; effort?: 'low' | 'medium' | 'high' | 'xhigh'; rationale?: string } | null;
  /** The work product recorded on complete_task; absent until the ticket is completed. */
  deliverable?: string | null;
  /** Produced artifacts bound to this ticket (T-52); absent on tickets created before the field. */
  artifacts?: Artifact[];
  /** Standing per-ticket reviewer override; null/absent → the project's defaultReviewer. */
  reviewer?: string | null;
  /** The recorded REVIEW handover, stamped when the ticket entered REVIEW. */
  review?: TaskReview | null;
}
/** Who was asked to review, who did the work, and when the review clock started (T-52). */
export interface TaskReview {
  reviewer: string | null;
  completedBy: string;
  completedByAgent: string | null;
  requestedBy: string;
  requestedAt: string;
}
/** Evidence that a ticket's work exists: the reviewed PR, the branch, commits, files, docs. */
export interface Artifact {
  kind: 'pr' | 'branch' | 'commit' | 'file' | 'doc' | 'attachment';
  url: string | null;
  ref: string | null;
  repo: string | null;
  title: string | null;
  addedBy: string;
  addedByAgent: string | null;
  ts: string;
}
export interface Column {
  id: string;
  name: string;
  order: number;
  status: Status;
  wipLimit: number | null;
  // NO `role` field here. The revamp added one (PRD R-010) and test/unit/architecture.test.ts
  // rejected it: this file mirrors what the CORE returns, and the core does not write a column
  // role. Until it does, the web derives the role from `status` via `roleOf()` in ui.tsx — which
  // is where `LifecycleRole` now lives, since it is a web-side vocabulary, not a wire field.
}
export type SprintStatus = 'PROPOSED' | 'ACTIVE' | 'CLOSED' | 'ABANDONED';
/**
 * A named cross-ticket objective (T-257) — mirror of the core's Sprint doc, which is exactly what the
 * gateway's `GET /api/projects/:p/sprints` (list_sprints) returns per row. Like every shape in this
 * file it is a hand-mirrored copy: the SPA is a separate package and cannot import from core.
 */
export interface Sprint {
  id: string;
  projectId: string;
  boardId: string;
  name: string;
  goal: string;
  acceptance: string;
  status: SprintStatus;
  /** The REVIEW-gate task; human sign-off = this task reaching DONE. */
  proposalTaskId: string | null;
  approvedBy: string | null;
  /** Maintained by the membership transactions; NOT a status rollup (done counts derive client-side). */
  memberCount: number;
  /** Sprint-level DONE/NEXT/REJECTED/UNCERTAIN markdown from the supervising master. */
  checkpoint: string | null;
  eventCursor: string | null;
  createdBy: string;
  createdAt: string;
  startedAt: string | null;
  closedAt: string | null;
  updatedBy: string;
  updatedAt: string;
  version: number;
}
export type TransitionTable = Record<string, Status[]>;
export interface Board {
  id: string;
  projectId: string;
  name: string;
  columns: Column[];
  transitions?: TransitionTable;
}
export interface Project {
  id: string;
  name: string;
  members: Record<string, Role>;
  settings: { reviewEnabled: boolean; quotaEnabled: boolean; txnCap: number | null; /** Accent hex color for the project card; null/absent → hashed palette fallback. */ color?: string | null };
}
export interface ContextItem {
  id: string;
  kind: 'note' | 'decision' | 'field' | 'link';
  body: string;
  author: string;
  ts: string;
}
export interface Comment {
  id: string;
  taskId: string;
  author: string;
  body: string;
  parentCommentId: string | null;
  mentions: string[];
  ts: string;
}
export interface Notification {
  id: string;
  identityId: string;
  projectId: string;
  boardId: string | null;
  type: string;
  taskId: string | null;
  actor: string;
  data?: Record<string, unknown>;
  read: boolean;
  ts: string;
}
/** A directed, actionable directive to a task's current holder (reopen-capable). Distinct from a comment. */
export interface FollowUp {
  id: string;
  taskId: string;
  projectId: string;
  body: string;
  author: string;
  /** Resolved holder/assignee at issue time (the notification recipient); null if unheld/unassigned. */
  target: string | null;
  status: 'open' | 'acked';
  reopened: boolean;
  ts: string;
}
/** A compact view of one run child in a race (what list_runs returns per run). */
export interface RunView {
  taskId: string;
  status: Status;
  assignee: string | null;
  leaseHolder: string | null;
  hasDeliverable: boolean;
  deliverable?: string | null;
  version: number;
}
export interface DependencyEdge {
  id: string;
  fromTaskId: string;
  toTaskId: string;
  type: 'blocks' | 'relates';
}
export interface MemoryEntry {
  id: string;
  scope: 'task' | 'agent' | 'shared';
  key: string;
  value: unknown;
  author: string;
  ts: string;
}
export interface Attachment {
  id: string;
  /** The name attach_file was given; "" for an attachment from before names were kept. */
  fileName?: string;
  mimeType: string;
  bytes: number;
  kind: 'file' | 'image' | 'audio' | 'video';
  status: 'pending' | 'ready' | 'failed';
  extractedText: string | null;
  thumbUri: string | null;
  downloadUrl?: string;
  ts: string;
}
export interface ActivityEvent {
  id: string;
  type: string;
  actor: string;
  /** Agent display name when an agent performed the action; null/undefined for human/UI actions. */
  agent?: string | null;
  taskId: string | null;
  data: unknown;
  ts: string;
}

export interface AgentSession {
  sessionKey: string;
  agentId: string; // stable per-agent handle (key component); agentName is the mutable display label
  agentName: string;
  identityId: string;
  identityDisplay?: string | null; // T-409: human label for the OWNER column; null = no account row
  kind: 'api_token' | 'oauth_user' | 'service_account' | 'dev'; // service_account and dev: rows written before P-257
  clientName?: string;
  clientVersion?: string;
  firstSeenAt: string;
  lastSeenAt: string; // the session's last MCP request; a `device:` row's last presence frame
  sessionCount: number;
  requestCount: number;
  toolCounts: Record<string, number>;
  lastTool?: string;
  lastProjectId?: string;
  revokedAt?: string; // set when this session was disconnected
  revokedBy?: string;
  /** T-94: is this agent's wake (push) subscription consumable? `false` = its IAM binding did not land,
   * so it receives no doorbell until it reconnects. ABSENT = no wake channel provisioned (not a fault). */
  wakeBound?: boolean;
  /** A device row's last delivered doorbell cursor (`device:<pid>` rows only, from the presence
   * frame): 0 is a process never rung, so a silent session is visible. Absent on an MCP session. */
  lastDeliveredCursor?: number;
}

export interface OpsSummary {
  projectId: string;
  days: number;
  total: number;
  errors: number;
  errorRate: number;
  avgLatencyMs: number;
  byTool: Array<{ tool: string; calls: number; avgMs: number }>;
  byDay: Array<{ day: string; calls: number }>;
  byAgent: Array<{ agent: string; identityId: string; calls: number }>;
  /** How long a tool_calls row is kept, and the oldest UTC day that reaches. */
  retention: { days: number; oldestDay: string };
}

export interface AccessRequest {
  email: string;
  status: 'pending' | 'approved' | 'denied';
  requestedAt: string;
  decidedAt?: string;
  decidedBy?: string;
  /** True when this account is a workspace admin (owner). Present on the admin access-requests list. */
  isAdmin?: boolean;
}

export function isAgent(id: string): boolean {
  return /agent|bot|\bsvc\b|gserviceaccount/i.test(id);
}

/**
 * Client-side mirrors of the core's input-size caps (T-197), used for `maxLength` on the editable
 * fields so an over-limit paste is stopped in the box instead of coming back as a raw server error.
 *
 * ADVISORY ONLY. The server is the authority — `src/config/constants.ts` — and these must never be
 * treated as the check. They are a hand-mirrored copy for the same reason the shapes above are: the
 * SPA is a separate package and cannot import from core. An unbound copy drifts, so
 * `test/unit/web-limits.test.ts` pins it to the core values.
 */
export const LIMITS = {
  title: 300,
  description: 32_000,
  summary: 20_000,
} as const;

// --- chronicle (CHS-4) ---

/**
 * The three provenance states. `derived-then-edited` is the one that must be VISIBLE, not merely
 * stored: it is what tells a reader that a sentence no longer matches the evidence it still cites.
 * If it is invisible in the UI, the feature's central claim is false.
 */
export type BlockState = 'derived' | 'authored' | 'derived-then-edited';

export interface ChronicleCitation {
  projectId: string;
  boardId: string;
  taskId: string;
  label?: string;
}

export interface ChronicleBlock {
  id: string;
  state: BlockState;
  text: string;
  citations: ChronicleCitation[];
  origin: { adrId: string; blockHash: string } | null;
}

/** What an edit POSTs back. No `state` — the server decides provenance, never the client. */
export interface ChronicleBlockInput {
  id: string;
  text: string;
  citations: ChronicleCitation[];
  origin: { adrId: string; blockHash: string } | null;
}

/** Which decision a sweep left outstanding on a block (T-298). */
export type ChronicleConflictKind = 'conflict' | 'conflict-restored-theirs' | 'kept-ours-orphaned';

/**
 * The persisted "a sweep conflicted here" signal riding every page row. Truthy means a human has a
 * decision to land; it clears the moment any human version lands through the edit path.
 */
export interface ChronicleConflictState {
  versionId: string;
  ts: string;
  items: Array<{ blockId: string; kind: ChronicleConflictKind }>;
}

/** The page-view composition: each outstanding item with THEIRS attached (null when the alternative is an absence). */
export interface ChronicleConflicts {
  versionId: string;
  ts: string;
  items: Array<{ blockId: string; kind: ChronicleConflictKind; theirs: ChronicleBlock | null }>;
}

/** The five kinds, as data so a `<select>` cannot drift from the union. */
export const CHRONICLE_KINDS = ['adr', 'topic', 'feature', 'diagram', 'contract'] as const;
export type ChroniclePageKind = (typeof CHRONICLE_KINDS)[number];

export interface ChroniclePage {
  id: string;
  projectId: string;
  kind: ChroniclePageKind;
  slug: string;
  title: string;
  headVersionId: string;
  sourcePath: string | null;
  /** Optional because rows written before T-298 may omit it; absent means none. */
  conflict?: ChronicleConflictState | null;
  /**
   * T-514 — the generated record's own frontmatter, carried onto the row so the index can count
   * rails, mark supersession and search by topic without fetching a single page body.
   *
   * OPTIONAL AT THIS BOUNDARY even though the core normalises all four before serving them. The
   * server is the authority and it always sends them; declaring them optional here costs one `??`
   * per use and means a row from an older core still renders instead of throwing. Same reasoning as
   * `conflict` above.
   */
  topics?: string[];
  /**
   * `null`/absent is a THIRD state, not a synonym for `partial`: the record declared no coverage.
   * Rendering the pessimistic value for it would put a badge on a page that never claimed one.
   */
  coverage?: 'full' | 'partial' | null;
  /**
   * ADR ids that supersede this record. A superseded record is STILL ACCEPTED — the emitter
   * deliberately does not flip `status` — so this marks it, never strikes it out or calls it void.
   */
  supersededBy?: string[];
  /**
   * Who landed the head version. Moves with the VERSION, not with `updatedAt` — a metadata refresh
   * bumps the timestamp and leaves this alone, so the two must never be rendered as one sentence.
   */
  updatedBy?: { author: string; agent: string | null } | null;
  createdAt: string;
  updatedAt: string;
}

export interface ChronicleVersion {
  id: string;
  pageId: string;
  parentVersionId: string | null;
  source: 'sweep' | 'human';
  author: string;
  agent: string | null;
  ts: string;
  blocks: ChronicleBlock[];
}

/** History is metadata only, so listing versions stays cheap. */
export type ChronicleVersionMeta = Omit<ChronicleVersion, 'blocks'> & { blockCount: number };

/**
 * The signed-in user's own preferences (T-201). `estimated` is always true and is deliberately not
 * optional: token counts are derived from text length, not a model's tokenizer, and the UI must not be
 * able to render the number without also having the caveat to hand.
 */
export interface MySettings {
  maxTicketTokens: number | null;
  tokenPolicy: 'off' | 'warn' | 'enforce' | null;
  /** Studio shortlist — `scope/type/assetId` keys. Always an array on the wire. */
  starredAssets: string[];
  defaults: { maxTicketTokens: number; tokenPolicy: 'off' | 'warn' | 'enforce'; charsPerToken: number };
  estimated: true;
}

// --- on-call telemetry integrations (spec §9/§10) ---

// Mirrors src/domain/oncall-sources.ts. The web package has no dependency on the core, so this is a
// hand-written copy — and test/unit/oncall-source-registry.test.ts fails when it drifts.
export type IntegrationSource =
  | 'grafana'
  | 'alertmanager'
  | 'dynatrace'
  | 'pagerduty'
  | 'datadog'
  | 'gcp'
  | 'generic'
  | 'webhook';
export type IntegrationAuthMode = 'hmac' | 'api_key' | 'basic' | 'jwt' | 'mtls';
export type LifecycleMode = 'correlate' | 'autoclose' | 'per_event';
export type RoutingMode = 'oncall' | 'broadcast' | 'upstream';

/** An integration as the API returns it: auth is REDACTED to its shape — the mode, when it rotated,
 * whether a dual-accept window is open — and never carries a secret, key or hash. */
export interface Integration {
  id: string;
  projectId: string;
  name: string;
  source: IntegrationSource;
  enabled: boolean;
  target: { boardId: string; columnId?: string | null };
  auth: {
    mode: IntegrationAuthMode;
    rotatedAt?: string;
    externallyIssued?: boolean;
    dualAcceptUntil?: string | null;
  };
  mapping: IntegrationMappingBody;
  routing: {
    mode?: RoutingMode;
    onCallAgent?: string | null;
    pdUserMap?: Record<string, string>;
  };
  lifecycle: LifecycleMode;
  policy: Record<string, unknown>;
  limits: { ratePerMin: number; payloadCapKb: number };
  version: number;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
  /** The URL path segment to paste into the source's webhook configuration. */
  endpointPath: string;
  /** Present only on a tombstoned row — `list_integrations` returns those under `includeDeleted`. */
  deletedAt?: string;
}

export interface Delivery {
  deliveryId: string;
  ts: string;
  kind: 'firing' | 'resolved' | 'test' | 'rejected';
  result: string;
  taskId?: string | null;
  error?: string | null;
  fingerprint?: string | null;
  title?: string | null;
  severity?: string | null;
}

export type AlertStatus = 'firing' | 'resolved' | 'test';

/**
 * One entry of the source catalog, served by `GET /api/oncall/sources`.
 *
 * NOT a hand-mirrored copy of `src/domain/oncall-sources.ts` — this is the WIRE shape of the thing
 * the gateway sends. The setup form renders itself from it, so adding a source to the catalog needs
 * no edit in this package beyond the `IntegrationSource` union (which is a type, not data).
 */
export interface OncallSource {
  id: IntegrationSource;
  label: string;
  /** The modes this source can PHYSICALLY produce, best first. Not a preference — a capability. */
  authModes: IntegrationAuthMode[];
  docsPage: string;
  authNote: string;
}

/** `webhook` source only: where the fields live in a payload shape BatonDeck does not know. */
export interface WebhookExtract {
  events?: string;
  fingerprint?: string[];
  title?: string[];
  severity?: string[];
  status?: string[];
  description?: string[];
  occurredAt?: string[];
  labels?: string;
  annotations?: string;
  links?: string[];
  dedupKey?: string[];
}

export interface IntegrationMappingBody {
  severityMap?: Record<string, Priority>;
  labels?: string[];
  annotationAllowlist?: string[];
  titleTemplate?: string | null;
  extract?: WebhookExtract;
  statusMap?: Record<string, AlertStatus>;
  defaults?: { title?: string; severity?: string; status?: AlertStatus };
}

export interface CreateIntegrationBody {
  name: string;
  source: IntegrationSource;
  boardId: string;
  columnId?: string;
  auth: { mode: IntegrationAuthMode; externalSecret?: string; apiKey?: string; jwt?: { issuer: string; audience: string; jwksUrl: string; subject?: string; email?: string } };
  routing: { mode: RoutingMode; onCallAgent?: string | null; pdUserMap?: Record<string, string> };
  mapping?: IntegrationMappingBody;
  lifecycle?: LifecycleMode;
  limits?: { ratePerMin?: number; payloadCapKb?: number };
}

export type IntegrationPatchBody = Partial<Omit<CreateIntegrationBody, 'source' | 'columnId'>> & {
  /** null clears the target column; absent leaves it as it is. */
  columnId?: string | null;
  enabled?: boolean;
};
