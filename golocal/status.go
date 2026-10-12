package main

// June Local status: a cached, read-only snapshot of the oven, the local
// server, the installed components and the usual failure modes.
//
// Everything expensive (component hashes, install.json, certificate parsing,
// statfs, network interfaces, preset counts) is refreshed by a background
// goroutine.  The HTTP handlers only merge cheap live state from the hub, so
// the page can poll frequently without touching the disk or spawning tools.

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	statusRefreshInterval = 15 * time.Second
	statusErrorRing       = 20
)

var (
	appVersion   = "0.5"
	appCommit    = "dev"
	appBuild     = "dev"
	processStart = time.Now()

	lastOvenFrameNano atomic.Int64
	lastLightOn       atomic.Bool

	clientSeenMu sync.Mutex
	clientSeen   = map[string]int64{}

	lastCommandMu sync.Mutex
	lastCommand   CommandStatus

	lightMu   sync.Mutex
	lightLast = LightStatus{}

	errorsMu     sync.Mutex
	statusErrors []ErrorEntry

	componentHashOnce sync.Once
	componentHashes   = map[string]string{}
	componentHashMu   sync.RWMutex

	statusMu   sync.RWMutex
	statusData StatusData
)

type VersionStatus struct {
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	BuildDate     string `json:"build_date"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	Restarts      int    `json:"restarts"`
}

type OvenStatus struct {
	Connection          string `json:"connection"`
	LastFrameAgeSeconds int64  `json:"last_frame_age_seconds"`
	Model               string `json:"model"`
	Android             string `json:"android"`
	SystemSlot          string `json:"system_slot"`
	CollectorMounted    bool   `json:"collector_mounted"`
}

type CookStatus struct {
	State            string          `json:"state"`
	Mode             string          `json:"mode,omitempty"`
	CurrentC         float64         `json:"current_c,omitempty"`
	TargetC          float64         `json:"target_c,omitempty"`
	RemainingSeconds int64           `json:"remaining_seconds,omitempty"`
	EtaSeconds       int64           `json:"eta_seconds,omitempty"`
	DoorOpen         *bool           `json:"door_open,omitempty"`
	ProbeC           float64         `json:"probe_c,omitempty"`
	UpdatedAt        string          `json:"updated_at,omitempty"`
	Plan             json.RawMessage `json:"plan,omitempty"`
}

type CameraStatus struct {
	CameradRunning      bool   `json:"camerad_running"`
	StockSocket         bool   `json:"stock_socket"`
	Source              string `json:"source"`
	LastFrameAgeSeconds int64  `json:"last_frame_age_seconds"`
	ViewerGate          bool   `json:"viewer_gate"`
	HelperError         string `json:"helper_error,omitempty"`
}

type LightStatus struct {
	HelperInstalled bool   `json:"helper_installed"`
	SceneOn         bool   `json:"scene_on"`
	LastAction      string `json:"last_action,omitempty"`
	LastResult      string `json:"last_result,omitempty"`
	LastAgeSeconds  int64  `json:"last_age_seconds,omitempty"`
}

type ClientStatus struct {
	Name            string         `json:"name"`
	Platform        string         `json:"platform,omitempty"`
	Version         string         `json:"version,omitempty"`
	Paired          bool           `json:"paired"`
	Connected       bool           `json:"connected"`
	LastSeenSeconds int64          `json:"last_seen_seconds"`
	LastCommand     *CommandStatus `json:"last_command,omitempty"`
}

type ClientsStatus struct {
	Paired    int            `json:"paired"`
	Connected int            `json:"connected"`
	Items     []ClientStatus `json:"items"`
}

type CommandStatus struct {
	Code       int       `json:"code"`
	Mode       string    `json:"mode,omitempty"`
	Client     string    `json:"-"`
	Status     string    `json:"status"`
	AgeSeconds int64     `json:"age_seconds"`
	At         time.Time `json:"-"`
}

type ComponentStatus struct {
	Name        string `json:"name"`
	Expected    string `json:"expected,omitempty"`
	Actual      string `json:"actual,omitempty"`
	OK          bool   `json:"ok"`
	Persistence string `json:"persistence,omitempty"`
}

type InstallStatus struct {
	Mode             string            `json:"mode,omitempty"`
	Date             string            `json:"date,omitempty"`
	InstallerVersion string            `json:"installer_version,omitempty"`
	BuildID          string            `json:"build_id,omitempty"`
	Persistence      string            `json:"persistence,omitempty"`
	CAmode           string            `json:"ca_mode,omitempty"`
	Hostname         string            `json:"hostname,omitempty"`
	InstallIP        string            `json:"install_ip,omitempty"`
	Components       []ComponentStatus `json:"components"`
}

type NetworkStatus struct {
	IP              string   `json:"ip,omitempty"`
	IPs             []string `json:"ips,omitempty"`
	Hostname        string   `json:"hostname,omitempty"`
	MDNS            string   `json:"mdns,omitempty"`
	TLS             bool     `json:"tls"`
	HTTP            bool     `json:"http"`
	TLSPort         int      `json:"tls_port,omitempty"`
	HTTPPort        int      `json:"http_port,omitempty"`
	HostsOverrides  bool     `json:"hosts_overrides"`
	DNS             []string `json:"dns,omitempty"`
	Gateway         string   `json:"gateway,omitempty"`
	HAClientVersion string   `json:"ha_client_version,omitempty"`
}

type CertStatus struct {
	Subject          string   `json:"subject,omitempty"`
	SANs             []string `json:"sans,omitempty"`
	Issuer           string   `json:"issuer,omitempty"`
	NotAfter         string   `json:"not_after,omitempty"`
	DaysRemaining    int      `json:"days_remaining"`
	Fingerprint      string   `json:"fingerprint,omitempty"`
	CAFingerprint    string   `json:"ca_fingerprint,omitempty"`
	CAInSystemStore  bool     `json:"ca_in_system_store"`
	LeafKeyPresent   bool     `json:"leaf_key_present"`
	SANCoversCurrent bool     `json:"san_covers_current_ip"`
	HasIPSAN         bool     `json:"has_ip_san"`
}

type StorageStatus struct {
	DataFreePct      float64 `json:"data_free_pct"`
	CollectorFreePct float64 `json:"collector_free_pct"`
	LogBytes         int64   `json:"log_bytes"`
	JsonlBytes       int64   `json:"jsonl_bytes"`
	MediaBytes       int64   `json:"media_bytes"`
	LatestFrameBytes int64   `json:"latest_frame_bytes"`
}

type PresetsStatus struct {
	CatalogEntries int   `json:"catalog_entries"`
	UpstreamBytes  int64 `json:"upstream_bytes"`
}

type ClockStatus struct {
	UTC      string `json:"utc"`
	SaneYear bool   `json:"sane_year"`
	Synced   bool   `json:"synced"`
}

type ErrorEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Source  string `json:"source"`
	Message string `json:"message"`
}

type Problem struct {
	Severity string `json:"severity"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix"`
}

