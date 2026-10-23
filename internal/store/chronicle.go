package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The Chronicle area of contract_chronicle.go, on both engines. Every write moves its page row
// first (an insert, or an update conditioned on the head it read), so the page row's lock orders
// every writer of one page, and only then writes the bodies, the version and the event: a writer
// that lost to a delete or another head finds no row and writes nothing.

type chronicleQueries interface {
	GetChroniclePage(ctx context.Context, arg sqlitedb.GetChroniclePageParams) (sqlitedb.ChroniclePage, error)
	InsertChroniclePage(ctx context.Context, arg sqlitedb.InsertChroniclePageParams) (int64, error)
	RefreshChroniclePage(ctx context.Context, arg sqlitedb.RefreshChroniclePageParams) (int64, error)
	MoveChronicleHead(ctx context.Context, arg sqlitedb.MoveChronicleHeadParams) (int64, error)
	SetChronicleConflict(ctx context.Context, arg sqlitedb.SetChronicleConflictParams) (int64, error)
	DeleteChroniclePage(ctx context.Context, arg sqlitedb.DeleteChroniclePageParams) (int64, error)
	ListChroniclePages(ctx context.Context, arg sqlitedb.ListChroniclePagesParams) ([]sqlitedb.ChroniclePage, error)
	InsertChronicleVersion(ctx context.Context, arg sqlitedb.InsertChronicleVersionParams) error
	GetChronicleVersion(ctx context.Context, arg sqlitedb.GetChronicleVersionParams) (sqlitedb.ChronicleVersion, error)
	ListChronicleVersions(ctx context.Context, arg sqlitedb.ListChronicleVersionsParams) ([]sqlitedb.ChronicleVersion, error)
	InsertChronicleBlob(ctx context.Context, arg sqlitedb.InsertChronicleBlobParams) error
	GetChronicleBlob(ctx context.Context, arg sqlitedb.GetChronicleBlobParams) (sqlitedb.ChronicleBlob, error)
	DeleteChronicleBlobsByPage(ctx context.Context, arg sqlitedb.DeleteChronicleBlobsByPageParams) (int64, error)
	CountChronicleVersionsNaming(ctx context.Context, arg sqlitedb.CountChronicleVersionsNamingParams) (int64, error)
	DeleteChronicleBlob(ctx context.Context, arg sqlitedb.DeleteChronicleBlobParams) (int64, error)
	ListChronicleObjects(ctx context.Context, projectID string) ([]string, error)
	AppendChronicleEvent(ctx context.Context, arg sqlitedb.AppendChronicleEventParams) error
}

func (s *SQLite) UpsertChroniclePage(ctx context.Context, in UpsertChroniclePageInput) (UpsertedChroniclePage, error) {
	return upsertChroniclePage(ctx, s, in)
}

func (s *Postgres) UpsertChroniclePage(ctx context.Context, in UpsertChroniclePageInput) (UpsertedChroniclePage, error) {
	return upsertChroniclePage(ctx, s, in)
}

func (s *SQLite) GetChroniclePage(ctx context.Context, projectID, pageID string) (ChroniclePage, error) {
	return getChroniclePage(ctx, s.queries(false), projectID, pageID)
}

func (s *Postgres) GetChroniclePage(ctx context.Context, projectID, pageID string) (ChroniclePage, error) {
	return getChroniclePage(ctx, s.queries(false), projectID, pageID)
}

func (s *SQLite) DeleteChroniclePage(ctx context.Context, projectID, pageID, expectedHeadVersionID, actor string, agent *string) error {
	return deleteChroniclePage(ctx, s, projectID, pageID, expectedHeadVersionID, actor, agent)
}

func (s *Postgres) DeleteChroniclePage(ctx context.Context, projectID, pageID, expectedHeadVersionID, actor string, agent *string) error {
	return deleteChroniclePage(ctx, s, projectID, pageID, expectedHeadVersionID, actor, agent)
}

func (s *SQLite) ListChroniclePages(ctx context.Context, projectID string, kind domain.ChroniclePageKind, cursor string, limit int) (ChroniclePageList, error) {
	return listChroniclePages(ctx, s.queries(false), projectID, kind, cursor, limit)
}

func (s *Postgres) ListChroniclePages(ctx context.Context, projectID string, kind domain.ChroniclePageKind, cursor string, limit int) (ChroniclePageList, error) {
	return listChroniclePages(ctx, s.queries(false), projectID, kind, cursor, limit)
}

func (s *SQLite) AppendChronicleVersion(ctx context.Context, projectID, pageID string, in AppendChronicleVersionInput) (ChronicleVersion, error) {
	return appendChronicleVersion(ctx, s, projectID, pageID, in)
}

