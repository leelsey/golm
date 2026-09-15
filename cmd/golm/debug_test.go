// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDebugFlagEndToEnd(t *testing.T) {
	srv := mockOpenAI(t)
	defer srv.Close()
	t.Setenv("DEBUG_TEST_KEY", "sk-super-secret")

	var out, errOut bytes.Buffer
	code := run([]string{
		"--debug", "--no-stream",
		"--provider", "openai", "--base-url", srv.URL + "/v1",
		"--api-key-env", "DEBUG_TEST_KEY", "--model", "m",
		"ping",
	}, strings.NewReader(""), &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%q", code, errOut.String())
	}

	s := errOut.String()
	if strings.Contains(s, "sk-super-secret") {
		t.Fatalf("API key leaked into debug output:\n%s", s)
	}
	for _, want := range []string{
		">> [1] POST", "/chat/completions",
		"Authorization: [redacted]",
		`"model":"m"`,
		"<< [1] 200 OK", `"content":"pong"`, "body end",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("debug output missing %q:\n%s", want, s)
		}
	}

	errOut.Reset()
	out.Reset()
	if code := run([]string{
		"--no-stream", "--provider", "openai", "--base-url", srv.URL + "/v1",
		"--api-key-env", "DEBUG_TEST_KEY", "--model", "m", "ping",
	}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(errOut.String(), ">> [1]") {
		t.Errorf("wire dump printed without --debug: %q", errOut.String())
	}
}
