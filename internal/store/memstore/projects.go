package memstore

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The areas of R0.8's contract files: projects, members, boards and columns, the task counter and
// idempotency keys.

func cloneProject(p store.Project) store.Project {
	p.Settings.RetryPolicy = clonePtr(p.Settings.RetryPolicy)
	other := make(map[string]json.RawMessage, len(p.Settings.Other))
	for k, v := range p.Settings.Other {
		other[k] = cloneBytes(v)
	}
	p.Settings.Other = other
	return p
}

func cloneBoard(b store.Board) store.Board {
	cols := make([]store.Column, len(b.Columns))
	for i, c := range b.Columns {
		c.WIPLimit = clonePtr(c.WIPLimit)
		cols[i] = c
	}
	b.Columns = cols
	return b
}

// sortedBoard is the board as the contract returns it: a copy, its columns by (order, id).
func sortedBoard(b store.Board) store.Board {
	out := cloneBoard(b)
	slices.SortStableFunc(out.Columns, func(a, b store.Column) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.ID, b.ID))
	})
	return out
}

func (s *Store) CreateProject(ctx context.Context, in store.CreateProjectInput, opts store.WriteOptions) (store.Project, error) {
	if strings.TrimSpace(in.Name) == "" || in.CreatedBy == "" {
		return store.Project{}, domain.Validation("a project needs a name and a creator", nil)
	}
	return idempotent(ctx, s, store.GlobalScope, opts, func(st *state) (store.Project, error) {
		p := store.Project{
			ID: newID("P-", 12), Name: in.Name, CreatedBy: in.CreatedBy, CreatedAt: nowMillis(),
			Settings: store.ProjectSettings{ReviewEnabled: true, Other: map[string]json.RawMessage{
				"quotaEnabled": json.RawMessage(`false`), "txnCap": json.RawMessage(`null`),
			}},
		}
		st.projects[p.ID] = cloneProject(p)
		st.members[[2]string{p.ID, in.CreatedBy}] = store.Member{
			ProjectID: p.ID, IdentityID: in.CreatedBy, Role: domain.RoleAdmin, AddedBy: in.CreatedBy, AddedAt: p.CreatedAt,
		}
		return p, nil
	})
}

func (s *Store) GetProject(ctx context.Context, projectID string) (store.Project, error) {
	return get(ctx, s, func(st *state) (store.Project, error) {
		p, ok := st.projects[projectID]
		if !ok {
			return store.Project{}, domain.NotFound("Project", projectID)
		}
		return cloneProject(p), nil
	})
}

func (s *Store) ListProjects(ctx context.Context, identityID string) ([]store.ProjectMembership, error) {
	return get(ctx, s, func(st *state) ([]store.ProjectMembership, error) {
		out := []store.ProjectMembership{}
		for _, m := range st.members {
			if m.IdentityID == identityID {
				out = append(out, store.ProjectMembership{Project: cloneProject(st.projects[m.ProjectID]), Role: m.Role})
			}
		}
		slices.SortFunc(out, func(a, b store.ProjectMembership) int {
			return cmp.Or(cmp.Compare(a.Project.CreatedAt, b.Project.CreatedAt), cmp.Compare(a.Project.ID, b.Project.ID))
		})
		return out, nil
	})
}

