package main

// Android 5 has no /etc/resolv.conf.  A static Go binary therefore uses its
// compiled-in default nameserver (127.0.0.1:53 / [::1]:53) and every updater
// HTTPS request dies with "connection refused".  The v0.9.1 hardware test
// (docs/incidents/2026-10-26-v0.9.1-wireless-updates-blocked.md) found this.
//
// This file discovers the resolver Android actually uses and installs a
// pure-Go net.Resolver that dials it.  It is a no-op on a normal Linux host.

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const androidGetprop = "/system/bin/getprop"

type dnsDiscovery struct {
	Servers []string
	Source  string // getprop | dhcp | resolv.conf | gateway | none
}

var (
	dnsMu      sync.Mutex
	dnsCached  dnsDiscovery
	dnsCacheAt time.Time
)

const dnsCacheTTL = 30 * time.Second

func androidHost() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := os.Stat(androidGetprop); err == nil {
		return true
	}
	_, err := exec.LookPath("getprop")
	return err == nil
}

func getpropValue(key string) string {
	path := androidGetprop
	if _, err := os.Stat(path); err != nil {
		p, err := exec.LookPath("getprop")
		if err != nil {
			return ""
		}
		path = p
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func validIPv4(s string) (string, bool) {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil || ip.To4() == nil {
		return "", false
	}
	return ip.String(), true
}

func parseNameservers(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "nameserver" {
			if ip, ok := validIPv4(f[1]); ok {
				out = append(out, ip)
			}
		}
	}
	return uniqueStrings(out)
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// discoverDNS returns the resolver servers for the updater.  Android's
// net.dns1..4 properties are authoritative; dhcp.<iface>.dnsN is the fallback,
// then /etc/resolv.conf on a normal host, then the default gateway.
func discoverDNS() dnsDiscovery {
	dnsMu.Lock()
	if !dnsCacheAt.IsZero() && time.Since(dnsCacheAt) < dnsCacheTTL {
		d := dnsCached
		dnsMu.Unlock()
		return d
	}
	dnsMu.Unlock()

	d := discoverDNSUncached()
	dnsMu.Lock()
	dnsCached = d
	dnsCacheAt = time.Now()
	dnsMu.Unlock()
	return d
}

func discoverDNSUncached() dnsDiscovery {
	if androidHost() {
		var v4 []string
		for _, key := range []string{"net.dns1", "net.dns2", "net.dns3", "net.dns4"} {
			if ip, ok := validIPv4(getpropValue(key)); ok {
				v4 = append(v4, ip)
			}
		}
		if len(v4) > 0 {
			return dnsDiscovery{Servers: uniqueStrings(v4), Source: "getprop"}
		}
		for _, iface := range []string{"wlan0", "eth0", "wlan1"} {
			for _, suffix := range []string{"dns1", "dns2", "dns3", "dns4"} {
				if ip, ok := validIPv4(getpropValue("dhcp." + iface + "." + suffix)); ok {
					v4 = append(v4, ip)
				}
			}
		}
		if len(v4) > 0 {
			return dnsDiscovery{Servers: uniqueStrings(v4), Source: "dhcp"}
		}
	}
	if b, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		if servers := parseNameservers(string(b)); len(servers) > 0 {
			return dnsDiscovery{Servers: servers, Source: "resolv.conf"}
		}
	}
	if gw := defaultGateway(); gw != "" {
		if ip, ok := validIPv4(gw); ok {
			return dnsDiscovery{Servers: []string{ip}, Source: "gateway"}
		}
	}
	return dnsDiscovery{Source: "none"}
}

// installDNSResolver makes net.DefaultResolver use the Android-provided DNS
// servers.  Call it before the updater makes any request.
func installDNSResolver() {
	if !androidHost() {
		return
	}
	d := discoverDNS()
	if len(d.Servers) == 0 {
		logLine("[net] no DNS resolver discovered (getprop/dhcp/resolv.conf/gateway); update checks will fail until Wi-Fi has DNS")
		return
	}
	logLine("[net] updater DNS resolver: %s (%s)", strings.Join(d.Servers, ","), d.Source)
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			servers := discoverDNS().Servers
			if len(servers) == 0 {
				return nil, fmt.Errorf("no DNS server discovered")
			}
			var lastErr error
			for _, server := range servers {
				conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "udp", net.JoinHostPort(server, "53"))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
}
