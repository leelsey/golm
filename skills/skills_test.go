// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package skills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, File), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadReadsFrontMatterOnly(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "release", `---
name: release
description: Cut a release, tag it and publish the binaries.
---

# Release

1. Run the gate.
2. Tag.
`)
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if set.Len() != 1 {
		t.Fatalf("loaded %d skills, want 1", set.Len())
	}
	sk := set.List()[0]
	if sk.Name != "release" {
		t.Errorf("name = %q", sk.Name)
	}
	if !strings.HasPrefix(sk.Description, "Cut a release") {
		t.Errorf("description = %q", sk.Description)
	}

	idx := set.Index()
	if !strings.Contains(idx, "release: Cut a release") {
		t.Errorf("index = %q, want the name and description", idx)
	}
	if strings.Contains(idx, "Run the gate") {
		t.Error("the index must not carry bodies; that is what the tool is for")
	}

	body, err := set.Read("release")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(body, "Run the gate") {
		t.Errorf("body = %q, want the instructions", body)
	}
	if strings.Contains(body, "description:") {
		t.Error("the front matter should not be repeated in the body")
	}
}

func TestLoadFallsBackToTheDirectoryName(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "no-front-matter", "# Just a document\n\nDo the thing.\n")
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if set.Len() != 1 || set.List()[0].Name != "no-front-matter" {
		t.Fatalf("skills = %+v, want one named for its directory", set.List())
	}

	if got := set.Index(); !strings.HasSuffix(got, "- no-front-matter") {
		t.Errorf("index = %q", got)
	}
}

func TestEarlierDirectoriesShadowLaterOnes(t *testing.T) {
	project, user := t.TempDir(), t.TempDir()
	write(t, user, "review", "---\nname: review\ndescription: the user's version\n---\nuser body\n")
	write(t, project, "review", "---\nname: review\ndescription: the project's version\n---\nproject body\n")

	set, err := Load(project, user)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if set.Len() != 1 {
		t.Fatalf("loaded %d, want the shadowed pair collapsed to 1", set.Len())
	}
	body, err := set.Read("review")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(body, "project body") {
		t.Errorf("body = %q, want the project's to win", body)
	}
}

func TestLoadSkipsAbsentDirectories(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "only", "---\nname: only\ndescription: d\n---\nbody\n")
	set, err := Load(filepath.Join(dir, "nowhere"), dir, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if set.Len() != 1 {
		t.Errorf("loaded %d, want 1", set.Len())
	}
}

func TestEmptySetCostsNothing(t *testing.T) {
	set, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if set.Index() != "" {
		t.Error("an empty library must add nothing to the prompt")
	}
	var nilSet *Set
	if nilSet.Len() != 0 || nilSet.Index() != "" {
		t.Error("a nil Set should behave as an empty one")
	}
	if _, err := nilSet.Read("x"); err == nil {
		t.Error("reading from a nil Set should fail")
	}
}

func TestToolReadsOnDemand(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "triage", "---\nname: triage\ndescription: d\n---\ntriage instructions\n")
	set, _ := Load(dir)
	tool := set.Tool()

	if tool.Name() != "read_skill" {
		t.Errorf("tool name = %q", tool.Name())
	}
	tr, ok := golm.TraitsOf(tool)
	if !ok {
		t.Fatal("the tool should declare its traits so a policy can wave it through")
	}
	if !tr.ReadOnly || !tr.Filesystem {
		t.Errorf("traits = %+v, want read-only and filesystem", tr)
	}

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"triage"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := (golm.ToolResult{Content: out}).Text(); !strings.Contains(got, "triage instructions") {
		t.Errorf("result = %q", got)
	}

	_, err = tool.Execute(context.Background(), json.RawMessage(`{"name":"absent"}`))
	if err == nil {
		t.Fatal("an unknown skill should be an error")
	}
	if !strings.Contains(err.Error(), "triage") {
		t.Errorf("err = %v, want it to list what there is", err)
	}
}

func TestBodyIsBounded(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "huge", "---\nname: huge\ndescription: d\n---\n"+strings.Repeat("x", maxBodyBytes+5000))
	set, _ := Load(dir)
	body, err := set.Read("huge")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(body) > maxBodyBytes+64 {
		t.Errorf("body = %d bytes, want it capped near %d", len(body), maxBodyBytes)
	}
	if !strings.HasSuffix(body, "[skill truncated]") {
		t.Error("a truncated body must say so; silently short instructions are worse than none")
	}
}

func TestWalkIsBounded(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join("a", "b", "c", "d", "e", "f")
	write(t, dir, deep, "---\nname: buried\ndescription: d\n---\nbody\n")
	write(t, dir, "shallow", "---\nname: shallow\ndescription: d\n---\nbody\n")
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	names := map[string]bool{}
	for _, sk := range set.List() {
		names[sk.Name] = true
	}
	if !names["shallow"] {
		t.Error("a skill at the top level should be found")
	}
	if names["buried"] {
		t.Error("the walk should stop before crawling an arbitrary tree")
	}
}
