// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/internal/jsonrpc"
)

const defaultMaxBodyBytes int64 = 32 << 20

const sseWriteTimeout = 10 * time.Second

// Handler turns an incoming user message into response parts.
type Handler interface {
	HandleMessage(ctx context.Context, msg Message) ([]Part, error)
}

// StreamHandler optionally streams text deltas.
type StreamHandler interface {
	HandleMessageStream(ctx context.Context, msg Message, emit func(text string) error) ([]Part, error)
}

// Server serves one A2A agent over JSON-RPC/HTTP.
type Server struct {
	AuthToken string

	MaxBodyBytes int64

	card    AgentCard
	handler Handler
	store   *TaskStore
}

func (s *Server) authorised(r *http.Request) bool {
	if s.AuthToken == "" {
		return true
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(s.AuthToken)) == 1
}

// NewServer returns a Server advertising card and backed by handler.
func NewServer(card AgentCard, h Handler) *Server {
	return &Server{MaxBodyBytes: defaultMaxBodyBytes, card: card, handler: h, store: NewTaskStore()}
}

// TaskStore returns the server's task store.
func (s *Server) TaskStore() *TaskStore { return s.store }

// HTTPHandler returns an http.Handler serving the Agent Card and JSON-RPC.
func (s *Server) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(wellKnownPath, s.handleCard)
	mux.HandleFunc("/", s.handleRPC)
	return mux
}

func (s *Server) handleCard(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(s.card)
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "unauthorised", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxBodyBytes)
	var req jsonrpc.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeRPC(w, jsonrpc.ErrorResponse(nil, jsonrpc.CodeInvalidRequest, "request body too large"))
			return
		}
		writeRPC(w, jsonrpc.ErrorResponse(nil, jsonrpc.CodeParseError, "parse error"))
		return
	}
	if req.JSONRPC != jsonrpc.Version {
		writeRPC(w, jsonrpc.ErrorResponse(req.ID, jsonrpc.CodeInvalidRequest, `invalid request: jsonrpc must be "2.0"`))
		return
	}
	switch req.Method {
	case "message/send":
		s.handleSend(w, r, &req)
	case "message/stream":
		s.handleStream(w, r, &req)
	case "tasks/get":
		s.handleTaskQuery(w, &req, false)
	case "tasks/cancel":
		s.handleTaskQuery(w, &req, true)
	default:
		writeRPC(w, jsonrpc.ErrorResponse(req.ID, jsonrpc.CodeMethodNotFound, "method not found: "+req.Method))
	}
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request, req *jsonrpc.Request) {
	var p MessageSendParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPC(w, jsonrpc.ErrorResponse(req.ID, jsonrpc.CodeInvalidParams, "invalid params"))
		return
	}
	task, _ := s.store.Send(r.Context(), s.handler, p.Message)
	writeResult(w, req.ID, task)
}

