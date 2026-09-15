// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestSubcommandHelpExitsZero(t *testing.T) {
	for _, args := range [][]string{{"bus", "-h"}, {"mcp-server", "-h"}, {"a2a-serve", "--help"}, {"config", "-h"}} {
		if code := run(args, strings.NewReader(""), io.Discard, io.Discard); code != 0 {
			t.Errorf("run(%v) = %d, want 0", args, code)
		}
	}
}

func TestOrchestrateWarnsAttachments(t *testing.T) {
	var stderr bytes.Buffer
	_ = run([]string{"--orchestrate", "--image", "x.png", "hi"}, strings.NewReader(""), io.Discard, &stderr)
	if !strings.Contains(stderr.String(), "ignored in --orchestrate") {
		t.Errorf("expected an attachment warning, got: %s", stderr.String())
	}
}

func TestSplitArgsQuoting(t *testing.T) {
	got := splitArgs(`tool --x "a b" 'c d'`)
	want := []string{"tool", "--x", "a b", "c d"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("splitArgs = %q, want %q", got, want)
	}
}

func TestExtOfJPEG(t *testing.T) {
	if got := extOf("image/jpeg"); got != ".jpg" {
		t.Errorf("extOf(image/jpeg) = %q, want .jpg", got)
	}
}

func TestInvalidModalityRejected(t *testing.T) {
	var errb strings.Builder
	code := run([]string{"--cli", "cat", "--modality", "vidio", "hi"}, strings.NewReader(""), &strings.Builder{}, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "invalid --modality") {
		t.Errorf("stderr = %q, want it to mention invalid --modality", errb.String())
	}
}

func TestTextModalityAccepted(t *testing.T) {
	var errb strings.Builder
	mods := []string{"TEXT", "Image"}
	if !validModalities(mods, &errb) {
		t.Fatalf("TEXT+Image rejected: %s", errb.String())
	}
	if mods[0] != "text" || mods[1] != "image" {
		t.Errorf("mods = %v, want lowercased", mods)
	}
}
