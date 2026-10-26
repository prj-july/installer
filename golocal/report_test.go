package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRedactReport(t *testing.T) {
	var v any
	_ = json.Unmarshal([]byte(`{"oven":{"identity":{"token_prefix":"abc","token_source":"seed","account_name":"me"}},
		"cert":{"fingerprint":"aa","leaf_key_present":true},"errors":[{"message":"JM1191G13481 0123456789abcdef0123456789abcdef"}]}`), &v)
	b, _ := json.Marshal(redactReport(v))
	got := string(b)
	for _, leak := range []string{`"abc"`, `"me"`, `"aa"`, "JM1191G13481", "0123456789abcdef0123456789abcdef"} {
		if strings.Contains(got, leak) {
			t.Fatalf("redacted report still contains %s: %s", leak, got)
		}
	}
	for _, keep := range []string{`"token_source":"seed"`, `"leaf_key_present":true`} {
		if !strings.Contains(got, keep) {
			t.Fatalf("redacted report lost %s: %s", keep, got)
		}
	}
}

func TestLocalReportPostsToIntake(t *testing.T) {
	var got map[string]any
	intake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"code":"JR-TEST22"}`))
	}))
	defer intake.Close()
	reportLastAt = time.Time{}
	hub := newHub(false, "")
	cfg := &Config{Timeout: 30, ReportURL: intake.URL}

	post := func(body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/local/report", strings.NewReader(body))
		r.Host = "oven.local"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handleLocalReport(hub, cfg, w, r)
		return w
	}
	if w := post(`{"note":"x"}`, "https://evil.example"); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin: got %d", w.Code)
	}
	if w := post(`{"note":"  "}`, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("empty note: got %d", w.Code)
	}
	w := post(`{"note":"app says disconnected","contact":"a@b.c"}`, "https://oven.local")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "JR-TEST22") {
		t.Fatalf("send: got %d %s", w.Code, w.Body.String())
	}
	if got["note"] != "app says disconnected" || got["source"] != "oven" || got["diagnostics"] == nil {
		t.Fatalf("intake got %v", got)
	}
	if w := post(`{"note":"again"}`, ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second report within a minute: got %d", w.Code)
	}
}
