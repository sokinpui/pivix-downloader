package model

import (
	"time"
)

type Setting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ArtworkStatus string

const (
	StatusArtworkPending    ArtworkStatus = "pending"
	StatusArtworkProcessing ArtworkStatus = "processing"
	StatusArtworkCompleted  ArtworkStatus = "completed"
	StatusArtworkPartial    ArtworkStatus = "partial"
	StatusArtworkFailed     ArtworkStatus = "failed"
)

type Artwork struct {
	ID           string        `json:"id"`
	Title        string        `json:"title"`
	UserID       string        `json:"user_id"`
	UserName     string        `json:"user_name"`
	PageCount    int           `json:"page_count"`
	Tags         []string      `json:"tags"`
	IllustType   int           `json:"illust_type"`
	SourceType   string        `json:"source_type"` // 'bookmark' | 'manual'
	Status       ArtworkStatus `json:"status"`
	ErrorMessage string        `json:"error_message"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
	Tasks        []DownloadTask `json:"tasks,omitempty"`
}

type TaskStatus string

const (
	StatusTaskPending     TaskStatus = "pending"
	StatusTaskDownloading TaskStatus = "downloading"
	StatusTaskCompleted   TaskStatus = "completed"
	StatusTaskFailed      TaskStatus = "failed"
)

type DownloadTask struct {
	ID           int64      `json:"id"`
	ArtworkID    string     `json:"artwork_id"`
	PageIndex    int        `json:"page_index"`
	ImageURL     string     `json:"image_url"`
	FilePath     string     `json:"file_path"`
	FileSize     int64      `json:"file_size"`
	Status       TaskStatus `json:"status"`
	RetryCount   int        `json:"retry_count"`
	ErrorMessage string     `json:"error_message"`
	CreatedAt    time.Time  `json:"created_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}
