package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("deliveries", RegisterDeliveries) }

// RegisterDeliveries adds the delivery-log tools, each one service.Oncall call, workspace-admin
// only like the integration tools.
func RegisterDeliveries(s *mcp.Server, svc service.Services) {
	o := svc.Oncall
	mcp.Tool(s, "deliveries", "list_deliveries",
		"An integration's webhook delivery log, newest first: what arrived, its result and the ticket it produced. Cursor-paged: nextCursor present means more rows.",
		o.ListDeliveries)
	mcp.Tool(s, "deliveries", "read_delivery_raw",
		"The kept raw payload of a refused delivery, base64: unauthenticated attacker-controlled bytes, never to be rendered. NOT_FOUND once its retention has passed.",
		o.ReadDeliveryRaw)
	mcp.Tool(s, "deliveries", "replay_delivery",
		"Ingest a stored delivery's normalized event again under a new delivery id. A delivery refused before parsing has none and is refused: unverified bytes are never replayed.",
		o.ReplayDelivery)
	mcp.Tool(s, "deliveries", "send_test_event",
		"Ingest a synthetic test alert through an integration to prove it is wired: files a real ticket labelled test on the target board and rings its doorbell.",
		o.SendTestEvent)
}
