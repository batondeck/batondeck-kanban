package memstore

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The Chronicle area of contract_chronicle.go: pages by (project, page), versions by (project,
// page, version) without their bodies, and the bodies by (project, key).

func cloneChroniclePage(p store.ChroniclePage) store.ChroniclePage {
	p.SourcePath, p.LastDerivedObject, p.Coverage = clonePtr(p.SourcePath), clonePtr(p.LastDerivedObject), clonePtr(p.Coverage)
	p.Topics, p.SupersededBy = slices.Clone(p.Topics), slices.Clone(p.SupersededBy)
	if p.Conflict != nil {
		c := *p.Conflict
		c.Items = slices.Clone(c.Items)
		p.Conflict = &c
	}
	if p.UpdatedBy != nil {
		b := *p.UpdatedBy
		b.Agent = clonePtr(b.Agent)
		p.UpdatedBy = &b
	}
	return p
}

func cloneChronicleVersion(v store.ChronicleVersion) store.ChronicleVersion {
	v.ParentVersionID, v.Agent = clonePtr(v.ParentVersionID), clonePtr(v.Agent)
	v.Blocks = nil
	return v
}

func (st *state) chronicleEvent(projectID, actor string, agent *string, typ domain.EventType, data map[string]any) {
	raw, err := json.Marshal(data)
	if err != nil {
		panic(fmt.Sprintf("memstore: marshal chronicle event: %v", err))
	}
	e := store.Event{ProjectID: projectID, ID: newID("E-", 24), Actor: actor, Agent: clonePtr(agent), Type: typ, Data: raw, Ts: nowMillis()}
	st.events[[2]string{projectID, e.ID}] = e
}

func (st *state) putChronicleBody(projectID, key string, body []byte) {
	k := [2]string{projectID, key}
	if _, ok := st.chronicleObjects[k]; !ok {
		st.chronicleObjects[k] = cloneBytes(body)
	}
}

// dropChronicleBody is the engines' dropChronicleBody.
func (st *state) dropChronicleBody(projectID, pageID string, key *string, keep ...*string) {
	if key == nil {
		return
	}
	for _, k := range keep {
		if k != nil && *k == *key {
			return
		}
	}
	for vk, v := range st.chronicleVersions {
		if vk[0] == projectID && vk[1] == pageID && v.BlocksObject == *key {
			return
		}
	}
	delete(st.chronicleObjects, [2]string{projectID, *key})
}

func (st *state) chronicleBody(projectID, key string) ([]domain.ChronicleBlock, error) {
	raw, ok := st.chronicleObjects[[2]string{projectID, key}]
	if !ok {
		return nil, domain.NotFound("ChronicleObject", key)
	}
	var blocks []domain.ChronicleBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("memstore: chronicle body %s: %w", key, err)
	}
	if blocks == nil {
		blocks = []domain.ChronicleBlock{}
	}
	return blocks, nil
}

func (st *state) chroniclePage(projectID, pageID string) (store.ChroniclePage, error) {
	p, ok := st.chroniclePages[[2]string{projectID, pageID}]
	if !ok {
		return store.ChroniclePage{}, domain.NotFound("ChroniclePage", pageID)
	}
	return cloneChroniclePage(p), nil
}

func validChronicleWrite(source domain.ChronicleSource, author string) error {
	if source != domain.SourceSweep && source != domain.SourceHuman {
		return domain.Validation("unknown chronicle source: "+string(source), map[string]any{"source": source})
	}
	if author == "" {
		return domain.Validation("a chronicle write needs an author", nil)
	}
	return nil
}

