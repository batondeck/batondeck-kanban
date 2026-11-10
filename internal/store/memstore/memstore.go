// Package memstore is the in-memory store.Store: every area's rows in maps under one mutex, for the
// service lanes to build and test against before the engines' statements exist (ADR-0003 B2). It is
// test-only — imported from _test files alone, which the layering guard allows — and it is judged by
// the same internal/store/conformance battery as SQLite and Postgres (TestMemstoreConformance), so
// it cannot answer differently from them where the battery looks. It imports internal/domain and
// internal/store and nothing else of this module.
package memstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// Store is the in-memory store. The zero value is not usable: call New.
//
// Concurrency is the SQLite engine's: one writer at a time. Every call, read or write, holds the
// database's one mutex for its whole length, and Atomically holds it for the whole callback, so a
// transaction sees no other caller's writes and no caller sees its writes before it returns.
type Store struct {
	db *database
	// tx is true for the Store an Atomically callback is handed: its calls run under the mutex the
	// transaction already holds.
	tx bool
	// done is set once that callback has returned: the transaction is over, and a call through a
	// Store that escaped it fails, as it does on a committed or rolled-back engine transaction.
	done *atomic.Bool
}

type database struct {
	mu     sync.Mutex
	closed bool
	st     *state
}

// state is every row. Rows are held by value and copied on the way in and out (clone), so a caller
// can never change a row the store holds, and a snapshot is a deep copy.
type state struct {
	migrated bool

	projects map[string]store.Project   // by id
	members  map[[2]string]store.Member // by (project, identity)
	grants   map[[2]string]store.Grant  // by (project, identity); gone with the member
	counters map[string]int64           // last task number, by project
	boards   map[[2]string]store.Board  // by (project, board); Columns unsorted
	idem     map[[3]string]idemEntry    // by (project, identity, key)
	users    map[string]store.User      // by id
	sessions map[string]store.Session   // by id hash
	tokens   map[string]store.Token     // by id
	tasks    map[[2]string]store.Task   // by (project, task)
	events   map[[2]string]store.Event  // by (project, event)
	changes  []store.Change             // id ascending
	lastChg  int64                      // the last change id handed out

	contextItems map[[3]string]store.ContextItem    // by (project, task, item)
	artifacts    map[[3]string]store.Artifact       // by (project, task, artifact)
	comments     map[[3]string]store.Comment        // by (project, task, comment)
	followUps    map[[3]string]store.FollowUp       // by (project, task, follow-up)
	memory       map[string]store.MemoryEntry       // by id
	summaries    map[[2]string]store.Summary        // by (project, task)
	edges        map[[2]string]store.DependencyEdge // by (project, edge)
	audit        []store.AuditRow                   // id ascending
	lastAudit    int64                              // the last audit id handed out
	toolCalls    []store.ToolCall                   // insertion order
	agents       map[[2]string]store.AgentSession   // by (identity, agent)
	wake         map[string]store.WakeSession       // by id
	identities   map[string]identityRow             // by id
	attachments  map[[3]string]store.Attachment     // by (project, task, attachment)
	blobs        map[string]store.Blob              // by sha256
	integrations map[[2]string]store.Integration    // by (project, integration)
	deliveries   map[[3]string]store.Delivery       // by (project, integration, delivery key)
	rawBodies    map[[3]string]rawBody              // by (project, integration, delivery key)
	alertStates  map[[3]string]store.AlertState     // by (project, integration, fingerprint key)

	chroniclePages    map[[2]string]store.ChroniclePage    // by (project, page)
	chronicleVersions map[[3]string]store.ChronicleVersion // by (project, page, version); no body
	chronicleObjects  map[[2]string][]byte                 // by (project, key)

	notifications map[[2]string]store.Notification // by (identity, notification)
	profiles      map[[2]string]store.AgentProfile // by (project, agent key)
	skillStats    map[[3]string]store.SkillStat    // by (project, agent key, capability)

	runs    map[[2]string]store.Run    // by (project, run task)
	sprints map[[2]string]store.Sprint // by (project, sprint)

	oauthClients  map[string]store.OAuthClient // by client id
	oauthCodes    map[string]store.OAuthCode   // by code hash
	oauthFamilies map[string]store.OAuthFamily // by id
	oauthTokens   map[string]store.OAuthToken  // by token hash; FamilyRevokedAt unset
}

type idemEntry struct {
	result    []byte
	expiresAt int64
}

// New returns an empty, unmigrated store.
func New() *Store {
	return &Store{db: &database{st: newState()}}
}

