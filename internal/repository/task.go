package repository

import (
	"database/sql"
	"pivis-downloader/internal/model"
	"time"
)

type TaskRepository struct {
	db *sql.DB
}

func NewTaskRepository(db *sql.DB) *TaskRepository {
	return &TaskRepository{db: db}
}

func (r *TaskRepository) CreateOrIgnore(task *model.DownloadTask) error {
	_, err := r.db.Exec(`
		INSERT OR IGNORE INTO download_tasks (artwork_id, page_index, image_url, status)
		VALUES (?, ?, ?, ?)
	`, task.ArtworkID, task.PageIndex, task.ImageURL, task.Status)
	return err
}

func (r *TaskRepository) GetByID(id int64) (*model.DownloadTask, error) {
	row := r.db.QueryRow(`
		SELECT id, artwork_id, page_index, image_url, file_path, file_size, status, retry_count, error_message, created_at, finished_at
		FROM download_tasks WHERE id = ?
	`, id)

	var t model.DownloadTask
	var createdAt string
	var finishedAt sql.NullString
	err := row.Scan(&t.ID, &t.ArtworkID, &t.PageIndex, &t.ImageURL, &t.FilePath, &t.FileSize, &t.Status, &t.RetryCount, &t.ErrorMessage, &createdAt, &finishedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	t.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	if finishedAt.Valid {
		ft, _ := time.Parse("2006-01-02 15:04:05", finishedAt.String)
		t.FinishedAt = &ft
	}
	return &t, nil
}

func (r *TaskRepository) ListByArtworkID(artworkID string) ([]model.DownloadTask, error) {
	rows, err := r.db.Query(`
		SELECT id, artwork_id, page_index, image_url, file_path, file_size, status, retry_count, error_message, created_at, finished_at
		FROM download_tasks WHERE artwork_id = ? ORDER BY page_index ASC
	`, artworkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []model.DownloadTask
	for rows.Next() {
		var t model.DownloadTask
		var createdAt string
		var finishedAt sql.NullString
		if err := rows.Scan(&t.ID, &t.ArtworkID, &t.PageIndex, &t.ImageURL, &t.FilePath, &t.FileSize, &t.Status, &t.RetryCount, &t.ErrorMessage, &createdAt, &finishedAt); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		if finishedAt.Valid {
			ft, _ := time.Parse("2006-01-02 15:04:05", finishedAt.String)
			t.FinishedAt = &ft
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func (r *TaskRepository) UpdateStatus(id int64, status model.TaskStatus, filePath string, fileSize int64, errMsg string) error {
	var query string
	var args []any

	if status == model.StatusTaskCompleted {
		query = `UPDATE download_tasks SET status = ?, file_path = ?, file_size = ?, error_message = '', finished_at = CURRENT_TIMESTAMP WHERE id = ?`
		args = []any{status, filePath, fileSize, id}
	} else if status == model.StatusTaskFailed {
		query = `UPDATE download_tasks SET status = ?, error_message = ?, retry_count = retry_count + 1 WHERE id = ?`
		args = []any{status, errMsg, id}
	} else {
		query = `UPDATE download_tasks SET status = ?, error_message = ? WHERE id = ?`
		args = []any{status, errMsg, id}
	}

	_, err := r.db.Exec(query, args...)
	return err
}

func (r *TaskRepository) ResetFailedTask(id int64) error {
	_, err := r.db.Exec(`
		UPDATE download_tasks 
		SET status = 'pending', error_message = '' 
		WHERE id = ? AND status = 'failed'
	`, id)
	return err
}

func (r *TaskRepository) ListPending() ([]model.DownloadTask, error) {
	rows, err := r.db.Query(`
		SELECT id, artwork_id, page_index, image_url, file_path, file_size, status, retry_count, error_message, created_at, finished_at
		FROM download_tasks WHERE status = 'pending' OR status = 'downloading' ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []model.DownloadTask
	for rows.Next() {
		var t model.DownloadTask
		var createdAt string
		var finishedAt sql.NullString
		if err := rows.Scan(&t.ID, &t.ArtworkID, &t.PageIndex, &t.ImageURL, &t.FilePath, &t.FileSize, &t.Status, &t.RetryCount, &t.ErrorMessage, &createdAt, &finishedAt); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		if finishedAt.Valid {
			ft, _ := time.Parse("2006-01-02 15:04:05", finishedAt.String)
			t.FinishedAt = &ft
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}
