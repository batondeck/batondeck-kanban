package wake

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/bus"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// TapBuffer is the dispatcher's bus.Tap buffer. A change it drops is answered by ringing every
// entry once (a contentless doorbell costs a woken agent one claim_next).
const TapBuffer = 1024

// Options builds a Directory.
type Options struct {
	Bus      *bus.Bus
	Resolver auth.Resolver
	// Scopes is the token scopes of a caller (service.Core.WakeScopes): its identity and the
	// broadcast scope of each project it is a member of.
	Scopes   func(ctx context.Context, caller domain.Caller) ([]string, error)
	Sessions service.WakeStore
	// Registry is Claude Code's session registry directory (~/.claude/sessions).
	Registry string
	Logger   *log.Logger
	// Sweep defaults to the package's Sweep; WriteTimeout bounds each socket step (default 1 s).
	Sweep        time.Duration
	WriteTimeout time.Duration
	// Windows is the coalescing windows; a zero field is the production value (Windows). serve sets
	// none; a test sets a shorter one.
	Windows Windows
	// ProcessUID is core.ProcessUID unless a test replaces it.
	ProcessUID func(pid int) (int, error)
	// TapBuffer is the dispatcher's bus.Tap buffer, TapBuffer when zero. serve sets none; a test that
	// overflows the tap sets a small one.
	TapBuffer int
}

// entry is one registered client process.
type entry struct {
	id, identity, agent, role string
	pid                       int64
	socket, socketToken       string // socket "" is a client with no adapter
	adapter                   string // AdapterClaudeCode, or "" (listed, never rung)
	clientSession, procStart  string // the registry's, at register
	bearer                    string
	scopes                    []string // recipient keys, recomputed by every recheck
	lastCursor                int64
	registered                time.Time
}

// keys is the recipient keys the entry rings on: a worker's token scopes plus its agent label's
// scope; a master's "master:<project>" per project of its scopes.
func (e *entry) keys() []string {
	var out []string
	for _, s := range e.scopes {
		if e.role == service.RoleMaster {
			if p, ok := strings.CutPrefix(s, "project:"); ok {
				out = append(out, "master:"+p)
			}
			continue
		}
		out = append(out, s)
	}
	if e.role == service.RoleWorker && e.agent != "" && !strings.HasPrefix(e.agent, "id:") {
		out = append(out, service.AgentScope(e.agent))
	}
	return out
}

// kind is the doorbell kind r rings e with, "" for none.
func (e *entry) kind(r row) string {
	if e.role == service.RoleMaster {
		if r.ProjectID == "" || masterSilent[r.Tool] || r.Actor == e.identity || !slices.Contains(e.keys(), "master:"+r.ProjectID) {
			return ""
		}
		return KindChanged
	}
	if (r.Kind == KindAssigned || r.Kind == KindBroadcast) && slices.Contains(e.keys(), r.Scope) {
		return r.Kind
	}
	return ""
}

// Directory is the device session directory and its dispatcher. Make one with NewDirectory, then
// Start it; it implements service.DeviceSessions.
type Directory struct {
	o       Options
	watch   *core.ExitWatcher
	mu      sync.Mutex
	entries map[int64]*entry          // pid → entry
	index   map[string]map[int64]bool // recipient key → pids
	changed chan struct{}             // closed on the next change
	catch   chan []released           // catch-up frames for the loop
	rescope chan int64                // a registered pid whose scopes the loop re-reads (Register)
	gen     atomic.Int64              // bumped by every sweep before it lists the entries
	writes  atomic.Int64              // doorbells written, for tests and the log
	done    chan struct{}
}

// rescopes is the tools whose change rows change who is a member of which project, the only
// writers of project_members: the dispatcher re-reads every entry's scopes before it routes one
// (P-229, P-331), so a membership gained or lost is heard at once, not at the next Sweep.
var rescopes = map[string]bool{"add_member": true, "remove_member": true, "create_project": true, "delete_project": true}

