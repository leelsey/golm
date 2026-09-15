// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func TestSaveMedia(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	msg := golm.Message{Content: []golm.Content{
		golm.Image{MediaType: "image/png", Data: []byte{1, 2, 3}},
		golm.Audio{MediaType: "audio/wav", Data: []byte{4, 5, 6}},
	}}
	saveMedia(msg, io.Discard)

	entries, _ := os.ReadDir(dir)
	var sawImage, sawAudio bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "golm-image-1") {
			sawImage = true
		}
		if strings.HasPrefix(e.Name(), "golm-audio-1") {
			sawAudio = true
		}
	}
	if !sawImage || !sawAudio {
		t.Errorf("expected saved image and audio files, got %v", entries)
	}
}