type StatusData struct {
	Success     bool          `json:"success"`
	OK          bool          `json:"ok"`
	Mode        string        `json:"mode"`
	Server      string        `json:"server"`
	Version     VersionStatus `json:"version"`
	Oven        OvenStatus    `json:"oven"`
	Cook        CookStatus    `json:"cook"`
	Camera      CameraStatus  `json:"camera"`
	Light       LightStatus   `json:"light"`
	Clients     ClientsStatus `json:"clients"`
	Install     InstallStatus `json:"install"`
	Network     NetworkStatus `json:"network"`
	Cert        CertStatus    `json:"cert"`
	Storage     StorageStatus `json:"storage"`
	Presets     PresetsStatus `json:"presets"`
	Clock       ClockStatus   `json:"clock"`
	Errors      []ErrorEntry  `json:"errors"`
	Problems    []Problem     `json:"problems"`
	GeneratedAt string        `json:"generated_at"`
}

func addStatusError(source, msg string) {
	if msg == "" {
		return
	}
	errorsMu.Lock()
	defer errorsMu.Unlock()
	if n := len(statusErrors); n > 0 && statusErrors[n-1].Source == source && statusErrors[n-1].Message == msg {
		return
	}
	statusErrors = append(statusErrors, ErrorEntry{
		Time: time.Now().UTC().Format(time.RFC3339), Level: "error",
		Source: source, Message: msg,
	})
	if len(statusErrors) > statusErrorRing {
		statusErrors = statusErrors[len(statusErrors)-statusErrorRing:]
	}
}

