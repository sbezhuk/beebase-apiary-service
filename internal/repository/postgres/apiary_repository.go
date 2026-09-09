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

// minSearchLength is the minimum number of characters required for the
// search term to be applied. Shorter terms produce noisy results and put
// unnecessary load on the database.
const minSearchLength = 3

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
		INSERT INTO apiaries (id, user_id, name, location, description, lat, lon, images, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	_, err := r.db.Exec(ctx, q, a.ID, a.UserID, a.Name, a.Location, a.Description, a.Lat, a.Lon, images(a.Images), a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: create apiary: %w", err)
	}

	return nil
}

func (r *ApiaryRepository) GetByID(ctx context.Context, userID, apiaryID uuid.UUID) (*apiary.Apiary, error) {
	const q = `
		SELECT id, user_id, name, location, description, lat, lon, images, created_at, updated_at, deleted_at
		FROM apiaries
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`

	var a apiary.Apiary

	err := r.db.QueryRow(ctx, q, apiaryID, userID).Scan(
		&a.ID, &a.UserID, &a.Name, &a.Location, &a.Description, &a.Lat, &a.Lon, &a.Images, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apiary.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get apiary: %w", err)
	}

	return &a, nil
}

func (r *ApiaryRepository) ListByUser(ctx context.Context, userID uuid.UUID, p pagination.Params, search *string) ([]*apiary.Apiary, int, error) {
	countQ := `
		SELECT count(*)
		FROM apiaries
		WHERE user_id = $1 AND deleted_at IS NULL
	`
	countArgs := []any{userID}

	q := `
		SELECT id, user_id, name, location, description, lat, lon, images, created_at, updated_at, deleted_at
		FROM apiaries
		WHERE user_id = $1 AND deleted_at IS NULL
	`
	listArgs := []any{userID}

	if search != nil && len(*search) >= minSearchLength {
		pattern := "%" + *search + "%"
		countQ += ` AND (name ILIKE $2 OR location ILIKE $2)`
		countArgs = append(countArgs, pattern)
		q += fmt.Sprintf(` AND (name ILIKE $2 OR location ILIKE $2)`)
		q += fmt.Sprintf(`
		ORDER BY created_at ASC, id ASC
		LIMIT $3 OFFSET $4`)
		listArgs = append(listArgs, pattern, p.Limit, p.Offset())
	} else {
		q += `
		ORDER BY created_at ASC, id ASC
		LIMIT $2 OFFSET $3`
		listArgs = append(listArgs, p.Limit, p.Offset())
	}

	var total int
	if err := r.db.QueryRow(ctx, countQ, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgres: count apiaries: %w", err)
	}

	rows, err := r.db.Query(ctx, q, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: list apiaries: %w", err)
	}
	defer rows.Close()

	apiaries := []*apiary.Apiary{}
	for rows.Next() {
		var a apiary.Apiary
		if err := rows.Scan(&a.ID, &a.UserID, &a.Name, &a.Location, &a.Description, &a.Lat, &a.Lon, &a.Images, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt); err != nil {
			return nil, 0, fmt.Errorf("postgres: scan apiary: %w", err)
		}
		apiaries = append(apiaries, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("postgres: list apiaries: %w", err)
	}

	return apiaries, total, nil
}

// ListAllByUser returns every apiary belonging to userID, unpaginated -
// used only by the account-deletion cascade (application/apiary.Service.
// DeleteAllByUser), never by an HTTP-facing listing.
func (r *ApiaryRepository) ListAllByUser(ctx context.Context, userID uuid.UUID) ([]*apiary.Apiary, error) {
	const q = `
		SELECT id, user_id, name, location, description, lat, lon, images, created_at, updated_at, deleted_at
		FROM apiaries
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at ASC, id ASC
	`

	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list all apiaries by user: %w", err)
	}
	defer rows.Close()

	apiaries := []*apiary.Apiary{}
	for rows.Next() {
		var a apiary.Apiary
		if err := rows.Scan(&a.ID, &a.UserID, &a.Name, &a.Location, &a.Description, &a.Lat, &a.Lon, &a.Images, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan apiary: %w", err)
		}
		apiaries = append(apiaries, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list all apiaries by user: %w", err)
	}

	return apiaries, nil
}

func (r *ApiaryRepository) Update(ctx context.Context, a *apiary.Apiary) error {
	const q = `
		UPDATE apiaries
		SET name = $1, location = $2, description = $3, lat = $4, lon = $5, images = $6, updated_at = $7
		WHERE id = $8 AND user_id = $9 AND deleted_at IS NULL
	`

	tag, err := r.db.Exec(ctx, q, a.Name, a.Location, a.Description, a.Lat, a.Lon, images(a.Images), a.UpdatedAt, a.ID, a.UserID)
	if err != nil {
		return fmt.Errorf("postgres: update apiary: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apiary.ErrNotFound
	}

	return nil
}

// images coalesces a nil slice to an empty one - the images column is
// NOT NULL, and pgx would otherwise encode a nil Go slice as SQL NULL.
func images(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}

func (r *ApiaryRepository) HardDelete(ctx context.Context, userID, apiaryID uuid.UUID) error {
	const q = `DELETE FROM apiaries WHERE id = $1 AND user_id = $2`

	tag, err := r.db.Exec(ctx, q, apiaryID, userID)
	if err != nil {
		return fmt.Errorf("postgres: hard delete apiary: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apiary.ErrNotFound
	}

	return nil
}