func newState() *state {
	return &state{
		projects: map[string]store.Project{},
		members:  map[[2]string]store.Member{},
		grants:   map[[2]string]store.Grant{},
		counters: map[string]int64{},
		boards:   map[[2]string]store.Board{},
		idem:     map[[3]string]idemEntry{},
		users:    map[string]store.User{},
		sessions: map[string]store.Session{},
		tokens:   map[string]store.Token{},
		tasks:    map[[2]string]store.Task{},
		events:   map[[2]string]store.Event{},

		contextItems: map[[3]string]store.ContextItem{},
		artifacts:    map[[3]string]store.Artifact{},
		comments:     map[[3]string]store.Comment{},
		followUps:    map[[3]string]store.FollowUp{},
		memory:       map[string]store.MemoryEntry{},
		summaries:    map[[2]string]store.Summary{},
		edges:        map[[2]string]store.DependencyEdge{},
		agents:       map[[2]string]store.AgentSession{},
		wake:         map[string]store.WakeSession{},
		identities:   map[string]identityRow{},
		attachments:  map[[3]string]store.Attachment{},
		blobs:        map[string]store.Blob{},
		integrations: map[[2]string]store.Integration{},
		deliveries:   map[[3]string]store.Delivery{},
		rawBodies:    map[[3]string]rawBody{},
		alertStates:  map[[3]string]store.AlertState{},

		chroniclePages:    map[[2]string]store.ChroniclePage{},
		chronicleVersions: map[[3]string]store.ChronicleVersion{},
		chronicleObjects:  map[[2]string][]byte{},

		notifications: map[[2]string]store.Notification{},
		profiles:      map[[2]string]store.AgentProfile{},
		skillStats:    map[[3]string]store.SkillStat{},

		runs:    map[[2]string]store.Run{},
		sprints: map[[2]string]store.Sprint{},

		oauthClients:  map[string]store.OAuthClient{},
		oauthCodes:    map[string]store.OAuthCode{},
		oauthFamilies: map[string]store.OAuthFamily{},
		oauthTokens:   map[string]store.OAuthToken{},
	}
}

var _ store.Store = (*Store)(nil)

// do runs fn on the state: under the mutex, or, inside Atomically, under the mutex the transaction
// holds. A cancelled context, a closed store and an unmigrated one fail before fn runs.
func (s *Store) do(ctx context.Context, fn func(st *state) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.over(); err != nil {
		return err
	}
	if !s.tx {
		s.db.mu.Lock()
		defer s.db.mu.Unlock()
	}
	if s.db.closed {
		return fmt.Errorf("memstore: closed")
	}
	if !s.db.st.migrated {
		return fmt.Errorf("memstore: not migrated")
	}
	return fn(s.db.st)
}

