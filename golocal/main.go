package main

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Cert                 string
	Key                  string
	CA                   string
	Port                 int
	HTTPPort             int
	Bind                 string
	Timeout              int
	ObserveOnly          bool
	OvenPath             string
	OvenPubkey           string
	OvenID               string
	Identity             string // companion identity.json from june_capture.py (--identity)
	IdentityFile         string // local oven identity record, minted/loaded here (--identity-file)
	SeedTokenFile        string // installer-seeded v2 token, survives a factory reset (--seed-token)
	AccountsDB           string // Android account database, probed read-only (--accounts-db)
	MintIdentity         bool   // create identity.json with a fresh token when absent (--mint-identity)
	SeedAccounts         bool   // refused at runtime; the pre-Android boot ensure owns accounts.db
	LogPath              string
	JsonlPath            string
	Tag                  string
	Append               bool
	ContentRoot          string
	CompanionState       string
	MediaDir             string
	MediaURL             string
	MJPEGFps             int
	MJPEGViewerGate      bool
	Fixture              string
	UpdaterConfig        string
	UpdatesDir           string
	UpdateTokenFile      string
	UpdatePersistentFile string
	Updater              *Updater
}

var (
	logMu     sync.Mutex
	logPath   string
	jsonlMu   sync.Mutex
	jsonlPath string
	runTag    string
	mediaURL  string
)

func logLine(format string, args ...any) {
	line := time.Now().Format("2006-01-02 15:04:05.000") + " " + fmt.Sprintf(format, args...)
	logMu.Lock()
	defer logMu.Unlock()
	fmt.Println(line)
	if logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.WriteString(line + "\n")
			_ = f.Close()
		}
	}
}

func writeJSONL(record map[string]any) {
	if jsonlPath == "" {
		return
	}
	record["ts"] = float64(time.Now().UnixNano()) / 1e9
	if runTag != "" {
		record["run"] = runTag
	}
	b, err := jsonMarshal(record)
	if err != nil {
		return
	}
	jsonlMu.Lock()
	defer jsonlMu.Unlock()
	if f, err := os.OpenFile(jsonlPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.Write(append(b, '\n'))
		_ = f.Close()
	}
}

func main() {
	cfg := parseFlags()
	mediaURL = cfg.MediaURL

	if !cfg.Append {
		for _, p := range []string{cfg.LogPath, cfg.JsonlPath} {
			if p != "" {
				if _, err := os.Stat(p); err == nil {
					log.Fatalf("refusing to start: capture file already exists: %s\nCaptures are never overwritten. Pick a new timestamped filename, or pass --append.", p)
				}
			}
		}
	}
	logPath = cfg.LogPath
	jsonlPath = cfg.JsonlPath
	runTag = cfg.Tag
	go rotateLogs()
	if dir := filepath.Dir(cfg.JsonlPath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	if dir := filepath.Dir(cfg.LogPath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	logLine("[init] June Local %s (%s, %s)", appVersion, appCommit, appBuild)
	logLine("[init] run tag=%q log=%q jsonl=%q", runTag, logPath, jsonlPath)
	writeJSONL(map[string]any{"event": "run-start", "log": cfg.LogPath, "jsonl": cfg.JsonlPath})
	initInteriorLight()

	if cfg.Identity != "" {
		// --identity is the *companion* identity from june_capture.py (it holds
		// the oven's public signing key). The oven's own identity record is
		// --identity-file; the two are deliberately separate documents.
		if b, err := os.ReadFile(cfg.Identity); err == nil {
			var idt map[string]any
			if jsonUnmarshal(b, &idt) == nil {
				if cfg.OvenPubkey == "" {
					cfg.OvenPubkey, _ = idt["oven_public_signing_key"].(string)
				}
				logLine("[init] loaded companion identity %s", cfg.Identity)
			}
		}
	}

	// Load (or mint) the local oven identity and log one redacted summary. The
	// Android account row itself can only be created before Android starts, so
	// this only observes it; the status page turns a missing row into a red
	// "identity-missing" problem.
	initOvenIdentity(cfg)
	cfg.Updater = createUpdater(cfg)

	if _, err := os.Stat(cfg.Cert); err != nil {
		log.Fatalf("missing cert: %s", cfg.Cert)
	}
	if _, err := os.Stat(cfg.Key); err != nil {
		log.Fatalf("missing key: %s", cfg.Key)
	}
	cert, err := tls.LoadX509KeyPair(cfg.Cert, cfg.Key)
	if err != nil {
		log.Fatalf("load cert: %v", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS10,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			logLine("[tls] SNI=%q from %s", hello.ServerName, hello.Conn.RemoteAddr())
			return &cert, nil
		},
	}

	hub := newHub(cfg.ObserveOnly, cfg.CompanionState)
	if cfg.OvenID != "" {
		hub.setOvenID(cfg.OvenID)
	}
	if cfg.Fixture != "" {
		if err := loadStatusFixture(hub, cfg.Fixture); err != nil {
			log.Fatalf("fixture %s: %v", cfg.Fixture, err)
		}
		logLine("[init] loaded cook fixture %s", cfg.Fixture)
	}
	startStatusRefresher(hub, cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handleRoot(hub, cfg, w, r)
	})

	var listeners []net.Listener
	tlsLn, err := tls.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port), tlsCfg)
	if err != nil {
		log.Fatalf("listen %s:%d: %v", cfg.Bind, cfg.Port, err)
	}
	listeners = append(listeners, tlsLn)
	logLine("[init] TLS on %s:%d (cert=%s)", cfg.Bind, cfg.Port, cfg.Cert)

	if cfg.HTTPPort > 0 {
		plainLn, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Bind, cfg.HTTPPort))
		if err != nil {
			log.Fatalf("listen %s:%d: %v", cfg.Bind, cfg.HTTPPort, err)
		}
		listeners = append(listeners, plainLn)
		logLine("[init] plaintext on %s:%d", cfg.Bind, cfg.HTTPPort)
	}

	mode := "RELAY"
	if cfg.ObserveOnly {
		mode = "OBSERVE-ONLY (no relay)"
	}
	logLine("==============================================================================")
	logLine("[init] June local server (Go) mode=%s", mode)
	logLine("[init] point DNS for api.junelife.com AND messaging.junelife.com here.")
	logLine("==============================================================================")

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 15 * time.Second}
	errCh := make(chan error, len(listeners))
	for _, ln := range listeners {
		go func(l net.Listener) { errCh <- srv.Serve(l) }(ln)
	}
	log.Fatal(<-errCh)
}