func (s *Store) UpdateSettings(ctx context.Context, projectID string, patch store.SettingsPatch) (store.Project, error) {
	if _, ok := patch["transitionTable"]; ok {
		return store.Project{}, domain.Validation("the transition table is fixed: settings take no transitionTable", map[string]any{"key": "transitionTable"})
	}
	return get(ctx, s, func(st *state) (store.Project, error) {
		cur, ok := st.projects[projectID]
		if !ok {
			return store.Project{}, domain.NotFound("Project", projectID)
		}
		p := cloneProject(cur)
		for k, v := range patch {
			isNull := bytes.Equal(bytes.TrimSpace(v), []byte("null"))
			switch k {
			case "reviewEnabled":
				var b bool
				if isNull || json.Unmarshal(v, &b) != nil {
					return store.Project{}, domain.Validation("reviewEnabled must be a boolean", map[string]any{"key": k})
				}
				p.Settings.ReviewEnabled = b
			case "retryPolicy":
				if isNull {
					p.Settings.RetryPolicy = nil
					continue
				}
				var rp domain.RetryPolicy
				d := json.NewDecoder(bytes.NewReader(v))
				d.DisallowUnknownFields()
				if err := d.Decode(&rp); err != nil {
					return store.Project{}, domain.Validation("retryPolicy must be a retry policy object", map[string]any{"key": k})
				}
				p.Settings.RetryPolicy = &rp
			default:
				if isNull {
					delete(p.Settings.Other, k)
					continue
				}
				// Compact, as the engines' re-marshalled settings column reads back.
				var compact bytes.Buffer
				if json.Compact(&compact, v) != nil {
					return store.Project{}, domain.Validation("a setting is not valid JSON", nil)
				}
				p.Settings.Other[k] = compact.Bytes()
			}
		}
		st.projects[projectID] = cloneProject(p)
		return p, nil
	})
}

func (s *Store) DeleteProjectRecursive(ctx context.Context, projectID string) error {
	return s.do(ctx, func(st *state) error {
		if _, ok := st.projects[projectID]; !ok {
			return domain.NotFound("Project", projectID)
		}
		delete(st.projects, projectID)
		delete(st.counters, projectID)
		maps.DeleteFunc(st.members, func(k [2]string, _ store.Member) bool { return k[0] == projectID })
		maps.DeleteFunc(st.boards, func(k [2]string, _ store.Board) bool { return k[0] == projectID })
		maps.DeleteFunc(st.idem, func(k [3]string, _ idemEntry) bool { return k[0] == projectID })
		maps.DeleteFunc(st.tasks, func(k [2]string, _ store.Task) bool { return k[0] == projectID })
		maps.DeleteFunc(st.events, func(k [2]string, _ store.Event) bool { return k[0] == projectID })
		maps.DeleteFunc(st.contextItems, func(k [3]string, _ store.ContextItem) bool { return k[0] == projectID })
		maps.DeleteFunc(st.artifacts, func(k [3]string, _ store.Artifact) bool { return k[0] == projectID })
		maps.DeleteFunc(st.comments, func(k [3]string, _ store.Comment) bool { return k[0] == projectID })
		maps.DeleteFunc(st.followUps, func(k [3]string, _ store.FollowUp) bool { return k[0] == projectID })
		maps.DeleteFunc(st.memory, func(_ string, e store.MemoryEntry) bool { return e.ProjectID == projectID })
		maps.DeleteFunc(st.summaries, func(k [2]string, _ store.Summary) bool { return k[0] == projectID })
		maps.DeleteFunc(st.edges, func(k [2]string, _ store.DependencyEdge) bool { return k[0] == projectID })
		maps.DeleteFunc(st.attachments, func(k [3]string, _ store.Attachment) bool { return k[0] == projectID })
		maps.DeleteFunc(st.chroniclePages, func(k [2]string, _ store.ChroniclePage) bool { return k[0] == projectID })
		maps.DeleteFunc(st.chronicleVersions, func(k [3]string, _ store.ChronicleVersion) bool { return k[0] == projectID })
		maps.DeleteFunc(st.chronicleObjects, func(k [2]string, _ []byte) bool { return k[0] == projectID })
		deleteProjectOncall(st, projectID)
		// The change log is the trace of what happened: it is kept, as on the engines.
		return nil
	})
}

func (s *Store) AddMember(ctx context.Context, projectID, identityID string, role domain.Role, addedBy string) (store.Member, error) {
	if _, err := domain.ParseRole(string(role)); err != nil {
		return store.Member{}, err
	}
	if identityID == "" || addedBy == "" {
		return store.Member{}, domain.Validation("a member needs an identity and who added it", nil)
	}
	return get(ctx, s, func(st *state) (store.Member, error) {
		if _, ok := st.projects[projectID]; !ok {
			return store.Member{}, domain.NotFound("Project", projectID)
		}
		k := [2]string{projectID, identityID}
		m, ok := st.members[k]
		if ok {
			m.Role = role
		} else {
			m = store.Member{ProjectID: projectID, IdentityID: identityID, Role: role, AddedBy: addedBy, AddedAt: nowMillis()}
		}
		st.members[k] = m
		return m, nil
	})
}