func samePtr[T comparable](a, b *T) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func (s *Store) UpsertChroniclePage(ctx context.Context, in store.UpsertChroniclePageInput) (store.UpsertedChroniclePage, error) {
	switch {
	case !slices.Contains(domain.ChroniclePageKinds(), in.Kind):
		return store.UpsertedChroniclePage{}, domain.Validation("unknown chronicle page kind: "+string(in.Kind), map[string]any{"kind": in.Kind})
	case in.Slug == "" || in.Title == "":
		return store.UpsertedChroniclePage{}, domain.Validation("a chronicle page needs a slug and a title", nil)
	}
	if err := validChronicleWrite(in.Source, in.Author); err != nil {
		return store.UpsertedChroniclePage{}, err
	}
	pageID := domain.ChroniclePageID(in.Slug)
	return get(ctx, s, func(st *state) (store.UpsertedChroniclePage, error) {
		if p, ok := st.chroniclePages[[2]string{in.ProjectID, pageID}]; ok {
			if in.RefuseExisting {
				return store.UpsertedChroniclePage{}, domain.Validation("a Chronicle record with this slug already exists", map[string]any{"slug": in.Slug, "pageId": pageID})
			}
			cur := domain.ChronicleRecordFields{Topics: p.Topics, Coverage: p.Coverage, SupersededBy: p.SupersededBy}
			next := domain.NextRecordFields(&cur, in.Record)
			if p.Kind != in.Kind || p.Title != in.Title || !samePtr(p.SourcePath, in.SourcePath) || !domain.SameRecordFields(cur, next) {
				p.Kind, p.Title, p.SourcePath = in.Kind, in.Title, clonePtr(in.SourcePath)
				p.Topics, p.Coverage, p.SupersededBy = next.Topics, clonePtr(next.Coverage), next.SupersededBy
				p.UpdatedAt = max(nowMillis(), p.UpdatedAt)
				st.chroniclePages[[2]string{in.ProjectID, pageID}] = cloneChroniclePage(p)
				st.chronicleEvent(in.ProjectID, in.Author, in.Agent, domain.EventChroniclePageUpdated, map[string]any{"pageId": pageID, "kind": in.Kind, "title": in.Title})
			}
			return store.UpsertedChroniclePage{Page: cloneChroniclePage(p)}, nil
		}
		if _, ok := st.projects[in.ProjectID]; !ok {
			return store.UpsertedChroniclePage{}, domain.NotFound("Project", in.ProjectID)
		}
		body := domain.ChronicleBody(in.Blocks)
		key := store.ChronicleObjectKey(pageID, body)
		var baseBlocks []domain.ChronicleBlock
		if in.Source == domain.SourceSweep {
			baseBlocks = in.Blocks
		}
		baseBody := domain.ChronicleBody(baseBlocks)
		baseKey := store.ChronicleObjectKey(pageID, baseBody)
		now := nowMillis()
		fields := domain.NextRecordFields(nil, in.Record)
		v := store.ChronicleVersion{
			ProjectID: in.ProjectID, PageID: pageID, ID: newID("CV-", 12), Source: in.Source, Author: in.Author,
			Agent: clonePtr(in.Agent), Ts: now, BlocksObject: key, BlockCount: int64(len(in.Blocks)),
		}
		p := store.ChroniclePage{
			ProjectID: in.ProjectID, ID: pageID, Kind: in.Kind, Slug: in.Slug, Title: in.Title, HeadVersionID: v.ID,
			SourcePath: clonePtr(in.SourcePath), LastDerivedObject: &baseKey, Topics: fields.Topics, Coverage: clonePtr(fields.Coverage),
			SupersededBy: fields.SupersededBy, UpdatedBy: &store.ChronicleByline{Author: in.Author, Agent: clonePtr(in.Agent)},
			CreatedAt: now, UpdatedAt: now,
		}
		st.chroniclePages[[2]string{in.ProjectID, pageID}] = cloneChroniclePage(p)
		st.putChronicleBody(in.ProjectID, key, body)
		st.putChronicleBody(in.ProjectID, baseKey, baseBody)
		st.chronicleVersions[[3]string{in.ProjectID, pageID, v.ID}] = v
		st.chronicleEvent(in.ProjectID, in.Author, in.Agent, domain.EventChroniclePageCreated, map[string]any{"pageId": pageID, "slug": in.Slug, "kind": in.Kind})
		return store.UpsertedChroniclePage{Page: cloneChroniclePage(p), Created: true}, nil
	})
}