func (s *Server) handleTaskQuery(w http.ResponseWriter, req *jsonrpc.Request, cancel bool) {
	var p TaskQueryParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPC(w, jsonrpc.ErrorResponse(req.ID, jsonrpc.CodeInvalidParams, "invalid params"))
		return
	}
	if cancel {
		task, err := s.store.Cancel(p.ID)
		switch {
		case errors.Is(err, ErrTaskNotFound):
			writeRPC(w, jsonrpc.ErrorResponse(req.ID, codeTaskNotFound, "task not found"))
		case errors.Is(err, ErrTaskNotCancelable):
			writeRPC(w, jsonrpc.ErrorResponse(req.ID, codeTaskNotCancelable, "task cannot be canceled"))
		default:
			writeResult(w, req.ID, task)
		}
		return
	}
	task, ok := s.store.Get(p.ID)
	if !ok {
		writeRPC(w, jsonrpc.ErrorResponse(req.ID, codeTaskNotFound, "task not found"))
		return
	}
	writeResult(w, req.ID, task)
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request, req *jsonrpc.Request) {
	var p MessageSendParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPC(w, jsonrpc.ErrorResponse(req.ID, jsonrpc.CodeInvalidParams, "invalid params"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	s.store.SendStream(r.Context(), s.handler, p.Message, &sseSink{s: s, w: w, flusher: flusher, id: req.ID})
}

type sseSink struct {
	s       *Server
	w       http.ResponseWriter
	flusher http.Flusher
	id      json.RawMessage
	taskID  string
	ctxID   string
}

func (k *sseSink) OnTask(taskID, contextID string) { k.taskID, k.ctxID = taskID, contextID }

func (k *sseSink) Status(st TaskStatus, final bool) error {
	return k.s.sendEvent(k.w, k.flusher, k.id, TaskStatusUpdateEvent{
		TaskID: k.taskID, ContextID: k.ctxID, Status: st, Final: final, Kind: "status-update"})
}

func (k *sseSink) Artifact(a Artifact, appendChunk, lastChunk bool) error {
	return k.s.sendEvent(k.w, k.flusher, k.id, TaskArtifactUpdateEvent{
		TaskID: k.taskID, ContextID: k.ctxID, Artifact: a, Append: appendChunk, LastChunk: lastChunk, Kind: "artifact-update"})
}

func (s *Server) sendEvent(w http.ResponseWriter, flusher http.Flusher, id json.RawMessage, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	b, err := json.Marshal(jsonrpc.ResultResponse(id, raw))
	if err != nil {
		return err
	}

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout))
	if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeRPC(w http.ResponseWriter, resp *jsonrpc.Response) {
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	raw, err := json.Marshal(result)
	if err != nil {
		writeRPC(w, jsonrpc.ErrorResponse(id, jsonrpc.CodeInternalError, err.Error()))
		return
	}
	writeRPC(w, jsonrpc.ResultResponse(id, raw))
}

type agentHandler struct{ agent *golm.Agent }

// AgentHandler adapts a golm.Agent into an A2A Handler.
func AgentHandler(a *golm.Agent) Handler { return &agentHandler{agent: a} }

func toGolmMessage(msg Message) (golm.Message, error) {
	m := golm.Message{Role: golm.RoleUser}
	for _, p := range msg.Parts {
		switch {
		case p.File != nil && p.File.URI != "" && p.File.Bytes == "":
			return golm.Message{}, fmt.Errorf("a2a: URI file parts are not supported (inline the bytes): %q", p.File.URI)
		case p.File != nil && p.File.Bytes != "":
			data, err := base64.StdEncoding.DecodeString(p.File.Bytes)
			if err != nil || len(data) == 0 {
				return golm.Message{}, fmt.Errorf("a2a: undecodable file part %q", p.File.Name)
			}
			switch {
			case strings.HasPrefix(p.File.MimeType, "audio/"):
				m.Content = append(m.Content, golm.Audio{MediaType: p.File.MimeType, Data: data})
			case strings.HasPrefix(p.File.MimeType, "image/"):
				m.Content = append(m.Content, golm.Image{MediaType: p.File.MimeType, Data: data})
			default:
				return golm.Message{}, fmt.Errorf("a2a: unsupported file media type %q for part %q (want image/* or audio/*)", p.File.MimeType, p.File.Name)
			}
		case p.Text != "":
			m.Content = append(m.Content, golm.Text{Text: p.Text})
		}
	}
	if len(m.Content) == 0 {
		m.Content = append(m.Content, golm.Text{Text: msg.Text()})
	}
	return m, nil
}

func (h *agentHandler) HandleMessage(ctx context.Context, msg Message) ([]Part, error) {
	gm, err := toGolmMessage(msg)
	if err != nil {
		return nil, err
	}
	res, err := h.agent.RunMessage(ctx, golm.NewSession(), gm)
	if err != nil {
		return nil, err
	}
	return []Part{TextPart(res.Text())}, nil
}

func (h *agentHandler) HandleMessageStream(ctx context.Context, msg Message, emit func(string) error) ([]Part, error) {
	gm, err := toGolmMessage(msg)
	if err != nil {
		return nil, err
	}
	res, err := h.agent.StreamMessage(ctx, golm.NewSession(), gm, func(ev golm.StreamEvent) error {
		if ev.Type == golm.EventTextDelta {
			return emit(ev.Text)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return []Part{TextPart(res.Text())}, nil
}

// CardForAgent builds a default streaming Agent Card for a single-skill agent served at url.
func CardForAgent(name, description, url string) AgentCard {
	return AgentCard{
		Name: name, Description: description, Version: golm.Version, URL: url,
		Capabilities:       Capabilities{Streaming: true},
		Skills:             []Skill{{ID: name, Name: name, Description: description, InputModes: []string{"text/plain"}, OutputModes: []string{"text/plain"}}},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
	}
}
