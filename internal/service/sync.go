package service

import (
	"context"
	"fmt"
	"log"
	"pivis-downloader/internal/model"
	"pivis-downloader/internal/pixiv"
	"pivis-downloader/internal/repository"
)

type SyncService struct {
	client      *pixiv.PixivClient
	artworkRepo *repository.ArtworkRepository
	taskRepo    *repository.TaskRepository
	eventHub    *EventHub
	enqueueFunc func(taskID int64)
}

func NewSyncService(
	client *pixiv.PixivClient,
	artworkRepo *repository.ArtworkRepository,
	taskRepo *repository.TaskRepository,
	eventHub *EventHub,
	enqueueFunc func(taskID int64),
) *SyncService {
	return &SyncService{
		client:      client,
		artworkRepo: artworkRepo,
		taskRepo:    taskRepo,
		eventHub:    eventHub,
		enqueueFunc: enqueueFunc,
	}
}

type SyncOptions struct {
	UserID     string
	ForceFull  bool
	EarlyExitN int // default 10
}

func (s *SyncService) SyncBookmarks(ctx context.Context, opts SyncOptions) error {
	if opts.UserID == "" {
		return nil
	}
	if opts.EarlyExitN <= 0 {
		opts.EarlyExitN = 10
	}

	limit := 48
	offset := 0
	consecutiveCompleted := decrConsecutiveCount() // helper tracker

	log.Printf("[SyncService] Starting bookmark sync for user %s (force_full=%v)...", opts.UserID, opts.ForceFull)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		works, err := s.client.FetchBookmarks(ctx, opts.UserID, offset, limit)
		if err != nil {
			log.Printf("[SyncService] Fetch bookmarks error at offset %d: %v", offset, err)
			break
		}

		if len(works) == 0 {
			break
		}

		batchEarlyExit := false
		for _, work := range works {
			illustID := work.ArtworkID()
			if illustID == "" {
				continue
			}

			// Check local db status
			existing, err := s.artworkRepo.GetByID(illustID)
			if err == nil && existing != nil && existing.Status == model.StatusArtworkCompleted {
				if !opts.ForceFull {
					consecutiveCompleted.inc()
					if consecutiveCompleted.val >= opts.EarlyExitN {
						log.Printf("[SyncService] Early exit triggered: found %d consecutive completed bookmarks.", opts.EarlyExitN)
						batchEarlyExit = true
						break
					}
				}
				continue
			}

			// Reset counter since we found an uncompleted or new artwork
			consecutiveCompleted.reset()

			// Upsert artwork
			art := &model.Artwork{
				ID:         illustID,
				Title:      work.Title,
				UserID:     work.AuthorID(),
				UserName:   work.UserName,
				PageCount:  work.PageCount,
				IllustType: work.IllustType,
				SourceType: "bookmark",
				Status:     model.StatusArtworkPending,
			}
			_ = s.artworkRepo.Upsert(art)
			s.eventHub.Publish(EventArtworkDiscovered, art)

			// Fetch pages and create tasks
			pages, err := s.client.FetchPages(ctx, illustID)
			if err != nil {
				log.Printf("[SyncService] Fetch pages for %s failed: %v", illustID, err)
				continue
			}

			for idx, page := range pages {
				if page.Urls.Original == "" {
					continue
				}
				task := &model.DownloadTask{
					ArtworkID: illustID,
					PageIndex: idx,
					ImageURL:  page.Urls.Original,
					Status:    model.StatusTaskPending,
				}
				if err := s.taskRepo.CreateOrIgnore(task); err == nil {
					storedTasks, _ := s.taskRepo.ListByArtworkID(illustID)
					for _, t := range storedTasks {
						if t.PageIndex == idx && t.Status == model.StatusTaskPending {
							s.enqueueFunc(t.ID)
						}
					}
				}
			}
		}

		if batchEarlyExit || len(works) < limit {
			break
		}

		offset += limit
	}

	s.eventHub.Publish(EventSyncFinished, map[string]any{"user_id": opts.UserID})
	log.Printf("[SyncService] Bookmark sync finished for user %s", opts.UserID)
	return nil
}

func (s *SyncService) GetRemoteBookmarks(ctx context.Context, userID string, offset, limit int) ([]pixiv.BookmarkWork, error) {
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	return s.client.FetchBookmarks(ctx, userID, offset, limit)
}

type counter struct {
	val int
}

func decrConsecutiveCount() *counter {
	return &counter{}
}

func (c *counter) inc() {
	c.val++
}

func (c *counter) reset() {
	c.val = 0
}
