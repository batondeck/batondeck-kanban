package wake

import (
	"slices"
	"strconv"
	"time"
)

// coalescer holds the live doorbells of every entry, one per key (an entry and a kind): a doorbell
// of a key already held keeps the higher cursor, and the key is released once it has been quiet for
// its quiet period, or maxHold after its first doorbell, whichever is sooner. It is used from one
// goroutine (#27's coalescer, keyed and windowed per key for the directory).
type coalescer struct {
	held    map[string]held
	timer   *time.Timer
	pending bool // timer is armed
}

type held struct {
	pid   int64
	frame Frame
	first time.Time
	due   time.Time
}

// released is one frame due for its entry.
type released struct {
	pid   int64
	frame Frame
}

func newCoalescer() *coalescer {
	t := time.NewTimer(time.Hour)
	t.Stop()
	return &coalescer{held: map[string]held{}, timer: t}
}

// window is a kind's quiet period and cap.
func window(kind string) (quiet, maxHold time.Duration) {
	if kind == KindAssigned {
		return AssignedQuiet, AssignedMaxHold
	}
	return SlowWindow, SlowWindow
}

// add holds f for entry pid, pushing its release to quiet from now, within maxHold of the first.
func (c *coalescer) add(pid int64, f Frame, now time.Time, quiet, maxHold time.Duration) {
	key := keyOf(pid, f.Kind)
	h, ok := c.held[key]
	if !ok {
		h = held{pid: pid, frame: f, first: now}
	} else if f.Cursor > h.frame.Cursor {
		h.frame = f
	}
	h.due = minTime(now.Add(quiet), h.first.Add(maxHold))
	c.held[key] = h
	c.arm(now)
}

func keyOf(pid int64, kind string) string { return kind + "/" + strconv.FormatInt(pid, 10) }

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// forget drops every frame held for pid.
func (c *coalescer) forget(pid int64) {
	for k, h := range c.held {
		if h.pid == pid {
			delete(c.held, k)
		}
	}
}

// empty reports whether no frame is held.
func (c *coalescer) empty() bool { return len(c.held) == 0 }

// C fires when a held frame is due; nil while nothing is held.
func (c *coalescer) C() <-chan time.Time {
	if !c.pending {
		return nil
	}
	return c.timer.C
}

// release is the frames due by now, lowest cursor first; call it when C fires.
func (c *coalescer) release(now time.Time) []released {
	c.pending = false
	var out []released
	for k, h := range c.held {
		if !h.due.After(now) {
			out = append(out, released{pid: h.pid, frame: h.frame})
			delete(c.held, k)
		}
	}
	slices.SortFunc(out, func(a, b released) int { return int(a.frame.Cursor - b.frame.Cursor) })
	c.arm(now)
	return out
}

// arm sets the timer to the earliest due.
func (c *coalescer) arm(now time.Time) {
	var next time.Time
	for _, h := range c.held {
		if next.IsZero() || h.due.Before(next) {
			next = h.due
		}
	}
	if c.pending && !c.timer.Stop() {
		select {
		case <-c.timer.C:
		default:
		}
	}
	c.pending = false
	if next.IsZero() {
		return
	}
	c.timer.Reset(max(next.Sub(now), 0))
	c.pending = true
}
