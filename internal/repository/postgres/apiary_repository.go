package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sbezhuk/beebase-apiary-service/internal/domain/apiary"
	"github.com/sbezhuk/beebase-common/pagination"
)

// ApiaryRepository implements domain/apiary.Repository against
// PostgreSQL. Every method scopes its query by user_id, so a user can
// never read or write an apiary they don't own: there's no separate
// ownership-check step to forget.
type ApiaryRepository struct {
	db Querier
}

// NewApiaryRepository returns an ApiaryRepository backed by db.
func NewApiaryRepository(db Querier) *ApiaryRepository {
	return &ApiaryRepository{db: db}
}

func (r *ApiaryRepository) Create(ctx context.Context, a *apiary.Apiary) error {
	const q = `
		INSERT INTO apiaries (id, user_id, name, location, description, lat, lon, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := r.db.Exec(ctx, q, a.ID, a.UserID, a.Name, a.Location, a.Description, a.Lat, a.Lon, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: create apiary: %w", err)
	}

	return nil
}

func (r *ApiaryRepository) GetByID(ctx context.Context, userID, apiaryID uuid.UUID) (*apiary.Apiary, error) {
	const q = `
		SELECT id, user_id, name, location, description, lat, lon, created_at, updated_at, deleted_at
		FROM apiaries
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`

	var a apiary.Apiary

	err := r.db.QueryRow(ctx, q, apiaryID, userID).Scan(
		&a.ID, &a.UserID, &a.Name, &a.Location, &a.Description, &a.Lat, &a.Lon, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apiary.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get apiary: %w", err)
	}

	return &a, nil
}

func (r *ApiaryRepository) ListByUser(ctx context.Context, userID uuid.UUID, p pagination.Params) ([]*apiary.Apiary, int, error) {
	const countQ = `
		SELECT count(*)
		FROM apiaries
		WHERE user_id = $1 AND deleted_at IS NULL
	`

	var total int
	if err := r.db.QueryRow(ctx, countQ, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgres: count apiaries: %w", err)
	}

	const q = `
		SELECT id, user_id, name, location, description, lat, lon, created_at, updated_at, deleted_at
		FROM apiaries
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at ASC, id ASC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.db.Query(ctx, q, userID, p.Limit, p.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: list apiaries: %w", err)
	}
	defer rows.Close()

	apiaries := []*apiary.Apiary{}
	for rows.Next() {
		var a apiary.Apiary
		if err := rows.Scan(&a.ID, &a.UserID, &a.Name, &a.Location, &a.Description, &a.Lat, &a.Lon, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt); err != nil {
			return nil, 0, fmt.Errorf("postgres: scan apiary: %w", err)
		}
		apiaries = append(apiaries, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("postgres: list apiaries: %w", err)
	}

	return apiaries, total, nil
}

func (r *ApiaryRepository) Update(ctx context.Context, a *apiary.Apiary) error {
	const q = `
		UPDATE apiaries
		SET name = $1, location = $2, description = $3, lat = $4, lon = $5, updated_at = $6
		WHERE id = $7 AND user_id = $8 AND deleted_at IS NULL
	`

	tag, err := r.db.Exec(ctx, q, a.Name, a.Location, a.Description, a.Lat, a.Lon, a.UpdatedAt, a.ID, a.UserID)
	if err != nil {
		return fmt.Errorf("postgres: update apiary: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apiary.ErrNotFound
	}

	return nil
}

func (r *ApiaryRepository) Delete(ctx context.Context, userID, apiaryID uuid.UUID) error {
	const q = `
		UPDATE apiaries
		SET deleted_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`

	tag, err := r.db.Exec(ctx, q, apiaryID, userID)
	if err != nil {
		return fmt.Errorf("postgres: delete apiary: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apiary.ErrNotFound
	}

	return nil
}
