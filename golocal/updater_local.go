package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"time"

	updatelib "june-golocal/internal/update"
)

// Two more ways to stage a Chef APK from /local, both behind the same HTTPS + update
// password checks as check/stage and both through the same boot-time transaction:
//
//   POST /local/update/upload   body: a signed bundle ZIP (manifest.json, manifest.sig and the
//                               artifact the manifest names). Verified exactly like a download.
//   POST /local/update/restore  stages the Chef APK june_reaper.sh replaced at the last update
//                               (/data/june/chef_stock_backup.apk).
//
// A restore keeps the current release sequence, so going back never re-opens older signed
// releases: the next update has to be newer than the one that was undone.

const (
	restoreArtifactID = "chef-restore"
	// A bundle is the signed APK plus two small files; the APK limit is the manifest's.
	maxUploadBytes = updatelib.MaxArtifactBytes + updatelib.MaxManifestBytes + 1<<20
)

// UpdateRestore describes the backup /local can go back to.
type UpdateRestore struct {
	Undoes     string `json:"undoes"`
	BackupSize int64  `json:"backup_size"`
	BackupAt   string `json:"backup_at,omitempty"`
}

// restoreOffer is non-nil only when a wireless update was applied (so the backup is the app
// from before it) and that backup is still on the oven. After a restore the backup holds the
// update that was undone, so it is not offered again.
func (u *Updater) restoreOffer() *UpdateRestore {
	if u.applied == nil || u.applied.ArtifactID == restoreArtifactID || u.backupFile == "" {
		return nil
	}
	st, err := os.Lstat(u.backupFile)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return nil
	}
	return &UpdateRestore{Undoes: u.applied.Release, BackupSize: st.Size(), BackupAt: st.ModTime().UTC().Format(time.RFC3339)}
}

