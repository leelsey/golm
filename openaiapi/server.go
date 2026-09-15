// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openaiapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/leelsey/golm"
)

// DefaultMaxBody bounds one request.
const DefaultMaxBody int64 = 32 << 20

// Server answers the OpenAI Chat Completions API from one or more agents.
type Server struct {
	AuthToken string

	MaxBody int64

	Store golm.SessionStore

	Logger *slog.Logger

	mu     sync.RWMutex
	models map[string]*served
	order  []string

	turns sessionLocks
}

type served struct {
	runner      golm.Runner
	description string

	orc *golm.Orchestrator
}

// NewServer returns a Server with nothing registered.
func NewServer() *Server { return &Server{models: map[string]*served{}} }

// Add registers r under the model name clients will ask for.
func (s *Server) Add(model string, r golm.Runner, description string) {
	if model == "" || r == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.models == nil {
		s.models = map[string]*served{}
	}
	if _, exists := s.models[model]; !exists {
		s.order = append(s.order, model)
	}
	e := &served{runner: r, description: description}
	if o, ok := r.(*golm.Orchestrator); ok {
		e.orc = o
	}
	s.models[model] = e
}

// Models lists the registered names, in registration order.
func (s *Server) Models() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.order...)
}

func (s *Server) lookup(model string) (*served, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.models[model]
	return e, ok
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, prefix := range []string{"/v1", ""} {
		mux.HandleFunc("POST "+prefix+"/chat/completions", s.handleChat)
		mux.HandleFunc("GET "+prefix+"/models", s.handleModels)
		mux.HandleFunc("GET "+prefix+"/models/{model}", s.handleModel)
	}
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "models": s.Models()})
	})
	return mux
}

func (s *Server) authorised(r *http.Request) bool {
	if s.AuthToken == "" {
		return true
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(s.AuthToken)) == 1
}

func (s *Server) maxBody() int64 {
	if s.MaxBody > 0 {
		return s.MaxBody
	}
	return DefaultMaxBody
}

func (s *Server) log(ctx context.Context, level slog.Level, msg string, attrs ...any) {
	if s.Logger != nil {
		s.Logger.Log(ctx, level, msg, attrs...)
	}
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		unauthorised(w)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := modelList{Object: "list", Data: make([]modelInfo, 0, len(s.order))}
	for _, name := range s.order {
		out.Data = append(out.Data, s.infoLocked(name))
	}
	sort.Slice(out.Data, func(i, j int) bool { return out.Data[i].ID < out.Data[j].ID })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		unauthorised(w)
		return
	}
	name := r.PathValue("model")
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.models[name]; !ok {
		writeJSON(w, http.StatusNotFound,
			errorBody("invalid_request_error", "model_not_found", "no model named %q", name))
		return
	}
	writeJSON(w, http.StatusOK, s.infoLocked(name))
}

func (s *Server) infoLocked(name string) modelInfo {
	return modelInfo{
		ID: name, Object: "model", Created: 0, OwnedBy: "golm",
		Description: s.models[name].description,
	}
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		unauthorised(w)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, s.maxBody()+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest,
			errorBody("invalid_request_error", "read_failed", "could not read the request body: %v", err))
		return
	}
	if int64(len(body)) > s.maxBody() {
		writeJSON(w, http.StatusRequestEntityTooLarge,
			errorBody("invalid_request_error", "body_too_large", "request body is over %d bytes", s.maxBody()))
		return
	}
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest,
			errorBody("invalid_request_error", "invalid_json", "could not parse the request: %v", err))
		return
	}
	if why := req.unsupported(); why != "" {
		writeJSON(w, http.StatusBadRequest,
			errorBody("invalid_request_error", "unsupported_parameter", "%s", why))
		return
	}
	entry, ok := s.lookup(req.Model)
	if !ok {
		writeJSON(w, http.StatusNotFound, errorBody("invalid_request_error", "model_not_found",
			"no model named %q; this server serves: %s", req.Model, strings.Join(s.Models(), ", ")))
		return
	}
	turn, history, err := split(req.Messages)
	if err != nil {
		writeJSON(w, http.StatusBadRequest,
			errorBody("invalid_request_error", "invalid_messages", "%v", err))
		return
	}

	ctx := r.Context()

	release, err := s.turns.acquire(ctx, req.Session)
	if err != nil {
		return
	}
	defer release()

	sess, err := s.session(ctx, req.Session, entry, history)
	if err != nil {
		if errors.Is(err, golm.ErrInvalidSessionID) {
			writeJSON(w, http.StatusBadRequest, errorBody("invalid_request_error", "invalid_session",
				"golm_session %q is not a usable conversation name", req.Session))
			return
		}
		writeJSON(w, http.StatusInternalServerError,
			errorBody("server_error", "session_failed", "%v", err))
		return
	}
	id := "chatcmpl-" + rand.Text()
	s.log(ctx, slog.LevelInfo, "openai: chat completion",
		"id", id, "model", req.Model, "stream", req.Stream,
		"messages", len(req.Messages), "session", req.Session)

	if req.Stream {
		s.streamChat(ctx, w, id, req, entry, sess, turn)
		return
	}
	s.completeChat(ctx, w, id, req, entry, sess, turn)
}

