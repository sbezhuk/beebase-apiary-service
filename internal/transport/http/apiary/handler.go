// Package apiary holds the HTTP handlers for apiary management. Handlers
// stay thin: they decode/validate the request, pull the authenticated
// user's ID from context, call into the application service, and map the
// result (or error) to a response. No business logic or repository
// access happens here.
package apiary

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	appapiary "github.com/sbezhuk/beebase-apiary-service/internal/application/apiary"
	"github.com/sbezhuk/beebase-apiary-service/internal/domain/apiary"
	httpmw "github.com/sbezhuk/beebase-common/authmw"
	"github.com/sbezhuk/beebase-common/httpx"
	"github.com/sbezhuk/beebase-common/pagination"
)

// Error codes for apiary failures, returned as the top-level "error.code".
// Each is a stable key a client can map to a localized message.
const (
	CodeApiaryNotFound  = "apiary_not_found"
	CodeInvalidApiaryID = "invalid_apiary_id"
	CodeImageNotFound   = "image_not_found"
)

// Handler exposes the apiary HTTP endpoints. Every method requires the
// request to have already passed through httpmw.RequireAuth.
type Handler struct {
	service *appapiary.Service
	log     *slog.Logger
}

// NewHandler returns a Handler backed by service.
func NewHandler(service *appapiary.Service, log *slog.Logger) *Handler {
	return &Handler{service: service, log: log}
}

// Create handles POST /apiaries.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	var req CreateRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	images := make([]uuid.UUID, len(req.Images))
	for i, s := range req.Images {
		images[i], _ = uuid.Parse(s) // already validated by req.Validate
	}

	a, err := h.service.Create(r.Context(), userID, token, appapiary.CreateInput{
		Name:        req.Name,
		Location:    req.Location,
		Description: req.Description,
		Lat:         req.Lat,
		Lon:         req.Lon,
		Images:      images,
	})
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, newResponse(a))
}

// List handles GET /apiaries.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.requireUserID(w, r)
	if !ok {
		return
	}

	p, fields := pagination.ParseParams(r)
	if len(fields) > 0 {
		httpx.WriteValidationError(w, fields)
		return
	}

	apiaries, total, err := h.service.List(r.Context(), userID, p)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, pagination.NewResponse(newListResponse(apiaries), p, total))
}

// Get handles GET /apiaries/{apiaryID}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.requireUserID(w, r)
	if !ok {
		return
	}

	apiaryID, ok := h.pathApiaryID(w, r)
	if !ok {
		return
	}

	a, err := h.service.Get(r.Context(), userID, apiaryID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, newResponse(a))
}

// Update handles PUT /apiaries/{apiaryID}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	apiaryID, ok := h.pathApiaryID(w, r)
	if !ok {
		return
	}

	var req UpdateRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	var images *[]uuid.UUID
	if req.Images != nil {
		parsed := make([]uuid.UUID, len(req.Images))
		for i, s := range req.Images {
			parsed[i], _ = uuid.Parse(s) // already validated by req.Validate
		}
		images = &parsed
	}

	a, err := h.service.Update(r.Context(), userID, token, apiaryID, appapiary.UpdateInput{
		Name:        req.Name,
		Location:    req.Location,
		Description: req.Description,
		Lat:         req.Lat,
		Lon:         req.Lon,
		Images:      images,
	})
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, newResponse(a))
}

// Delete handles DELETE /apiaries/{apiaryID}. It cascades: every hive
// under the apiary (and, transitively, their inspections and media), and
// every media item attached directly to the apiary, is deleted first,
// then the apiary itself.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	apiaryID, ok := h.pathApiaryID(w, r)
	if !ok {
		return
	}

	if err := h.service.Delete(r.Context(), userID, token, apiaryID); err != nil {
		h.writeServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) requireUserID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	userID, ok := httpmw.UserIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, httpmw.CodeMissingAuthorization, "missing authentication")
		return uuid.Nil, false
	}
	return userID, true
}

// requireAuth returns the authenticated user's ID alongside their raw
// access token (read back off the request's own Authorization header,
// which RequireAuth already validated) so it can be forwarded to
// hive-service/media-service - when cascading a delete, or when Create/
// Update ask media-service to verify ownership of newly-referenced images.
func (h *Handler) requireAuth(w http.ResponseWriter, r *http.Request) (uuid.UUID, string, bool) {
	userID, ok := h.requireUserID(w, r)
	if !ok {
		return uuid.Nil, "", false
	}

	const prefix = "Bearer "
	token := strings.TrimPrefix(r.Header.Get("Authorization"), prefix)

	return userID, token, true
}

func (h *Handler) pathApiaryID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "apiaryID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, CodeInvalidApiaryID, "apiary id must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, apiary.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, CodeApiaryNotFound, "apiary not found")
	case errors.Is(err, appapiary.ErrImageNotFound):
		httpx.WriteValidationError(w, map[string]string{"images": CodeImageNotFound})
	default:
		httpx.WriteInternalError(w, h.log, err)
	}
}