var _ service.DeviceSessions = (*Directory)(nil)

// NewDirectory makes a directory; Start opens its exit watch and fills it.
func NewDirectory(o Options) (*Directory, error) {
	if o.Sweep <= 0 {
		o.Sweep = Sweep
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = time.Second
	}
	if o.ProcessUID == nil {
		o.ProcessUID = core.ProcessUID
	}
	if o.TapBuffer <= 0 {
		o.TapBuffer = TapBuffer
	}
	o.Windows = o.Windows.withDefaults()
	return &Directory{o: o, entries: map[int64]*entry{}, index: map[string]map[int64]bool{},
		changed: make(chan struct{}), catch: make(chan []released, 16), rescope: make(chan int64, 16), done: make(chan struct{})}, nil
}

// Start rebuilds the directory from its rows, re-validating each pid, socket and bearer (a row that
// fails is dropped), subscribes the dispatcher to the bus, and runs it until ctx ends or the bus
// closes.
func (d *Directory) Start(ctx context.Context) error {
	w, err := core.NewExitWatcher()
	if err != nil {
		return err
	}
	d.watch = w
	tap, err := d.o.Bus.Tap(ctx, d.o.TapBuffer)
	if err != nil {
		w.Close()
		return err
	}
	rows, err := d.o.Sessions.ListWakeSessions(ctx)
	if err != nil {
		tap.Close()
		w.Close()
		return err
	}
	// The restored entries' catch-up frames go to the loop as it starts, not through d.catch: the
	// loop is its only reader, so a send from here would block Start once more entries had something
	// to catch up on than the channel holds (core-cli-ops#1).
	var missed []released
	for _, r := range rows {
		why, frames := d.restore(ctx, r)
		if why != "" {
			d.o.Logger.Printf("wake: dropped the row of pid %d at start-up: %s", r.PID, why)
			if err := d.o.Sessions.DropWakeSession(ctx, r.ID); err != nil {
				d.o.Logger.Printf("wake: dropping row %s: %+v", r.ID, err)
			}
		}
		missed = append(missed, frames...)
	}
	go d.loop(ctx, tap, missed)
	return nil
}

// restore re-validates one row and adds its entry, "" when it holds, else why not; frames is what
// the entry missed since its last delivered cursor (catchUp).
func (d *Directory) restore(ctx context.Context, r service.WakeSessionRow) (why string, frames []released) {
	switch {
	case r.PID <= 0 || (r.Role != service.RoleMaster && r.Role != service.RoleWorker):
		return "no pid or role (a row from before the directory)", nil
	case !r.Sealed:
		return "its bearer does not open", nil
	}
	if uid, err := d.o.ProcessUID(int(r.PID)); err != nil || uid != os.Getuid() {
		return "the process is gone or not this uid's", nil
	}
	adapter := ""
	if r.SocketPath != "" {
		reg, err := readRegistry(d.o.Registry, r.PID)
		if err != nil || reg.ProcStart != r.ProcStart || reg.MessagingSocketPath != r.SocketPath {
			return "its registry entry is gone or names another process", nil
		}
		if reg.PeerProtocol == peerProtocol {
			adapter = AdapterClaudeCode
		}
	}
	caller, err := d.o.Resolver.ResolveCaller(auth.FreshAccount(ctx), r.Bearer)
	if err != nil || caller.Identity != r.IdentityID {
		return "its bearer no longer resolves to its identity", nil
	}
	if err := d.o.Sessions.CheckWakeAgent(ctx, caller, r.AgentID); err != nil {
		// A row written before the register check held (P-322, P-345), or under a name since minted.
		if be, ok := domain.AsBatonDeckError(err); ok && be.Code == domain.CodeForbidden {
			if strings.HasPrefix(r.AgentID, "id:") {
				return fmt.Sprintf("its agent %q names another identity", r.AgentID), nil
			}
			return fmt.Sprintf("its agent %q is another agent's label", r.AgentID), nil
		}
		return "reading its agent's label failed", nil
	}
	scopes, err := d.o.Scopes(ctx, caller)
	if err != nil {
		return "reading its scopes failed", nil
	}
	if err := d.watch.Add(int(r.PID)); err != nil {
		return "the process exited", nil
	}
	e := &entry{id: r.ID, identity: r.IdentityID, agent: r.AgentID, role: r.Role, pid: r.PID, socket: r.SocketPath,
		socketToken: r.SocketToken, adapter: adapter, clientSession: r.ClientSessionID, procStart: r.ProcStart,
		bearer: r.Bearer, scopes: scopes, lastCursor: r.LastCursor, registered: time.UnixMilli(r.CreatedAt)}
	d.put(e)
	d.o.Logger.Printf("wake: restored pid %d (%s, %s)", e.pid, e.role, e.identity)
	return "", d.missed(ctx, e)
}

