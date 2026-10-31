package repo

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// FileStatus preserves Git's index and worktree columns independently. Path and
// OriginalPath are slash-separated, repository-relative names, without quoting.
type FileStatus struct {
	Path, OriginalPath  string
	Index, Worktree     byte
	Untracked, Conflict bool
}

// WorktreeStatus is an immutable snapshot. Ignored entries ending in / cover a
// whole directory; others cover one file. An empty Root means no checkout.
type WorktreeStatus struct {
	Root    string
	Files   map[string]FileStatus
	Ignored []string
}

// RepoRootContext discovers the enclosing checkout, including worktrees and
// submodules. dir must be a directory. Git's exit 128 means no usable checkout.
func RepoRootContext(ctx context.Context, dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	out, err := gitCmd(ctx, dir, "rev-parse", "--show-toplevel").Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok && e.ExitCode() == 128 {
			return "", nil
		}
		return "", err
	}
	return filepath.Clean(strings.TrimSuffix(string(out), "\n")), nil
}

// ReadWorktreeStatus reads the checkout enclosing dir without refreshing Git's
// index on disk. Callers bound the context and schedule this away from UI updates.
func ReadWorktreeStatus(ctx context.Context, dir string) (WorktreeStatus, error) {
	root, err := RepoRootContext(ctx, dir)
	if err != nil || root == "" {
		return WorktreeStatus{}, err
	}
	records, err := readStatusRecords(ctx, root, "--ignored=matching", "--ignore-submodules=none")
	if err != nil {
		return WorktreeStatus{}, err
	}
	return records.snapshot(root), nil
}

// Keep Git's record order for list callers; snapshots offer keyed lookup instead.
type statusRecords struct {
	files   []FileStatus
	ignored []string
}

func (s statusRecords) snapshot(root string) WorktreeStatus {
	snapshot := WorktreeStatus{Root: root, Files: make(map[string]FileStatus, len(s.files)), Ignored: s.ignored}
	for _, f := range s.files {
		snapshot.Files[f.Path] = f
	}
	return snapshot
}

func readStatusRecords(ctx context.Context, dir string, options ...string) (statusRecords, error) {
	args := append([]string{"--no-optional-locks", "status", "--porcelain=v2", "-z", "--untracked-files=all"}, options...)
	out, err := gitCmd(ctx, dir, args...).Output()
	if ctx.Err() != nil {
		return statusRecords{}, ctx.Err()
	}
	if err != nil {
		return statusRecords{}, fmt.Errorf("read status: %w", err)
	}
	return parseWorktreeStatus(string(out))
}

func parseWorktreeStatus(out string) (statusRecords, error) {
	var s statusRecords
	if out != "" && !strings.HasSuffix(out, "\x00") {
		return s, fmt.Errorf("unterminated status record")
	}
	records := strings.Split(out, "\x00")
	for i := 0; i < len(records); i++ {
		r := records[i]
		if r == "" || r[0] == '#' {
			continue
		}
		var f FileStatus
		switch r[0] {
		case '?', '!':
			if len(r) < 3 || r[1] != ' ' {
				return s, fmt.Errorf("malformed status record")
			}
			if r[0] == '!' {
				s.ignored = append(s.ignored, r[2:])
				continue
			}
			f = FileStatus{Path: r[2:], Untracked: true}
		case '1', '2', 'u':
			n := 9
			if r[0] == '2' {
				n = 10
			}
			if r[0] == 'u' {
				n = 11
			}
			fields := strings.SplitN(r, " ", n)
			if len(fields) != n || len(fields[1]) != 2 || fields[n-1] == "" {
				return s, fmt.Errorf("malformed tracked status record")
			}
			f = FileStatus{Path: fields[n-1], Index: fields[1][0], Worktree: fields[1][1], Conflict: r[0] == 'u'}
			if r[0] == '2' {
				i++
				if i >= len(records) || records[i] == "" {
					return s, fmt.Errorf("missing rename origin")
				}
				f.OriginalPath = records[i]
			}
		default:
			return s, fmt.Errorf("unknown status record %q", r[0])
		}
		s.files = append(s.files, f)
	}
	return s, nil
}
