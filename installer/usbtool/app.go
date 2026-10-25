//go:build windows || darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"june-usb/stick"
)

// App is bound to the frontend as window.go.main.App.
type App struct {
	ctx context.Context

	mu        sync.Mutex
	cancel    context.CancelFunc // running download or hash
	busy      bool               // writing to a stick
	release   *stick.Release  // latest.json
	releases  []stick.Release // releases.json, newest first
	confirmed string          // driveKey of the stick the user agreed to erase
	iso       *ISOInfo
}

// ISOInfo describes the ISO that will be written.
type ISOInfo struct {
	Path     string `json:"path"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Verified bool   `json:"verified"` // matches a published release
	BIOS     bool   `json:"bios"`     // carries legacy BIOS boot code
}

// Info is static information for the UI.
type Info struct {
	Version  string `json:"version"`
	OS       string `json:"os"`
	GuideURL string `json:"guideUrl"`
	// ARM is true on an ARM computer (Apple chip, Snapdragon), which can make
	// the stick but can't start the installer.
	ARM bool `json:"arm"`
}

const guideURL = "https://project-july.org/setup"

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	debugf("startup")
}

// beforeClose asks before quitting while a stick is being written.
func (a *App) beforeClose(ctx context.Context) bool {
	a.mu.Lock()
	busy := a.busy
	a.mu.Unlock()
	if !busy {
		return false
	}
	answer, _ := wr.MessageDialog(ctx, wr.MessageDialogOptions{
		Type:          wr.QuestionDialog,
		Title:         "Still writing the stick",
		Message:       "Quitting now leaves the stick unusable until you prepare it again. Quit anyway?",
		Buttons:       []string{"Keep writing", "Quit"},
		DefaultButton: "Keep writing",
		CancelButton:  "Keep writing",
	})
	return answer != "Quit" && answer != "Yes"
}

func (a *App) Info() Info {
	debugf("Info called by the UI")
	// Releases set ToolVersion to the tag, e.g. "usb-v1.2"; show "1.2".
	return Info{Version: strings.TrimPrefix(stick.ToolVersion, "usb-v"), OS: runtime.GOOS, GuideURL: guideURL, ARM: stick.HostIsARM()}
}

// Release fetches the current release manifest.
func (a *App) Release() (*stick.Release, error) {
	rel, err := stick.FetchRelease(a.ctx, manifestURL())
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.release = rel
	a.mu.Unlock()
	return rel, nil
}

// offered is how many releases the app offers (and keeps cached).
const offered = 3

// Releases fetches every published release, newest first, so the user can
// pick an older one.
func (a *App) Releases() ([]stick.Release, error) {
	rels, err := stick.FetchReleases(a.ctx, releasesURL())
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.releases = rels
	a.release = &rels[0]
	a.mu.Unlock()
	// Offer the newest few; a.releases keeps them all, so an ISO the user
	// picks is still recognised if it is older.
	if len(rels) > offered {
		rels = rels[:offered]
	}
	return rels, nil
}

func manifestURL() string {
	if u := os.Getenv("JUNE_USB_MANIFEST"); u != "" {
		return u
	}
	return stick.DefaultManifestURL
}

func releasesURL() string {
	if u := os.Getenv("JUNE_USB_RELEASES"); u != "" {
		return u
	}
	return stick.DefaultReleasesURL
}

// findRelease returns the known release with this version, or with this
// SHA256 when version is empty.
func (a *App) findRelease(version, sum string) *stick.Release {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.releases {
		r := &a.releases[i]
		if (version != "" && r.Version == version) || (version == "" && r.SHA256 == sum) {
			return r
		}
	}
	if r := a.release; r != nil && ((version != "" && r.Version == version) || (version == "" && r.SHA256 == sum)) {
		return r
	}
	return nil
}

func (a *App) startJob() (context.Context, func(), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil || a.busy {
		return nil, nil, errors.New("another task is still running")
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.cancel = cancel
	return ctx, func() {
		a.mu.Lock()
		a.cancel = nil
		a.mu.Unlock()
		cancel()
	}, nil
}

// Cancel stops a running download or hash.
func (a *App) Cancel() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
}

type byteEvent struct {
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Phase string `json:"phase"` // "download" or "check"
}

// Download fetches the ISO of the given release (or reuses a verified cached
// copy).
func (a *App) Download(version string) (*ISOInfo, error) {
	rel := a.findRelease(version, "")
	if rel == nil {
		return nil, fmt.Errorf("release %s is not known yet", version)
	}
	ctx, done, err := a.startJob()
	if err != nil {
		return nil, err
	}
	defer done()
	path, err := stick.CachePath(rel)
	if err != nil {
		return nil, err
	}
	phase := "download"
	if st, err := os.Stat(path); err == nil && st.Size() == rel.Size {
		phase = "check"
	}
	if err := stick.Download(ctx, rel, path, func(d, t int64) {
		wr.EventsEmit(a.ctx, "bytes", byteEvent{d, t, phase})
	}); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, errors.New("cancelled")
		}
		return nil, err
	}
	// Keep only the versions the app offers (1.4 GB each).
	a.mu.Lock()
	keep := a.releases
	a.mu.Unlock()
	if len(keep) > offered {
		keep = keep[:offered]
	}
	if len(keep) > 0 {
		stick.PruneCache(append(append([]stick.Release{}, keep...), *rel))
	}
	return a.useISO(path, rel.SHA256, rel)
}

// ChooseISO lets the user pick an ISO they already have and checks it.
func (a *App) ChooseISO() (*ISOInfo, error) {
	path, err := wr.OpenFileDialog(a.ctx, wr.OpenDialogOptions{
		Title:            "Choose june-local-live.iso",
		Filters:          []wr.FileFilter{{DisplayName: "ISO images (*.iso)", Pattern: "*.iso"}},
		DefaultDirectory: downloadsDir(),
	})
	if err != nil || path == "" {
		return nil, err
	}
	ctx, done, err := a.startJob()
	if err != nil {
		return nil, err
	}
	defer done()
	if iso, err := stick.OpenISO(path); err != nil {
		return nil, err
	} else {
		iso.Close()
	}
	sum, err := stick.HashFile(ctx, path, func(d, t int64) {
		wr.EventsEmit(a.ctx, "bytes", byteEvent{d, t, "check"})
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, errors.New("cancelled")
		}
		return nil, err
	}
	return a.useISO(path, sum, a.findRelease("", sum))
}

func (a *App) useISO(path, sum string, rel *stick.Release) (*ISOInfo, error) {
	iso, err := stick.OpenISO(path)
	if err != nil {
		return nil, err
	}
	defer iso.Close()
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	info := &ISOInfo{
		Path: path, Version: iso.Version(), SHA256: sum, Size: st.Size(),
		Verified: rel != nil && rel.SHA256 == sum, BIOS: iso.HasBIOSBoot(),
	}
	if info.Verified {
		info.Version = rel.Version
	}
	a.mu.Lock()
	a.iso = info
	a.mu.Unlock()
	return info, nil
}

func downloadsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Downloads")
}

// Drives lists the drives the user may choose.
func (a *App) Drives(showAll bool) ([]stick.Drive, error) {
	d, err := stick.ListDrives(showAll)
	if d == nil {
		d = []stick.Drive{}
	}
	for _, x := range d {
		debugf("Drives: %s %q %d %v eligible=%v", x.ID, x.Name, x.Size, x.Volumes, x.Eligible)
	}
	debugf("Drives(showAll=%v): %d drives, err=%v", showAll, len(d), err)
	return d, err
}

// ConfirmErase asks, in a system dialog outside the web UI, whether this
// exact stick may be erased.  A yes allows one Prepare of that stick in
// "fresh" mode.  Windows dialogs only offer Yes/No; macOS shows the buttons.
func (a *App) ConfirmErase(drive stick.Drive) (bool, error) {
	answer, err := wr.MessageDialog(a.ctx, wr.MessageDialogOptions{
		Type:          wr.QuestionDialog,
		Title:         "Erase " + stickName(drive) + "?",
		Message:       eraseQuestion(drive),
		Buttons:       []string{"Cancel", "Erase"},
		DefaultButton: "Cancel",
		CancelButton:  "Cancel",
	})
	if err != nil {
		return false, err
	}
	ok := answer == "Erase" || answer == "Yes"
	a.mu.Lock()
	a.confirmed = ""
	if ok {
		a.confirmed = driveKey(drive)
	}
	a.mu.Unlock()
	return ok, nil
}

func driveKey(d stick.Drive) string {
	return fmt.Sprintf("%s|%s|%s|%d", d.ID, d.Serial, d.Name, d.Size)
}

// Prepare writes the chosen ISO to drive.  mode is "fresh" or "update".
// Progress arrives as "progress" events and log lines as "log" events.
func (a *App) Prepare(drive stick.Drive, mode string) (stick.Result, error) {
	a.mu.Lock()
	if a.busy || a.cancel != nil {
		a.mu.Unlock()
		return stick.Result{}, errors.New("another task is still running")
	}
	iso := a.iso
	if iso == nil {
		a.mu.Unlock()
		return stick.Result{}, errors.New("no installer ISO has been chosen")
	}
	// Erasing needs a fresh "yes" from ConfirmErase for this exact stick.
	confirmed := a.confirmed
	a.confirmed = ""
	if stick.Mode(mode) == stick.Fresh && confirmed != driveKey(drive) {
		a.mu.Unlock()
		return stick.Result{}, errors.New("erasing was not confirmed")
	}
	a.busy = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.busy = false
		a.mu.Unlock()
	}()

	start := time.Now()
	// Writing is not cancellable: stopping half way leaves a broken stick.
	res, err := stick.Prepare(context.Background(), stick.Options{
		Drive: drive, ISOPath: iso.Path, Mode: stick.Mode(mode),
		Progress: func(p stick.Progress) { wr.EventsEmit(a.ctx, "progress", p) },
		Log:      func(s string) { wr.EventsEmit(a.ctx, "log", s) },
	})
	if err == nil {
		wr.EventsEmit(a.ctx, "log", "Finished in "+time.Since(start).Round(time.Second).String())
	}
	return res, err
}

// stickName is how the user knows the stick: its volume labels, else its model.
func stickName(d stick.Drive) string {
	var labels []string
	for _, v := range d.Volumes {
		if v.Label != "" {
			labels = append(labels, "“"+v.Label+"”")
		}
	}
	if len(labels) == 0 {
		return d.Name
	}
	return strings.Join(labels, " and ")
}

func eraseQuestion(d stick.Drive) string {
	var where []string
	for _, v := range d.Volumes {
		if v.Mount != "" {
			where = append(where, strings.TrimSuffix(v.Mount, `\`))
		}
	}
	at := ""
	if len(where) > 0 {
		at = ", " + strings.Join(where, " ")
	}
	return fmt.Sprintf("Everything on %s (%s, %s%s) will be deleted. This cannot be undone.\n\nErase it and create the June stick?",
		stickName(d), d.Name, stick.HumanSize(d.Size), at)
}

// Steps are the progress tracker labels.
func (a *App) Steps() []string { return stick.Steps }

// OpenGuide opens the setup guide in the browser.
func (a *App) OpenGuide() { wr.BrowserOpenURL(a.ctx, guideURL) }
