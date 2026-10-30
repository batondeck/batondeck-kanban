package core

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	c, err := Load("", env(map[string]string{"XDG_DATA_HOME": "/xdg"}))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Listen, []string{"tcp://127.0.0.1:8080"}) || c.Database.Synchronous != "FULL" || c.PublicURL != "" {
		t.Fatalf("defaults: %+v", c)
	}
	if c.Engine() != EngineSQLite || c.SQLitePath() != "/xdg/batondeck-kanban/batondeck-kanban.db" {
		t.Fatalf("default engine %s at %s", c.Engine(), c.SQLitePath())
	}
}

// The default data dir: $XDG_DATA_HOME/batondeck-kanban when that is absolute, else
// ~/.local/share/batondeck-kanban; a relative XDG_DATA_HOME is ignored, as the XDG spec says.
func TestDefaultDataDir(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"XDG_DATA_HOME": "/xdg", "HOME": "/home/a"}, "/xdg/batondeck-kanban"},
		{map[string]string{"HOME": "/home/a"}, "/home/a/.local/share/batondeck-kanban"},
		{map[string]string{"XDG_DATA_HOME": "rel", "HOME": "/home/a"}, "/home/a/.local/share/batondeck-kanban"},
	} {
		if got, err := DefaultDataDir(env(tc.env)); err != nil || got != tc.want {
			t.Fatalf("%v: %q, %v, want %q", tc.env, got, err, tc.want)
		}
	}
}

