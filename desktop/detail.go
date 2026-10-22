package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"verdana/backend"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// tab ids: the ephemeral detail tab lives at the end and vanishes the
// moment any other tab is picked.
const (
	tabWindows   = 0
	tabInstalled = 1
	tabDiscovery = 2
	tabDev       = 3
	tabExtra     = 4
	tabHome      = 5
)

// extraTab is the ephemeral tab: either a napp page or a profile page.
type extraTab struct {
	kind   string // "napp" or "profile"
	title  string
	nappID string
	napp   backend.Napp // fallback copy for napps not in installed/discovery
	pubkey string       // hex, for profile tabs (and napp author)
}

var extraTabState *extraTab

// detailBtns are the clickables of the header and the ephemeral tab that
// don't need to be threaded through every layout call.
var detailBtns struct {
	selfProfile widget.Clickable // the header's avatar and name
	profileView widget.Clickable // a profile tab's avatar and name
	nappEvent   widget.Clickable // a napp tab's "Event" button
	nappRemove  widget.Clickable // a napp tab's "Remove" button (previews)
}

// installedNow and previewSet hold the ids of the installed and of the
// previewed napps, updatable those of either with a newer version out, all
// refreshed every frame from the state: cards and the napp page show a
// previewed napp as on disk but not installed.
var installedNow, previewSet, updatable map[string]bool

// openLabel is what the Open button of a napp says: "Open" for an installed
// napp, "Preview" for any other (downloaded already or not), which opens it
// without installing it.
func openLabel(n backend.Napp) string {
	if installedNow[n.ID] {
		return "Open"
	}
	return "Preview"
}

// openOrPreview runs the Open button (see openLabel). checkUpdate is for the
// buttons outside the napp's own page, which checks for updates on its own.
func openOrPreview(n backend.Napp, installedSet, busy map[string]bool, checkUpdate bool) {
	if in, ok := backend.InstalledNapp(n.ID); ok && installedSet[n.ID] {
		backend.Launch(in)
	} else if !busy[n.ID] {
		go backend.Preview(n, checkUpdate)
	}
}

// cardLabels says what a discovery or profile card offers for a napp: the
// primary button (install/uninstall), the second one (update, when the napp
// is on disk and has one) and whether the Open/Preview button shows.
func cardLabels(n backend.Napp, installedSet, busy map[string]bool) (primary, second string, open bool) {
	local := installedSet[n.ID] || previewSet[n.ID]
	primary = "Install"
	if installedSet[n.ID] {
		primary = "Uninstall"
	}
	if busy[n.ID] {
		return "Working…", "", installedSet[n.ID]
	}
	if local && updatable[n.ID] {
		second = "Update"
	}
	return primary, second, true
}

// cardSecondClicked runs a card's second button (see cardLabels).
func cardSecondClicked(n backend.Napp, installedSet, busy map[string]bool) {
	if busy[n.ID] {
		return
	}
	if _, second, _ := cardLabels(n, installedSet, busy); second == "Update" {
		go backend.Update(n.ID)
	}
}

func openNappTab(n backend.Napp) {
	pubkey := ""
	if n.Author.Hex() != "" {
		pubkey = n.Author.Hex()
	}
	extraTabState = &extraTab{kind: "napp", title: truncate(n.Label(), 18), nappID: n.ID, napp: n, pubkey: pubkey}
	ensureProfile(pubkey)
	go backend.CheckForUpdate(n.ID)
	setTab(tabExtra)
}

func openProfileTab(pubkeyHex string) {
	pubkeyHex = strings.TrimSpace(pubkeyHex)
	if pubkeyHex == "" {
		return
	}
	name := pubkeyHex
	if len(name) > 12 {
		name = name[:12] + "…"
	}
	if p := cachedProfile(pubkeyHex); p != nil && p.ShortName != "" {
		name = truncate(p.ShortName, 18)
	}
	extraTabState = &extraTab{kind: "profile", title: name, pubkey: pubkeyHex}
	ensureProfile(pubkeyHex)
	ensureAuthorNapps(pubkeyHex)
	setTab(tabExtra)
}

func clearExtraTab() { extraTabState = nil }

// ─── cached profile + author napps ──────────────────────────────
// Fetches run off the Gio loop; results land here and invalidate the
// window. Layout reads only the caches, never blocks.

