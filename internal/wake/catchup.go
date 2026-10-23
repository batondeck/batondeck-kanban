package wake

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/bus"
)

// replayPage is how many changes one bus.Replay reads; a full page is followed by the next.
const replayPage = 1000

// catchUp emits, oldest first and uncoalesced, every doorbell of scopes after since, read from the
// change log through bus.Replay page by page until a page comes back short. It returns every id it
// read, so that the live stream (subscribed before it) passes over the ones already emitted.
func catchUp(ctx context.Context, b *bus.Bus, scopes []string, since int64, emit func(Frame) error) (map[int64]struct{}, error) {
	seen := map[int64]struct{}{}
	for {
		rows, err := b.Replay(ctx, scopes, since, replayPage)
		if err != nil {
			return seen, err
		}
		for _, c := range rows {
			seen[c.ID] = struct{}{}
			since = c.ID
			if f, ok := frameOf(c.ID, c.Kind, c.Payload); ok {
				if err := emit(f); err != nil {
					return seen, err
				}
			}
		}
		if len(rows) < replayPage {
			return seen, nil
		}
	}
}