func (u *Updater) RestoreBackup() (UpdateStatus, error) {
	if u == nil || !u.Status().Enabled {
		return u.Status(), errors.New("updater is disabled")
	}
	u.opMu.Lock()
	defer u.opMu.Unlock()
	u.mu.RLock()
	offer, applied, pending := u.restoreOffer(), u.applied, u.pending
	u.mu.RUnlock()
	if offer == nil {
		err := errors.New("there is no earlier app on this oven to go back to")
		return u.Status(), err
	}
	fail := func(err error) (UpdateStatus, error) {
		u.setError(fmt.Errorf("restore: %w", err))
		return u.Status(), err
	}
	if err := validateAPK(u.backupFile); err != nil {
		return fail(err)
	}
	f, err := os.Open(u.backupFile)
	if err != nil {
		return fail(err)
	}
	h := sha256.New()
	size, err := io.Copy(h, f)
	f.Close()
	if err != nil {
		return fail(err)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if pending != nil && pending.ArtifactID == restoreArtifactID && pending.ArtifactSHA256 == sum {
		return u.Status(), nil
	}
	artifact := updatelib.Artifact{ID: "chef", Type: "apk", Size: size, SHA256: sum}
	record := &updateRecord{
		Release: "Back to the app before " + applied.Release, Channel: applied.Channel,
		Sequence: applied.Sequence, PublishedAt: offer.BackupAt,
		Notes:      "Restored from this oven's own backup, taken when " + applied.Release + " was installed.",
		ArtifactID: restoreArtifactID, ArtifactSize: size, ArtifactSHA256: sum,
	}
	err = u.commitTransaction(record, pending, func(txnDir string) error {
		f, err := os.Open(u.backupFile)
		if err != nil {
			return err
		}
		defer f.Close()
		// Checked against the hash taken above, so a backup that changes in between is refused.
		_, err = writeVerifiedArtifact(f, artifact, txnDir)
		return err
	})
	if err != nil {
		return fail(err)
	}
	logLine("[update] restore staged: back to the app before %s", applied.Release)
	return u.Status(), nil
}

// StageFile stages a signed bundle uploaded from /local. The bundle is the publisher's
// repository directory zipped (june-update-publisher bundle), optionally inside one folder.
func (u *Updater) StageFile(body io.Reader) (UpdateStatus, error) {
	if u == nil || !u.Status().Enabled {
		return u.Status(), errors.New("updater is disabled")
	}
	u.opMu.Lock()
	defer u.opMu.Unlock()
	u.setChecking()
	fail := func(err error) (UpdateStatus, error) {
		u.setError(err)
		return u.Status(), err
	}
	if err := ensureUpdateFreeSpace(u.transactionsDir, maxUploadBytes/2); err != nil {
		return fail(err)
	}
	tmp, err := os.CreateTemp(u.transactionsDir, ".upload-*.zip")
	if err != nil {
		return fail(err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	_, err = io.Copy(tmp, body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fail(fmt.Errorf("upload: %w", err))
	}
	zr, err := zip.OpenReader(tmpName)
	if err != nil {
		return fail(errors.New("that file is not an update bundle (a .zip from Project July)"))
	}
	defer zr.Close()

	files := map[string]*zip.File{}
	var manifestFile *zip.File
	for _, f := range zr.File {
		files[f.Name] = f
		if path.Base(f.Name) == "manifest.json" && (manifestFile == nil || len(f.Name) < len(manifestFile.Name)) {
			manifestFile = f
		}
	}
	if manifestFile == nil {
		return fail(errors.New("the bundle has no manifest.json"))
	}
	dir := path.Dir(manifestFile.Name)
	inBundle := func(name string) string {
		if dir == "." {
			return name
		}
		return dir + "/" + name
	}
	sigFile := files[inBundle("manifest.sig")]
	if sigFile == nil {
		return fail(errors.New("the bundle has no manifest.sig next to manifest.json"))
	}
	manifestRaw, err := readZipLimited(manifestFile, updatelib.MaxManifestBytes)
	if err != nil {
		return fail(err)
	}
	signatureEncoded, err := readZipLimited(sigFile, updatelib.MaxSignatureBytes)
	if err != nil {
		return fail(err)
	}
	signatureRaw, err := updatelib.DecodeSignature(signatureEncoded)
	if err != nil {
		return fail(err)
	}
	if err := updatelib.VerifySignature(u.publicKey, manifestRaw, signatureRaw); err != nil {
		return fail(errors.New("the bundle's signature is not from Project July's release key"))
	}
	manifest, err := updatelib.ParseManifest(manifestRaw, u.config.Channel, u.config.Target, u.appVersion, u.now().UTC())
	if err != nil {
		return fail(err)
	}
	if manifest.Sequence <= u.currentSequence() {
		return fail(fmt.Errorf("%s is not newer than what this oven has; it never goes back to an older release", manifest.Release))
	}
	artifact := manifest.Artifacts[0]
	if artifact.ID != "chef" || artifact.Type != "apk" || !updatelib.SafeRelativePath(artifact.File) {
		return fail(errors.New("unsupported artifact"))
	}
	apk := files[inBundle(artifact.File)]
	if apk == nil {
		return fail(fmt.Errorf("the bundle is missing %s", artifact.File))
	}
	manifestHash := sha256.Sum256(manifestRaw)
	signatureHash := sha256.Sum256(signatureEncoded)
	record := &updateRecord{
		Release: manifest.Release, Channel: manifest.Channel, Sequence: manifest.Sequence,
		PublishedAt: manifest.PublishedAt, Notes: manifest.Notes,
		ArtifactID: artifact.ID, ArtifactSize: artifact.Size, ArtifactSHA256: artifact.SHA256,
		AppID: artifact.AppID, VersionCode: artifact.VersionCode,
		ManifestSHA256: hex.EncodeToString(manifestHash[:]), SignatureSHA256: hex.EncodeToString(signatureHash[:]),
	}
	u.mu.RLock()
	pending := u.pending
	u.mu.RUnlock()
	err = u.commitTransaction(record, pending, func(txnDir string) error {
		rc, err := apk.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		_, err = writeVerifiedArtifact(rc, artifact, txnDir)
		return err
	})
	if err != nil {
		return fail(err)
	}
	u.mu.Lock()
	u.status.Available = releaseFromManifest(manifest, artifact, u.now().UTC())
	u.mu.Unlock()
	logLine("[update] staged %s from an uploaded bundle", manifest.Release)
	return u.Status(), nil
}

func readZipLimited(f *zip.File, max int64) ([]byte, error) {
	if f.UncompressedSize64 > uint64(max) {
		return nil, fmt.Errorf("%s is too large", f.Name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s is too large", f.Name)
	}
	return b, nil
}
