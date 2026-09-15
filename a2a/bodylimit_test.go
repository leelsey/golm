// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm/internal/jsonrpc"
)

func TestRPCBodyCap(t *testing.T) {
	srv := NewServer(CardForAgent("e", "e", "http://x/"), echoHandler{})
	srv.MaxBodyBytes = 64
	hs := httptest.NewServer(srv.HTTPHandler())
	defer hs.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"message/send","params":"` + strings.Repeat("a", 200) + `"}`
	resp, err := http.Post(hs.URL+"/", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var rpcResp jsonrpc.Response
	_ = json.NewDecoder(resp.Body).Decode(&rpcResp)
	if rpcResp.Error == nil {
		t.Fatal("oversized body was not rejected")
	}
}
