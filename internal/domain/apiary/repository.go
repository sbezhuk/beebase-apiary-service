package apiary

import (
	"context"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-common/pagination"
)

// Repository is the port through which the application persists and
// retrieves apiaries. Every method that targets a specific apiary takes
// the owning userID alongside the apiary ID, so ownership is enforced by
// the query itself (typically a single "WHERE id = $1 AND user_id = $2"),
// not by a separate check layered on top: there is no path to reading or
// writing another user's apiary.
type Repository interface {
	Create(ctx context.Context, a *Apiary) error
	// CreateWithLimit creates a new apiary, but only if the user owns fewer
	// than maxCount active apiaries. If maxCount <= 0, creation is unlimited.
	// Returns ErrLimitReached if the limit is exceeded.
	CreateWithLimit(ctx context.Context, a *Apiary, maxCount int) error
	// CountByUser returns the total number of non-deleted apiaries owned by userID.
	CountByUser(ctx context.Context, userID uuid.UUID) (int, error)
	GetByID(ctx context.Context, userID, apiaryID uuid.UUID) (*Apiary, error)
	// ListByUser returns the page of apiaries described by p, along with
	// the total number of apiaries userID owns (independent of p, for
	// computing pagination metadata). When search is non-nil its value is
	// matched case-insensitively against name and location; a nil search
	// means no filter. When sortOrder is non-nil ("asc" or "desc") the
	// page is ordered by creation date in that direction instead of the
	// default order; a nil sortOrder keeps the default order.
	ListByUser(ctx context.Context, userID uuid.UUID, p pagination.Params, search, sortOrder *string) (apiaries []*Apiary, total int, err error)
	// ListAllByUser returns every apiary userID owns, unpaginated. Used
	// only by Service.DeleteAllByUser, which must cascade-delete every
	// apiary an account owns regardless of how many there are - unlike
	// ListByUser, this is never exposed to an HTTP caller.
	ListAllByUser(ctx context.Context, userID uuid.UUID) ([]*Apiary, error)
	// Update persists a.Name, a.Location, a.Description, and a.UpdatedAt for the
	// apiary identified by a.ID, scoped to a.UserID.
	Update(ctx context.Context, a *Apiary) error
	// HardDelete physically removes the apiary row. There is no
	// soft-delete path left on this port: an apiary delete is always a
	// full cascade (see application/apiary.Service.Delete), called only
	// after hive-service and media-service have already deleted
	// everything that belonged to this apiary.
	HardDelete(ctx context.Context, userID, apiaryID uuid.UUID) error
}
