package repoui

import (
	"context"
	"strings"
	"time"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// The Tags screen: a sidebar of tag actions beside two stacked listings —
// origin's tags over the local ones — so "is the release tag pushed yet" answers
// itself at a glance. The remote half is a network read, so it loads async
// (remoteTagsCmd, kicked off by the screen's Init) while the local half renders
// immediately from disk. The actions all stay inside this hub: New Tag is a
// one-field form prefilled with the next semver tag, Delete and Push are pickers
// over the local list (Push's filtered to what origin doesn't have), and Fetch
// just re-runs the remote load in place. The screen is the PopStop hub, so a
// finished tag task's esc lands back here, not on the Git menu below it.

// remoteTagsTimeout caps the ls-remote behind the Tags screen, so an unreachable
// origin fails the pane into an error line instead of spinning forever. Shorter
// than FetchTimeout: it's one round-trip, not a fan-out.
const remoteTagsTimeout = 30 * time.Second

// remoteTagsMsg delivers one remoteTagsCmd load. ModularScreen fans non-key msgs
// out to every panel; the tagListPanel holding the remote listing claims it.
type remoteTagsMsg struct {
	tags []string
	err  error
}

// remoteTagsCmd lists origin's tags off the UI thread (repo.RemoteTags is a
// network read) and delivers the result as a remoteTagsMsg.
func remoteTagsCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), remoteTagsTimeout)
		defer cancel()
		tags, err := repo.RemoteTags(ctx, dir)
		return remoteTagsMsg{tags: tags, err: err}
	}
}

// tagListPanel is the remote-tags ScrollContainer plus the two domain behaviors
// a component can't carry: what a remoteTagsMsg means, and remembering the
// result — the sidebar's New/Push rows diff against the last load rather than
// re-hitting the network. Embedding promotes the Panel/Focusable plumbing, so
// the only override needed is UpdatePanel — claim the load result, otherwise
// behave exactly like a ScrollContainer.
type tagListPanel struct {
	*components.ScrollContainer
	tags []string // last successful load; nil while loading or after an error
}

var _ components.Panel = (*tagListPanel)(nil)
var _ components.Focusable = (*tagListPanel)(nil)
var _ components.PanelUpdater = (*tagListPanel)(nil)
var _ components.PanelHelper = (*tagListPanel)(nil)

func (p *tagListPanel) UpdatePanel(sh *core.Shared, msg tea.Msg) (core.Action, bool) {
	if m, ok := msg.(remoteTagsMsg); ok {
		switch {
		case m.err != nil:
			p.tags = nil
			p.SetStatus("could not reach origin: " + m.err.Error())
		case len(m.tags) == 0:
			p.tags = nil
			p.SetStatus("origin has no tags")
		default:
			p.tags = m.tags
			p.SetLines(m.tags)
		}
		return core.Action{}, true
	}
	return p.ScrollContainer.UpdatePanel(sh, msg)
}

// Tags reports the last successful remote load (nil when none). The push picker
// treats nil as "unknown" and offers every local tag — git's own "already
// exists" rejection is the backstop for a stale or missing snapshot.
func (p *tagListPanel) Tags() []string { return p.tags }

// TagsScreen builds the tag view for one checkout.
func TagsScreen(sh *core.Shared, r repo.Repo) *components.ModularScreen {
	remote := &tagListPanel{ScrollContainer: components.NewScrollContainer("Remote Tags")}
	remote.SetStatus("fetching remote tags…")

	local := components.NewScrollContainer("Local Tags")
	setLocal := func() {
		if tags := repo.LocalTags(r.Dir); len(tags) > 0 {
			local.SetLines(tags)
		} else {
			local.SetStatus("no local tags")
		}
	}
	setLocal()

	sidebar := components.NewListPanel([]list.Item{
		components.Item{
			Name: "✚ New Tag",
			Desc: "tag the current commit",
			Pick: func(*core.Shared) core.Action {
				return core.Push(newTagForm(r, remote))
			},
		},
		components.Item{
			Name: "✖ Delete Tag",
			Desc: "remove a local tag",
			Pick: func(*core.Shared) core.Action {
				return core.Push(deleteTagPicker(r))
			},
		},
		components.Item{
			Name: "⇧ Push Tag",
			Desc: "push one local tag to origin",
			Pick: func(*core.Shared) core.Action {
				return core.Push(pushTagPicker(r, remote.Tags()))
			},
		},
		components.Item{
			Name: "⟳ Fetch Tags",
			Desc: "re-read origin's tags",
			Pick: func(*core.Shared) core.Action {
				remote.SetStatus("fetching remote tags…")
				return core.Async(remoteTagsCmd(r.Dir))
			},
		},
	}, "Tags", components.ListPanelOpts{})

	return components.NewModularScreen(
		[][]components.Slot{
			{{Panel: sidebar}},
			{{Panel: remote, Weight: 1}, {Panel: local, Weight: 1}},
		},
		components.ModularOpts{
			Crumb:     "Tags",
			Dir:       r.Dir, // "t" opens a terminal at this repo from the Tags screen (DirLocator)
			ColWidths: []int{30, 0},
			// The hub a finished tag task's esc returns to (Task's dismiss is
			// PopTo): without this the pop would sail past to the Git menu.
			PopStop: true,
			Init:    func(*core.Shared) tea.Cmd { return remoteTagsCmd(r.Dir) },
			Refresh: func(_ *core.Shared, payload any) bool {
				if _, ok := payload.(RefreshMsg); !ok {
					return false
				}
				setLocal()
				return true
			},
		},
	)
}

