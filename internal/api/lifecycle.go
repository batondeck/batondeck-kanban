package api

// lifecycleRoutes are the lifecycle a human drives from the UI (app.ts: claim, release, complete,
// block, handoff); the worker-only heartbeat is not exposed, as in conductor.
func lifecycleRoutes(o *Options) []route {
	l := o.Services.Lifecycle
	return []route{
		{Method: "POST", Path: ppt + "/claim", h: call(o, l.Claim, byTask)},
		{Method: "POST", Path: ppt + "/release", h: call(o, l.Release, byTask)},
		{Method: "POST", Path: ppt + "/complete", h: call(o, l.Complete, byTask)},
		{Method: "POST", Path: ppt + "/block", h: call(o, l.Block, byTask)},
		{Method: "POST", Path: ppt + "/handoff", h: call(o, l.Handoff, byTask)},
	}
}
