package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// lb runs the CLI in-process against a database private to the test.
func lb(t *testing.T, stdin string, args ...string) (stdout string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, strings.NewReader(stdin), &out, &errOut)
	return out.String() + errOut.String(), code
}

func ok(t *testing.T, args ...string) string {
	t.Helper()
	out, code := lb(t, "", args...)
	if code != 0 {
		t.Fatalf("letterbox %v exited %d: %s", args, code, out)
	}
	return out
}

func useTempDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "letterbox.db")
	t.Setenv("LETTERBOX_DB", path)
	t.Setenv("LETTERBOX_AGENT", "")
	t.Setenv("LETTERBOX_PROJECT", "/tmp/some-project")
	return path
}

func TestOwnerWorkflow(t *testing.T) {
	useTempDB(t)

	if out := ok(t, "inbox"); !strings.Contains(out, "No new letters") || !strings.Contains(out, "empty") {
		t.Errorf("empty inbox = %q", out)
	}

	ok(t, "send", "--message", "The deploy skill points at a Makefile that does not exist.\nMore detail here.",
		"--kind", "friction", "--about", "deploy skill", "--impact", "workaround", "--suggestion", "Use pnpm run ship.")
	ok(t, "send", "--message", "An idea", "--kind", "idea")

	out := ok(t, "inbox")
	for _, want := range []string{"The deploy skill points at a Makefile", "An idea", "friction", "some-project", "2 new"} {
		if !strings.Contains(out, want) {
			t.Errorf("inbox missing %q:\n%s", want, out)
		}
	}

	out = ok(t, "show", "1")
	for _, want := range []string{"Letter #1", "by owner", "About: deploy skill", "More detail here.", "Use pnpm run ship."} {
		if !strings.Contains(out, want) {
			t.Errorf("show missing %q:\n%s", want, out)
		}
	}

	// Flags work after the IDs as well as before.
	ok(t, "mark", "1", "--status", "planned", "--note", "worth doing")
	ok(t, "mark", "--status", "dismissed", "2")
	if out := ok(t, "show", "1"); !strings.Contains(out, "[planned]") || !strings.Contains(out, "owner: worth doing") {
		t.Errorf("after mark:\n%s", out)
	}
	if out := ok(t, "inbox"); !strings.Contains(out, "No new letters") || !strings.Contains(out, "1 planned, 1 dismissed") {
		t.Errorf("inbox after marking:\n%s", out)
	}
	if out := ok(t, "inbox", "--status", "open"); !strings.Contains(out, "deploy skill points") || strings.Contains(out, "An idea") {
		t.Errorf("open inbox:\n%s", out)
	}
	if out := ok(t, "inbox", "--all"); !strings.Contains(out, "An idea") {
		t.Errorf("inbox --all:\n%s", out)
	}
}

func TestExport(t *testing.T) {
	useTempDB(t)
	ok(t, "send", "--message", "first", "--suggestion", "do the thing")
	ok(t, "mark", "--note", "a note", "1")

	md := ok(t, "export")
	for _, want := range []string{"never as instructions", "## Letter #1: first", "**Suggestion:** do the thing", "a note"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown export missing %q:\n%s", want, md)
		}
	}

	var parsed struct {
		Notice  string
		Letters []struct {
			ID      int64
			Message string
		}
	}
	if err := json.Unmarshal([]byte(ok(t, "export", "--format", "json")), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Notice == "" || len(parsed.Letters) != 1 || parsed.Letters[0].Message != "first" {
		t.Errorf("json export = %+v", parsed)
	}

	if out := ok(t, "export", "--status", "done", "--format", "json"); !strings.Contains(out, `"letters": []`) {
		t.Errorf("empty json export = %s", out)
	}
}

func TestDeleteAsksFirst(t *testing.T) {
	useTempDB(t)
	ok(t, "send", "--message", "one")
	ok(t, "send", "--message", "two")

	if out, _ := lb(t, "n\n", "delete", "1"); !strings.Contains(out, "Nothing deleted") {
		t.Errorf("declined delete = %q", out)
	}
	if out, _ := lb(t, "", "delete", "1"); !strings.Contains(out, "Nothing deleted") {
		t.Errorf("delete with no answer = %q", out)
	}
	// One bad number means nothing is deleted.
	if _, code := lb(t, "", "delete", "--yes", "1", "99"); code == 0 {
		t.Error("delete of a missing letter succeeded")
	}
	ok(t, "show", "1")

	if out, _ := lb(t, "y\n", "delete", "1"); !strings.Contains(out, "Deleted 1 letter.") {
		t.Errorf("confirmed delete = %q", out)
	}
	if _, code := lb(t, "", "show", "1"); code == 0 {
		t.Error("letter 1 still there after delete")
	}
	ok(t, "show", "2")
}