func (s *Postgres) AppendChronicleVersion(ctx context.Context, projectID, pageID string, in AppendChronicleVersionInput) (ChronicleVersion, error) {
	return appendChronicleVersion(ctx, s, projectID, pageID, in)
}

func (s *SQLite) RecordChronicleConflicts(ctx context.Context, projectID, pageID string, in RecordChronicleConflictsInput) error {
	return recordChronicleConflicts(ctx, s, projectID, pageID, in)
}

func (s *Postgres) RecordChronicleConflicts(ctx context.Context, projectID, pageID string, in RecordChronicleConflictsInput) error {
	return recordChronicleConflicts(ctx, s, projectID, pageID, in)
}

func (s *SQLite) GetChronicleConflictTheirs(ctx context.Context, projectID, pageID string) ([]domain.ChronicleBlock, bool, error) {
	return chroniclePointedBody(ctx, s.queries(false), projectID, pageID, conflictPointer)
}

func (s *Postgres) GetChronicleConflictTheirs(ctx context.Context, projectID, pageID string) ([]domain.ChronicleBlock, bool, error) {
	return chroniclePointedBody(ctx, s.queries(false), projectID, pageID, conflictPointer)
}

func (s *SQLite) GetChronicleBase(ctx context.Context, projectID, pageID string) ([]domain.ChronicleBlock, bool, error) {
	return chroniclePointedBody(ctx, s.queries(false), projectID, pageID, basePointer)
}

func (s *Postgres) GetChronicleBase(ctx context.Context, projectID, pageID string) ([]domain.ChronicleBlock, bool, error) {
	return chroniclePointedBody(ctx, s.queries(false), projectID, pageID, basePointer)
}

func (s *SQLite) ListChronicleObjects(ctx context.Context, projectID string) ([]string, error) {
	return listChronicleObjects(ctx, s.queries(false), projectID)
}

func (s *Postgres) ListChronicleObjects(ctx context.Context, projectID string) ([]string, error) {
	return listChronicleObjects(ctx, s.queries(false), projectID)
}

func (s *SQLite) GetChronicleVersion(ctx context.Context, projectID, pageID, versionID string) (ChronicleVersion, error) {
	return getChronicleVersion(ctx, s.queries(false), projectID, pageID, versionID)
}

func (s *Postgres) GetChronicleVersion(ctx context.Context, projectID, pageID, versionID string) (ChronicleVersion, error) {
	return getChronicleVersion(ctx, s.queries(false), projectID, pageID, versionID)
}

func (s *SQLite) ListChronicleVersions(ctx context.Context, projectID, pageID, cursor string, limit int) (ChronicleVersionList, error) {
	return listChronicleVersions(ctx, s.queries(false), projectID, pageID, cursor, limit)
}

func (s *Postgres) ListChronicleVersions(ctx context.Context, projectID, pageID, cursor string, limit int) (ChronicleVersionList, error) {
	return listChronicleVersions(ctx, s.queries(false), projectID, pageID, cursor, limit)
}

// ChronicleObjectKey is a body's key: its page, then the SHA-256 of the bytes.
func ChronicleObjectKey(pageID string, body []byte) string {
	sum := sha256.Sum256(body)
	return pageID + "/" + hex.EncodeToString(sum[:])
}

// ChroniclePageKeyRange is the half-open key range of a page's bodies: '0' is the byte after '/'.
func ChroniclePageKeyRange(pageID string) (from, to string) { return pageID + "/", pageID + "0" }

func chronicleListLimit(limit int) int {
	switch {
	case limit <= 0:
		return 50
	case limit > 200:
		return 200
	}
	return limit
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

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("store: marshal %T: %v", v, err))
	}
	return string(b)
}

func nullJSON[T any](p *T) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: jsonText(p), Valid: true}
}

func nullCoverage(c *domain.ChronicleCoverage) sql.NullString {
	if c == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*c), Valid: true}
}

