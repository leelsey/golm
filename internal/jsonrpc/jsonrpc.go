// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package jsonrpc holds JSON-RPC 2.0 wire types and standard error codes, shared by the MCP.
package jsonrpc

import "encoding/json"

// Version is the JSON-RPC protocol version string.
const Version = "2.0"

// Standard JSON-RPC 2.0 error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Request is a JSON-RPC request or notification.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification reports whether the request omits an ID.
func (r *Request) IsNotification() bool { return len(r.ID) == 0 }

// Response is a JSON-RPC response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a JSON-RPC error object.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// Errorf builds a *Error with the given code and message.
func Errorf(code int, msg string) *Error { return &Error{Code: code, Message: msg} }

// ErrorResponse builds a Response carrying an error for the given id.
func ErrorResponse(id json.RawMessage, code int, msg string) *Response {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return &Response{JSONRPC: Version, ID: id, Error: &Error{Code: code, Message: msg}}
}

// ResultResponse builds a success Response for the given id and raw result.
func ResultResponse(id, result json.RawMessage) *Response {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return &Response{JSONRPC: Version, ID: id, Result: result}
}
