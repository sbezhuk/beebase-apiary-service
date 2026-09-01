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

// MediaDeleter deletes every media item attached directly to an apiary
// (as opposed to one of its hives), in media-service.
type MediaDeleter interface {
	DeleteByOwner(ctx context.Context, accessToken string, apiaryID uuid.UUID) error
}