func statusErrorsSnapshot() []ErrorEntry {
	errorsMu.Lock()
	defer errorsMu.Unlock()
	out := make([]ErrorEntry, len(statusErrors))
	copy(out, statusErrors)
	return out
}

func noteClientSeen(deviceID string) {
	if deviceID == "" {
		return
	}
	clientSeenMu.Lock()
	clientSeen[deviceID] = time.Now().UnixNano()
	clientSeenMu.Unlock()
}

func clientSeenAge(deviceID string) int64 {
	clientSeenMu.Lock()
	defer clientSeenMu.Unlock()
	at := clientSeen[deviceID]
	if at == 0 {
		return -1
	}
	return int64(time.Since(time.Unix(0, at)).Seconds())
}

func noteCommand(code int, order int64, device, mode string) {
	lastCommandMu.Lock()
	lastCommand = CommandStatus{Code: code, Mode: mode, Client: device, Status: "pending", At: time.Now()}
	lastCommandMu.Unlock()
}

func noteAck(order int64, status string) {
	lastCommandMu.Lock()
	defer lastCommandMu.Unlock()
	if lastCommand.Status == "pending" {
		lastCommand.Status = status
	}
}

func lastCommandSnapshot() *CommandStatus {
	lastCommandMu.Lock()
	defer lastCommandMu.Unlock()
	if lastCommand.Code == 0 {
		return nil
	}
	c := lastCommand
	c.AgeSeconds = int64(time.Since(c.At).Seconds())
	return &c
}

func setLightStatus(action, result string, on bool) {
	lightMu.Lock()
	lightLast.HelperInstalled = true
	lightLast.SceneOn = on
	lightLast.LastAction = action
	lightLast.LastResult = result
	lightMu.Unlock()
}

func lightStatusSnapshot() LightStatus {
	lightMu.Lock()
	defer lightMu.Unlock()
	return lightLast
}

// startStatusRefresher builds the expensive snapshot once and then keeps it
// fresh on a slow ticker.  Cheap live state is merged in currentStatus.
func startStatusRefresher(hub *Hub, cfg *Config) {
	refreshStatus(hub, cfg)
	go func() {
		t := time.NewTicker(statusRefreshInterval)
		defer t.Stop()
		for range t.C {
			refreshStatus(hub, cfg)
		}
	}()
}

func currentStatus(hub *Hub, cfg *Config) StatusData {
	statusMu.RLock()
	d := statusData
	statusMu.RUnlock()

	d.Success = true
	d.Mode = "local"
	d.Server = "june-local"
	d.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	d.Version = VersionStatus{Version: appVersion, Commit: appCommit, BuildDate: appBuild,
		UptimeSeconds: int64(time.Since(processStart).Seconds())}
	d.Clock = ClockStatus{UTC: time.Now().UTC().Format(time.RFC3339), SaneYear: time.Now().Year() >= 2024, Synced: time.Now().Year() >= 2024}

	st := hub.statusSnapshot()
	d.Oven.Connection = st.ConnectionState
	if last := lastOvenFrameNano.Load(); last > 0 {
		d.Oven.LastFrameAgeSeconds = int64(time.Since(time.Unix(0, last)).Seconds())
	} else {
		d.Oven.LastFrameAgeSeconds = -1
	}
	d.Cook = cookFromSnapshot(st)
	d.Camera.ViewerGate = cameraViewerActive()
	d.Light = lightStatusSnapshot()
	d.Clients = clientsFromHub(hub)
	if d.Errors == nil {
		d.Errors = []ErrorEntry{}
	}
	d.Errors = statusErrorsSnapshot()
	d.Problems = buildProblems(d)
	d.OK = len(d.Problems) == 0
	return d
}