// RegisterReq is a registration as the route reads it.
type RegisterReq struct {
	PID       int64
	Role      string
	Agent     string
	Socket    string
	Token     string
	SessionID string
	ProcStart string
	Cursor    int64
}

// RegisterResp is the registered entry.
type RegisterResp struct {
	ID        string `json:"id"`
	PID       int64  `json:"pid"`
	Role      string `json:"role"`
	AgentID   string `json:"agentId"`
	Adapter   string `json:"adapter"`
	ExpiresAt string `json:"expiresAt"`
}

// Register adds the client process req names for caller, whose bearer is bearer and whose
// connection came from peer. The pid must be alive and owned by the peer's uid; a socket must be
// the one the pid's Claude Code registry entry names. A pid already registered by the same
// process (same procStart) is replaced in place and catches up from its last delivered cursor.
func (d *Directory) Register(ctx context.Context, peer core.Peer, caller domain.Caller, bearer string, req RegisterReq) (RegisterResp, error) {
	if req.PID <= 0 {
		return RegisterResp{}, domain.Validation("pid must be a positive process id", map[string]any{"field": "pid"})
	}
	uid, err := d.o.ProcessUID(int(req.PID))
	if errors.Is(err, core.ErrNoProcess) {
		return RegisterResp{}, domain.Validation(fmt.Sprintf("pid %d is not a live process", req.PID), map[string]any{"field": "pid"})
	}
	if err != nil {
		return RegisterResp{}, domain.NewError(domain.CodeInternal, "reading the pid's owner failed", nil)
	}
	if uid != peer.UID {
		d.o.Logger.Printf("wake: refused pid %d: owned by uid %d, the peer (pid %d) is uid %d", req.PID, uid, peer.PID, peer.UID)
		return RegisterResp{}, domain.NewError(domain.CodeForbidden, fmt.Sprintf("pid %d is not owned by the caller's uid", req.PID), map[string]any{"field": "pid"})
	}
	if req.Role != service.RoleMaster && req.Role != service.RoleWorker {
		return RegisterResp{}, domain.Validation("role must be master or worker", map[string]any{"field": "role"})
	}
	var reg registryEntry
	adapter := ""
	if req.Socket != "" {
		if !filepath.IsAbs(req.Socket) {
			return RegisterResp{}, domain.Validation("socket must be an absolute path", map[string]any{"field": "socket"})
		}
		if reg, err = readRegistry(d.o.Registry, req.PID); err != nil {
			return RegisterResp{}, domain.Validation(fmt.Sprintf("pid %d: %v", req.PID, err), map[string]any{"field": "pid"})
		}
		switch {
		case reg.MessagingSocketPath != req.Socket:
			return RegisterResp{}, domain.Validation("the socket is not the one the pid's registry entry names", map[string]any{"field": "socket"})
		case req.SessionID != "" && req.SessionID != reg.SessionID:
			return RegisterResp{}, domain.Validation("the sessionId is not the pid's registry entry's", map[string]any{"field": "sessionId"})
		case req.ProcStart != "" && req.ProcStart != reg.ProcStart:
			return RegisterResp{}, domain.Validation("the procStart is not the pid's registry entry's", map[string]any{"field": "procStart"})
		}
		if reg.PeerProtocol == peerProtocol {
			adapter = AdapterClaudeCode
		}
	}
	// A sweep that lists the entries between this read and the put below misses this entry and may
	// have seen a membership this read did not: the generation says so, and the loop re-reads it.
	gen := d.gen.Load()
	scopes, err := d.o.Scopes(ctx, caller)
	if err != nil {
		return RegisterResp{}, err
	}
	d.mu.Lock()
	var old entry // a copy: the dispatcher writes lastCursor under mu
	cur := d.entries[req.PID]
	found := cur != nil
	if found {
		old = *cur
	}
	d.mu.Unlock()
	id, cursor := "", req.Cursor
	if found {
		if old.identity != caller.Identity {
			return RegisterResp{}, domain.NewError(domain.CodeForbidden, fmt.Sprintf("pid %d is registered by another identity", req.PID), nil)
		}
		if old.procStart == reg.ProcStart {
			id, cursor = old.id, max(cursor, old.lastCursor)
		} else {
			d.drop(ctx, cur, "re-registered by another process")
		}
	}
	if err := d.watch.Add(int(req.PID)); err != nil {
		if errors.Is(err, core.ErrNoProcess) {
			return RegisterResp{}, domain.Validation(fmt.Sprintf("pid %d is not a live process", req.PID), map[string]any{"field": "pid"})
		}
		return RegisterResp{}, domain.NewError(domain.CodeInternal, "watching the pid failed", nil)
	}
	resp, err := d.o.Sessions.RegisterWakeSession(ctx, caller, service.RegisterWakeSessionReq{
		ID: id, PID: req.PID, Role: req.Role, Agent: req.Agent, SocketPath: req.Socket, SocketToken: req.Token,
		ClientSessionID: reg.SessionID, ProcStart: reg.ProcStart, Cursor: cursor, Bearer: bearer,
	})
	if err != nil {
		if !found || old.id != id {
			d.watch.Remove(int(req.PID))
		}
		return RegisterResp{}, err
	}
	e := &entry{id: resp.ID, identity: caller.Identity, agent: resp.AgentID, role: req.Role, pid: req.PID, socket: req.Socket,
		socketToken: req.Token, adapter: adapter, clientSession: reg.SessionID, procStart: reg.ProcStart, bearer: bearer,
		scopes: scopes, lastCursor: cursor, registered: time.Now()}
	d.put(e)
	d.o.Logger.Printf("wake: registered pid %d (%s, %s, agent %s, adapter %q) from peer pid %d", e.pid, e.role, e.identity, e.agent, adapter, peer.PID)
	if d.gen.Load() != gen {
		select {
		case d.rescope <- e.pid:
		case <-ctx.Done():
		case <-d.done:
		}
	}
	if cursor > 0 {
		d.catchUp(ctx, e)
	}
	return RegisterResp{ID: resp.ID, PID: req.PID, Role: req.Role, AgentID: resp.AgentID, Adapter: adapter, ExpiresAt: resp.ExpiresAt}, nil
}

