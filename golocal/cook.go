package main

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// CookView is the compact cook snapshot for Home Assistant and the cook page.
// Field names stay stable so REST sensors can point at /local/cook.
type CookView struct {
	Success          bool    `json:"success"`
	State            string  `json:"state"`
	Mode             string  `json:"mode,omitempty"`
	CurrentC         float64 `json:"current_c,omitempty"`
	TargetC          float64 `json:"target_c,omitempty"`
	RemainingSeconds int64   `json:"remaining_seconds,omitempty"`
	DoorOpen         *bool   `json:"door_open,omitempty"`
	ProbeC           float64 `json:"probe_c,omitempty"`
	OvenConnected    bool    `json:"oven_connected"`
	UpdatedAt        string  `json:"updated_at,omitempty"`
}

func cookView(hub *Hub) CookView {
	st := hub.statusSnapshot()
	c := parseCook(st)
	v := CookView{
		Success:          true,
		State:            c.State,
		Mode:             c.Mode,
		CurrentC:         c.CurrentC,
		TargetC:          c.TargetC,
		RemainingSeconds: c.RemainingSeconds,
		DoorOpen:         c.DoorOpen,
		ProbeC:           c.ProbeC,
		OvenConnected:    st.ConnectionState == "connected",
		UpdatedAt:        c.UpdatedAt,
	}
	if v.State == "" {
		v.State = "idle"
	}
	return v
}

func parseCook(st StatusSnapshot) CookStatus {
	c := CookStatus{State: "idle"}
	if !st.DeviceStateAt.IsZero() {
		c.UpdatedAt = st.DeviceStateAt.UTC().Format(time.RFC3339)
	}

	ds := mapFromRaw(st.DeviceState)
	applyDeviceCook(&c, ds)
	applyDeviceCook(&c, mapFromRaw(st.CookingState))
	applyCookPlan(&c, st.CookPlan)

	if c.State == "" {
		c.State = "idle"
	}
	return c
}

func applyDeviceCook(c *CookStatus, ds map[string]any) {
	if ds == nil {
		return
	}
	if s, ok := ds["state"].(string); ok && s != "" {
		c.State = s
	}
	if s, ok := stringField(ds, "cook_mode", "mode", "primitive_type"); ok {
		c.Mode = s
	}
	if v, ok := numberField(ds, "temperature_cavity", "cavity_temperature", "current_temperature"); ok {
		c.CurrentC = milliToC(v)
	}
	if temp, ok := ds["temperature"].(map[string]any); ok {
		if v, ok := numberField(temp, "current", "cavity", "value"); ok {
			c.CurrentC = milliToC(v)
		}
		if v, ok := numberField(temp, "target"); ok && c.TargetC == 0 {
			c.TargetC = milliToC(v)
		}
	}
	if temps, ok := ds["temperatures"].(map[string]any); ok {
		if v, ok := numberField(temps, "cavity", "oven"); ok {
			c.CurrentC = milliToC(v)
		}
	}
	if v, ok := numberField(ds, "temperature_target_cavity", "target_temperature_cavity", "target_c"); ok && c.TargetC == 0 {
		c.TargetC = milliToC(v)
	}
	if v, ok := numberField(ds, "temperature_probe", "probe_temperature"); ok && v > 0 {
		c.ProbeC = milliToC(v)
	}
	if v, ok := numberField(ds, "cook_time_remaining", "remaining_time", "time_remaining", "remaining_cook_time"); ok {
		c.RemainingSeconds = milliToSeconds(v)
	}
	if timer, ok := ds["timer"].(map[string]any); ok {
		if v, ok := numberField(timer, "remaining", "remaining_ms", "seconds"); ok {
			c.RemainingSeconds = milliToSeconds(v)
		}
	} else if v, ok := numberField(ds, "timer"); ok {
		c.RemainingSeconds = milliToSeconds(v)
	}
	if b, ok := boolField(ds, "door_open"); ok {
		c.DoorOpen = &b
	} else if door, ok := ds["door"].(map[string]any); ok {
		if b, ok := boolField(door, "open"); ok {
			c.DoorOpen = &b
		}
	} else if s, ok := ds["door"].(string); ok {
		open := strings.EqualFold(s, "open")
		c.DoorOpen = &open
	}
}

func applyCookPlan(c *CookStatus, raw json.RawMessage) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	c.Plan = raw
	var plan struct {
		Food struct {
			Name string `json:"name"`
			Plan struct {
				Steps []struct {
					CookMode          string `json:"cook_mode"`
					TemperatureCavity int    `json:"temperature_cavity"`
					ExitCriteria      []struct {
						Type   string `json:"type"`
						Target struct {
							Value any `json:"value"`
						} `json:"target"`
					} `json:"exit_criteria"`
				} `json:"steps"`
			} `json:"plan"`
		} `json:"food"`
	}
	if json.Unmarshal(raw, &plan) != nil {
		return
	}
	if c.Mode == "" && plan.Food.Name != "" {
		c.Mode = plan.Food.Name
	}
	var eta int64
	for _, step := range plan.Food.Plan.Steps {
		if step.CookMode != "" {
			c.Mode = step.CookMode
		}
		if step.TemperatureCavity > 0 {
			c.TargetC = milliToC(float64(step.TemperatureCavity))
		}
		for _, ec := range step.ExitCriteria {
			if ec.Type == "duration" {
				if v, ok := asFloat(ec.Target.Value); ok {
					eta += milliToSeconds(v)
				}
			}
		}
	}
	c.EtaSeconds = eta
	if c.RemainingSeconds == 0 && eta > 0 && c.State != "idle" {
		c.RemainingSeconds = eta
	}
}

func mapFromRaw(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

func stringField(m map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s, true
		}
	}
	return "", false
}

func numberField(m map[string]any, keys ...string) (float64, bool) {
	for _, k := range keys {
		if v, ok := asFloat(m[k]); ok {
			return v, true
		}
	}
	return 0, false
}

func boolField(m map[string]any, keys ...string) (bool, bool) {
	for _, k := range keys {
		switch v := m[k].(type) {
		case bool:
			return v, true
		case string:
			if strings.EqualFold(v, "true") || v == "1" {
				return true, true
			}
			if strings.EqualFold(v, "false") || v == "0" {
				return false, true
			}
		}
	}
	return false, false
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// milliToC treats values above 1000 as millidegrees C (June's wire unit).
func milliToC(v float64) float64 {
	if v > 1000 || v < -1000 {
		return v / 1000
	}
	return v
}

func milliToSeconds(v float64) int64 {
	if v > 10000 || v < -10000 {
		return int64(v / 1000)
	}
	return int64(v)
}

func loadStatusFixture(hub *Hub, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc struct {
		ConnectionState string          `json:"connection_state"`
		DeviceState     json.RawMessage `json:"device_state"`
		CookPlan        json.RawMessage `json:"cook_plan"`
		CookingState    json.RawMessage `json:"cooking_state"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return err
	}
	hub.applyFixture(doc.ConnectionState, doc.DeviceState, doc.CookPlan, doc.CookingState)
	return nil
}