// ---------- new tag ----------

// newTagForm is the one-field create form. The field is prefilled with the next
// semver tag after the highest one origin or the local checkout knows about
// (repo.NextTag), which is what a release flow wants nine times out of ten; the
// user edits or retypes for the tenth. The remote panel doubles as the remote
// snapshot, so the prefill needs no extra network read — it's whatever the last
// load (or the in-flight one, as nil) left behind.
func newTagForm(r repo.Repo, remote *tagListPanel) *components.FormScreen {
	form := components.NewForm(components.FormOpts{
		Crumb: "New Tag",
		Fields: []components.FormField{
			components.NewHeading("Tag the current commit in " + r.Name),
			components.NewSpacer(),
			components.NewTextField("name", "Tag: ", "1.0.0"),
		},
		Focus: "name",
		Help: []key.Binding{
			core.Hint("create", core.Keys.Select),
			core.Hint("cancel", core.Keys.Back),
		},
		OnSubmit: func(sh *core.Shared, f *components.FormScreen) core.Action {
			name := strings.TrimSpace(f.Value("name"))
			if name == "" {
				return core.SetStatusAndLog("a tag name is required")
			}
			for _, t := range repo.LocalTags(r.Dir) {
				if t == name {
					return core.SetStatusAndLog("tag " + name + " already exists locally")
				}
			}
			for _, t := range remote.Tags() {
				if t == name {
					return core.SetStatusAndLog("tag " + name + " already exists on origin")
				}
			}
			return core.Replace(Task("tagging "+name+"…", opTag, r.Dir,
				func(ctx context.Context, dir string, report repo.Reporter) error {
					return repo.GitTag(ctx, dir, name, report)
				}))
		},
	})
	if next := repo.NextTag(repo.LocalTags(r.Dir), remote.Tags()); next != "" {
		form.SetValue("name", next)
	}
	return form
}

// ---------- delete ----------

// deleteTagPicker lists the local tags; picking one confirms, then deletes. The
// confirm earns its keep here: a tag that was never pushed has no other copy.
func deleteTagPicker(r repo.Repo) *components.PickerScreen {
	var items []list.Item
	for _, t := range repo.LocalTags(r.Dir) {
		items = append(items, components.Item{
			Name: t,
			Desc: "delete this local tag",
			Pick: func(*core.Shared) core.Action {
				return core.Push(components.CreateConfirmScreen(components.ConfirmSimple{
					Text: "Delete local tag " + t + " in " + r.Name + "?\n\nIf it was never pushed, it's gone for good.",
					OnYes: core.Replace(Task("deleting tag "+t+"…", opDelete, r.Dir,
						func(ctx context.Context, dir string, report repo.Reporter) error {
							return repo.GitDeleteTag(ctx, dir, t, report)
						})),
				}))
			},
		})
	}
	items = components.EnsurePlaceholder(items, "no local tags", "create one with ✚ New Tag")
	return components.NewPicker(items, components.PickerOpts{
		Title: "Delete Tag",
		Crumb: "Delete",
		Dir:   r.Dir,
	})
}

// ---------- push ----------

// pushTagPicker lists the local tags origin doesn't have (per the remote pane's
// last load); picking one runs `git push origin <tag>`. A nil remote list means
// "unknown", not "empty", so every local tag is offered — pushing one origin
// already has just surfaces git's own rejection in the task log.
func pushTagPicker(r repo.Repo, remote []string) *components.PickerScreen {
	onRemote := map[string]bool{}
	for _, t := range remote {
		onRemote[t] = true
	}
	var items []list.Item
	for _, t := range repo.LocalTags(r.Dir) {
		if onRemote[t] {
			continue
		}
		items = append(items, components.Item{
			Name: t,
			Desc: "git push origin " + t,
			Pick: func(*core.Shared) core.Action {
				return core.Push(Task("pushing tag "+t+"…", opPush, r.Dir,
					func(ctx context.Context, dir string, report repo.Reporter) error {
						return repo.GitPushTag(ctx, dir, t, report)
					}))
			},
		})
	}
	items = components.EnsurePlaceholder(items, "nothing to push", "origin has every local tag")
	return components.NewPicker(items, components.PickerOpts{
		Title: "Push Tag",
		Crumb: "Push",
		Dir:   r.Dir,
	})
}