func refreshStatus(hub *Hub, cfg *Config) {
	d := currentStatus(hub, cfg)
	d.Version = VersionStatus{Version: appVersion, Commit: appCommit, BuildDate: appBuild,
		UptimeSeconds: int64(time.Since(processStart).Seconds())}
	d.Oven.Model = readTrimmed("/proc/device-tree/model")
	if d.Oven.Model == "" {
		d.Oven.Model = "meerkat"
	}
	d.Oven.Android = androidVersion()
	d.Oven.SystemSlot, d.Oven.CollectorMounted = systemSlot()
	d.Camera = cameraStatus(cfg)
	d.Light = lightStatusSnapshot()
	if _, err := os.Stat("/system/app/JuneLight/JuneLight.apk"); err == nil {
		d.Light.HelperInstalled = true
	}
	d.Install = installStatus(cfg)
	d.Network = networkStatus(cfg)
	d.Cert = certStatus(cfg, d.Network)
	d.Storage = storageStatus(cfg)
	d.Presets = presetsStatus(cfg)
	d.Clients = clientsFromHub(hub)
	d.Errors = statusErrorsSnapshot()
	d.Problems = buildProblems(d)
	d.OK = len(d.Problems) == 0
	statusMu.Lock()
	statusData = d
	statusMu.Unlock()
}

func cookFromSnapshot(st StatusSnapshot) CookStatus {
	return parseCook(st)
}

func clientsFromHub(hub *Hub) ClientsStatus {
	out := ClientsStatus{}
	hub.mu.Lock()
	info := make(map[string]CompanionInfo, len(hub.companionsInfo))
	for k, v := range hub.companionsInfo {
		info[k] = v
	}
	connected := map[string]bool{}
	for _, p := range hub.companions {
		if p.deviceID != "" {
			connected[p.deviceID] = true
		}
	}
	hub.mu.Unlock()

	cmd := lastCommandSnapshot()
	for id, ci := range info {
		item := ClientStatus{Name: ci.Name, Platform: ci.Platform, Version: ci.Version,
			Paired: true, Connected: connected[id], LastSeenSeconds: clientSeenAge(id)}
		if cmd != nil && cmd.Client == id {
			item.LastCommand = cmd
		}
		out.Items = append(out.Items, item)
		out.Paired++
	}
	for id := range connected {
		if _, ok := info[id]; !ok {
			out.Items = append(out.Items, ClientStatus{Name: "connected client", Paired: false, Connected: true, LastSeenSeconds: clientSeenAge(id)})
			out.Connected++
		}
	}
	for _, item := range out.Items {
		if item.Connected {
			out.Connected++
		}
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Name < out.Items[j].Name })
	return out
}

func cameraStatus(cfg *Config) CameraStatus {
	c := CameraStatus{Source: "stock-socket"}
	c.CameradRunning = processRunning("june-camerad")
	c.StockSocket = fileContains("/proc/net/unix", "cameracapturerd-capture.sock")
	if st, err := os.Stat(filepath.Join(cfg.MediaDir, "prod", "images", "latest.jpg")); err == nil {
		c.LastFrameAgeSeconds = int64(time.Since(st.ModTime()).Seconds())
	} else {
		c.LastFrameAgeSeconds = -1
	}
	if !c.CameradRunning {
		c.HelperError = "june-camerad is not running"
		addStatusError("camera", c.HelperError)
	} else if !c.StockSocket {
		c.HelperError = "cameracapturerd capture socket not found"
		addStatusError("camera", c.HelperError)
	}
	return c
}

