package memstore

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The context-item, summary and subtask areas of contract_{context,summaries,subtasks}.go.

var contextKinds = map[store.ContextKind]bool{store.ContextNote: true, store.ContextDecision: true, store.ContextField: true, store.ContextLink: true}

func (s *Store) AddContextItem(ctx context.Context, in store.AddContextItemInput) (store.ContextItem, error) {
	switch {
	case !contextKinds[in.Kind]:
		return store.ContextItem{}, domain.Validation("unknown context item kind: "+string(in.Kind), map[string]any{"kind": in.Kind})
	case in.Body == "":
		return store.ContextItem{}, domain.Validation("a context item needs a body", nil)
	case in.Author == "":
		return store.ContextItem{}, domain.Validation("a context item needs an author", nil)
	}
	return get(ctx, s, func(st *state) (store.ContextItem, error) {
		if _, err := st.task(in.ProjectID, in.TaskID); err != nil {
			return store.ContextItem{}, err
		}
		it := store.ContextItem{ProjectID: in.ProjectID, TaskID: in.TaskID, ID: newID("CI-", 16), Kind: in.Kind, Body: in.Body, Author: in.Author, TS: nowMillis()}
		st.contextItems[[3]string{it.ProjectID, it.TaskID, it.ID}] = it
		return it, nil
	})
}

// byTSAndID is (ts, id) ascending, ids as bytes.
func byTSAndID(ats, bts int64, aid, bid string) int {
	return cmp.Or(cmp.Compare(ats, bts), strings.Compare(aid, bid))
}

func (s *Store) ListContextItems(ctx context.Context, projectID, taskID string, q store.ContextItemsQuery) (store.ContextItemsPage, error) {
	var afterTS int64
	var afterID string
	if q.Cursor != "" {
		order, id, err := store.DecodeCursor(q.Cursor)
		if err != nil {
			return store.ContextItemsPage{}, err
		}
		ts, perr := strconv.ParseInt(order, 10, 64)
		if perr != nil || id == "" {
			return store.ContextItemsPage{}, domain.Validation("Invalid cursor", nil)
		}
		afterTS, afterID = ts, id
	}
	limit := clamp(q.Limit, 50, 200)
	return get(ctx, s, func(st *state) (store.ContextItemsPage, error) {
		if _, err := st.task(projectID, taskID); err != nil {
			return store.ContextItemsPage{}, err
		}
		var items []store.ContextItem
		for k, it := range st.contextItems {
			if k[0] == projectID && k[1] == taskID {
				items = append(items, it)
			}
		}
		dir := -1
		if q.Ascending {
			dir = 1
		}
		slices.SortFunc(items, func(a, b store.ContextItem) int { return dir * byTSAndID(a.TS, b.TS, a.ID, b.ID) })
		page := store.ContextItemsPage{Items: []store.ContextItem{}}
		for _, it := range items {
			if afterID != "" && dir*byTSAndID(it.TS, afterTS, it.ID, afterID) <= 0 {
				continue
			}
			if len(page.Items) == limit {
				last := page.Items[limit-1]
				page.NextCursor = store.EncodeCursor(strconv.FormatInt(last.TS, 10), last.ID)
				break
			}
			page.Items = append(page.Items, it)
		}
		return page, nil
	})
}

func (s *Store) DeleteContextItemsByTask(ctx context.Context, projectID, taskID string) (int64, error) {
	return get(ctx, s, func(st *state) (int64, error) {
		var n int64
		for k := range st.contextItems {
			if k[0] == projectID && k[1] == taskID {
				delete(st.contextItems, k)
				n++
			}
		}
		return n, nil
	})
}

func (s *Store) SetSummary(ctx context.Context, projectID, taskID, body, updatedBy string, expectedVersion int64) (store.Summary, error) {
	return get(ctx, s, func(st *state) (store.Summary, error) {
		if _, err := st.taskAt(projectID, taskID, expectedVersion); err != nil {
			return store.Summary{}, err
		}
		sum := store.Summary{ProjectID: projectID, TaskID: taskID, Body: body, UpdatedBy: updatedBy, UpdatedAt: nowMillis()}
		st.summaries[[2]string{projectID, taskID}] = sum
		return sum, nil
	})
}

func (s *Store) GetSummary(ctx context.Context, projectID, taskID string) (store.Summary, error) {
	return get(ctx, s, func(st *state) (store.Summary, error) {
		if _, err := st.task(projectID, taskID); err != nil {
			return store.Summary{}, err
		}
		sum, ok := st.summaries[[2]string{projectID, taskID}]
		if !ok {
			sum = store.Summary{ProjectID: projectID, TaskID: taskID}
		}
		return sum, nil
	})
}

func (s *Store) ListSubtasks(ctx context.Context, projectID, parentTaskID string) ([]string, error) {
	return get(ctx, s, func(st *state) ([]string, error) {
		if _, err := st.task(projectID, parentTaskID); err != nil {
			return nil, err
		}
		out := []string{}
		for k, t := range st.tasks {
			if k[0] == projectID && t.ParentTaskID != nil && *t.ParentTaskID == parentTaskID {
				out = append(out, t.ID)
			}
		}
		slices.SortFunc(out, func(a, b string) int { return cmp.Compare(taskNumber(a), taskNumber(b)) })
		return out, nil
	})
}

func taskNumber(id string) int64 {
	n, _ := strconv.ParseInt(strings.TrimPrefix(id, "T-"), 10, 64)
	return n
}
