package apiary

import (
	"context"

	"github.com/google/uuid"
)

// Repository is the port through which the application persists and
// retrieves apiaries. Every method that targets a specific apiary takes
// the owning userID alongside the apiary ID, so ownership is enforced by
// the query itself (typically a single "WHERE id = $1 AND user_id = $2"),
// not by a separate check layered on top: there is no path to reading or
// writing another user's apiary.
type Repository interface {
	Create(ctx context.Context, a *Apiary) error
	GetByID(ctx context.Context, userID, apiaryID uuid.UUID) (*Apiary, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]*Apiary, error)
	// Update persists a.Name, a.Location, a.Description, and a.UpdatedAt for the
	// apiary identified by a.ID, scoped to a.UserID.
	Update(ctx context.Context, a *Apiary) error
	// Delete soft-deletes the apiary (sets deleted_at) rather than
	// removing the row, per the project's synchronizable-entity plan.
	Delete(ctx context.Context, userID, apiaryID uuid.UUID) error
}