// get is do for a call that returns a value.
func get[T any](ctx context.Context, s *Store, fn func(st *state) (T, error)) (T, error) {
	var out T
	err := s.do(ctx, func(st *state) error {
		v, err := fn(st)
		out = v
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

// Migrate marks the store migrated: there is no schema to apply.
func (s *Store) Migrate(ctx context.Context) (store.Migrated, error) {
	if err := ctx.Err(); err != nil {
		return store.Migrated{}, err
	}
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if s.db.st.migrated {
		return store.Migrated{Version: 1}, nil
	}
	s.db.st.migrated = true
	return store.Migrated{Applied: 1, Version: 1}, nil
}

// MigrateDown empties the store and marks it unmigrated, as the engines' down migrations drop every
// table.
func (s *Store) MigrateDown(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.st = newState()
	return nil
}

// SchemaCurrent is nil once Migrate has run.
func (s *Store) SchemaCurrent(ctx context.Context) error {
	return s.do(ctx, func(*state) error { return nil })
}

// ServerVersion names the in-memory store.
func (s *Store) ServerVersion(context.Context) (string, error) { return "memstore", nil }

// HoldsSealedValues is the engines' answer over the maps: a live integration whose auth block holds
// an oncall envelope, or a wake session with a sealed inbox token or bearer.
func (s *Store) HoldsSealedValues(ctx context.Context) (bool, error) {
	return get(ctx, s, func(st *state) (bool, error) {
		for _, i := range st.integrations {
			if i.DeletedAt == nil && bytes.Contains(i.Auth, []byte(`"enc1:`)) {
				return true, nil
			}
		}
		for _, w := range st.wake {
			if w.SocketTokenSealed != nil || w.BearerSealed != nil {
				return true, nil
			}
		}
		return false, nil
	})
}

// Close closes the store; every later call fails.
func (s *Store) Close() error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.closed = true
	return nil
}

// Atomically runs fn with the mutex held throughout, on a snapshot it restores if fn returns an
// error or panics. Called inside fn, it runs fn on the same transaction.
func (s *Store) Atomically(ctx context.Context, fn func(tx store.Store) error) (err error) {
	if s.tx {
		if err := s.over(); err != nil {
			return err
		}
		return fn(s)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if s.db.closed {
		return fmt.Errorf("memstore: closed")
	}
	snapshot := s.db.st.clone()
	committed := false
	defer func() {
		if !committed {
			s.db.st = snapshot
		}
	}()
	tx := &Store{db: s.db, tx: true, done: new(atomic.Bool)}
	defer tx.done.Store(true)
	if err := fn(tx); err != nil {
		return err
	}
	committed = true
	return nil
}

// over refuses a call through a transaction's Store after its Atomically has returned.
func (s *Store) over() error {
	if s.tx && s.done.Load() {
		return domain.NewError(domain.CodeInternal, "memstore: a transaction's Store was used after its Atomically returned", nil)
	}
	return nil
}

func (st *state) clone() *state {
	c := newState()
	c.migrated = st.migrated
	for k, v := range st.projects {
		c.projects[k] = cloneProject(v)
	}
	for k, v := range st.members {
		c.members[k] = v
	}
	for k, v := range st.grants {
		c.grants[k] = cloneGrant(v)
	}
	for k, v := range st.counters {
		c.counters[k] = v
	}
	for k, v := range st.boards {
		c.boards[k] = cloneBoard(v)
	}
	for k, v := range st.idem {
		c.idem[k] = idemEntry{append([]byte(nil), v.result...), v.expiresAt}
	}
	for k, v := range st.users {
		c.users[k] = v
	}
	for k, v := range st.sessions {
		c.sessions[k] = v
	}
	for k, v := range st.tokens {
		c.tokens[k] = v
	}
	for k, v := range st.tasks {
		c.tasks[k] = cloneTask(v)
	}
	for k, v := range st.events {
		c.events[k] = cloneEvent(v)
	}
	c.changes = make([]store.Change, len(st.changes))
	for i, ch := range st.changes {
		c.changes[i] = cloneChange(ch)
	}
	c.lastChg = st.lastChg
	maps.Copy(c.contextItems, st.contextItems)
	// An artifact's and a follow-up's pointers are never written through: a retraction replaces the
	// whole entry. A comment's mentions are copied.
	maps.Copy(c.artifacts, st.artifacts)
	maps.Copy(c.followUps, st.followUps)
	for k, v := range st.comments {
		v.Mentions = cloneStrings(v.Mentions)
		c.comments[k] = v
	}
	for k, v := range st.memory {
		c.memory[k] = cloneMemory(v)
	}
	maps.Copy(c.summaries, st.summaries)
	maps.Copy(c.edges, st.edges)
	c.audit = make([]store.AuditRow, len(st.audit))
	for i, r := range st.audit {
		c.audit[i] = cloneAudit(r)
	}
	c.lastAudit = st.lastAudit
	c.toolCalls = append([]store.ToolCall(nil), st.toolCalls...)
	for k, v := range st.identities {
		v.settings.StarredAssets = cloneStrings(v.settings.StarredAssets)
		c.identities[k] = v
	}
	for k, v := range st.agents {
		c.agents[k] = cloneAgentSession(v)
	}
	for k, v := range st.wake {
		c.wake[k] = cloneWakeSession(v)
	}
	maps.Copy(c.attachments, st.attachments)
	maps.Copy(c.blobs, st.blobs)
	for k, v := range st.chroniclePages {
		c.chroniclePages[k] = cloneChroniclePage(v)
	}
	for k, v := range st.chronicleVersions {
		c.chronicleVersions[k] = cloneChronicleVersion(v)
	}
	for k, v := range st.chronicleObjects {
		c.chronicleObjects[k] = cloneBytes(v)
	}
	for k, v := range st.notifications {
		c.notifications[k] = cloneNotification(v)
	}
	for k, v := range st.profiles {
		c.profiles[k] = cloneProfile(v)
	}
	maps.Copy(c.skillStats, st.skillStats)
	maps.Copy(c.runs, st.runs)
	for k, v := range st.sprints {
		c.sprints[k] = cloneSprint(v)
	}
	for k, v := range st.integrations {
		c.integrations[k] = cloneIntegration(v)
	}
	for k, v := range st.deliveries {
		c.deliveries[k] = cloneDelivery(v)
	}
	for k, v := range st.rawBodies {
		c.rawBodies[k] = rawBody{body: append([]byte(nil), v.body...), expiresAt: v.expiresAt}
	}
	for k, v := range st.alertStates {
		c.alertStates[k] = v
	}
	for k, v := range st.oauthClients {
		c.oauthClients[k] = cloneOAuthClient(v)
	}
	maps.Copy(c.oauthCodes, st.oauthCodes)
	maps.Copy(c.oauthFamilies, st.oauthFamilies)
	maps.Copy(c.oauthTokens, st.oauthTokens)
	return c
}

// newID is prefix and n random hex digits, the engines' id shape.
func newID(prefix string, n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("memstore: crypto/rand: %v", err))
	}
	return prefix + hex.EncodeToString(b)[:n]
}

func nowMillis() int64 { return time.Now().UnixMilli() }

// clamp is a list limit as the contracts read one: <= 0 is def, above max is max.
func clamp(limit, def, max int) int {
	switch {
	case limit <= 0:
		return def
	case limit > max:
		return max
	}
	return limit
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte{}, b...)
}

func cloneStrings(ss []string) []string {
	if ss == nil {
		return nil
	}
	return append([]string{}, ss...)
}
