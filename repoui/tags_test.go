package repoui

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"
	"github.com/charmbracelet/x/ansi"
)

func tagTestRepo(t *testing.T, tags []string) repo.Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "core.hooksPath=" + os.DevNull}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "--no-gpg-sign", "-qm", "initial")
	for _, tag := range tags {
		run("tag", tag)
	}
	return repo.Repo{Name: "Example", Dir: dir}
}

func TestNewTagFormCurrentAndNext(t *testing.T) {
	for _, tc := range []struct {
		name          string
		local, remote []string
		current, next string
	}{
		{"local", []string{"v0.1.0"}, nil, "v0.1.0", "v0.1.1"},
		{"remote wins", []string{"v0.1.0"}, []string{"v0.2.9"}, "v0.2.9", "v0.2.10"},
		{"local wins", []string{"2.0.0"}, []string{"v1.0.0"}, "2.0.0", "2.0.1"},
		{"none", nil, nil, "(no version tag)", ""},
		{"custom tags", []string{"nightly"}, []string{"latest"}, "(no version tag)", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := tagTestRepo(t, tc.local)
			f := newTagForm(r, &tagListPanel{tags: tc.remote})
			if f.Value("name") != tc.next || f.FocusedKey() != "name" {
				t.Fatalf("value = %q, focus = %q", f.Value("name"), f.FocusedKey())
			}
			sh := core.NewShared(nil)
			f.Init(sh)
			f.SetSize(sh, 80, 20)
			if view := ansi.Strip(f.View(sh)); !strings.Contains(view, tc.current+" → ") {
				t.Fatalf("missing current tag:\n%s", view)
			}
		})
	}
}

func TestNewTagFormValidation(t *testing.T) {
	r := tagTestRepo(t, []string{"v1.0.0"})
	for _, tc := range []struct{ name, status string }{
		{"", "a tag name is required"},
		{"v1.0.0", "already exists locally"},
		{"v2.0.0", "already exists on origin"},
		{"v2.0.1", ""},
	} {
		f := newTagForm(r, &tagListPanel{tags: []string{"v2.0.0"}})
		f.SetValue("name", tc.name)
		sh := core.NewShared(nil)
		sh.Chrome = &core.Chrome{Status: components.NewStatusLine()}
		router := core.NewRouter(sh, []core.TabEntry{{Title: "Test", New: func(*core.Shared) core.Screen { return f }}})
		m, _ := router.Update(keyMsg("enter"))
		if tc.status != "" {
			if m.(core.Router).Top() != f || !strings.Contains(sh.Chrome.Status.View(), tc.status) {
				t.Fatalf("%q should stay on the form with %q", tc.name, tc.status)
			}
		} else if _, ok := m.(core.Router).Top().(*components.TaskScreen); !ok {
			t.Fatal("valid tag should open the creation task")
		}
	}
}
