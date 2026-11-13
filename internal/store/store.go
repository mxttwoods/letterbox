// Package store keeps letters in a SQLite file shared by every letterbox
// process on the machine.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Letter statuses. Only the owner sets planned and dismissed.
const (
	StatusNew        = "new"
	StatusRead       = "read"
	StatusPlanned    = "planned"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
	StatusDismissed  = "dismissed"
)

var (
	Statuses = []string{StatusNew, StatusRead, StatusPlanned, StatusInProgress, StatusDone, StatusDismissed}
	Kinds    = []string{"friction", "idea", "other"}
	Impacts  = []string{"blocked", "workaround", "minor"}

	// OpenStatuses are the ones that still need attention.
	OpenStatuses = []string{StatusNew, StatusRead, StatusPlanned, StatusInProgress}
)

var ErrNotFound = errors.New("letter not found")

type Letter struct {
	ID            int64     `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	Agent         string    `json:"agent"`
	ClientName    string    `json:"client_name,omitempty"`
	ClientVersion string    `json:"client_version,omitempty"`
	Project       string    `json:"project,omitempty"`
	ProcessID     string    `json:"-"`
	Kind          string    `json:"kind"`
	Impact        string    `json:"impact"`
	Status        string    `json:"status"`
	Title         string    `json:"title"`
	Message       string    `json:"message,omitempty"`
	About         string    `json:"about,omitempty"`
	Suggestion    string    `json:"suggestion,omitempty"`
	Notes         []Note    `json:"notes,omitempty"`
}

type Note struct {
	CreatedAt time.Time `json:"created_at"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
}

// Filter narrows List. Zero values mean "no restriction".
type Filter struct {
	Statuses []string
	Kind     string
	Since    time.Time
	Limit    int
}

type Store struct {
	db *sql.DB
}

// migrations are applied in order; a database's PRAGMA user_version is the
// number already applied. Never edit an entry that has shipped: add a new one.
var migrations = []string{
	`CREATE TABLE letters (
		id             INTEGER PRIMARY KEY,
		created_at     TEXT NOT NULL,
		agent          TEXT NOT NULL,
		client_name    TEXT NOT NULL DEFAULT '',
		client_version TEXT NOT NULL DEFAULT '',
		project        TEXT NOT NULL DEFAULT '',
		process_id     TEXT NOT NULL,
		kind           TEXT NOT NULL DEFAULT 'other',
		impact         TEXT NOT NULL DEFAULT 'minor',
		title          TEXT NOT NULL,
		message        TEXT NOT NULL,
		about          TEXT NOT NULL DEFAULT '',
		suggestion     TEXT NOT NULL DEFAULT '',
		status         TEXT NOT NULL DEFAULT 'new'
	);
	CREATE INDEX letters_status_created ON letters(status, created_at);
	CREATE TABLE notes (
		id         INTEGER PRIMARY KEY,
		letter_id  INTEGER NOT NULL REFERENCES letters(id) ON DELETE CASCADE,
		created_at TEXT NOT NULL,
		author     TEXT NOT NULL,
		body       TEXT NOT NULL
	);
	CREATE INDEX notes_letter ON notes(letter_id);`,
}

// DefaultPath is where the shared database lives unless LETTERBOX_DB is set.
func DefaultPath() (string, error) {
	if p := os.Getenv("LETTERBOX_DB"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "letterbox", "letterbox.db"), nil
}

// Open opens the database at path, creating and migrating it if needed.
// It is safe to call from several processes at the same moment.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// Create the file ourselves so it is private from the start; SQLite gives
	// its -wal and -shm files the same permissions as the main file.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()

	// busy_timeout comes first so the later pragmas wait for other processes.
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection per process: writes are tiny and this rules out a
	// process deadlocking against itself.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// tx runs fn in a write transaction, retrying a few times if another process
// holds the lock for longer than busy_timeout.
func (s *Store) tx(fn func(*sql.Tx) error) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = s.txOnce(fn)
		if err == nil || !isBusy(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
	}
	return err
}

func (s *Store) txOnce(fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(context.Background(), nil) // BEGIN IMMEDIATE, via _txlock
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func isBusy(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked")
}

func (s *Store) migrate() error {
	return s.tx(func(tx *sql.Tx) error {
		// The version is read inside the write transaction, so two processes
		// starting together cannot both decide to migrate.
		var version int
		if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
			return err
		}
		if version > len(migrations) {
			return fmt.Errorf("database is version %d but this letterbox only knows version %d; rebuild letterbox from the latest source", version, len(migrations))
		}
		for i := version; i < len(migrations); i++ {
			if _, err := tx.Exec(migrations[i]); err != nil {
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
		}
		if version < len(migrations) {
			if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, len(migrations))); err != nil {
				return err
			}
		}
		return nil
	})
}

