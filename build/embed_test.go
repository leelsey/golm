// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package build_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/build"
	"github.com/leelsey/golm/sessionstore"
)

// This is the embedder's path, exercised from OUTSIDE the package, exactly as another project would reach it.
func TestEmbeddingFromAnotherPackage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("content-type", "application/json")

		if !strings.Contains(string(b), "\"role\":\"tool\"") {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[
				{"id":"c1","type":"function","function":{"name":"add","arguments":"{\"a\":21,\"b\":21}"}}]},
				"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":9,"completion_tokens":2}}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"42"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":15,"completion_tokens":1}}`)
	}))
	defer srv.Close()
	t.Setenv("EMBED_TEST_KEY", "k")

	skillsDir := t.TempDir()
	d := filepath.Join(skillsDir, "arithmetic")
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"),
		[]byte("---\nname: arithmetic\ndescription: how this host wants sums done\n---\nadd carefully\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &golm.Config{
		DefaultAgent: "assistant",
		Providers: []golm.ProviderConfig{{
			Name: "local", Type: "openai",
			BaseURL: srv.URL + "/v1", APIKeyEnv: "EMBED_TEST_KEY",
		}},
		Agents: []golm.PersonaConfig{{
			Name: "assistant", Provider: "local", Model: "gpt-4o",
			System: "be brief", MaxSteps: 4, MaxTotalTokens: 100000,
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}

	rt, err := build.New(context.Background(), build.Options{
		Config:    cfg,
		SkillDirs: []string{skillsDir},
		CacheDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("build.New: %v", err)
	}
	defer rt.Close()

	rt.Agent.Tools.Register(golm.NewTypedTool("add", "add two integers",
		func(_ context.Context, in struct {
			A int `json:"a"`
			B int `json:"b"`
		}) (string, error) {
			return strconv.Itoa(in.A + in.B), nil
		}))

	store := sessionstore.NewFiles(t.TempDir())
	sess := golm.SessionData{ID: "embedded"}.Session()

	res, err := rt.Agent.Run(context.Background(), sess, "what is 21 + 21?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Text() != "42" {
		t.Errorf("answer = %q, want %q", res.Text(), "42")
	}
	if res.Steps != 2 {
		t.Errorf("steps = %d, want 2 (the call and the answer)", res.Steps)
	}
	if res.Usage.InputTokens != 24 {
		t.Errorf("run usage input = %d, want both steps counted", res.Usage.InputTokens)
	}

	if len(rt.Agent.SystemPrompt.Sections()) == 0 {
		t.Error("the skills index should be in the prompt")
	}
	if _, ok := rt.Agent.Tools.Get("read_skill"); !ok {
		t.Error("the skills tool should be registered")
	}

	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := store.Load(context.Background(), "embedded")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if back.Len() != sess.Len() {
		t.Errorf("reloaded %d messages, want %d", back.Len(), sess.Len())
	}
	if back.Usage() != sess.Usage() {
		t.Errorf("reloaded usage %v, want %v", back.Usage(), sess.Usage())
	}

	var calls, results int
	for _, m := range back.History() {
		for _, c := range m.Content {
			switch c.(type) {
			case golm.ToolUse:
				calls++
			case golm.ToolResult:
				results++
			}
		}
	}
	if calls != 1 || results != 1 {
		t.Errorf("reloaded transcript has %d calls and %d results, want one of each", calls, results)
	}
}
