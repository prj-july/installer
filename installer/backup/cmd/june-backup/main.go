// Command june-backup verifies June oven backup images and dry-runs a restore.
// Writing the oven is off by default and needs -write -confirm RESTORE.
//
//	june-backup checksum -image FILE
//	june-backup verify   -image FILE [-expected-size N]
//	june-backup latest   -dir DIR [-expected-size N]
//	june-backup restore  -image FILE|-dir DIR [-device DEV] [-dry-run|-write] [-confirm RESTORE]
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	backup "june-backup"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "checksum":
		err = runChecksum(args)
	case "verify":
		err = runVerify(args)
	case "latest":
		err = runLatest(args)
	case "restore":
		err = runRestore(args)
	case "-h", "-help", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		printStop(err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `june-backup — check a June oven backup, or dry-run a restore.

  june-backup checksum -image FILE
      Write FILE.sha256 (basename only, so it still checks after remount).

  june-backup verify -image FILE
      Check size and SHA256. Does not touch the oven.

  june-backup latest -dir DIR
      Print the newest image in DIR that passes verify.

  june-backup restore -image FILE|-dir DIR [-device DEV]
      Default: dry-run. Prints what a restore would do.
      To write: -write -confirm RESTORE (same word the installer already asks for).

Nothing is written unless -write and -confirm RESTORE are both set.
`)
}

func imageFlags(fs *flag.FlagSet) (*string, *string, *int64, *bool) {
	image := fs.String("image", "", "backup image (.img)")
	dir := fs.String("dir", "", "folder of backup images")
	size := fs.Int64("expected-size", backup.DefaultExpectedSize, "June eMMC size in bytes")
	unverified := fs.Bool("allow-unverified", false, "skip the SHA256 sidecar (unsafe)")
	return image, dir, size, unverified
}

func runChecksum(args []string) error {
	fs := flag.NewFlagSet("checksum", flag.ContinueOnError)
	image := fs.String("image", "", "backup image (.img)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *image == "" {
		return fmt.Errorf("checksum needs -image")
	}
	rep, err := backup.WriteSidecar(*image)
	if err != nil {
		return err
	}
	fmt.Printf("[+] SHA256 sidecar: %s\n", rep.Sidecar)
	fmt.Printf("[+] SHA256: %s\n", rep.SHA256)
	return nil
}

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	image, _, size, unverified := imageFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *image == "" {
		return fmt.Errorf("verify needs -image")
	}
	rep, err := backup.Verify(*image, backup.Options{ExpectedSize: *size, AllowUnverified: *unverified})
	if err != nil {
		return err
	}
	printReport(rep)
	return nil
}

func runLatest(args []string) error {
	fs := flag.NewFlagSet("latest", flag.ContinueOnError)
	_, dir, size, unverified := imageFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return fmt.Errorf("latest needs -dir")
	}
	rep, err := backup.LatestVerified(*dir, backup.Options{ExpectedSize: *size, AllowUnverified: *unverified})
	if err != nil {
		return err
	}
	printReport(rep)
	return nil
}

func runRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	image, dir, size, unverified := imageFlags(fs)
	device := fs.String("device", "", "oven eMMC device (for example /dev/sdb)")
	write := fs.Bool("write", false, "write the image (off by default)")
	dry := fs.Bool("dry-run", false, "plan only (default)")
	confirm := fs.String("confirm", "", "must be RESTORE with -write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *write && *dry {
		return fmt.Errorf("use either -dry-run or -write, not both")
	}
	opt := backup.RestoreOptions{
		Options: backup.Options{ExpectedSize: *size, AllowUnverified: *unverified},
		Device:  *device,
		Write:   *write,
		Confirm: *confirm,
	}
	img := *image
	if img == "" && *dir != "" {
		rep, err := backup.LatestVerified(*dir, opt.Options)
		if err != nil {
			return err
		}
		img = rep.Image
		fmt.Printf("[*] Using latest verified backup: %s\n", img)
	}
	if img == "" {
		return fmt.Errorf("restore needs -image or -dir")
	}
	if !*write {
		fmt.Println("[*] Dry-run restore (nothing will be written)")
	} else {
		fmt.Println("[*] Write restore requested")
	}
	plan, err := backup.PlanRestore(img, opt)
	if err != nil {
		return err
	}
	printReport(plan.Image)
	if plan.Device != "" {
		fmt.Printf("[*] Device: %s", plan.Device)
		if plan.DeviceSize > 0 {
			fmt.Printf(" (%d bytes)", plan.DeviceSize)
		}
		fmt.Println()
	} else {
		fmt.Println("[*] No -device given; image checks only.")
	}
	if err := backup.Execute(plan, opt); err != nil {
		return err
	}
	if opt.Write {
		fmt.Printf("[+] Restore complete: %s -> %s\n", plan.Image.Image, plan.Device)
	} else {
		if plan.WouldWrite {
			fmt.Println("[+] Would write the image. Nothing was written.")
		} else {
			fmt.Println("[+] Image is usable. Pass -device to check the oven disk, or -write -confirm RESTORE to write.")
		}
	}
	return nil
}

func printReport(rep backup.Report) {
	fmt.Printf("[+] Backup image OK: %s (%d bytes)\n", rep.Image, rep.Size)
	if rep.SHA256 != "" {
		fmt.Printf("[+] SHA256: %s\n", rep.SHA256)
	}
}

func printStop(err error) {
	if s, ok := err.(backup.Stop); ok {
		fmt.Fprintf(os.Stderr, "[-] %s\n", s.Title)
		if s.Detail != "" {
			// "[-] " on every line: the installer GUI only shows those lines.
			for _, line := range strings.Split(s.Detail, "\n") {
				fmt.Fprintf(os.Stderr, "[-] %s\n", line)
			}
		}
		return
	}
	fmt.Fprintf(os.Stderr, "[-] %s\n", err.Error())
}
