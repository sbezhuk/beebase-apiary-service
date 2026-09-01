package apiary

import (
	"context"

	"github.com/google/uuid"
)

// HiveCascadeDeleter deletes every hive belonging to an apiary - and,
// transitively, their inspections and media - in hive-service, as part of
// cascading an apiary delete. It's a port because hives live in a
// different service with its own database.
type HiveCascadeDeleter interface {
	DeleteByApiary(ctx context.Context, accessToken string, apiaryID uuid.UUID) error
}

// MediaClient is apiary-service's dependency on media-service: deleting
// every media item attached directly to an apiary (as opposed to one of
// its hives) when cascading an apiary delete, and reconciling which media
// stay attached to an apiary on update.
type MediaClient interface {
	// DeleteByOwner deletes every media item attached directly to an
	// apiary.
	DeleteByOwner(ctx context.Context, accessToken string, apiaryID uuid.UUID) error
	// ListAttached returns the IDs of every media item currently
	// attached to apiaryID, belonging to whoever presented accessToken.
	ListAttached(ctx context.Context, accessToken string, apiaryID uuid.UUID) ([]uuid.UUID, error)
	// VerifyAttached confirms mediaID exists, belongs to whoever
	// presented accessToken, and is already attached to apiaryID.
	// Returns ErrImageNotFound otherwise.
	VerifyAttached(ctx context.Context, accessToken string, apiaryID, mediaID uuid.UUID) error
	// Detach removes a single media item, used to drop images an update
	// no longer wants attached to this apiary.
	Detach(ctx context.Context, accessToken string, mediaID uuid.UUID) error
}
