package apiary

import "errors"

// ErrImageNotFound is returned when an ID in UpdateInput.Images can't be
// attached to this apiary - whether because it doesn't exist, belongs to
// a different user, or is already attached to a different owner
// entirely. A media item's owner is fixed the first time it's attached
// and can't be moved, so an update can only ever attach the caller's own
// not-yet-attached uploads or keep ones already attached to this same
// apiary; anything else is rejected without distinguishing why, by the
// same non-leaking convention apiary.ErrNotFound already follows.
var ErrImageNotFound = errors.New("image not found")
