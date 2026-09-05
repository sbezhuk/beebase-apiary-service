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

// Create creates a new apiary owned by userID. If in.Images is non-empty,
// it's deduplicated (preserving first-seen order) and every id's
// ownership is verified against media-service (see
// MediaClient.VerifyOwnership) before anything is persisted; if
// verification fails, Create returns the error immediately, having
// created nothing - there is no rollback to do, unlike the old
// attach-after-insert flow this replaced. accessToken is the caller's own
// access token, forwarded to media-service.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, accessToken string, in CreateInput) (*apiary.Apiary, error) {
	dedup := dedupeImages(in.Images)

	if len(dedup) > 0 {
		if err := s.media.VerifyOwnership(ctx, accessToken, dedup); err != nil {
			return nil, err
		}
	}

	a := apiary.New(userID, in.Name, in.Location, in.Description)
	a.Lat = in.Lat
	a.Lon = in.Lon
	a.Images = dedup

	if err := s.apiaries.Create(ctx, a); err != nil {
		return nil, fmt.Errorf("apiary: create: %w", err)
	}

	return a, nil
}

// Get returns the apiary identified by apiaryID, if it belongs to
// userID - including the media ids it references (Apiary.Images), read
// straight from the row rather than a media-service round trip.
func (s *Service) Get(ctx context.Context, userID, apiaryID uuid.UUID) (*apiary.Apiary, error) {
	return s.apiaries.GetByID(ctx, userID, apiaryID)
}

// List returns the page of apiaries described by p, out of every apiary
// belonging to userID.
func (s *Service) List(ctx context.Context, userID uuid.UUID, p pagination.Params) ([]*apiary.Apiary, int, error) {
	return s.apiaries.ListByUser(ctx, userID, p)
}

// Update replaces the editable fields of the apiary identified by
// apiaryID, if it belongs to userID, and returns the resulting apiary.
// accessToken is the caller's own access token, forwarded to
// media-service so it can run its own ownership check. When in.Images is
// non-nil, it's deduplicated (preserving first-seen order) and, if
// non-empty, every id's ownership is verified against media-service
// before anything changes; if verification fails, Update returns the
// error immediately, leaving the apiary's row (including its current
// Images) completely untouched. On success, Images is simply replaced
// with the deduplicated set - there is nothing external to reconcile
// against, since apiary-service's own Images column is already the sole
// source of truth for what's referenced. When in.Images is nil, Images is
// left untouched entirely.
func (s *Service) Update(ctx context.Context, userID uuid.UUID, accessToken string, apiaryID uuid.UUID, in UpdateInput) (*apiary.Apiary, error) {
	a, err := s.apiaries.GetByID(ctx, userID, apiaryID)
	if err != nil {
		return nil, err
	}

	if in.Images != nil {
		dedup := dedupeImages(*in.Images)
		if len(dedup) > 0 {
			if err := s.media.VerifyOwnership(ctx, accessToken, dedup); err != nil {
				return nil, err
			}
		}
		a.Images = dedup
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

// dedupeImages returns ids with duplicates removed, preserving the order
// each id first appeared in - so a client submitting the same id twice
// can't cause redundant work or a spurious count mismatch against
// media-service's response.
func dedupeImages(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	dedup := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		dedup = append(dedup, id)
	}
	return dedup
}

// Delete cascades: every hive under apiaryID (and, transitively, their
// inspections and media) is deleted first via hive-service, then every
// media file this apiary itself references (a.Images) is hard-deleted via
// media-service, then the apiary itself is hard-deleted. accessToken is
// the caller's own access token, forwarded to hive-service and
// media-service so each can run its own ownership check. If any step
// fails, Delete stops and returns the error without rolling back steps
// that already succeeded - there is no distributed transaction across
// these services, by design.
func (s *Service) Delete(ctx context.Context, userID uuid.UUID, accessToken string, apiaryID uuid.UUID) error {
	a, err := s.apiaries.GetByID(ctx, userID, apiaryID)
	if err != nil {
		return err
	}
	return s.deleteCascade(ctx, userID, accessToken, a)
}

// DeleteAllByUser cascades every apiary userID owns, in-process (no
// self-HTTP-call): for each apiary it runs the identical cascade Delete
// uses. It stops at the first apiary that fails, leaving apiaries already
// fully deleted earlier in the loop deleted - the same no-rollback
// contract as Delete, just applied across a batch. Used by auth-service
// when it deletes an account, forwarding the caller's own access token.
func (s *Service) DeleteAllByUser(ctx context.Context, userID uuid.UUID, accessToken string) error {
	apiaries, err := s.apiaries.ListAllByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("apiary: list all by user: %w", err)
	}

	for _, a := range apiaries {
		if err := s.deleteCascade(ctx, userID, accessToken, a); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) deleteCascade(ctx context.Context, userID uuid.UUID, accessToken string, a *apiary.Apiary) error {
	if err := s.hives.DeleteByApiary(ctx, accessToken, a.ID); err != nil {
		return err
	}
	if len(a.Images) > 0 {
		if err := s.media.DeleteByIDs(ctx, accessToken, a.Images); err != nil {
			return err
		}
	}
	return s.apiaries.HardDelete(ctx, userID, a.ID)
}
