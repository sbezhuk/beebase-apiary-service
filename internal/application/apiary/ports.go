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

// MediaClient is apiary-service's dependency on media-service.
// apiary-service is the sole source of truth for which media ids are
// referenced by a given apiary (see Apiary.Images) - media-service has no
// notion of apiaries/hives at all. This client is used only to verify, on
// create/update, that every newly-referenced media id actually belongs to
// the caller, and to hard-delete an apiary's own media files when the
// apiary itself is cascade-deleted.
type MediaClient interface {
	// VerifyOwnership confirms every id in ids belongs to the caller
	// (whoever presented accessToken), by asking media-service directly -
	// it's the only remaining source of truth for "does this media id
	// exist and belong to me". Returns ErrImageNotFound if any id doesn't
	// (unknown, deleted, or someone else's - indistinguishable, by the
	// same non-leaking convention apiary.ErrNotFound already follows).
	VerifyOwnership(ctx context.Context, accessToken string, ids []uuid.UUID) error
	// DeleteByIDs hard-deletes every media item in ids, used when the
	// apiary itself is being cascade-deleted.
	DeleteByIDs(ctx context.Context, accessToken string, ids []uuid.UUID) error
}
