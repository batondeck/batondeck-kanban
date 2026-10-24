package cli

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// POST /hooks/{id} is mounted by serve outside every bearer, session and CSRF check, over TCP: a
// signed delivery with no credential but its signature is 202 with the ticket; an unsigned one 401;
// an id of the wrong shape and an unknown one the same flat 404; a body over the outer 512 KiB cap
// 413 before the service sees it (an unknown id too); a body over the integration's own cap 413.
// The delivery expires after the configured window.
func TestServeMountsHooksOutsideEverySession(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "bk.db"), "FULL")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	env := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	keys, err := core.OpenKeyring("", func(k string) (string, bool) { return env, k == core.MasterKeyEnv })
	if err != nil {
		t.Fatal(err)
	}
	logs := &lockedLog{}
	a, err := compose(st, "x", "", log.New(logs, "", 0), keys, service.MediaOptions{}, core.Retention{DeliveriesDays: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer a.core.Close(ctx)
	admin := domain.Caller{Identity: "root", WorkspaceAdmin: true}
	p, err := a.core.Projects().Create(ctx, admin, service.CreateProjectReq{Name: "P"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := a.core.Boards().Create(ctx, admin, service.CreateBoardReq{ProjectID: p.ProjectID, Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	in, err := a.core.Oncall().CreateIntegration(ctx, admin, service.CreateIntegrationReq{ProjectID: p.ProjectID, BoardID: b.Board.ID, Name: "n",
		Source: "generic", Auth: service.IntegrationAuthInput{Mode: "hmac"}, Routing: service.IntegrationRouting{Mode: "broadcast"},
		Limits: service.IntegrationLimitsIn{PayloadCapKb: 2}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.handler("x"))
	defer srv.Close()
	post := func(id string, body []byte, sign bool) (int, string, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/hooks/"+id, bytes.NewReader(body))
		if sign {
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			m := hmac.New(sha256.New, []byte(in.Secret))
			m.Write(append([]byte(ts+"."), body...))
			req.Header.Set(service.HeaderTimestamp, ts)
			req.Header.Set(service.HeaderSignature, "v0="+hex.EncodeToString(m.Sum(nil)))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		got, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(got), resp.Header
	}
	body := []byte(`{"fingerprint":"f","status":"firing","severity":"critical","title":"Queue backlog"}`)
	code, got, _ := post(in.Integration.ID, body, true)
	var accepted struct {
		Accepted int                  `json:"accepted"`
		Results  []service.HookResult `json:"results"`
	}
	if code != http.StatusAccepted || json.Unmarshal([]byte(got), &accepted) != nil || accepted.Accepted != 1 || accepted.Results[0].Result != "created" {
		t.Fatalf("a signed delivery: %d %s", code, got)
	}
	task, err := st.GetTask(ctx, p.ProjectID, *accepted.Results[0].TaskID)
	if err != nil || task.Title != "Queue backlog" {
		t.Fatalf("the ticket: %+v, %v", task, err)
	}
	// P-109: the delivery's expiry is compose's retention.deliveries_days, not the 30-day default.
	page, err := st.ListDeliveries(ctx, p.ProjectID, in.Integration.ID, store.DeliveryFilter{}, "", 10)
	if err != nil || len(page.Deliveries) != 1 || page.Deliveries[0].ExpiresAt-page.Deliveries[0].TS != (3*24*time.Hour).Milliseconds() {
		t.Fatalf("the delivery %+v, %v: want it to expire 3 days after it was written", page.Deliveries, err)
	}
	if code, got, _ := post(in.Integration.ID, body, false); code != http.StatusUnauthorized || !strings.Contains(got, `"UNAUTHENTICATED"`) {
		t.Fatalf("an unsigned delivery: %d %s", code, got)
	}
	_, unknown, _ := post("I-00000000000000000000000000000000", body, true)
	for _, id := range []string{"I-00000000000000000000000000000000", "bad.id", strings.Repeat("a", 65)} {
		if code, got, _ := post(id, body, true); code != http.StatusNotFound || got != unknown {
			t.Fatalf("id %q: %d %s, want the flat 404 %s", id, code, got, unknown)
		}
	}
	// The outer cap reads at most 512 KiB + 1 before any lookup: an unknown id with a bigger body is
	// 413, not the 404 a read of the whole body and a lookup would answer.
	for _, id := range []string{in.Integration.ID, "I-00000000000000000000000000000000"} {
		if code, got, _ := post(id, bytes.Repeat([]byte("x"), 512<<10+1), true); code != http.StatusRequestEntityTooLarge || !strings.Contains(got, "too_large") {
			t.Fatalf("%s over the outer cap: %d %s", id, code, got)
		}
	}
	// Review B2 of #56: both route-level refusals are a stderr line naming the presented id.
	for _, want := range []string{
		`ingest: refused a delivery to integration "bad.id": not_found: the id is not an integration id's shape`,
		`ingest: refused a delivery to integration "I-00000000000000000000000000000000": too_large: over the route's 524288-byte cap (declared length 524289)`,
		`ingest: refused a delivery to integration "` + in.Integration.ID + `": too_large: over the route's 524288-byte cap (declared length 524289)`,
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("no refusal line %q", want)
		}
	}
	// A chunked body declares no length: still refused at the cap, and logged.
	chunked, _ := http.NewRequest(http.MethodPost, srv.URL+"/hooks/"+in.Integration.ID, io.MultiReader(bytes.NewReader(bytes.Repeat([]byte("z"), 512<<10+1))))
	resp, err := http.DefaultClient.Do(chunked)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if want := `integration "` + in.Integration.ID + `": too_large: over the route's 524288-byte cap (declared length -1)`; resp.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(logs.String(), want) {
		t.Errorf("a chunked body over the cap: %d, line %q in the log: %v", resp.StatusCode, want, strings.Contains(logs.String(), want))
	}
	if code, _, _ := post(in.Integration.ID, append(body[:len(body)-1], []byte(`,"description":"`+strings.Repeat("y", 2048)+`"}`)...), true); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the integration's 2 KiB cap: %d", code)
	}
	if !strings.Contains(logs.String(), "ingest: integration "+in.Integration.ID+" refused a delivery's signature") {
		t.Fatalf("no refusal line in %q", logs.String())
	}
}
