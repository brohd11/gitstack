package repoui

import (
	"context"
	"strings"
	"time"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// The Tags screen: tag actions beside origin's tags over the local ones, so whether a
// release tag is on origin is visible at once. The remote list loads asynchronously (from
// Init). New Tag is prefilled with the next semver; Delete and Push are pickers (Push
// only over tags origin lacks); Fetch reloads the remote list. It is a PopStop hub.

// remoteTagsTimeout caps the ls-remote so an unreachable origin shows an error.
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

// tagListPanel is the remote-tags ScrollContainer that handles remoteTagsMsg and keeps the
// last result, which the New and Push rows compare against without another network read.
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

// Tags returns the last successful remote load, or nil (unknown, so Push offers every
// local tag and git rejects duplicates).
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
				if !refreshesRepo(payload, r.Dir) {
					return false
				}
				setLocal()
				return true
			},
		},
	)
}

// ---------- new tag ----------

// newTagForm is the one-field create form, prefilled with repo.NextTag from the last
// remote load and the local tags.
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

// pushTagPicker lists local tags origin lacks (per the last load) and sends the picked one
// to origin. A nil remote list offers every local tag.
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
