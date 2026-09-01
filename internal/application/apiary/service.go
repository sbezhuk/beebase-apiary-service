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
	media    MediaDeleter
}

// NewService constructs a Service.
func NewService(apiaries apiary.Repository, hives HiveCascadeDeleter, media MediaDeleter) *Service {
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

// Get returns the apiary identified by apiaryID, if it belongs to userID.
func (s *Service) Get(ctx context.Context, userID, apiaryID uuid.UUID) (*apiary.Apiary, error) {
	return s.apiaries.GetByID(ctx, userID, apiaryID)
}

// List returns the page of apiaries described by p, out of every apiary
// belonging to userID.
func (s *Service) List(ctx context.Context, userID uuid.UUID, p pagination.Params) ([]*apiary.Apiary, int, error) {
	return s.apiaries.ListByUser(ctx, userID, p)
}

// Update replaces the editable fields of the apiary identified by
// apiaryID, if it belongs to userID.
func (s *Service) Update(ctx context.Context, userID, apiaryID uuid.UUID, in UpdateInput) (*apiary.Apiary, error) {
	a, err := s.apiaries.GetByID(ctx, userID, apiaryID)
	if err != nil {
		return nil, err
	}

	a.Name = in.Name
	a.Location = in.Location
	a.Description = in.Description
	a.Lat = in.Lat
	a.Lon = in.Lon
	a.UpdatedAt = time.Now().UTC()

	if err := s.apiaries.Update(ctx, a); err != nil {
		return nil, fmt.Errorf("apiary: update: %w", err)
	}

	return a, nil
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
