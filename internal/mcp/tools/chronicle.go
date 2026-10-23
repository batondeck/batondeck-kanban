package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("chronicle", RegisterChronicle) }

// RegisterChronicle adds the four Chronicle tools, each one service.Chronicle call.
func RegisterChronicle(s *mcp.Server, svc service.Services) {
	c := svc.Chronicle
	mcp.Tool(s, "chronicle", "create_chronicle_page",
		"Start a Chronicle record by hand: every block authored, origin ignored. A taken slug or a sweep-shaped block id is refused. Change it later with edit_chronicle_page.",
		c.CreatePage)
	mcp.Tool(s, "chronicle", "ingest_chronicle_page",
		"Ingest a generated record: creates the page, or merges the new blocks with human edits (both-changed blocks come back as conflicts). An unchanged re-ingest writes no version.",
		c.IngestPage)
	mcp.Tool(s, "chronicle", "edit_chronicle_page",
		"Commit a new version of a page from expectedParentVersionId (a moved head is STALE). origin is inherited, never taken; drifted lists blocks now derived-then-edited.",
		c.EditPage)
	mcp.Tool(s, "chronicle", "delete_chronicle_page",
		"Destroy a page, every version and every body. Irreversible. Pass expectedHeadVersionId to refuse with STALE if the page changed since you read it.",
		c.DeletePage)
}