func rotateLogs() {
	const maxLogSize = 32 << 20 // 32 MiB
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for range t.C {
		for _, p := range []string{logPath, jsonlPath} {
			if p == "" {
				continue
			}
			if st, err := os.Stat(p); err == nil && st.Size() > maxLogSize {
				_ = os.Rename(p, p+".1")
			}
		}
	}
}

func parseFlags() *Config {
	cfg := &Config{}
	flag.StringVar(&cfg.Cert, "cert", "certs/junelife_chain_godaddy_intermediate.crt", "TLS cert chain")
	flag.StringVar(&cfg.Key, "key", "certs/june-local.key", "TLS private key")
	flag.StringVar(&cfg.CA, "ca", "/system/etc/june_ca.crt", "CA certificate file for /local/ca.crt (optional)")
	flag.IntVar(&cfg.Port, "port", 443, "TLS listen port")
	flag.IntVar(&cfg.HTTPPort, "http-port", 80, "plaintext listen port (0 = disabled)")
	flag.StringVar(&cfg.Bind, "bind", "0.0.0.0", "bind address")
	flag.IntVar(&cfg.Timeout, "timeout", 600, "socket timeout (seconds)")
	flag.BoolVar(&cfg.ObserveOnly, "observe-only", false, "log everything, relay nothing")
	flag.StringVar(&cfg.OvenPath, "oven-path", "", "pin the oven WebSocket path")
	flag.StringVar(&cfg.OvenPubkey, "oven-pubkey", "", "base64 oven public signing key")
	flag.StringVar(&cfg.OvenID, "oven-id", "", "optional oven device id to use before the oven connects")
	flag.StringVar(&cfg.Identity, "identity", "", "companion identity.json from june_capture.py (not the oven identity; see --identity-file)")
	flag.StringVar(&cfg.Identity, "companion-identity", "", "alias for --identity (companion identity.json)")
	flag.StringVar(&cfg.IdentityFile, "identity-file", defaultIdentityFile, "local oven identity record; created when absent by --mint-identity")
	flag.StringVar(&cfg.SeedTokenFile, "seed-token", defaultSeedTokenFile, "installer-seeded v2 token copied into identity.json when it is absent")
	flag.StringVar(&cfg.AccountsDB, "accounts-db", defaultAccountsDB, "Android accounts.db probed read-only for the June account row")
	flag.BoolVar(&cfg.MintIdentity, "mint-identity", true, "create identity.json with a fresh v2 token when none exists")
	flag.BoolVar(&cfg.SeedAccounts, "seed-accounts", false, "refused at runtime: june-local never writes accounts.db (the pre-Android boot ensure does)")
	flag.StringVar(&cfg.LogPath, "log", "local_cloud.log", "log file")
	flag.StringVar(&cfg.JsonlPath, "jsonl", "capture/local_cloud_frames.jsonl", "JSONL capture file")
	flag.StringVar(&cfg.Tag, "tag", "", "provenance tag stamped into every JSONL record")
	flag.BoolVar(&cfg.Append, "append", false, "allow appending to existing log/JSONL files")
	// Absolute by default: the live root filesystem is mounted read-only, so a
	// path relative to / (the service working directory) cannot be created.
	flag.StringVar(&cfg.ContentRoot, "content-root", "/data/june/upstream-content", "presets content mirror")
	flag.StringVar(&cfg.CompanionState, "companion-state", "companions_state.json", "companion registry file")
	flag.StringVar(&cfg.MediaDir, "media-dir", "media", "live-view JPEG directory")
	flag.StringVar(&cfg.MediaURL, "media-url", "", "public base URL for rewritten 10011 image_url (empty = keep original)")
	flag.IntVar(&cfg.MJPEGFps, "mjpeg-fps", 10, "MJPEG stream frame rate")
	flag.BoolVar(&cfg.MJPEGViewerGate, "mjpeg-viewer-gate", true, "open the camera gate while an MJPEG client is watching")
	flag.StringVar(&cfg.Fixture, "fixture", "", "JSON file with device_state/cook_plan for local testing (no oven)")
	flag.StringVar(&cfg.UpdaterConfig, "updater-config", "/system/etc/june/updater.json", "signed update repository configuration")
	flag.StringVar(&cfg.UpdatesDir, "updates-dir", "/data/june/updates", "atomic update staging directory")
	flag.StringVar(&cfg.UpdateTokenFile, "update-token-file", "/data/june/updates/control-token", "per-install wireless update control token")
	flag.StringVar(&cfg.UpdatePersistentFile, "update-persistent-record", "/system/etc/june/chef_update.json", "release metadata retained beside the persistent Chef APK")
	flag.Parse()
	return cfg
}

