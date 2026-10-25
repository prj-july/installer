# june-local (Go)

The on-oven local server and camera bridge for Project July. It replaces June's
cloud endpoints on the LAN: it terminates TLS for the June hostnames, answers
the REST surface, accepts the oven/companion WebSockets, and relays signed
envelopes byte-for-byte.

The same tree builds:

- `june-local` — the local server / cloud replacement.
- `june-camerad` — the camera daemon. On a stock oven it is a client of
  `cameracapturerd`'s Unix socket and never opens `/dev/video0` itself; on
  systems without the stock daemon, `--source v4l2` captures from V4L2.

This source is mirrored into the public `prj-july/installer` repository under
`golocal/`. Prebuilt binaries are deliberately not committed; build them from
source for your target.

## Build

Requires Go 1.22 or newer. Override `GO` if your toolchain is not on `PATH`:

```sh
make oven                    # bin/june-local-linux-armv7   (oven, Android 5)
make camerad                 # bin/june-camerad-linux-armv7 (oven)
make docker                  # bin/june-local-linux-amd64   (Docker/NAS)
make tools                   # update publisher + wireless update client
make test
```

The ARM binaries are static (`CGO_ENABLED=0`) and run on the oven's Tegra K1.
The signed wireless update flow is documented in
[`../WIRELESS_UPDATES.md`](../WIRELESS_UPDATES.md).

## Run

```sh
june-local \
  --cert /path/to/leaf.crt --key /path/to/leaf.key \
  --bind 0.0.0.0 --port 443 \
  --log ./june-local.log --jsonl ./capture/frames.jsonl --append
```

Common flags:

- `--http-port` — plaintext fallback listener (default 80; 0 disables).
- `--media-dir` — live-view JPEG directory.
- `--media-url` — public base URL for rewritten `10011 image_url` values.
- `--oven-id` — optional oven device id to use before the oven's first signed
  frame; normally the oven identifies itself.
- `--mjpeg-fps` and `--mjpeg-viewer-gate` — MJPEG stream cadence and the
  viewer-driven camera gate (default on).
- `--fixture FILE` — load `device_state` / `cooking_state` / `cook_plan` from JSON so `/local/cook`
  can be tried without an oven (Home Assistant REST sensors, UI work).

`june-camerad` defaults to `--source auto`, which prefers the stock
`cameracapturerd` socket when `/system/bin/cameracapturerd` exists:

```sh
june-camerad --source auto \
  --stock-socket /juneOS2/cameracapturerd-capture.sock \
  --relay http://127.0.0.1/internal/video \
  --trigger-url http://127.0.0.1/internal/camera-bridge-enabled
```

It connects only while the gate is open and releases the socket as soon as the
viewer/cook state changes. The bridge never holds `/dev/video0` on a stock
oven, which keeps toast doneness and door-open food recognition working.

## Wire protocol notes

- **Byte-exact relay.** Relayed WebSocket frames are forwarded as the exact
  bytes received. The Ed25519+BLAKE2b signature covers the envelope with
  `"signature":""` in the app's key order, so re-marshaling through
  `encoding/json` would break verification. The relay parses only for routing
  and forwards the original bytes.
- **Pairing.** SRP-6a mailbox: the companion posts B/salt/companion_info and
  the server pushes it to the oven as `11025`; the oven's A/oven_info response
  is pushed back to the companion that minted the pairing.
- **Expires** is sent in whole seconds because the oven's `PairingPin` uses a
  signed 32-bit integer.

## Cook status

`GET /local/cook` is a compact JSON snapshot for Home Assistant (current
temperature, target, remaining timer, state). A browser that sends `Accept:
text/html` gets a live page instead; `/local/cook.json` and `/local/cook.html`
are the explicit URLs.

```yaml
# Home Assistant configuration.yaml
rest:
  - resource: http://OVEN_IP/local/cook
    scan_interval: 5
    sensor:
      - name: June oven temperature
        value_template: "{{ value_json.current_c }}"
        unit_of_measurement: "°C"
      - name: June oven state
        value_template: "{{ value_json.state }}"
      - name: June oven timer
        value_template: "{{ value_json.remaining_seconds }}"
        unit_of_measurement: "s"
```

Without an oven:

```sh
june-local --cert cert.pem --key key.pem --http-port 18080 --port 18443 \
  --bind 127.0.0.1 --append --fixture testdata/cook-baking.json
curl -s http://127.0.0.1:18080/local/cook
```

## Tests

`go test ./...` covers the WebSocket relay, the stock-cameracapturerd protocol,
the camera bridge gate, multipart cook-frame uploads, the MJPEG viewer
gate, and the cook-status JSON/page. It does not require a physical oven.