// Unregister drops caller's entry for pid; NOT_FOUND when caller has none there.
func (d *Directory) Unregister(ctx context.Context, caller domain.Caller, pid int64) error {
	d.mu.Lock()
	e := d.entries[pid]
	d.mu.Unlock()
	if e == nil || e.identity != caller.Identity {
		return domain.NotFound("WakeSession", fmt.Sprint(pid))
	}
	d.drop(ctx, e, "unregistered")
	return nil
}

// put adds or replaces e and signals the change.
func (d *Directory) put(e *entry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if old := d.entries[e.pid]; old != nil {
		d.unindex(old)
	}
	d.entries[e.pid] = e
	d.reindex(e)
	d.signal()
}

func (d *Directory) reindex(e *entry) {
	for _, k := range e.keys() {
		if d.index[k] == nil {
			d.index[k] = map[int64]bool{}
		}
		d.index[k][e.pid] = true
	}
}

func (d *Directory) unindex(e *entry) {
	for _, k := range e.keys() {
		delete(d.index[k], e.pid)
		if len(d.index[k]) == 0 {
			delete(d.index, k)
		}
	}
}

// signal closes the change channel; call it with mu held.
func (d *Directory) signal() {
	close(d.changed)
	d.changed = make(chan struct{})
}

// drop removes e and its row, and stops watching its pid, unless a register has replaced e
// meanwhile: the verdict of a recheck that read e is e's, never its replacement's
// (core-cli-ops#12).
func (d *Directory) drop(ctx context.Context, e *entry, why string) {
	pid := e.pid
	d.mu.Lock()
	if d.entries[pid] != e {
		d.mu.Unlock()
		return
	}
	d.unindex(e)
	delete(d.entries, pid)
	d.signal()
	d.mu.Unlock()
	d.watch.Remove(int(pid))
	d.o.Logger.Printf("wake: dropped pid %d (%s, %s): %s", pid, e.role, e.identity, why)
	if err := d.o.Sessions.DropWakeSession(context.WithoutCancel(ctx), e.id); err != nil {
		d.o.Logger.Printf("wake: deleting the row of pid %d: %+v", pid, err)
	}
}

