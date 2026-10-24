package core

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaults(t *testing.T) {
	c, err := Load("", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Listen, []string{"tcp://127.0.0.1:8080"}) || c.Database.Synchronous != "FULL" || c.PublicURL != "" {
		t.Fatalf("defaults: %+v", c)
	}
	if c.Engine() != EngineSQLite || c.SQLitePath() != DefaultSQLitePath {
		t.Fatalf("default engine %s at %s", c.Engine(), c.SQLitePath())
	}
}

// env > file > defaults, key by key: the file beats the default, the environment beats the file.
func TestEnvBeatsFileBeatsDefault(t *testing.T) {
	p := writeConfig(t, `{"listen":["tcp://127.0.0.1:9001","unix:///run/bk/bk.sock"],"public_url":"https://file.example","database":{"url":"/srv/file.db","synchronous":"NORMAL"}}`)
	c, err := Load(p, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Listen, []string{"tcp://127.0.0.1:9001", "unix:///run/bk/bk.sock"}) || c.PublicURL != "https://file.example" || c.Database.URL != "/srv/file.db" || c.Database.Synchronous != "NORMAL" {
		t.Fatalf("file layer: %+v", c)
	}
	c, err = Load(p, env(map[string]string{
		"BATONDECK_LISTEN":               "tcp://127.0.0.1:9002, unix:///tmp/b.sock",
		"BATONDECK_PUBLIC_URL":           "https://env.example",
		"BATONDECK_DATABASE_URL":         "postgres://u:p@db/bk",
		"BATONDECK_DATABASE_SYNCHRONOUS": "FULL",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Listen, []string{"tcp://127.0.0.1:9002", "unix:///tmp/b.sock"}) || c.PublicURL != "https://env.example" || c.Database.URL != "postgres://u:p@db/bk" || c.Database.Synchronous != "FULL" {
		t.Fatalf("env layer: %+v", c)
	}
	if c.Engine() != EnginePostgres {
		t.Fatalf("postgres URL selected %s", c.Engine())
	}
}

func TestEngineSelection(t *testing.T) {
	for url, want := range map[string]string{
		"":                  EngineSQLite,
		"/var/lib/bk.db":    EngineSQLite,
		"bk.db":             EngineSQLite,
		"postgres://h/db":   EnginePostgres,
		"postgresql://h/db": EnginePostgres,
	} {
		c, err := Load("", env(map[string]string{"BATONDECK_DATABASE_URL": url}))
		if err != nil {
			t.Fatalf("%q: %v", url, err)
		}
		if got := c.Engine(); got != want {
			t.Fatalf("%q selected %s, want %s", url, got, want)
		}
	}
}

func TestRefusals(t *testing.T) {
	cases := map[string]struct {
		file string
		env  map[string]string
		rule string
	}{
		"unknown key":          {file: `{"data_dir":"/x"}`, rule: "unknown field"},
		"other scheme":         {env: map[string]string{"BATONDECK_DATABASE_URL": "mysql://h/db"}, rule: RuleDatabaseURL},
		"synchronous off enum": {env: map[string]string{"BATONDECK_DATABASE_SYNCHRONOUS": "OFF"}, rule: RuleEnum},
		"bare listen address":  {env: map[string]string{"BATONDECK_LISTEN": ":8080"}, rule: RuleListen},
		"listen scheme":        {env: map[string]string{"BATONDECK_LISTEN": "http://:8080"}, rule: RuleListen},
		"relative unix path":   {env: map[string]string{"BATONDECK_LISTEN": "unix://run/bk.sock"}, rule: RuleListen},
		"tcp without a port":   {env: map[string]string{"BATONDECK_LISTEN": "tcp://localhost"}, rule: RuleListen},
		"empty listen":         {env: map[string]string{"BATONDECK_LISTEN": " , "}, rule: RuleListen},
		"empty listen in file": {file: `{"listen":[]}`, rule: RuleListen},
	}
	for name, tc := range cases {
		path := ""
		if tc.file != "" {
			path = writeConfig(t, tc.file)
		}
		_, err := Load(path, env(tc.env))
		if err == nil || !strings.Contains(err.Error(), tc.rule) {
			t.Fatalf("%s: err %v, want one naming %q", name, err, tc.rule)
		}
	}
}

// The retention windows: 7 and 45 days and an hourly sweep by default, the file's object over them,
// the environment over the file, and anything below one (or not a number) refused by name.
func TestRetentionWindows(t *testing.T) {
	c, err := Load("", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Retention != (Retention{ChangeLogDays: 7, ToolCallsDays: 45, DeliveriesDays: 30, RawBodiesDays: 7, SweepIntervalSeconds: 3600}) {
		t.Fatalf("default retention %+v", c.Retention)
	}
	p := writeConfig(t, `{"retention":{"change_log_days":3,"tool_calls_days":90,"deliveries_days":14,"raw_bodies_days":2,"sweep_interval_seconds":60}}`)
	if c, err = Load(p, env(nil)); err != nil || c.Retention != (Retention{ChangeLogDays: 3, ToolCallsDays: 90, DeliveriesDays: 14, RawBodiesDays: 2, SweepIntervalSeconds: 60}) {
		t.Fatalf("file retention %+v, %v", c.Retention, err)
	}
	if c, err = Load(p, env(map[string]string{"BATONDECK_RETENTION_CHANGE_LOG_DAYS": "1", "BATONDECK_RETENTION_SWEEP_INTERVAL_SECONDS": "2", "BATONDECK_RETENTION_RAW_BODIES_DAYS": "1"})); err != nil ||
		c.Retention != (Retention{ChangeLogDays: 1, ToolCallsDays: 90, DeliveriesDays: 14, RawBodiesDays: 1, SweepIntervalSeconds: 2}) {
		t.Fatalf("env retention %+v, %v", c.Retention, err)
	}
	for _, bad := range []map[string]string{
		{"BATONDECK_RETENTION_CHANGE_LOG_DAYS": "0"},
		{"BATONDECK_RETENTION_TOOL_CALLS_DAYS": "-1"},
		{"BATONDECK_RETENTION_TOOL_CALLS_DAYS": "week"},
		{"BATONDECK_RETENTION_SWEEP_INTERVAL_SECONDS": "0"},
		{"BATONDECK_RETENTION_DELIVERIES_DAYS": "0"},
		{"BATONDECK_RETENTION_RAW_BODIES_DAYS": "-7"},
	} {
		if _, err := Load("", env(bad)); err == nil || !strings.Contains(err.Error(), RuleRetention) {
			t.Fatalf("%v: %v, want %s", bad, err, RuleRetention)
		}
	}
}

// The blob store: "blobs" beside the SQLite file (beside DefaultSQLitePath on Postgres) and 50 MiB
// by default, the file's object over them, the environment over the file, and a size below one byte
// (or not a number) refused by name.
func TestBlobConfig(t *testing.T) {
	c, err := Load("", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.BlobDir() != filepath.Join("data", "blobs") || c.Blob.MaxBytes != 50<<20 {
		t.Fatalf("default blob dir %q, max %d", c.BlobDir(), c.Blob.MaxBytes)
	}
	if c, err = Load("", env(map[string]string{"BATONDECK_DATABASE_URL": "/srv/bk/bk.db"})); err != nil || c.BlobDir() != "/srv/bk/blobs" {
		t.Fatalf("beside the SQLite file: %q, %v", c.BlobDir(), err)
	}
	if c, err = Load("", env(map[string]string{"BATONDECK_DATABASE_URL": "postgres://h/db"})); err != nil || c.BlobDir() != filepath.Join("data", "blobs") {
		t.Fatalf("postgres: %q, %v", c.BlobDir(), err)
	}
	p := writeConfig(t, `{"blob":{"dir":"/srv/blobs","max_bytes":1024}}`)
	if c, err = Load(p, env(nil)); err != nil || c.BlobDir() != "/srv/blobs" || c.Blob.MaxBytes != 1024 {
		t.Fatalf("file blob %+v, %v", c.Blob, err)
	}
	if c, err = Load(p, env(map[string]string{"BATONDECK_BLOB_DIR": "/var/b", "BATONDECK_BLOB_MAX_BYTES": "7"})); err != nil || c.Blob != (Blob{Dir: "/var/b", MaxBytes: 7}) {
		t.Fatalf("env blob %+v, %v", c.Blob, err)
	}
	for _, bad := range []map[string]string{{"BATONDECK_BLOB_MAX_BYTES": "0"}, {"BATONDECK_BLOB_MAX_BYTES": "1MiB"}} {
		if _, err := Load("", env(bad)); err == nil || !strings.Contains(err.Error(), RuleBlob) {
			t.Fatalf("%v: %v, want %s", bad, err, RuleBlob)
		}
	}
}
