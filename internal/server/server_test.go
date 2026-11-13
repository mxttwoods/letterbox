package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mxttwoods/letterbox/internal/store"
)

type harness struct {
	t      *testing.T
	store  *store.Store
	client *mcp.ClientSession
	now    time.Time
}

// connect starts a server for cfg and a client talking to it in memory.
func connect(t *testing.T, cfg Config, clientName string) *harness {
	t.Helper()
	h := &harness{t: t, now: time.Now()}
	if cfg.Store == nil {
		st, err := store.Open(filepath.Join(t.TempDir(), "letterbox.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		cfg.Store = st
	}
	h.store = cfg.Store
	cfg.Now = func() time.Time { return h.now }

	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := New(cfg).Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: "9.9"}, nil)
	h.client, err = client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.client.Close() })
	return h
}

// call returns the tool's text and whether the result was an error.
func (h *harness) call(tool string, args map[string]any) (string, bool) {
	h.t.Helper()
	res, err := h.client.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		// Schema rejections may surface as protocol errors; treat them as failures too.
		return err.Error(), true
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), res.IsError
}

func (h *harness) mustCall(tool string, args map[string]any) string {
	h.t.Helper()
	out, isErr := h.call(tool, args)
	if isErr {
		h.t.Fatalf("%s(%v) failed: %s", tool, args, out)
	}
	return out
}