// Every subcommand loads the config the same way, so two working directories must resolve the same
// absolute database and blob dir (a CWD-relative ./data made `user add` from another directory
// create a second database).
func TestDefaultPathsAreTheSameFromAnyWorkingDirectory(t *testing.T) {
	lookup := env(map[string]string{"HOME": t.TempDir()})
	var dbs, blobs []string
	for _, wd := range []string{t.TempDir(), t.TempDir()} {
		t.Chdir(wd)
		c, err := Load("", lookup)
		if err != nil {
			t.Fatal(err)
		}
		if p, err := filepath.Abs(c.SQLitePath()); err != nil || p != c.SQLitePath() {
			t.Fatalf("database %q is relative to the working directory", c.SQLitePath())
		}
		dbs, blobs = append(dbs, c.SQLitePath()), append(blobs, c.BlobDir())
	}
	if dbs[0] != dbs[1] || blobs[0] != blobs[1] {
		t.Fatalf("two working directories, two data dirs: %q %q", dbs, blobs)
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
		// public_url is an origin, refused where the config is loaded (P-22), not first in serve's OAuth setup.
		"public_url without a scheme":    {env: map[string]string{"BATONDECK_PUBLIC_URL": "board.example.com"}, rule: RulePublicURL},
		"public_url other scheme":        {env: map[string]string{"BATONDECK_PUBLIC_URL": "ftp://x.example"}, rule: RulePublicURL},
		"public_url with a path":         {env: map[string]string{"BATONDECK_PUBLIC_URL": "https://x.example/base"}, rule: RulePublicURL},
		"public_url with a query":        {env: map[string]string{"BATONDECK_PUBLIC_URL": "https://x.example?q=1"}, rule: RulePublicURL},
		"public_url with an empty query": {env: map[string]string{"BATONDECK_PUBLIC_URL": "https://x.example?"}, rule: RulePublicURL},
		"public_url opaque":              {env: map[string]string{"BATONDECK_PUBLIC_URL": "https:x.example"}, rule: RulePublicURL},
		"public_url with a fragment":     {env: map[string]string{"BATONDECK_PUBLIC_URL": "https://x.example#f"}, rule: RulePublicURL},
		"public_url with userinfo":       {env: map[string]string{"BATONDECK_PUBLIC_URL": "https://u:p@x.example"}, rule: RulePublicURL},
		"public_url port without host":   {env: map[string]string{"BATONDECK_PUBLIC_URL": "http://:8080"}, rule: RulePublicURL},
		"public_url in file":             {file: `{"public_url":"x.example:8080"}`, rule: RulePublicURL},
		// One JSON value and nothing after it but whitespace (P-22).
		"trailing garbage": {file: `{"listen":["tcp://127.0.0.1:9001"]} garbage`, rule: "trailing data"},
		"trailing brace":   {file: `{"listen":["tcp://127.0.0.1:9001"]}}`, rule: "trailing data"},
		"trailing comment": {file: "{\"listen\":[\"tcp://127.0.0.1:9001\"]}\n// a comment\n", rule: "trailing data"},
		"two objects":      {file: `{"listen":["tcp://127.0.0.1:9001"]}{"public_url":"https://x.example"}`, rule: "trailing data"},
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
// the environment over the file, anything below one, past the bounds (a day past a century would
// overflow time.Duration's cutoff) or not a number refused by name, and the bounds themselves loaded.
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
		{"BATONDECK_RETENTION_CHANGE_LOG_DAYS": "36501"},
		{"BATONDECK_RETENTION_TOOL_CALLS_DAYS": "200000"},
		{"BATONDECK_RETENTION_DELIVERIES_DAYS": "36501"},
		{"BATONDECK_RETENTION_RAW_BODIES_DAYS": "36501"},
		{"BATONDECK_RETENTION_SWEEP_INTERVAL_SECONDS": "86401"},
	} {
		if _, err := Load("", env(bad)); err == nil || !strings.Contains(err.Error(), RuleRetention) {
			t.Fatalf("%v: %v, want %s", bad, err, RuleRetention)
		}
	}
	atMax := map[string]string{"BATONDECK_RETENTION_SWEEP_INTERVAL_SECONDS": strconv.Itoa(MaxSweepIntervalSeconds)}
	for _, k := range []string{"CHANGE_LOG", "TOOL_CALLS", "DELIVERIES", "RAW_BODIES"} {
		atMax["BATONDECK_RETENTION_"+k+"_DAYS"] = strconv.Itoa(MaxRetentionDays)
	}
	want := Retention{ChangeLogDays: MaxRetentionDays, ToolCallsDays: MaxRetentionDays, DeliveriesDays: MaxRetentionDays, RawBodiesDays: MaxRetentionDays, SweepIntervalSeconds: MaxSweepIntervalSeconds}
	if c, err := Load("", env(atMax)); err != nil || c.Retention != want {
		t.Fatalf("every bound at its maximum: %+v, %v", c, err)
	}
}

