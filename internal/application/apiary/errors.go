package apiary

import "errors"

// ErrImageNotFound is returned when an ID in UpdateInput.Images doesn't
// identify a media item already attached to this apiary - whether
// because it doesn't exist, belongs to a different user, or is attached
// to a different owner entirely. A media item's owner is fixed at upload
// time in media-service and can't be moved, so the only IDs an update can
// ever legitimately keep are ones already attached to this same apiary;
// anything else is rejected without distinguishing why, by the same
// non-leaking convention apiary.ErrNotFound already follows.
var ErrImageNotFound = errors.New("image not found")