// Entries is the live entries, oldest first (service.DeviceSessions).
func (d *Directory) Entries() []service.DeviceSession {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]service.DeviceSession, 0, len(d.entries))
	for _, e := range d.entries {
		out = append(out, service.DeviceSession{PID: e.pid, Role: e.role, IdentityID: e.identity, AgentID: e.agent, Adapter: e.adapter,
			RegisteredAt: e.registered.UTC().Format(time.RFC3339Nano), LastDeliveredCursor: e.lastCursor})
	}
	slices.SortFunc(out, func(a, b service.DeviceSession) int { return strings.Compare(a.RegisteredAt, b.RegisteredAt) })
	return out
}

// Changed is closed on the next register, unregister or delivered cursor (service.DeviceSessions).
func (d *Directory) Changed() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.changed
}

// Writes is how many doorbells the dispatcher has written.
func (d *Directory) Writes() int64 { return d.writes.Load() }

// Windows is the coalescing windows the dispatcher holds doorbells for.
func (d *Directory) Windows() Windows { return d.o.Windows }

// Done is closed when the dispatcher has stopped.
func (d *Directory) Done() <-chan struct{} { return d.done }

// catchUp hands the loop what e missed (missed), for it to hold as it holds live doorbells. A
// register calls it; Start hands its restored entries' frames to the loop as it starts.
func (d *Directory) catchUp(ctx context.Context, e *entry) {
	if got := d.missed(ctx, e); len(got) > 0 {
		select {
		case d.catch <- got:
		case <-ctx.Done():
		case <-d.done:
		}
	}
}

