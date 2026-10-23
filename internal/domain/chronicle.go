package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"unicode"
)

// The Chronicle's pure rules (conductor src/domain/chronicle.ts): a page's id from its slug, a
// block's provenance from its text and origin, and the record fields' absent-versus-empty rule.
// The three-way merge is chronicle_merge.go.

// ChroniclePageKind is what a Chronicle page records.
type ChroniclePageKind string

const (
	ChronicleADR      ChroniclePageKind = "adr"
	ChronicleTopic    ChroniclePageKind = "topic"
	ChronicleFeature  ChroniclePageKind = "feature"
	ChronicleDiagram  ChroniclePageKind = "diagram"
	ChronicleContract ChroniclePageKind = "contract"
)

// ChroniclePageKinds are the kinds, in the schema's order.
func ChroniclePageKinds() []ChroniclePageKind {
	return []ChroniclePageKind{ChronicleADR, ChronicleTopic, ChronicleFeature, ChronicleDiagram, ChronicleContract}
}

// ChronicleCoverage is how much evidence a generated record had.
type ChronicleCoverage string

const (
	CoverageFull    ChronicleCoverage = "full"
	CoveragePartial ChronicleCoverage = "partial"
)

// ChronicleCoverages are the coverages, in the schema's order.
func ChronicleCoverages() []ChronicleCoverage {
	return []ChronicleCoverage{CoverageFull, CoveragePartial}
}

// ChronicleSource is who committed a version: the sweep (a generator) or a human.
type ChronicleSource string

const (
	SourceSweep ChronicleSource = "sweep"
	SourceHuman ChronicleSource = "human"
)

// BlockState is a block's provenance, always computed (ResolveBlockState), never supplied.
type BlockState string

const (
	BlockDerived           BlockState = "derived"
	BlockAuthored          BlockState = "authored"
	BlockDerivedThenEdited BlockState = "derived-then-edited"
)

// ChronicleCitation points at one task by its full (project, board, task) triple: a task id alone is
// not unique even within one project's boards.
type ChronicleCitation struct {
	ProjectID string `json:"projectId"`
	BoardID   string `json:"boardId"`
	TaskID    string `json:"taskId"`
	Label     string `json:"label,omitzero"`
}

// BlockOrigin is the derivation a block claims: the record and the hash of the text it was derived as.
type BlockOrigin struct {
	AdrID     string `json:"adrId"`
	BlockHash string `json:"blockHash"`
}

// ChronicleBlock is one paragraph of a page, identified by an id stable across versions.
type ChronicleBlock struct {
	ID        string              `json:"id"`
	State     BlockState          `json:"state"`
	Text      string              `json:"text"`
	Citations []ChronicleCitation `json:"citations"`
	Origin    *BlockOrigin        `json:"origin"`
}

// trimJS is JavaScript's String.prototype.trim: Unicode white space and the BOM, from both ends.
func trimJS(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' })
}

// BlockHash is the hex SHA-256 of a block's trimmed text: whitespace at the ends is not an edit.
func BlockHash(text string) string {
	sum := sha256.Sum256([]byte(trimJS(text)))
	return hex.EncodeToString(sum[:])
}

// ChroniclePageID is a page's id, derived from its slug ("CP-" and 12 hex digits of its SHA-256),
// so re-ingesting a record addresses the same page and two writers of one slug collide on one row.
// Two projects recording the same slug share a page id: every lookup is keyed by project too.
func ChroniclePageID(slug string) string {
	sum := sha256.Sum256([]byte(slug))
	return "CP-" + hex.EncodeToString(sum[:])[:12]
}

// ResolveBlockState is a block's provenance: authored with no origin, derived while its text still
// hashes to the origin's, derived-then-edited once it does not.
func ResolveBlockState(text string, origin *BlockOrigin) BlockState {
	switch {
	case origin == nil:
		return BlockAuthored
	case BlockHash(text) == origin.BlockHash:
		return BlockDerived
	}
	return BlockDerivedThenEdited
}

// WithComputedProvenance is blocks with each state recomputed from its text and origin, whatever
// state it carried, and nil citations made empty: the one place a stored block's state comes from.
func WithComputedProvenance(blocks []ChronicleBlock) []ChronicleBlock {
	out := make([]ChronicleBlock, len(blocks))
	for i, b := range blocks {
		cites := slices.Clone(b.Citations)
		if cites == nil {
			cites = []ChronicleCitation{}
		}
		var origin *BlockOrigin
		if b.Origin != nil {
			o := *b.Origin
			origin = &o
		}
		out[i] = ChronicleBlock{ID: b.ID, State: ResolveBlockState(b.Text, origin), Text: b.Text, Citations: cites, Origin: origin}
	}
	return out
}

// ChronicleRecordFields are the generated record's frontmatter a page carries.
type ChronicleRecordFields struct {
	Topics       []string
	Coverage     *ChronicleCoverage
	SupersededBy []string
}

// ChronicleRecordFieldsInput is a write's record fields, where absent and empty differ: a nil slice
// or an unset coverage leaves the stored value alone, an empty slice or a set nil coverage clears it.
type ChronicleRecordFieldsInput struct {
	Topics       []string
	CoverageSet  bool
	Coverage     *ChronicleCoverage
	SupersededBy []string
}

// NextRecordFields is the record fields after a write of in over existing (nil: a new page).
func NextRecordFields(existing *ChronicleRecordFields, in ChronicleRecordFieldsInput) ChronicleRecordFields {
	var out ChronicleRecordFields
	if existing != nil {
		out = ChronicleRecordFields{Topics: slices.Clone(existing.Topics), Coverage: existing.Coverage, SupersededBy: slices.Clone(existing.SupersededBy)}
	}
	if in.Topics != nil {
		out.Topics = slices.Clone(in.Topics)
	}
	if in.CoverageSet {
		out.Coverage = in.Coverage
	}
	if in.SupersededBy != nil {
		out.SupersededBy = slices.Clone(in.SupersededBy)
	}
	if out.Topics == nil {
		out.Topics = []string{}
	}
	if out.SupersededBy == nil {
		out.SupersededBy = []string{}
	}
	return out
}

// SameRecordFields compares two records' fields by content.
func SameRecordFields(a, b ChronicleRecordFields) bool {
	sameCov := (a.Coverage == nil && b.Coverage == nil) || (a.Coverage != nil && b.Coverage != nil && *a.Coverage == *b.Coverage)
	return sameCov && slices.Equal(a.Topics, b.Topics) && slices.Equal(a.SupersededBy, b.SupersededBy)
}

// SameVisibleBlocks reports whether two block lists show a reader the same thing: ids in order,
// trimmed text, origin and citations, field by field. State is not compared: with equal text the
// origin decides it, and a placeholder state on an incoming block must not manufacture a version.
func SameVisibleBlocks(a, b []ChronicleBlock) bool {
	return slices.EqualFunc(a, b, func(x, y ChronicleBlock) bool {
		return x.ID == y.ID && trimJS(x.Text) == trimJS(y.Text) && sameOrigin(x.Origin, y.Origin) && slices.Equal(x.Citations, y.Citations)
	})
}

func sameOrigin(a, b *BlockOrigin) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