func (s *Store) RemoveMember(ctx context.Context, projectID, identityID string) error {
	return s.do(ctx, func(st *state) error {
		k := [2]string{projectID, identityID}
		if _, ok := st.members[k]; !ok {
			if _, ok := st.projects[projectID]; !ok {
				return domain.NotFound("Project", projectID)
			}
			return domain.NotFound("Member", identityID)
		}
		delete(st.members, k)
		return nil
	})
}

func (s *Store) ListMembers(ctx context.Context, projectID string) ([]store.Member, error) {
	return get(ctx, s, func(st *state) ([]store.Member, error) {
		if _, ok := st.projects[projectID]; !ok {
			return nil, domain.NotFound("Project", projectID)
		}
		out := []store.Member{}
		for k, m := range st.members {
			if k[0] == projectID {
				out = append(out, m)
			}
		}
		slices.SortFunc(out, func(a, b store.Member) int {
			return cmp.Or(cmp.Compare(a.AddedAt, b.AddedAt), cmp.Compare(a.IdentityID, b.IdentityID))
		})
		return out, nil
	})
}

func (s *Store) GetMember(ctx context.Context, projectID, identityID string) (store.Member, error) {
	return get(ctx, s, func(st *state) (store.Member, error) {
		m, ok := st.members[[2]string{projectID, identityID}]
		if !ok {
			return store.Member{}, domain.NotFound("Member", identityID)
		}
		return m, nil
	})
}

// defaultColumns is the engines' default board: conductor's columns without Cancelled, and without
// Review when the project's reviewEnabled is off.
func defaultColumns(reviewEnabled bool) []store.ColumnInput {
	all := []store.ColumnInput{
		{Name: "To do", Status: domain.Todo},
		{Name: "In progress", Status: domain.InProgress},
		{Name: "Blocked", Status: domain.Blocked},
		{Name: "Review", Status: domain.Review},
		{Name: "Done", Status: domain.Done},
	}
	if reviewEnabled {
		return all
	}
	return slices.DeleteFunc(all, func(c store.ColumnInput) bool { return c.Status == domain.Review })
}

func validColumn(c store.ColumnInput) error {
	if strings.TrimSpace(c.Name) == "" {
		return domain.Validation("a column needs a name", nil)
	}
	if _, err := domain.ParseStatus(string(c.Status)); err != nil {
		return err
	}
	if c.WIPLimit != nil && *c.WIPLimit <= 0 {
		return domain.Validation("a WIP limit must be positive", map[string]any{"wipLimit": *c.WIPLimit})
	}
	return nil
}

func (s *Store) CreateBoard(ctx context.Context, in store.CreateBoardInput, opts store.WriteOptions) (store.Board, error) {
	if strings.TrimSpace(in.Name) == "" || in.CreatedBy == "" {
		return store.Board{}, domain.Validation("a board needs a name and a creator", nil)
	}
	if in.Columns != nil && len(in.Columns) == 0 {
		return store.Board{}, domain.Validation("a board needs at least one column", nil)
	}
	for _, c := range in.Columns {
		if err := validColumn(c); err != nil {
			return store.Board{}, err
		}
	}
	return idempotent(ctx, s, in.ProjectID, opts, func(st *state) (store.Board, error) {
		p, ok := st.projects[in.ProjectID]
		if !ok {
			return store.Board{}, domain.NotFound("Project", in.ProjectID)
		}
		cols := in.Columns
		if cols == nil {
			cols = defaultColumns(p.Settings.ReviewEnabled)
		}
		b := store.Board{ProjectID: in.ProjectID, ID: newID("B-", 12), Name: in.Name, CreatedBy: in.CreatedBy, CreatedAt: nowMillis()}
		for i, c := range cols {
			order := int64(i)
			if c.Order != nil {
				order = *c.Order
			}
			b.Columns = append(b.Columns, store.Column{ID: newID("C-", 10), Name: c.Name, Order: order, Status: c.Status, WIPLimit: clonePtr(c.WIPLimit)})
		}
		st.boards[[2]string{b.ProjectID, b.ID}] = cloneBoard(b)
		return sortedBoard(b), nil
	})
}

