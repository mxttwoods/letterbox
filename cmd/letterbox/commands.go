package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mxttwoods/letterbox/internal/clean"
	"github.com/mxttwoods/letterbox/internal/server"
	"github.com/mxttwoods/letterbox/internal/store"
)

type cli struct {
	store  *store.Store
	stdin  io.Reader
	out    io.Writer
	errOut io.Writer
}

func (c *cli) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.errOut)
	return fs
}

// parse lets flags come before or after the IDs: the standard library stops
// at the first non-flag argument, so keep going until nothing is left.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func parseIDs(args []string) ([]int64, error) {
	if len(args) == 0 {
		return nil, errors.New("give at least one letter number")
	}
	var ids []int64
	for _, a := range args {
		id, err := strconv.ParseInt(strings.TrimPrefix(a, "#"), 10, 64)
		if err != nil || id < 1 {
			return nil, fmt.Errorf("%q is not a letter number", a)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func notFound(id int64, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("there is no letter #%d", id)
	}
	return err
}

// filter builds a store.Filter from the flags shared by inbox and export.
func filter(status, kind, since string) (store.Filter, error) {
	statuses, err := server.StatusFilter(status)
	if err != nil {
		return store.Filter{}, err
	}
	if kind != "" && !contains(store.Kinds, kind) {
		return store.Filter{}, fmt.Errorf("kind must be one of %s", strings.Join(store.Kinds, ", "))
	}
	sinceTime, err := server.ParseSince(since, time.Now())
	if err != nil {
		return store.Filter{}, err
	}
	return store.Filter{Statuses: statuses, Kind: kind, Since: sinceTime}, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (c *cli) inbox(args []string) error {
	fs := c.flags("inbox")
	all := fs.Bool("all", false, "show every letter, whatever its status")
	status := fs.String("status", "new", "open, all, or one status")
	kind := fs.String("kind", "", "friction, idea or other")
	since := fs.String("since", "", "a span like 7d or a date like 2026-11-01")
	limit := fs.Int("limit", 50, "most letters to show")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if *all {
		*status = "all"
	}
	f, err := filter(*status, *kind, *since)
	if err != nil {
		return err
	}
	f.Limit = *limit
	letters, err := c.store.List(f)
	if err != nil {
		return err
	}
	counts, err := c.store.Counts()
	if err != nil {
		return err
	}

	if len(letters) == 0 {
		fmt.Fprintf(c.out, "No %s letters.\n", statusWords(*status))
	} else {
		renderTable(c.out, letters)
		if len(letters) == *limit {
			fmt.Fprintf(c.out, "(showing the newest %d; raise --limit to see more)\n", *limit)
		}
	}
	fmt.Fprintln(c.out, summary(counts))
	return nil
}

func statusWords(status string) string {
	if status == "all" {
		return "stored"
	}
	return strings.ReplaceAll(status, "_", " ")
}

func (c *cli) show(args []string) error {
	ids, err := parseIDs(args)
	if err != nil {
		return err
	}
	for i, id := range ids {
		l, err := c.store.Get(id)
		if err != nil {
			return notFound(id, err)
		}
		if i > 0 {
			fmt.Fprintln(c.out)
		}
		renderLetter(c.out, l)
	}
	return nil
}

func (c *cli) mark(args []string) error {
	fs := c.flags("mark")
	status := fs.String("status", "", "read, planned, in_progress, done, dismissed or new")
	note := fs.String("note", "", "a note to attach")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	ids, err := parseIDs(rest)
	if err != nil {
		return err
	}
	noteText := clean.Text(*note)
	if *status == "" && noteText == "" {
		return errors.New("give --status, --note, or both")
	}
	if *status != "" && !contains(store.Statuses, *status) {
		return fmt.Errorf("status must be one of %s", strings.Join(store.Statuses, ", "))
	}
	for _, id := range ids {
		if *status != "" {
			if err := c.store.SetStatus(id, *status); err != nil {
				return notFound(id, err)
			}
		}
		if noteText != "" {
			if err := c.store.AddNote(id, "owner", noteText); err != nil {
				return notFound(id, err)
			}
		}
		switch {
		case *status != "" && noteText != "":
			fmt.Fprintf(c.out, "Letter #%d marked %s, note added.\n", id, *status)
		case *status != "":
			fmt.Fprintf(c.out, "Letter #%d marked %s.\n", id, *status)
		default:
			fmt.Fprintf(c.out, "Note added to letter #%d.\n", id)
		}
	}
	return nil
}

func (c *cli) export(args []string) error {
	fs := c.flags("export")
	status := fs.String("status", "new", "open, all, or one status")
	kind := fs.String("kind", "", "friction, idea or other")
	since := fs.String("since", "", "a span like 30d or a date like 2026-11-01")
	format := fs.String("format", "md", "md or json")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	f, err := filter(*status, *kind, *since)
	if err != nil {
		return err
	}
	letters, err := c.store.List(f)
	if err != nil {
		return err
	}
	switch *format {
	case "md":
		renderMarkdown(c.out, letters)
		return nil
	case "json":
		if letters == nil {
			letters = []*store.Letter{}
		}
		enc := json.NewEncoder(c.out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"notice": server.UntrustedNotice, "letters": letters})
	}
	return errors.New("format must be md or json")
}

func (c *cli) delete(args []string) error {
	fs := c.flags("delete")
	yes := fs.Bool("yes", false, "don't ask for confirmation")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	ids, err := parseIDs(rest)
	if err != nil {
		return err
	}
	// Look every letter up first so nothing is deleted if one number is wrong.
	for _, id := range ids {
		l, err := c.store.Get(id)
		if err != nil {
			return notFound(id, err)
		}
		fmt.Fprintf(c.out, "#%d  %s\n", l.ID, clean.Line(l.Title))
	}
	if !*yes {
		fmt.Fprintf(c.out, "Delete %s for good? [y/N] ", plural(len(ids), "letter"))
		line, _ := bufio.NewReader(c.stdin).ReadString('\n')
		if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
			fmt.Fprintln(c.out, "Nothing deleted.")
			return nil
		}
	}
	for _, id := range ids {
		if err := c.store.Delete(id); err != nil {
			return notFound(id, err)
		}
	}
	fmt.Fprintf(c.out, "Deleted %s.\n", plural(len(ids), "letter"))
	return nil
}

func (c *cli) backup(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: letterbox backup <file>")
	}
	if err := c.store.Backup(args[0]); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Backup written to %s\n", args[0])
	return nil
}

func (c *cli) send(args []string) error {
	fs := c.flags("send")
	message := fs.String("message", "", "the letter (required)")
	kind := fs.String("kind", "", "friction, idea or other")
	about := fs.String("about", "", "what the letter concerns")
	impact := fs.String("impact", "", "blocked, workaround or minor")
	suggestion := fs.String("suggestion", "", "a proposed change")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	l, err := server.NewLetter(*message, *kind, *about, *impact, *suggestion)
	if err != nil {
		return err
	}
	l.Agent = os.Getenv("LETTERBOX_AGENT")
	if l.Agent == "" {
		l.Agent = "owner"
	}
	l.Project = detectProject()
	l.ProcessID = "cli"
	id, err := c.store.Insert(l)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Letter #%d filed.\n", id)
	return nil
}
