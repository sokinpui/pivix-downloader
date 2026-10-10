package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/sokinpui/pixiv-downloader/internal/engine"
	"github.com/sokinpui/pixiv-downloader/internal/repository"
	"github.com/sokinpui/pixiv-downloader/internal/service"
)

type Handler struct {
	settingsRepo  *repository.SettingsRepository
	artworkRepo   *repository.ArtworkRepository
	taskRepo      *repository.TaskRepository
	eventHub      *service.EventHub
	syncService   *service.SyncService
	submitService *service.SubmissionService
	engine        *engine.DownloadEngine
	updateClient  func(sessionID, proxyAddr string) error
}

func NewHandler(
	settingsRepo *repository.SettingsRepository,
	artworkRepo *repository.ArtworkRepository,
	taskRepo *repository.TaskRepository,
	eventHub *service.EventHub,
	syncService *service.SyncService,
	submitService *service.SubmissionService,
	engine *engine.DownloadEngine,
	updateClient func(sessionID, proxyAddr string) error,
) *Handler {
	return &Handler{
		settingsRepo:  settingsRepo,
		artworkRepo:   artworkRepo,
		taskRepo:      taskRepo,
		eventHub:      eventHub,
		syncService:   syncService,
		submitService: submitService,
		engine:        engine,
		updateClient:  updateClient,
	}
}

type JSONResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func respondJSON(w http.ResponseWriter, code int, data any, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(JSONResponse{
		Code:    0,
		Message: message,
		Data:    data,
	})
}

func respondError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(JSONResponse{
		Code:    -1,
		Message: message,
	})
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Settings
	mux.HandleFunc("GET /api/settings", h.getSettings)
	mux.HandleFunc("PUT /api/settings", h.updateSettings)

	// Sync & Submit & Tasks
	mux.HandleFunc("POST /api/sync/bookmarks", h.syncBookmarks)
	mux.HandleFunc("POST /api/sync/stop", h.stopSync)
	mux.HandleFunc("POST /api/artworks/submit", h.submitArtwork)
	mux.HandleFunc("POST /api/tasks/{task_id}/retry", h.retryTask)

	// Query
	mux.HandleFunc("GET /api/artworks", h.listArtworks)
	mux.HandleFunc("GET /api/artworks/{id}", h.getArtworkDetail)
	mux.HandleFunc("GET /api/bookmarks", h.listBookmarks)

	// SSE
	mux.HandleFunc("GET /api/events", h.handleSSE)
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.settingsRepo.GetAll()
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Mask session ID for security if present
	if sess, ok := settings["session_id"]; ok && len(sess) > 8 {
		settings["session_id"] = sess[:4] + "****" + sess[len(sess)-4:]
	}

	respondJSON(w, http.StatusOK, settings, "success")
}

func (h *Handler) updateSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	for k, v := range body {
		if k == "session_id" && strings.Contains(v, "****") {
			// Do not overwrite with masked value
			continue
		}
		_ = h.settingsRepo.Set(k, v)
	}

	// Reload client settings if session_id or proxy changed
	sessionID, _ := h.settingsRepo.Get("session_id")
	proxy, _ := h.settingsRepo.Get("proxy")
	if err := h.updateClient(sessionID, proxy); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to update client settings: "+err.Error())
		return
	}

	respondJSON(w, http.StatusOK, nil, "settings updated successfully")
}

func (h *Handler) syncBookmarks(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ForceFull bool   `json:"force_full"`
		UserID    string `json:"user_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	userID := body.UserID
	if userID == "" {
		userID, _ = h.settingsRepo.Get("user_id")
	}
	if userID == "" {
		respondError(w, http.StatusBadRequest, "user_id is not configured")
		return
	}

	go func() {
		_ = h.syncService.SyncBookmarks(context.Background(), service.SyncOptions{
			UserID:    userID,
			ForceFull: body.ForceFull,
		})
	}()

	respondJSON(w, http.StatusOK, nil, "sync triggered in background")
}

func (h *Handler) stopSync(w http.ResponseWriter, r *http.Request) {
	h.syncService.StopSync()
	h.engine.CancelActiveDownloads()
	respondJSON(w, http.StatusOK, nil, "sync and active downloads stopped successfully")
}

func (h *Handler) submitArtwork(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URLOrID string `json:"url_or_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.URLOrID == "" {
		respondError(w, http.StatusBadRequest, "url_or_id is required")
		return
	}

	art, err := h.submitService.SubmitByInput(r.Context(), body.URLOrID)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, art, "artwork submitted successfully")
}

func (h *Handler) retryTask(w http.ResponseWriter, r *http.Request) {
	taskIDStr := r.PathValue("task_id")
	taskID, err := strconv.ParseInt(taskIDStr, 10, 64)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid task id")
		return
	}

	task, err := h.taskRepo.GetByID(taskID)
	if err != nil || task == nil {
		respondError(w, http.StatusNotFound, "task not found")
		return
	}

	_ = h.taskRepo.ResetFailedTask(taskID)
	h.engine.EnqueueTask(taskID)

	respondJSON(w, http.StatusOK, nil, "task re-queued for download")
}

func (h *Handler) listArtworks(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	source := r.URL.Query().Get("source")
	pageStr := r.URL.Query().Get("page")
	limitStr := r.URL.Query().Get("limit")

	page := 1
	limit := 20
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	offset := (page - 1) * limit

	artworks, err := h.artworkRepo.List(status, source, limit, offset)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, artworks, "success")
}

func (h *Handler) getArtworkDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	art, err := h.artworkRepo.GetByID(id)
	if err != nil || art == nil {
		respondError(w, http.StatusNotFound, "artwork not found")
		return
	}

	tasks, err := h.taskRepo.ListByArtworkID(id)
	if err == nil {
		art.Tasks = tasks
	}

	respondJSON(w, http.StatusOK, art, "success")
}

func (h *Handler) listBookmarks(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		userID, _ = h.settingsRepo.Get("user_id")
	}
	if userID == "" {
		respondError(w, http.StatusBadRequest, "user_id is required")
		return
	}

	pageStr := r.URL.Query().Get("page")
	limitStr := r.URL.Query().Get("limit")

	page := 1
	limit := 24
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	offset := (page - 1) * limit

	bookmarks, err := h.syncService.GetRemoteBookmarks(r.Context(), userID, offset, limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, bookmarks, "success")
}

func (h *Handler) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := h.eventHub.Subscribe()
	defer h.eventHub.Unsubscribe(ch)

	// Send initial connection ping
	fmt.Fprintf(w, "event: connected\ndata: {\"message\":\"connected\"}\n\n")
	flusher.Flush()

	notify := r.Context().Done()

	for {
		select {
		case <-notify:
			return
		case ev := <-ch:
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, string(payload))
			flusher.Flush()
		}
	}
}

func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
