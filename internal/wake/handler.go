package wake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/bus"
	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Buffer is a stream's bus subscription buffer. A stream whose subscription drops a change ends
// with ": dropped", naming the cursor to reconnect with when it knows one, so that the client
// replays what it missed.
const Buffer = 256

// Options builds the /wake handler.
type Options struct {
	Bus      *bus.Bus
	Resolver auth.Resolver
	// Scopes is the token scopes a caller's stream subscribes to (service.Core.WakeScopes), read
	// once per connection.
	Scopes func(ctx context.Context, caller domain.Caller) ([]string, error)
	Logger *log.Logger
	// Keepalive, Quiet, MaxHold, Buffer and Recheck default to the package constants; tests change
	// them.
	Keepalive time.Duration
	Quiet     time.Duration
	MaxHold   time.Duration
	Buffer    int
	Recheck   time.Duration
}

// Handler is GET /wake[?since=<cursor>]. The bearer is required; since, when given, is a
// non-negative integer cursor (400 otherwise) and every doorbell after it is replayed first.
func Handler(o Options) http.Handler {
	if o.Keepalive <= 0 {
		o.Keepalive = Keepalive
	}
	if o.Quiet <= 0 {
		o.Quiet = Quiet
	}
	if o.MaxHold <= 0 {
		o.MaxHold = MaxHold
	}
	if o.Buffer <= 0 {
		o.Buffer = Buffer
	}
	if o.Recheck <= 0 {
		o.Recheck = Recheck
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { o.serve(w, r) })
}

func (o Options) serve(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.Bearer(o.Resolver, o.Logger, w, r)
	if !ok {
		return
	}
	token, _ := auth.BearerToken(r)
	var since int64
	raw, replay := r.URL.Query()["since"]
	if replay {
		n, err := strconv.ParseInt(raw[0], 10, 64)
		if len(raw) != 1 || err != nil || n < 0 {
			auth.WriteError(w, http.StatusBadRequest, domain.CodeValidation, "since must be one non-negative integer cursor")
			return
		}
		since = n
	}
	ctx := r.Context()
	scopes, err := o.Scopes(ctx, caller)
	if err != nil {
		o.Logger.Printf("wake: the scopes of %s: %+v", caller.Identity, err)
		auth.WriteError(w, http.StatusInternalServerError, domain.CodeInternal, "reading the caller's scopes failed")
		return
	}
	// Subscribe before replaying, so that nothing committed meanwhile falls between the two.
	sub, err := o.Bus.Subscribe(ctx, scopes, o.Buffer)
	if err != nil {
		if errors.Is(err, bus.ErrClosed) {
			auth.WriteError(w, http.StatusServiceUnavailable, domain.CodeInternal, "the server is stopping")
			return
		}
		if ctx.Err() == nil {
			o.Logger.Printf("wake: subscribing %s: %+v", caller.Identity, err)
			auth.WriteError(w, http.StatusInternalServerError, domain.CodeInternal, "subscribing to the doorbell failed")
		}
		return
	}
	defer sub.Close()

	s := &stream{w: w, rc: http.NewResponseController(w)}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if s.comment("ok") != nil {
		return
	}
	var seen map[int64]struct{}
	if replay {
		if seen, err = catchUp(ctx, o.Bus, scopes, since, s.frame); err != nil {
			if ctx.Err() == nil {
				o.Logger.Printf("wake: replaying %s from %d: %+v", caller.Identity, since, err)
			}
			return
		}
	}
	co := newCoalescer(o.Quiet, o.MaxHold)
	keep := time.NewTicker(o.Keepalive)
	defer keep.Stop()
	recheck := time.NewTicker(o.Recheck)
	defer recheck.Stop()
	// safe is a cursor every doorbell at or below which this stream has written (or coalesced
	// away): the replay point it names when it ends behind. -1 until it knows one.
	safe, recv := int64(-1), int64(0)
	if replay {
		safe = since
	}
	// end writes the last comment, naming the cursor to reconnect with when there is one.
	end := func(reason string) {
		if safe < 0 {
			_ = s.comment(reason + ", reconnect")
		} else {
			_ = s.comment(fmt.Sprintf("%s, reconnect with since=%d", reason, safe))
		}
	}
	for {
		if sub.Dropped() > 0 {
			o.Logger.Printf("wake: the stream of %s dropped %d change(s); ending it, safe cursor %d", caller.Identity, sub.Dropped(), safe)
			end("dropped")
			return
		}
		if co.empty() && recv > safe {
			safe = recv
		}
		select {
		case <-ctx.Done():
			return
		case c, open := <-sub.C():
			if !open {
				return // the bus closed: the server is stopping
			}
			recv = max(recv, c.ID)
			if _, dup := seen[c.ID]; dup {
				continue
			}
			if f, ok := frameOf(c.ID, c.Kind, c.Payload); ok {
				co.add(f, time.Now())
			}
		case <-co.C():
			for _, f := range co.release(time.Now()) {
				if s.frame(f) != nil {
					return
				}
			}
		case <-keep.C:
			if s.comment("keepalive") != nil {
				return
			}
		case <-recheck.C:
			switch why := o.recheck(ctx, token, scopes); why {
			case "":
			case "reauthenticate":
				_ = s.comment(why)
				return
			default:
				end(why)
				return
			}
		}
	}
}

// recheck re-resolves the stream's bearer and re-reads its scopes: "" while both hold, "reauthenticate"
// when the bearer no longer resolves (revoked, expired, its user disabled), "scopes changed" when the
// memberships moved, and "recheck failed" when either read failed (the stream ends rather than
// trusting what it can no longer confirm).
func (o Options) recheck(ctx context.Context, token string, scopes []string) string {
	caller, err := o.Resolver.ResolveCaller(ctx, token)
	if err != nil {
		if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeUnauthenticated {
			return "reauthenticate"
		}
		if ctx.Err() == nil {
			o.Logger.Printf("wake: re-resolving a stream's bearer: %+v", err)
		}
		return "recheck failed"
	}
	now, err := o.Scopes(ctx, caller)
	if err != nil {
		if ctx.Err() == nil {
			o.Logger.Printf("wake: re-reading the scopes of %s: %+v", caller.Identity, err)
		}
		return "recheck failed"
	}
	if !sameSet(now, scopes) {
		return "scopes changed"
	}
	return ""
}

func sameSet(a, b []string) bool {
	in := func(x string, set []string) bool { return slices.Contains(set, x) }
	for _, x := range a {
		if !in(x, b) {
			return false
		}
	}
	for _, x := range b {
		if !in(x, a) {
			return false
		}
	}
	return true
}

// stream writes SSE events and flushes each one.
type stream struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func (s *stream) frame(f Frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "id: %d\ndata: %s\n\n", f.Cursor, b); err != nil {
		return err
	}
	return s.rc.Flush()
}

func (s *stream) comment(text string) error {
	if _, err := fmt.Fprintf(s.w, ": %s\n\n", text); err != nil {
		return err
	}
	return s.rc.Flush()
}
