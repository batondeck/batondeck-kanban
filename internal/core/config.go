package core

// Configuration: precedence environment > file > defaults. The file is JSON (stdlib, no
// dependency); secrets never live here.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Named validation rules: a rejected config names the exact rule violated.
const (
	RuleDatabaseURL    = "database_url_is_a_file_path_or_postgres"
	RuleEnum           = "invalid_enum_value"
	RuleRetention      = "retention_days_are_positive_integers"
	RuleListen         = "listen_entries_are_tcp_or_unix_urls"
	RuleBlob           = "blob_max_bytes_is_a_positive_integer"
	RuleTrustedProxies = "trusted_proxies_are_ip_addresses_or_cidrs"
	RulePublicURL      = "public_url_is_an_http_origin"
)

// The engines database.url selects between.
const (
	EngineSQLite   = "sqlite"
	EnginePostgres = "postgres"
)

// DefaultDBFile is the SQLite file's name in the default data dir, used when database.url is unset.
const DefaultDBFile = "batondeck-kanban.db"

// DefaultDataDir is the absolute per-user data dir every subcommand resolves alike from any working
// directory: $XDG_DATA_HOME/batondeck-kanban when XDG_DATA_HOME is absolute, else
// ~/.local/share/batondeck-kanban.
func DefaultDataDir(lookup func(string) (string, bool)) (string, error) {
	if v, ok := lookup("XDG_DATA_HOME"); ok && filepath.IsAbs(v) {
		return filepath.Join(v, "batondeck-kanban"), nil
	}
	home, ok := lookup("HOME")
	if !ok || !filepath.IsAbs(home) {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", fmt.Errorf("%s: no XDG_DATA_HOME or HOME for the default data dir: set BATONDECK_DATABASE_URL", RuleDatabaseURL)
		}
	}
	return filepath.Join(home, ".local", "share", "batondeck-kanban"), nil
}

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
// entry carries its own expiry), an agent session goes 30 minutes idle (retention.AgentSessionIdle)
// and task events are never swept.
type Retention struct {
	// ChangeLogDays is the change log's window: 7 days by default (hdtp retention.ChangeLogKept).
	ChangeLogDays int `json:"change_log_days"`
	// ToolCallsDays is tool_calls' window: 45 days by default (conductor's BigQuery table expiry).
	ToolCallsDays int `json:"tool_calls_days"`
	// DeliveriesDays is an on-call delivery's window: 30 days by default. It is stamped on the row
	// (and its alert state) when written, so a change applies to deliveries written after it. An
	// alert state is swept on that window once its ticket is DONE or deleted; an open ticket's
	// stays, so a late firing still appends to it. A DONE ticket reopened after its state was swept
	// is no longer correlated: its alert's next firing files a new ticket.
	DeliveriesDays int `json:"deliveries_days"`
	// RawBodiesDays is a refused delivery's raw body window: 7 days by default, stamped likewise.
	RawBodiesDays int `json:"raw_bodies_days"`
	// SweepIntervalSeconds is how often serve sweeps: 3600 (hourly) by default.
	SweepIntervalSeconds int `json:"sweep_interval_seconds"`
}

// The retention bounds: a window is at most a century, far inside time.Duration's ~106,751 days, so
// no window overflows into a cutoff in the future (which would sweep a whole table); the sweep runs at
// least daily.
const (
	MaxRetentionDays        = 36500
	MaxSweepIntervalSeconds = 86400
)

// DefaultRetention is the retention object unset.
var DefaultRetention = Retention{ChangeLogDays: 7, ToolCallsDays: 45, DeliveriesDays: 30, RawBodiesDays: 7, SweepIntervalSeconds: 3600}

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
	// TrustedProxies are the reverse proxies in front of the server, each an IP address or a CIDR
	// prefix: a request whose peer is one is counted (by the sign-in limits and OAuth registration)
	// against the client its X-Forwarded-For names. Empty, the header is never read. Environment:
	// BATONDECK_TRUSTED_PROXIES, comma-separated.
	TrustedProxies []string `json:"trusted_proxies"`
	// Retention is the retention sweep's windows.
	Retention Retention `json:"retention"`
	// Blob is the blob store. Environment: BATONDECK_BLOB_DIR, BATONDECK_BLOB_MAX_BYTES.
	Blob Blob `json:"blob"`

	// dataDir is DefaultDataDir as Load resolved it.
	dataDir string
}

// EnvVar is one BATONDECK_* variable the server reads, as `serve -h` and `doctor -h` list it.
type EnvVar struct {
	Name, Default, Doc string
}

