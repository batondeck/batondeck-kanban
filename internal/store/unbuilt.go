package store

import "github.com/batondeck/batondeck-kanban/internal/domain"

// An engine method whose contract exists before its statements do returns unbuilt's error, and the
// conformance batteries skip on it (UnbuiltTask) rather than fail the engine for a lane not merged
// yet. An engine's stub calls unbuilt, memstore's Unbuilt; none is left since R1.9 built search, the
// last stubbed area.

// unbuiltDetail is the detail an unbuilt method's error names its task under.
const unbuiltDetail = "unbuilt"

func unbuilt(method, task string) error {
	return domain.NewError(domain.CodeInternal, "store: "+method+" is not built on this engine yet ("+task+")",
		map[string]any{unbuiltDetail: task})
}

// Unbuilt is unbuilt for a store outside this package (memstore): the same error, so the batteries
// skip on it the same way.
func Unbuilt(method, task string) error { return unbuilt(method, task) }

// UnbuiltTask reports whether err is an unbuilt method's, and the task that builds it.
func UnbuiltTask(err error) (string, bool) {
	e, ok := domain.AsBatonDeckError(err)
	if !ok || e.Code != domain.CodeInternal {
		return "", false
	}
	task, ok := e.Details[unbuiltDetail].(string)
	return task, ok
}
