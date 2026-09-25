package repo

import (
	"errors"
	"strconv"
	"strings"
)

// Commit history for the UI, captured whole through gitCapture (blank message lines are
// content, and failures must stay errors). No color: the renderer colors it.

// LogLimit caps a log capture; long histories would be parsed and re-rendered on every
// toggle for a view about recent work. LogScreen says when the cap was hit.
const LogLimit = 500

// Field and record separators (ASCII US/RS), which never appear in git's fields.
const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
)

// logFormat is one commit per record: hash, abbreviated hash, parents, decorations, author
// name and email, date, subject, body.
const logFormat = "%H" + fieldSep + "%h" + fieldSep + "%P" + fieldSep + "%D" +
	fieldSep + "%an" + fieldSep + "%ae" + fieldSep + "%ad" + fieldSep + "%s" +
	fieldSep + "%b" + recordSep

// logFields is how many fields logFormat produces; a record that splits into anything else
// is malformed and skipped rather than read at the wrong offsets.
const logFields = 9

// RefKind classifies a decoration so each kind gets git's color for it.
type RefKind int

const (
	RefBranch RefKind = iota // a local branch
	RefRemote                // a remote-tracking branch (origin/main)
	RefTag                   // a tag
	RefHead                  // HEAD, and the branch it points at when it isn't detached
)

// Ref is one decoration on a commit. For RefHead, Name is the branch HEAD points at, or ""
// when the checkout is detached.
type Ref struct {
	Name string
	Kind RefKind
}

// Commit is one entry of the log. Body is the message below the subject, which is empty for
// most commits and several paragraphs for the ones that matter.
type Commit struct {
	Hash    string
	Short   string
	Parents []string // two or more means a merge
	Refs    []Ref
	Author  string
	Email   string
	Date    string
	Subject string
	Body    string
}

// Merge reports whether the commit has more than one parent.
func (c Commit) Merge() bool { return len(c.Parents) > 1 }

// ErrNoCommits reports a repo with no commits: not a failure, so callers check it with
// errors.Is and say so plainly instead of git's message.
var ErrNoCommits = errors.New("this repo has no commits yet")

// Log reads dir's most recent commits, newest first, up to limit (LogLimit when limit is not
// positive).
func Log(dir string, limit int) ([]Commit, error) {
	if dir == "" {
		return nil, errors.New("no repo directory")
	}
	if limit <= 0 {
		limit = LogLimit
	}
	if !hasHEAD(dir) {
		return nil, ErrNoCommits
	}

	out, err := gitCapture(dir, "-c", "core.quotepath=false", "log", "--no-color",
		"-n", strconv.Itoa(limit), "--date=default", "--pretty=format:"+logFormat)
	if err != nil {
		return nil, err
	}
	return parseLog(out, remoteSet(dir)), nil
}

// remoteSet is the repo's remote names, to tell `origin/main` (remote) from `feature/main`
// (local); a "/" alone proves nothing. No remotes, or a failed read, gives an empty set,
// classifying slash refs as local.
func remoteSet(dir string) map[string]bool {
	out := GitOutput(dir, "remote")
	if out == "" {
		return nil
	}
	set := make(map[string]bool)
	for _, name := range strings.Fields(out) {
		set[name] = true
	}
	return set
}

// parseLog splits the capture into commits. Records are separated by recordSep and joined by
// git with a newline, so every record after the first opens with one.
func parseLog(raw string, remotes map[string]bool) []Commit {
	records := strings.Split(raw, recordSep)
	commits := make([]Commit, 0, len(records))

	for _, rec := range records {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue // the tail after the final separator
		}
		f := strings.Split(rec, fieldSep)
		if len(f) != logFields {
			continue
		}
		commits = append(commits, Commit{
			Hash:    f[0],
			Short:   f[1],
			Parents: strings.Fields(f[2]),
			Refs:    parseRefs(f[3], remotes),
			Author:  f[4],
			Email:   f[5],
			Date:    f[6],
			Subject: f[7],
			// %b ends in the newlines that separated it from the next record's fields, and
			// a message often carries a trailing blank line of its own besides.
			Body: strings.TrimRight(f[8], " \t\n"),
		})
	}
	return commits
}

// parseRefs reads a commit's decorations (%D) — "HEAD -> main, tag: v1.0.0, origin/main" —
// into classified refs. An empty decoration list yields nil.
func parseRefs(d string, remotes map[string]bool) []Ref {
	d = strings.TrimSpace(d)
	if d == "" {
		return nil
	}

	var refs []Ref
	for _, part := range strings.Split(d, ", ") {
		part = strings.TrimSpace(part)
		switch {
		case part == "":
			continue
		case strings.HasPrefix(part, "tag: "):
			refs = append(refs, Ref{Name: strings.TrimPrefix(part, "tag: "), Kind: RefTag})
		case part == "HEAD":
			// Detached: HEAD names a commit rather than a branch.
			refs = append(refs, Ref{Kind: RefHead})
		case strings.HasPrefix(part, "HEAD -> "):
			// One ref, not two: the arrow binds HEAD to the branch, and rendering them
			// separately would put a comma between them.
			refs = append(refs, Ref{Name: strings.TrimPrefix(part, "HEAD -> "), Kind: RefHead})
		case isRemoteRef(part, remotes):
			refs = append(refs, Ref{Name: part, Kind: RefRemote})
		default:
			refs = append(refs, Ref{Name: part, Kind: RefBranch})
		}
	}
	return refs
}

// isRemoteRef reports whether name's first path segment is one of the repo's remotes.
func isRemoteRef(name string, remotes map[string]bool) bool {
	i := strings.IndexByte(name, '/')
	return i > 0 && remotes[name[:i]]
}