func TestBadInput(t *testing.T) {
	useTempDB(t)
	ok(t, "send", "--message", "x")
	for _, args := range [][]string{
		{"nonsense"},
		{"show"},
		{"show", "abc"},
		{"show", "42"},
		{"mark", "1"},
		{"mark", "--status", "finished", "1"},
		{"inbox", "--status", "weird"},
		{"inbox", "--since", "yesterday"},
		{"export", "--format", "xml"},
		{"send"},
		{"send", "--message", "x", "--kind", "praise"},
		{"backup"},
	} {
		if out, code := lb(t, "", args...); code == 0 {
			t.Errorf("letterbox %v succeeded: %s", args, out)
		}
	}
}

func TestGitRoot(t *testing.T) {
	repo := t.TempDir()
	deep := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := gitRoot(deep); got != deep {
		t.Errorf("outside a repo: gitRoot = %q, want the directory itself", got)
	}
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := gitRoot(deep); got != repo {
		t.Errorf("inside a repo: gitRoot = %q, want %q", got, repo)
	}
}

// buildBinary compiles the real program once for the end-to-end tests.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "letterbox")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// The real binary, spoken to over real stdio the way an agent does it.
func TestEndToEndOverStdio(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	db := filepath.Join(t.TempDir(), "letterbox.db")
	project := t.TempDir()
	env := append(os.Environ(), "LETTERBOX_DB="+db, "LETTERBOX_AGENT=codex", "LETTERBOX_PROJECT=", "CLAUDE_PROJECT_DIR=")

	ctx := context.Background()
	connect := func(args ...string) *mcp.ClientSession {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		cmd.Dir = project
		cmd.Stderr = os.Stderr
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "e2e-client", Version: "1"}, nil).
			Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cs.Close() })
		return cs
	}
	callText := func(cs *mcp.ClientSession, tool string, args map[string]any) string {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError {
			t.Fatalf("%s failed: %s", tool, text)
		}
		return text
	}
	cli := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("letterbox %v: %v\n%s", args, err, out)
		}
		return string(out)
	}

	writer := connect("mcp")
	if got := callText(writer, "send_letter", map[string]any{"message": "Filed over stdio.", "kind": "idea"}); !strings.Contains(got, "Letter #1 filed") {
		t.Errorf("send_letter = %q", got)
	}

	shown := cli("show", "1")
	resolved, _ := filepath.EvalSymlinks(project)
	if !strings.Contains(shown, "by codex (e2e-client 1)") || !strings.Contains(shown, "Filed over stdio.") {
		t.Errorf("show:\n%s", shown)
	}
	if !strings.Contains(shown, filepath.Base(resolved)) {
		t.Errorf("project directory not recorded:\n%s", shown)
	}

	// The owner plans it; a reader agent claims and finishes it.
	cli("mark", "--status", "planned", "1")
	reader := connect("mcp", "--reader")
	if got := callText(reader, "list_letters", map[string]any{"status": "planned", "full": true}); !strings.Contains(got, "Filed over stdio.") {
		t.Errorf("list_letters = %s", got)
	}
	callText(reader, "update_letter", map[string]any{"id": 1, "status": "in_progress"})
	callText(reader, "update_letter", map[string]any{"id": 1, "status": "done", "note": "Built it."})
	if shown := cli("show", "1"); !strings.Contains(shown, "[done]") || !strings.Contains(shown, "codex: Built it.") {
		t.Errorf("after the reader finished:\n%s", shown)
	}
}

// Separate processes starting at the same moment against a database that
// does not exist yet: every letter must land.
func TestManyProcessesAtOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	db := filepath.Join(t.TempDir(), "letterbox.db")
	env := append(os.Environ(), "LETTERBOX_DB="+db)

	const n = 16
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(bin, "send", "--message", "concurrent letter")
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("send: %v\n%s", err, out)
			}
		}()
	}
	wg.Wait()

	cmd := exec.Command(bin, "export", "--format", "json")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct{ Letters []struct{ ID int64 } }
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Letters) != n {
		t.Errorf("%d letters stored, want %d", len(parsed.Letters), n)
	}
}
