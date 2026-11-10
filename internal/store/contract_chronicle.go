package store

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// ChronicleByline is who committed a page's head version.
type ChronicleByline struct {
	Author string  `json:"author"`
	Agent  *string `json:"agent"`
}

// ChronicleConflict is a page's standing unresolved conflicts: the head they were found against,
// when, the items, and the body of the conflicting derivation (TheirsObject).
type ChronicleConflict struct {
	VersionID    string                         `json:"versionId"`
	Ts           int64                          `json:"ts"`
	Items        []domain.ChronicleConflictItem `json:"items"`
	TheirsObject string                         `json:"theirsObject"`
}

// ChroniclePage is a Chronicle record. ID is domain.ChroniclePageID(Slug). HeadVersionID always
// names a version. LastDerivedObject is the merge base's body (a human-created page's is the empty
// list); Conflict is nil when none stands. Times are Unix milliseconds.
type ChroniclePage struct {
	ProjectID         string
	ID                string
	Kind              domain.ChroniclePageKind
	Slug              string
	Title             string
	HeadVersionID     string
	SourcePath        *string
	LastDerivedObject *string
	Conflict          *ChronicleConflict
	Topics            []string
	Coverage          *domain.ChronicleCoverage
	SupersededBy      []string
	UpdatedBy         *ChronicleByline
	CreatedAt         int64
	UpdatedAt         int64
}

// ChronicleVersion is one immutable version of a page. Blocks is the body, filled by
// GetChronicleVersion and AppendChronicleVersion and nil in a list (BlockCount counts it there).
type ChronicleVersion struct {
	ProjectID       string
	PageID          string
	ID              string
	ParentVersionID *string
	Source          domain.ChronicleSource
	Author          string
	Agent           *string
	Ts              int64
	BlocksObject    string
	BlockCount      int64
	Blocks          []domain.ChronicleBlock
}

// UpsertChroniclePageInput is a page to create with its first version, or whose metadata to
// refresh. A block's State is ignored (recomputed). Record is the record fields, absent leaving the
// stored value alone (domain.NextRecordFields). RefuseExisting makes a taken slug VALIDATION
// instead of a refresh.
type UpsertChroniclePageInput struct {
	ProjectID      string
	Kind           domain.ChroniclePageKind
	Slug           string
	Title          string
	SourcePath     *string
	Blocks         []domain.ChronicleBlock
	Source         domain.ChronicleSource
	Author         string
	Agent          *string
	Record         domain.ChronicleRecordFieldsInput
	RefuseExisting bool
}

// UpsertedChroniclePage is the page after an upsert and whether this call created it.
type UpsertedChroniclePage struct {
	Page    ChroniclePage
	Created bool
}

// AppendChronicleVersionInput is a version to append from ExpectedParentVersionID. HasDerived marks
// a sweep's write: DerivedBlocks becomes the page's merge base and Conflicts its conflict state (set
// when non-empty, cleared when empty); a human write (HasDerived false) clears any standing conflict
// and settles it: the conflict's derivation becomes the base (with none standing, the base stays).
type AppendChronicleVersionInput struct {
	ExpectedParentVersionID string
	Blocks                  []domain.ChronicleBlock
	Source                  domain.ChronicleSource
	Author                  string
	Agent                   *string
	HasDerived              bool
	DerivedBlocks           []domain.ChronicleBlock
	Conflicts               []domain.ChronicleConflictItem
}

// RecordChronicleConflictsInput records conflicts found against head VersionID with no version
// written: Theirs is the conflicting derivation.
type RecordChronicleConflictsInput struct {
	VersionID string
	Items     []domain.ChronicleConflictItem
	Theirs    []domain.ChronicleBlock
	Actor     string
	Agent     *string
}

// ChroniclePageList is one page of pages by slug; NextCursor is "" on the last.
type ChroniclePageList struct {
	Pages      []ChroniclePage
	NextCursor string
}

// ChronicleVersionList is one page of versions, newest first, bodies not loaded; NextCursor is "" on
// the last.
type ChronicleVersionList struct {
	Versions   []ChronicleVersion
	NextCursor string
}

// Chronicle is the Chronicle area (conductor store.ts:1824-2021, the eleven methods of
// `// --- chronicle (CHS-1) ---`). Keys: a page is (project_id, id), a version (project_id, page_id,
// id), a body (project_id, "<pageId>/<sha256 of the body>"); nothing is keyed by a page id alone.
// Every write appends its chronicle.* event (no board, no task) in the same transaction. Errors: a
// missing project is domain.NotFound("Project", id), a missing page domain.NotFound("ChroniclePage",
// id), a missing version domain.NotFound("ChronicleVersion", id); a head that moved is
// domain.ChronicleStale; an unknown kind or source, an empty slug, title or author, and a taken slug
// under RefuseExisting are domain.Validation. A list limit <= 0 is 50, above 200 is 200.
type Chronicle interface {
	// UpsertChroniclePage creates the page and its first version (a sweep's merge base is its blocks,
	// a human's the empty list), or refreshes an existing page's kind, title, source path and record
	// fields (an event only when one changed); the head, base and conflict never move here.
	UpsertChroniclePage(ctx context.Context, in UpsertChroniclePageInput) (UpsertedChroniclePage, error)
	// GetChroniclePage is the page.
	GetChroniclePage(ctx context.Context, projectID, pageID string) (ChroniclePage, error)
	// DeleteChroniclePage deletes the page, every version and every body of it, in one transaction.
	// expectedHeadVersionID "" is unconditional; else a moved head is STALE and nothing is deleted.
	DeleteChroniclePage(ctx context.Context, projectID, pageID, expectedHeadVersionID, actor string, agent *string) error
	// ListChroniclePages is one page of the project's pages by slug; kind "" is every kind.
	ListChroniclePages(ctx context.Context, projectID string, kind domain.ChroniclePageKind, cursor string, limit int) (ChroniclePageList, error)
	// AppendChronicleVersion appends an immutable version and moves the head to it, only from
	// ExpectedParentVersionID (else STALE). Provenance is recomputed.
	AppendChronicleVersion(ctx context.Context, projectID, pageID string, in AppendChronicleVersionInput) (ChronicleVersion, error)
	// RecordChronicleConflicts sets the page's conflict state (replacing any) against its head,
	// which must be in.VersionID (else STALE).
	RecordChronicleConflicts(ctx context.Context, projectID, pageID string, in RecordChronicleConflictsInput) error
	// GetChronicleConflictTheirs is the conflicting derivation of the standing conflict; ok is false
	// when none stands.
	GetChronicleConflictTheirs(ctx context.Context, projectID, pageID string) (blocks []domain.ChronicleBlock, ok bool, err error)
	// GetChronicleBase is the page's merge base; ok is false when it has none.
	GetChronicleBase(ctx context.Context, projectID, pageID string) (blocks []domain.ChronicleBlock, ok bool, err error)
	// ListChronicleObjects is every body key of the project, sorted: an orphan is otherwise invisible.
	ListChronicleObjects(ctx context.Context, projectID string) ([]string, error)
	// GetChronicleVersion is the version with its body.
	GetChronicleVersion(ctx context.Context, projectID, pageID, versionID string) (ChronicleVersion, error)
	// ListChronicleVersions is one page of the page's versions, newest first ((ts, id) descending).
	ListChronicleVersions(ctx context.Context, projectID, pageID, cursor string, limit int) (ChronicleVersionList, error)
}

var (
	_ Chronicle = (*SQLite)(nil)
	_ Chronicle = (*Postgres)(nil)
)
