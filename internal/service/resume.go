package service

import (
	"context"
	"regexp"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

// resume is what claim_task, claim_next and get_task_context hand an agent that picks up a task
// someone already worked (P-117): the branch and head commit it recorded as artifacts and its last
// checkpoint note, the worker skill's "Resumable lanes". It is derived from the task's rows on each
// read; nothing is stored for it.

// resumeItemsMax is how far back a checkpoint is looked for: the newest page ListContextItems
// serves, its own clamp.
const resumeItemsMax = 200

// checkpointHeadingRE is a line that opens one of a checkpoint's four sections: the heading at the
// line's start, bare, after a list bullet or Markdown #s, optionally **-wrapped, then a colon and
// the section's first text, or nothing.
var checkpointHeadingRE = regexp.MustCompile(`^[ \t]*(?:[-*+][ \t]+)?(?:#{1,6}[ \t]+)?(?:\*\*)?(DONE|NEXT|REJECTED|UNCERTAIN)(?:\*\*)?[ \t]*(?::(?:\*\*)?(.*))?$`)

// resumeOf reads the task's artifacts and its newest context items and derives its resume.
func (c *Core) resumeOf(ctx context.Context, projectID, taskID string) (*ResumeBriefing, error) {
	arts, err := c.store.ListArtifacts(ctx, projectID, taskID)
	if err != nil {
		return nil, err
	}
	page, err := c.store.ListContextItems(ctx, projectID, taskID, store.ContextItemsQuery{Limit: resumeItemsMax})
	if err != nil {
		return nil, err
	}
	return resumeFrom(arts, lastCheckpoint(page.Items)), nil
}

// resumeFrom is the resume of a task with these artifacts and that last checkpoint: the newest live
// branch artifact's ref (else its url), the newest live commit artifact's likewise, and cp. It is
// nil, "nothing to resume", only when all three are absent.
func resumeFrom(arts []store.Artifact, cp *Checkpoint) *ResumeBriefing {
	var branch, commit *store.Artifact
	for i := range arts {
		a := &arts[i]
		if a.Retraction != nil {
			continue
		}
		switch a.Kind {
		case store.ArtifactBranch:
			if branch == nil || a.TS >= branch.TS {
				branch = a
			}
		case store.ArtifactCommit:
			if commit == nil || a.TS >= commit.TS {
				commit = a
			}
		}
	}
	out := ResumeBriefing{Branch: artifactPointer(branch), HeadCommit: artifactPointer(commit), LastCheckpoint: cp}
	if out.Branch == nil && out.HeadCommit == nil && out.LastCheckpoint == nil {
		return nil
	}
	return &out
}

// artifactPointer is what an artifact names: its ref, else its url; nil when it names nothing.
func artifactPointer(a *store.Artifact) *string {
	if a == nil {
		return nil
	}
	for _, s := range []*string{a.Ref, a.URL} {
		if s != nil && strings.TrimSpace(*s) != "" {
			v := strings.TrimSpace(*s)
			return &v
		}
	}
	return nil
}

// lastCheckpoint is the newest note among items (newest first, as ListContextItems serves them)
// that holds at least one checkpoint heading, parsed; nil when none does.
func lastCheckpoint(items []store.ContextItem) *Checkpoint {
	for _, it := range items {
		if it.Kind != store.ContextNote {
			continue
		}
		if cp := parseCheckpoint(it.Body); cp != nil {
			return cp
		}
	}
	return nil
}

// parseCheckpoint is body's four sections: each heading's text, from the rest of its line to the
// next heading, trimmed; nil for a section the note lacks or leaves empty; of a heading written
// twice, the first that has text. It is nil when body holds no heading at all.
func parseCheckpoint(body string) *Checkpoint {
	var cp Checkpoint
	found := false
	var cur **string
	var text []string
	flush := func() {
		if cur != nil && *cur == nil {
			if s := strings.TrimSpace(strings.Join(text, "\n")); s != "" {
				*cur = &s
			}
		}
		cur, text = nil, nil
	}
	for line := range strings.SplitSeq(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		m := checkpointHeadingRE.FindStringSubmatch(line)
		if m == nil {
			if cur != nil {
				text = append(text, line)
			}
			continue
		}
		flush()
		found = true
		switch m[1] {
		case "DONE":
			cur = &cp.DONE
		case "NEXT":
			cur = &cp.NEXT
		case "REJECTED":
			cur = &cp.REJECTED
		case "UNCERTAIN":
			cur = &cp.UNCERTAIN
		}
		text = []string{m[2]}
	}
	flush()
	if !found {
		return nil
	}
	return &cp
}
