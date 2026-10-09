package repository

import (
	"database/sql"
	"github.com/sokinpui/pivix-downloader/internal/model"
	"time"
)

type ArtworkRepository struct {
	db *sql.DB
}

func NewArtworkRepository(db *sql.DB) *ArtworkRepository {
	return &ArtworkRepository{db: db}
}

func (r *ArtworkRepository) Upsert(art *model.Artwork) error {
	_, err := r.db.Exec(`
		INSERT INTO artworks (id, title, user_id, user_name, page_count, illust_type, source_type, status, error_message, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			user_id = excluded.user_id,
			user_name = excluded.user_name,
			page_count = excluded.page_count,
			illust_type = excluded.illust_type,
			status = CASE WHEN artworks.status = 'completed' THEN 'completed' ELSE excluded.status END,
			error_message = excluded.error_message,
			updated_at = CURRENT_TIMESTAMP
	`, art.ID, art.Title, art.UserID, art.UserName, art.PageCount, art.IllustType, art.SourceType, art.Status, art.ErrorMessage)
	return err
}

func (r *ArtworkRepository) GetByID(id string) (*model.Artwork, error) {
	row := r.db.QueryRow(`
		SELECT id, title, user_id, user_name, page_count, illust_type, source_type, status, error_message, created_at, updated_at
		FROM artworks WHERE id = ?
	`, id)

	var art model.Artwork
	var createdAt, updatedAt string
	err := row.Scan(&art.ID, &art.Title, &art.UserID, &art.UserName, &art.PageCount, &art.IllustType, &art.SourceType, &art.Status, &art.ErrorMessage, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	art.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	art.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
	return &art, nil
}

func (r *ArtworkRepository) List(status, source string, limit, offset int) ([]model.Artwork, error) {
	query := `SELECT id, title, user_id, user_name, page_count, illust_type, source_type, status, error_message, created_at, updated_at FROM artworks WHERE 1=1`
	var args []any

	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	if source != "" {
		query += ` AND source_type = ?`
		args = append(args, source)
	}

	query += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var artworks []model.Artwork
	for rows.Next() {
		var art model.Artwork
		var createdAt, updatedAt string
		if err := rows.Scan(&art.ID, &art.Title, &art.UserID, &art.UserName, &art.PageCount, &art.IllustType, &art.SourceType, &art.Status, &art.ErrorMessage, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		art.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		art.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
		artworks = append(artworks, art)
	}
	return artworks, nil
}

func (r *ArtworkRepository) UpdateStatus(id string, status model.ArtworkStatus, errMsg string) error {
	_, err := r.db.Exec(`
		UPDATE artworks 
		SET status = ?, error_message = ?, updated_at = CURRENT_TIMESTAMP 
		WHERE id = ?
	`, status, errMsg, id)
	return err
}