func split(msgs []chatMessage) (golm.Message, []golm.Message, error) {
	last := -1
	for i, m := range msgs {
		if m.Role == "user" {
			last = i
		}
	}
	if last < 0 {
		return golm.Message{}, nil, errors.New("messages must contain at least one user message")
	}
	if last != len(msgs)-1 {
		return golm.Message{}, nil, errors.New("the last message must be the user turn to answer")
	}
	turn := golm.UserText(msgs[last].text())
	if strings.TrimSpace(msgs[last].text()) == "" {
		return golm.Message{}, nil, errors.New("the user turn is empty")
	}
	history := make([]golm.Message, 0, last)
	for _, m := range msgs[:last] {
		text := m.text()
		if strings.TrimSpace(text) == "" {
			continue
		}
		switch m.Role {
		case "system", "developer":

			history = append(history, golm.Message{Role: golm.RoleSystem,
				Content: []golm.Content{golm.Text{Text: text}}})
		case "assistant":
			history = append(history, golm.AssistantText(text))
		case "user":
			history = append(history, golm.UserText(text))
		case "tool", "function":

			history = append(history, golm.UserText("[tool result] "+text))
		default:
			return golm.Message{}, nil, fmt.Errorf("unknown message role %q", m.Role)
		}
	}
	return turn, history, nil
}

func (s *Server) session(ctx context.Context, name string, entry *served, history []golm.Message) (*golm.Session, error) {
	if name == "" || s.Store == nil {
		sess := golm.NewSession()
		sess.Append(history...)
		return sess, nil
	}
	sess, err := s.Store.Load(ctx, name)
	if errors.Is(err, golm.ErrSessionNotFound) {
		sess = golm.SessionData{ID: name, Created: time.Now()}.Session()

		sess.Append(history...)
		return sess, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading session %q: %w", name, err)
	}
	if entry.orc != nil {
		if _, err := entry.orc.LoadConversation(ctx, s.Store, sess); err != nil {
			return nil, err
		}
	}
	return sess, nil
}

func (s *Server) save(ctx context.Context, name string, entry *served, sess *golm.Session) {
	if name == "" || s.Store == nil {
		return
	}

	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var err error
	if entry.orc != nil {
		err = entry.orc.SaveConversation(sctx, s.Store, sess)
	} else {
		err = s.Store.Save(sctx, sess)
	}
	if err != nil {
		s.log(sctx, slog.LevelWarn, "openai: session not saved", "session", name, "error", err)
	}
}

func (s *Server) completeChat(ctx context.Context, w http.ResponseWriter, id string, req chatRequest, entry *served, sess *golm.Session, turn golm.Message) {
	res, err := entry.runner.StreamMessage(ctx, sess, turn, func(golm.StreamEvent) error { return nil })
	s.save(ctx, req.Session, entry, sess)
	if err != nil {
		s.writeRunError(w, err, res)
		return
	}
	reason := finishReason(res.StopReason)
	writeJSON(w, http.StatusOK, chatResponse{
		ID: id, Object: "chat.completion", Created: time.Now().Unix(), Model: req.Model,
		Choices: []chatChoice{{
			Index:        0,
			Message:      &outMessage{Role: "assistant", Content: res.Text()},
			FinishReason: &reason,
		}},
		Usage: usageOf(res.Usage),
	})
}

func (s *Server) writeRunError(w http.ResponseWriter, err error, res golm.Result) {
	switch {
	case errors.Is(err, context.Canceled):

		return
	case errors.Is(err, golm.ErrRefused):
		writeJSON(w, http.StatusBadRequest,
			errorBody("invalid_request_error", "content_filter", "the model declined to answer"))
	case errors.Is(err, golm.ErrContextOverflow):
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_request_error", "context_length_exceeded",
			"the conversation is longer than the model's context window"))
	case errors.Is(err, golm.ErrTokenBudget):
		writeJSON(w, http.StatusTooManyRequests,
			errorBody("insufficient_quota", "token_budget_exhausted", "%v", err))
	case errors.Is(err, golm.ErrMaxSteps):
		writeJSON(w, http.StatusBadGateway, errorBody("server_error", "max_steps",
			"the agent did not reach an answer within its step limit (%d steps)", res.Steps))
	default:
		writeJSON(w, http.StatusBadGateway, errorBody("server_error", "upstream_error", "%v", err))
	}
}

func unauthorised(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="golm"`)
	writeJSON(w, http.StatusUnauthorized,
		errorBody("invalid_request_error", "invalid_api_key", "a bearer token is required"))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