// missed replays the log after e's last delivered cursor and returns what rings e, coalesced as
// live doorbells are (one frame per kind).
func (d *Directory) missed(ctx context.Context, e *entry) []released {
	d.mu.Lock()
	snap := *e // the loop rewrites the scopes under mu while this replays
	d.mu.Unlock()
	e = &snap
	if e.lastCursor <= 0 {
		return nil
	}
	var replay func(ctx context.Context, since int64, limit int) ([]row, error)
	if e.role == service.RoleMaster {
		var projects []string
		for _, k := range e.keys() {
			projects = append(projects, strings.TrimPrefix(k, "master:"))
		}
		replay = func(ctx context.Context, since int64, limit int) ([]row, error) {
			cs, err := d.o.Bus.ReplayProjects(ctx, projects, since, limit)
			out := make([]row, 0, len(cs))
			for _, c := range cs {
				out = append(out, rowOf(c.ID, c.Scope, c.ProjectID, c.Kind, c.Payload))
			}
			return out, err
		}
	} else {
		scopes := e.keys()
		replay = func(ctx context.Context, since int64, limit int) ([]row, error) {
			cs, err := d.o.Bus.Replay(ctx, scopes, since, limit)
			out := make([]row, 0, len(cs))
			for _, c := range cs {
				out = append(out, rowOf(c.ID, c.Scope, c.ProjectID, c.Kind, c.Payload))
			}
			return out, err
		}
	}
	got := map[string]released{}
	if _, err := catchUp(ctx, replay, e.lastCursor, func(r row) {
		if k := e.kind(r); k != "" {
			f := Frame{Kind: k, Cursor: r.ID, ProjectID: r.ProjectID, BoardID: r.BoardID}
			if old, ok := got[k]; ok {
				f = merge(old.frame, f)
			}
			got[k] = released{pid: e.pid, frame: f}
		}
	}); err != nil {
		d.o.Logger.Printf("wake: catching pid %d up from %d: %+v", e.pid, e.lastCursor, err)
		return nil
	}
	return slices.Collect(maps.Values(got))
}

// loop is the dispatcher: the tap's rows into the coalescer, due frames onto their sockets, exits
// out of the directory, and the sweep. It is the one writer of the entries' scopes (recheck). It
// holds missed, the restored entries' catch-up, first.
func (d *Directory) loop(ctx context.Context, tap *bus.Subscription, missed []released) {
	defer close(d.done)
	defer d.watch.Close()
	defer tap.Close()
	co := newCoalescer()
	d.hold(co, missed)
	sweep := time.NewTicker(d.o.Sweep)
	defer sweep.Stop()
	var dropped int64
	for {
		select {
		case <-ctx.Done():
			return
		case c, open := <-tap.C():
			if !open {
				return
			}
			r := rowOf(c.ID, c.Scope, c.ProjectID, c.Kind, c.Payload)
			if rescopes[r.Tool] {
				d.sweep(ctx) // before r routes: r's own project may be one an entry just joined
			}
			d.route(co, r)
		case frames := <-d.catch:
			d.hold(co, frames)
		case <-co.C():
			for _, r := range co.release(time.Now()) {
				d.deliver(ctx, r)
			}
		case pid := <-d.watch.Exited():
			d.mu.Lock()
			e := d.entries[int64(pid)]
			d.mu.Unlock()
			if e != nil {
				d.drop(ctx, e, "the process exited")
			}
		case pid := <-d.rescope:
			d.mu.Lock()
			e := d.entries[pid]
			d.mu.Unlock()
			if e != nil {
				d.sweepOne(ctx, e)
			}
		case <-sweep.C:
			d.sweep(ctx)
		}
		if n := tap.Dropped(); n > dropped {
			d.o.Logger.Printf("wake: the dispatcher dropped %d change(s); ringing every entry", n-dropped)
			dropped = n
			d.sweep(ctx) // a dropped change may be a membership's (P-229)
			d.ringAll(co)
		}
	}
}

// hold puts catch-up frames into the coalescer, each under its kind's windows.
func (d *Directory) hold(co *coalescer, frames []released) {
	now := time.Now()
	for _, r := range frames {
		q, m := d.o.Windows.of(r.frame.Kind)
		co.add(r.pid, r.frame, now, q, m)
	}
}

