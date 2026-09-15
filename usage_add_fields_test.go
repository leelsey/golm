// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"reflect"
	"testing"
)

// Reflective, not a maintained list.
func TestUsageAddCoversEveryNumericField(t *testing.T) {
	var a, b Usage
	va, vb := reflect.ValueOf(&a).Elem(), reflect.ValueOf(&b).Elem()
	for i := 0; i < va.NumField(); i++ {
		if va.Field(i).Kind() == reflect.Int {
			va.Field(i).SetInt(int64(i + 1))
			vb.Field(i).SetInt(int64(100 * (i + 1)))
		}
	}

	sum := a
	sum.Add(b)
	got := reflect.ValueOf(sum)
	for i := 0; i < va.NumField(); i++ {
		if va.Field(i).Kind() != reflect.Int {
			continue
		}
		want := va.Field(i).Int() + vb.Field(i).Int()
		if got.Field(i).Int() != want {
			t.Errorf("Add dropped %s: got %d, want %d",
				va.Type().Field(i).Name, got.Field(i).Int(), want)
		}
	}
}