func pageFromRow(r sqlitedb.ChroniclePage) (ChroniclePage, error) {
	p := ChroniclePage{
		ProjectID: r.ProjectID, ID: r.ID, Kind: domain.ChroniclePageKind(r.Kind), Slug: r.Slug, Title: r.Title,
		HeadVersionID: r.HeadVersionID, SourcePath: strPtr(r.SourcePath), LastDerivedObject: strPtr(r.LastDerivedObject),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if err := json.Unmarshal([]byte(r.Topics), &p.Topics); err != nil {
		return ChroniclePage{}, fmt.Errorf("store: chronicle page %s topics: %w", r.ID, err)
	}
	if err := json.Unmarshal([]byte(r.SupersededBy), &p.SupersededBy); err != nil {
		return ChroniclePage{}, fmt.Errorf("store: chronicle page %s superseded_by: %w", r.ID, err)
	}
	if r.Coverage.Valid {
		c := domain.ChronicleCoverage(r.Coverage.String)
		p.Coverage = &c
	}
	if r.Conflict.Valid {
		p.Conflict = new(ChronicleConflict)
		if err := json.Unmarshal([]byte(r.Conflict.String), p.Conflict); err != nil {
			return ChroniclePage{}, fmt.Errorf("store: chronicle page %s conflict: %w", r.ID, err)
		}
	}
	if r.UpdatedBy.Valid {
		p.UpdatedBy = new(ChronicleByline)
		if err := json.Unmarshal([]byte(r.UpdatedBy.String), p.UpdatedBy); err != nil {
			return ChroniclePage{}, fmt.Errorf("store: chronicle page %s updated_by: %w", r.ID, err)
		}
	}
	if p.Topics == nil {
		p.Topics = []string{}
	}
	if p.SupersededBy == nil {
		p.SupersededBy = []string{}
	}
	return p, nil
}

func versionFromRow(r sqlitedb.ChronicleVersion) ChronicleVersion {
	return ChronicleVersion{
		ProjectID: r.ProjectID, PageID: r.PageID, ID: r.ID, ParentVersionID: strPtr(r.ParentVersionID),
		Source: domain.ChronicleSource(r.Source), Author: r.Author, Agent: strPtr(r.Agent), Ts: r.Ts,
		BlocksObject: r.BlocksObject, BlockCount: r.BlockCount,
	}
}

func getChroniclePage(ctx context.Context, q sqlQueries, projectID, pageID string) (ChroniclePage, error) {
	r, err := q.GetChroniclePage(ctx, sqlitedb.GetChroniclePageParams{ProjectID: projectID, ID: pageID})
	if err := found(err, "ChroniclePage", pageID); err != nil {
		return ChroniclePage{}, err
	}
	return pageFromRow(r)
}

// lostChronicleHead is the error of a conditioned write that moved no row: the page is gone, or
// its head is not the one expected.
func lostChronicleHead(ctx context.Context, q sqlQueries, projectID, pageID, expected string) error {
	p, err := getChroniclePage(ctx, q, projectID, pageID)
	if err != nil {
		return err
	}
	return domain.ChronicleStale(expected, p.HeadVersionID)
}

func putChronicleBody(ctx context.Context, q sqlQueries, projectID, key string, body []byte) error {
	err := q.InsertChronicleBlob(ctx, sqlitedb.InsertChronicleBlobParams{ProjectID: projectID, ObjectKey: key, Body: body, CreatedAt: nowMillis()})
	if isForeignKeyViolation(err) {
		return domain.NotFound("Project", projectID)
	}
	if err != nil {
		return fmt.Errorf("store: put chronicle body: %w", err)
	}
	return nil
}

// dropChronicleBody deletes the body under key, which a pointer of the page stopped naming, unless
// it is one of keep (the page's pointers after the write) or a version names it.
func dropChronicleBody(ctx context.Context, q sqlQueries, projectID, pageID string, key *string, keep ...*string) error {
	if key == nil {
		return nil
	}
	for _, k := range keep {
		if k != nil && *k == *key {
			return nil
		}
	}
	n, err := q.CountChronicleVersionsNaming(ctx, sqlitedb.CountChronicleVersionsNamingParams{ProjectID: projectID, PageID: pageID, BlocksObject: *key})
	if err != nil {
		return fmt.Errorf("store: chronicle body references: %w", err)
	}
	if n > 0 {
		return nil
	}
	if _, err := q.DeleteChronicleBlob(ctx, sqlitedb.DeleteChronicleBlobParams{ProjectID: projectID, ObjectKey: *key}); err != nil {
		return fmt.Errorf("store: drop chronicle body: %w", err)
	}
	return nil
}

func appendChronicleEvent(ctx context.Context, q sqlQueries, projectID, actor string, agent *string, typ domain.EventType, data map[string]any) error {
	err := q.AppendChronicleEvent(ctx, sqlitedb.AppendChronicleEventParams{
		ProjectID: projectID, ID: newID("E-", 24), Actor: actor, Agent: nullStr(agent), Type: string(typ), Data: jsonText(data), Ts: nowMillis(),
	})
	if err != nil {
		return fmt.Errorf("store: chronicle event: %w", err)
	}
	return nil
}

func upsertChroniclePage(ctx context.Context, e engine, in UpsertChroniclePageInput) (UpsertedChroniclePage, error) {
	switch {
	case !slices.Contains(domain.ChroniclePageKinds(), in.Kind):
		return UpsertedChroniclePage{}, domain.Validation("unknown chronicle page kind: "+string(in.Kind), map[string]any{"kind": in.Kind})
	case in.Slug == "" || in.Title == "":
		return UpsertedChroniclePage{}, domain.Validation("a chronicle page needs a slug and a title", nil)
	}
	if err := validChronicleWrite(in.Source, in.Author); err != nil {
		return UpsertedChroniclePage{}, err
	}
	pageID := domain.ChroniclePageID(in.Slug)
	var out UpsertedChroniclePage
	err := e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		existing, err := getChroniclePage(ctx, q, in.ProjectID, pageID)
		if err == nil {
			out, err = refreshChroniclePage(ctx, q, existing, in)
			return err
		}
		if be, ok := domain.AsBatonDeckError(err); !ok || be.Code != domain.CodeNotFound {
			return err
		}
		body := domain.ChronicleBody(in.Blocks)
		key := ChronicleObjectKey(pageID, body)
		var baseBlocks []domain.ChronicleBlock
		if in.Source == domain.SourceSweep {
			baseBlocks = in.Blocks
		}
		baseBody := domain.ChronicleBody(baseBlocks)
		baseKey := ChronicleObjectKey(pageID, baseBody)
		versionID := newID("CV-", 12)
		now := nowMillis()
		fields := domain.NextRecordFields(nil, in.Record)
		byline := &ChronicleByline{Author: in.Author, Agent: in.Agent}
		n, err := q.InsertChroniclePage(ctx, sqlitedb.InsertChroniclePageParams{
			ProjectID: in.ProjectID, ID: pageID, Kind: string(in.Kind), Slug: in.Slug, Title: in.Title, HeadVersionID: versionID,
			SourcePath: nullStr(in.SourcePath), LastDerivedObject: nullStr(&baseKey), Topics: jsonText(fields.Topics),
			Coverage: nullCoverage(fields.Coverage), SupersededBy: jsonText(fields.SupersededBy), UpdatedBy: nullJSON(byline), CreatedAt: now,
		})
		if isForeignKeyViolation(err) {
			return domain.NotFound("Project", in.ProjectID)
		}
		if err != nil {
			return fmt.Errorf("store: insert chronicle page: %w", err)
		}
		if n == 0 { // another writer of the slug committed first: this is now a refresh
			existing, err := getChroniclePage(ctx, q, in.ProjectID, pageID)
			if err != nil {
				return err
			}
			out, err = refreshChroniclePage(ctx, q, existing, in)
			return err
		}
		if err := putChronicleBody(ctx, q, in.ProjectID, key, body); err != nil {
			return err
		}
		if baseKey != key {
			if err := putChronicleBody(ctx, q, in.ProjectID, baseKey, baseBody); err != nil {
				return err
			}
		}
		if err := q.InsertChronicleVersion(ctx, sqlitedb.InsertChronicleVersionParams{
			ProjectID: in.ProjectID, PageID: pageID, ID: versionID, Source: string(in.Source), Author: in.Author,
			Agent: nullStr(in.Agent), Ts: now, BlocksObject: key, BlockCount: int64(len(in.Blocks)),
		}); err != nil {
			return fmt.Errorf("store: insert chronicle version: %w", err)
		}
		if err := appendChronicleEvent(ctx, q, in.ProjectID, in.Author, in.Agent, domain.EventChroniclePageCreated,
			map[string]any{"pageId": pageID, "slug": in.Slug, "kind": in.Kind}); err != nil {
			return err
		}
		page, err := getChroniclePage(ctx, q, in.ProjectID, pageID)
		out = UpsertedChroniclePage{Page: page, Created: true}
		return err
	})
	return out, err
}

