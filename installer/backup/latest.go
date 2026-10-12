package backup

import (
	"os"
	"path/filepath"
	"strings"
)

// LatestVerified walks dir (not nested more than two levels) and returns the
// newest image whose sidecar verifies. Images that fail size or checksum are
// skipped so a restore rolls back to the last known-good backup.
func LatestVerified(dir string, opt Options) (Report, error) {
	dir = filepath.Clean(dir)
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return Report{}, stop("missing",
			"Backup folder not found",
			fmtPath(dir)+" is not a folder of backup images.")
	}
	var best Report
	var bestMod int64
	found := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() {
				rel, _ := filepath.Rel(dir, path)
				if rel != "." && strings.Count(rel, string(os.PathSeparator)) >= 2 {
					return filepath.SkipDir
				}
			}
			return nil
		}
		low := strings.ToLower(d.Name())
		if !strings.HasSuffix(low, ".img") && !strings.HasSuffix(low, ".raw") {
			return nil
		}
		found++
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rep, err := Verify(path, opt)
		if err != nil {
			return nil
		}
		mod := info.ModTime().UnixNano()
		if best.Image == "" || mod >= bestMod {
			best = rep
			bestMod = mod
		}
		return nil
	})
	if best.Image == "" {
		if found == 0 {
			return Report{}, stop("missing",
				"No backup images in that folder",
				fmtPath(dir)+" has no .img or .raw files.\nMake a backup before you try to restore.")
		}
		return Report{}, stop("checksum",
			"No verified backup in that folder",
			fmtPath(dir)+" has image files, but none passed the size and SHA256 checks.\nDo not restore a damaged backup. Make a new backup if you still can.")
	}
	return best, nil
}

func fmtPath(p string) string {
	if p == "" {
		return "(empty path)"
	}
	return p
}