func (s *Store) GetChroniclePage(ctx context.Context, projectID, pageID string) (store.ChroniclePage, error) {
	return get(ctx, s, func(st *state) (store.ChroniclePage, error) { return st.chroniclePage(projectID, pageID) })
}

func (s *Store) DeleteChroniclePage(ctx context.Context, projectID, pageID, expected, actor string, agent *string) error {
	return s.do(ctx, func(st *state) error {
		p, err := st.chroniclePage(projectID, pageID)
		if err != nil {
			return err
		}
		if expected != "" && p.HeadVersionID != expected {
			return domain.ChronicleStale(expected, p.HeadVersionID)
		}
		delete(st.chroniclePages, [2]string{projectID, pageID})
		for k := range st.chronicleVersions {
			if k[0] == projectID && k[1] == pageID {
				delete(st.chronicleVersions, k)
			}
		}
		for k := range st.chronicleObjects {
			if k[0] == projectID && strings.HasPrefix(k[1], pageID+"/") {
				delete(st.chronicleObjects, k)
			}
		}
		st.chronicleEvent(projectID, actor, agent, domain.EventChroniclePageDeleted, map[string]any{"pageId": pageID, "slug": p.Slug, "kind": p.Kind, "title": p.Title})
		return nil
	})
}

func chronicleListLimit(limit int) int { return clamp(limit, 50, 200) }

func (s *Store) ListChroniclePages(ctx context.Context, projectID string, kind domain.ChroniclePageKind, cursor string, limit int) (store.ChroniclePageList, error) {
	if kind != "" && !slices.Contains(domain.ChroniclePageKinds(), kind) {
		return store.ChroniclePageList{}, domain.Validation("unknown chronicle page kind: "+string(kind), map[string]any{"kind": kind})
	}
	after := ""
	if cursor != "" {
		slug, _, err := store.DecodeCursor(cursor)
		if err != nil {
			return store.ChroniclePageList{}, err
		}
		after = slug
	}
	limit = chronicleListLimit(limit)
	return get(ctx, s, func(st *state) (store.ChroniclePageList, error) {
		var all []store.ChroniclePage
		for k, p := range st.chroniclePages {
			if k[0] == projectID && (kind == "" || p.Kind == kind) && p.Slug > after {
				all = append(all, cloneChroniclePage(p))
			}
		}
		slices.SortFunc(all, func(a, b store.ChroniclePage) int { return strings.Compare(a.Slug, b.Slug) })
		out := store.ChroniclePageList{Pages: []store.ChroniclePage{}}
		for i, p := range all {
			if i == limit {
				last := out.Pages[limit-1]
				out.NextCursor = store.EncodeCursor(last.Slug, last.ID)
				break
			}
			out.Pages = append(out.Pages, p)
		}
		return out, nil
	})
}