// refreshChroniclePage is an upsert of an existing page: VALIDATION under RefuseExisting, else its
// kind, title, source path and record fields refreshed, with an event only when one changed.
func refreshChroniclePage(ctx context.Context, q sqlQueries, p ChroniclePage, in UpsertChroniclePageInput) (UpsertedChroniclePage, error) {
	if in.RefuseExisting {
		return UpsertedChroniclePage{}, domain.Validation("a Chronicle record with this slug already exists", map[string]any{"slug": in.Slug, "pageId": p.ID})
	}
	cur := domain.ChronicleRecordFields{Topics: p.Topics, Coverage: p.Coverage, SupersededBy: p.SupersededBy}
	next := domain.NextRecordFields(&cur, in.Record)
	samePath := (p.SourcePath == nil && in.SourcePath == nil) || (p.SourcePath != nil && in.SourcePath != nil && *p.SourcePath == *in.SourcePath)
	if p.Kind == in.Kind && p.Title == in.Title && samePath && domain.SameRecordFields(cur, next) {
		return UpsertedChroniclePage{Page: p}, nil
	}
	now := nowMillis()
	n, err := q.RefreshChroniclePage(ctx, sqlitedb.RefreshChroniclePageParams{
		Kind: string(in.Kind), Title: in.Title, SourcePath: nullStr(in.SourcePath), Topics: jsonText(next.Topics),
		Coverage: nullCoverage(next.Coverage), SupersededBy: jsonText(next.SupersededBy), UpdatedAt: now, ProjectID: p.ProjectID, ID: p.ID,
	})
	if err != nil {
		return UpsertedChroniclePage{}, fmt.Errorf("store: refresh chronicle page: %w", err)
	}
	if n == 0 {
		return UpsertedChroniclePage{}, domain.NewError(domain.CodeStale, "Chronicle page is being deleted — retry", map[string]any{"pageId": p.ID})
	}
	if err := appendChronicleEvent(ctx, q, p.ProjectID, in.Author, in.Agent, domain.EventChroniclePageUpdated,
		map[string]any{"pageId": p.ID, "kind": in.Kind, "title": in.Title}); err != nil {
		return UpsertedChroniclePage{}, err
	}
	p.Kind, p.Title, p.SourcePath = in.Kind, in.Title, in.SourcePath
	p.Topics, p.Coverage, p.SupersededBy = next.Topics, next.Coverage, next.SupersededBy
	p.UpdatedAt = now
	return UpsertedChroniclePage{Page: p}, nil
}

