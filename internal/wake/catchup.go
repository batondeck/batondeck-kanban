package wake

import "context"

// replayPage is how many changes one replay reads; a full page is followed by the next.
const replayPage = 1000

// catchUp hands every row replay returns after since to emit, oldest first, page by page until a
// page comes back short (#27's catch-up, over any of the bus's replays). It returns the last id read.
func catchUp(ctx context.Context, replay func(ctx context.Context, since int64, limit int) ([]row, error), since int64, emit func(row)) (int64, error) {
	for {
		rows, err := replay(ctx, since, replayPage)
		if err != nil {
			return since, err
		}
		for _, r := range rows {
			since = r.ID
			emit(r)
		}
		if len(rows) < replayPage {
			return since, nil
		}
	}
}
