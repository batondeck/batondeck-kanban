package authfake

import (
	"context"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

func TestFakeResolvesOnlyConfiguredBearers(t *testing.T) {
	want := domain.Caller{Identity: "U-1", TokenScope: "U-1", WorkspaceAdmin: true}
	f := Resolver{Callers: map[string]domain.Caller{"good": want}}
	got, err := f.ResolveCaller(context.Background(), "good")
	if err != nil || got != want {
		t.Fatalf("configured bearer: %+v %v", got, err)
	}
	for _, b := range []string{"", "bad", "GOOD"} {
		_, err := f.ResolveCaller(context.Background(), b)
		if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeUnauthenticated {
			t.Fatalf("bearer %q: %v, want UNAUTHENTICATED", b, err)
		}
	}
}
