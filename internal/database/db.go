package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func InitDB(dbPath string) (*sql.DB, error) {
	dir := filepath.Dir(dbPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create db directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping sqlite db: %w", err)
	}

	if err := createTables(db); err != nil {
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	return db, nil
}

func createTables(db *sql.DB) error {
	schema := `
-- 系统运行时配置
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 画作元数据表（排重与归档基准）
CREATE TABLE IF NOT EXISTS artworks (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    user_id TEXT NOT NULL,
    user_name TEXT NOT NULL DEFAULT '',
    page_count INTEGER NOT NULL DEFAULT 1,
    illust_type INTEGER NOT NULL DEFAULT 0,
    source_type TEXT NOT NULL,                -- 'bookmark' | 'manual'
    status TEXT NOT NULL,                     -- 'pending' | 'processing' | 'completed' | 'partial' | 'failed'
    error_message TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_artworks_status ON artworks(status);
CREATE INDEX IF NOT EXISTS idx_artworks_created_at ON artworks(created_at DESC);

-- 具体单图下载任务表
CREATE TABLE IF NOT EXISTS download_tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    artwork_id TEXT NOT NULL,
    page_index INTEGER NOT NULL,
    image_url TEXT NOT NULL,
    file_path TEXT NOT NULL DEFAULT '',
    file_size INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,                     -- 'pending' | 'downloading' | 'completed' | 'failed'
    retry_count INTEGER NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME,
    FOREIGN KEY(artwork_id) REFERENCES artworks(id) ON DELETE CASCADE,
    UNIQUE(artwork_id, page_index)
);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON download_tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_artwork_id ON download_tasks(artwork_id);
`
	_, err := db.Exec(schema)
	return err
}
