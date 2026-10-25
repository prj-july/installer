# June Local installer mockups

Rendered from the real installer UI (version 0.8, build 20261024-1602-b5ca5293).

| File | Stage | What it shows |
| --- | --- | --- |
| `png/01-home.png` | Installer home | June Local installer home. The header shows the version and build; the tiles are Install June Local, Install with your own CA, Back up, Restore and More. |
| `png/02-basic-options.png` | Basic install options | Basic install: the oven name (default june) and the persistence option. The project CA is added to the oven's trust store and the factory messaging app stays in place. |
| `png/03-basic-persistence.png` | Basic install with persistence | Persistence selected: 'Keep June Local after a factory reset' stages the cook-modes, camera, light and messaging patches in /system so a factory reset keeps them. |
| `png/04-custom-options.png` | Custom install options | Custom install with a per-home CA: the oven LAN IP and hostname. The hostname defaults to june.local. |
| `png/05-connect-waiting.png` | Connect the oven (waiting) | Steps on the left, the USB/SW2 illustration on the right, and the oven-found box below. Waiting for RCM mode. |
| `png/06-connect-found.png` | Oven found, 3 s auto-start | The oven is detected; the box counts down from 3 seconds and starts the install automatically. Back cancels the countdown. |
| `png/07-installing.png` | Installing | Progress, elapsed time and the step tracker while the installer runs. The fan note explains that the fans spin up and stay fast. |
| `png/08-success.png` | Install success + QR codes | Success screen with the restart/shutdown buttons and QR codes for the oven status page, setup guide, CA certificate and companion app (phone access on). |
| `png/09-backup-destination.png` | Backup destination | Choose the backup drive, folder and file name. The current boot USB is listed and marked as such. |
| `png/10-restore-confirm.png` | Restore confirmation | Restore: choose the backup image and type RESTORE to confirm. The warning explains that the oven is overwritten. |
| `png/11-phone-sheet.png` | Phone access | Connect a phone: the QR code opens the same installer on the phone so jobs can be started and followed from there. |
