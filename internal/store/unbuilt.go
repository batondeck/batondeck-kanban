package store

import "github.com/batondeck/batondeck-kanban/internal/domain"

// An engine method whose contract exists before its statements do returns unbuilt's error, and the
// conformance batteries skip on it (UnbuiltTask) rather than fail the engine for a lane not merged
// yet. Store contract II (R0.19) carries the same three declarations: whichever lane merges second
// deletes its copy.

// unbuiltDetail is the detail an unbuilt method's error names its task under.
const unbuiltDetail = "unbuilt"

func unbuilt(method, task string) error {
	return domain.NewError(domain.CodeInternal, "store: "+method+" is not built on this engine yet ("+task+")",
		map[string]any{unbuiltDetail: task})
}

// UnbuiltTask reports whether err is an unbuilt method's, and the task that builds it.
func UnbuiltTask(err error) (string, bool) {
	e, ok := domain.AsBatonDeckError(err)
	if !ok || e.Code != domain.CodeInternal {
		return "", false
	}
	task, ok := e.Details[unbuiltDetail].(string)
	return task, ok
}