// The blob store: "blobs" beside the SQLite file (in the default data dir on Postgres) and 50 MiB
// by default, the file's object over them, the environment over the file, and a size below one byte
// (or not a number) refused by name.
func TestBlobConfig(t *testing.T) {
	c, err := Load("", env(map[string]string{"XDG_DATA_HOME": "/xdg"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.BlobDir() != "/xdg/batondeck-kanban/blobs" || c.Blob.MaxBytes != 50<<20 {
		t.Fatalf("default blob dir %q, max %d", c.BlobDir(), c.Blob.MaxBytes)
	}
	if c, err = Load("", env(map[string]string{"BATONDECK_DATABASE_URL": "/srv/bk/bk.db"})); err != nil || c.BlobDir() != "/srv/bk/blobs" {
		t.Fatalf("beside the SQLite file: %q, %v", c.BlobDir(), err)
	}
	if c, err = Load("", env(map[string]string{"BATONDECK_DATABASE_URL": "postgres://h/db", "XDG_DATA_HOME": "/xdg"})); err != nil || c.BlobDir() != "/xdg/batondeck-kanban/blobs" {
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

// TestTrustedProxies: trusted_proxies from the file, BATONDECK_TRUSTED_PROXIES over it
// (comma-separated, blanks dropped, empty = none), each an address or a masked prefix; anything
// else is refused naming RuleTrustedProxies and the entry (P-291).
func TestTrustedProxies(t *testing.T) {
	c, err := Load("", env(map[string]string{"XDG_DATA_HOME": "/xdg"}))
	if err != nil || c.TrustedProxies != nil {
		t.Fatalf("default: %v, %v", c.TrustedProxies, err)
	}
	p := writeConfig(t, `{"trusted_proxies":["10.0.0.1","fd00::/8"]}`)
	if c, err = Load(p, env(map[string]string{"XDG_DATA_HOME": "/xdg"})); err != nil || !slices.Equal(c.TrustedProxies, []string{"10.0.0.1", "fd00::/8"}) {
		t.Fatalf("file: %v, %v", c.TrustedProxies, err)
	}
	if c, err = Load(p, env(map[string]string{"XDG_DATA_HOME": "/xdg", "BATONDECK_TRUSTED_PROXIES": " 127.0.0.1, 172.16.0.0/12 ,,"})); err != nil ||
		!slices.Equal(c.TrustedProxies, []string{"127.0.0.1", "172.16.0.0/12"}) {
		t.Fatalf("env over file: %v, %v", c.TrustedProxies, err)
	}
	if c, err = Load(p, env(map[string]string{"XDG_DATA_HOME": "/xdg", "BATONDECK_TRUSTED_PROXIES": ""})); err != nil || len(c.TrustedProxies) != 0 {
		t.Fatalf("empty env clears the file's: %v, %v", c.TrustedProxies, err)
	}
	got, err := ParseTrustedProxies([]string{"10.0.0.1", "172.16.9.9/12", "::ffff:192.0.2.1", "2001:db8::1"})
	var s []string
	for _, p := range got {
		s = append(s, p.String())
	}
	if err != nil || !slices.Equal(s, []string{"10.0.0.1/32", "172.16.0.0/12", "192.0.2.1/32", "2001:db8::1/128"}) {
		t.Fatalf("parsed %v, %v", s, err)
	}
	for _, bad := range []string{"10.0.0.0/33", "proxy.local", "10.0.0.1:80", "fe80::1%eth0", "*"} {
		if _, err := Load("", env(map[string]string{"XDG_DATA_HOME": "/xdg", "BATONDECK_TRUSTED_PROXIES": "10.0.0.1," + bad})); err == nil ||
			!strings.Contains(err.Error(), RuleTrustedProxies) || !strings.Contains(err.Error(), strconv.Quote(bad)) {
			t.Errorf("%q: %v, want %s naming it", bad, err, RuleTrustedProxies)
		}
	}
}

// public_url: unset stays unset (no OAuth server), an origin loads, and a trailing slash is dropped so
// the issuer and the attachment links share one base; a config file of one object followed by
// whitespace loads.
func TestPublicURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                         "",
		"https://board.example":    "https://board.example",
		"https://board.example/":   "https://board.example",
		"http://127.0.0.1:18089//": "http://127.0.0.1:18089",
		"https://[::1]:8443":       "https://[::1]:8443",
	} {
		c, err := Load("", env(map[string]string{"BATONDECK_PUBLIC_URL": in, "XDG_DATA_HOME": "/xdg"}))
		if err != nil {
			t.Fatalf("public_url %q: %v", in, err)
		}
		if c.PublicURL != want {
			t.Errorf("public_url %q loaded as %q, want %q", in, c.PublicURL, want)
		}
	}
	c, err := Load(writeConfig(t, "{\"public_url\":\"https://file.example/\"}\n\t \n"), env(map[string]string{"XDG_DATA_HOME": "/xdg"}))
	if err != nil {
		t.Fatalf("file with trailing whitespace: %v; want it loaded", err)
	}
	if c.PublicURL != "https://file.example" {
		t.Errorf("file's public_url loaded as %q, want https://file.example", c.PublicURL)
	}
}
