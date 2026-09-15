package apiary

import (
	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-apiary-service/internal/domain/apiary"
)

// WithAccess wraps an apiary with whether it's currently writable for the
// caller who asked: always true on Pro; on Free, true only for the one
// apiary within FreeMaxApiaries (see Service.isWritable). Embedding
// *apiary.Apiary lets callers keep using its fields directly (a.Name,
// a.ID, ...) without unwrapping.
type WithAccess struct {
	*apiary.Apiary
	Writable bool
}

// CreateInput is the input to Service.Create.
type CreateInput struct {
	Name        string
	Location    string
	Description string
	Lat         *float64
	Lon         *float64
	// Images is the set of already-uploaded media ids to attach
	// immediately, so a caller doesn't need a separate PUT just to attach
	// photos. Empty/nil means no images.
	Images []uuid.UUID
}

// UpdateInput is the input to Service.Update. Update replaces all fields
// (PUT semantics), not a partial patch - except Images, which is left
// alone when nil so a caller that doesn't mention images at all can't
// accidentally detach every photo on an unrelated field edit.
type UpdateInput struct {
	Name        string
	Location    string
	Description string
	Lat         *float64
	Lon         *float64
	// Images is the desired final set of media IDs attached to this
	// apiary - each one either already attached here, or the caller's
	// own not-yet-attached upload (media-service links it on the fly).
	// Nil means "leave attached media alone"; a non-nil slice (including
	// an empty one) replaces the attached set exactly, attaching
	// whatever's newly listed and detaching whatever isn't listed.
	Images *[]uuid.UUID
}
