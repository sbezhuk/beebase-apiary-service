// Package apiary holds the HTTP handlers for apiary management. Handlers
// stay thin: they decode/validate the request, pull the authenticated
// user's ID from context, call into the application service, and map the
// result (or error) to a response. No business logic or repository
// access happens here.
package apiary

import (
	"context"
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
	CodeApiaryNotFound     = "apiary_not_found"
	CodeInvalidApiaryID    = "invalid_apiary_id"
	CodeImageNotFound      = "image_not_found"
	CodeInvalidSearch      = "invalid_search"
	CodeInvalidSortOrder   = "invalid_sort_order"
	CodeApiaryLimitReached = "apiary_limit_reached"
	CodeApiaryNameExists   = "apiary_name_exists"
	CodeMediaLimitReached  = "media_limit_reached"
	// CodeResourceProLocked identifies a write attempted against a
	// resource that itself currently requires Pro (outside the caller's
	// Free entitlement) - as opposed to CodeApiaryLimitReached, which
	// identifies a creation blocked by the plan's resource-count quota.
	CodeResourceProLocked = "resource_pro_locked"
)

const minSearchLength = 3

// Handler exposes the apiary HTTP endpoints. Every method requires the
// request to have already passed through httpmw.RequireAuth.
type Handler struct {
	service       *appapiary.Service
	log           *slog.Logger
	publicBaseURL string
	reminders     interface {
		Cleanup(context.Context, string, uuid.UUID) error
	}
}

// NewHandler returns a Handler backed by service. publicBaseURL is the
// gateway's externally reachable base URL, used to build each image's
// image_url.
func NewHandler(service *appapiary.Service, log *slog.Logger, publicBaseURL string, reminders ...interface {
	Cleanup(context.Context, string, uuid.UUID) error
}) *Handler {
	h := &Handler{service: service, log: log, publicBaseURL: publicBaseURL}
	if len(reminders) > 0 {
		h.reminders = reminders[0]
	}
	return h
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

	httpx.WriteJSON(w, http.StatusCreated, newResponse(a, h.publicBaseURL))
}

// List handles GET /apiaries.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	p, fields := pagination.ParseParams(r)
	search, fields := parseSearch(r, fields)
	sortOrder, fields := parseSortOrder(r, fields)
	if len(fields) > 0 {
		httpx.WriteValidationError(w, fields)
		return
	}
	withoutHives := parseWithoutHives(r)

	apiaries, total, err := h.service.List(r.Context(), userID, token, p, search, sortOrder, withoutHives)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, pagination.NewResponse(newListResponse(apiaries, h.publicBaseURL), p, total))
}

// parseWithoutHives reads the optional "without_hives" query parameter:
// only the exact value "true" filters; anything else (absent, "false",
// or garbage) leaves results unfiltered - there's no invalid value to
// reject here, unlike search/sortOrder.
func parseWithoutHives(r *http.Request) bool {
	return r.URL.Query().Get("withoutHives") == "true"
}

func parseSearch(r *http.Request, fields map[string]string) (*string, map[string]string) {
	s := r.URL.Query().Get("search")
	if s == "" {
		return nil, fields
	}
	if len(s) < minSearchLength {
		if fields == nil {
			fields = map[string]string{}
		}
		fields["search"] = CodeInvalidSearch
		return nil, fields
	}
	return &s, fields
}

// parseSortOrder reads the optional "sortOrder" query parameter, which
// requests the list be ordered by creation date instead of the endpoint's
// default order. A missing value means "use the default order" (nil); an
// invalid value ("asc"/"desc" are the only accepted ones) is reported as a
// validation error the same way parseSearch reports one.
func parseSortOrder(r *http.Request, fields map[string]string) (*string, map[string]string) {
	s := r.URL.Query().Get("sortOrder")
	if s == "" {
		return nil, fields
	}
	if s != "asc" && s != "desc" {
		if fields == nil {
			fields = map[string]string{}
		}
		fields["sortOrder"] = CodeInvalidSortOrder
		return nil, fields
	}
	return &s, fields
}

// Get handles GET /apiaries/{apiaryId}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	apiaryID, ok := h.pathApiaryID(w, r)
	if !ok {
		return
	}

	a, err := h.service.Get(r.Context(), userID, token, apiaryID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, newResponse(a, h.publicBaseURL))
}

// WritableApiaryID handles GET /apiaries/writable. It's called by
// hive-service (forwarding the caller's own access token, exactly like
// every other cross-service call in this codebase) to resolve parent-
// apiary writability without duplicating apiary-service's Free-apiary
// selection algorithm.
func (h *Handler) WritableApiaryID(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	id, unrestricted, err := h.service.WritableApiaryID(r.Context(), userID, token)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, WritableApiaryResponse{Unrestricted: unrestricted, ApiaryID: id})
}

// Update handles PUT /apiaries/{apiaryId}.
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

	httpx.WriteJSON(w, http.StatusOK, newResponse(a, h.publicBaseURL))
}

// Delete handles DELETE /apiaries/{apiaryId}. It cascades: every hive
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
	if h.reminders != nil {
		if err := h.reminders.Cleanup(r.Context(), "apiary", apiaryID); err != nil {
			h.log.Warn("reminder cleanup failed", "entity_type", "apiary", "entity_id", apiaryID, "error", err)
		}
	}
}

// DeleteAllMine handles DELETE /apiaries. It cascades every apiary the
// caller owns (and, transitively, their hives, inspections, and media).
// Called by auth-service when it deletes an account, forwarding the
// caller's own access token.
func (h *Handler) DeleteAllMine(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	if err := h.service.DeleteAllByUser(r.Context(), userID, token); err != nil {
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
	id, err := uuid.Parse(chi.URLParam(r, "apiaryId"))
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
	case errors.Is(err, appapiary.ErrApiaryLimitReached):
		httpx.WriteError(w, http.StatusForbidden, CodeApiaryLimitReached, "free tier allows a maximum of 1 apiary")
	case errors.Is(err, appapiary.ErrReadOnly):
		httpx.WriteError(w, http.StatusForbidden, CodeResourceProLocked, "this apiary requires Pro to edit")
	case errors.Is(err, appapiary.ErrMediaLimitReached):
		httpx.WriteError(w, http.StatusBadRequest, CodeMediaLimitReached, "maximum 5 photos allowed")
	case errors.Is(err, apiary.ErrNameTaken):
		httpx.WriteError(w, http.StatusConflict, CodeApiaryNameExists, "apiary name already exists")
	default:
		httpx.WriteInternalError(w, h.log, err)
	}
}
