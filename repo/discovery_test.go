package repo

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFindGitReposTraversal(t *testing.T) {
	for _, rootRepo := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain base", true: "checkout base"}[rootRepo], func(t *testing.T) {
			base := t.TempDir()
			for _, rel := range []string{
				"alpha/.git", "alpha/child/.git", "alpha/child/grandchild/.git",
				"alpha/.git/metadata/.git", "group/beta/inner/.git",
				"group/plain/deep/.git", "zeta/.git",
			} {
				if err := os.MkdirAll(filepath.Join(base, rel), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			// Worktrees and submodules use a .git file; it is also a traversal boundary.
			if err := os.WriteFile(filepath.Join(base, "group/beta/.git"), []byte("gitdir: elsewhere\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if rootRepo {
				if err := os.Mkdir(filepath.Join(base, ".git"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, tc := range []struct {
				name  string
				depth int
				opts  []ScanOptions
				want  []string
			}{
				{"legacy", 5, nil, []string{"alpha", "alpha/child", "alpha/child/grandchild", "group/beta", "group/beta/inner", "group/plain/deep", "zeta"}},
				{"nested", 5, []ScanOptions{{}}, []string{"alpha", "alpha/child", "alpha/child/grandchild", "group/beta", "group/beta/inner", "group/plain/deep", "zeta"}},
				{"top level", 5, []ScanOptions{{SkipNested: true}}, []string{"alpha", "group/beta", "group/plain/deep", "zeta"}},
				{"nested depth limit", 2, nil, []string{"alpha", "alpha/child", "group/beta", "zeta"}},
				{"top level depth limit", 2, []ScanOptions{{SkipNested: true}}, []string{"alpha", "group/beta", "zeta"}},
				{"zero depth", 0, []ScanOptions{{SkipNested: true}}, nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					got, err := FindGitRepos(base, tc.depth, tc.opts...)
					if err != nil {
						t.Fatal(err)
					}
					for i := range tc.want {
						tc.want[i] = filepath.FromSlash(tc.want[i])
					}
					if !reflect.DeepEqual(got, tc.want) {
						t.Fatalf("got %v, want %v", got, tc.want)
					}
				})
			}
		})
	}
}
