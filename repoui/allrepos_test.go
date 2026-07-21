package repoui

import (
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"
)

func tgt(name string, ahead, behind int) repo.Repo {
	return repo.Repo{Name: name, Sync: repo.GitSync{Ahead: ahead, Behind: behind, Tracking: true}}
}

func TestConfirmBodyPull(t *testing.T) {
	body := confirmBody("pull", []repo.Repo{
		tgt("dialogic", 0, 2),
		tgt("phantom_camera", 0, 0),
		tgt("debug_draw", 0, 1),
	})

	if !strings.Contains(body, "Pull 3 repo(s) — fast-forward only:") {
		t.Errorf("missing header:\n%s", body)
	}
	for _, want := range []string{"dialogic", "2 behind origin", "phantom_camera", "up to date", "debug_draw", "1 behind origin"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "diverged will fail and be skipped") {
		t.Errorf("pull body should warn about diverged repos:\n%s", body)
	}
}

func TestConfirmBodyPush(t *testing.T) {
	body := confirmBody("push", []repo.Repo{
		tgt("dialogic", 3, 0),
		tgt("phantom_camera", 0, 0),
	})
	if !strings.Contains(body, "Push 2 repo(s):") {
		t.Errorf("missing header:\n%s", body)
	}
	if !strings.Contains(body, "3 to push") || !strings.Contains(body, "nothing to push") {
		t.Errorf("push annotations wrong:\n%s", body)
	}
	if strings.Contains(body, "fast-forward") || strings.Contains(body, "diverged") {
		t.Errorf("push body should not carry pull's caveats:\n%s", body)
	}
}

func TestConfirmBodyFetchNoAnnotations(t *testing.T) {
	// Fetch acts on all repos regardless of state, so no per-repo count is meaningful.
	body := confirmBody("fetch", []repo.Repo{tgt("a", 5, 5), tgt("b", 0, 0)})
	if !strings.Contains(body, "Fetch 2 repo(s):") {
		t.Errorf("missing header:\n%s", body)
	}
	if strings.Contains(body, "behind") || strings.Contains(body, "to push") {
		t.Errorf("fetch body should carry no divergence annotations:\n%s", body)
	}
}

func TestConfirmBodyCaps(t *testing.T) {
	var many []repo.Repo
	for i := 0; i < 20; i++ {
		many = append(many, tgt("repo"+string(rune('a'+i)), 0, 1))
	}
	body := confirmBody("pull", many)
	if n := strings.Count(body, "behind origin"); n != maxConfirmList {
		t.Errorf("listed %d repos, want cap of %d:\n%s", n, maxConfirmList, body)
	}
	if !strings.Contains(body, "… and 8 more") {
		t.Errorf("body should say how many were omitted:\n%s", body)
	}
	if !strings.Contains(body, "Pull 20 repo(s)") {
		t.Errorf("the count must be the true total, not the shown subset:\n%s", body)
	}
}

func TestScopeTargetsIncludeRoot(t *testing.T) {
	scope := Scope{Label: "all", Repos: func(*core.Shared) []repo.Repo {
		return []repo.Repo{tgt("nested", 0, 0)}
	}}
	root := RootOption{Repo: func(*core.Shared) (repo.Repo, bool) {
		return repo.Repo{Name: "base", Root: true}, true
	}}
	noRoot := RootOption{}

	// Toggle off: scopes-only, even with a provider.
	if got := scopeTargets(scope, root, false, nil); len(got) != 1 || got[0].Name != "nested" {
		t.Errorf("scopeTargets(includeRoot=false) = %+v, want just [nested]", got)
	}
	// Toggle on with a yielding provider: the root rides along, appended after the scope repos.
	got := scopeTargets(scope, root, true, nil)
	if len(got) != 2 || got[1].Name != "base" {
		t.Errorf("scopeTargets(includeRoot=true) = %+v, want [nested base]", got)
	}
	// Toggle on but no provider: scopes-only.
	if got := scopeTargets(scope, noRoot, true, nil); len(got) != 1 {
		t.Errorf("scopeTargets(nil provider, includeRoot=true) = %+v, want just [nested]", got)
	}
	// Toggle on but the provider yields nothing (base not a checkout): scopes-only.
	dryRoot := RootOption{Repo: func(*core.Shared) (repo.Repo, bool) { return repo.Repo{}, false }}
	if got := scopeTargets(scope, dryRoot, true, nil); len(got) != 1 {
		t.Errorf("scopeTargets(ok=false, includeRoot=true) = %+v, want just [nested]", got)
	}
}

func TestPastTense(t *testing.T) {
	for verb, want := range map[string]string{"fetch": "fetched", "pull": "pulled", "push": "pushed"} {
		if got := pastTense(verb); got != want {
			t.Errorf("pastTense(%q) = %q, want %q", verb, got, want)
		}
	}
}

func TestTitleWord(t *testing.T) {
	for in, want := range map[string]string{"pull": "Pull", "push": "Push", "fetch": "Fetch", "": ""} {
		if got := titleWord(in); got != want {
			t.Errorf("titleWord(%q) = %q, want %q", in, got, want)
		}
	}
}
