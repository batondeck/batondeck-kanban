package service

import (
	"context"
	"encoding/json"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Chronicle is the Chronicle's four tools and its four reads (the chronicle resources, and R2.7's
// GET routes). Every write is tier write and grant-governed; every read is the project's read tier
// (get_project's posture). A page's version is its headVersionId: the STALE checks compare it, and
// there is no integer page version.
type Chronicle interface {
	// CreatePage is create_chronicle_page: a hand-started page, every block authored.
	// Tier: write. Event: chronicle.page_created. Audit: none. Doorbell: none. Replays on idempotencyKey.
	CreatePage(ctx context.Context, caller domain.Caller, req CreateChroniclePageReq) (CreateChroniclePageResp, error)

	// IngestPage is ingest_chronicle_page: create on first sight, else the three-way merge.
	// Tier: write. Event: chronicle.page_created, or chronicle.page_updated, chronicle.version_added
	// or chronicle.conflicts_flagged. Audit: none. Doorbell: none. Replays on idempotencyKey.
	IngestPage(ctx context.Context, caller domain.Caller, req IngestChroniclePageReq) (IngestChroniclePageResp, error)

	// EditPage is edit_chronicle_page: a new immutable version from expectedParentVersionId.
	// Tier: write. Event: chronicle.version_added. Audit: none. Doorbell: none. Replays on idempotencyKey.
	EditPage(ctx context.Context, caller domain.Caller, req EditChroniclePageReq) (EditChroniclePageResp, error)

	// DeletePage is delete_chronicle_page: the page, its versions and its bodies, irreversibly.
	// Tier: write. Event: chronicle.page_deleted. Audit: chronicle.page_deleted. Doorbell: none.
	DeletePage(ctx context.Context, caller domain.Caller, req DeleteChroniclePageReq) (DeleteChroniclePageResp, error)

	// ListPages is the chronicle resource: 100 pages by slug, then nextCursor. Tier: read.
	ListPages(ctx context.Context, caller domain.Caller, req ListChroniclePagesReq) (ChronicleIndex, error)
	// GetPage is the chronicle-page resource: the page, its head with blocks, the drifted block ids
	// and the standing conflicts with both sides; a missing page is {page: null, head: null}. Tier: read.
	GetPage(ctx context.Context, caller domain.Caller, req GetChroniclePageReq) (ChroniclePageView, error)
	// ListVersions is the chronicle-history resource: 50 versions newest first, metadata only; a
	// missing page has none. Tier: read.
	ListVersions(ctx context.Context, caller domain.Caller, req ListChronicleVersionsReq) (ChronicleHistory, error)
	// GetVersion is the chronicle-version resource: one version with its blocks, null when missing.
	// Tier: read.
	GetVersion(ctx context.Context, caller domain.Caller, req GetChronicleVersionReq) (ChronicleVersionView, error)
}

// ChronicleBlockInput is one block a caller writes: no state (provenance is computed), and an origin
// only a sweep's is kept from.
type ChronicleBlockInput struct {
	Citations []domain.ChronicleCitation `json:"citations,omitzero"`
	ID        string                     `json:"id"`
	Origin    *domain.BlockOrigin        `json:"origin,omitzero"`
	Text      string                     `json:"text"`
}

// CreateChroniclePageReq is create_chronicle_page's input.
type CreateChroniclePageReq struct {
	Blocks         []ChronicleBlockInput    `json:"blocks"`
	Kind           domain.ChroniclePageKind `json:"kind"`
	ProjectID      string                   `json:"projectId"`
	Slug           string                   `json:"slug"`
	Title          string                   `json:"title"`
	IdempotencyKey string                   `json:"idempotencyKey,omitzero"`
}

// CreateChroniclePageResp is create_chronicle_page's output.
type CreateChroniclePageResp struct {
	PageID    string `json:"pageId"`
	VersionID string `json:"versionId"`
}

// IngestChroniclePageReq is ingest_chronicle_page's input. Topics and SupersededBy absent (nil)
// leave the stored value alone and [] clears it; Coverage absent leaves it, null clears it.
type IngestChroniclePageReq struct {
	Blocks         []ChronicleBlockInput              `json:"blocks"`
	Kind           domain.ChroniclePageKind           `json:"kind"`
	ProjectID      string                             `json:"projectId"`
	Slug           string                             `json:"slug"`
	Title          string                             `json:"title"`
	Coverage       Nullable[domain.ChronicleCoverage] `json:"coverage,omitzero"`
	IdempotencyKey string                             `json:"idempotencyKey,omitzero"`
	SourcePath     *string                            `json:"sourcePath,omitzero"`
	SupersededBy   []string                           `json:"supersededBy,omitzero"`
	Topics         []string                           `json:"topics,omitzero"`
}

// IngestChroniclePageResp is ingest_chronicle_page's output.
type IngestChroniclePageResp struct {
	Conflicts []string `json:"conflicts"`
	Created   bool     `json:"created"`
	PageID    string   `json:"pageId"`
	Unchanged bool     `json:"unchanged"`
	VersionID string   `json:"versionId"`
}

// EditChroniclePageReq is edit_chronicle_page's input.
type EditChroniclePageReq struct {
	Blocks                  []ChronicleBlockInput `json:"blocks"`
	ExpectedParentVersionID string                `json:"expectedParentVersionId"`
	PageID                  string                `json:"pageId"`
	ProjectID               string                `json:"projectId"`
	IdempotencyKey          string                `json:"idempotencyKey,omitzero"`
}

// EditChroniclePageResp is edit_chronicle_page's output: drifted is the blocks now
// derived-then-edited.
type EditChroniclePageResp struct {
	Drifted   []string `json:"drifted"`
	VersionID string   `json:"versionId"`
}

// DeleteChroniclePageReq is delete_chronicle_page's input: expectedHeadVersionId absent deletes
// whatever the head.
type DeleteChroniclePageReq struct {
	PageID                string `json:"pageId"`
	ProjectID             string `json:"projectId"`
	ExpectedHeadVersionID string `json:"expectedHeadVersionId,omitzero"`
}

// DeleteChroniclePageResp is delete_chronicle_page's output.
type DeleteChroniclePageResp struct {
	Ok True `json:"ok"`
}

// ListChroniclePagesReq is the chronicle resource's read.
type ListChroniclePagesReq struct {
	ProjectID string
	Cursor    string
}

// GetChroniclePageReq is the chronicle-page resource's read.
type GetChroniclePageReq struct {
	ProjectID string
	PageID    string
}

// ListChronicleVersionsReq is the chronicle-history resource's read.
type ListChronicleVersionsReq struct {
	ProjectID string
	PageID    string
	Cursor    string
}

// GetChronicleVersionReq is the chronicle-version resource's read.
type GetChronicleVersionReq struct {
	ProjectID string
	PageID    string
	VersionID string
}

// ChronicleByline is who committed a page's head.
type ChronicleByline struct {
	Author string  `json:"author"`
	Agent  *string `json:"agent"`
}

// ChronicleConflictState is a page's standing conflicts as stored (conductor's ChronicleConflictState).
type ChronicleConflictState struct {
	VersionID    string                         `json:"versionId"`
	Ts           string                         `json:"ts"`
	Items        []domain.ChronicleConflictItem `json:"items"`
	TheirsObject string                         `json:"theirsObject"`
}

// ChroniclePage is a page as the reads return it (conductor's ChroniclePage).
type ChroniclePage struct {
	ID                string                    `json:"id"`
	ProjectID         string                    `json:"projectId"`
	Kind              domain.ChroniclePageKind  `json:"kind"`
	Slug              string                    `json:"slug"`
	Title             string                    `json:"title"`
	HeadVersionID     string                    `json:"headVersionId"`
	SourcePath        *string                   `json:"sourcePath"`
	LastDerivedObject *string                   `json:"lastDerivedObject"`
	Conflict          *ChronicleConflictState   `json:"conflict"`
	Topics            []string                  `json:"topics"`
	Coverage          *domain.ChronicleCoverage `json:"coverage"`
	SupersededBy      []string                  `json:"supersededBy"`
	UpdatedBy         *ChronicleByline          `json:"updatedBy"`
	CreatedAt         string                    `json:"createdAt"`
	UpdatedAt         string                    `json:"updatedAt"`
}

// ChronicleVersion is a version as the reads return it; Blocks is [] in a history.
type ChronicleVersion struct {
	ID              string                  `json:"id"`
	PageID          string                  `json:"pageId"`
	ParentVersionID *string                 `json:"parentVersionId"`
	Source          domain.ChronicleSource  `json:"source"`
	Author          string                  `json:"author"`
	Agent           *string                 `json:"agent"`
	Ts              string                  `json:"ts"`
	Blocks          []domain.ChronicleBlock `json:"blocks"`
	BlocksObject    string                  `json:"blocksObject"`
	BlockCount      int64                   `json:"blockCount"`
}

// ChronicleVersionMeta is one row of a history: a version without its body.
type ChronicleVersionMeta struct {
	ID              string                 `json:"id"`
	ParentVersionID *string                `json:"parentVersionId"`
	Source          domain.ChronicleSource `json:"source"`
	Author          string                 `json:"author"`
	Agent           *string                `json:"agent"`
	Ts              string                 `json:"ts"`
	BlockCount      int64                  `json:"blockCount"`
}

// ChronicleIndex is the chronicle resource: nextCursor present means more pages exist.
type ChronicleIndex struct {
	Pages      []ChroniclePage `json:"pages"`
	NextCursor string          `json:"nextCursor,omitzero"`
}

// ChronicleConflictView is one standing conflict with the derivation's side (theirs: null where the
// alternative is an absence).
type ChronicleConflictView struct {
	BlockID string                  `json:"blockId"`
	Kind    domain.MergeDisposition `json:"kind"`
	Theirs  *domain.ChronicleBlock  `json:"theirs"`
}

// ChronicleConflictsView is a page's standing conflicts as the page read shows them.
type ChronicleConflictsView struct {
	VersionID string                  `json:"versionId"`
	Ts        string                  `json:"ts"`
	Items     []ChronicleConflictView `json:"items"`
}

// ChroniclePageView is the chronicle-page resource. Page nil is a missing page, written as
// {"page":null,"head":null} as conductor does.
type ChroniclePageView struct {
	Page      *ChroniclePage
	Head      *ChronicleVersion
	Drifted   []string
	Conflicts *ChronicleConflictsView
}

func (v ChroniclePageView) MarshalJSON() ([]byte, error) {
	if v.Page == nil {
		return []byte(`{"page":null,"head":null}`), nil
	}
	drifted := v.Drifted
	if drifted == nil {
		drifted = []string{}
	}
	return json.Marshal(struct {
		Page      *ChroniclePage          `json:"page"`
		Head      *ChronicleVersion       `json:"head"`
		Drifted   []string                `json:"drifted"`
		Conflicts *ChronicleConflictsView `json:"conflicts"`
	}{v.Page, v.Head, drifted, v.Conflicts})
}

// ChronicleHistory is the chronicle-history resource: nextCursor present means older versions exist.
type ChronicleHistory struct {
	NextCursor string                 `json:"nextCursor,omitzero"`
	Versions   []ChronicleVersionMeta `json:"versions"`
}

// ChronicleVersionView is the chronicle-version resource: version null when missing.
type ChronicleVersionView struct {
	Version *ChronicleVersion `json:"version"`
}