func (h *harness) toolNames() []string {
	h.t.Helper()
	res, err := h.client.ListTools(context.Background(), nil)
	if err != nil {
		h.t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestWriterExposesOnlySendLetter(t *testing.T) {
	h := connect(t, Config{}, "some-client")
	if got := strings.Join(h.toolNames(), ","); got != "send_letter" {
		t.Errorf("writer tools = %s, want only send_letter", got)
	}

	res, err := h.client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Tools[0].Meta["anthropic/alwaysLoad"] != true {
		t.Errorf("send_letter _meta = %v, want anthropic/alwaysLoad true", res.Tools[0].Meta)
	}
	if got := h.client.InitializeResult().Instructions; !strings.HasPrefix(got, "Use `send_letter`") {
		t.Errorf("instructions do not lead with the trigger: %q", got)
	}
}

func TestReaderExposesReadTools(t *testing.T) {
	h := connect(t, Config{Reader: true}, "some-client")
	if got := strings.Join(h.toolNames(), ","); got != "get_letter,list_letters,update_letter" {
		t.Errorf("reader tools = %s", got)
	}
}

func TestSendLetterStoresEverything(t *testing.T) {
	h := connect(t, Config{Agent: "codex", Project: "/tmp/proj"}, "codex-mcp-client")
	out := h.mustCall("send_letter", map[string]any{
		"message":    "The deploy skill told me to run make ship.\nThere is no Makefile.",
		"kind":       "friction",
		"about":      "deploy skill",
		"impact":     "workaround",
		"suggestion": "Point the skill at pnpm run ship.",
	})
	if !strings.Contains(out, "Letter #1 filed") {
		t.Errorf("reply = %q", out)
	}

	l, err := h.store.Get(1)
	if err != nil {
		t.Fatal(err)
	}
	if l.Agent != "codex" || l.ClientName != "codex-mcp-client" || l.ClientVersion != "9.9" {
		t.Errorf("agent=%q client=%q %q", l.Agent, l.ClientName, l.ClientVersion)
	}
	if l.Project != "/tmp/proj" || l.Kind != "friction" || l.Impact != "workaround" || l.About != "deploy skill" {
		t.Errorf("stored letter = %+v", l)
	}
	if l.Title != "The deploy skill told me to run make ship." {
		t.Errorf("title = %q", l.Title)
	}
	if l.Status != store.StatusNew || l.ProcessID == "" {
		t.Errorf("status=%q process=%q", l.Status, l.ProcessID)
	}
}

func TestSendLetterDefaultsAndClientNameFallback(t *testing.T) {
	h := connect(t, Config{}, "pi")
	h.mustCall("send_letter", map[string]any{"message": "just the message"})
	l, err := h.store.Get(1)
	if err != nil {
		t.Fatal(err)
	}
	if l.Agent != "pi" || l.Kind != "other" || l.Impact != "minor" {
		t.Errorf("agent=%q kind=%q impact=%q", l.Agent, l.Kind, l.Impact)
	}
}

func TestSendLetterRejectsBadInput(t *testing.T) {
	h := connect(t, Config{}, "c")
	bad := []map[string]any{
		{},
		{"message": ""},
		{"message": "   \n "},
		{"message": strings.Repeat("x", maxMessage+1)},
		{"message": "ok", "kind": "praise"},
		{"message": "ok", "impact": "huge"},
		{"message": "ok", "about": strings.Repeat("x", maxAbout+1)},
		{"message": "ok", "suggestion": strings.Repeat("x", maxSuggestion+1)},
	}
	for _, args := range bad {
		if out, isErr := h.call("send_letter", args); !isErr {
			t.Errorf("send_letter(%.60v) succeeded: %s", args, out)
		}
	}
	ls, err := h.store.List(store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 0 {
		t.Errorf("%d letters stored from bad input", len(ls))
	}
}

func TestSendLetterIgnoresUnknownFields(t *testing.T) {
	h := connect(t, Config{}, "c")
	h.mustCall("send_letter", map[string]any{"message": "ok", "model": "some-model", "title": "extra"})
	if _, err := h.store.Get(1); err != nil {
		t.Errorf("letter with extra fields was not stored: %v", err)
	}
}

func TestSendLetterScrubsAndSanitizes(t *testing.T) {
	h := connect(t, Config{}, "c")
	key := "sk-" + "ant-api03-abcdefghijklmnopqrstuvwxyz012345" // fake; split for secret scanners
	h.mustCall("send_letter", map[string]any{
		"message":    "The env file had \x1b[31m" + key + "\x1b[0m in it​.",
		"suggestion": "rotate " + key,
	})
	l, err := h.store.Get(1)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{l.Message, l.Suggestion, l.Title} {
		if strings.Contains(field, key) {
			t.Errorf("secret survived in %q", field)
		}
		if strings.ContainsAny(field, "\x1b​") {
			t.Errorf("escape or invisible character survived in %q", field)
		}
	}
}

func TestSendLetterHourlyCap(t *testing.T) {
	h := connect(t, Config{}, "c")
	for i := 0; i < lettersPerHour; i++ {
		h.mustCall("send_letter", map[string]any{"message": "letter"})
	}
	out, isErr := h.call("send_letter", map[string]any{"message": "one too many"})
	if !isErr || !strings.Contains(out, "limit") {
		t.Errorf("letter over the cap: isErr=%v out=%q", isErr, out)
	}

	// Rejected input does not use up the allowance, and the window rolls.
	h.now = h.now.Add(61 * time.Minute)
	h.call("send_letter", map[string]any{"message": ""})
	h.mustCall("send_letter", map[string]any{"message": "an hour later"})

	ls, err := h.store.List(store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != lettersPerHour+1 {
		t.Errorf("%d letters stored, want %d", len(ls), lettersPerHour+1)
	}
}

type listed struct {
	Notice  string `json:"notice"`
	More    string `json:"more"`
	Letters []struct {
		ID      int64  `json:"id"`
		Status  string `json:"status"`
		Title   string `json:"title"`
		Message string `json:"message"`
	} `json:"letters"`
}

func (h *harness) list(args map[string]any) listed {
	h.t.Helper()
	var out listed
	if err := json.Unmarshal([]byte(h.mustCall("list_letters", args)), &out); err != nil {
		h.t.Fatal(err)
	}
	return out
}

func seed(t *testing.T, st *store.Store, msg string) int64 {
	t.Helper()
	id, err := st.Insert(&store.Letter{Agent: "codex", ProcessID: "p", Kind: "idea", Impact: "minor", Title: Title(msg), Message: msg})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestListLetters(t *testing.T) {
	h := connect(t, Config{Reader: true, Agent: "claude-code"}, "c")
	a := seed(t, h.store, "first letter\nwith a body")
	b := seed(t, h.store, "second letter")
	if err := h.store.SetStatus(a, store.StatusDismissed); err != nil {
		t.Fatal(err)
	}

	out := h.list(nil)
	if out.Notice != UntrustedNotice {
		t.Errorf("notice = %q", out.Notice)
	}
	if len(out.Letters) != 1 || out.Letters[0].ID != b {
		t.Fatalf("default list = %+v, want only the open letter", out.Letters)
	}
	if out.Letters[0].Message != "" {
		t.Errorf("message included without full=true: %q", out.Letters[0].Message)
	}
	if out.Letters[0].Title != "second letter" {
		t.Errorf("title = %q", out.Letters[0].Title)
	}

	out = h.list(map[string]any{"status": "all", "full": true})
	if len(out.Letters) != 2 || out.Letters[1].Message != "first letter\nwith a body" {
		t.Errorf("full list = %+v", out.Letters)
	}

	if got := h.list(map[string]any{"status": "done"}); len(got.Letters) != 0 {
		t.Errorf("done list = %+v, want empty", got.Letters)
	}
	if out, isErr := h.call("list_letters", map[string]any{"since": "soon"}); !isErr {
		t.Errorf("bad since accepted: %s", out)
	}
}

func TestListLettersFullBudget(t *testing.T) {
	h := connect(t, Config{Reader: true}, "c")
	big := strings.Repeat("x", 3900)
	for i := 0; i < 10; i++ {
		seed(t, h.store, big)
	}
	out := h.list(map[string]any{"full": true})
	with := 0
	for _, l := range out.Letters {
		if l.Message != "" {
			with++
		}
	}
	if with != fullBudget/3900 {
		t.Errorf("%d letters carried text, want %d", with, fullBudget/3900)
	}
	if !strings.Contains(out.More, "get_letter") {
		t.Errorf("more = %q", out.More)
	}
}

func TestGetLetter(t *testing.T) {
	h := connect(t, Config{Reader: true}, "c")
	id := seed(t, h.store, "hello")
	out := h.mustCall("get_letter", map[string]any{"id": id})
	if !strings.Contains(out, UntrustedNotice) || !strings.Contains(out, `"message": "hello"`) {
		t.Errorf("get_letter = %s", out)
	}
	if out, isErr := h.call("get_letter", map[string]any{"id": 99}); !isErr {
		t.Errorf("missing letter returned: %s", out)
	}
}

func TestUpdateLetterRules(t *testing.T) {
	h := connect(t, Config{Reader: true, Agent: "claude-code"}, "c")
	id := seed(t, h.store, "build me")
	status := func() string {
		t.Helper()
		l, err := h.store.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		return l.Status
	}
	fails := func(args map[string]any, wantInMsg string) {
		t.Helper()
		out, isErr := h.call("update_letter", args)
		if !isErr {
			t.Errorf("update_letter(%v) succeeded: %s", args, out)
		} else if !strings.Contains(out, wantInMsg) {
			t.Errorf("update_letter(%v) error = %q, want it to mention %q", args, out, wantInMsg)
		}
	}

	// Agents cannot set the owner's statuses, by schema or by handler.
	for _, st := range []string{"planned", "dismissed", "new"} {
		fails(map[string]any{"id": id, "status": st}, "")
	}
	// A letter the owner has not planned cannot be claimed or finished.
	fails(map[string]any{"id": id, "status": "in_progress"}, "not planned")
	fails(map[string]any{"id": id, "status": "done"}, "in_progress")
	fails(map[string]any{"id": id}, "status, a note")
	fails(map[string]any{"id": 99, "status": "read"}, "no letter")
	if status() != store.StatusNew {
		t.Fatalf("status changed to %q by rejected calls", status())
	}

	h.mustCall("update_letter", map[string]any{"id": id, "status": "read"})
	if status() != store.StatusRead {
		t.Fatalf("status = %q, want read", status())
	}

	// The owner plans it; now one claim wins and a second is refused.
	if err := h.store.SetStatus(id, store.StatusPlanned); err != nil {
		t.Fatal(err)
	}
	// Marking a planned letter read must not undo the owner's decision.
	fails(map[string]any{"id": id, "status": "read"}, "planned")
	h.mustCall("update_letter", map[string]any{"id": id, "status": "in_progress"})
	fails(map[string]any{"id": id, "status": "in_progress"}, "already claimed")

	out := h.mustCall("update_letter", map[string]any{"id": id, "status": "done", "note": "Added the Makefile target."})
	if !strings.Contains(out, "now done") || !strings.Contains(out, "Note added") {
		t.Errorf("reply = %q", out)
	}
	l, err := h.store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if l.Status != store.StatusDone || len(l.Notes) != 1 || l.Notes[0].Author != "claude-code" || l.Notes[0].Body != "Added the Makefile target." {
		t.Errorf("after done: status=%q notes=%+v", l.Status, l.Notes)
	}
}

func TestTitle(t *testing.T) {
	if got := Title("short line\nrest"); got != "short line" {
		t.Errorf("Title = %q", got)
	}
	long := Title(strings.Repeat("word ", 40))
	if n := len([]rune(long)); n > 80 || !strings.HasSuffix(long, "…") {
		t.Errorf("long title = %q (%d runes)", long, n)
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"12h": now.Add(-12 * time.Hour),
		"7d":  now.AddDate(0, 0, -7),
		"2w":  now.AddDate(0, 0, -14),
	} {
		got, err := ParseSince(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("ParseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if got, err := ParseSince("2026-11-01", now); err != nil || got.Day() != 1 || got.Month() != 11 {
		t.Errorf("ParseSince(date) = %v, %v", got, err)
	}
	if got, err := ParseSince("", now); err != nil || !got.IsZero() {
		t.Errorf("ParseSince(empty) = %v, %v", got, err)
	}
	for _, bad := range []string{"soon", "7", "d", "-3d", "7y"} {
		if _, err := ParseSince(bad, now); err == nil {
			t.Errorf("ParseSince(%q) succeeded", bad)
		}
	}
}
