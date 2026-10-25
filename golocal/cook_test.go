package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCookBaking(t *testing.T) {
	st := StatusSnapshot{
		ConnectionState: "connected",
		DeviceState:     json.RawMessage(`{"state":"cooking","temperature_cavity":175500,"cook_time_remaining":540000,"door_open":false}`),
		CookPlan:        json.RawMessage(`{"food":{"name":"Salmon","plan":{"steps":[{"cook_mode":"bake","temperature_cavity":177000,"exit_criteria":[{"type":"duration","target":{"value":900000}}]}]}}}`),
		DeviceStateAt:   time.Date(2026, 10, 12, 2, 0, 0, 0, time.UTC),
	}
	c := parseCook(st)
	if c.State != "cooking" {
		t.Fatalf("state = %q", c.State)
	}
	if c.Mode != "bake" {
		t.Fatalf("mode = %q", c.Mode)
	}
	if c.CurrentC != 175.5 {
		t.Fatalf("current_c = %v", c.CurrentC)
	}
	if c.TargetC != 177 {
		t.Fatalf("target_c = %v", c.TargetC)
	}
	if c.RemainingSeconds != 540 {
		t.Fatalf("remaining = %d", c.RemainingSeconds)
	}
	if c.EtaSeconds != 900 {
		t.Fatalf("eta = %d", c.EtaSeconds)
	}
	if c.DoorOpen == nil || *c.DoorOpen {
		t.Fatalf("door_open = %v", c.DoorOpen)
	}
}

func TestParseCookNestedTemperature(t *testing.T) {
	st := StatusSnapshot{
		DeviceState: json.RawMessage(`{"state":"preheating","temperature":{"current":120000,"target":180000},"timer":{"remaining":60000},"door":"open"}`),
	}
	c := parseCook(st)
	if c.State != "preheating" {
		t.Fatalf("state = %q", c.State)
	}
	if c.CurrentC != 120 || c.TargetC != 180 {
		t.Fatalf("temps current=%v target=%v", c.CurrentC, c.TargetC)
	}
	if c.RemainingSeconds != 60 {
		t.Fatalf("remaining = %d", c.RemainingSeconds)
	}
	if c.DoorOpen == nil || !*c.DoorOpen {
		t.Fatalf("door should be open")
	}
}

func TestParseCookIdle(t *testing.T) {
	c := parseCook(StatusSnapshot{DeviceState: json.RawMessage(`{"state":"idle"}`)})
	if c.State != "idle" || c.CurrentC != 0 || c.RemainingSeconds != 0 {
		t.Fatalf("idle cook = %+v", c)
	}
}

func TestCookViewHTTP(t *testing.T) {
	hub := newHub(true, "")
	if err := loadStatusFixture(hub, filepath.Join("testdata", "cook-baking.json")); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/local/cook", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	handleREST(hub, &Config{}, rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
	var view CookView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if !view.Success || view.State != "active" || view.CurrentC != 175.5 || view.TargetC != 177 || view.RemainingSeconds != 540 {
		t.Fatalf("cook json = %+v", view)
	}
	if !view.OvenConnected {
		t.Fatal("fixture should mark the oven connected")
	}

	htmlReq := httptest.NewRequest(http.MethodGet, "/local/cook", nil)
	htmlReq.Header.Set("Accept", "text/html")
	htmlRec := httptest.NewRecorder()
	handleREST(hub, &Config{}, htmlRec, htmlReq)
	if htmlRec.Code != 200 {
		t.Fatalf("html status = %d", htmlRec.Code)
	}
	body := htmlRec.Body.String()
	if !strings.Contains(body, "June oven cook") || !strings.Contains(body, `"/local/cook.json"`) {
		t.Fatalf("cook page missing markup: %s", body[:200])
	}
	if ct := htmlRec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type = %s", ct)
	}

	jsReq := httptest.NewRequest(http.MethodGet, "/local/cook.json", nil)
	jsReq.Header.Set("Accept", "text/html")
	jsRec := httptest.NewRecorder()
	handleREST(hub, &Config{}, jsRec, jsReq)
	if !strings.Contains(jsRec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("/local/cook.json served %s", jsRec.Header().Get("Content-Type"))
	}
}

