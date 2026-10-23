package service

import (
	"context"
	"regexp"
	"slices"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// chronicleSvc is service.Chronicle (conductor src/tools/chronicle.ts and the chronicle resources of
// src/resources/index.ts). Each write is one mutate: the ingest's read, merge and write included, so
// one idempotency key covers the whole call and the merge sees the head it writes from.
type chronicleSvc struct{ c *Core }

var _ Chronicle = chronicleSvc{}

// Chronicle is the Chronicle service.
func (c *Core) Chronicle() Chronicle { return chronicleSvc{c} }

// chronicleReadTool is the posture the chronicle reads are authorized under: the project's read.
const chronicleReadTool = "get_project"

// Conductor's chronicle argument bounds (src/tools/chronicle.ts, src/config/constants.ts).
const (
	maxChronicleBlocks = 500
	// originWalkLimit is how far back an edit looks for the origin of a block its parent lacks.
	originWalkLimit = 20
	// chronicleIndexLimit and chronicleHistoryLimit are the resources' page sizes.
	chronicleIndexLimit   = 100
	chronicleHistoryLimit = 50
)

var (
	authoredSlugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(/[a-z0-9][a-z0-9-]*)*$`)
	sweepBlockIDRE = regexp.MustCompile(`^(?:(?:cov|ctx|dec|rej|chk|ev)\d+|truncated)/\d+$`)
	blockHashRE    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	topicSlugRE    = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	adrIDRE        = regexp.MustCompile(`^ADR-[0-9]{4}$`)
)

func boundedID(field, v string) error {
	if err := required(field, v); err != nil {
		return err
	}
	return maxChars(field, v, maxKeyChars)
}

// checkBlocks is the block list's bounds: at most 500, each id 1-200 characters and unique, text at
// most 20000, at most 32 citations naming the full (project, board, task) triple, an origin with an
// ADR id and a 64-hex hash; authored refuses the sweep's reserved id shape too.
func checkBlocks(blocks []ChronicleBlockInput, authored bool) error {
	if len(blocks) > maxChronicleBlocks {
		return domain.Validation("blocks holds at most 500 blocks", map[string]any{"field": "blocks", "max": maxChronicleBlocks})
	}
	seen := map[string]bool{}
	for _, b := range blocks {
		if err := boundedID("blocks.id", b.ID); err != nil {
			return err
		}
		if seen[b.ID] {
			return domain.Validation("block ids must be unique within a page: a duplicate id silently deletes one of them", map[string]any{"field": "blocks", "id": b.ID})
		}
		seen[b.ID] = true
		if authored && sweepBlockIDRE.MatchString(b.ID) {
			return domain.Validation("block ids of the form (cov|ctx|dec|rej|chk|ev)N/M are reserved for sweep-derived blocks — pick another id (the web editor uses `a-<hex>`)", map[string]any{"field": "blocks", "id": b.ID})
		}
		if err := maxChars("blocks.text", b.Text, maxSummaryChars); err != nil {
			return err
		}
		if len(b.Citations) > maxLabels {
			return domain.Validation("a block carries at most 32 citations", map[string]any{"field": "blocks.citations", "max": maxLabels})
		}
		for _, ct := range b.Citations {
			for _, f := range []struct{ name, v string }{{"citations.projectId", ct.ProjectID}, {"citations.boardId", ct.BoardID}, {"citations.taskId", ct.TaskID}} {
				if err := required(f.name, f.v); err != nil {
					return err
				}
			}
			if err := maxChars("citations.label", ct.Label, maxTitleChars); err != nil {
				return err
			}
		}
		if o := b.Origin; o != nil && (o.AdrID == "" || !blockHashRE.MatchString(o.BlockHash)) {
			return domain.Validation("an origin names its adrId and a 64-hex blockHash", map[string]any{"field": "blocks.origin", "id": b.ID})
		}
	}
	return nil
}

func checkTitle(title string) error {
	if err := required("title", title); err != nil {
		return err
	}
	return maxChars("title", title, maxTitleChars)
}

func toBlocks(in []ChronicleBlockInput, keepOrigin bool) []domain.ChronicleBlock {
	out := make([]domain.ChronicleBlock, len(in))
	for i, b := range in {
		out[i] = domain.ChronicleBlock{ID: b.ID, State: domain.BlockAuthored, Text: b.Text, Citations: b.Citations}
		if keepOrigin && b.Origin != nil {
			o := *b.Origin
			out[i].Origin = &o
		}
	}
	return out
}

// checkBody refuses a body over blob.max_bytes, measured as the store will write it.
func (s chronicleSvc) checkBody(blocks []domain.ChronicleBlock) error {
	if n := int64(len(domain.ChronicleBody(blocks))); n > s.c.media.MaxBytes {
		return domain.Validation("the page body exceeds blob.max_bytes", map[string]any{"field": "blocks", "bytes": n, "max": s.c.media.MaxBytes})
	}
	return nil
}

func agentOf(c domain.Caller) *string {
	if c.Agent == "" {
		return nil
	}
	a := c.Agent
	return &a
}

// CreatePage is create_chronicle_page: a taken slug is VALIDATION, inside the write; every origin
// is dropped, since only a sweep may say where text came from.
func (s chronicleSvc) CreatePage(ctx context.Context, caller domain.Caller, req CreateChroniclePageReq) (CreateChroniclePageResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "create_chronicle_page", domain.GrantSubject{}, false)
	if err != nil {
		return CreateChroniclePageResp{}, err
	}
	if !slices.Contains(domain.ChroniclePageKinds(), req.Kind) {
		return CreateChroniclePageResp{}, domain.Validation("unknown chronicle page kind: "+string(req.Kind), map[string]any{"field": "kind"})
	}
	if err := required("slug", req.Slug); err != nil {
		return CreateChroniclePageResp{}, err
	}
	if err := maxChars("slug", req.Slug, maxTitleChars); err != nil {
		return CreateChroniclePageResp{}, err
	}
	if !authoredSlugRE.MatchString(req.Slug) {
		return CreateChroniclePageResp{}, domain.Validation("slug must be lowercase letters, digits and dashes, with `/` between sections", map[string]any{"field": "slug"})
	}
	if err := checkTitle(req.Title); err != nil {
		return CreateChroniclePageResp{}, err
	}
	if err := checkBlocks(req.Blocks, true); err != nil {
		return CreateChroniclePageResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return CreateChroniclePageResp{}, err
	}
	blocks := toBlocks(req.Blocks, false)
	if err := s.checkBody(blocks); err != nil {
		return CreateChroniclePageResp{}, err
	}
	return mutate(ctx, s.c, caller, mutation{
		Tool: "create_chronicle_page", Args: map[string]any{"projectId": req.ProjectID, "slug": req.Slug},
		ProjectID: req.ProjectID, IdempotencyKey: req.IdempotencyKey, Access: a,
	}, func(t *txn) (CreateChroniclePageResp, error) {
		up, err := t.Tx.UpsertChroniclePage(ctx, store.UpsertChroniclePageInput{
			ProjectID: req.ProjectID, Kind: req.Kind, Slug: req.Slug, Title: req.Title, Blocks: blocks,
			Source: domain.SourceHuman, Author: caller.Identity, Agent: agentOf(caller), RefuseExisting: true,
		})
		if err != nil {
			return CreateChroniclePageResp{}, err
		}
		return CreateChroniclePageResp{PageID: up.Page.ID, VersionID: up.Page.HeadVersionID}, nil
	})
}

// IngestPage is ingest_chronicle_page: first sight creates the page with these blocks as version
// and merge base; a re-ingest merges them (domain.MergeVersions) into the head over the base. No
// visible change writes no version, but its conflicts are recorded on the page.
func (s chronicleSvc) IngestPage(ctx context.Context, caller domain.Caller, req IngestChroniclePageReq) (IngestChroniclePageResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "ingest_chronicle_page", domain.GrantSubject{}, false)
	if err != nil {
		return IngestChroniclePageResp{}, err
	}
	if !slices.Contains(domain.ChroniclePageKinds(), req.Kind) {
		return IngestChroniclePageResp{}, domain.Validation("unknown chronicle page kind: "+string(req.Kind), map[string]any{"field": "kind"})
	}
	if err := required("slug", req.Slug); err != nil {
		return IngestChroniclePageResp{}, err
	}
	if err := maxChars("slug", req.Slug, maxTitleChars); err != nil {
		return IngestChroniclePageResp{}, err
	}
	if err := checkTitle(req.Title); err != nil {
		return IngestChroniclePageResp{}, err
	}
	if req.SourcePath != nil {
		if err := maxChars("sourcePath", *req.SourcePath, maxTitleChars); err != nil {
			return IngestChroniclePageResp{}, err
		}
	}
	if err := checkRecordList("topics", req.Topics, func(v string) bool { return jsLen(v) <= maxLabelChars && topicSlugRE.MatchString(v) }); err != nil {
		return IngestChroniclePageResp{}, err
	}
	if err := checkRecordList("supersededBy", req.SupersededBy, adrIDRE.MatchString); err != nil {
		return IngestChroniclePageResp{}, err
	}
	if req.Coverage.Set && !req.Coverage.Null && !slices.Contains(domain.ChronicleCoverages(), req.Coverage.V) {
		return IngestChroniclePageResp{}, domain.Validation("unknown coverage: "+string(req.Coverage.V), map[string]any{"field": "coverage"})
	}
	if err := checkBlocks(req.Blocks, false); err != nil {
		return IngestChroniclePageResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return IngestChroniclePageResp{}, err
	}
	theirs := toBlocks(req.Blocks, true)
	if err := s.checkBody(theirs); err != nil {
		return IngestChroniclePageResp{}, err
	}
	return mutate(ctx, s.c, caller, mutation{
		Tool: "ingest_chronicle_page", Args: map[string]any{"projectId": req.ProjectID, "slug": req.Slug},
		ProjectID: req.ProjectID, IdempotencyKey: req.IdempotencyKey, Access: a,
	}, func(t *txn) (IngestChroniclePageResp, error) {
		up, err := t.Tx.UpsertChroniclePage(ctx, store.UpsertChroniclePageInput{
			ProjectID: req.ProjectID, Kind: req.Kind, Slug: req.Slug, Title: req.Title, SourcePath: req.SourcePath,
			Blocks: theirs, Source: domain.SourceSweep, Author: caller.Identity, Agent: agentOf(caller),
			Record: domain.ChronicleRecordFieldsInput{
				Topics: req.Topics, SupersededBy: req.SupersededBy, CoverageSet: req.Coverage.Set, Coverage: req.Coverage.Ptr(),
			},
		})
		if err != nil {
			return IngestChroniclePageResp{}, err
		}
		page := up.Page
		resp := IngestChroniclePageResp{PageID: page.ID, VersionID: page.HeadVersionID, Created: up.Created, Conflicts: []string{}}
		if up.Created {
			return resp, nil
		}
		head, err := t.Tx.GetChronicleVersion(ctx, req.ProjectID, page.ID, page.HeadVersionID)
		if err != nil {
			return IngestChroniclePageResp{}, err
		}
		base, ok, err := t.Tx.GetChronicleBase(ctx, req.ProjectID, page.ID)
		if err != nil {
			return IngestChroniclePageResp{}, err
		}
		if !ok {
			base = head.Blocks
		}
		merged := domain.MergeVersions(base, head.Blocks, theirs)
		next := domain.MergedBlocks(merged)
		resp.Conflicts = merged.Conflicts
		if domain.SameVisibleBlocks(next, head.Blocks) {
			resp.Unchanged = true
			if len(merged.Conflicts) > 0 {
				err := t.Tx.RecordChronicleConflicts(ctx, req.ProjectID, page.ID, store.RecordChronicleConflictsInput{
					VersionID: head.ID, Items: domain.ConflictItems(merged), Theirs: theirs, Actor: caller.Identity, Agent: agentOf(caller),
				})
				if err != nil {
					return IngestChroniclePageResp{}, err
				}
			}
			return resp, nil
		}
		if err := s.checkBody(next); err != nil {
			return IngestChroniclePageResp{}, err
		}
		v, err := t.Tx.AppendChronicleVersion(ctx, req.ProjectID, page.ID, store.AppendChronicleVersionInput{
			ExpectedParentVersionID: head.ID, Blocks: next, Source: merged.Source, Author: caller.Identity, Agent: agentOf(caller),
			HasDerived: true, DerivedBlocks: theirs, Conflicts: domain.ConflictItems(merged),
		})
		if err != nil {
			return IngestChroniclePageResp{}, err
		}
		resp.VersionID = v.ID
		return resp, nil
	})
}

func checkRecordList(field string, vs []string, ok func(string) bool) error {
	if len(vs) > maxLabels {
		return domain.Validation(field+" holds at most 32 entries", map[string]any{"field": field, "max": maxLabels})
	}
	for _, v := range vs {
		if !ok(v) {
			return domain.Validation(field+" holds an entry of the wrong shape: "+v, map[string]any{"field": field})
		}
	}
	return nil
}

// EditPage is edit_chronicle_page. Every origin the caller sends is discarded: a block's origin is
// inherited from the parent version, or from the nearest of up to 20 ancestors that carried the
// block, else it is authored. A parent that is not the head is STALE.
func (s chronicleSvc) EditPage(ctx context.Context, caller domain.Caller, req EditChroniclePageReq) (EditChroniclePageResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "edit_chronicle_page", domain.GrantSubject{}, false)
	if err != nil {
		return EditChroniclePageResp{}, err
	}
	if err := boundedID("pageId", req.PageID); err != nil {
		return EditChroniclePageResp{}, err
	}
	if err := boundedID("expectedParentVersionId", req.ExpectedParentVersionID); err != nil {
		return EditChroniclePageResp{}, err
	}
	if err := checkBlocks(req.Blocks, false); err != nil {
		return EditChroniclePageResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return EditChroniclePageResp{}, err
	}
	blocks := toBlocks(req.Blocks, false)
	if err := s.checkBody(blocks); err != nil {
		return EditChroniclePageResp{}, err
	}
	return mutate(ctx, s.c, caller, mutation{
		Tool: "edit_chronicle_page", Args: map[string]any{"projectId": req.ProjectID, "pageId": req.PageID},
		ProjectID: req.ProjectID, IdempotencyKey: req.IdempotencyKey, Access: a,
	}, func(t *txn) (EditChroniclePageResp, error) {
		inherited := map[string]*domain.BlockOrigin{}
		parent, err := t.Tx.GetChronicleVersion(ctx, req.ProjectID, req.PageID, req.ExpectedParentVersionID)
		hasParent := err == nil
		if err != nil {
			if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeNotFound {
				return EditChroniclePageResp{}, err
			}
		}
		if hasParent {
			for _, b := range parent.Blocks {
				inherited[b.ID] = b.Origin
			}
			missing := map[string]bool{}
			for _, b := range blocks {
				if _, ok := inherited[b.ID]; !ok {
					missing[b.ID] = true
				}
			}
			cursor := parent.ParentVersionID
			for hops := 0; cursor != nil && hops < originWalkLimit && len(missing) > 0; hops++ {
				v, err := t.Tx.GetChronicleVersion(ctx, req.ProjectID, req.PageID, *cursor)
				if err != nil {
					break // best effort: an unreadable ancestor leaves the rest authored
				}
				for _, b := range v.Blocks {
					if missing[b.ID] {
						delete(missing, b.ID)
						inherited[b.ID] = b.Origin
					}
				}
				cursor = v.ParentVersionID
			}
		}
		for i := range blocks {
			if o := inherited[blocks[i].ID]; o != nil {
				oo := *o
				blocks[i].Origin = &oo
			}
		}
		v, err := t.Tx.AppendChronicleVersion(ctx, req.ProjectID, req.PageID, store.AppendChronicleVersionInput{
			ExpectedParentVersionID: req.ExpectedParentVersionID, Blocks: blocks, Source: domain.SourceHuman,
			Author: caller.Identity, Agent: agentOf(caller),
		})
		if err != nil {
			return EditChroniclePageResp{}, err
		}
		drifted := []string{}
		for _, b := range v.Blocks {
			if b.State == domain.BlockDerivedThenEdited {
				drifted = append(drifted, b.ID)
			}
		}
		return EditChroniclePageResp{VersionID: v.ID, Drifted: drifted}, nil
	})
}

// DeletePage is delete_chronicle_page: one transaction removes the page, every version and every
// body; a moved head under expectedHeadVersionId is STALE and removes nothing.
func (s chronicleSvc) DeletePage(ctx context.Context, caller domain.Caller, req DeleteChroniclePageReq) (DeleteChroniclePageResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "delete_chronicle_page", domain.GrantSubject{}, false)
	if err != nil {
		return DeleteChroniclePageResp{}, err
	}
	if err := boundedID("pageId", req.PageID); err != nil {
		return DeleteChroniclePageResp{}, err
	}
	if err := maxChars("expectedHeadVersionId", req.ExpectedHeadVersionID, maxKeyChars); err != nil {
		return DeleteChroniclePageResp{}, err
	}
	return mutate(ctx, s.c, caller, mutation{
		Tool: "delete_chronicle_page", Args: map[string]any{"projectId": req.ProjectID, "pageId": req.PageID},
		ProjectID: req.ProjectID, Access: a,
	}, func(t *txn) (DeleteChroniclePageResp, error) {
		err := t.Tx.DeleteChroniclePage(ctx, req.ProjectID, req.PageID, req.ExpectedHeadVersionID, caller.Identity, agentOf(caller))
		return DeleteChroniclePageResp{Ok: true}, err
	})
}

func (s chronicleSvc) authorizeRead(ctx context.Context, caller domain.Caller, projectID string) error {
	_, err := s.c.authorize(ctx, caller, projectID, chronicleReadTool, domain.GrantSubject{}, false)
	return err
}

func chroniclePageOf(p store.ChroniclePage) ChroniclePage {
	out := ChroniclePage{
		ID: p.ID, ProjectID: p.ProjectID, Kind: p.Kind, Slug: p.Slug, Title: p.Title, HeadVersionID: p.HeadVersionID,
		SourcePath: p.SourcePath, LastDerivedObject: p.LastDerivedObject, Topics: p.Topics, Coverage: p.Coverage,
		SupersededBy: p.SupersededBy, CreatedAt: isoMs(p.CreatedAt), UpdatedAt: isoMs(p.UpdatedAt),
	}
	if p.Conflict != nil {
		out.Conflict = &ChronicleConflictState{VersionID: p.Conflict.VersionID, Ts: isoMs(p.Conflict.Ts), Items: p.Conflict.Items, TheirsObject: p.Conflict.TheirsObject}
	}
	if p.UpdatedBy != nil {
		out.UpdatedBy = &ChronicleByline{Author: p.UpdatedBy.Author, Agent: p.UpdatedBy.Agent}
	}
	return out
}

func chronicleVersionOf(v store.ChronicleVersion) ChronicleVersion {
	blocks := v.Blocks
	if blocks == nil {
		blocks = []domain.ChronicleBlock{}
	}
	return ChronicleVersion{
		ID: v.ID, PageID: v.PageID, ParentVersionID: v.ParentVersionID, Source: v.Source, Author: v.Author, Agent: v.Agent,
		Ts: isoMs(v.Ts), Blocks: blocks, BlocksObject: v.BlocksObject, BlockCount: v.BlockCount,
	}
}

func isNotFound(err error) bool {
	e, ok := domain.AsBatonDeckError(err)
	return ok && e.Code == domain.CodeNotFound
}

// ListPages is the chronicle resource.
func (s chronicleSvc) ListPages(ctx context.Context, caller domain.Caller, req ListChroniclePagesReq) (ChronicleIndex, error) {
	if err := s.authorizeRead(ctx, caller, req.ProjectID); err != nil {
		return ChronicleIndex{}, err
	}
	l, err := s.c.store.ListChroniclePages(ctx, req.ProjectID, "", req.Cursor, chronicleIndexLimit)
	if err != nil {
		return ChronicleIndex{}, s.c.mapErr("chronicle", err)
	}
	out := ChronicleIndex{Pages: make([]ChroniclePage, len(l.Pages)), NextCursor: l.NextCursor}
	for i, p := range l.Pages {
		out.Pages[i] = chroniclePageOf(p)
	}
	return out, nil
}

// GetPage is the chronicle-page resource: the page and its head (two reads), and the conflicting
// derivation only while a conflict stands.
func (s chronicleSvc) GetPage(ctx context.Context, caller domain.Caller, req GetChroniclePageReq) (ChroniclePageView, error) {
	if err := s.authorizeRead(ctx, caller, req.ProjectID); err != nil {
		return ChroniclePageView{}, err
	}
	p, err := s.c.store.GetChroniclePage(ctx, req.ProjectID, req.PageID)
	if isNotFound(err) {
		return ChroniclePageView{}, nil
	}
	if err != nil {
		return ChroniclePageView{}, s.c.mapErr("chronicle-page", err)
	}
	head, err := s.c.store.GetChronicleVersion(ctx, req.ProjectID, p.ID, p.HeadVersionID)
	if err != nil {
		return ChroniclePageView{}, s.c.mapErr("chronicle-page", err)
	}
	page, hv := chroniclePageOf(p), chronicleVersionOf(head)
	view := ChroniclePageView{Page: &page, Head: &hv, Drifted: []string{}}
	for _, b := range head.Blocks {
		if b.State == domain.BlockDerivedThenEdited {
			view.Drifted = append(view.Drifted, b.ID)
		}
	}
	if p.Conflict != nil {
		theirs, _, err := s.c.store.GetChronicleConflictTheirs(ctx, req.ProjectID, p.ID)
		if err != nil {
			return ChroniclePageView{}, s.c.mapErr("chronicle-page", err)
		}
		cv := &ChronicleConflictsView{VersionID: p.Conflict.VersionID, Ts: isoMs(p.Conflict.Ts), Items: []ChronicleConflictView{}}
		for _, it := range p.Conflict.Items {
			item := ChronicleConflictView{BlockID: it.BlockID, Kind: it.Kind}
			if i := slices.IndexFunc(theirs, func(b domain.ChronicleBlock) bool { return b.ID == it.BlockID }); i >= 0 {
				b := theirs[i]
				item.Theirs = &b
			}
			cv.Items = append(cv.Items, item)
		}
		view.Conflicts = cv
	}
	return view, nil
}

// ListVersions is the chronicle-history resource.
func (s chronicleSvc) ListVersions(ctx context.Context, caller domain.Caller, req ListChronicleVersionsReq) (ChronicleHistory, error) {
	if err := s.authorizeRead(ctx, caller, req.ProjectID); err != nil {
		return ChronicleHistory{}, err
	}
	l, err := s.c.store.ListChronicleVersions(ctx, req.ProjectID, req.PageID, req.Cursor, chronicleHistoryLimit)
	if isNotFound(err) {
		return ChronicleHistory{Versions: []ChronicleVersionMeta{}}, nil
	}
	if err != nil {
		return ChronicleHistory{}, s.c.mapErr("chronicle-history", err)
	}
	out := ChronicleHistory{Versions: make([]ChronicleVersionMeta, len(l.Versions)), NextCursor: l.NextCursor}
	for i, v := range l.Versions {
		out.Versions[i] = ChronicleVersionMeta{ID: v.ID, ParentVersionID: v.ParentVersionID, Source: v.Source, Author: v.Author, Agent: v.Agent, Ts: isoMs(v.Ts), BlockCount: v.BlockCount}
	}
	return out, nil
}

// GetVersion is the chronicle-version resource.
func (s chronicleSvc) GetVersion(ctx context.Context, caller domain.Caller, req GetChronicleVersionReq) (ChronicleVersionView, error) {
	if err := s.authorizeRead(ctx, caller, req.ProjectID); err != nil {
		return ChronicleVersionView{}, err
	}
	v, err := s.c.store.GetChronicleVersion(ctx, req.ProjectID, req.PageID, req.VersionID)
	if isNotFound(err) {
		return ChronicleVersionView{}, nil
	}
	if err != nil {
		return ChronicleVersionView{}, s.c.mapErr("chronicle-version", err)
	}
	cv := chronicleVersionOf(v)
	return ChronicleVersionView{Version: &cv}, nil
}
