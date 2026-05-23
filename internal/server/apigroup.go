package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/perf"
	"github.com/mostlygeek/llama-swap/internal/router"
	"github.com/mostlygeek/llama-swap/internal/shared"
)

// apiModel is one entry in the /api/events modelStatus payload.
type apiModel struct {
	Id          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	State       string   `json:"state"`
	Unlisted    bool     `json:"unlisted"`
	PeerID      string   `json:"peerID"`
	Aliases     []string `json:"aliases,omitempty"`
}

// modelStatus returns every configured model joined with its current process
// state (defaulting to "stopped"), followed by peer models.
func (s *Server) modelStatus() []apiModel {
	running := s.local.RunningModels()

	ids := make([]string, 0, len(s.cfg.Models))
	for id := range s.cfg.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	models := make([]apiModel, 0, len(ids))
	for _, id := range ids {
		mc := s.cfg.Models[id]
		state := "stopped"
		if st, ok := running[id]; ok {
			state = string(st)
		}
		models = append(models, apiModel{
			Id:          id,
			Name:        mc.Name,
			Description: mc.Description,
			State:       state,
			Unlisted:    mc.Unlisted,
			Aliases:     mc.Aliases,
		})
	}

	for peerID, peer := range s.cfg.Peers {
		for _, modelID := range peer.Models {
			models = append(models, apiModel{Id: modelID, PeerID: peerID})
		}
	}

	return models
}

// handleAPIUnloadAll stops every running local process.
func (s *Server) handleAPIUnloadAll(w http.ResponseWriter, r *http.Request) {
	s.local.Unload(0)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"msg": "ok"})
}

// handleAPIUnloadModel stops a single named local process.
func (s *Server) handleAPIUnloadModel(w http.ResponseWriter, r *http.Request) {
	requested := strings.TrimPrefix(r.PathValue("model"), "/")
	realName, found := s.cfg.RealModelName(requested)
	if !found {
		router.SendResponse(w, r, http.StatusNotFound, "model not found")
		return
	}
	if !s.local.Handles(realName) {
		router.SendResponse(w, r, http.StatusNotFound, "no local server found for requested model")
		return
	}
	s.local.Unload(0, realName)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// handleAPIMetrics serves the activity log as a JSON array.
func (s *Server) handleAPIMetrics(w http.ResponseWriter, r *http.Request) {
	data, err := s.metrics.getMetricsJSON()
	if err != nil {
		router.SendResponse(w, r, http.StatusInternalServerError, "failed to get metrics")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// handleAPIPerformance serves the buffered system/GPU stats, optionally
// filtered to samples after the ?after=<RFC3339> timestamp.
func (s *Server) handleAPIPerformance(w http.ResponseWriter, r *http.Request) {
	if s.perf == nil {
		router.SendResponse(w, r, http.StatusServiceUnavailable, "performance monitor not available")
		return
	}

	sysStats, gpuStats := s.perf.Current()

	if afterStr := r.URL.Query().Get("after"); afterStr != "" {
		after, err := time.Parse(time.RFC3339, afterStr)
		if err != nil {
			router.SendResponse(w, r, http.StatusBadRequest, "invalid 'after' timestamp, use RFC3339 format")
			return
		}
		filteredSys := make([]perf.SysStat, 0, len(sysStats))
		for _, st := range sysStats {
			if st.Timestamp.After(after) {
				filteredSys = append(filteredSys, st)
			}
		}
		sysStats = filteredSys

		filteredGpu := make([]perf.GpuStat, 0, len(gpuStats))
		for _, g := range gpuStats {
			if g.Timestamp.After(after) {
				filteredGpu = append(filteredGpu, g)
			}
		}
		gpuStats = filteredGpu
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"sys_stats": sysStats,
		"gpu_stats": gpuStats,
	})
}

// handleAPIVersion serves the build metadata.
func (s *Server) handleAPIVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"version":    s.build.Version,
		"commit":     s.build.Commit,
		"build_date": s.build.Date,
	})
}

// handleAPICapture returns the stored request/response capture for a metric ID.
func (s *Server) handleAPICapture(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		router.SendResponse(w, r, http.StatusBadRequest, "invalid capture ID")
		return
	}

	capture := s.metrics.getCaptureByID(id)
	if capture == nil {
		router.SendResponse(w, r, http.StatusNotFound, "capture not found")
		return
	}

	jsonBytes, err := json.Marshal(capture)
	if err != nil {
		router.SendResponse(w, r, http.StatusInternalServerError, "failed to marshal capture")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonBytes)
}

type messageType string

const (
	msgTypeModelStatus messageType = "modelStatus"
	msgTypeLogData     messageType = "logData"
	msgTypeMetrics     messageType = "metrics"
	msgTypeInFlight    messageType = "inflight"
)

type messageEnvelope struct {
	Type messageType `json:"type"`
	Data string      `json:"data"`
}

// handleAPIEvents streams server events (model status, log data, metrics,
// in-flight counts) to the client as Server-Sent Events.
func (s *Server) handleAPIEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// prevent nginx from buffering SSE
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		router.SendResponse(w, r, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	// internal/event already has a 50K event buffer
	// a 1K message buffer should be enough, watch the logs for the warning that the sendBuffer is full
	sendBuffer := make(chan messageEnvelope, 1024)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	send := func(msg messageEnvelope) {
		select {
		case sendBuffer <- msg:
		case <-ctx.Done():
			s.proxylog.Warn("handleAPIEvents send suppressed due to context done")
		default:
			s.proxylog.Warn("handleAPIEvents sendBuffer full, dropped message")
		}
	}
	sendModels := func() {
		if data, err := json.Marshal(s.modelStatus()); err == nil {
			send(messageEnvelope{Type: msgTypeModelStatus, Data: string(data)})
		}
	}
	sendLogData := func(source string, data []byte) {
		if j, err := json.Marshal(map[string]string{"source": source, "data": string(data)}); err == nil {
			send(messageEnvelope{Type: msgTypeLogData, Data: string(j)})
		}
	}
	sendMetrics := func(metrics []ActivityLogEntry) {
		if j, err := json.Marshal(metrics); err == nil {
			send(messageEnvelope{Type: msgTypeMetrics, Data: string(j)})
		}
	}
	sendInFlight := func(total int) {
		if j, err := json.Marshal(map[string]int{"total": total}); err == nil {
			send(messageEnvelope{Type: msgTypeInFlight, Data: string(j)})
		}
	}

	defer event.On(func(e shared.ProcessStateChangeEvent) { sendModels() })()
	defer event.On(func(e shared.ConfigFileChangedEvent) { sendModels() })()
	defer s.proxylog.OnLogData(func(data []byte) { sendLogData("proxy", data) })()
	defer s.upstreamlog.OnLogData(func(data []byte) { sendLogData("upstream", data) })()
	defer event.On(func(e ActivityLogEvent) { sendMetrics([]ActivityLogEntry{e.Metrics}) })()
	defer event.On(func(e shared.InFlightRequestsEvent) { sendInFlight(e.Total) })()

	// initial payload
	sendLogData("proxy", s.proxylog.GetHistory())
	sendLogData("upstream", s.upstreamlog.GetHistory())
	sendModels()
	sendMetrics(s.metrics.getMetrics())
	sendInFlight(int(s.inflight.Current()))

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.shutdownCtx.Done():
			return
		case msg := <-sendBuffer:
			data, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event:message\ndata:%s\n\n", data)
			flusher.Flush()
		}
	}
}
