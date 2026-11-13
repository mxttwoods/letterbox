// Command letterbox is a suggestion box for coding agents. Agents file
// letters through `letterbox mcp`; the owner reads them with the other
// commands.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mxttwoods/letterbox/internal/server"
	"github.com/mxttwoods/letterbox/internal/store"
)

const usage = `letterbox — a suggestion box for your coding agents

Reading:
  letterbox inbox [--all] [--status S] [--kind K] [--since 7d]   list letters (new ones by default)
  letterbox show <id>...                                          read letters in full
  letterbox export [--status S] [--since 30d] [--format md|json]  print letters to hand to an agent

Sorting:
  letterbox mark --status S [--note "…"] <id>...   S: read, planned, in_progress, done, dismissed, new
  letterbox delete [--yes] <id>...                 remove letters for good

Other:
  letterbox send --message "…" [--kind K] [--about A] [--impact I] [--suggestion S]
  letterbox backup <file>       write a safe copy of the database
  letterbox path                print where the database lives
  letterbox mcp [--reader]      run the MCP server (agents start this, not you)
  letterbox version

Statuses: new → read → planned → in_progress → done, or dismissed.
Agents may build only from letters you have marked planned.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}
	cmd, rest := args[0], args[1:]

	var err error
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
	case "version", "--version":
		fmt.Fprintln(stdout, "letterbox", server.Version)
	case "path":
		var p string
		if p, err = store.DefaultPath(); err == nil {
			fmt.Fprintln(stdout, p)
		}
	case "mcp":
		err = runMCP(rest, stderr)
	case "inbox", "show", "mark", "export", "delete", "backup", "send":
		err = withStore(func(st *store.Store) error {
			c := &cli{store: st, stdin: stdin, out: stdout, errOut: stderr}
			switch cmd {
			case "inbox":
				return c.inbox(rest)
			case "show":
				return c.show(rest)
			case "mark":
				return c.mark(rest)
			case "export":
				return c.export(rest)
			case "delete":
				return c.delete(rest)
			case "backup":
				return c.backup(rest)
			default:
				return c.send(rest)
			}
		})
	default:
		fmt.Fprintf(stderr, "letterbox: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err == flag.ErrHelp {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "letterbox:", err)
		return 1
	}
	return 0
}

func withStore(fn func(*store.Store) error) error {
	path, err := store.DefaultPath()
	if err != nil {
		return err
	}
	st, err := store.Open(path)
	if err != nil {
		return err
	}
	defer st.Close()
	return fn(st)
}

func runMCP(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	reader := fs.Bool("reader", false, "expose the tools for reading letters instead of send_letter")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withStore(func(st *store.Store) error {
		srv := server.New(server.Config{
			Store:   st,
			Agent:   os.Getenv("LETTERBOX_AGENT"),
			Project: detectProject(),
			Reader:  *reader,
		})
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		// stdout is the protocol channel; nothing else may write to it.
		err := srv.Run(ctx, &mcp.StdioTransport{})
		if ctx.Err() != nil {
			return nil
		}
		return err
	})
}

// detectProject works out which project the agent is in. Agents start MCP
// servers in the directory the session was started from.
func detectProject() string {
	if p := os.Getenv("LETTERBOX_PROJECT"); p != "" {
		return p
	}
	if p := os.Getenv("CLAUDE_PROJECT_DIR"); p != "" {
		return gitRoot(p)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return gitRoot(cwd)
}

// gitRoot returns the repository containing dir, or dir itself if there is none.
func gitRoot(dir string) string {
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}
