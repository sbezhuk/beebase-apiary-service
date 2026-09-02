package apiary

import "errors"

// ErrImageNotFound is returned when an ID in CreateInput.Images or
// UpdateInput.Images doesn't belong to the caller, per media-service's
// own ownership check (see MediaClient.VerifyOwnership) - whether because
// it doesn't exist, was deleted, or belongs to a different user entirely.
// These cases are rejected without distinguishing why, by the same
// non-leaking convention apiary.ErrNotFound already follows.
var ErrImageNotFound = errors.New("image not found")
