June Local wireless updater client
==================================

This client talks to an already-installed June Local oven over HTTPS. It does
not need ADB, RCM, UMS, a shared signing key, or inbound remote access.

1. Download the oven CA from http://<oven-ip>/local/ca.crt and trust it on the
   computer if the oven uses a per-home certificate.
2. Enter the per-install control token printed by the installer or stored on
   the boot USB at /june-logs/update-token.txt.
3. Run:

   june-wireless-update -oven https://<oven-ip> -ca june-local-ca.crt \
     -token-file update-token.txt check

   june-wireless-update -oven https://<oven-ip> -ca june-local-ca.crt \
     -token-file update-token.txt stage

4. Power-cycle the oven normally. The boot applier verifies, backs up and
   applies the staged signed APK.

The control token is a secret. Do not send it over plain HTTP or publish it.
