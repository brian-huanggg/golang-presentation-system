package platform

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Config is read once at startup from environment variables (Architecture 9.3).
// Secrets are never committed; see .env.example for the names.
type Config struct {
	HTTPAddr string
	LogLevel slog.Level

	DB    DBConfig
	MinIO MinIOConfig
	// ClamAVAddr is host:port of clamd.
	ClamAVAddr string
	// AdminsFile is the path of the admin seed JSON (Vault-supplied in production).
	AdminsFile string

	// EventYear and EventDeadline come from configuration, not the database (ADR 0014).
	EventYear     int
	EventDeadline time.Time

	// SessionIdleTimeout and SessionLifetime are AUTH-005 (30 minutes idle, 8 hours
	// absolute); configurable only so tests and staging can shorten them (ADR 0007).
	SessionIdleTimeout time.Duration
	SessionLifetime    time.Duration
}

type DBConfig struct {
	Host, Name, User string
	Port             int
	Password         string
}

type MinIOConfig struct {
	Endpoint, Bucket, AccessKey, SecretKey string
	UseSSL                                 bool
}

// DSN builds the go-sql-driver/mysql DSN. It contains the password: never log it.
func (d DBConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&loc=UTC&charset=utf8mb4",
		d.User, d.Password, d.Host, d.Port, d.Name)
}

// String and GoString redact secrets so a stray %v or %+v cannot leak them.
func (d DBConfig) String() string   { return fmt.Sprintf("%s@%s:%d/%s", d.User, d.Host, d.Port, d.Name) }
func (d DBConfig) GoString() string { return d.String() }
func (m MinIOConfig) String() string {
	return fmt.Sprintf("%s bucket=%s ssl=%t", m.Endpoint, m.Bucket, m.UseSSL)
}
func (m MinIOConfig) GoString() string { return m.String() }

// Load reads configuration through getenv (os.Getenv in production, a map in
// tests). It reports every problem at once so a misconfigured deploy is fixed
// in one pass. Startup must fail on a non-nil error.
func Load(getenv func(string) string) (Config, error) {
	var errs []error
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}
	require := func(key string) string {
		v := strings.TrimSpace(getenv(key))
		if v == "" {
			errs = append(errs, fmt.Errorf("%s is required", key))
		}
		return v
	}
	port := func(key, def string) int {
		n, err := strconv.Atoi(get(key, def))
		if err != nil || n < 1 || n > 65535 {
			errs = append(errs, fmt.Errorf("%s must be a port number (1-65535)", key))
		}
		return n
	}

	var c Config
	c.HTTPAddr = get("HTTP_ADDR", ":8080")

	lvl, ok := ParseLogLevel(get("LOG_LEVEL", "info"))
	if !ok {
		errs = append(errs, errors.New("LOG_LEVEL must be one of debug, info, warn, error"))
	}
	c.LogLevel = lvl

	c.DB = DBConfig{
		Host:     get("MARIADB_HOST", "127.0.0.1"),
		Port:     port("MARIADB_PORT", "3306"),
		Name:     require("MARIADB_DATABASE"),
		User:     require("MARIADB_USER"),
		Password: require("MARIADB_PASSWORD"),
	}
	c.MinIO = MinIOConfig{
		Endpoint:  require("MINIO_ENDPOINT"),
		Bucket:    get("MINIO_BUCKET", "esg-ideas"),
		AccessKey: require("MINIO_ACCESS_KEY"),
		SecretKey: require("MINIO_SECRET_KEY"),
	}
	if v := get("MINIO_USE_SSL", "false"); v != "true" && v != "false" {
		errs = append(errs, errors.New("MINIO_USE_SSL must be true or false"))
	} else {
		c.MinIO.UseSSL = v == "true"
	}
	c.ClamAVAddr = get("CLAMAV_ADDR", "127.0.0.1:3310")
	c.AdminsFile = require("ADMINS_FILE")

	year, err := strconv.Atoi(strings.TrimSpace(getenv("EVENT_YEAR")))
	if err != nil || year < 2000 || year > 2999 {
		errs = append(errs, errors.New("EVENT_YEAR is required and must be a four-digit year"))
	}
	c.EventYear = year

	deadline, err := parseDeadline(getenv("EVENT_DEADLINE"))
	if err != nil {
		errs = append(errs, err)
	}
	c.EventDeadline = deadline

	duration := func(key, def string) time.Duration {
		d, err := time.ParseDuration(get(key, def))
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("%s must be a positive duration such as 30m", key))
		}
		return d
	}
	c.SessionIdleTimeout = duration("SESSION_IDLE_TIMEOUT", "30m")
	c.SessionLifetime = duration("SESSION_LIFETIME", "8h")
	if c.SessionIdleTimeout > 0 && c.SessionLifetime > 0 && c.SessionIdleTimeout > c.SessionLifetime {
		errs = append(errs, errors.New("SESSION_IDLE_TIMEOUT must not exceed SESSION_LIFETIME"))
	}

	return c, errors.Join(errs...)
}

// parseDeadline demands RFC 3339 with an explicit numeric offset (ADR 0014); a
// bare "Z" or a missing offset is ambiguous for a Taipei deadline, so reject it.
// The result is converted to UTC for internal use.
func parseDeadline(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("EVENT_DEADLINE is required (RFC 3339 with an explicit offset, e.g. 2026-12-31T23:59:59+08:00)")
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("EVENT_DEADLINE is not valid RFC 3339: %w", err)
	}
	if strings.HasSuffix(s, "Z") || strings.HasSuffix(s, "z") {
		return time.Time{}, errors.New("EVENT_DEADLINE must carry an explicit offset such as +08:00, not Z")
	}
	return t.UTC(), nil
}
