package memstore

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The runs area: a run row is kept beside its run task and leaves with it (DeleteTask), as the
// engines' ON DELETE CASCADE drops it.

func (s *Store) InsertRun(ctx context.Context, r store.Run) error {
	if err := store.CheckRun(r); err != nil {
		return err
	}
	return s.do(ctx, func(st *state) error {
		if _, err := st.task(r.ProjectID, r.RunTaskID); err != nil {
			return err
		}
		k := [2]string{r.ProjectID, r.RunTaskID}
		if _, dup := st.runs[k]; dup {
			return domain.Validation("run "+r.RunTaskID+" is already recorded", map[string]any{"runTaskId": r.RunTaskID})
		}
		st.runs[k] = r
		return nil
	})
}

func (s *Store) ListRuns(ctx context.Context, projectID, parentTaskID string) ([]store.Run, error) {
	return get(ctx, s, func(st *state) ([]store.Run, error) {
		out := []store.Run{}
		for k, r := range st.runs {
			if k[0] == projectID && r.ParentTaskID == parentTaskID {
				out = append(out, r)
			}
		}
		slices.SortFunc(out, func(a, b store.Run) int {
			return cmp.Or(cmp.Compare(a.Ordinal, b.Ordinal), strings.Compare(a.RunTaskID, b.RunTaskID))
		})
		if len(out) > 200 {
			out = out[:200]
		}
		return out, nil
	})
}
