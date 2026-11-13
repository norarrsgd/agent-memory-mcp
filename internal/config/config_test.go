package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAndPrecedence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENT_MEMORY_DB_PATH", filepath.Join(t.TempDir(), "env.db"))
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.SearchLimit != 5 || c.MaxResponseChars != 8000 || c.BusyTimeoutMS != 5000 || c.MaxDBMB != 1024 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.DBPath != os.Getenv("AGENT_MEMORY_DB_PATH") {
		t.Fatal("environment not applied")
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, body := range []string{`{"unknown":true}`, `{"search_limit":21}`, `{"max_response_chars":20}`, `{"max_db_mb":8}`, `{"max_db_mb":70000}`, `{"db_path":"file:unsafe"}`, `{} {}`} {
		t.Run(body, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(p, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(p); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestViewerEnabledDefaultsOffAndParsesEnvironment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c, err := Load("")
	if err != nil || c.ViewerEnabled {
		t.Fatalf("viewer must default to off: %+v %v", c, err)
	}
	t.Setenv("AGENT_MEMORY_VIEWER_ENABLED", "true")
	if c, err = Load(""); err != nil || !c.ViewerEnabled {
		t.Fatalf("environment did not enable the viewer: %+v %v", c, err)
	}
	t.Setenv("AGENT_MEMORY_VIEWER_ENABLED", "maybe")
	if _, err = Load(""); err == nil {
		t.Fatal("unparsable value accepted")
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err = os.WriteFile(p, []byte(`{"viewer_enabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_MEMORY_VIEWER_ENABLED", "false")
	if c, err = Load(p); err != nil || c.ViewerEnabled {
		t.Fatalf("environment must override the file: %+v %v", c, err)
	}
}
