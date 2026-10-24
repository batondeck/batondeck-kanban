package core

// Configuration: precedence environment > file > defaults. The file is JSON (stdlib, no
// dependency); secrets never live here.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Named validation rules: a rejected config names the exact rule violated.
const (
	RuleDatabaseURL = "database_url_is_a_file_path_or_postgres"
	RuleEnum        = "invalid_enum_value"
	RuleRetention   = "retention_days_are_positive_integers"
	RuleListen      = "listen_entries_are_tcp_or_unix_urls"
	RuleBlob        = "blob_max_bytes_is_a_positive_integer"
)

// The engines database.url selects between.
const (
	EngineSQLite   = "sqlite"
	EnginePostgres = "postgres"
)

// DefaultSQLitePath is the SQLite file used when database.url is unset.
const DefaultSQLitePath = "./data/batondeck-kanban.db"

// Database is the `database` object of the config file.
type Database struct {
	// URL is unset or a file path for SQLite, or a postgres:// (postgresql://) URL for Postgres.
	URL string `json:"url"`
	// Synchronous is SQLite's `synchronous` pragma: FULL (the default) or NORMAL, the opt-in.
	// It is not read by Postgres.
	Synchronous string `json:"synchronous"`
}

// Retention is the `retention` object of the config file: how many days the retention sweep
// (internal/retention) keeps a row of each table it sweeps by age. Memory has no window here (each
// entry carries its own expiry) and task events are never swept.
type Retention struct {
	// ChangeLogDays is the change log's window: 7 days by default (hdtp retention.ChangeLogKept).
	ChangeLogDays int `json:"change_log_days"`
	// ToolCallsDays is tool_calls' window: 45 days by default (conductor's BigQuery table expiry).
	ToolCallsDays int `json:"tool_calls_days"`
	// SweepIntervalSeconds is how often serve sweeps: 3600 (hourly) by default.
	SweepIntervalSeconds int `json:"sweep_interval_seconds"`
}

// DefaultRetention is the retention object unset.
var DefaultRetention = Retention{ChangeLogDays: 7, ToolCallsDays: 45, SweepIntervalSeconds: 3600}

// DefaultBlobMaxBytes is blob.max_bytes unset: conductor's ATTACHMENT_MAX_BYTES, 50 MiB.
const DefaultBlobMaxBytes = 50 << 20

// Blob is the `blob` object of the config file: the local blob store of attachments.
type Blob struct {
	// Dir is the blob directory; "" is "blobs" beside the SQLite file (BlobDir).
	Dir string `json:"dir"`
	// MaxBytes is the largest attachment, in bytes: attach_file refuses a larger one and an upload's
	// body is cut off past it (413).
	MaxBytes int64 `json:"max_bytes"`
}

// Config is the resolved configuration. Highest precedence first: the BATONDECK_* environment,
// the JSON file, the defaults. Load reads all three.
type Config struct {
	Database Database `json:"database"`
	// Listen is every address the HTTP server binds, each tcp://host:port or unix:///abs/path; all
	// serve the same handler (internal/core/listen.go). Environment: BATONDECK_LISTEN, comma-separated.
	Listen []string `json:"listen"`
	// PublicURL is the externally reachable base (scheme://host[:port]); "" until configured. It
	// is the OAuth server's issuer and the base of every URL in its RFC 8414 and RFC 9728 documents;
	// while it is "", serve runs no OAuth server (internal/auth/oauth).
	PublicURL string `json:"public_url"`
	// Retention is the retention sweep's windows.
	Retention Retention `json:"retention"`
	// Blob is the blob store. Environment: BATONDECK_BLOB_DIR, BATONDECK_BLOB_MAX_BYTES.
	Blob Blob `json:"blob"`
}

