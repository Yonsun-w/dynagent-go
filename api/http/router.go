package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/admin/ai_project/internal/app"
	"github.com/admin/ai_project/internal/platform"
	"github.com/admin/ai_project/internal/resume"
	"github.com/admin/ai_project/internal/state"
)

type Router struct {
	app     *app.App
	resume  *resume.Service
	handler http.Handler
}

type createTaskRequest struct {
	Text     string            `json:"text"`
	Keywords []string          `json:"keywords"`
	Labels   map[string]string `json:"labels"`
}

func New(appInstance *app.App) *Router {
	router := &Router{
		app:    appInstance,
		resume: resume.New(appInstance.Store),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/tasks", router.createTask)
	mux.HandleFunc("GET /v1/tasks/", router.taskRoutes)
	mux.HandleFunc("GET /v1/nodes", router.listNodes)
	mux.Handle(appInstance.Config.Observability.MetricsPath, appInstance.Observe.MetricsHandler())
	router.handler = mux
	return router
}

func (r *Router) Handler() http.Handler {
	return r.withTrace(r.handler)
}

func (r *Router) withTrace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		traceID := req.Header.Get("X-Trace-Id")
		if traceID == "" {
			traceID = platform.NewTraceID()
		}
		ctx := platform.ContextWithTraceID(req.Context(), traceID)
		w.Header().Set("X-Trace-Id", traceID)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

func (r *Router) createTask(w http.ResponseWriter, req *http.Request) {
	var payload createTaskRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	taskID := uuid.NewString()
	st, err := state.New(taskID, platform.TraceIDFromContext(req.Context()), state.UserInput{
		Text:     payload.Text,
		Keywords: payload.Keywords,
		Ext:      map[string]any{},
	}, payload.Labels)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	summary, err := r.app.Engine.Run(req.Context(), st)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "task_id": taskID})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": taskID, "summary": summary})
}

func (r *Router) taskRoutes(w http.ResponseWriter, req *http.Request) {
	path := strings.TrimPrefix(req.URL.Path, "/v1/tasks/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "task id is required"})
		return
	}
	taskID := parts[0]
	switch {
	case len(parts) == 1 && req.Method == http.MethodGet:
		record, err := r.app.Store.GetTask(req.Context(), taskID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, record)
	case len(parts) == 2 && parts[1] == "summary" && req.Method == http.MethodGet:
		record, err := r.app.Store.GetTask(req.Context(), taskID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, record.Summary)
	case len(parts) == 2 && parts[1] == "replay" && req.Method == http.MethodGet:
		record, err := r.app.Store.GetTask(req.Context(), taskID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"task_id": taskID, "steps": record.Steps, "snapshots": record.Snapshots})
	case len(parts) == 2 && parts[1] == "resume" && req.Method == http.MethodPost:
		st, err := r.resume.LatestSnapshot(req.Context(), taskID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		st.Task.Status = state.TaskStatusRunning
		summary, err := r.app.Engine.Run(context.WithoutCancel(req.Context()), st)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"task_id": taskID, "summary": summary})
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "route not found"})
	}
}

func (r *Router) listNodes(w http.ResponseWriter, req *http.Request) {
	_ = req
	writeJSON(w, http.StatusOK, map[string]any{"nodes": r.app.Registry.List()})
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func waitForServerShutdown(ctx context.Context, timeout time.Duration, shutdown func(context.Context) error) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return shutdown(shutdownCtx)
}

func mapError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	return http.StatusInternalServerError
}
