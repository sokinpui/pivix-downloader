package service

import (
	"context"
	"fmt"
	"github.com/sokinpui/pixiv-downloader/internal/model"
	"github.com/sokinpui/pixiv-downloader/internal/pixiv"
	"github.com/sokinpui/pixiv-downloader/internal/repository"
)

type SubmissionService struct {
	client      *pixiv.PixivClient
	artworkRepo *repository.ArtworkRepository
	taskRepo    *repository.TaskRepository
	eventHub    *EventHub
	enqueueFunc func(taskID int64)
}

func NewSubmissionService(
	client *pixiv.PixivClient,
	artworkRepo *repository.ArtworkRepository,
	taskRepo *repository.TaskRepository,
	eventHub *EventHub,
	enqueueFunc func(taskID int64),
) *SubmissionService {
	return &SubmissionService{
		client:      client,
		artworkRepo: artworkRepo,
		taskRepo:    taskRepo,
		eventHub:    eventHub,
		enqueueFunc: enqueueFunc,
	}
}

func (s *SubmissionService) SubmitByInput(ctx context.Context, input string) (*model.Artwork, error) {
	illustID := ParseArtworkID(input)
	if illustID == "" {
		return nil, fmt.Errorf("invalid pixiv artwork link or id: %s", input)
	}

	work, err := s.client.FetchIllustDetail(ctx, illustID)
	if err != nil {
		return nil, fmt.Errorf("fetch illust detail failed: %w", err)
	}

	art := &model.Artwork{
		ID:         illustID,
		Title:      work.Title,
		UserID:     work.AuthorID(),
		UserName:   work.UserName,
		PageCount:  work.PageCount,
		Tags:       work.Tags,
		IllustType: work.IllustType,
		SourceType: "manual",
		Status:     model.StatusArtworkPending,
	}

	if err := s.artworkRepo.Upsert(art); err != nil {
		return nil, fmt.Errorf("save artwork to db failed: %w", err)
	}

	pages, err := s.client.FetchPages(ctx, illustID)
	if err != nil {
		return nil, fmt.Errorf("fetch pages failed: %w", err)
	}

	s.eventHub.Publish(EventArtworkDiscovered, art)

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
			// Fetch newly created task ID
			storedTasks, _ := s.taskRepo.ListByArtworkID(illustID)
			for _, t := range storedTasks {
				if t.PageIndex == idx {
					s.enqueueFunc(t.ID)
					break
				}
			}
		}
	}

	return s.artworkRepo.GetByID(illustID)
}