var (
	profMu       sync.Mutex
	profileCache = make(map[string]backend.ProfileDetail)
	profileBusy  = make(map[string]bool)

	authorMu       sync.Mutex
	authorNapps    = make(map[string][]backend.Napp)
	authorFetching = make(map[string]bool)
	authorErr      = make(map[string]string)
)

func cachedProfile(pubkeyHex string) *backend.ProfileDetail {
	profMu.Lock()
	defer profMu.Unlock()
	if p, ok := profileCache[pubkeyHex]; ok {
		cp := p
		return &cp
	}
	return nil
}

func ensureProfile(pubkeyHex string) {
	if pubkeyHex == "" {
		return
	}
	profMu.Lock()
	if _, ok := profileCache[pubkeyHex]; ok {
		profMu.Unlock()
		return
	}
	if profileBusy[pubkeyHex] {
		profMu.Unlock()
		return
	}
	profileBusy[pubkeyHex] = true
	profMu.Unlock()
	go func() {
		p := backend.FetchProfileDetail(pubkeyHex)
		profMu.Lock()
		profileCache[pubkeyHex] = p
		delete(profileBusy, pubkeyHex)
		profMu.Unlock()
		if gioWin != nil {
			gioWin.Invalidate()
		}
	}()
}

func cachedAuthorNapps(pubkeyHex string) ([]backend.Napp, bool, string) {
	authorMu.Lock()
	defer authorMu.Unlock()
	n := authorNapps[pubkeyHex]
	return n, authorFetching[pubkeyHex], authorErr[pubkeyHex]
}

func ensureAuthorNapps(pubkeyHex string) {
	if pubkeyHex == "" {
		return
	}
	authorMu.Lock()
	if _, ok := authorNapps[pubkeyHex]; ok {
		authorMu.Unlock()
		return
	}
	if authorFetching[pubkeyHex] {
		authorMu.Unlock()
		return
	}
	authorFetching[pubkeyHex] = true
	delete(authorErr, pubkeyHex)
	authorMu.Unlock()
	go func() {
		list := backend.FetchAuthorNapps(pubkeyHex)
		authorMu.Lock()
		authorNapps[pubkeyHex] = list
		authorFetching[pubkeyHex] = false
		authorMu.Unlock()
		if gioWin != nil {
			gioWin.Invalidate()
		}
	}()
}

// detailNapp resolves the napp a napp tab shows: the freshest copy the
// launcher knows (so install/uninstall/update reflect immediately),
// falling back to the copy taken when the tab was opened.
func detailNapp(tab *extraTab) backend.Napp {
	if tab == nil {
		return backend.Napp{}
	}
	if n, ok := backend.LookupNapp(tab.nappID); ok {
		return n
	}
	return tab.napp
}

// ─── detail layouts ─────────────────────────────────────────────

func detailRow(th *material.Theme, label, value string) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		if value == "" {
			return layout.Dimensions{}
		}
		return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := emph(material.Body2(th, label+": "))
					l.Color = currentTheme().subtle
					return l.Layout(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, value)
					return l.Layout(gtx)
				}),
			)
		})
	})
}

