package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const HardResponseChars = 32000
const MaxInputBytes = 256 * 1024

type Config struct {
	DBPath           string `json:"db_path"`
	SearchLimit      int    `json:"search_limit"`
	MaxResponseChars int    `json:"max_response_chars"`
	BusyTimeoutMS    int    `json:"busy_timeout_ms"`
	MaxDBMB          int    `json:"max_db_mb"`
	LogLevel         string `json:"log_level"`
	BackupDir        string `json:"backup_dir"`
	ExportDir        string `json:"export_dir"`
	// ViewerEnabled registers the optional memory_view tool, which writes HTML snapshots of stored memories.
	ViewerEnabled bool `json:"viewer_enabled"`
}

func Load(path string, overrides ...func(*Config)) (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	c := Config{DBPath: filepath.Join(home, ".local/share/agent-memory/memory.db"), SearchLimit: 5, MaxResponseChars: 8000, BusyTimeoutMS: 5000, MaxDBMB: 1024, LogLevel: "error"}
	explicit := path != ""
	if path == "" {
		path = os.Getenv("AGENT_MEMORY_CONFIG")
		explicit = path != ""
	}
	if path == "" {
		path = filepath.Join(home, ".config/agent-memory/config.json")
	}
	f, err := os.Open(path)
	if err == nil {
		defer f.Close()
		dec := json.NewDecoder(io.LimitReader(f, 65537))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return c, fmt.Errorf("invalid configuration: %w", err)
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			return c, fmt.Errorf("configuration must contain one JSON object")
		}
	} else if explicit || !os.IsNotExist(err) {
		return c, fmt.Errorf("configuration unavailable: %w", err)
	}
	for _, field := range []struct {
		key  string
		dest *string
	}{{"DB_PATH", &c.DBPath}, {"LOG_LEVEL", &c.LogLevel}, {"BACKUP_DIR", &c.BackupDir}, {"EXPORT_DIR", &c.ExportDir}} {
		if v, ok := os.LookupEnv("AGENT_MEMORY_" + field.key); ok {
			*field.dest = v
		}
	}
	for _, field := range []struct {
		key  string
		dest *int
	}{{"SEARCH_LIMIT", &c.SearchLimit}, {"MAX_RESPONSE_CHARS", &c.MaxResponseChars}, {"BUSY_TIMEOUT_MS", &c.BusyTimeoutMS}, {"MAX_DB_MB", &c.MaxDBMB}} {
		if v, ok := os.LookupEnv("AGENT_MEMORY_" + field.key); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				return c, fmt.Errorf("invalid AGENT_MEMORY_%s", field.key)
			}
			*field.dest = n
		}
	}
	if v, ok := os.LookupEnv("AGENT_MEMORY_VIEWER_ENABLED"); ok {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return c, fmt.Errorf("invalid AGENT_MEMORY_VIEWER_ENABLED")
		}
		c.ViewerEnabled = enabled
	}
	for _, override := range overrides {
		override(&c)
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.DBPath == "" || strings.HasPrefix(c.DBPath, "file:") || strings.ContainsRune(c.DBPath, 0) || !filepath.IsAbs(c.DBPath) {
		return fmt.Errorf("db_path must be an absolute filesystem path")
	}
	if c.SearchLimit < 1 || c.SearchLimit > 20 {
		return fmt.Errorf("search_limit must be 1..20")
	}
	if c.MaxResponseChars < 1024 || c.MaxResponseChars > HardResponseChars {
		return fmt.Errorf("max_response_chars must be 1024..32000")
	}
	if c.BusyTimeoutMS < 1 || c.BusyTimeoutMS > 30000 {
		return fmt.Errorf("busy_timeout_ms must be 1..30000")
	}
	if c.MaxDBMB < 16 || c.MaxDBMB > 65536 {
		return fmt.Errorf("max_db_mb must be 16..65536")
	}
	if c.LogLevel != "error" && c.LogLevel != "info" && c.LogLevel != "debug" {
		return fmt.Errorf("log_level must be error, info or debug")
	}
	for _, p := range []string{c.BackupDir, c.ExportDir} {
		if p != "" && (!filepath.IsAbs(p) || strings.ContainsRune(p, 0)) {
			return fmt.Errorf("destination directory must be absolute")
		}
	}
	return nil
}
