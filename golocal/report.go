package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// "Send report" on /local: the oven posts the person's note plus its own status to the
// Project July bug report intake (website repo, reports/). Nothing is sent unless someone
// presses the button, and tokens, keys, serials and account names are removed first.

const (
	defaultReportURL  = "https://reports.project-july.org/v1/reports"
	reportMinInterval = time.Minute
	reportTimeout     = 20 * time.Second
	reportMaxRequest  = 8 << 10
)

var (
	reportMu     sync.Mutex
	reportLastAt time.Time
	reportClient = &http.Client{Timeout: reportTimeout}
)

type reportRequest struct {
	Note    string `json:"note"`
	Contact string `json:"contact"`
}

func handleLocalReport(hub *Hub, cfg *Config, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "method not allowed"})
		return
	}
	if !sameRequestOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "error": "cross-origin report refused"})
		return
	}
	var req reportRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, reportMaxRequest)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "send a JSON note"})
		return
	}
	note := strings.TrimSpace(req.Note)
	if note == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "say briefly what went wrong"})
		return
	}

	reportMu.Lock()
	if !reportLastAt.IsZero() && time.Since(reportLastAt) < reportMinInterval {
		reportMu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"success": false, "error": "a report was just sent; wait a minute"})
		return
	}
	reportLastAt = time.Now()
	reportMu.Unlock()

	ensureFreshStatus(hub, cfg)
	diag, err := redactedStatus(currentStatus(hub, cfg))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "could not collect diagnostics"})
		return
	}
	url := cfg.ReportURL
	if url == "" {
		url = defaultReportURL
	}
	ctx, cancel := context.WithTimeout(r.Context(), reportTimeout)
	defer cancel()
	code, err := sendReport(ctx, url, map[string]any{
		"source":      "oven",
		"note":        truncate(note, 2000),
		"contact":     truncate(strings.TrimSpace(req.Contact), 200),
		"client":      "June Local " + appVersion,
		"diagnostics": diag,
	})
	if err != nil {
		logLine("[report] send failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"success": false, "error": err.Error()})
		return
	}
	logLine("[report] sent %s", code)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "code": code})
}

func sendReport(ctx context.Context, url string, body map[string]any) (string, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := reportClient.Do(req)
	if err != nil {
		return "", errors.New("could not reach Project July; is the oven online?")
	}
	defer resp.Body.Close()
	var out struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&out)
	if resp.StatusCode != http.StatusCreated || out.Code == "" {
		if out.Error != "" {
			return "", errors.New(out.Error)
		}
		return "", fmt.Errorf("report service answered %d", resp.StatusCode)
	}
	return out.Code, nil
}

// Same rule as the intake's redact(): drop anything that signs in to or controls the oven or
// identifies its owner, by key name at any depth, and blank serials and long hex in text.
var (
	reportSecretKey = regexp.MustCompile(`(?i)(token|secret|password|passwd|seed|private|signing|_key$|^key$|fingerprint|serial|account_name|email)`)
	reportKeepKey   = regexp.MustCompile(`^(token_source|token_type|leaf_key_present|has_ip_san)$`)
	reportSerial    = regexp.MustCompile(`\bJ[A-Z]\d{4}[A-Z0-9]{5,}\b`)
	reportLongHex   = regexp.MustCompile(`(?i)\b[0-9a-f]{32,}\b`)
)

func redactedStatus(d StatusData) (any, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return redactReport(v), nil
}

func redactReport(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if reportSecretKey.MatchString(k) && !reportKeepKey.MatchString(k) {
				t[k] = "[removed]"
			} else {
				t[k] = redactReport(val)
			}
		}
		return t
	case []any:
		for i := range t {
			t[i] = redactReport(t[i])
		}
		return t
	case string:
		return reportLongHex.ReplaceAllString(reportSerial.ReplaceAllString(t, "[serial]"), "[hex]")
	}
	return v
}