// boardOf is the project's board, or NotFound("Board").
func (st *state) boardOf(projectID, boardID string) (store.Board, error) {
	b, ok := st.boards[[2]string{projectID, boardID}]
	if !ok {
		return store.Board{}, domain.NotFound("Board", boardID)
	}
	return b, nil
}

func (s *Store) GetBoard(ctx context.Context, projectID, boardID string) (store.Board, store.TaskCounts, error) {
	var counts store.TaskCounts
	b, err := get(ctx, s, func(st *state) (store.Board, error) {
		b, err := st.boardOf(projectID, boardID)
		if err != nil {
			return store.Board{}, err
		}
		counts = store.TaskCounts{}
		for _, c := range b.Columns {
			counts[c.ID] = 0
		}
		for k, t := range st.tasks {
			if k[0] == projectID && t.BoardID == boardID {
				if _, ok := counts[t.ColumnID]; ok {
					counts[t.ColumnID]++
				}
			}
		}
		return sortedBoard(b), nil
	})
	if err != nil {
		return store.Board{}, nil, err
	}
	return b, counts, nil
}

func (s *Store) ListBoards(ctx context.Context, projectID string) ([]store.Board, error) {
	return get(ctx, s, func(st *state) ([]store.Board, error) {
		if _, ok := st.projects[projectID]; !ok {
			return nil, domain.NotFound("Project", projectID)
		}
		out := []store.Board{}
		for k, b := range st.boards {
			if k[0] == projectID {
				out = append(out, sortedBoard(b))
			}
		}
		slices.SortFunc(out, func(a, b store.Board) int {
			return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ID, b.ID))
		})
		return out, nil
	})
}

func (s *Store) AddColumn(ctx context.Context, projectID, boardID string, in store.ColumnInput, opts store.WriteOptions) (store.Column, error) {
	if err := validColumn(in); err != nil {
		return store.Column{}, err
	}
	return idempotent(ctx, s, projectID, opts, func(st *state) (store.Column, error) {
		b, err := st.boardOf(projectID, boardID)
		if err != nil {
			return store.Column{}, err
		}
		order := int64(0)
		if in.Order != nil {
			order = *in.Order
		} else if len(b.Columns) > 0 {
			order = slices.MaxFunc(b.Columns, func(a, b store.Column) int { return cmp.Compare(a.Order, b.Order) }).Order + 1
		}
		c := store.Column{ID: newID("C-", 10), Name: in.Name, Order: order, Status: in.Status, WIPLimit: clonePtr(in.WIPLimit)}
		b.Columns = append(b.Columns, c)
		st.boards[[2]string{projectID, boardID}] = cloneBoard(b)
		return c, nil
	})
}

func (s *Store) UpdateColumn(ctx context.Context, projectID, boardID, columnID string, patch store.ColumnPatch) (store.Board, error) {
	return get(ctx, s, func(st *state) (store.Board, error) {
		cur, err := st.boardOf(projectID, boardID)
		if err != nil {
			return store.Board{}, err
		}
		b := cloneBoard(cur)
		i := slices.IndexFunc(b.Columns, func(c store.Column) bool { return c.ID == columnID })
		if i < 0 {
			return store.Board{}, domain.NotFound("Column", columnID)
		}
		c := b.Columns[i]
		next := store.ColumnInput{Name: c.Name, Status: c.Status, WIPLimit: c.WIPLimit, Order: &c.Order}
		if patch.Name != nil {
			next.Name = *patch.Name
		}
		if patch.Status != nil {
			next.Status = *patch.Status
		}
		if patch.SetWIPLimit {
			next.WIPLimit = patch.WIPLimit
		}
		if patch.Order != nil {
			next.Order = patch.Order
		}
		if err := validColumn(next); err != nil {
			return store.Board{}, err
		}
		b.Columns[i] = store.Column{ID: c.ID, Name: next.Name, Order: *next.Order, Status: next.Status, WIPLimit: clonePtr(next.WIPLimit)}
		st.boards[[2]string{projectID, boardID}] = cloneBoard(b)
		return sortedBoard(b), nil
	})
}