func deleteChroniclePage(ctx context.Context, e engine, projectID, pageID, expected, actor string, agent *string) error {
	return e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		p, err := getChroniclePage(ctx, q, projectID, pageID)
		if err != nil {
			return err
		}
		n, err := q.DeleteChroniclePage(ctx, sqlitedb.DeleteChroniclePageParams{ProjectID: projectID, ID: pageID, ExpectedHeadVersionID: expected})
		if err != nil {
			return fmt.Errorf("store: delete chronicle page: %w", err)
		}
		if n == 0 {
			return lostChronicleHead(ctx, q, projectID, pageID, expected)
		}
		// The versions go with the page row (chronicle_versions' ON DELETE CASCADE); the bodies,
		// which no foreign key names, by their key range.
		from, to := ChroniclePageKeyRange(pageID)
		if _, err := q.DeleteChronicleBlobsByPage(ctx, sqlitedb.DeleteChronicleBlobsByPageParams{ProjectID: projectID, KeyFrom: from, KeyTo: to}); err != nil {
			return fmt.Errorf("store: delete chronicle bodies: %w", err)
		}
		return appendChronicleEvent(ctx, q, projectID, actor, agent, domain.EventChroniclePageDeleted,
			map[string]any{"pageId": pageID, "slug": p.Slug, "kind": p.Kind, "title": p.Title})
	})
}

func listChroniclePages(ctx context.Context, q sqlQueries, projectID string, kind domain.ChroniclePageKind, cursor string, limit int) (ChroniclePageList, error) {
	if kind != "" && !slices.Contains(domain.ChroniclePageKinds(), kind) {
		return ChroniclePageList{}, domain.Validation("unknown chronicle page kind: "+string(kind), map[string]any{"kind": kind})
	}
	after := ""
	if cursor != "" {
		slug, _, err := DecodeCursor(cursor)
		if err != nil {
			return ChroniclePageList{}, err
		}
		after = slug
	}
	limit = chronicleListLimit(limit)
	rows, err := q.ListChroniclePages(ctx, sqlitedb.ListChroniclePagesParams{ProjectID: projectID, Kind: string(kind), AfterSlug: after, PageLimit: int64(limit + 1)})
	if err != nil {
		return ChroniclePageList{}, fmt.Errorf("store: list chronicle pages: %w", err)
	}
	out := ChroniclePageList{Pages: []ChroniclePage{}}
	for i, r := range rows {
		if i == limit {
			last := out.Pages[limit-1]
			out.NextCursor = EncodeCursor(last.Slug, last.ID)
			break
		}
		p, err := pageFromRow(r)
		if err != nil {
			return ChroniclePageList{}, err
		}
		out.Pages = append(out.Pages, p)
	}
	return out, nil
}