func installStatus(cfg *Config) InstallStatus {
	in := InstallStatus{Components: componentStatuses()}
	b, err := os.ReadFile("/data/june/install.json")
	if err != nil {
		in.Mode = "unknown"
		return in
	}
	var doc struct {
		Mode             string            `json:"mode"`
		Date             string            `json:"date"`
		InstallerVersion string            `json:"installer_version"`
		BuildID          string            `json:"build_id"`
		Persistence      string            `json:"persistence"`
		CAmode           string            `json:"ca_mode"`
		Hostname         string            `json:"hostname"`
		InstallIP        string            `json:"install_ip"`
		Components       map[string]string `json:"components"`
	}
	if json.Unmarshal(b, &doc) == nil {
		in.Mode, in.Date, in.InstallerVersion = doc.Mode, doc.Date, doc.InstallerVersion
		in.BuildID, in.Persistence, in.CAmode = doc.BuildID, doc.Persistence, doc.CAmode
		in.Hostname, in.InstallIP = doc.Hostname, doc.InstallIP
		for i := range in.Components {
			if exp := doc.Components[in.Components[i].Name]; exp != "" {
				in.Components[i].Expected = exp
				if in.Components[i].Actual != "" {
					in.Components[i].OK = in.Components[i].OK && in.Components[i].Actual == exp
				}
			}
		}
	}
	return in
}

func networkStatus(cfg *Config) NetworkStatus {
	n := NetworkStatus{TLS: cfg.Port > 0, HTTP: cfg.HTTPPort > 0, TLSPort: cfg.Port, HTTPPort: cfg.HTTPPort}
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			addrs, _ := iface.Addrs()
			for _, a := range addrs {
				ipnet, ok := a.(*net.IPNet)
				if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil {
					continue
				}
				n.IPs = append(n.IPs, ipnet.IP.String())
			}
		}
	}
	sort.Strings(n.IPs)
	if len(n.IPs) > 0 {
		n.IP = n.IPs[0]
	}
	if hn := readTrimmed("/system/etc/june_hostname"); hn != "" {
		n.Hostname = strings.TrimSuffix(hn, ".local")
		n.MDNS = n.Hostname + ".local"
	}
	hosts, _ := os.ReadFile("/system/etc/hosts")
	n.HostsOverrides = strings.Contains(string(hosts), "api.junelife.com") && strings.Contains(string(hosts), "messaging.junelife.com")
	if b, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && f[0] == "nameserver" {
				n.DNS = append(n.DNS, f[1])
			}
		}
	}
	n.Gateway = defaultGateway()
	n.HAClientVersion = lastHAVersion()
	return n
}

func certStatus(cfg *Config, netStatus NetworkStatus) CertStatus {
	c := CertStatus{LeafKeyPresent: false}
	if _, err := os.Stat(cfg.Key); err == nil {
		c.LeafKeyPresent = true
	}
	b, err := os.ReadFile(cfg.Cert)
	if err != nil {
		return c
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return c
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return c
	}
	c.Subject = cert.Subject.String()
	c.Issuer = cert.Issuer.String()
	c.NotAfter = cert.NotAfter.UTC().Format(time.RFC3339)
	c.DaysRemaining = int(time.Until(cert.NotAfter).Hours() / 24)
	sum := sha256.Sum256(cert.Raw)
	c.Fingerprint = hex.EncodeToString(sum[:8])
	for _, dns := range cert.DNSNames {
		c.SANs = append(c.SANs, dns)
	}
	for _, ip := range cert.IPAddresses {
		c.HasIPSAN = true
		c.SANs = append(c.SANs, ip.String())
		if netStatus.IP != "" && ip.String() == netStatus.IP {
			c.SANCoversCurrent = true
		}
	}
	c.CAInSystemStore = caInSystemStore(cert)
	if cfg.CA != "" {
		if cb, err := os.ReadFile(cfg.CA); err == nil {
			if block, _ := pem.Decode(cb); block != nil {
				if ca, err := x509.ParseCertificate(block.Bytes); err == nil {
					s := sha256.Sum256(ca.Raw)
					c.CAFingerprint = hex.EncodeToString(s[:8])
				}
			}
		}
	}
	return c
}

