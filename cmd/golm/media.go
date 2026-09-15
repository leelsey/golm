// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/leelsey/golm"
)

func warnUnsupportedAttachments(stderr io.Writer, name string, caps golm.Capabilities, images, audios stringList) {
	if len(images) > 0 && !caps.Images {
		fmt.Fprintf(stderr, "golm: warning: provider %q does not support image input; %d attachment(s) ignored\n", name, len(images))
	}
	if len(audios) > 0 && !caps.Audio {
		fmt.Fprintf(stderr, "golm: warning: provider %q does not support audio input; %d attachment(s) ignored\n", name, len(audios))
	}
}

func buildUserMessage(prompt string, images, audios stringList) (golm.Message, error) {
	msg := golm.Message{Role: golm.RoleUser}
	if prompt != "" {
		msg.Content = append(msg.Content, golm.Text{Text: prompt})
	}
	for _, p := range images {
		data, err := os.ReadFile(p)
		if err != nil {
			return golm.Message{}, err
		}
		msg.Content = append(msg.Content, golm.Image{MediaType: mediaTypeOf(p, "image/png"), Data: data})
	}
	for _, p := range audios {
		data, err := os.ReadFile(p)
		if err != nil {
			return golm.Message{}, err
		}
		msg.Content = append(msg.Content, golm.Audio{MediaType: mediaTypeOf(p, "audio/wav"), Data: data})
	}
	return msg, nil
}

func mediaTypeOf(path, fallback string) string {
	if mt := mime.TypeByExtension(filepath.Ext(path)); mt != "" {
		if i := strings.IndexByte(mt, ';'); i >= 0 {
			mt = strings.TrimSpace(mt[:i])
		}
		return mt
	}
	return fallback
}

func saveMedia(msg golm.Message, stderr io.Writer) {
	img, aud := 0, 0
	for _, c := range msg.Content {
		switch v := c.(type) {
		case golm.Image:
			img++
			writeMediaFile(stderr, "image", img, v.MediaType, v.Data)
		case golm.Audio:
			aud++
			writeMediaFile(stderr, "audio", aud, v.MediaType, v.Data)
		}
	}
}

func writeMediaFile(stderr io.Writer, kind string, n int, mediaType string, data []byte) {
	if len(data) == 0 {
		return
	}
	ext := extOf(mediaType)
	name := fmt.Sprintf("golm-%s-%d%s", kind, n, ext)
	for i := 1; fileExists(name); i++ {
		name = fmt.Sprintf("golm-%s-%d-%d%s", kind, n, i, ext)
	}
	name = filepath.Base(name)
	if err := os.WriteFile(name, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "golm: save %s: %v\n", name, err)
		return
	}
	fmt.Fprintf(stderr, "golm: saved %s output -> %s\n", kind, name)
}

func extOf(mediaType string) string {
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = strings.TrimSpace(mediaType[:i])
	}
	if mediaType == "image/jpeg" {
		return ".jpg"
	}
	if exts, _ := mime.ExtensionsByType(mediaType); len(exts) > 0 {
		return exts[0]
	}
	if i := strings.IndexByte(mediaType, '/'); i >= 0 {
		if sub := mediaType[i+1:]; isAlnum(sub) {
			return "." + sub
		}
	}
	return ".bin"
}

func validModalities(mods []string, stderr io.Writer) bool {
	for i, m := range mods {
		switch l := strings.ToLower(m); l {
		case "text", "audio", "image":
			mods[i] = l
		default:
			fmt.Fprintf(stderr, "golm: invalid --modality %q (want text, audio or image)\n", m)
			return false
		}
	}
	return true
}

func isAlnum(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