// Load reads path (optional, "" = no file) and environment via lookup (injectable for
// tests; production passes os.LookupEnv). Precedence: env > file > defaults.
func Load(path string, lookup func(string) (string, bool)) (*Config, error) {
	c := Config{
		Database:  Database{Synchronous: "FULL"},
		Listen:    []string{"tcp://127.0.0.1:8080"},
		Retention: DefaultRetention,
		Blob:      Blob{MaxBytes: DefaultBlobMaxBytes},
	}

	// file layer
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("config file: %w", err)
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("config file %s: %w", path, err)
		}
	}

	// env layer (BATONDECK_*)
	envStr := func(key string, dst *string) {
		if v, ok := lookup(key); ok {
			*dst = v
		}
	}
	envStr("BATONDECK_DATABASE_URL", &c.Database.URL)
	envStr("BATONDECK_DATABASE_SYNCHRONOUS", &c.Database.Synchronous)
	if v, ok := lookup("BATONDECK_LISTEN"); ok {
		c.Listen = nil
		for _, a := range strings.Split(v, ",") {
			if a = strings.TrimSpace(a); a != "" {
				c.Listen = append(c.Listen, a)
			}
		}
	}
	envStr("BATONDECK_PUBLIC_URL", &c.PublicURL)
	envDays := func(key string, dst *int) error {
		v, ok := lookup(key)
		if !ok {
			return nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%s: %s %q is not an integer", RuleRetention, key, v)
		}
		*dst = n
		return nil
	}
	if err := envDays("BATONDECK_RETENTION_CHANGE_LOG_DAYS", &c.Retention.ChangeLogDays); err != nil {
		return nil, err
	}
	if err := envDays("BATONDECK_RETENTION_TOOL_CALLS_DAYS", &c.Retention.ToolCallsDays); err != nil {
		return nil, err
	}
	if err := envDays("BATONDECK_RETENTION_SWEEP_INTERVAL_SECONDS", &c.Retention.SweepIntervalSeconds); err != nil {
		return nil, err
	}
	envStr("BATONDECK_BLOB_DIR", &c.Blob.Dir)
	if v, ok := lookup("BATONDECK_BLOB_MAX_BYTES"); ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: BATONDECK_BLOB_MAX_BYTES %q is not an integer", RuleBlob, v)
		}
		c.Blob.MaxBytes = n
	}

	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	switch c.Database.Synchronous {
	case "FULL", "NORMAL":
	default:
		return fmt.Errorf("%s: database.synchronous %q (want FULL|NORMAL)", RuleEnum, c.Database.Synchronous)
	}
	if c.Retention.ChangeLogDays <= 0 || c.Retention.ToolCallsDays <= 0 || c.Retention.SweepIntervalSeconds <= 0 {
		return fmt.Errorf("%s: retention change_log_days %d, tool_calls_days %d, sweep_interval_seconds %d (each must be at least 1)", RuleRetention,
			c.Retention.ChangeLogDays, c.Retention.ToolCallsDays, c.Retention.SweepIntervalSeconds)
	}
	if c.Blob.MaxBytes <= 0 {
		return fmt.Errorf("%s: blob.max_bytes %d (must be at least 1)", RuleBlob, c.Blob.MaxBytes)
	}
	if len(c.Listen) == 0 {
		return fmt.Errorf("%s: listen is empty (want at least one tcp://host:port or unix:///path)", RuleListen)
	}
	for _, a := range c.Listen {
		if _, err := ParseListen(a); err != nil {
			return fmt.Errorf("%s: %w", RuleListen, err)
		}
	}
	if strings.Contains(c.Database.URL, "://") && c.Engine() != EnginePostgres {
		return fmt.Errorf("%s: database.url %q is neither a file path nor a postgres:// URL", RuleDatabaseURL, c.Database.URL)
	}
	return nil
}

// Engine is the store engine database.url selects: postgres for a postgres:// or postgresql://
// URL, sqlite otherwise.
func (c *Config) Engine() string {
	u := c.Database.URL
	if strings.HasPrefix(u, "postgres://") || strings.HasPrefix(u, "postgresql://") {
		return EnginePostgres
	}
	return EngineSQLite
}

// SQLitePath is the SQLite file: database.url, or DefaultSQLitePath when it is unset. It is
// meaningful only when Engine is sqlite.
func (c *Config) SQLitePath() string {
	if c.Database.URL == "" {
		return DefaultSQLitePath
	}
	return c.Database.URL
}

// BlobDir is the blob directory: blob.dir, else "blobs" beside the SQLite file (on Postgres,
// beside DefaultSQLitePath).
func (c *Config) BlobDir() string {
	if c.Blob.Dir != "" {
		return c.Blob.Dir
	}
	db := c.SQLitePath()
	if c.Engine() != EngineSQLite {
		db = DefaultSQLitePath
	}
	return filepath.Join(filepath.Dir(db), "blobs")
}