const timeLayout = time.RFC3339

func now() string { return time.Now().UTC().Format(timeLayout) }

// Insert files a letter and returns its ID. CreatedAt and Status are set here.
func (s *Store) Insert(l *Letter) (int64, error) {
	var id int64
	err := s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO letters
			(created_at, agent, client_name, client_version, project, process_id, kind, impact, title, message, about, suggestion)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			now(), l.Agent, l.ClientName, l.ClientVersion, l.Project, l.ProcessID, l.Kind, l.Impact, l.Title, l.Message, l.About, l.Suggestion)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

const letterColumns = `id, created_at, agent, client_name, client_version, project, process_id, kind, impact, title, message, about, suggestion, status`

type scanner interface{ Scan(...any) error }

func scanLetter(row scanner) (*Letter, error) {
	var l Letter
	var created string
	err := row.Scan(&l.ID, &created, &l.Agent, &l.ClientName, &l.ClientVersion, &l.Project, &l.ProcessID,
		&l.Kind, &l.Impact, &l.Title, &l.Message, &l.About, &l.Suggestion, &l.Status)
	if err != nil {
		return nil, err
	}
	l.CreatedAt, _ = time.Parse(timeLayout, created)
	return &l, nil
}

// Get returns one letter with its notes.
func (s *Store) Get(id int64) (*Letter, error) {
	l, err := scanLetter(s.db.QueryRow(`SELECT `+letterColumns+` FROM letters WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.Notes, err = s.notes(id)
	return l, err
}

func (s *Store) notes(id int64) ([]Note, error) {
	rows, err := s.db.Query(`SELECT created_at, author, body FROM notes WHERE letter_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []Note
	for rows.Next() {
		var n Note
		var created string
		if err := rows.Scan(&created, &n.Author, &n.Body); err != nil {
			return nil, err
		}
		n.CreatedAt, _ = time.Parse(timeLayout, created)
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

// List returns letters matching f, newest first, with their notes.
func (s *Store) List(f Filter) ([]*Letter, error) {
	query := `SELECT ` + letterColumns + ` FROM letters`
	var where []string
	var args []any
	if len(f.Statuses) > 0 {
		where = append(where, `status IN (`+placeholders(len(f.Statuses))+`)`)
		for _, st := range f.Statuses {
			args = append(args, st)
		}
	}
	if f.Kind != "" {
		where = append(where, `kind = ?`)
		args = append(args, f.Kind)
	}
	if !f.Since.IsZero() {
		where = append(where, `created_at >= ?`)
		args = append(args, f.Since.UTC().Format(timeLayout))
	}
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += ` ORDER BY id DESC`
	if f.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, f.Limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var letters []*Letter
	for rows.Next() {
		l, err := scanLetter(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		letters = append(letters, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Notes are fetched after the rows are closed: there is one connection.
	for _, l := range letters {
		if l.Notes, err = s.notes(l.ID); err != nil {
			return nil, err
		}
	}
	return letters, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// Counts returns the number of letters in each status.
func (s *Store) Counts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM letters GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		counts[st] = n
	}
	return counts, rows.Err()
}

// SetStatus sets a letter's status unconditionally. This is the owner's path.
func (s *Store) SetStatus(id int64, status string) error {
	return s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE letters SET status = ? WHERE id = ?`, status, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// Advance moves a letter to status only if it is currently in from. It
// reports whether the letter moved and the status it has afterwards. This is
// the agents' path: a single conditional UPDATE, so two agents cannot both
// claim the same letter.
func (s *Store) Advance(id int64, from, to string) (moved bool, current string, err error) {
	err = s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE letters SET status = ? WHERE id = ? AND status = ?`, to, id, from)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		moved = n == 1
		err = tx.QueryRow(`SELECT status FROM letters WHERE id = ?`, id).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return moved, current, err
}

// AddNote appends a note to a letter. Notes are never edited or replaced.
func (s *Store) AddNote(id int64, author, body string) error {
	return s.tx(func(tx *sql.Tx) error {
		var exists int
		err := tx.QueryRow(`SELECT 1 FROM letters WHERE id = ?`, id).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO notes (letter_id, created_at, author, body) VALUES (?, ?, ?, ?)`, id, now(), author, body)
		return err
	})
}

// Delete removes a letter and its notes.
func (s *Store) Delete(id int64) error {
	return s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM letters WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// Backup writes a consistent copy of the database to dest, which must not
// already exist. Copying the .db file by hand can miss data still in the WAL.
func (s *Store) Backup(dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("%s already exists", dest)
	}
	if _, err := s.db.Exec(`VACUUM INTO ?`, dest); err != nil {
		return err
	}
	return os.Chmod(dest, 0o600)
}
