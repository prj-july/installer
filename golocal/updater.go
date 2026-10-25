package main

import (
	"archive/zip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	updatelib "june-golocal/internal/update"
)

const (
	updateControlHeader     = "X-June-Update-Token"
	updateTokenBytes        = 32
	updateDownloadTimeout   = 15 * time.Minute
	updateManifestTimeout   = 20 * time.Second
	updateStageFreeOverhead = 32 << 20
	updateTransactionPrefix = "txn-"
)

type updaterConfigFile struct {
	Schema       int    `json:"schema"`
	Enabled      bool   `json:"enabled"`
	Channel      string `json:"channel"`
	ManifestURL  string `json:"manifest_url"`
	SignatureURL string `json:"signature_url"`
	PublicKey    string `json:"public_key"`
	Target       string `json:"target"`
	MinSequence  int64  `json:"min_sequence,omitempty"`
}

type updateRecord struct {
	Release         string `json:"release"`
	Channel         string `json:"channel"`
	Sequence        int64  `json:"sequence"`
	PublishedAt     string `json:"published_at"`
	Notes           string `json:"notes,omitempty"`
	ArtifactID      string `json:"artifact_id"`
	ArtifactFile    string `json:"artifact_file"`
	ArtifactSize    int64  `json:"artifact_size"`
	ArtifactSHA256  string `json:"artifact_sha256"`
	AppID           string `json:"app_id,omitempty"`
	VersionCode     int64  `json:"version_code,omitempty"`
	StagedAt        string `json:"staged_at,omitempty"`
	AppliedAt       string `json:"applied_at,omitempty"`
	ManifestSHA256  string `json:"manifest_sha256,omitempty"`
	SignatureSHA256 string `json:"signature_sha256,omitempty"`
}

type UpdateRelease struct {
	Release        string `json:"release"`
	Channel        string `json:"channel"`
	Sequence       int64  `json:"sequence"`
	PublishedAt    string `json:"published_at"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	Notes          string `json:"notes,omitempty"`
	ArtifactID     string `json:"artifact_id"`
	ArtifactSize   int64  `json:"artifact_size"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	AppID          string `json:"app_id,omitempty"`
	VersionCode    int64  `json:"version_code,omitempty"`
}

