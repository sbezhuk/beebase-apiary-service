package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sbezhuk/beebase-apiary-service/internal/domain/apiary"
	"github.com/sbezhuk/beebase-common/pagination"
)

// minSearchLength is the minimum number of characters required for the
// search term to be applied. Shorter terms produce noisy results and put
// unnecessary load on the database.
const minSearchLength = 3

// uniqueViolationCode is PostgreSQL's SQLSTATE for a unique constraint
// violation.
const uniqueViolationCode = "23505"

// createdAtOrderClause returns the ORDER BY clause for a list query. When
// sortOrder is nil, defaultClause (the query's normal, pre-existing order)
// is used unchanged; otherwise the list is ordered by creation date in the
// requested direction, with id tied to the same direction as a stable
// tiebreaker (matching the convention every other ORDER BY in this
// repository already follows).
func createdAtOrderClause(sortOrder *string, defaultClause string) string {
	if sortOrder == nil {
		return defaultClause
	}
	dir := "ASC"
	if *sortOrder == "desc" {
		dir = "DESC"
	}
	return fmt.Sprintf("created_at %s, id %s", dir, dir)
}

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
		if isUniqueNameViolation(err) {
			return apiary.ErrNameTaken
		}
		return fmt.Errorf("postgres: create apiary: %w", err)
	}

	return nil
}

// CountByUser returns the total number of non-deleted apiaries owned by userID.
func (r *ApiaryRepository) CountByUser(ctx context.Context, userID uuid.UUID) (int, error) {
	const q = `
		SELECT count(*)
		FROM apiaries
		WHERE user_id = $1 AND deleted_at IS NULL
	`
	var count int
	if err := r.db.QueryRow(ctx, q, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("postgres: count apiaries: %w", err)
	}
	return count, nil
}

// CreateWithLimit creates a new apiary, but only if the user currently owns
// fewer than maxCount active apiaries. If maxCount <= 0, creation is unlimited.
// Uses a transaction-scoped advisory lock on the user ID to prevent race conditions.
func (r *ApiaryRepository) CreateWithLimit(ctx context.Context, a *apiary.Apiary, maxCount int) error {
	if maxCount <= 0 {
		return r.Create(ctx, a)
	}

	pool, isPool := r.db.(*pgxpool.Pool)
	if isPool {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("postgres: begin tx: %w", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()

		const lockQ = `SELECT pg_advisory_xact_lock(hashtext('apiary_limit:' || $1::text))`
		if _, err := tx.Exec(ctx, lockQ, a.UserID); err != nil {
			return fmt.Errorf("postgres: acquire advisory lock: %w", err)
		}

		const countQ = `SELECT count(*) FROM apiaries WHERE user_id = $1 AND deleted_at IS NULL`
		var count int
		if err := tx.QueryRow(ctx, countQ, a.UserID).Scan(&count); err != nil {
			return fmt.Errorf("postgres: count apiaries: %w", err)
		}
		if count >= maxCount {
			return apiary.ErrLimitReached
		}

		const insertQ = `
			INSERT INTO apiaries (id, user_id, name, location, description, lat, lon, images, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`
		if _, err := tx.Exec(ctx, insertQ, a.ID, a.UserID, a.Name, a.Location, a.Description, a.Lat, a.Lon, images(a.Images), a.CreatedAt, a.UpdatedAt); err != nil {
			if isUniqueNameViolation(err) {
				return apiary.ErrNameTaken
			}
			return fmt.Errorf("postgres: create apiary: %w", err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("postgres: commit tx: %w", err)
		}
		return nil
	}

	const lockQ = `SELECT pg_advisory_xact_lock(hashtext('apiary_limit:' || $1::text))`
	if _, err := r.db.Exec(ctx, lockQ, a.UserID); err != nil {
		return fmt.Errorf("postgres: acquire advisory lock: %w", err)
	}

	const countQ = `SELECT count(*) FROM apiaries WHERE user_id = $1 AND deleted_at IS NULL`
	var count int
	if err := r.db.QueryRow(ctx, countQ, a.UserID).Scan(&count); err != nil {
		return fmt.Errorf("postgres: count apiaries: %w", err)
	}
	if count >= maxCount {
		return apiary.ErrLimitReached
	}

	return r.Create(ctx, a)
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

func (r *ApiaryRepository) ListByUser(ctx context.Context, userID uuid.UUID, p pagination.Params, search, sortOrder *string) ([]*apiary.Apiary, int, error) {
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

	orderBy := createdAtOrderClause(sortOrder, "created_at ASC, id ASC")

	if search != nil && len(*search) >= minSearchLength {
		pattern := "%" + *search + "%"
		countQ += ` AND (name ILIKE $2 OR location ILIKE $2)`
		countArgs = append(countArgs, pattern)
		q += fmt.Sprintf(` AND (name ILIKE $2 OR location ILIKE $2)`)
		q += fmt.Sprintf(`
		ORDER BY %s
		LIMIT $3 OFFSET $4`, orderBy)
		listArgs = append(listArgs, pattern, p.Limit, p.Offset())
	} else {
		q += fmt.Sprintf(`
		ORDER BY %s
		LIMIT $2 OFFSET $3`, orderBy)
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
		if isUniqueNameViolation(err) {
			return apiary.ErrNameTaken
		}
		return fmt.Errorf("postgres: update apiary: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apiary.ErrNotFound
	}

	return nil
}

func isUniqueNameViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == uniqueViolationCode &&
		pgErr.ConstraintName == "idx_apiaries_user_id_name_unique_active"
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
