package repo

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// CommitFile is a working-tree change with its prospective commit's line counts.
// A nil Stat means counts could not be read; a non-nil zero Stat is a verified
// change without textual additions or deletions.
type CommitFile struct {
	Status FileStatus
	Stat   *DiffStat
}

// CommitPreview reads all changed files, including untracked files, without
// staging anything. Only a failed status read fails the preview; unavailable
// statistics leave the affected files' Stat nil.
func CommitPreview(dir string) ([]CommitFile, error) {
	status, err := ReadWorktreeStatus(context.Background(), dir)
	if err != nil || len(status.Files) == 0 {
		return nil, err
	}
	dir = status.Root
	base := diffAgainst
	if !hasHEAD(dir) {
		// Hash the empty tree without writing an object. This also supports
		// repositories using SHA-256 object IDs.
		base, err = gitCapture(dir, "hash-object", "-t", "tree", "--stdin")
		base = strings.TrimSpace(base)
	}
	var stats map[string]DiffStat
	if err == nil {
		var out string
		out, err = gitCapture(dir, "diff", "--no-ext-diff", "--no-textconv", "--numstat", "-z", "--find-renames", base, "--")
		if err == nil {
			stats, err = parseCommitStats(out)
		}
	}
	files := make([]CommitFile, 0, len(status.Files))
	for _, f := range status.Files {
		file := CommitFile{Status: f}
		switch {
		case f.Conflict:
			// An unresolved merge has no prospective commit to count yet.
		case f.Untracked:
			file.Stat = untrackedCommitStat(dir, f.Path)
		case err == nil:
			st := stats[f.Path]
			file.Stat = &st
		}
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Status.Untracked != files[j].Status.Untracked {
			return !files[i].Status.Untracked
		}
		return files[i].Status.Path < files[j].Status.Path
	})
	return files, nil
}

func untrackedCommitStat(dir, path string) *DiffStat {
	out, err := gitCapture(dir, "diff", "--no-index", "--no-ext-diff", "--no-textconv", "--numstat", "-z", "--", os.DevNull, path)
	// Some Git versions also exit 1 for a missing input. A real difference
	// must include numstat output; otherwise the error is not a line count.
	if err != nil && (exitCode(err) != 1 || out == "") {
		return nil
	}
	stats, err := parseCommitStats(out)
	if err != nil {
		return nil
	}
	var total DiffStat
	for _, st := range stats {
		total.Added += st.Added
		total.Deleted += st.Deleted
		total.Binary = total.Binary || st.Binary
	}
	return &total
}

// NUL-delimited numstat keeps unusual filenames intact. Renames have an empty
// path in the count record, followed by separate old and new path records.
func parseCommitStats(out string) (map[string]DiffStat, error) {
	stats := make(map[string]DiffStat)
	for out != "" {
		record, rest, ok := strings.Cut(out, "\x00")
		if !ok {
			return nil, fmt.Errorf("unterminated numstat record")
		}
		out = rest
		parts := strings.SplitN(record, "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("malformed numstat record")
		}
		path := parts[2]
		if path == "" {
			_, out, ok = strings.Cut(out, "\x00")
			if !ok {
				return nil, fmt.Errorf("missing rename origin")
			}
			path, out, ok = strings.Cut(out, "\x00")
			if !ok || path == "" {
				return nil, fmt.Errorf("missing rename destination")
			}
		}
		st := DiffStat{Binary: parts[0] == "-" && parts[1] == "-"}
		if !st.Binary {
			var err1, err2 error
			st.Added, err1 = strconv.Atoi(parts[0])
			st.Deleted, err2 = strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil || st.Added < 0 || st.Deleted < 0 {
				return nil, fmt.Errorf("invalid numstat counts")
			}
		}
		stats[path] = st
	}
	return stats, nil
}
