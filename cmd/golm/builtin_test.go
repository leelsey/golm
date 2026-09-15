// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type toolRecorder struct {
	names []string
	calls int
}

func (tr *toolRecorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		json.Unmarshal(b, &req)
		tr.names = nil
		for _, t := range req.Tools {
			tr.names = append(tr.names, t.Function.Name)
		}
		tr.calls++
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	})
}

func runCLI(t *testing.T, srv *httptest.Server, args ...string) (string, int) {
	t.Helper()
	t.Setenv("GOLM_TEST_KEY", "k")
	base := []string{
		"--provider", "openai", "--base-url", srv.URL + "/v1",
		"--api-key-env", "GOLM_TEST_KEY", "--model", "gpt-4o", "--no-stream",
	}
	var out, errOut bytes.Buffer
	code := run(append(base, args...), strings.NewReader(""), &out, &errOut)
	return errOut.String(), code
}

func TestBuiltinFlagsOfferTheTools(t *testing.T) {
	rec := &toolRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	dir := t.TempDir()

	if errOut, code := runCLI(t, srv, "hello"); code != 0 {
		t.Fatalf("run = %d: %s", code, errOut)
	}
	if len(rec.names) != 0 {
		t.Errorf("tools = %v, want none without a flag", rec.names)
	}

	if errOut, code := runCLI(t, srv, "--workspace", dir, "hello"); code != 0 {
		t.Fatalf("run = %d: %s", code, errOut)
	}
	for _, want := range []string{"read_file", "write_file", "edit_file", "list_dir", "search"} {
		if !slices.Contains(rec.names, want) {
			t.Errorf("tools = %v, want %q", rec.names, want)
		}
	}

	if errOut, code := runCLI(t, srv, "--workspace", dir, "--read-only", "hello"); code != 0 {
		t.Fatalf("run = %d: %s", code, errOut)
	}
	for _, unwanted := range []string{"write_file", "edit_file"} {
		if slices.Contains(rec.names, unwanted) {
			t.Errorf("tools = %v, want %q withheld by --read-only", rec.names, unwanted)
		}
	}

	if errOut, code := runCLI(t, srv, "--fetch", "--allow-run", "echo", "hello"); code != 0 {
		t.Fatalf("run = %d: %s", code, errOut)
	}
	for _, want := range []string{"fetch", "run"} {
		if !slices.Contains(rec.names, want) {
			t.Errorf("tools = %v, want %q", rec.names, want)
		}
	}
}

// The agent's file tools reach only the directory the flag named, whatever the model asks for.
func TestWorkspaceFlagConfinesTheAgent(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("not yours"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inside.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("content-type", "application/json")
		if !strings.Contains(string(b), `"role":"tool"`) {
			payload, _ := json.Marshal(map[string]string{"path": asked})
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[
				{"id":"c1","type":"function","function":{"name":"read_file","arguments":`+
				strconvQuote(string(payload))+`}}]},"finish_reason":"tool_calls"}]}`)
			return
		}

		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.Unmarshal(b, &req)
		last := ""
		for _, m := range req.Messages {
			if m.Role == "tool" {
				last = m.Content
			}
		}
		out, _ := json.Marshal(last)
		io.WriteString(w, `{"choices":[{"message":{"content":`+string(out)+`},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	asked = "inside.txt"
	var out, errOut bytes.Buffer
	t.Setenv("GOLM_TEST_KEY", "k")
	args := []string{
		"--provider", "openai", "--base-url", srv.URL + "/v1",
		"--api-key-env", "GOLM_TEST_KEY", "--model", "gpt-4o", "--no-stream",
		"--workspace", dir, "read it",
	}
	if code := run(args, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("run = %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "mine") {
		t.Errorf("output = %q, want the file's contents", out.String())
	}

	asked = outside
	out.Reset()
	errOut.Reset()
	if code := run(args, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("run = %d: %s", code, errOut.String())
	}
	if strings.Contains(out.String(), "not yours") {
		t.Fatalf("the agent read a file outside the workspace: %q", out.String())
	}
	if !strings.Contains(out.String(), "outside the workspace") {
		t.Errorf("output = %q, want the refusal reported to the model", out.String())
	}

	if !strings.Contains(errOut.String(), "tool call(s) failed") {
		t.Errorf("stderr = %q, want the failed tool counted", errOut.String())
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// A setting refused in a config file must not be silently ignored when it comes from a flag instead.
func TestReadOnlyWithoutAWorkspaceIsRefused(t *testing.T) {
	srv := httptest.NewServer((&toolRecorder{}).handler())
	defer srv.Close()
	errOut, code := runCLI(t, srv, "--read-only", "hello")
	if code == 0 {
		t.Fatal("--read-only with no workspace should fail rather than do nothing")
	}
	if !strings.Contains(errOut, "workspace") {
		t.Errorf("stderr = %q, want it to name what is missing", errOut)
	}
}
