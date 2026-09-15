// Package apiary implements the apiary use cases: create, get, list,
// update, and delete. It depends only on the domain/apiary port, never on
// HTTP or PostgreSQL directly.
package apiary

import (
	"context"
	"errors"
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
	apiaries      apiary.Repository
	hives         HiveClient
	media         MediaClient
	subscriptions EntitlementResolver
}

// NewService constructs a Service.
func NewService(apiaries apiary.Repository, hives HiveClient, media MediaClient, subscriptions EntitlementResolver) *Service {
	return &Service{apiaries: apiaries, hives: hives, media: media, subscriptions: subscriptions}
}

// Create creates a new apiary owned by userID. If in.Images is non-empty,
// it's deduplicated (preserving first-seen order) and every id's
// ownership is verified against media-service (see
// MediaClient.VerifyOwnership) before anything is persisted; if
// verification fails, Create returns the error immediately, having
// created nothing - there is no rollback to do, unlike the old
// attach-after-insert flow this replaced. accessToken is the caller's own
// access token, forwarded to media-service and subscription-service.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, accessToken string, in CreateInput) (*WithAccess, error) {
	entitlement, err := s.subscriptions.GetEntitlement(ctx, accessToken)
	if err != nil {
		return nil, fmt.Errorf("apiary: resolve entitlement: %w", err)
	}

	maxApiaries := 0 // 0 means unlimited
	if entitlement == EntitlementFree {
		count, err := s.apiaries.CountByUser(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("apiary: count apiaries: %w", err)
		}
		if count >= FreeMaxApiaries {
			return nil, ErrApiaryLimitReached
		}
		maxApiaries = FreeMaxApiaries
	}

	dedup := dedupeImages(in.Images)
	if len(dedup) > MaxMediaAttachments {
		return nil, ErrMediaLimitReached
	}

	if len(dedup) > 0 {
		if err := s.media.VerifyOwnership(ctx, accessToken, dedup); err != nil {
			return nil, err
		}
	}

	a := apiary.New(userID, in.Name, in.Location, in.Description)
	a.Lat = in.Lat
	a.Lon = in.Lon
	a.Images = dedup

	if err := s.apiaries.CreateWithLimit(ctx, a, maxApiaries); err != nil {
		if errors.Is(err, apiary.ErrLimitReached) {
			return nil, ErrApiaryLimitReached
		}
		return nil, fmt.Errorf("apiary: create: %w", err)
	}

	// A just-created apiary is always writable: under Pro nothing is ever
	// restricted, and under Free the count check above only lets this
	// succeed when the user owned zero apiaries a moment ago, which makes
	// this one both their oldest and only apiary - definitionally the one
	// FreeMaxApiaries slot.
	return &WithAccess{Apiary: a, Writable: true}, nil
}

// Get returns the apiary identified by apiaryID, if it belongs to
// userID - including the media ids it references (Apiary.Images), read
// straight from the row rather than a media-service round trip - along
// with whether it's currently writable for the caller (see isWritable).
// accessToken is the caller's own access token, forwarded to
// subscription-service to resolve their current entitlement.
func (s *Service) Get(ctx context.Context, userID uuid.UUID, accessToken string, apiaryID uuid.UUID) (*WithAccess, error) {
	a, err := s.apiaries.GetByID(ctx, userID, apiaryID)
	if err != nil {
		return nil, err
	}

	writable, err := s.resolveWritable(ctx, accessToken, userID, apiaryID)
	if err != nil {
		return nil, err
	}

	return &WithAccess{Apiary: a, Writable: writable}, nil
}

// WritableApiaryID resolves, for whoever presented accessToken, the id of
// the one apiary that currently falls within their Free entitlement (nil
// if they own none), or reports unrestricted=true when they currently have
// Pro - meaning every apiary they own is writable and ApiaryID is
// meaningless. Backs GET /api/v1/apiaries/writable, which hive-service
// calls (forwarding the same caller's access token) to resolve parent-
// apiary writability without re-implementing this selection itself.
func (s *Service) WritableApiaryID(ctx context.Context, userID uuid.UUID, accessToken string) (apiaryID *uuid.UUID, unrestricted bool, err error) {
	entitlement, err := s.subscriptions.GetEntitlement(ctx, accessToken)
	if err != nil {
		return nil, false, fmt.Errorf("apiary: resolve entitlement: %w", err)
	}
	if entitlement != EntitlementFree {
		return nil, true, nil
	}

	ids, err := s.apiaries.WritableIDs(ctx, userID, FreeMaxApiaries)
	if err != nil {
		return nil, false, fmt.Errorf("apiary: writable apiary ids: %w", err)
	}
	if len(ids) == 0 {
		return nil, false, nil
	}
	return &ids[0], false, nil
}