func appendChronicleVersion(ctx context.Context, e engine, projectID, pageID string, in AppendChronicleVersionInput) (ChronicleVersion, error) {
	if err := validChronicleWrite(in.Source, in.Author); err != nil {
		return ChronicleVersion{}, err
	}
	if in.ExpectedParentVersionID == "" {
		return ChronicleVersion{}, domain.Validation("an appended version names its parent", nil)
	}
	var out ChronicleVersion
	err := e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		p, err := getChroniclePage(ctx, q, projectID, pageID)
		if err != nil {
			return err
		}
		if p.HeadVersionID != in.ExpectedParentVersionID {
			return domain.ChronicleStale(in.ExpectedParentVersionID, p.HeadVersionID)
		}
		body := domain.ChronicleBody(in.Blocks)
		key := ChronicleObjectKey(pageID, body)
		versionID := newID("CV-", 12)
		// A version is always later than its parent (UpdatedAt is at least the head's ts), so
		// (ts, id) order is the chain's order even when two land in one millisecond.
		now := max(nowMillis(), p.UpdatedAt+1)
		base := p.LastDerivedObject
		var derivedBody []byte
		var conflict *ChronicleConflict
		if in.HasDerived {
			derivedBody = domain.ChronicleBody(in.DerivedBlocks)
			dk := ChronicleObjectKey(pageID, derivedBody)
			base = &dk
			if len(in.Conflicts) > 0 {
				conflict = &ChronicleConflict{VersionID: versionID, Ts: now, Items: slices.Clone(in.Conflicts), TheirsObject: dk}
			}
		}
		n, err := q.MoveChronicleHead(ctx, sqlitedb.MoveChronicleHeadParams{
			HeadVersionID: versionID, LastDerivedObject: nullStr(base), Conflict: nullJSON(conflict),
			UpdatedBy: nullJSON(&ChronicleByline{Author: in.Author, Agent: in.Agent}), UpdatedAt: now,
			ProjectID: projectID, ID: pageID, ExpectedHeadVersionID: in.ExpectedParentVersionID,
		})
		if err != nil {
			return fmt.Errorf("store: move chronicle head: %w", err)
		}
		if n == 0 {
			return lostChronicleHead(ctx, q, projectID, pageID, in.ExpectedParentVersionID)
		}
		if err := putChronicleBody(ctx, q, projectID, key, body); err != nil {
			return err
		}
		if in.HasDerived && *base != key {
			if err := putChronicleBody(ctx, q, projectID, *base, derivedBody); err != nil {
				return err
			}
		}
		parent := in.ExpectedParentVersionID
		if err := q.InsertChronicleVersion(ctx, sqlitedb.InsertChronicleVersionParams{
			ProjectID: projectID, PageID: pageID, ID: versionID, ParentVersionID: nullStr(&parent), Source: string(in.Source),
			Author: in.Author, Agent: nullStr(in.Agent), Ts: now, BlocksObject: key, BlockCount: int64(len(in.Blocks)),
		}); err != nil {
			return fmt.Errorf("store: insert chronicle version: %w", err)
		}
		data := map[string]any{"pageId": pageID, "versionId": versionID, "source": in.Source}
		if conflict != nil {
			ids := make([]string, len(conflict.Items))
			for i, it := range conflict.Items {
				ids[i] = it.BlockID
			}
			data["conflicts"] = ids
		}
		if err := appendChronicleEvent(ctx, q, projectID, in.Author, in.Agent, domain.EventChronicleVersionAdded, data); err != nil {
			return err
		}
		var newTheirs *string
		if conflict != nil {
			newTheirs = &conflict.TheirsObject
		}
		if in.HasDerived {
			if err := dropChronicleBody(ctx, q, projectID, pageID, p.LastDerivedObject, base, newTheirs); err != nil {
				return err
			}
		}
		if p.Conflict != nil {
			if err := dropChronicleBody(ctx, q, projectID, pageID, &p.Conflict.TheirsObject, base, newTheirs); err != nil {
				return err
			}
		}
		out = ChronicleVersion{
			ProjectID: projectID, PageID: pageID, ID: versionID, ParentVersionID: &parent, Source: in.Source, Author: in.Author,
			Agent: in.Agent, Ts: now, BlocksObject: key, BlockCount: int64(len(in.Blocks)), Blocks: domain.WithComputedProvenance(in.Blocks),
		}
		return nil
	})
	return out, err
}

func recordChronicleConflicts(ctx context.Context, e engine, projectID, pageID string, in RecordChronicleConflictsInput) error {
	if in.Actor == "" || in.VersionID == "" {
		return domain.Validation("recorded conflicts name their head and their actor", nil)
	}
	return e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		p, err := getChroniclePage(ctx, q, projectID, pageID)
		if err != nil {
			return err
		}
		if p.HeadVersionID != in.VersionID {
			return domain.ChronicleStale(in.VersionID, p.HeadVersionID)
		}
		body := domain.ChronicleBody(in.Theirs)
		key := ChronicleObjectKey(pageID, body)
		now := nowMillis()
		conflict := &ChronicleConflict{VersionID: in.VersionID, Ts: now, Items: slices.Clone(in.Items), TheirsObject: key}
		if conflict.Items == nil {
			conflict.Items = []domain.ChronicleConflictItem{}
		}
		n, err := q.SetChronicleConflict(ctx, sqlitedb.SetChronicleConflictParams{
			Conflict: nullJSON(conflict), UpdatedAt: now, ProjectID: projectID, ID: pageID, ExpectedHeadVersionID: in.VersionID,
		})
		if err != nil {
			return fmt.Errorf("store: set chronicle conflict: %w", err)
		}
		if n == 0 {
			return lostChronicleHead(ctx, q, projectID, pageID, in.VersionID)
		}
		if err := putChronicleBody(ctx, q, projectID, key, body); err != nil {
			return err
		}
		ids := make([]string, len(conflict.Items))
		for i, it := range conflict.Items {
			ids[i] = it.BlockID
		}
		if err := appendChronicleEvent(ctx, q, projectID, in.Actor, in.Agent, domain.EventChronicleConflictsFlagged,
			map[string]any{"pageId": pageID, "versionId": in.VersionID, "blocks": ids}); err != nil {
			return err
		}
		if p.Conflict != nil {
			return dropChronicleBody(ctx, q, projectID, pageID, &p.Conflict.TheirsObject, &key, p.LastDerivedObject)
		}
		return nil
	})
}