// EnvVars is every BATONDECK_* variable read in this package, in help order; a test fails when a
// source here reads one this list does not name.
func EnvVars() []EnvVar {
	r := DefaultRetention
	days := func(n int) string { return strconv.Itoa(n) }
	return []EnvVar{
		{"BATONDECK_CONFIG", "unset, no file is read", "path to the JSON config file; the -config flag overrides it"},
		{"BATONDECK_DATABASE_URL", "$XDG_DATA_HOME/batondeck-kanban/" + DefaultDBFile + ", else ~/.local/share/batondeck-kanban/" + DefaultDBFile, "a SQLite file path, or a postgres:// URL"},
		{"BATONDECK_DATABASE_SYNCHRONOUS", "FULL", "SQLite's synchronous pragma: FULL or NORMAL"},
		{"BATONDECK_LISTEN", "tcp://127.0.0.1:8080", "comma-separated tcp://host:port or unix:///abs/path addresses"},
		{"BATONDECK_PUBLIC_URL", "unset, no OAuth server: API tokens only", "the externally reachable base URL: the OAuth issuer, and the base of attachment links (unset, each link takes the requesting client's host)"},
		{"BATONDECK_TRUSTED_PROXIES", "unset, X-Forwarded-For is never read", "comma-separated IP addresses or CIDRs of the reverse proxies in front of the server: a request from one counts against the client its X-Forwarded-For names (the sign-in and registration limits)"},
		{"BATONDECK_RETENTION_CHANGE_LOG_DAYS", days(r.ChangeLogDays), "days the change log is kept"},
		{"BATONDECK_RETENTION_TOOL_CALLS_DAYS", days(r.ToolCallsDays), "days tool-call records are kept"},
		{"BATONDECK_RETENTION_DELIVERIES_DAYS", days(r.DeliveriesDays), "days an on-call delivery is kept"},
		{"BATONDECK_RETENTION_RAW_BODIES_DAYS", days(r.RawBodiesDays), "days a refused delivery's raw body is kept"},
		{"BATONDECK_RETENTION_SWEEP_INTERVAL_SECONDS", days(r.SweepIntervalSeconds), "seconds between retention sweeps"},
		{"BATONDECK_BLOB_DIR", "blobs beside the SQLite file; on Postgres, blobs in the default data dir", "the attachment blob directory"},
		{"BATONDECK_BLOB_MAX_BYTES", strconv.Itoa(DefaultBlobMaxBytes), "the largest attachment, in bytes"},
		{MasterKeyEnv, "master.key beside the SQLite file (on Postgres, beside the config file), made on first run", "base64 32-byte master key"},
	}
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
		// Decode reads one JSON value and stops: anything after it but whitespace (a second object,
		// a stray brace) would otherwise be dropped without a word.
		if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("config file %s: trailing data after the JSON object", path)
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
	if v, ok := lookup("BATONDECK_TRUSTED_PROXIES"); ok {
		c.TrustedProxies = nil
		for _, a := range strings.Split(v, ",") {
			if a = strings.TrimSpace(a); a != "" {
				c.TrustedProxies = append(c.TrustedProxies, a)
			}
		}
	}
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
	if err := envDays("BATONDECK_RETENTION_DELIVERIES_DAYS", &c.Retention.DeliveriesDays); err != nil {
		return nil, err
	}
	if err := envDays("BATONDECK_RETENTION_RAW_BODIES_DAYS", &c.Retention.RawBodiesDays); err != nil {
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
	// The default data dir is an error only where it is used: no database.url, or Postgres with no blob.dir.
	dir, err := DefaultDataDir(lookup)
	if err != nil && (c.Database.URL == "" || (c.Engine() == EnginePostgres && c.Blob.Dir == "")) {
		return nil, err
	}
	c.dataDir = dir
	return &c, nil
}

func (c *Config) validate() error {
	switch c.Database.Synchronous {
	case "FULL", "NORMAL":
	default:
		return fmt.Errorf("%s: database.synchronous %q (want FULL|NORMAL)", RuleEnum, c.Database.Synchronous)
	}
	if r := c.Retention; min(r.ChangeLogDays, r.ToolCallsDays, r.DeliveriesDays, r.RawBodiesDays, r.SweepIntervalSeconds) <= 0 ||
		max(r.ChangeLogDays, r.ToolCallsDays, r.DeliveriesDays, r.RawBodiesDays) > MaxRetentionDays || r.SweepIntervalSeconds > MaxSweepIntervalSeconds {
		return fmt.Errorf("%s: retention change_log_days %d, tool_calls_days %d, deliveries_days %d, raw_bodies_days %d (each 1 to %d), sweep_interval_seconds %d (1 to %d)",
			RuleRetention, r.ChangeLogDays, r.ToolCallsDays, r.DeliveriesDays, r.RawBodiesDays, MaxRetentionDays, r.SweepIntervalSeconds, MaxSweepIntervalSeconds)
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
	if _, err := ParseTrustedProxies(c.TrustedProxies); err != nil {
		return err
	}
	if c.PublicURL != "" {
		origin, err := publicOrigin(c.PublicURL)
		if err != nil {
			return err
		}
		c.PublicURL = origin
	}
	// database.url may carry the database password, so no message here quotes it.
	if c.Engine() == EnginePostgres {
		if _, _, ok := postgresHostDB(c.Database.URL); !ok {
			return fmt.Errorf("%s: database.url is a postgres:// URL whose user:password@host/database does not split one way: percent-encode each '@' (%%40) and '/' (%%2F) in the user, the password or a parameter (the URL is not shown: it may hold a password)", RuleDatabaseURL)
		}
	} else if scheme, _, found := strings.Cut(c.Database.URL, "://"); found {
		if urlScheme.MatchString(scheme) {
			return fmt.Errorf("%s: database.url is a %q URL, neither a file path nor a postgres:// (postgresql://) URL (the rest is not shown: it may hold a password)", RuleDatabaseURL, scheme+"://")
		}
		return fmt.Errorf("%s: database.url holds \"://\" but is neither a file path nor a postgres:// (postgresql://) URL (it is not shown: it may hold a password)", RuleDatabaseURL)
	}
	return nil
}

// urlScheme is RFC 3986's scheme, short enough to be one: what a refused database.url is named by.
var urlScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]{0,31}$`)

// postgresHostDB splits a postgres:// URL the way libpq and pgx read it: the userinfo is what comes
// before the first '@' ahead of the first '/', the host list runs from there to the first '/' or '?',
// and the database from that '/' to the '?'. ok is false unless the URL holds at most one '@', ahead
// of that '/': otherwise the split may put part of a password in the host or the database (an
// unencoded '@' or '/' in it), where an error message or a log line would show it.
func postgresHostDB(raw string) (host, db string, ok bool) {
	_, rest, _ := strings.Cut(raw, "://")
	authority, path, _ := strings.Cut(rest, "/")
	if strings.Count(rest, "@") > 1 || strings.Contains(path, "@") {
		return "", "", false
	}
	if _, after, found := strings.Cut(authority, "@"); found {
		authority = after
	}
	host, _, _ = strings.Cut(authority, "?")
	db, _, _ = strings.Cut(path, "?")
	return host, db, true
}

// PostgresHostDB is the host (with its port) and the database a postgres:// database.url names, as
// libpq and pgx read it; never its user or password. It is meaningful only when Engine is postgres.
func (c *Config) PostgresHostDB() (host, db string) {
	host, db, _ = postgresHostDB(c.Database.URL)
	return host, db
}

// ParseTrustedProxies reads trusted_proxies: each entry an IP address (its own /32 or /128) or a
// CIDR prefix, masked; an IPv4-mapped IPv6 address is its IPv4 address. Anything else is refused
// under RuleTrustedProxies, naming the entry.
func ParseTrustedProxies(entries []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, e := range entries {
		e = strings.TrimSpace(e)
		var p netip.Prefix
		var err error
		if strings.Contains(e, "/") {
			p, err = netip.ParsePrefix(e)
			p = p.Masked()
		} else {
			var a netip.Addr
			if a, err = netip.ParseAddr(e); err == nil {
				if a.Zone() != "" {
					err = fmt.Errorf("a zone is not allowed")
				}
				a = a.Unmap()
				p = netip.PrefixFrom(a, a.BitLen())
			}
		}
		if err != nil {
			return nil, fmt.Errorf("%s: trusted_proxies entry %q is neither an IP address nor a CIDR prefix such as 10.0.0.0/8", RuleTrustedProxies, e)
		}
		out = append(out, p)
	}
	return out, nil
}

// publicOrigin checks public_url the way the OAuth server does (internal/auth/oauth issuerOf), where
// the config is loaded, so every subcommand refuses a bad one before it touches the store or binds a
// listener: an http or https URL with a host and no path, query, fragment or userinfo. A trailing
// slash is dropped, so the issuer and the attachment links start with the same base. An opaque URL
// (`https:x.example`) has no host, so the Hostname check refuses it.
func publicOrigin(publicURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(publicURL, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Path != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("%s: public_url %q is not scheme://host[:port] (http or https, no path, query, fragment or userinfo)", RulePublicURL, publicURL)
	}
	return u.Scheme + "://" + u.Host, nil
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

// SQLitePath is the SQLite file: database.url, or DefaultDBFile in the default data dir when it
// is unset. It is meaningful only when Engine is sqlite.
func (c *Config) SQLitePath() string {
	if c.Database.URL == "" {
		return filepath.Join(c.dataDir, DefaultDBFile)
	}
	return c.Database.URL
}

// BlobDir is the blob directory: blob.dir, else "blobs" beside the SQLite file (on Postgres, in
// the default data dir).
func (c *Config) BlobDir() string {
	if c.Blob.Dir != "" {
		return c.Blob.Dir
	}
	if c.Engine() != EngineSQLite {
		return filepath.Join(c.dataDir, "blobs")
	}
	return filepath.Join(filepath.Dir(c.SQLitePath()), "blobs")
}
