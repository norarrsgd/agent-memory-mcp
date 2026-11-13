package sqlite

import (
	"io"
	"os"
	"path/filepath"

	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
)

// WritePrivateFile writes a new 0600 file in a private directory. The content is fully written and synced
// before it is published, and an existing destination is never overwritten.
func WritePrivateFile(path string, write func(io.Writer) error) error {
	if err := PrivatePath(path, true); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".agent-memory-*")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	if err = write(f); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Link(temporary, path); err != nil {
		return domain.Err("conflict", "destination already exists or cannot be published")
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
