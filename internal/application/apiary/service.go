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
)

// Service implements the apiary use cases. Every method takes the
// requesting user's ID (extracted from their verified access token by the
// transport layer) and passes it straight through to the repository,
// which enforces ownership at the query level.
type Service struct {
	apiaries apiary.Repository
}

// NewService constructs a Service.
func NewService(apiaries apiary.Repository) *Service {
	return &Service{apiaries: apiaries}
}

// Create creates a new apiary owned by userID.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, in CreateInput) (*apiary.Apiary, error) {
	a := apiary.New(userID, in.Name, in.Location, in.Notes)
	if err := s.apiaries.Create(ctx, a); err != nil {
		return nil, fmt.Errorf("apiary: create: %w", err)
	}
	return a, nil
}

// Get returns the apiary identified by apiaryID, if it belongs to userID.
func (s *Service) Get(ctx context.Context, userID, apiaryID uuid.UUID) (*apiary.Apiary, error) {
	return s.apiaries.GetByID(ctx, userID, apiaryID)
}

// List returns every apiary belonging to userID.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]*apiary.Apiary, error) {
	return s.apiaries.ListByUser(ctx, userID)
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
	a.Notes = in.Notes
	a.UpdatedAt = time.Now().UTC()

	if err := s.apiaries.Update(ctx, a); err != nil {
		return nil, fmt.Errorf("apiary: update: %w", err)
	}

	return a, nil
}

// Delete deletes the apiary identified by apiaryID, if it belongs to
// userID.
func (s *Service) Delete(ctx context.Context, userID, apiaryID uuid.UUID) error {
	return s.apiaries.Delete(ctx, userID, apiaryID)
}
