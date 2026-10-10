package service

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/sokinpui/pixiv-downloader/internal/model"
	"github.com/sokinpui/pixiv-downloader/internal/pixiv"
	"github.com/sokinpui/pixiv-downloader/internal/repository"
)

type SyncService struct {
	client      *pixiv.PixivClient
	artworkRepo *repository.ArtworkRepository
	taskRepo    *repository.TaskRepository
	eventHub    *EventHub
	enqueueFunc func(taskID int64)

	mu         sync.Mutex
	cancelSync context.CancelFunc
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
	s.mu.Lock()
	if s.cancelSync != nil {
		s.cancelSync()
	}
	syncCtx, cancel := context.WithCancel(ctx)
	s.cancelSync = cancel
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		if s.cancelSync != nil {
			s.cancelSync()
			s.cancelSync = nil
		}
		s.mu.Unlock()
	}()

	if opts.UserID == "" {
		return nil
	}
	if opts.EarlyExitN <= 0 {
		opts.EarlyExitN = 10
	}

	limit := 48
	offset := 0
	consecutiveCompleted := decrConsecutiveCount()

	log.Printf("[SyncService] Starting bookmark sync for user %s (force_full=%v)...", opts.UserID, opts.ForceFull)

	for {
		if syncCtx.Err() != nil {
			log.Printf("[SyncService] Bookmark sync cancelled for user %s", opts.UserID)
			return syncCtx.Err()
		}

		works, err := s.client.FetchBookmarks(syncCtx, opts.UserID, offset, limit)
		if err != nil {
			log.Printf("[SyncService] Fetch bookmarks error at offset %d: %v", offset, err)
			break
		}

		if len(works) == 0 {
			break
		}

		batchEarlyExit := false
		for _, work := range works {
			if syncCtx.Err() != nil {
				break
			}

			illustID := work.ArtworkID()
			if illustID == "" {
				continue
			}

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

			consecutiveCompleted.reset()

			art := &model.Artwork{
				ID:         illustID,
				Title:      work.Title,
				UserID:     work.AuthorID(),
				UserName:   work.UserName,
				PageCount:  work.PageCount,
				Tags:       work.Tags,
				IllustType: work.IllustType,
				SourceType: "bookmark",
				Status:     model.StatusArtworkPending,
			}
			_ = s.artworkRepo.Upsert(art)
			s.eventHub.Publish(EventArtworkDiscovered, art)

			pages, err := s.client.FetchPages(syncCtx, illustID)
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

func (s *SyncService) StopSync() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelSync != nil {
		s.cancelSync()
		s.cancelSync = nil
		log.Printf("[SyncService] Stop sync requested via API")
	}
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
