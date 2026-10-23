package mcp_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
)

// maxDescription is the cap on a tool description, in characters (ADR-0004 A3).
const maxDescription = 200

// conductorOnly is text a description must not carry: conductor's transport and its retired states
// (ADR-0004 A3).
var conductorOnly = []string{"Pub/Sub", "CANCELLED", "DEAD_LETTER"}

// descriptionFaults names each tool whose description is over maxDescription characters, with its
// length, or carries conductorOnly text.
func descriptionFaults(listed []*sdk.Tool) []string {
	var out []string
	for _, tl := range listed {
		if n := utf8.RuneCountInString(tl.Description); n > maxDescription {
			out = append(out, fmt.Sprintf("%s: description is %d characters, over %d", tl.Name, n, maxDescription))
		}
		for _, s := range conductorOnly {
			if strings.Contains(tl.Description, s) {
				out = append(out, fmt.Sprintf("%s: description carries conductor-only %q", tl.Name, s))
			}
		}
	}
	return out
}

// TestDescriptionFaults: 201 characters and conductor-only text are refused, naming the tool and
// the length; exactly 200, counted in characters not bytes, passes.
func TestDescriptionFaults(t *testing.T) {
	at200 := strings.Repeat("é", maxDescription)
	if f := descriptionFaults([]*sdk.Tool{{Name: "ok", Description: at200}}); len(f) != 0 {
		t.Fatalf("a 200-character description was refused: %v", f)
	}
	f := descriptionFaults([]*sdk.Tool{{Name: "long", Description: at200 + "x"}})
	if len(f) != 1 || f[0] != "long: description is 201 characters, over 200" {
		t.Fatalf("a 201-character description: %v", f)
	}
	for _, s := range conductorOnly {
		if f := descriptionFaults([]*sdk.Tool{{Name: "c", Description: "Lands " + s + "."}}); len(f) != 1 {
			t.Errorf("a description carrying %q: %v", s, f)
		}
	}
}

// TestRegisteredDescriptions: every tool the registry holds, as an admin client lists it over every
// page of tools/list, has a description of at most 200 characters and no conductor-only text.
func TestRegisteredDescriptions(t *testing.T) {
	h := bootTools(t, servicefake.New().Services())
	cs := h.Connect(t, bearerAdmin)
	var listed []*sdk.Tool
	for tl, err := range cs.Tools(testCtx(t), nil) {
		if err != nil {
			t.Fatal(err)
		}
		listed = append(listed, tl)
	}
	if n := len(h.Server().Manifest()); n == 0 || len(listed) != n {
		t.Fatalf("tools/list returned %d tools, the registry holds %d", len(listed), n)
	}
	for _, f := range descriptionFaults(listed) {
		t.Error(f)
	}
	t.Logf("%d registered tool descriptions checked", len(listed))
}
