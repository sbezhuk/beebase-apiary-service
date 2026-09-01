// Package apiary implements the apiary use cases: create, get, list,
// update, and delete. It depends only on the domain/apiary port, never on
// HTTP or PostgreSQL directly.
package apiary

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-apiary-service/internal/domain/apiary"
	"github.com/sbezhuk/beebase-common/pagination"
)

// Service implements the apiary use cases. Every method takes the
// requesting user's ID (extracted from their verified access token by the
// transport layer) and passes it straight through to the repository,
// which enforces ownership at the query level.
type Service struct {
	apiaries apiary.Repository
	hives    HiveCascadeDeleter
	media    MediaClient
}

// NewService constructs a Service.
func NewService(apiaries apiary.Repository, hives HiveCascadeDeleter, media MediaClient) *Service {
	return &Service{apiaries: apiaries, hives: hives, media: media}
}

// Create creates a new apiary owned by userID.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, in CreateInput) (*apiary.Apiary, error) {
	a := apiary.New(userID, in.Name, in.Location, in.Description)
	a.Lat = in.Lat
	a.Lon = in.Lon
	if err := s.apiaries.Create(ctx, a); err != nil {
		return nil, fmt.Errorf("apiary: create: %w", err)
	}
	return a, nil
}

// Get returns the apiary identified by apiaryID, if it belongs to
// userID, alongside the IDs of every media item currently attached to
// it. accessToken is the caller's own access token, forwarded to
// media-service so it can run its own ownership check.
func (s *Service) Get(ctx context.Context, userID uuid.UUID, accessToken string, apiaryID uuid.UUID) (*apiary.Apiary, []uuid.UUID, error) {
	a, err := s.apiaries.GetByID(ctx, userID, apiaryID)
	if err != nil {
		return nil, nil, err
	}

	images, err := s.media.ListAttached(ctx, accessToken, apiaryID)
	if err != nil {
		return nil, nil, fmt.Errorf("apiary: list attached media: %w", err)
	}

	return a, images, nil
}

// List returns the page of apiaries described by p, out of every apiary
// belonging to userID.
func (s *Service) List(ctx context.Context, userID uuid.UUID, p pagination.Params) ([]*apiary.Apiary, int, error) {
	return s.apiaries.ListByUser(ctx, userID, p)
}

// Update replaces the editable fields of the apiary identified by
// apiaryID, if it belongs to userID, and returns the resulting set of
// attached media IDs. accessToken is the caller's own access token,
// forwarded to media-service so it can run its own ownership check. When
// in.Images is non-nil, it reconciles the apiary's attached media in
// media-service to match exactly (see reconcileImages); when nil, the
// currently attached media is left untouched and simply reported back.
func (s *Service) Update(ctx context.Context, userID uuid.UUID, accessToken string, apiaryID uuid.UUID, in UpdateInput) (*apiary.Apiary, []uuid.UUID, error) {
	a, err := s.apiaries.GetByID(ctx, userID, apiaryID)
	if err != nil {
		return nil, nil, err
	}

	var images []uuid.UUID
	if in.Images != nil {
		images, err = s.reconcileImages(ctx, accessToken, apiaryID, *in.Images)
	} else {
		images, err = s.media.ListAttached(ctx, accessToken, apiaryID)
	}
	if err != nil {
		return nil, nil, err
	}

	a.Name = in.Name
	a.Location = in.Location
	a.Description = in.Description
	a.Lat = in.Lat
	a.Lon = in.Lon
	a.UpdatedAt = time.Now().UTC()

	if err := s.apiaries.Update(ctx, a); err != nil {
		return nil, nil, fmt.Errorf("apiary: update: %w", err)
	}

	return a, images, nil
}

// reconcileImages makes apiaryID's attached media in media-service match
// desired exactly, and returns the resulting set. Every currently
// attached media ID absent from desired is detached; every ID in desired
// must already be attached to apiaryID - a media item's owner is fixed at
// upload time in media-service and can't be moved between owners, so this
// can only ever prune the attached set, never attach media uploaded
// elsewhere - or Update fails with ErrImageNotFound before any detach
// happens. desired is deduplicated first so a client submitting the same
// ID twice can't cause redundant work or an error.
func (s *Service) reconcileImages(ctx context.Context, accessToken string, apiaryID uuid.UUID, desired []uuid.UUID) ([]uuid.UUID, error) {
	current, err := s.media.ListAttached(ctx, accessToken, apiaryID)
	if err != nil {
		return nil, fmt.Errorf("apiary: list attached media: %w", err)
	}
	currentSet := make(map[uuid.UUID]bool, len(current))
	for _, id := range current {
		currentSet[id] = true
	}

	wanted := make(map[uuid.UUID]bool, len(desired))
	dedup := make([]uuid.UUID, 0, len(desired))
	for _, id := range desired {
		if wanted[id] {
			continue
		}
		wanted[id] = true
		dedup = append(dedup, id)
	}

	for _, id := range dedup {
		if currentSet[id] {
			continue
		}
		if err := s.media.VerifyAttached(ctx, accessToken, apiaryID, id); err != nil {
			return nil, err
		}
	}

	for id := range currentSet {
		if wanted[id] {
			continue
		}
		if err := s.media.Detach(ctx, accessToken, id); err != nil {
			return nil, fmt.Errorf("apiary: detach media %s: %w", id, err)
		}
	}

	return dedup, nil
}

// Delete cascades: every hive under apiaryID (and, transitively, their
// inspections and media) is deleted first via hive-service, then every
// media item attached directly to the apiary, then the apiary itself is
// hard-deleted. accessToken is the caller's own access token, forwarded
// to hive-service and media-service so each can run its own ownership
// check. If any step fails, Delete stops and returns the error without
// rolling back steps that already succeeded - there is no distributed
// transaction across these services, by design.
func (s *Service) Delete(ctx context.Context, userID uuid.UUID, accessToken string, apiaryID uuid.UUID) error {
	if _, err := s.apiaries.GetByID(ctx, userID, apiaryID); err != nil {
		return err
	}
	if err := s.hives.DeleteByApiary(ctx, accessToken, apiaryID); err != nil {
		return err
	}
	if err := s.media.DeleteByOwner(ctx, accessToken, apiaryID); err != nil {
		return err
	}
	return s.apiaries.HardDelete(ctx, userID, apiaryID)
}
