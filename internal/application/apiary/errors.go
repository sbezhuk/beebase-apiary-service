package apiary

import "errors"

// ErrImageNotFound is returned when an ID in CreateInput.Images or
// UpdateInput.Images doesn't belong to the caller, per media-service's
// own ownership check (see MediaClient.VerifyOwnership) - whether because
// it doesn't exist, was deleted, or belongs to a different user entirely.
// These cases are rejected without distinguishing why, by the same
// non-leaking convention apiary.ErrNotFound already follows.
var ErrImageNotFound = errors.New("image not found")

// ErrApiaryLimitReached is returned when a free-tier user attempts to create
// more apiaries than permitted by the free plan.
var ErrApiaryLimitReached = errors.New("apiary limit reached")

// ErrMediaLimitReached is returned when an attempt is made to attach more
// photos than permitted by the media attachment limit.
var ErrMediaLimitReached = errors.New("media limit reached")
