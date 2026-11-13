package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", "letterbox.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func letter(msg string) *Letter {
	return &Letter{Agent: "test", ProcessID: "p1", Kind: "friction", Impact: "minor", Title: msg, Message: msg}
}

func mustInsert(t *testing.T, s *Store, l *Letter) int64 {
	t.Helper()
	id, err := s.Insert(l)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestInsertAndGet(t *testing.T) {
	s, _ := open(t)
	in := letter("the lint skill has no docs")
	in.About = "lint skill"
	in.Suggestion = "add a README"
	in.Project = "/tmp/proj"
	id := mustInsert(t, s, in)

	got, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Message != in.Message || got.About != in.About || got.Suggestion != in.Suggestion || got.Project != in.Project {
		t.Errorf("round trip mismatch: %+v", got)
	}
	if got.Status != StatusNew {
		t.Errorf("status = %q, want new", got.Status)
	}
	if time.Since(got.CreatedAt) > time.Minute || got.CreatedAt.IsZero() {
		t.Errorf("created_at = %v", got.CreatedAt)
	}

	if _, err := s.Get(999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(999) err = %v, want ErrNotFound", err)
	}
}

func TestFilePermissions(t *testing.T) {
	_, path := open(t)
	for p, want := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", p, got, want)
		}
	}
}

func TestListFilters(t *testing.T) {
	s, _ := open(t)
	a := mustInsert(t, s, letter("a"))
	idea := letter("b")
	idea.Kind = "idea"
	b := mustInsert(t, s, idea)
	c := mustInsert(t, s, letter("c"))
	if err := s.SetStatus(a, StatusDismissed); err != nil {
		t.Fatal(err)
	}

	ids := func(f Filter) string {
		t.Helper()
		ls, err := s.List(f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, l := range ls {
			out = append(out, fmt.Sprint(l.ID))
		}
		return strings.Join(out, ",")
	}

	if got, want := ids(Filter{}), fmt.Sprintf("%d,%d,%d", c, b, a); got != want {
		t.Errorf("all = %s, want %s (newest first)", got, want)
	}
	if got, want := ids(Filter{Statuses: OpenStatuses}), fmt.Sprintf("%d,%d", c, b); got != want {
		t.Errorf("open = %s, want %s", got, want)
	}
	if got, want := ids(Filter{Kind: "idea"}), fmt.Sprint(b); got != want {
		t.Errorf("kind = %s, want %s", got, want)
	}
	if got, want := ids(Filter{Limit: 1}), fmt.Sprint(c); got != want {
		t.Errorf("limit = %s, want %s", got, want)
	}
	if got := ids(Filter{Since: time.Now().Add(time.Hour)}); got != "" {
		t.Errorf("since future = %s, want none", got)
	}

	counts, err := s.Counts()
	if err != nil {
		t.Fatal(err)
	}
	if counts[StatusNew] != 2 || counts[StatusDismissed] != 1 {
		t.Errorf("counts = %v", counts)
	}
}

func TestAdvanceIsConditional(t *testing.T) {
	s, _ := open(t)
	id := mustInsert(t, s, letter("x"))

	// Not planned yet: an agent cannot claim it.
	moved, cur, err := s.Advance(id, StatusPlanned, StatusInProgress)
	if err != nil || moved || cur != StatusNew {
		t.Fatalf("claim of unplanned letter: moved=%v cur=%q err=%v", moved, cur, err)
	}

	if err := s.SetStatus(id, StatusPlanned); err != nil {
		t.Fatal(err)
	}
	moved, cur, err = s.Advance(id, StatusPlanned, StatusInProgress)
	if err != nil || !moved || cur != StatusInProgress {
		t.Fatalf("first claim: moved=%v cur=%q err=%v", moved, cur, err)
	}
	// A second claim loses.
	moved, cur, err = s.Advance(id, StatusPlanned, StatusInProgress)
	if err != nil || moved || cur != StatusInProgress {
		t.Fatalf("second claim: moved=%v cur=%q err=%v", moved, cur, err)
	}

	if _, _, err := s.Advance(999, StatusPlanned, StatusInProgress); !errors.Is(err, ErrNotFound) {
		t.Errorf("Advance(999) err = %v, want ErrNotFound", err)
	}
}

func TestConcurrentClaimHasOneWinner(t *testing.T) {
	s, path := open(t)
	id := mustInsert(t, s, letter("x"))
	if err := s.SetStatus(id, StatusPlanned); err != nil {
		t.Fatal(err)
	}

	const n = 8
	wins := make(chan bool, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, err := Open(path) // each claimant is its own "process"
			if err != nil {
				t.Error(err)
				return
			}
			defer other.Close()
			moved, _, err := other.Advance(id, StatusPlanned, StatusInProgress)
			if err != nil {
				t.Error(err)
			}
			wins <- moved
		}()
	}
	wg.Wait()
	close(wins)
	won := 0
	for w := range wins {
		if w {
			won++
		}
	}
	if won != 1 {
		t.Errorf("%d claimants won, want exactly 1", won)
	}
}

func TestNotesAppendAndCascade(t *testing.T) {
	s, _ := open(t)
	id := mustInsert(t, s, letter("x"))
	if err := s.AddNote(id, "owner", "first"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddNote(id, "codex", "second"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddNote(999, "owner", "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("AddNote(999) err = %v, want ErrNotFound", err)
	}

	got, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Notes) != 2 || got.Notes[0].Body != "first" || got.Notes[1].Author != "codex" {
		t.Errorf("notes = %+v", got.Notes)
	}

	if err := s.Delete(id); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(id); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete err = %v, want ErrNotFound", err)
	}
	var orphans int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Errorf("%d notes left after deleting their letter", orphans)
	}
}

func TestReopenKeepsData(t *testing.T) {
	s, path := open(t)
	id := mustInsert(t, s, letter("x"))
	s.Close()

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if _, err := again.Get(id); err != nil {
		t.Errorf("letter lost after reopen: %v", err)
	}
}

func TestRefusesNewerDatabase(t *testing.T) {
	s, path := open(t)
	if _, err := s.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, len(migrations)+1)); err != nil {
		t.Fatal(err)
	}
	s.Close()

	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "version") {
		t.Errorf("Open of newer database: err = %v, want a version error", err)
	}
}

// Many processes starting at once against a database that does not exist yet
// must all succeed, and the schema must be created exactly once.
func TestConcurrentOpenOfEmptyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "letterbox.db")
	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := Open(path)
			if err != nil {
				errs <- err
				return
			}
			defer s.Close()
			if _, err := s.Insert(letter(fmt.Sprintf("letter %d", i))); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ls, err := s.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != n {
		t.Errorf("%d letters stored, want %d", len(ls), n)
	}
}

func TestBackup(t *testing.T) {
	s, _ := open(t)
	id := mustInsert(t, s, letter("keep me"))
	dest := filepath.Join(t.TempDir(), "backup.db")
	if err := s.Backup(dest); err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(dest); err == nil {
		t.Error("Backup over an existing file succeeded, want an error")
	}

	db, err := sql.Open("sqlite", "file:"+dest)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var msg string
	if err := db.QueryRow(`SELECT message FROM letters WHERE id = ?`, id).Scan(&msg); err != nil {
		t.Fatal(err)
	}
	if msg != "keep me" {
		t.Errorf("backup message = %q", msg)
	}
}