func storageStatus(cfg *Config) StorageStatus {
	s := StorageStatus{}
	s.DataFreePct = freePct("/data")
	s.CollectorFreePct = freePct("/system/priv-app")
	s.LogBytes = fileSize(cfg.LogPath)
	s.JsonlBytes = fileSize(cfg.JsonlPath)
	s.MediaBytes = dirSize(filepath.Join(cfg.MediaDir, "prod", "images"))
	s.LatestFrameBytes = fileSize(filepath.Join(cfg.MediaDir, "prod", "images", "latest.jpg"))
	return s
}

func presetsStatus(cfg *Config) PresetsStatus {
	p := PresetsStatus{}
	_ = filepath.Walk(cfg.ContentRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		p.CatalogEntries++
		p.UpstreamBytes += info.Size()
		return nil
	})
	return p
}

func buildProblems(d StatusData) []Problem {
	var out []Problem
	add := func(sev, id, title, detail, fix string) {
		out = append(out, Problem{Severity: sev, ID: id, Title: title, Detail: detail, Fix: fix})
	}
	if d.Oven.Connection != "connected" {
		add("red", "oven-disconnected", "Oven not connected",
			"The relay has no live connection from the oven.",
			"Check the oven is powered and on the same network; the relay reconnects automatically.")
	}
	if d.Oven.SystemSlot == "sap" || !d.Oven.CollectorMounted {
		add("red", "factory-fallback", "Factory fallback system is running",
			"The oven booted the secondary system and the June app layer is not mounted.",
			"Power-cycle the oven. If it persists: two-finger hold a top corner at boot, choose Exit. If still stuck, reinstall from the USB stick.")
	}
	if d.Camera.CameradRunning && !d.Camera.StockSocket {
		add("amber", "camera-bridge", "Camera bridge is not connected",
			"june-camerad cannot reach the stock camera socket.",
			"Reinstall the 0.5+ image (abstract-socket fix), then check the camerad log.")
	}
	if d.Cert.DaysRemaining < 0 {
		add("red", "cert-expiry", "Certificate has expired",
			fmt.Sprintf("The TLS certificate expired %d days ago.", -d.Cert.DaysRemaining),
			"Re-run the custom install to issue a new CA and leaf certificate.")
	} else if d.Cert.DaysRemaining < 30 {
		add("amber", "cert-expiry", fmt.Sprintf("Certificate expires in %d days", d.Cert.DaysRemaining),
			"The oven's TLS certificate is close to expiry.", "Re-run the custom install to issue a new certificate.")
	}
	if d.Network.IP != "" && d.Cert.HasIPSAN && !d.Cert.SANCoversCurrent {
		add("amber", "cert-ip-mismatch", "Certificate does not cover the current IP",
			fmt.Sprintf("The oven is at %s but that address is not in the certificate SANs.", d.Network.IP),
			"Use the oven's hostname, or re-run the custom install with the current IP.")
	}
	if d.Install.CAmode != "" && d.Install.InstallIP != "" && d.Network.IP != "" && d.Install.InstallIP != d.Network.IP {
		add("amber", "cert-ip-changed", "Oven IP changed since install",
			fmt.Sprintf("Installed at %s, currently %s.", d.Install.InstallIP, d.Network.IP),
			"Use the oven's hostname, or re-run the custom install with the current IP.")
	}
	if d.Install.Mode != "" && d.Install.Mode != "unknown" {
		for _, comp := range d.Install.Components {
			if !comp.OK {
				add("amber", "component-"+strings.ToLower(comp.Name), comp.Name+" is not the expected build",
					"Installed component does not match install.json.", "Reinstall from the 0.5+ USB image.")
			}
		}
	}
	if d.Storage.DataFreePct >= 0 && d.Storage.DataFreePct < 10 {
		add("amber", "storage-low", "Storage is running low",
			fmt.Sprintf("/data has %.0f%% free.", d.Storage.DataFreePct),
			"Free space before taking a backup or reinstalling.")
	}
	if d.Presets.CatalogEntries == 0 {
		add("amber", "presets-empty", "Preset content is missing",
			"Guided programs and recipes load from the local preset mirror.",
			"Reinstall from the 0.5+ USB image, or re-stage the upstream-content mirror.")
	}
	return out
}

