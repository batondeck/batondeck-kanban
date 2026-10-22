package core

import (
	"os"
	"path/filepath"
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
	if c.Listen != "127.0.0.1:8080" || c.Database.Synchronous != "FULL" || c.PublicURL != "" {
		t.Fatalf("defaults: %+v", c)
	}
	if c.Engine() != EngineSQLite || c.SQLitePath() != DefaultSQLitePath {
		t.Fatalf("default engine %s at %s", c.Engine(), c.SQLitePath())
	}
}

// env > file > defaults, key by key: the file beats the default, the environment beats the file.
func TestEnvBeatsFileBeatsDefault(t *testing.T) {
	p := writeConfig(t, `{"listen":"127.0.0.1:9001","public_url":"https://file.example","database":{"url":"/srv/file.db","synchronous":"NORMAL"}}`)
	c, err := Load(p, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9001" || c.PublicURL != "https://file.example" || c.Database.URL != "/srv/file.db" || c.Database.Synchronous != "NORMAL" {
		t.Fatalf("file layer: %+v", c)
	}
	c, err = Load(p, env(map[string]string{
		"BATONDECK_LISTEN":               "127.0.0.1:9002",
		"BATONDECK_PUBLIC_URL":           "https://env.example",
		"BATONDECK_DATABASE_URL":         "postgres://u:p@db/bk",
		"BATONDECK_DATABASE_SYNCHRONOUS": "FULL",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9002" || c.PublicURL != "https://env.example" || c.Database.URL != "postgres://u:p@db/bk" || c.Database.Synchronous != "FULL" {
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
