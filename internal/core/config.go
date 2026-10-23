package core

// Configuration: precedence environment > file > defaults. The file is JSON (stdlib, no
// dependency); secrets never live here.

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Named validation rules: a rejected config names the exact rule violated.
const (
	RuleDatabaseURL = "database_url_is_a_file_path_or_postgres"
	RuleEnum        = "invalid_enum_value"
	RuleRetention   = "retention_days_are_positive_integers"
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
}

// Config is the resolved configuration. Highest precedence first: the BATONDECK_* environment,
// the JSON file, the defaults. Load reads all three.
type Config struct {
	Database Database `json:"database"`
	// Listen is the host:port the HTTP server binds.
	Listen string `json:"listen"`
	// PublicURL is the externally reachable base (scheme://host[:port]); "" until configured. It
	// is the OAuth server's issuer and the base of every URL in its RFC 8414 and RFC 9728 documents;
	// while it is "", serve runs no OAuth server (internal/auth/oauth).
	PublicURL string `json:"public_url"`
	// Retention is the retention sweep's windows.
	Retention Retention `json:"retention"`
}

// Load reads path (optional, "" = no file) and environment via lookup (injectable for
// tests; production passes os.LookupEnv). Precedence: env > file > defaults.
func Load(path string, lookup func(string) (string, bool)) (*Config, error) {
	c := Config{
		Database:  Database{Synchronous: "FULL"},
		Listen:    "127.0.0.1:8080",
		Retention: Retention{ChangeLogDays: 7, ToolCallsDays: 45},
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
	envStr("BATONDECK_LISTEN", &c.Listen)
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
	if c.Retention.ChangeLogDays <= 0 || c.Retention.ToolCallsDays <= 0 {
		return fmt.Errorf("%s: retention change_log_days %d, tool_calls_days %d (each must be at least 1)", RuleRetention, c.Retention.ChangeLogDays, c.Retention.ToolCallsDays)
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