type UpdatePending struct {
	Release        string `json:"release"`
	Channel        string `json:"channel"`
	Sequence       int64  `json:"sequence"`
	ArtifactSize   int64  `json:"artifact_size"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	StagedAt       string `json:"staged_at"`
}

type UpdateStatus struct {
	Configured      bool           `json:"configured"`
	Enabled         bool           `json:"enabled"`
	Channel         string         `json:"channel,omitempty"`
	Target          string         `json:"target,omitempty"`
	State           string         `json:"state"`
	CurrentRelease  string         `json:"current_release,omitempty"`
	CurrentSequence int64          `json:"current_sequence,omitempty"`
	AppliedAt       string         `json:"applied_at,omitempty"`
	Available       *UpdateRelease `json:"available,omitempty"`
	Pending         *UpdatePending `json:"pending,omitempty"`
	LastCheckedAt   string         `json:"last_checked_at,omitempty"`
	LastError       string         `json:"last_error,omitempty"`
}

type updaterOptions struct {
	ConfigPath     string
	DataDir        string
	TokenFile      string
	PersistentFile string
	AppVersion     string
	AllowInsecure  bool
	SkipPlatform   bool
	TokenOverride  string
	Client         *http.Client
	Now            func() time.Time
}

type Updater struct {
	mu     sync.RWMutex
	opMu   sync.Mutex
	status UpdateStatus

	config          updaterConfigFile
	publicKey       ed25519.PublicKey
	manifestURL     string
	signatureURL    string
	dataDir         string
	transactionsDir string
	readyFile       string
	appliedFile     string
	persistentFile  string
	tokenFile       string
	token           string
	appVersion      string
	allowHTTP       bool
	skipPlatform    bool
	client          *http.Client
	now             func() time.Time
	applied         *updateRecord
	pending         *updateRecord
}

func createUpdater(cfg *Config) *Updater {
	u, err := newUpdater(updaterOptions{
		ConfigPath:     cfg.UpdaterConfig,
		DataDir:        cfg.UpdatesDir,
		TokenFile:      cfg.UpdateTokenFile,
		PersistentFile: cfg.UpdatePersistentFile,
		AppVersion:     appVersion,
	})
	if err != nil {
		if cfg.UpdaterConfig != "" {
			logLine("[update] disabled: %v", err)
		}
		return &Updater{status: UpdateStatus{State: "disabled", LastError: err.Error(), Target: updatelib.TargetJuneOvenAndroid5}}
	}
	if u.status.Enabled {
		logLine("[update] enabled channel=%s current=%s pending=%t", u.status.Channel, u.status.CurrentRelease, u.status.Pending != nil)
	}
	return u
}

func newUpdater(opts updaterOptions) (*Updater, error) {
	if opts.AppVersion == "" {
		opts.AppVersion = appVersion
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.DataDir == "" {
		opts.DataDir = "/data/june/updates"
	}
	if opts.TokenFile == "" {
		opts.TokenFile = filepath.Join(opts.DataDir, "control-token")
	}
	if opts.PersistentFile == "" {
		opts.PersistentFile = "/system/etc/june/chef_update.json"
	}
	u := &Updater{
		dataDir:         opts.DataDir,
		transactionsDir: filepath.Join(opts.DataDir, "transactions"),
		readyFile:       filepath.Join(opts.DataDir, "ready"),
		appliedFile:     filepath.Join(opts.DataDir, "applied.json"),
		persistentFile:  opts.PersistentFile,
		tokenFile:       opts.TokenFile,
		appVersion:      opts.AppVersion,
		allowHTTP:       opts.AllowInsecure,
		skipPlatform:    opts.SkipPlatform,
		now:             opts.Now,
		status: UpdateStatus{
			State:  "disabled",
			Target: updatelib.TargetJuneOvenAndroid5,
		},
	}

	raw, err := os.ReadFile(opts.ConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return u, nil
		}
		return u, fmt.Errorf("read updater config: %w", err)
	}
	u.status.Configured = true
	if err := json.Unmarshal(raw, &u.config); err != nil {
		return u, fmt.Errorf("parse updater config: %w", err)
	}
	if u.config.Schema != 1 {
		return u, fmt.Errorf("unsupported updater config schema %d", u.config.Schema)
	}
	if u.config.MinSequence < 0 {
		return u, errors.New("updater config min_sequence cannot be negative")
	}
	u.status.Channel = u.config.Channel
	if u.config.Target == "" {
		u.config.Target = updatelib.TargetJuneOvenAndroid5
	}
	u.status.Target = u.config.Target
	if !u.config.Enabled {
		u.status.Enabled = false
		u.status.State = "disabled"
		return u, nil
	}
	if err := validateRemoteURL(u.config.ManifestURL, u.allowHTTP); err != nil {
		return u, fmt.Errorf("manifest_url: %w", err)
	}
	if u.config.SignatureURL == "" {
		u.config.SignatureURL = strings.TrimRight(u.config.ManifestURL, "/") + ".sig"
	}
	if err := validateRemoteURL(u.config.SignatureURL, u.allowHTTP); err != nil {
		return u, fmt.Errorf("signature_url: %w", err)
	}
	if u.config.Target != updatelib.TargetJuneOvenAndroid5 {
		return u, fmt.Errorf("unsupported updater target %q", u.config.Target)
	}
	if !u.skipPlatform && runtime.GOARCH != "arm" {
		return u, fmt.Errorf("target %s requires an ARMv7 oven build, running %s", u.config.Target, runtime.GOARCH)
	}
	u.publicKey, err = updatelib.ParsePublicKey(u.config.PublicKey)
	if err != nil {
		return u, err
	}
	u.manifestURL = u.config.ManifestURL
	u.signatureURL = u.config.SignatureURL

	if err := os.MkdirAll(u.transactionsDir, 0o755); err != nil {
		return u, fmt.Errorf("create update directory: %w", err)
	}
	u.token, err = loadOrCreateUpdateToken(u.tokenFile, opts.TokenOverride)
	if err != nil {
		return u, err
	}
	if opts.Client != nil {
		u.client = opts.Client
	} else {
		u.client = &http.Client{
			Timeout: updateDownloadTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("too many redirects")
				}
				return validateRemoteURL(req.URL.String(), u.allowHTTP)
			},
		}
	}
	if err := u.loadRecords(); err != nil {
		return u, err
	}
	u.status.Enabled = true
	switch {
	case u.pending != nil:
		u.status.State = "staged"
	case u.applied != nil:
		u.status.State = "up-to-date"
	default:
		u.status.State = "ready"
	}
	if u.status.CurrentSequence < u.config.MinSequence {
		u.status.CurrentSequence = u.config.MinSequence
	}
	return u, nil
}

func (u *Updater) loadRecords() error {
	var applied, persistent *updateRecord
	if err := readJSONFile(u.appliedFile, &applied); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read applied update: %w", err)
	}
	if err := readJSONFile(u.persistentFile, &persistent); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read persistent applied update: %w", err)
	}
	if applied != nil && persistent != nil && applied.Sequence == persistent.Sequence &&
		(applied.Release != persistent.Release || applied.ArtifactSHA256 != persistent.ArtifactSHA256) {
		return errors.New("applied and persistent update records disagree at the same sequence")
	}
	installed := applied
	installedPath := u.appliedFile
	if persistent != nil && (installed == nil || persistent.Sequence > installed.Sequence) {
		installed = persistent
		installedPath = u.persistentFile
	}
	pending, err := u.loadPendingTransaction()
	if err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.applied, u.pending = installed, pending
	if installed != nil {
		u.status.CurrentRelease = installed.Release
		u.status.CurrentSequence = installed.Sequence
		u.status.AppliedAt = installed.AppliedAt
		if u.status.AppliedAt == "" {
			if st, err := os.Stat(installedPath); err == nil {
				u.status.AppliedAt = st.ModTime().UTC().Format(time.RFC3339)
			}
		}
	}
	if pending != nil {
		u.status.Pending = pendingUpdate(pending)
	}
	return nil
}

func (u *Updater) loadPendingTransaction() (*updateRecord, error) {
	readyStat, err := os.Lstat(u.readyFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect update commit marker: %w", err)
	}
	if readyStat.Mode()&os.ModeSymlink != 0 || !readyStat.Mode().IsRegular() {
		return nil, errors.New("update commit marker is not a regular file")
	}
	ready, err := os.ReadFile(u.readyFile)
	if err != nil {
		return nil, fmt.Errorf("read update commit marker: %w", err)
	}
	name := strings.TrimSpace(string(ready))
	if !validTransactionName(name) {
		return nil, errors.New("update commit marker contains an invalid transaction name")
	}
	dir := filepath.Join(u.transactionsDir, name)
	st, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("committed update transaction: %w", err)
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return nil, errors.New("committed update transaction is not a regular directory")
	}
	var pending updateRecord
	if err := readJSONFile(filepath.Join(dir, "pending.json"), &pending); err != nil {
		return nil, fmt.Errorf("read committed update record: %w", err)
	}
	hashFile, err := os.ReadFile(filepath.Join(dir, "pending.sha256"))
	if err != nil {
		return nil, fmt.Errorf("read committed update hash: %w", err)
	}
	fields := strings.Fields(string(hashFile))
	if len(fields) < 1 || len(fields[0]) != sha256.Size*2 || fields[0] != pending.ArtifactSHA256 {
		return nil, errors.New("committed update hash marker does not match its record")
	}
	stage := filepath.Join(dir, "chef_cookmodes.apk")
	stageStat, err := os.Lstat(stage)
	if err != nil {
		return nil, fmt.Errorf("committed update artifact: %w", err)
	}
	if stageStat.Mode()&os.ModeSymlink != 0 || !stageStat.Mode().IsRegular() || stageStat.Size() != pending.ArtifactSize {
		return nil, errors.New("committed update artifact is missing, unsafe, or the wrong size")
	}
	pending.ArtifactFile = stage
	return &pending, nil
}

func validTransactionName(name string) bool {
	if !strings.HasPrefix(name, updateTransactionPrefix) || len(name) != len(updateTransactionPrefix)+32 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(name, updateTransactionPrefix))
	return err == nil
}

func readJSONFile(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func pendingUpdate(r *updateRecord) *UpdatePending {
	if r == nil {
		return nil
	}
	return &UpdatePending{
		Release: r.Release, Channel: r.Channel, Sequence: r.Sequence,
		ArtifactSize: r.ArtifactSize, ArtifactSHA256: r.ArtifactSHA256, StagedAt: r.StagedAt,
	}
}

func (u *Updater) Status() UpdateStatus {
	if u == nil {
		return UpdateStatus{State: "disabled", Target: updatelib.TargetJuneOvenAndroid5}
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	st := u.status
	if st.Available != nil {
		available := *st.Available
		st.Available = &available
	}
	if st.Pending != nil {
		pending := *st.Pending
		st.Pending = &pending
	}
	return st
}

func (u *Updater) Check(ctx context.Context) (UpdateStatus, error) {
	if u == nil || !u.Status().Enabled {
		return u.Status(), errors.New("updater is disabled")
	}
	u.opMu.Lock()
	defer u.opMu.Unlock()
	u.setChecking()
	manifest, _, _, err := u.checkLocked(ctx)
	if err != nil {
		u.setError(err)
		return u.Status(), err
	}
	if manifest.Sequence <= u.currentSequence() {
		u.markUpToDate()
		return u.Status(), nil
	}
	u.markAvailable(manifest, u.now().UTC())
	return u.Status(), nil
}

func (u *Updater) Stage(ctx context.Context) (UpdateStatus, error) {
	if u == nil || !u.Status().Enabled {
		return u.Status(), errors.New("updater is disabled")
	}
	u.opMu.Lock()
	defer u.opMu.Unlock()
	u.setChecking()
	manifest, manifestRaw, signatureRaw, err := u.checkLocked(ctx)
	if err != nil {
		u.setError(err)
		return u.Status(), err
	}
	if manifest.Sequence <= u.currentSequence() {
		u.markUpToDate()
		return u.Status(), nil
	}
	artifact := manifest.Artifacts[0]
	u.mu.RLock()
	pending := u.pending
	u.mu.RUnlock()
	if pending != nil && pending.Sequence == manifest.Sequence && pending.ArtifactSHA256 == artifact.SHA256 {
		u.mu.Lock()
		u.status.Pending = pendingUpdate(pending)
		u.status.State = "staged"
		u.status.LastError = ""
		u.status.LastCheckedAt = u.now().UTC().Format(time.RFC3339)
		u.mu.Unlock()
		return u.Status(), nil
	}

	ctx, cancel := context.WithTimeout(ctx, updateDownloadTimeout)
	defer cancel()
	downloadURL, err := updatelib.ResolveArtifactURL(u.manifestURL, artifact)
	if err != nil {
		u.setError(err)
		return u.Status(), err
	}
	if err := validateRemoteURL(downloadURL.String(), u.allowHTTP); err != nil {
		u.setError(err)
		return u.Status(), err
	}
	if err := ensureUpdateFreeSpace(u.transactionsDir, artifact.Size); err != nil {
		u.setError(err)
		return u.Status(), err
	}
	txnName, txnDir, err := u.createUpdateTransaction()
	if err != nil {
		u.setError(err)
		return u.Status(), err
	}
	cleanupTxn := true
	defer func() {
		if cleanupTxn {
			_ = os.RemoveAll(txnDir)
		}
	}()
	_, err = u.downloadArtifact(ctx, downloadURL, artifact, txnDir)
	if err != nil {
		u.setError(err)
		return u.Status(), err
	}
	manifestHash := sha256.Sum256(manifestRaw)
	signatureHash := sha256.Sum256(signatureRaw)
	record := &updateRecord{
		Release: manifest.Release, Channel: manifest.Channel, Sequence: manifest.Sequence,
		PublishedAt: manifest.PublishedAt, Notes: manifest.Notes,
		ArtifactID:     artifact.ID,
		ArtifactFile:   filepath.Join(u.transactionsDir, txnName, "chef_cookmodes.apk"),
		ArtifactSize:   artifact.Size,
		ArtifactSHA256: artifact.SHA256, AppID: artifact.AppID, VersionCode: artifact.VersionCode,
		StagedAt:       u.now().UTC().Format(time.RFC3339),
		ManifestSHA256: hex.EncodeToString(manifestHash[:]), SignatureSHA256: hex.EncodeToString(signatureHash[:]),
	}
	if err := writeJSONAtomic(filepath.Join(txnDir, "pending.json"), record, 0o644); err != nil {
		u.setError(fmt.Errorf("write pending update record: %w", err))
		return u.Status(), err
	}
	hashLine := artifact.SHA256 + "  chef_cookmodes.apk\n"
	if err := writeTextAtomic(filepath.Join(txnDir, "pending.sha256"), hashLine, 0o644); err != nil {
		u.setError(fmt.Errorf("write pending hash marker: %w", err))
		return u.Status(), err
	}
	if err := syncDir(txnDir); err != nil {
		u.setError(fmt.Errorf("sync update transaction: %w", err))
		return u.Status(), err
	}
	finalDir := filepath.Join(u.transactionsDir, txnName)
	if err := os.Rename(txnDir, finalDir); err != nil {
		u.setError(fmt.Errorf("commit update transaction: %w", err))
		return u.Status(), err
	}
	cleanupTxn = false
	if err := syncDir(u.transactionsDir); err != nil {
		_ = os.RemoveAll(finalDir)
		u.setError(fmt.Errorf("sync committed update directory: %w", err))
		return u.Status(), err
	}
	if err := writeTextAtomic(u.readyFile, txnName+"\n", 0o644); err != nil {
		u.setError(fmt.Errorf("publish update commit marker: %w", err))
		return u.Status(), err
	}
	if pending != nil {
		u.removePreviousTransaction(pending)
	}
	u.mu.Lock()
	u.pending = record
	u.status.Pending = pendingUpdate(record)
	u.status.Available = releaseFromManifest(manifest, artifact, u.now().UTC())
	u.status.State = "staged"
	u.status.LastCheckedAt = u.now().UTC().Format(time.RFC3339)
	u.status.LastError = ""
	u.mu.Unlock()
	return u.Status(), nil
}

func (u *Updater) createUpdateTransaction() (string, string, error) {
	for attempt := 0; attempt < 5; attempt++ {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return "", "", err
		}
		name := updateTransactionPrefix + hex.EncodeToString(raw)
		dir := filepath.Join(u.transactionsDir, "."+name+".tmp")
		if err := os.Mkdir(dir, 0o755); err == nil {
			if err := syncDir(u.transactionsDir); err != nil {
				_ = os.RemoveAll(dir)
				return "", "", err
			}
			return name, dir, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", "", err
		}
	}
	return "", "", errors.New("could not allocate a unique update transaction")
}

func (u *Updater) removePreviousTransaction(record *updateRecord) {
	if record == nil {
		return
	}
	name := filepath.Base(filepath.Dir(record.ArtifactFile))
	if !validTransactionName(name) {
		return
	}
	_ = os.RemoveAll(filepath.Join(u.transactionsDir, name))
}

func (u *Updater) checkLocked(ctx context.Context) (updatelib.Manifest, []byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, updateManifestTimeout)
	defer cancel()
	manifestRaw, err := u.fetchLimited(ctx, u.manifestURL, updatelib.MaxManifestBytes)
	if err != nil {
		return updatelib.Manifest{}, nil, nil, fmt.Errorf("fetch manifest: %w", err)
	}
	signatureEncoded, err := u.fetchLimited(ctx, u.signatureURL, updatelib.MaxSignatureBytes)
	if err != nil {
		return updatelib.Manifest{}, nil, nil, fmt.Errorf("fetch signature: %w", err)
	}
	signatureRaw, err := updatelib.DecodeSignature(signatureEncoded)
	if err != nil {
		return updatelib.Manifest{}, nil, nil, err
	}
	if err := updatelib.VerifySignature(u.publicKey, manifestRaw, signatureRaw); err != nil {
		return updatelib.Manifest{}, nil, nil, err
	}
	manifest, err := updatelib.ParseManifest(
		manifestRaw, u.config.Channel, u.config.Target, u.appVersion, u.now().UTC(),
	)
	if err != nil {
		return updatelib.Manifest{}, nil, nil, err
	}
	return manifest, manifestRaw, signatureEncoded, nil
}

func (u *Updater) fetchLimited(ctx context.Context, rawURL string, max int64) ([]byte, error) {
	if err := validateRemoteURL(rawURL, u.allowHTTP); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, application/octet-stream")
	req.Header.Set("User-Agent", "june-local-updater/"+u.appVersion)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > max {
		return nil, fmt.Errorf("response is larger than %d bytes", max)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("response is larger than %d bytes", max)
	}
	return body, nil
}

func (u *Updater) downloadArtifact(ctx context.Context, uurl *url.URL, artifact updatelib.Artifact, destDir string) (string, error) {
	if artifact.ID != "chef" || artifact.Type != "apk" {
		return "", errors.New("unsupported artifact")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uurl.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.android.package-archive, application/octet-stream")
	req.Header.Set("User-Agent", "june-local-updater/"+u.appVersion)
	resp, err := u.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > artifact.Size {
		return "", fmt.Errorf("artifact Content-Length %d exceeds signed size %d", resp.ContentLength, artifact.Size)
	}
	tmp, err := os.CreateTemp(destDir, ".chef-*.part")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		_ = tmp.Close()
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	hash := sha256.New()
	limited := &io.LimitedReader{R: resp.Body, N: artifact.Size + 1}
	n, err := io.Copy(io.MultiWriter(tmp, hash), limited)
	if err != nil {
		return "", err
	}
	if n != artifact.Size {
		return "", fmt.Errorf("artifact size mismatch: downloaded %d bytes, signed %d", n, artifact.Size)
	}
	gotHash := hex.EncodeToString(hash.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(gotHash), []byte(artifact.SHA256)) != 1 {
		return "", fmt.Errorf("artifact SHA-256 mismatch: got %s", gotHash)
	}
	if err := tmp.Chmod(0o644); err != nil {
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := validateAPK(tmpName); err != nil {
		return "", err
	}
	stagePath := filepath.Join(destDir, "chef_cookmodes.apk")
	if err := os.Rename(tmpName, stagePath); err != nil {
		return "", err
	}
	cleanup = false
	if err := syncDir(destDir); err != nil {
		return "", err
	}
	return stagePath, nil
}

func validateAPK(path string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("downloaded artifact is not a ZIP/APK: %w", err)
	}
	defer zr.Close()
	haveManifest, haveDex := false, false
	for _, f := range zr.File {
		switch f.Name {
		case "AndroidManifest.xml":
			haveManifest = true
		case "classes.dex":
			haveDex = true
		}
		if haveManifest && haveDex {
			return nil
		}
	}
	return errors.New("downloaded APK is missing AndroidManifest.xml or classes.dex")
}

func ensureUpdateFreeSpace(dir string, artifactSize int64) error {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return fmt.Errorf("check update free space: %w", err)
	}
	need := uint64(artifactSize)*2 + updateStageFreeOverhead
	available := st.Bavail * uint64(st.Bsize)
	if available < need {
		return fmt.Errorf("not enough free space for update: need %d MiB, have %d MiB", need>>20, available>>20)
	}
	return nil
}

func validateRemoteURL(raw string, allowHTTP bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.User != nil || u.Host == "" {
		return errors.New("URL must have a host and no embedded credentials")
	}
	if u.Scheme == "https" {
		return nil
	}
	if allowHTTP && u.Scheme == "http" {
		return nil
	}
	return errors.New("URL must use HTTPS")
}

func loadOrCreateUpdateToken(path, override string) (string, error) {
	if override != "" {
		if !validUpdateToken(override) {
			return "", errors.New("update control token must be 64 hexadecimal characters")
		}
		return override, nil
	}
	st, err := os.Lstat(path)
	if err == nil {
		if st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
			return "", fmt.Errorf("update control token %s must be a regular file, not a symlink", path)
		}
		if stat, ok := st.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Geteuid() {
			return "", fmt.Errorf("update control token %s is not owned by uid %d", path, os.Geteuid())
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return "", fmt.Errorf("protect update control token: %w", err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read update control token: %w", err)
		}
		token := strings.TrimSpace(string(b))
		if !validUpdateToken(token) {
			return "", fmt.Errorf("update control token file %s is invalid", path)
		}
		return token, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read update control token: %w", err)
	}
	raw := make([]byte, updateTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := writeTextAtomic(path, token+"\n", 0o600); err != nil {
		return "", fmt.Errorf("create update control token: %w", err)
	}
	logLine("[update] generated control token at %s", path)
	return token, nil
}

func validUpdateToken(token string) bool {
	if len(token) != updateTokenBytes*2 {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}

func writeJSONAtomic(path string, value any, mode os.FileMode) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeTextAtomic(path, string(b), mode)
}

func writeTextAtomic(path, text string, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (u *Updater) authorized(token string) bool {
	if u == nil || u.token == "" || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(u.token)) == 1
}

func (u *Updater) currentSequence() int64 {
	u.mu.RLock()
	defer u.mu.RUnlock()
	current := u.config.MinSequence
	if u.applied != nil && u.applied.Sequence > current {
		current = u.applied.Sequence
	}
	return current
}

func (u *Updater) setChecking() {
	u.mu.Lock()
	u.status.State = "checking"
	u.status.LastError = ""
	u.mu.Unlock()
}

func (u *Updater) setError(err error) {
	u.mu.Lock()
	u.status.State = "error"
	u.status.LastError = err.Error()
	u.status.LastCheckedAt = u.now().UTC().Format(time.RFC3339)
	u.mu.Unlock()
}

func (u *Updater) markUpToDate() {
	u.mu.Lock()
	u.status.State = "up-to-date"
	u.status.Available = nil
	u.status.LastError = ""
	u.status.LastCheckedAt = u.now().UTC().Format(time.RFC3339)
	u.mu.Unlock()
}

func (u *Updater) markAvailable(m updatelib.Manifest, now time.Time) {
	artifact := m.Artifacts[0]
	u.mu.Lock()
	u.status.Available = releaseFromManifest(m, artifact, now)
	u.status.State = "update-available"
	u.status.LastError = ""
	u.status.LastCheckedAt = now.UTC().Format(time.RFC3339)
	u.mu.Unlock()
}

func releaseFromManifest(m updatelib.Manifest, a updatelib.Artifact, now time.Time) *UpdateRelease {
	return &UpdateRelease{
		Release: m.Release, Channel: m.Channel, Sequence: m.Sequence,
		PublishedAt: m.PublishedAt, ExpiresAt: m.ExpiresAt, Notes: m.Notes,
		ArtifactID: a.ID, ArtifactSize: a.Size, ArtifactSHA256: a.SHA256,
		AppID: a.AppID, VersionCode: a.VersionCode,
	}
}

func sameRequestOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

func (u *Updater) handleLocalUpdate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/local/update":
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "update": u.Status()})
		return
	case r.Method != http.MethodPost:
		w.Header().Set("Allow", "GET, POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "method not allowed"})
		return
	}
	if !sameRequestOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "error": "cross-origin update request refused"})
		return
	}
	if r.TLS == nil {
		writeJSON(w, http.StatusUpgradeRequired, map[string]any{
			"success": false, "error": "update controls require HTTPS; open https://<oven>/local",
		})
		return
	}
	if !u.authorized(r.Header.Get(updateControlHeader)) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "error": "valid update control token required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), updateDownloadTimeout+updateManifestTimeout)
	defer cancel()
	var (
		status UpdateStatus
		err    error
	)
	switch r.URL.Path {
	case "/local/update/check":
		status, err = u.Check(ctx)
	case "/local/update/stage":
		status, err = u.Stage(ctx)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"success": false, "error": err.Error(), "update": status,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "update": status})
}