// resolveWritable reports whether apiaryID is currently writable for
// userID: always true under Pro, otherwise true only if it's among the
// oldest FreeMaxApiaries live apiaries by (created_at, id) - see
// isWritable. accessToken is forwarded to subscription-service.
func (s *Service) resolveWritable(ctx context.Context, accessToken string, userID, apiaryID uuid.UUID) (bool, error) {
	entitlement, err := s.subscriptions.GetEntitlement(ctx, accessToken)
	if err != nil {
		return false, fmt.Errorf("apiary: resolve entitlement: %w", err)
	}
	if entitlement != EntitlementFree {
		return true, nil
	}
	return s.isWritable(ctx, userID, apiaryID)
}

// isWritable reports whether apiaryID is one of userID's oldest
// FreeMaxApiaries live apiaries by (created_at, id) - the deterministic,
// dynamically-derived Free-tier selection this whole package is built
// around. It's recomputed from current data on every call (no persisted
// "locked" flag, no dependency on when Pro was purchased), so a delete
// that drops the user back under the limit, or a fresh loss of Pro,
// changes the answer immediately without any reconciliation step.
func (s *Service) isWritable(ctx context.Context, userID, apiaryID uuid.UUID) (bool, error) {
	ids, err := s.apiaries.WritableIDs(ctx, userID, FreeMaxApiaries)
	if err != nil {
		return false, fmt.Errorf("apiary: writable apiary ids: %w", err)
	}
	for _, id := range ids {
		if id == apiaryID {
			return true, nil
		}
	}
	return false, nil
}

// List returns the page of apiaries described by p, out of every apiary
// belonging to userID. When search is non-nil its value is matched
// case-insensitively against the apiary's name and location fields. When
// sortOrder is non-nil ("asc" or "desc") the page is ordered by creation
// date in that direction instead of the repository's default order.
// When withoutHives is true, results are additionally restricted to
// apiaries that currently have zero hives - accessToken is only ever
// used for that filter, forwarded to hive-service so it can answer
// against its own data (this service has none of its own).
func (s *Service) List(ctx context.Context, userID uuid.UUID, accessToken string, p pagination.Params, search, sortOrder *string, withoutHives bool) ([]*WithAccess, int, error) {
	var apiaryIDsWithHives []uuid.UUID
	if withoutHives {
		ids, err := s.hives.ApiaryIDsWithHives(ctx, accessToken)
		if err != nil {
			return nil, 0, fmt.Errorf("apiary: get apiary ids with hives: %w", err)
		}
		apiaryIDsWithHives = ids
	}

	apiaries, total, err := s.apiaries.ListByUser(ctx, userID, p, search, sortOrder, withoutHives, apiaryIDsWithHives)
	if err != nil {
		return nil, 0, err
	}

	// Entitlement is resolved once per request here, then a single bounded
	// query determines the whole writable set - never a per-row check -
	// and that set is computed from userID's complete live apiaries, not
	// from this page/search result, so which page or search term is being
	// viewed can never change which apiary is writable.
	entitlement, err := s.subscriptions.GetEntitlement(ctx, accessToken)
	if err != nil {
		return nil, 0, fmt.Errorf("apiary: resolve entitlement: %w", err)
	}

	var writableSet map[uuid.UUID]bool
	if entitlement == EntitlementFree {
		ids, err := s.apiaries.WritableIDs(ctx, userID, FreeMaxApiaries)
		if err != nil {
			return nil, 0, fmt.Errorf("apiary: writable apiary ids: %w", err)
		}
		writableSet = make(map[uuid.UUID]bool, len(ids))
		for _, id := range ids {
			writableSet[id] = true
		}
	}

	out := make([]*WithAccess, len(apiaries))
	for i, a := range apiaries {
		out[i] = &WithAccess{Apiary: a, Writable: entitlement != EntitlementFree || writableSet[a.ID]}
	}
	return out, total, nil
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
func (s *Service) Update(ctx context.Context, userID uuid.UUID, accessToken string, apiaryID uuid.UUID, in UpdateInput) (*WithAccess, error) {
	a, err := s.apiaries.GetByID(ctx, userID, apiaryID)
	if err != nil {
		return nil, err
	}

	entitlement, err := s.subscriptions.GetEntitlement(ctx, accessToken)
	if err != nil {
		return nil, fmt.Errorf("apiary: resolve entitlement: %w", err)
	}
	if entitlement == EntitlementFree {
		writable, err := s.isWritable(ctx, userID, apiaryID)
		if err != nil {
			return nil, err
		}
		if !writable {
			return nil, ErrReadOnly
		}
	}

	if in.Images != nil {
		dedup := dedupeImages(*in.Images)
		if len(dedup) > MaxMediaAttachments {
			return nil, ErrMediaLimitReached
		}
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

	// The write above only ever succeeds when the apiary was just proven
	// writable (Pro, or the Free-writable check above), so the result is
	// always writable too.
	return &WithAccess{Apiary: a, Writable: true}, nil
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