type chroniclePointer int

const (
	basePointer chroniclePointer = iota
	conflictPointer
)

func readChronicleBody(ctx context.Context, q sqlQueries, projectID, key string) ([]domain.ChronicleBlock, error) {
	r, err := q.GetChronicleBlob(ctx, sqlitedb.GetChronicleBlobParams{ProjectID: projectID, ObjectKey: key})
	if err := found(err, "ChronicleObject", key); err != nil {
		return nil, err
	}
	var blocks []domain.ChronicleBlock
	if err := json.Unmarshal(r.Body, &blocks); err != nil {
		return nil, fmt.Errorf("store: chronicle body %s: %w", key, err)
	}
	if blocks == nil {
		blocks = []domain.ChronicleBlock{}
	}
	return blocks, nil
}

func chroniclePointedBody(ctx context.Context, q sqlQueries, projectID, pageID string, which chroniclePointer) ([]domain.ChronicleBlock, bool, error) {
	p, err := getChroniclePage(ctx, q, projectID, pageID)
	if err != nil {
		return nil, false, err
	}
	var key *string
	switch which {
	case basePointer:
		key = p.LastDerivedObject
	case conflictPointer:
		if p.Conflict != nil {
			key = &p.Conflict.TheirsObject
		}
	}
	if key == nil {
		return nil, false, nil
	}
	blocks, err := readChronicleBody(ctx, q, projectID, *key)
	return blocks, err == nil, err
}

func listChronicleObjects(ctx context.Context, q sqlQueries, projectID string) ([]string, error) {
	keys, err := q.ListChronicleObjects(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: list chronicle objects: %w", err)
	}
	if keys == nil {
		keys = []string{}
	}
	return keys, nil
}

func getChronicleVersion(ctx context.Context, q sqlQueries, projectID, pageID, versionID string) (ChronicleVersion, error) {
	r, err := q.GetChronicleVersion(ctx, sqlitedb.GetChronicleVersionParams{ProjectID: projectID, PageID: pageID, ID: versionID})
	if err := found(err, "ChronicleVersion", versionID); err != nil {
		return ChronicleVersion{}, err
	}
	v := versionFromRow(r)
	if v.Blocks, err = readChronicleBody(ctx, q, projectID, v.BlocksObject); err != nil {
		return ChronicleVersion{}, err
	}
	return v, nil
}

func listChronicleVersions(ctx context.Context, q sqlQueries, projectID, pageID, cursor string, limit int) (ChronicleVersionList, error) {
	if _, err := getChroniclePage(ctx, q, projectID, pageID); err != nil {
		return ChronicleVersionList{}, err
	}
	afterTs, afterID, err := tsCursor(cursor)
	if err != nil {
		return ChronicleVersionList{}, err
	}
	limit = chronicleListLimit(limit)
	rows, err := q.ListChronicleVersions(ctx, sqlitedb.ListChronicleVersionsParams{
		ProjectID: projectID, PageID: pageID, AfterID: afterID, AfterTs: afterTs, PageLimit: int64(limit + 1),
	})
	if err != nil {
		return ChronicleVersionList{}, fmt.Errorf("store: list chronicle versions: %w", err)
	}
	out := ChronicleVersionList{Versions: []ChronicleVersion{}}
	for i, r := range rows {
		if i == limit {
			last := out.Versions[limit-1]
			out.NextCursor = EncodeCursor(strconv.FormatInt(last.Ts, 10), last.ID)
			break
		}
		out.Versions = append(out.Versions, versionFromRow(r))
	}
	return out, nil
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) GetChroniclePage(ctx context.Context, arg sqlitedb.GetChroniclePageParams) (sqlitedb.ChroniclePage, error) {
	r, err := p.q.GetChroniclePage(ctx, pgdb.GetChroniclePageParams(arg))
	return sqlitedb.ChroniclePage(r), err
}

