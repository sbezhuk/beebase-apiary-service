package apiary

import (
	"context"

	"github.com/google/uuid"
)

// HiveClient is this service's dependency on hive-service.
type HiveClient interface {
	// DeleteByApiary deletes every hive belonging to an apiary - and,
	// transitively, their inspections and media - as part of cascading
	// an apiary delete.
	DeleteByApiary(ctx context.Context, accessToken string, apiaryID uuid.UUID) error
	// ApiaryIDsWithHives returns the id of every apiary belonging to
	// whoever presented accessToken that currently has at least one
	// hive. Used to filter apiary listings to "apiaries without hives" -
	// this service has no notion of hives of its own, so it asks
	// hive-service instead of duplicating that data.
	ApiaryIDsWithHives(ctx context.Context, accessToken string) ([]uuid.UUID, error)
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

type EntityCleanup interface {
	Cleanup(ctx context.Context, entityType string, entityID uuid.UUID) error
}

// Entitlement values returned by subscription-service.
const (
	EntitlementFree = "free"
	EntitlementPro  = "pro"

	// FreeMaxApiaries is the maximum number of apiaries a free-tier user can own.
	FreeMaxApiaries = 1

	// MaxMediaAttachments is the maximum number of media attachments allowed per apiary.
	MaxMediaAttachments = 5
)

// EntitlementResolver resolves the subscription entitlement for a user by
// forwarding their access token to subscription-service.
type EntitlementResolver interface {
	GetEntitlement(ctx context.Context, accessToken string) (string, error)
}
