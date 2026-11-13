package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/norarrsgd/agent-memory-mcp/internal/claudehook"
	"github.com/norarrsgd/agent-memory-mcp/internal/config"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"github.com/norarrsgd/agent-memory-mcp/internal/mcpserver"
	"github.com/norarrsgd/agent-memory-mcp/internal/memory"
	"github.com/norarrsgd/agent-memory-mcp/internal/snapshot"
	"github.com/norarrsgd/agent-memory-mcp/internal/storage/sqlite"
)

const usage = `agent-memory <command> [options]
Commands: serve, doctor, status, backup, export, import, view, projects register/list, hook (Claude Code hook; reads stdin)
Run agent-memory <command> --help for command options.
`

func Run(ctx context.Context, args []string, out, diagnostics io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, usage)
		return 0
	}
	command := args[0]
	args = args[1:]
	if command == "hook" {
		// Stateless Claude Code hook: reads stdin and the transcript only, never opens the database.
		if claudehook.Run(os.Stdin, out) != nil {
			fmt.Fprintln(diagnostics, "hook output failed")
			return 1
		}
		return 0
	}
	action := ""
	if command == "projects" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}
	switch command {
	case "serve", "doctor", "status", "backup", "export", "import", "view", "projects":
	default:
		fmt.Fprintln(diagnostics, "unknown command; use --help")
		return 2
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(out)
	configPath := fs.String("config", "", "optional JSON configuration path")
	dbPath := fs.String("db-path", "", "absolute database path")
	var output, input, key, remote, local, organization, description string
	var searchLimit, response, busy int
	var rescan, flagged, inactive bool
	var logLevel string
	fs.IntVar(&searchLimit, "search-limit", 0, "default search result count (1..20)")
	fs.IntVar(&response, "max-response-chars", 0, "response ceiling (1024..32000)")
	fs.IntVar(&busy, "busy-timeout-ms", 0, "SQLite busy timeout (1..30000)")
	fs.StringVar(&logLevel, "log-level", "", "error, info or debug")
	if command == "backup" || command == "export" || command == "view" {
		fs.StringVar(&output, "output", "", "absolute private output path; never overwritten")
	}
	if command == "view" {
		fs.StringVar(&key, "project", "", "limit the snapshot to one registered project key")
		fs.BoolVar(&inactive, "include-inactive", false, "also include superseded, archived and expired records")
	}
	if command == "doctor" {
		fs.BoolVar(&rescan, "scan", false, "also rescan stored records for credential patterns; reports IDs only and exits 1 if any match")
	}
	if command == "import" {
		fs.StringVar(&input, "input", "", "absolute path of a private JSON export")
	}
	if command == "projects" {
		fs.StringVar(&key, "key", "", "stable project key")
		fs.StringVar(&remote, "remote-url", "", "repository remote URL (metadata only)")
		fs.StringVar(&local, "local-path", "", "absolute repository path (metadata only)")
		fs.StringVar(&organization, "organization", "", "organization")
		fs.StringVar(&description, "description", "", "project description")
	}
	fs.Usage = func() {
		fmt.Fprintf(out, "Usage: agent-memory %s", command)
		if command == "projects" {
			fmt.Fprint(out, " register|list")
		}
		fmt.Fprintln(out, " [options]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(diagnostics, "invalid arguments; use --help")
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(diagnostics, "unexpected positional arguments")
		return 2
	}
	if command == "projects" && action != "register" && action != "list" {
		fmt.Fprintln(diagnostics, "projects requires register or list")
		return 2
	}
	c, err := config.Load(*configPath, func(c *config.Config) {
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "db-path":
				c.DBPath = *dbPath
			case "search-limit":
				c.SearchLimit = searchLimit
			case "max-response-chars":
				c.MaxResponseChars = response
			case "busy-timeout-ms":
				c.BusyTimeoutMS = busy
			case "log-level":
				c.LogLevel = logLevel
			}
		})
	})
	if err != nil {
		fmt.Fprintln(diagnostics, "configuration error:", err)
		return 2
	}
	store, err := sqlite.OpenLimited(c.DBPath, c.BusyTimeoutMS, c.MaxDBMB)
	if err != nil {
		fmt.Fprintln(diagnostics, domain.SafeError(err))
		return 1
	}
	defer store.Close()
	svc := memory.New(store, c)
	var result any
	switch command {
	case "serve":
		level := slog.LevelError
		if c.LogLevel == "info" {
			level = slog.LevelInfo
		}
		if c.LogLevel == "debug" {
			level = slog.LevelDebug
		}
		logger := slog.New(slog.NewJSONHandler(diagnostics, &slog.HandlerOptions{Level: level}))
		logger.Info("starting stdio memory server")
		err = mcpserver.New(svc, c.MaxResponseChars).Run(ctx, &mcp.StdioTransport{MaxLineLength: config.MaxInputBytes + 16384})
		if errors.Is(err, context.Canceled) {
			err = nil
		}
	case "doctor":
		var report map[string]any
		report, err = store.Doctor(ctx)
		if err == nil && rescan {
			var audit domain.AuditResult
			audit, err = svc.Audit(ctx)
			report["audit"] = audit
			flagged = len(audit.FlaggedMemories)+len(audit.FlaggedProjects) > 0
		}
		result = report
	case "status":
		result, err = store.Status(ctx)
	case "projects":
		if action == "register" {
			err = svc.RegisterProject(ctx, domain.Project{Key: key, RemoteURL: remote, LocalPath: local, Organization: organization, Description: description})
			result = map[string]any{"registered": key}
		} else {
			result, err = svc.Projects(ctx, domain.ProjectsInput{})
		}
	case "backup", "export":
		if output == "" {
			dir := c.BackupDir
			extension := ".db"
			if command == "export" {
				dir = c.ExportDir
				extension = ".json"
			}
			if dir == "" {
				dir = filepath.Join(filepath.Dir(c.DBPath), command+"s")
			}
			output = filepath.Join(dir, time.Now().UTC().Format("20060102T150405.000000000Z")+extension)
		}
		if command == "backup" {
			err = store.Backup(ctx, output)
		} else {
			var snapshot domain.Export
			snapshot, err = store.Export(ctx)
			if err == nil {
				err = writeExport(output, snapshot)
			}
		}
		result = map[string]any{"output": output}
	case "view":
		result, err = snapshot.Write(ctx, svc, c.DBPath, output, snapshot.Options{ProjectKey: key, IncludeInactive: inactive}, time.Now())
	case "import":
		var snapshot domain.Export
		snapshot, err = readExport(input)
		if err == nil {
			err = svc.Import(ctx, snapshot)
		}
		result = map[string]any{"imported": err == nil}
	}
	if err != nil {
		fmt.Fprintln(diagnostics, domain.SafeError(err))
		return 1
	}
	if result != nil {
		if err = json.NewEncoder(out).Encode(result); err != nil {
			fmt.Fprintln(diagnostics, "output failed")
			return 1
		}
	}
	if flagged {
		fmt.Fprintln(diagnostics, "stored records match credential patterns; see audit")
		return 1
	}
	return 0
}
func writeExport(path string, data domain.Export) error {
	return sqlite.WritePrivateFile(path, func(w io.Writer) error { return json.NewEncoder(w).Encode(data) })
}
func readExport(path string) (domain.Export, error) {
	var data domain.Export
	if err := sqlite.PrivatePath(path, false); err != nil {
		return data, err
	}
	f, err := os.Open(path)
	if err != nil {
		return data, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return data, err
	}
	if info.Size() > 100*1024*1024 {
		return data, domain.Err("validation", "import exceeds 100 MiB")
	}
	dec := json.NewDecoder(io.LimitReader(f, 100*1024*1024+1))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&data); err != nil {
		return data, domain.Err("validation", "invalid export JSON")
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		return data, domain.Err("validation", "export must contain one JSON object")
	}
	return data, nil
}