func (s *Store) RemoveColumn(ctx context.Context, projectID, boardID, columnID string) (store.Board, error) {
	return get(ctx, s, func(st *state) (store.Board, error) {
		cur, err := st.boardOf(projectID, boardID)
		if err != nil {
			return store.Board{}, err
		}
		b := cloneBoard(cur)
		i := slices.IndexFunc(b.Columns, func(c store.Column) bool { return c.ID == columnID })
		if i < 0 {
			return store.Board{}, domain.NotFound("Column", columnID)
		}
		n := 0
		for k, t := range st.tasks {
			if k[0] == projectID && t.BoardID == boardID && t.ColumnID == columnID {
				n++
			}
		}
		if n > 0 {
			return store.Board{}, domain.Validation("Cannot remove a column that still has tasks — move them first", map[string]any{"columnId": columnID, "tasks": int64(n)})
		}
		if len(b.Columns) <= 1 {
			return store.Board{}, domain.Validation("Cannot remove the last column", map[string]any{"columnId": columnID})
		}
		b.Columns = slices.Delete(b.Columns, i, i+1)
		st.boards[[2]string{projectID, boardID}] = cloneBoard(b)
		return sortedBoard(b), nil
	})
}

func (s *Store) NextTaskID(ctx context.Context, projectID string) (string, error) {
	return get(ctx, s, func(st *state) (string, error) { return st.nextTaskID(projectID) })
}

func (st *state) nextTaskID(projectID string) (string, error) {
	if _, ok := st.projects[projectID]; !ok {
		return "", domain.NotFound("Project", projectID)
	}
	st.counters[projectID]++
	return "T-" + strconv.FormatInt(st.counters[projectID], 10), nil
}

func (s *Store) WithIdempotency(ctx context.Context, projectID, identity, key string, ttl time.Duration, fn func(tx store.Store) ([]byte, error)) ([]byte, error) {
	var out []byte
	err := s.Atomically(ctx, func(tx store.Store) error {
		if key == "" {
			r, err := fn(tx)
			out = r
			return err
		}
		if ttl <= 0 {
			return domain.Validation("an idempotency key needs a positive window", map[string]any{"ttlMs": ttl.Milliseconds()})
		}
		t := tx.(*Store)
		k := [3]string{projectID, identity, key}
		now := nowMillis()
		if e, ok := t.db.st.idem[k]; ok && e.expiresAt > now {
			out = cloneBytes(e.result)
			return nil
		}
		r, err := fn(tx)
		if err != nil {
			return err
		}
		t.db.st.idem[k] = idemEntry{result: cloneBytes(r), expiresAt: now + ttl.Milliseconds()}
		out = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// idempotent is WithIdempotency for a typed result, as the engines keep it: fn's value as JSON,
// decoded on a replay. fn runs on the transaction's state.
func idempotent[T any](ctx context.Context, s *Store, scope string, opts store.WriteOptions, fn func(st *state) (T, error)) (T, error) {
	var zero T
	raw, err := s.WithIdempotency(ctx, scope, opts.Identity, opts.IdempotencyKey, opts.IdempotencyTTL, func(tx store.Store) ([]byte, error) {
		v, err := get(ctx, tx.(*Store), fn)
		if err != nil {
			return nil, err
		}
		return json.Marshal(v)
	})
	if err != nil {
		return zero, err
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return zero, err
	}
	return v, nil
}
