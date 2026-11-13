package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mxttwoods/letterbox/internal/clean"
	"github.com/mxttwoods/letterbox/internal/server"
	"github.com/mxttwoods/letterbox/internal/store"
)

// Letter text is cleaned when it is filed. It is cleaned again on the way
// out so that nothing stored by other means can drive the terminal.

func line(s string) string { return clean.Line(s) }

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// shortPath writes a home-relative path with ~.
func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+string(filepath.Separator)) {
			return "~" + p[len(home):]
		}
	}
	return p
}

func day(t time.Time) string {
	t = t.Local()
	if t.Year() == time.Now().Year() {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 2006")
}

func renderTable(w io.Writer, letters []*store.Letter) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tFILED\tAGENT\tKIND\tIMPACT\tSTATUS\tWHERE\tTITLE")
	for _, l := range letters {
		where := "-"
		if l.Project != "" {
			where = filepath.Base(shortPath(l.Project))
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			l.ID, day(l.CreatedAt), line(l.Agent), l.Kind, l.Impact, l.Status, line(where), line(l.Title))
	}
	tw.Flush()
}

// summary is the one-line count under the inbox.
func summary(counts map[string]int) string {
	var parts []string
	for _, st := range store.Statuses {
		if n := counts[st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, strings.ReplaceAll(st, "_", " ")))
		}
	}
	if len(parts) == 0 {
		return "The letterbox is empty."
	}
	return "In the box: " + strings.Join(parts, ", ") + "."
}

func renderLetter(w io.Writer, l *store.Letter) {
	fmt.Fprintf(w, "Letter #%d  [%s]  %s, impact %s\n", l.ID, l.Status, l.Kind, l.Impact)
	fmt.Fprintf(w, "Filed %s by %s", l.CreatedAt.Local().Format("Mon Jan 2 2006 15:04"), line(l.Agent))
	if l.ClientName != "" && l.ClientName != l.Agent {
		fmt.Fprintf(w, " (%s)", line(strings.TrimSpace(l.ClientName+" "+l.ClientVersion)))
	}
	if l.Project != "" {
		fmt.Fprintf(w, " in %s", line(shortPath(l.Project)))
	}
	fmt.Fprintln(w)
	if l.About != "" {
		fmt.Fprintf(w, "About: %s\n", line(l.About))
	}
	fmt.Fprintf(w, "\n%s\n", clean.Text(l.Message))
	if l.Suggestion != "" {
		fmt.Fprintf(w, "\nSuggestion:\n%s\n", clean.Text(l.Suggestion))
	}
	if len(l.Notes) > 0 {
		fmt.Fprintln(w, "\nNotes:")
		for _, n := range l.Notes {
			fmt.Fprintf(w, "  %s  %s: %s\n", day(n.CreatedAt), line(n.Author), line(n.Body))
		}
	}
}

// renderMarkdown writes letters in a form meant to be handed to an agent.
func renderMarkdown(w io.Writer, letters []*store.Letter) {
	fmt.Fprintf(w, "# Letterbox export\n\n> %s\n\n%s.\n", server.UntrustedNotice, plural(len(letters), "letter"))
	for _, l := range letters {
		fmt.Fprintf(w, "\n## Letter #%d: %s\n\n", l.ID, line(l.Title))
		fmt.Fprintf(w, "- Filed: %s by %s\n", l.CreatedAt.Local().Format("2006-01-02"), line(l.Agent))
		fmt.Fprintf(w, "- Kind: %s, impact: %s, status: %s\n", l.Kind, l.Impact, l.Status)
		if l.Project != "" {
			fmt.Fprintf(w, "- Where: %s\n", line(shortPath(l.Project)))
		}
		if l.About != "" {
			fmt.Fprintf(w, "- About: %s\n", line(l.About))
		}
		fmt.Fprintf(w, "\n%s\n", clean.Text(l.Message))
		if l.Suggestion != "" {
			fmt.Fprintf(w, "\n**Suggestion:** %s\n", clean.Text(l.Suggestion))
		}
		for _, n := range l.Notes {
			fmt.Fprintf(w, "\n**Note** (%s, %s): %s\n", line(n.Author), n.CreatedAt.Local().Format("2006-01-02"), line(n.Body))
		}
	}
}