func (p pgQueries) InsertChroniclePage(ctx context.Context, arg sqlitedb.InsertChroniclePageParams) (int64, error) {
	return p.q.InsertChroniclePage(ctx, pgdb.InsertChroniclePageParams(arg))
}

func (p pgQueries) RefreshChroniclePage(ctx context.Context, arg sqlitedb.RefreshChroniclePageParams) (int64, error) {
	return p.q.RefreshChroniclePage(ctx, pgdb.RefreshChroniclePageParams(arg))
}

func (p pgQueries) MoveChronicleHead(ctx context.Context, arg sqlitedb.MoveChronicleHeadParams) (int64, error) {
	return p.q.MoveChronicleHead(ctx, pgdb.MoveChronicleHeadParams(arg))
}

func (p pgQueries) SetChronicleConflict(ctx context.Context, arg sqlitedb.SetChronicleConflictParams) (int64, error) {
	return p.q.SetChronicleConflict(ctx, pgdb.SetChronicleConflictParams(arg))
}

func (p pgQueries) DeleteChroniclePage(ctx context.Context, arg sqlitedb.DeleteChroniclePageParams) (int64, error) {
	return p.q.DeleteChroniclePage(ctx, pgdb.DeleteChroniclePageParams(arg))
}

func (p pgQueries) ListChroniclePages(ctx context.Context, arg sqlitedb.ListChroniclePagesParams) ([]sqlitedb.ChroniclePage, error) {
	rows, err := p.q.ListChroniclePages(ctx, pgdb.ListChroniclePagesParams(arg))
	return convert(rows, func(r pgdb.ChroniclePage) sqlitedb.ChroniclePage { return sqlitedb.ChroniclePage(r) }), err
}

func (p pgQueries) InsertChronicleVersion(ctx context.Context, arg sqlitedb.InsertChronicleVersionParams) error {
	return p.q.InsertChronicleVersion(ctx, pgdb.InsertChronicleVersionParams(arg))
}

func (p pgQueries) GetChronicleVersion(ctx context.Context, arg sqlitedb.GetChronicleVersionParams) (sqlitedb.ChronicleVersion, error) {
	r, err := p.q.GetChronicleVersion(ctx, pgdb.GetChronicleVersionParams(arg))
	return sqlitedb.ChronicleVersion(r), err
}

func (p pgQueries) ListChronicleVersions(ctx context.Context, arg sqlitedb.ListChronicleVersionsParams) ([]sqlitedb.ChronicleVersion, error) {
	rows, err := p.q.ListChronicleVersions(ctx, pgdb.ListChronicleVersionsParams(arg))
	return convert(rows, func(r pgdb.ChronicleVersion) sqlitedb.ChronicleVersion { return sqlitedb.ChronicleVersion(r) }), err
}

func (p pgQueries) InsertChronicleBlob(ctx context.Context, arg sqlitedb.InsertChronicleBlobParams) error {
	return p.q.InsertChronicleBlob(ctx, pgdb.InsertChronicleBlobParams(arg))
}

func (p pgQueries) GetChronicleBlob(ctx context.Context, arg sqlitedb.GetChronicleBlobParams) (sqlitedb.ChronicleBlob, error) {
	r, err := p.q.GetChronicleBlob(ctx, pgdb.GetChronicleBlobParams(arg))
	return sqlitedb.ChronicleBlob(r), err
}

func (p pgQueries) DeleteChronicleBlobsByPage(ctx context.Context, arg sqlitedb.DeleteChronicleBlobsByPageParams) (int64, error) {
	return p.q.DeleteChronicleBlobsByPage(ctx, pgdb.DeleteChronicleBlobsByPageParams(arg))
}

func (p pgQueries) CountChronicleVersionsNaming(ctx context.Context, arg sqlitedb.CountChronicleVersionsNamingParams) (int64, error) {
	return p.q.CountChronicleVersionsNaming(ctx, pgdb.CountChronicleVersionsNamingParams(arg))
}

func (p pgQueries) DeleteChronicleBlob(ctx context.Context, arg sqlitedb.DeleteChronicleBlobParams) (int64, error) {
	return p.q.DeleteChronicleBlob(ctx, pgdb.DeleteChronicleBlobParams(arg))
}

func (p pgQueries) ListChronicleObjects(ctx context.Context, projectID string) ([]string, error) {
	return p.q.ListChronicleObjects(ctx, projectID)
}

func (p pgQueries) AppendChronicleEvent(ctx context.Context, arg sqlitedb.AppendChronicleEventParams) error {
	return p.q.AppendChronicleEvent(ctx, pgdb.AppendChronicleEventParams(arg))
}