func (s *Store) AppendChronicleVersion(ctx context.Context, projectID, pageID string, in store.AppendChronicleVersionInput) (store.ChronicleVersion, error) {
	if err := validChronicleWrite(in.Source, in.Author); err != nil {
		return store.ChronicleVersion{}, err
	}
	if in.ExpectedParentVersionID == "" {
		return store.ChronicleVersion{}, domain.Validation("an appended version names its parent", nil)
	}
	return get(ctx, s, func(st *state) (store.ChronicleVersion, error) {
		p, err := st.chroniclePage(projectID, pageID)
		if err != nil {
			return store.ChronicleVersion{}, err
		}
		if p.HeadVersionID != in.ExpectedParentVersionID {
			return store.ChronicleVersion{}, domain.ChronicleStale(in.ExpectedParentVersionID, p.HeadVersionID)
		}
		body := domain.ChronicleBody(in.Blocks)
		key := store.ChronicleObjectKey(pageID, body)
		now := max(nowMillis(), p.UpdatedAt+1) // later than the parent, as on the engines
		parent := in.ExpectedParentVersionID
		v := store.ChronicleVersion{
			ProjectID: projectID, PageID: pageID, ID: newID("CV-", 12), ParentVersionID: &parent, Source: in.Source,
			Author: in.Author, Agent: clonePtr(in.Agent), Ts: now, BlocksObject: key, BlockCount: int64(len(in.Blocks)),
		}
		oldBase, oldConflict := p.LastDerivedObject, p.Conflict
		base := p.LastDerivedObject
		if !in.HasDerived && oldConflict != nil {
			// A human write settles the standing conflict: its derivation becomes the base.
			settled := oldConflict.TheirsObject
			base = &settled
		}
		var conflict *store.ChronicleConflict
		st.putChronicleBody(projectID, key, body)
		if in.HasDerived {
			derived := domain.ChronicleBody(in.DerivedBlocks)
			dk := store.ChronicleObjectKey(pageID, derived)
			base = &dk
			st.putChronicleBody(projectID, dk, derived)
			if len(in.Conflicts) > 0 {
				conflict = &store.ChronicleConflict{VersionID: v.ID, Ts: now, Items: slices.Clone(in.Conflicts), TheirsObject: dk}
			}
		}
		p.HeadVersionID, p.LastDerivedObject, p.Conflict = v.ID, base, conflict
		p.UpdatedBy = &store.ChronicleByline{Author: in.Author, Agent: clonePtr(in.Agent)}
		p.UpdatedAt = now
		st.chroniclePages[[2]string{projectID, pageID}] = cloneChroniclePage(p)
		st.chronicleVersions[[3]string{projectID, pageID, v.ID}] = cloneChronicleVersion(v)
		data := map[string]any{"pageId": pageID, "versionId": v.ID, "source": in.Source}
		var newTheirs *string
		if conflict != nil {
			ids := make([]string, len(conflict.Items))
			for i, it := range conflict.Items {
				ids[i] = it.BlockID
			}
			data["conflicts"] = ids
			newTheirs = &conflict.TheirsObject
		}
		st.chronicleEvent(projectID, in.Author, in.Agent, domain.EventChronicleVersionAdded, data)
		if in.HasDerived || base != oldBase {
			st.dropChronicleBody(projectID, pageID, oldBase, base, newTheirs)
		}
		if oldConflict != nil {
			st.dropChronicleBody(projectID, pageID, &oldConflict.TheirsObject, base, newTheirs)
		}
		v.Blocks = domain.WithComputedProvenance(in.Blocks)
		return v, nil
	})
}

func (s *Store) RecordChronicleConflicts(ctx context.Context, projectID, pageID string, in store.RecordChronicleConflictsInput) error {
	if in.Actor == "" || in.VersionID == "" {
		return domain.Validation("recorded conflicts name their head and their actor", nil)
	}
	return s.do(ctx, func(st *state) error {
		p, err := st.chroniclePage(projectID, pageID)
		if err != nil {
			return err
		}
		if p.HeadVersionID != in.VersionID {
			return domain.ChronicleStale(in.VersionID, p.HeadVersionID)
		}
		body := domain.ChronicleBody(in.Theirs)
		key := store.ChronicleObjectKey(pageID, body)
		now := max(nowMillis(), p.UpdatedAt)
		old := p.Conflict
		items := slices.Clone(in.Items)
		if items == nil {
			items = []domain.ChronicleConflictItem{}
		}
		p.Conflict = &store.ChronicleConflict{VersionID: in.VersionID, Ts: now, Items: items, TheirsObject: key}
		p.UpdatedAt = now
		st.chroniclePages[[2]string{projectID, pageID}] = cloneChroniclePage(p)
		st.putChronicleBody(projectID, key, body)
		ids := make([]string, len(items))
		for i, it := range items {
			ids[i] = it.BlockID
		}
		st.chronicleEvent(projectID, in.Actor, in.Agent, domain.EventChronicleConflictsFlagged, map[string]any{"pageId": pageID, "versionId": in.VersionID, "blocks": ids})
		if old != nil {
			st.dropChronicleBody(projectID, pageID, &old.TheirsObject, &key, p.LastDerivedObject)
		}
		return nil
	})
}

