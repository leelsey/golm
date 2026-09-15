// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"encoding/json"
	"testing"
)

// Part has hand-written JSON marshalling because the A2A wire shape is a union.
func FuzzPartRoundTrip(f *testing.F) {
	f.Add(`{"kind":"text","text":"hi"}`)
	f.Add(`{"kind":"file","file":{"name":"a","mimeType":"text/plain","bytes":"aGk="}}`)
	f.Add(`{"kind":"data","data":{"a":1}}`)
	f.Add(`{"kind":"text"}`)
	f.Add(`{}`)
	f.Add(`{"kind":"nonsense","text":"x","data":{"a":1}}`)
	f.Add(`{"kind":"file","file":null}`)
	f.Add(`{"kind":"data","data":null}`)
	f.Add(`null`)
	f.Add(`[]`)

	f.Fuzz(func(t *testing.T, in string) {
		var p Part
		if err := json.Unmarshal([]byte(in), &p); err != nil {
			return
		}
		first, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("a Part that decoded will not encode: %v (from %q)", err, in)
		}
		var again Part
		if err := json.Unmarshal(first, &again); err != nil {
			t.Fatalf("a Part this package produced will not decode: %v (%q)", err, first)
		}
		second, err := json.Marshal(again)
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		if string(first) != string(second) {
			t.Fatalf("Part is not stable across a round trip:\n  in:     %q\n  once:   %s\n  twice:  %s", in, first, second)
		}
	})
}

// A whole Message is what message/send carries.
func FuzzMessageRoundTrip(f *testing.F) {
	f.Add(`{"role":"user","parts":[{"kind":"text","text":"hi"}]}`)
	f.Add(`{"role":"agent","parts":[]}`)
	f.Add(`{"parts":[{"kind":"data","data":{"x":[1,2,3]}}]}`)
	f.Add(`{"role":"user"}`)

	f.Fuzz(func(t *testing.T, in string) {
		var m Message
		if err := json.Unmarshal([]byte(in), &m); err != nil {
			return
		}
		first, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("a Message that decoded will not encode: %v (from %q)", err, in)
		}
		var again Message
		if err := json.Unmarshal(first, &again); err != nil {
			t.Fatalf("a Message this package produced will not decode: %v", err)
		}
		second, _ := json.Marshal(again)
		if string(first) != string(second) {
			t.Fatalf("Message is not stable:\n  once:  %s\n  twice: %s", first, second)
		}
	})
}
