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
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel")
	cmd.Env = GitEnv()
	out, err := cmd.Output()
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
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-C", root,
		"status", "--porcelain=v2", "-z", "--untracked-files=all",
		"--ignored=matching", "--ignore-submodules=none")
	cmd.Env = GitEnv()
	out, err := cmd.Output()
	if err != nil {
		return WorktreeStatus{}, fmt.Errorf("read status: %w", err)
	}
	s, err := parseWorktreeStatus(string(out))
	s.Root = root
	return s, err
}

func parseWorktreeStatus(out string) (WorktreeStatus, error) {
	s := WorktreeStatus{Files: make(map[string]FileStatus)}
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
				s.Ignored = append(s.Ignored, r[2:])
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
		s.Files[f.Path] = f
	}
	return s, nil
}