func (s *Store) GetChronicleConflictTheirs(ctx context.Context, projectID, pageID string) ([]domain.ChronicleBlock, bool, error) {
	return s.pointedBody(ctx, projectID, pageID, func(p store.ChroniclePage) *string {
		if p.Conflict == nil {
			return nil
		}
		return &p.Conflict.TheirsObject
	})
}

func (s *Store) GetChronicleBase(ctx context.Context, projectID, pageID string) ([]domain.ChronicleBlock, bool, error) {
	return s.pointedBody(ctx, projectID, pageID, func(p store.ChroniclePage) *string { return p.LastDerivedObject })
}

func (s *Store) pointedBody(ctx context.Context, projectID, pageID string, pointer func(store.ChroniclePage) *string) ([]domain.ChronicleBlock, bool, error) {
	var blocks []domain.ChronicleBlock
	var ok bool
	err := s.do(ctx, func(st *state) error {
		p, err := st.chroniclePage(projectID, pageID)
		if err != nil {
			return err
		}
		key := pointer(p)
		if key == nil {
			return nil
		}
		blocks, err = st.chronicleBody(projectID, *key)
		ok = err == nil
		return err
	})
	return blocks, ok, err
}

func (s *Store) ListChronicleObjects(ctx context.Context, projectID string) ([]string, error) {
	return get(ctx, s, func(st *state) ([]string, error) {
		out := []string{}
		for k := range st.chronicleObjects {
			if k[0] == projectID {
				out = append(out, k[1])
			}
		}
		slices.Sort(out)
		return out, nil
	})
}

func (s *Store) GetChronicleVersion(ctx context.Context, projectID, pageID, versionID string) (store.ChronicleVersion, error) {
	return get(ctx, s, func(st *state) (store.ChronicleVersion, error) {
		v, ok := st.chronicleVersions[[3]string{projectID, pageID, versionID}]
		if !ok {
			return store.ChronicleVersion{}, domain.NotFound("ChronicleVersion", versionID)
		}
		v = cloneChronicleVersion(v)
		blocks, err := st.chronicleBody(projectID, v.BlocksObject)
		if err != nil {
			return store.ChronicleVersion{}, err
		}
		v.Blocks = blocks
		return v, nil
	})
}

func (s *Store) ListChronicleVersions(ctx context.Context, projectID, pageID, cursor string, limit int) (store.ChronicleVersionList, error) {
	var afterTs int64
	afterID := ""
	if cursor != "" {
		order, id, err := store.DecodeCursor(cursor)
		if err != nil {
			return store.ChronicleVersionList{}, err
		}
		ts, perr := strconv.ParseInt(order, 10, 64)
		if perr != nil || id == "" {
			return store.ChronicleVersionList{}, domain.Validation("Invalid cursor", nil)
		}
		afterTs, afterID = ts, id
	}
	limit = chronicleListLimit(limit)
	return get(ctx, s, func(st *state) (store.ChronicleVersionList, error) {
		if _, err := st.chroniclePage(projectID, pageID); err != nil {
			return store.ChronicleVersionList{}, err
		}
		var all []store.ChronicleVersion
		for k, v := range st.chronicleVersions {
			if k[0] != projectID || k[1] != pageID {
				continue
			}
			if afterID != "" && !(v.Ts < afterTs || (v.Ts == afterTs && v.ID < afterID)) {
				continue
			}
			all = append(all, cloneChronicleVersion(v))
		}
		slices.SortFunc(all, func(a, b store.ChronicleVersion) int {
			return cmp.Or(cmp.Compare(b.Ts, a.Ts), strings.Compare(b.ID, a.ID))
		})
		out := store.ChronicleVersionList{Versions: []store.ChronicleVersion{}}
		for i, v := range all {
			if i == limit {
				last := out.Versions[limit-1]
				out.NextCursor = store.EncodeCursor(strconv.FormatInt(last.Ts, 10), last.ID)
				break
			}
			out.Versions = append(out.Versions, v)
		}
		return out, nil
	})
}
