// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package netbus bridges golm's in-process event Bus across processes.
package netbus

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultRingSize = 256

const (
	defaultMaxPublishBytes int64 = 16 << 20
	defaultWriteTimeout          = 10 * time.Second
)

// Event is the wire form of a bus event.
type Event struct {
	ID     int64           `json:"id,omitempty"`
	Topic  string          `json:"topic"`
	Origin string          `json:"origin,omitempty"`
	Agent  string          `json:"agent,omitempty"`
	Kind   string          `json:"kind,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`

	wire []byte
}

type subscriber struct {
	topics map[string]bool
	ch     chan Event
}

// Hub is a central publish/subscribe broker.
type Hub struct {
	AuthToken string

	MaxPublishBytes int64
	WriteTimeout    time.Duration

	mu        sync.RWMutex
	subs      map[int]*subscriber
	nextSub   int
	shutdown  bool
	pub       sync.Mutex
	ring      *ring
	heartbeat time.Duration
}

// NewHub returns a ready Hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[int]*subscriber), ring: newRing(defaultRingSize), heartbeat: 30 * time.Second,
		MaxPublishBytes: defaultMaxPublishBytes, WriteTimeout: defaultWriteTimeout}
}

// Handler returns the hub's HTTP handler.
func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/publish", h.handlePublish)
	mux.HandleFunc("/subscribe", h.handleSubscribe)
	return mux
}

// CloseAll closes every active subscriber channel, making their SSE handlers return promptly.
func (h *Hub) CloseAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.shutdown = true
	for id, s := range h.subs {
		close(s.ch)
		delete(h.subs, id)
	}
}

// SubscriberCount reports the number of active subscribers.
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

func (h *Hub) authorised(r *http.Request) bool {
	if h.AuthToken == "" {
		return true
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(h.AuthToken)) == 1
}

func (h *Hub) handlePublish(w http.ResponseWriter, r *http.Request) {
	if !h.authorised(r) {
		http.Error(w, "unauthorised", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.MaxPublishBytes)
	var ev Event
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, "bad event", http.StatusBadRequest)
		return
	}

	if ev.Origin == "" {
		ev.Origin = "external"
	}

	h.pub.Lock()
	ev = h.ring.add(ev)
	h.broadcast(ev)
	h.pub.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

func (h *Hub) broadcast(ev Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.subs {
		if len(s.topics) == 0 || s.topics[ev.Topic] {
			select {
			case s.ch <- ev:
			default:
			}
		}
	}
}

func (h *Hub) addSub(topics map[string]bool) (int, chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan Event, 64)
	if h.shutdown {
		close(ch)
		return -1, ch
	}
	id := h.nextSub
	h.nextSub++
	h.subs[id] = &subscriber{topics: topics, ch: ch}
	return id, ch
}

func (h *Hub) removeSub(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.subs[id]; ok {
		close(s.ch)
		delete(h.subs, id)
	}
}

func (h *Hub) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if !h.authorised(r) {
		http.Error(w, "unauthorised", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	topics := map[string]bool{}
	if t := r.URL.Query().Get("topics"); t != "" {
		for _, p := range strings.Split(t, ",") {
			if p != "" {
				topics[p] = true
			}
		}
	}
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("connection", "keep-alive")

	rc := http.NewResponseController(w)
	id, ch := h.addSub(topics)
	defer h.removeSub(id)

	flusher.Flush()

	var replayed map[int64]bool
	if last := lastEventID(r); last > 0 {
		snapshot := h.ring.after(last)
		replayed = make(map[int64]bool, len(snapshot))
		for _, ev := range snapshot {
			replayed[ev.ID] = true
			if len(topics) == 0 || topics[ev.Topic] {
				_ = rc.SetWriteDeadline(time.Now().Add(h.WriteTimeout))
				if writeEvent(w, ev) != nil {
					return
				}
			}
		}
		flusher.Flush()
	}

	ticker := time.NewTicker(h.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if replayed[ev.ID] {
				delete(replayed, ev.ID)
				continue
			}
			_ = rc.SetWriteDeadline(time.Now().Add(h.WriteTimeout))
			if writeEvent(w, ev) != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			_ = rc.SetWriteDeadline(time.Now().Add(h.WriteTimeout))
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

var kindSanitiser = strings.NewReplacer("\r", "", "\n", "")

func writeEvent(w http.ResponseWriter, ev Event) error {
	data := ev.wire
	if data == nil {
		b, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		data = b
	}
	kind := kindSanitiser.Replace(ev.Kind)
	_, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, kind, data)
	return err
}

func lastEventID(r *http.Request) int64 {
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("lastEventID")
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

type ring struct {
	mu  sync.Mutex
	seq int64
	buf []Event
	cap int
}

func newRing(c int) *ring { return &ring{cap: c} }

func (r *ring) add(ev Event) Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	ev.ID = r.seq
	r.buf = append(r.buf, ev)
	if len(r.buf) > r.cap {
		r.buf = r.buf[len(r.buf)-r.cap:]
	}
	if b, err := json.Marshal(ev); err == nil {
		ev.wire = b
	}
	return ev
}

func (r *ring) after(id int64) []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Event
	for _, ev := range r.buf {
		if ev.ID > id {
			out = append(out, ev)
		}
	}
	return out
}
