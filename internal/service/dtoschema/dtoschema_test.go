package dtoschema

import (
	"reflect"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// A nullable enum field (set_my_settings' tokenPolicy, edit_chronicle_page's coverage) admits null
// and its values, and refuses anything else: inferred as [null, string] with the enum, null itself
// failed the enum, so `"coverage": null` was refused before the service saw it.
func TestNullableEnumAdmitsNull(t *testing.T) {
	type req struct {
		Policy   service.Nullable[service.TokenPolicy]      `json:"policy,omitzero"`
		Coverage service.Nullable[domain.ChronicleCoverage] `json:"coverage,omitzero"`
	}
	s, err := For(reflect.TypeFor[req]()).Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ok := range []map[string]any{{"policy": nil}, {"policy": "warn"}, {"coverage": nil}, {"coverage": "full"}, {}} {
		if err := s.Validate(ok); err != nil {
			t.Errorf("%v refused: %v", ok, err)
		}
	}
	for _, bad := range []map[string]any{{"policy": "strict"}, {"coverage": "most"}, {"policy": 1}} {
		if err := s.Validate(bad); err == nil {
			t.Errorf("%v admitted", bad)
		}
	}
}