// layoutNappDetail is the ephemeral napp page: everything the launcher
// knows, plus install/uninstall, and open and pin/unpin (when installed).
func layoutNappDetail(
	gtx layout.Context,
	th *material.Theme,
	tab *extraTab,
	openBtn, primaryBtn, updateBtn, authorBtn, pinBtn *widget.Clickable,
	installedSet map[string]bool,
	busy map[string]bool,
	st backend.State,
) layout.Dimensions {
	n := detailNapp(tab)
	if n.ID == "" {
		l := material.Body2(th, "Napp not found.")
		l.Color = currentTheme().muted
		return l.Layout(gtx)
	}
	installed := installedSet[n.ID]
	previewed := previewSet[n.ID]
	local := installed || previewed
	working := busy[n.ID]
	primaryLabel := "Install"
	if installed {
		primaryLabel = "Uninstall"
	}
	if working {
		primaryLabel = "Working…"
	}
	hasUpdate := n.UpdateAvailable != nil
	pinned := slices.ContainsFunc(st.Pinned, func(p backend.Napp) bool { return p.ID == n.ID })

	authorName, authorPic := "", ""
	if n.Author.Hex() != "" {
		if p := cachedProfile(n.Author.Hex()); p != nil && p.ShortName != "" {
			authorName, authorPic = p.ShortName, p.Picture
		} else {
			authorName = n.AuthorShortName()
			if authorName == "" {
				authorName = n.Author.Hex()
				if len(authorName) > 16 {
					authorName = authorName[:16] + "…"
				}
			}
		}
	}

	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return nappIcon(gtx, n, 56)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					t := material.H6(th, n.Label())
					t.Font.Weight = font.Bold
					return t.Layout(gtx)
				}),
			)
		}),
	}
	if n.Description != "" {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(th, n.Description)
				l.Color = currentTheme().subtle
				return l.Layout(gtx)
			})
		}))
	}
	// author row: the only tappable part of a card/page that leads to a
	// profile instead of a napp page
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			inner := func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return avatar(gtx, authorPic, 22)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						c := material.Body2(th, authorName)
						c.Color = currentTheme().muted
						return c.Layout(gtx)
					}),
				)
			}
			if authorBtn == nil || n.Author.Hex() == "" {
				return inner(gtx)
			}
			pointer.CursorPointer.Add(gtx.Ops)
			return withTooltip(gtx, authorBtn, "See "+authorName+"'s profile and the other napps they published", func(gtx layout.Context) layout.Dimensions {
				return authorBtn.Layout(gtx, inner)
			})
		})
	}))
	// action buttons row
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if (!installed && working) || openBtn == nil {
						return layout.Dimensions{}
					}
					pointer.CursorPointer.Add(gtx.Ops)
					label := openLabel(n)
					b := material.Button(th, openBtn, label)
					b.Background = currentTheme().suggestBg
					b.Color = currentTheme().suggestFg
					return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return withTooltip(gtx, openBtn, nappButtonTip(label, n), b.Layout)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					// a previewed napp has no Uninstall: this removes it
					if !previewed || installed || working {
						return layout.Dimensions{}
					}
					label := "Remove"
					pointer.CursorPointer.Add(gtx.Ops)
					btn := &detailBtns.nappRemove
					b := material.Button(th, btn, label)
					b.Background = currentTheme().chipBg
					b.Color = currentTheme().chipFg
					return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return withTooltip(gtx, btn, nappButtonTip(label, n), b.Layout)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !local || pinBtn == nil {
						return layout.Dimensions{}
					}
					pointer.CursorPointer.Add(gtx.Ops)
					label := "Pin"
					if pinned {
						label = "Unpin"
					}
					b := material.Button(th, pinBtn, label)
					b.Background = currentTheme().chipBg
					b.Color = currentTheme().chipFg
					return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return withTooltip(gtx, pinBtn, pinTip(pinned), b.Layout)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if primaryBtn == nil {
						return layout.Dimensions{}
					}
					pointer.CursorPointer.Add(gtx.Ops)
					b := material.Button(th, primaryBtn, primaryLabel)
					if installed {
						b.Background = currentTheme().chipBg
						b.Color = currentTheme().chipFg
					}
					return withTooltip(gtx, primaryBtn, nappButtonTip(primaryLabel, n), b.Layout)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !hasUpdate || updateBtn == nil {
						return layout.Dimensions{}
					}
					pointer.CursorPointer.Add(gtx.Ops)
					return withTooltip(gtx, updateBtn, nappButtonTip("Update", n), material.Button(th, updateBtn, "Update").Layout)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !hasUpdate || updateBtn == nil {
						return layout.Dimensions{}
					}
					return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					btn := &detailBtns.nappEvent
					b := material.Button(th, btn, "Event")
					b.Background = currentTheme().chipBg
					b.Color = currentTheme().chipFg
					return withTooltip(gtx, btn, "Open the event that describes "+n.Label()+" in a napp that views kind "+strconv.Itoa(int(n.KindOf()))+" events", b.Layout)
				}),
			)
		})
	}))
	if hasUpdate {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(th, "An update is available.")
				l.Color = currentTheme().subtle
				return l.Layout(gtx)
			})
		}))
	}
	children = append(children,
		detailRow(th, "ID", n.ID),
		detailRow(th, "d", n.D),
		detailRow(th, "Author", n.Author.Hex()),
		detailRow(th, "Created", n.CreatedAt.Time().Format(time.RFC3339)),
		detailRow(th, "Actions", strings.Join(n.Actions, ", ")),
		detailRow(th, "Requires", strings.Join(n.Requires, ", ")),
		detailRow(th, "Servers", strings.Join(n.Servers, ", ")),
		detailRow(th, "Icon", n.Icon),
	)
	if len(n.Paths) > 0 {
		paths := make([]string, 0, len(n.Paths))
		for _, p := range n.Paths {
			short := p.Sha256
			if len(short) > 8 {
				short = short[:8]
			}
			paths = append(paths, fmt.Sprintf("%s (%s)", p.Path, short))
		}
		children = append(children, detailRow(th, "Files", strings.Join(paths, ", ")))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// layoutProfileDetail is the ephemeral profile page: name, about,
// picture, nip05, plus the napps fetched from the author's write relays
// and the discovery relays.
func layoutProfileDetail(
	gtx layout.Context,
	th *material.Theme,
	tab *extraTab,
	list *widget.List,
	cardBtns, openBtns, actionBtns, updateBtns []widget.Clickable,
	installedSet map[string]bool,
	busy map[string]bool,
	readAuthBtn *widget.Clickable,
	st backend.State,
) layout.Dimensions {
	pubkey := ""
	if tab != nil {
		pubkey = tab.pubkey
	}
	p := backend.ProfileDetail{Pubkey: pubkey}
	if cp := cachedProfile(pubkey); cp != nil {
		p = *cp
	}
	napps, fetching, fetchErr := cachedAuthorNapps(pubkey)

	self := pubkey != "" && pubkey == st.Pubkey

	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// the avatar and name fire view:0 from the launcher, so the
			// user picks the napp that shows this profile
			name := p.ShortName
			if name == "" {
				name = pubkey
			}
			pointer.CursorPointer.Add(gtx.Ops)
			btn := &detailBtns.profileView
			return withTooltip(gtx, btn, "Open "+name+"'s profile in a napp", func(gtx layout.Context) layout.Dimensions {
				return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return avatar(gtx, p.Picture, 56)
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							t := material.H6(th, name)
							t.Font.Weight = font.Bold
							return t.Layout(gtx)
						}),
					)
				})
			})
		}),
	}
	if p.DisplayName != "" && p.DisplayName != p.Name {
		children = append(children, detailRow(th, "Display name", p.DisplayName))
	}
	if p.Name != "" {
		children = append(children, detailRow(th, "Name", p.Name))
	}
	children = append(children,
		detailRow(th, "About", p.About),
		detailRow(th, "NIP-05", p.NIP05),
		detailRow(th, "Website", p.Website),
		detailRow(th, "npub", p.Npub),
		detailRow(th, "Pubkey", p.Pubkey),
	)
	if self {
		children = append(children,
			layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				t := emph(material.Body2(th, "Settings"))
				t.Color = currentTheme().subtle
				return t.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return readAuthButton(gtx, th, readAuthBtn, st.ReadAuth)
			}),
		)
	}
	children = append(children,
		layout.Rigid(layout.Spacer{Height: unit.Dp(24)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			t := emph(material.Body2(th, "Published napps"))
			t.Color = currentTheme().subtle
			return t.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
	)
	if fetchErr != "" {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, fetchErr)
			l.Color = currentTheme().danger
			return l.Layout(gtx)
		}))
	} else if fetching {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, "Fetching napps…")
			l.Color = currentTheme().muted
			return l.Layout(gtx)
		}))
	} else if len(napps) == 0 {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, "No napps found on their relays.")
			l.Color = currentTheme().muted
			return l.Layout(gtx)
		}))
	}
	// the whole page scrolls as one list: item 0 is the profile header,
	// info rows and settings; the rest are the author's napps
	return material.List(th, list).Layout(gtx, 1+len(napps), func(gtx layout.Context, i int) layout.Dimensions {
		if i == 0 {
			return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
			})
		}
		i--
		n := napps[i]
		var cardBtn, openBtn, actBtn, updBtn *widget.Clickable
		if i < len(cardBtns) {
			cardBtn = &cardBtns[i]
		}
		label, updLabel, canOpen := cardLabels(n, installedSet, busy)
		if i < len(openBtns) && canOpen {
			openBtn = &openBtns[i]
		}
		if i < len(actionBtns) {
			actBtn = &actionBtns[i]
		}
		if i < len(updateBtns) && updLabel != "" {
			updBtn = &updateBtns[i]
		}
		// inside a profile the author row is the profile itself:
		// no nested author button
		return renderNappCard(gtx, th, cardBtn, nil, openBtn, actBtn, updBtn, label, updLabel, nil, false, n)
	})
}