// handleRoot routes between REST and WebSocket.
func handleRoot(hub *Hub, cfg *Config, w http.ResponseWriter, r *http.Request) {
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		serveWS(hub, cfg, w, r)
		return
	}
	handleREST(hub, cfg, w, r)
}

// serveWS hijacks the connection, performs the RFC 6455 handshake, and runs the
// relay session.
func serveWS(hub *Hub, cfg *Config, w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket unsupported", http.StatusInternalServerError)
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return
	}

	role := classify(r.URL.Path, cfg.OvenPath)
	key := r.Header.Get("Sec-WebSocket-Key")
	accept := wsAccept(key)
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n" +
		"X-June-Cloud-Region: AMER PROD\r\n\r\n"
	if _, err := conn.Write([]byte(resp)); err != nil {
		_ = conn.Close()
		return
	}

	logLine("[ws] upgraded role=%s path=%s", role, r.URL.Path)
	writeJSONL(map[string]any{"dir": "ws-open", "role": role, "path": r.URL.Path, "headers": headerMap(r.Header)})

	ws := newWSConn(conn, rw.Reader)
	peer := &Peer{ws: ws, addr: r.RemoteAddr, path: r.URL.Path, role: role, hub: hub, alive: true}
	hub.attach(peer)
	closeReason := "peer-closed"
	defer func() {
		writeJSONL(map[string]any{"dir": "ws-close", "role": role, "path": r.URL.Path, "reason": closeReason})
		hub.detach(peer)
		ws.close()
	}()

	_ = conn.SetReadDeadline(time.Now().Add(time.Duration(cfg.Timeout) * time.Second))
	for {
		opcode, payload, err := ws.readMessage()
		if err != nil {
			break
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Duration(cfg.Timeout) * time.Second))
		switch opcode {
		case 0x1, 0x2:
			text := payload
			if opcode == 0x2 {
				// binary: forward verbatim as binary
				relayBinary(hub, peer, text)
				continue
			}
			relay(hub, peer, text)
		}
	}
}

func relayBinary(hub *Hub, peer *Peer, raw []byte) {
	// Binary frames are rare; forward them byte-exactly to the counterpart.
	logLine("[%s->%s] binary %dB", peer.role, counterRole(peer.role), len(raw))
	writeJSONL(map[string]any{"dir": peer.role, "path": peer.path, "raw_b64": b64Encode(raw)})
	if hub.observeOnly {
		return
	}
	for _, t := range hub.targetsFor(peer) {
		_ = t.ws.writeFrame(0x2, raw)
	}
}

func classify(path string, ovenPath string) string {
	if ovenPath != "" && strings.Split(path, "?")[0] == ovenPath {
		return "oven"
	}
	if strings.Contains(path, "companion") {
		return "companion"
	}
	return "oven"
}

func headerMap(h http.Header) map[string]any {
	m := map[string]any{}
	for k, v := range h {
		key := strings.ToLower(k)
		switch key {
		case "authorization", "proxy-authorization", "x-june-update-token", "cookie", "set-cookie":
			m[key] = "<redacted>"
		default:
			m[key] = strings.Join(v, ", ")
		}
	}
	return m
}

// jsonMarshal / jsonUnmarshal / b64Encode indirections keep the rest of the code
// free of direct encoding/json imports where raw bytes matter.
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func jsonUnmarshal(b []byte, v any) error {
	return json.Unmarshal(b, v)
}

func b64Encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