func TestLocalStatusIncludesCookTemps(t *testing.T) {
	hub := newHub(true, "")
	if err := loadStatusFixture(hub, filepath.Join("testdata", "cook-baking.json")); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/local/status", nil)
	rec := httptest.NewRecorder()
	handleREST(hub, &Config{}, rec, req)
	if !strings.Contains(rec.Body.String(), `"current_c":175.5`) {
		t.Fatalf("/local/status missing current_c: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"remaining_seconds":540`) {
		t.Fatalf("/local/status missing remaining_seconds: %s", rec.Body.String())
	}
}

func TestCookingStateFrame(t *testing.T) {
	hub := newHub(true, "")
	hub.setDeviceState(json.RawMessage(`{"state":"cooking"}`))
	hub.setCookingState(json.RawMessage(`{"cook_time_remaining":120000,"temperature_cavity":90000}`))
	c := parseCook(hub.statusSnapshot())
	if c.RemainingSeconds != 120 || c.CurrentC != 90 {
		t.Fatalf("cooking state not applied: %+v", c)
	}
}

// The oven's real 10013 frame nests telemetry under sensor_data and
// cook_state_data.
func TestCookingStateFrameNested(t *testing.T) {
	hub := newHub(true, "")
	hub.setDeviceState(json.RawMessage(`{"state":"active"}`))
	hub.setCookPlan(json.RawMessage(`{"food":{"name":"bake","plan":{"steps":[{"cook_mode":"bake_standard","temperature_cavity":176667,"exit_criteria":[{"type":"duration","target":{"value":900000}}]}]}}}`))
	hub.setCookingState(json.RawMessage(`{"sensor_data":{"cavity":150000,"probe":[{"id":"left","value":60000}]},"cook_state_data":{"cook_time_elapsed":891000,"cook_time_remaining":9000}}`))
	c := parseCook(hub.statusSnapshot())
	if c.CurrentC != 150 || c.ProbeC != 60 {
		t.Fatalf("temps current=%v probe=%v", c.CurrentC, c.ProbeC)
	}
	if c.RemainingSeconds != 9 {
		t.Fatalf("remaining = %d, want 9", c.RemainingSeconds)
	}
	if c.EtaSeconds != 900 {
		t.Fatalf("eta = %d", c.EtaSeconds)
	}
}

// The plan's total duration is not a countdown.
func TestCookRemainingNotFromPlan(t *testing.T) {
	c := parseCook(StatusSnapshot{
		DeviceState: json.RawMessage(`{"state":"active"}`),
		CookPlan:    json.RawMessage(`{"food":{"plan":{"steps":[{"exit_criteria":[{"type":"duration","target":{"value":900000}}]}]}}}`),
	})
	if c.RemainingSeconds != 0 || c.EtaSeconds != 900 {
		t.Fatalf("remaining=%d eta=%d", c.RemainingSeconds, c.EtaSeconds)
	}
}

func TestCookingStateClearedOnEnd(t *testing.T) {
	hub := newHub(true, "")
	hub.setCookingState(json.RawMessage(`{"sensor_data":{"cavity":150000}}`))
	hub.setCookingState(nil)
	if c := parseCook(hub.statusSnapshot()); c.CurrentC != 0 {
		t.Fatalf("stale cooking state: %+v", c)
	}
}

func TestFixtureFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "fix.json")
	src, err := os.ReadFile(filepath.Join("testdata", "cook-idle.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, src, 0o644); err != nil {
		t.Fatal(err)
	}
	hub := newHub(true, "")
	if err := loadStatusFixture(hub, p); err != nil {
		t.Fatal(err)
	}
	v := cookView(hub)
	if v.State != "idle" || v.CurrentC != 28 || !v.OvenConnected {
		t.Fatalf("idle fixture = %+v", v)
	}
}

func TestEndedCookIsLastCook(t *testing.T) {
	hub := newHub(true, "")
	hub.setDeviceState(json.RawMessage(`{"state":"active"}`))
	hub.setCookPlan(json.RawMessage(`{"food":{"name":"bake","plan":{"steps":[{"cook_mode":"bake_standard","temperature_cavity":176667,"exit_criteria":[{"type":"duration","target":{"value":900000}}]}]}}}`))
	if c := parseCook(hub.statusSnapshot()); c.State != "active" || c.Mode != "bake_standard" || c.LastCook != nil {
		t.Fatalf("live cook = %+v", c)
	}

	hub.endCook() // 10017 CookPlanEnded; device state still says active
	c := parseCook(hub.statusSnapshot())
	if c.State != "idle" || c.Mode != "" || c.TargetC != 0 || c.EtaSeconds != 0 || c.RemainingSeconds != 0 || c.Plan != nil {
		t.Fatalf("ended cook still looks live: %+v", c)
	}
	if c.LastCook == nil || c.LastCook.Mode != "bake_standard" || c.LastCook.TargetC != 176.667 || c.LastCook.EndedAt == "" {
		t.Fatalf("last cook = %+v", c.LastCook)
	}

	hub.setCookPlan(json.RawMessage(`{"food":{"plan":{"steps":[{"cook_mode":"broil"}]}}}`)) // next cook starts
	if c := parseCook(hub.statusSnapshot()); c.State != "active" || c.Mode != "broil" || c.LastCook != nil {
		t.Fatalf("new cook = %+v", c)
	}
}

func TestIdleWithCachedPlanIsLastCook(t *testing.T) {
	c := parseCook(StatusSnapshot{
		DeviceState: json.RawMessage(`{"state":"idle"}`),
		CookPlan:    json.RawMessage(`{"food":{"plan":{"steps":[{"cook_mode":"bake","temperature_cavity":177000}]}}}`),
	})
	if c.Mode != "" || c.TargetC != 0 || c.LastCook == nil || c.LastCook.Mode != "bake" {
		t.Fatalf("idle cook = %+v", c)
	}
}