// route holds r's doorbell for every entry it rings.
func (d *Directory) route(co *coalescer, r row) {
	d.mu.Lock()
	var hits []released
	for _, k := range []string{r.Scope, "master:" + r.ProjectID} {
		for pid := range d.index[k] {
			if e := d.entries[pid]; e != nil {
				if kind := e.kind(r); kind != "" {
					hits = append(hits, released{pid: pid, frame: Frame{Kind: kind, Cursor: r.ID, ProjectID: r.ProjectID, BoardID: r.BoardID}})
				}
			}
		}
	}
	d.mu.Unlock()
	now := time.Now()
	for _, h := range hits {
		q, m := d.o.Windows.of(h.frame.Kind)
		co.add(h.pid, h.frame, now, q, m)
	}
}

// ringAll holds one frame for every entry at the bus's frontier: a worker's assigned, a master's
// changed, each on its first project.
func (d *Directory) ringAll(co *coalescer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now, cur := time.Now(), d.o.Bus.Frontier()
	for _, e := range d.entries {
		kind := KindAssigned
		if e.role == service.RoleMaster {
			kind = KindChanged
		}
		project := ""
		for _, s := range e.scopes {
			if p, ok := strings.CutPrefix(s, "project:"); ok {
				project = p
				break
			}
		}
		q, m := d.o.Windows.of(kind)
		co.add(e.pid, Frame{Kind: kind, Cursor: cur, ProjectID: project}, now, q, m)
	}
}

// recheck re-resolves e's bearer uncached and re-reads its scopes, updating them in place (owner
// ruling 2026-10-23): "" while the entry holds, else why it must be dropped. ok false is a failed
// read, which keeps the entry and skips this delivery. With agent (the delivery's recheck, not the
// sweep's) a worker's label is judged again as restore judges it, so a free name another identity
// has since minted stops ringing (P-344).
func (d *Directory) recheck(ctx context.Context, e *entry, agent bool) (why string, ok bool) {
	caller, err := d.o.Resolver.ResolveCaller(auth.FreshAccount(ctx), e.bearer)
	if err != nil {
		if be, isBD := domain.AsBatonDeckError(err); isBD && be.Code == domain.CodeUnauthenticated {
			return "its bearer no longer resolves (revoked, expired or its user disabled)", true
		}
		d.o.Logger.Printf("wake: re-resolving the bearer of pid %d: %+v", e.pid, err)
		return "", false
	}
	if caller.Identity != e.identity {
		return "its bearer resolves to another identity", true
	}
	if agent {
		if why, ok := d.recheckAgent(ctx, e, caller); !ok || why != "" {
			return why, ok
		}
	}
	scopes, err := d.o.Scopes(ctx, caller)
	if err != nil {
		d.o.Logger.Printf("wake: re-reading the scopes of pid %d: %+v", e.pid, err)
		return "", false
	}
	if !sameSet(scopes, e.scopes) {
		d.mu.Lock()
		if d.entries[e.pid] == e {
			d.unindex(e)
			e.scopes = scopes
			d.reindex(e)
		}
		d.mu.Unlock()
		d.o.Logger.Printf("wake: the scopes of pid %d changed to %v", e.pid, scopes)
	}
	return "", true
}

// recheckAgent is restore's agent check for a worker under a name that is not its bearer's own
// token label: "" while CheckWakeAgent passes it, restore's reason when it is another agent's
// label now. A master, an id: agent and a worker under its bearer's own label read nothing: the
// mint refuses a label another identity holds, so the own label needs no token read.
func (d *Directory) recheckAgent(ctx context.Context, e *entry, caller domain.Caller) (why string, ok bool) {
	if e.label() == "" || (caller.Labelled && caller.Agent == e.agent) {
		return "", true
	}
	if err := d.o.Sessions.CheckWakeAgent(ctx, caller, e.agent); err != nil {
		if be, isBD := domain.AsBatonDeckError(err); isBD && be.Code == domain.CodeForbidden {
			return fmt.Sprintf("its agent %q is another agent's label", e.agent), true
		}
		d.o.Logger.Printf("wake: re-reading the label of pid %d's agent: %+v", e.pid, err)
		return "", false
	}
	return "", true
}