// Minimal helpers -----------------------------------------------------------

func processRunning(name string) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cmd, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if strings.Contains(string(cmd), name) {
			return true
		}
	}
	return false
}

func fileContains(path, needle string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), needle)
}

func readTrimmed(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func androidVersion() string {
	b, err := os.ReadFile("/system/build.prop")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "ro.build.version.release=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "ro.build.version.release="))
		}
	}
	return ""
}

func systemSlot() (string, bool) {
	b, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return "unknown", false
	}
	slot := "app"
	collector := false
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if f[1] == "/system" {
			if strings.Contains(f[0], "SAP") {
				slot = "sap"
			}
		}
		if f[1] == "/system/priv-app" {
			collector = true
		}
	}
	return slot, collector
}

func freePct(path string) float64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil || st.Blocks == 0 {
		return -1
	}
	return float64(st.Bavail) / float64(st.Blocks) * 100
}

func fileSize(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}

func dirSize(path string) int64 {
	var total int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func caInSystemStore(leaf *x509.Certificate) bool {
	dir := "/system/etc/security/cacerts"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		block, _ := pem.Decode(b)
		if block == nil {
			continue
		}
		if c, err := x509.ParseCertificate(block.Bytes); err == nil {
			if c.Equal(leaf) || c.Subject.String() == leaf.Issuer.String() {
				return true
			}
		}
	}
	return false
}

func defaultGateway() string {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) >= 3 && f[1] == "00000000" {
			var v uint32
			if _, err := fmt.Sscanf(f[2], "%x", &v); err == nil {
				return fmt.Sprintf("%d.%d.%d.%d", v&0xff, (v>>8)&0xff, (v>>16)&0xff, (v>>24)&0xff)
			}
		}
	}
	return ""
}

var lastHAVersionValue atomic.Value

func noteHAUserAgent(ua string) {
	if strings.Contains(ua, "HomeAssistant") {
		lastHAVersionValue.Store(ua)
	}
}

func lastHAVersion() string {
	if v := lastHAVersionValue.Load(); v != nil {
		return v.(string)
	}
	return ""
}

func componentStatuses() []ComponentStatus {
	componentHashOnce.Do(func() {
		go computeComponentHashes()
	})
	names := []struct{ name, path string }{
		{"june-local", "/system/xbin/june-local"},
		{"june-camerad", "/system/xbin/june-camerad"},
		{"OvenLight", "/system/app/JuneLight/JuneLight.apk"},
		{"Chef", "/system/priv-app/Chef/Chef.apk"},
		{"JuneCloudMessaging", "/system/priv-app/JuneCloudMessaging/JuneCloudMessaging.apk"},
		{"Remotes", "/system/priv-app/Remotes/Remotes.apk"},
		{"MediaCapturer", "/system/priv-app/MediaCapturer/MediaCapturer.apk"},
	}
	var out []ComponentStatus
	for _, n := range names {
		actual := ""
		componentHashMu.RLock()
		actual = componentHashes[n.path]
		componentHashMu.RUnlock()
		_, err := os.Stat(n.path)
		out = append(out, ComponentStatus{Name: n.name, Actual: actual, OK: err == nil})
	}
	return out
}

func computeComponentHashes() {
	for _, path := range []string{
		"/system/xbin/june-local",
		"/system/xbin/june-camerad",
		"/system/app/JuneLight/JuneLight.apk",
		"/system/priv-app/Chef/Chef.apk",
		"/system/priv-app/JuneCloudMessaging/JuneCloudMessaging.apk",
		"/system/priv-app/Remotes/Remotes.apk",
		"/system/priv-app/MediaCapturer/MediaCapturer.apk",
	} {
		if sum, err := sha256File(path); err == nil {
			componentHashMu.Lock()
			componentHashes[path] = sum
			componentHashMu.Unlock()
		}
	}
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
