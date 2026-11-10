package domain

// The Chronicle three-way merge (conductor src/domain/chronicle-merge.ts): a re-ingest is a merge
// of the new derivation into a page that may carry human edits, never an overwrite. base is the
// previous raw derivation (the page's merge base), ours the head, theirs the new derivation. Per
// block, by stable id. A regeneration never silently deletes human writing, and never silently
// resurrects what a human deleted.

// MergeDisposition is why a merged block ended up where it did.
type MergeDisposition string

const (
	// TakenTheirs: nobody edited it here, or ours already reads as theirs; the new derivation wins.
	TakenTheirs MergeDisposition = "taken-theirs"
	// KeptOurs: a human edited it and the derivation did not change it.
	KeptOurs MergeDisposition = "kept-ours"
	// MergeConflict: both changed it; ours is kept and flagged.
	MergeConflict MergeDisposition = "conflict"
	// AddedTheirs: the derivation introduced it.
	AddedTheirs MergeDisposition = "added-theirs"
	// ConflictRestoredTheirs: a human deleted it and the derivation changed it since; theirs is
	// restored and flagged (ours is an absence, so there is nothing to keep).
	ConflictRestoredTheirs MergeDisposition = "conflict-restored-theirs"
	// KeptOursAuthored: a human wrote it and the derivation knows nothing of it.
	KeptOursAuthored MergeDisposition = "kept-ours-authored"
	// KeptOursOrphaned: the derivation dropped a block a human had edited; kept and flagged.
	KeptOursOrphaned MergeDisposition = "kept-ours-orphaned"
)

// MergedBlock is one block of a merge's output. Theirs is set only for MergeConflict.
type MergedBlock struct {
	Block       ChronicleBlock
	Disposition MergeDisposition
	Theirs      *ChronicleBlock
}

// MergeResult is a merge's output: the blocks, the ids needing a human decision (empty is a clean
// merge), and the source the version records (sweep only when no human block was touched).
type MergeResult struct {
	Blocks    []MergedBlock
	Conflicts []string
	Source    ChronicleSource
}

// ChronicleConflictItem is one outstanding decision on a page: the block and which kind it is
// (conflict, conflict-restored-theirs or kept-ours-orphaned).
type ChronicleConflictItem struct {
	BlockID string           `json:"blockId"`
	Kind    MergeDisposition `json:"kind"`
}

func blocksByID(bs []ChronicleBlock) map[string]ChronicleBlock {
	m := make(map[string]ChronicleBlock, len(bs))
	for _, b := range bs {
		m[b.ID] = b
	}
	return m
}

// sameText is the edit signal: equal trimmed text, both present.
func sameText(a, b ChronicleBlock, aOK, bOK bool) bool {
	return aOK && bOK && trimJS(a.Text) == trimJS(b.Text)
}

// MergeVersions merges theirs into ours over base. The output follows theirs' order, with blocks
// only ours holds appended in their relative order.
func MergeVersions(base, ours, theirs []ChronicleBlock) MergeResult {
	baseBy, oursBy, theirsBy := blocksByID(base), blocksByID(ours), blocksByID(theirs)
	out := []MergedBlock{}
	conflicts := []string{}
	humanTouched := false

	for _, t := range theirs {
		o, inOurs := oursBy[t.ID]
		b, inBase := baseBy[t.ID]
		if !inOurs {
			if inBase {
				// In base, gone from ours: a human deleted it.
				humanTouched = true
				if sameText(t, b, true, true) {
					continue // the derivation has not touched it since: the deletion stands
				}
				conflicts = append(conflicts, t.ID)
				out = append(out, MergedBlock{Block: t, Disposition: ConflictRestoredTheirs})
				continue
			}
			out = append(out, MergedBlock{Block: t, Disposition: AddedTheirs})
			continue
		}
		oursEdited := !sameText(o, b, true, inBase)
		theirsChanged := !sameText(t, b, true, inBase)
		switch {
		case !oursEdited, sameText(o, t, true, true):
			// Unedited, or both sides landed the same text (a human took the derivation's side, or
			// wrote what it now says): theirs, so the block is derived again rather than a conflict.
			out = append(out, MergedBlock{Block: t, Disposition: TakenTheirs})
		case !theirsChanged:
			humanTouched = true
			out = append(out, MergedBlock{Block: o, Disposition: KeptOurs})
		default:
			humanTouched = true
			conflicts = append(conflicts, t.ID)
			tt := t
			out = append(out, MergedBlock{Block: o, Disposition: MergeConflict, Theirs: &tt})
		}
	}

	for _, o := range ours {
		if _, ok := theirsBy[o.ID]; ok {
			continue
		}
		b, inBase := baseBy[o.ID]
		switch {
		case !inBase:
			humanTouched = true
			out = append(out, MergedBlock{Block: o, Disposition: KeptOursAuthored})
		case !sameText(o, b, true, true):
			humanTouched = true
			conflicts = append(conflicts, o.ID)
			out = append(out, MergedBlock{Block: o, Disposition: KeptOursOrphaned})
		}
		// else: unedited and dropped by the derivation, so genuinely gone.
	}

	source := SourceSweep
	if humanTouched {
		source = SourceHuman
	}
	return MergeResult{Blocks: out, Conflicts: conflicts, Source: source}
}

// MergedBlocks is the merge's block list, ready to write as a version.
func MergedBlocks(r MergeResult) []ChronicleBlock {
	out := make([]ChronicleBlock, len(r.Blocks))
	for i, m := range r.Blocks {
		out[i] = m.Block
	}
	return out
}

// ConflictItems is the merge's outstanding decisions as persistable items, in r.Conflicts' order.
func ConflictItems(r MergeResult) []ChronicleConflictItem {
	out := []ChronicleConflictItem{}
	for _, m := range r.Blocks {
		switch m.Disposition {
		case MergeConflict, ConflictRestoredTheirs, KeptOursOrphaned:
			out = append(out, ChronicleConflictItem{BlockID: m.Block.ID, Kind: m.Disposition})
		}
	}
	return out
}
