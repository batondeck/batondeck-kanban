package service

import (
	"context"
	"unicode/utf8"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// search_tasks's bounds, conductor's (src/tools/media.ts): k defaults to 10 and is at most 50, the
// query at most 1000 characters.
const (
	searchDefaultK = 10
	searchMaxK     = 50
	searchMaxQuery = 1000
)

// SearchTasks is search_tasks, keyword only: the project's tasks whose title, description or search
// tokens hold every term of the query, best match first, at most k, each with the engine's rank as
// its score. Two statements: the page (store.Search) and its tasks (ListTasksByIDs). mode semantic
// is VALIDATION (the plan drops embeddings); hybrid and keyword run keyword and say so, as
// conductor's did with no embedder.
func (m media) SearchTasks(ctx context.Context, caller domain.Caller, req SearchTasksReq) (SearchTasksResp, error) {
	c := m.c
	if _, err := c.authorize(ctx, caller, req.ProjectID, "search_tasks", domain.GrantSubject{}, false); err != nil {
		return SearchTasksResp{}, err
	}
	if err := required("query", req.Query); err != nil {
		return SearchTasksResp{}, err
	}
	switch {
	case utf8.RuneCountInString(req.Query) > searchMaxQuery:
		return SearchTasksResp{}, domain.Validation("query is at most 1000 characters", map[string]any{"field": "query"})
	case req.Mode == "semantic":
		return SearchTasksResp{}, domain.Validation("mode semantic is not available: this server searches by keyword only "+
			"(SQLite FTS5 or Postgres tsvector) and keeps no embeddings; use mode keyword", map[string]any{"field": "mode"})
	case req.Mode != "" && req.Mode != "keyword" && req.Mode != "hybrid":
		return SearchTasksResp{}, domain.Validation("unknown mode: "+string(req.Mode), map[string]any{"field": "mode"})
	case req.K < 0 || req.K > searchMaxK:
		return SearchTasksResp{}, domain.Validation("k is 1 to 50", map[string]any{"field": "k"})
	}
	k := int(req.K)
	if k == 0 {
		k = searchDefaultK
	}
	page, err := c.store.SearchTasks(ctx, req.ProjectID, req.Query, store.SearchFilter{BoardID: req.BoardID}, "", k)
	if err != nil {
		return SearchTasksResp{}, c.mapErr("search_tasks", err)
	}
	out := SearchTasksResp{Mode: "keyword", Results: make([]SearchResult, 0, len(page.Hits))}
	if len(page.Hits) == 0 {
		return out, nil
	}
	ids := make([]string, len(page.Hits))
	for i, h := range page.Hits {
		ids[i] = h.TaskID
	}
	tasks, err := c.store.ListTasksByIDs(ctx, req.ProjectID, ids)
	if err != nil {
		return SearchTasksResp{}, c.mapErr("search_tasks", err)
	}
	byID := make(map[string]store.Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	for _, h := range page.Hits {
		t, ok := byID[h.TaskID]
		if !ok { // deleted between the two statements
			continue
		}
		dto, err := taskOut(t, "")
		if err != nil {
			return SearchTasksResp{}, c.mapErr("search_tasks", err)
		}
		out.Results = append(out.Results, SearchResult{Score: h.Rank, Task: dto})
	}
	return out, nil
}
