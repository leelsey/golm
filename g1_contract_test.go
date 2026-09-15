// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import "testing"

func TestRequestForwardsEffortSafetyToolChoice(t *testing.T) {
	a := &Agent{
		Model:      "m",
		Effort:     EffortLow,
		Safety:     SafetyNone,
		ToolChoice: "emit",
	}
	req := a.request(NewSession())
	if req.Effort != EffortLow {
		t.Errorf("Effort not forwarded: %q", req.Effort)
	}
	if req.Safety != SafetyNone {
		t.Errorf("Safety not forwarded: %q", req.Safety)
	}
	if req.ToolChoice != "emit" {
		t.Errorf("ToolChoice not forwarded: %q", req.ToolChoice)
	}
}

func TestPersonaThinkingDisabled(t *testing.T) {
	if got := (PersonaConfig{Thinking: "disabled"}).ThinkingConfig().Mode; got != ThinkingDisabled {
		t.Errorf("thinking=disabled -> %v, want ThinkingDisabled", got)
	}
	if got := (PersonaConfig{Thinking: "off"}).ThinkingConfig().Mode; got != ThinkingOff {
		t.Errorf("thinking=off -> %v, want ThinkingOff", got)
	}
}

func TestValidateRejectsBadEnums(t *testing.T) {
	base := func(mut func(*PersonaConfig)) *Config {
		p := PersonaConfig{Name: "a", Provider: "p", Model: "m"}
		mut(&p)
		return &Config{
			Providers: []ProviderConfig{{Name: "p", Type: "anthropic", APIKeyEnv: "K"}},
			Agents:    []PersonaConfig{p},
		}
	}
	if err := base(func(p *PersonaConfig) { p.Effort = "turbo" }).Validate(); err == nil {
		t.Error("bad effort accepted")
	}
	if err := base(func(p *PersonaConfig) { p.Safety = "max" }).Validate(); err == nil {
		t.Error("bad safety accepted")
	}
	if err := base(func(p *PersonaConfig) { p.Thinking = "sometimes" }).Validate(); err == nil {
		t.Error("bad thinking accepted")
	}
	if err := base(func(p *PersonaConfig) { p.Effort = "high"; p.Safety = "none"; p.Thinking = "disabled" }).Validate(); err != nil {
		t.Errorf("valid enums rejected: %v", err)
	}
}

func TestConfigTemperatureZeroExpressible(t *testing.T) {
	zero := 0.0
	a := &Agent{Temperature: &zero}
	if req := a.request(NewSession()); req.Temperature == nil || *req.Temperature != 0 {
		t.Fatalf("temperature 0 not forwarded as explicit: %v", req.Temperature)
	}
}
