// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import "testing"

func TestNoSelfDelegation(t *testing.T) {
	o := NewOrchestrator(nil)
	o.Add("solo", "sub", &Agent{Provider: &fakeProvider{}, Model: "x"})
	if err := o.WireDelegation(nil); err != nil {
		t.Fatalf("WireDelegation: %v", err)
	}
	main := o.Main()
	if main == nil {
		t.Fatal("no main agent")
	}
	if main.Tools != nil {
		if _, ok := main.Tools.Get("solo"); ok {
			t.Error("main agent must not have a delegation tool for itself")
		}
	}
}
