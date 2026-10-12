package backup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// RestoreConfirm is the phrase the installer already requires before a restore.
const RestoreConfirm = "RESTORE"

// RestoreOptions control a restore plan. Write defaults to false (dry-run).
type RestoreOptions struct {
	Options
	Device  string
	Write   bool
	Confirm string
	// OpenDevice, if set, is used instead of os.OpenFile when Write is true.
	// Tests inject a regular file here so nothing touches a real disk.
	OpenDevice func(path string) (io.WriteCloser, error)
	// BootDisks, if set, lists devices that must never be written (the live
	// USB and the installer PC). Tests inject a list; production uses
	// DetectBootDisks.
	BootDisks func() []string
	// DeviceSize, if set, reports the destination size. Tests inject it.
	DeviceSize func(path string) (int64, error)
}

// RestorePlan is what a restore would do. Execute writes only when Write is
// true and the plan was produced with a matching confirmation.
type RestorePlan struct {
	Image      Report `json:"image"`
	Device     string `json:"device,omitempty"`
	DeviceSize int64  `json:"device_size,omitempty"`
	Write      bool   `json:"write"`
	WouldWrite bool   `json:"would_write"`
}

// PlanRestore verifies the image and, if a device is named, checks that it is
// safe to write. It never opens the device for writing.
func PlanRestore(image string, opt RestoreOptions) (RestorePlan, error) {
	rep, err := Verify(image, opt.Options)
	if err != nil {
		return RestorePlan{Image: rep}, err
	}
	plan := RestorePlan{Image: rep, Device: opt.Device, Write: opt.Write}

	if opt.Write {
		if opt.Confirm != RestoreConfirm {
			return plan, stop("confirm",
				"Restore not confirmed",
				"Writing the oven storage needs -confirm RESTORE.\nDry-run is the default; nothing was written.")
		}
		if opt.Device == "" {
			return plan, stop("device",
				"No oven storage device given",
				"Pass -device (the June eMMC, once it appears) to write a backup.\nDry-run still works without a device.")
		}
	}

	if opt.Device == "" {
		plan.WouldWrite = false
		return plan, nil
	}

	boot := opt.BootDisks
	if boot == nil {
		boot = DetectBootDisks
	}
	for _, b := range boot() {
		if sameDevice(opt.Device, b) {
			return plan, stop("boot-disk",
				"That device is this computer, not the oven",
				fmt.Sprintf("%s is the disk this installer booted from (or the system disk).\nRefusing to write. The oven appears as a %d-byte USB disk after RCM/UMS.",
					opt.Device, opt.expected()))
		}
	}

	sizeFn := opt.DeviceSize
	if sizeFn == nil {
		sizeFn = StatDeviceSize
	}
	devSize, err := sizeFn(opt.Device)
	if err != nil {
		if !opt.Write {
			// Dry-run can still report a verified image when the oven is not
			// plugged in.
			return plan, nil
		}
		return plan, stop("device",
			"Oven storage device not found",
			fmt.Sprintf("%s is missing or cannot be opened: %v\nPut the oven in RCM mode and wait for the eMMC to appear, then retry.",
				opt.Device, err))
	}
	plan.DeviceSize = devSize
	if !opt.AllowMissingSize && devSize != opt.expected() {
		return plan, stop("device-size",
			"That device is not a June oven",
			fmt.Sprintf("%s is %d bytes; a June oven is %d bytes.\nRefusing to write. This check stops a restore onto the wrong disk.",
				opt.Device, devSize, opt.expected()))
	}
	if !opt.AllowMissingSize && devSize != rep.Size {
		return plan, stop("device-size",
			"The backup and the oven are different sizes",
			fmt.Sprintf("Image is %d bytes, device %s is %d bytes.\nRefusing to write.",
				rep.Size, opt.Device, devSize))
	}
	plan.WouldWrite = true
	return plan, nil
}

// Execute carries out a restore plan. Dry-run plans return nil without
// writing. Write plans copy the image onto the device and fsync.
func Execute(plan RestorePlan, opt RestoreOptions) error {
	if !opt.Write {
		return nil
	}
	if !plan.WouldWrite || plan.Device == "" {
		return stop("device",
			"Restore was not planned to write",
			"Run june-backup restore with -write -confirm RESTORE and a -device that passed the checks.")
	}
	open := opt.OpenDevice
	if open == nil {
		open = func(path string) (io.WriteCloser, error) {
			return os.OpenFile(path, os.O_WRONLY, 0)
		}
	}
	src, err := os.Open(plan.Image.Image)
	if err != nil {
		return stop("checksum",
			"Could not read the backup image",
			err.Error())
	}
	defer src.Close()
	dst, err := open(plan.Device)
	if err != nil {
		return stop("device",
			"Could not open the oven storage for writing",
			fmt.Sprintf("%s: %v", plan.Device, err))
	}
	defer dst.Close()

	buf := make([]byte, 4<<20)
	if _, err := io.CopyBuffer(dst, src, buf); err != nil {
		return stop("write",
			"Writing the oven storage failed",
			fmt.Sprintf("%v\nKeep the oven powered and connected. Do not unplug it. Retry the restore.", err))
	}
	if sc, ok := dst.(interface{ Sync() error }); ok {
		_ = sc.Sync()
	}
	return nil
}

// StatDeviceSize returns the size of a block device or regular file.
func StatDeviceSize(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if st.Mode()&os.ModeDevice != 0 {
		name := filepath.Base(path)
		b, err := os.ReadFile(filepath.Join("/sys/class/block", name, "size"))
		if err != nil {
			return 0, err
		}
		sectors, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		if err != nil {
			return 0, err
		}
		return sectors * 512, nil
	}
	return st.Size(), nil
}

// DetectBootDisks lists whole disks that currently back /, the live medium,
// or /boot. A restore must never target these.
func DetectBootDisks() []string {
	b, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		src, target := f[0], f[1]
		switch target {
		case "/", "/boot", "/run/live/medium", "/lib/live/mount/medium":
		default:
			continue
		}
		if !strings.HasPrefix(src, "/dev/") {
			continue
		}
		disk := wholeDisk(src)
		if disk == "" || seen[disk] {
			continue
		}
		seen[disk] = true
		out = append(out, disk)
	}
	return out
}

func wholeDisk(part string) string {
	base := filepath.Base(part)
	sys := filepath.Join("/sys/class/block", base, "partition")
	if _, err := os.Stat(sys); err == nil {
		parent := filepath.Base(filepath.Dir(mustReadlink(filepath.Join("/sys/class/block", base))))
		if parent != "" && parent != "." {
			return "/dev/" + parent
		}
	}
	return part
}

func mustReadlink(path string) string {
	s, err := os.Readlink(path)
	if err != nil {
		return path
	}
	if !filepath.IsAbs(s) {
		s = filepath.Join(filepath.Dir(path), s)
	}
	return filepath.Clean(s)
}

func sameDevice(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	return wholeDisk(a) == wholeDisk(b) && wholeDisk(a) != ""
}