// sweep rechecks every entry: every Sweep, on a membership change (rescopes) and after the tap
// dropped changes. It bumps gen first, so a Register that read its scopes before and puts its entry
// after the listing has the loop recheck that entry (P-229). Its recheck reads no token row
// (R3.5: 3 statements an entry).
func (d *Directory) sweep(ctx context.Context) {
	d.gen.Add(1)
	d.mu.Lock()
	all := slices.Collect(maps.Values(d.entries))
	d.mu.Unlock()
	for _, e := range all {
		d.sweepOne(ctx, e)
	}
}

// sweepOne rechecks e, dropping it when it no longer holds.
func (d *Directory) sweepOne(ctx context.Context, e *entry) {
	if why, ok := d.recheck(ctx, e, false); ok && why != "" {
		d.drop(ctx, e, why)
	}
}

// label is the agent label a worker entry registered under (its bearer's token label, or the
// --agent it registered with), which its assigned doorbell names (Frame.LineFor); "" for a master
// or an entry under its identity's derived id.
func (e *entry) label() string {
	if e.role != service.RoleWorker || strings.HasPrefix(e.agent, "id:") {
		return ""
	}
	return e.agent
}

// deliver writes one released frame to its entry's socket, after the recheck: a revoked bearer, a
// worker's name another identity has since minted (P-344), a gone registry entry, a reused pid or a
// socket nobody answers drops the entry instead. The cursor
// is the lower of the frame's and the bus's frontier (P-108), and is recorded once written.
// ponytail: one socket write inline in the loop (bounded by WriteTimeout per step); a device has a
// handful of entries. Upgrade path: a delivery goroutine per entry.
func (d *Directory) deliver(ctx context.Context, r released) {
	d.mu.Lock()
	e := d.entries[r.pid]
	d.mu.Unlock()
	if e == nil || e.adapter == "" {
		return
	}
	why, ok := d.recheck(ctx, e, true)
	if !ok {
		return
	}
	if why != "" {
		d.drop(ctx, e, why)
		return
	}
	if !slices.Contains(e.scopes, service.BroadcastScope(r.frame.ProjectID)) {
		return // the recheck found it no longer a member of the frame's project
	}
	reg, err := readRegistry(d.o.Registry, e.pid)
	switch {
	case err != nil:
		d.drop(ctx, e, "its registry entry is gone: "+err.Error())
		return
	case reg.ProcStart != e.procStart:
		d.drop(ctx, e, "its pid now names another process")
		return
	}
	f := r.frame
	if fr := d.o.Bus.Frontier(); fr > 0 && fr < f.Cursor {
		f.Cursor = fr
	}
	token := peerToken(d.o.Registry, e.pid, reg.MessagingSocketPath)
	if token == "" {
		token = e.socketToken
	}
	if err := writeDoorbell(reg.MessagingSocketPath, frameBytes(reg.SessionID, token, f, e.label()), d.o.WriteTimeout); err != nil {
		if gone(err) {
			d.drop(ctx, e, "its socket is gone: "+err.Error())
			return
		}
		d.o.Logger.Printf("wake: writing pid %d's doorbell: %v", e.pid, err)
		return
	}
	d.writes.Add(1)
	d.o.Logger.Printf("wake: rang pid %d (%s) %s cursor %d on %s", e.pid, e.role, f.Kind, f.Cursor, f.ProjectID)
	if f.Cursor > e.lastCursor {
		d.mu.Lock()
		e.lastCursor = f.Cursor
		d.signal() // the presence frames carry the cursor (R2.5): a delivery is a change to show
		d.mu.Unlock()
		if err := d.o.Sessions.SetWakeSessionCursor(ctx, e.id, f.Cursor); err != nil {
			d.o.Logger.Printf("wake: recording pid %d's cursor: %+v", e.pid, err)
		}
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}
