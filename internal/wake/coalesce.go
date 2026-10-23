package wake

import (
	"slices"
	"time"
)

// coalescer holds the live doorbells of one stream, one frame per kind: a doorbell of a kind
// already held replaces the frame when its cursor is higher, and the frame is released once its
// kind has been quiet for quiet, or maxHold after the first one, whichever is sooner. The quiet
// period outlasts the bus's read interval, so a burst the bus splits across two reads is still one
// frame carrying its latest cursor. It is used from one goroutine.
type coalescer struct {
	quiet, maxHold time.Duration
	held           map[string]Frame
	first          map[string]time.Time
	due            map[string]time.Time
	timer          *time.Timer
	pending        bool // timer is armed
}

func newCoalescer(quiet, maxHold time.Duration) *coalescer {
	t := time.NewTimer(time.Hour)
	t.Stop()
	return &coalescer{quiet: quiet, maxHold: maxHold, held: map[string]Frame{}, first: map[string]time.Time{}, due: map[string]time.Time{}, timer: t}
}

// add holds f, pushing its kind's release to quiet from now, within maxHold of the first.
func (c *coalescer) add(f Frame, now time.Time) {
	old, ok := c.held[f.Kind]
	if !ok {
		c.first[f.Kind] = now
	}
	if !ok || f.Cursor > old.Cursor {
		c.held[f.Kind] = f
	}
	c.due[f.Kind] = minTime(now.Add(c.quiet), c.first[f.Kind].Add(c.maxHold))
	c.arm(now)
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
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
func (c *coalescer) release(now time.Time) []Frame {
	c.pending = false
	var out []Frame
	for k, at := range c.due {
		if !at.After(now) {
			out = append(out, c.held[k])
			delete(c.held, k)
			delete(c.due, k)
			delete(c.first, k)
		}
	}
	slices.SortFunc(out, func(a, b Frame) int { return int(a.Cursor - b.Cursor) })
	c.arm(now)
	return out
}

// arm sets the timer to the earliest window end.
func (c *coalescer) arm(now time.Time) {
	var next time.Time
	for _, at := range c.due {
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}
	if next.IsZero() {
		return
	}
	if c.pending && !c.timer.Stop() {
		select {
		case <-c.timer.C:
		default:
		}
	}
	c.timer.Reset(max(next.Sub(now), 0))
	c.pending = true
}
