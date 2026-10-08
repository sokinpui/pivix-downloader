package engine

import (
	"context"
	"log"
	"sync"
	"time"

	"pivis-downloader/internal/model"
	"pivis-downloader/internal/pixiv"
	"pivis-downloader/internal/repository"
	"pivis-downloader/internal/service"
)

type DownloadEngine struct {
	client       *pixiv.PixivClient
	taskRepo     *repository.TaskRepository
	artworkRepo  *repository.ArtworkRepository
	eventHub     *service.EventHub
	outputDir    string
	maxWorkers   int

	taskQueue    chan int64
	wg           sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
}

func NewDownloadEngine(
	client *pixiv.PixivClient,
	taskRepo *repository.TaskRepository,
	artworkRepo *repository.ArtworkRepository,
	eventHub *service.EventHub,
	outputDir string,
	maxWorkers int,
) *DownloadEngine {
	ctx, cancel := context.WithCancel(context.Background())
	if maxWorkers <= 0 {
		maxWorkers = 2
	}
	return &DownloadEngine{
		client:      client,
		taskRepo:    taskRepo,
		artworkRepo: artworkRepo,
		eventHub:    eventHub,
		outputDir:   outputDir,
		maxWorkers:  maxWorkers,
		taskQueue:   make(chan int64, 1000),
		ctx:         ctx,
		cancel:      cancel,
	}
}

func (e *DownloadEngine) Start() {
	for i := 0; i < e.maxWorkers; i++ {
		e.wg.Add(1)
		go e.worker(i + 1)
	}
}

func (e *DownloadEngine) Stop() {
	e.cancel()
	close(e.taskQueue)
	e.wg.Wait()
}

func (e *DownloadEngine) EnqueueTask(taskID int64) {
	select {
	case e.taskQueue <- taskID:
	case <-e.ctx.Done():
	}
}

func (e *DownloadEngine) worker(id int) {
	defer e.wg.Done()

	for taskID := range e.taskQueue {
		if e.ctx.Err() != nil {
			return
		}

		e.processTask(taskID)
	}
}

func (e *DownloadEngine) processTask(taskID int64) {
	task, err := e.taskRepo.GetByID(taskID)
	if err != nil || task == nil {
		log.Printf("[Worker] failed to get task %d: %v", taskID, err)
		return
	}

	if task.Status == model.StatusTaskCompleted {
		return
	}

	// Set task to downloading
	_ = e.taskRepo.UpdateStatus(task.ID, model.StatusTaskDownloading, "", 0, "")
	e.eventHub.Publish(service.EventTaskStarted, map[string]any{
		"task_id":     task.ID,
		"artwork_id":  task.ArtworkID,
		"page_index":  task.PageIndex,
	})

	// Perform download with retries (max 3 attempts)
	var savedPath string
	var fileSize int64
	var downloadErr error

	for attempt := 1; attempt <= 3; attempt++ {
		savedPath, fileSize, downloadErr = e.client.DownloadImage(e.ctx, task.ImageURL, e.outputDir)
		if downloadErr == nil {
			break
		}
		log.Printf("[Worker] Task %d (Artwork %s P%d) attempt %d failed: %v. Retrying...", task.ID, task.ArtworkID, task.PageIndex, attempt, downloadErr)
		time.Sleep(time.Duration(attempt) * 2 * time.Second)
	}

	if downloadErr != nil {
		errMsg := downloadErr.Error()
		_ = e.taskRepo.UpdateStatus(task.ID, model.StatusTaskFailed, "", 0, errMsg)
		_ = e.artworkRepo.UpdateStatus(task.ArtworkID, model.StatusArtworkFailed, errMsg)

		e.eventHub.Publish(service.EventTaskFailed, map[string]any{
			"task_id":      task.ID,
			"artwork_id":   task.ArtworkID,
			"page_index":   task.PageIndex,
			"error_message": errMsg,
		})
		return
	}

	// Success
	_ = e.taskRepo.UpdateStatus(task.ID, model.StatusTaskCompleted, savedPath, fileSize, "")
	e.eventHub.Publish(service.EventTaskCompleted, map[string]any{
		"task_id":    task.ID,
		"artwork_id": task.ArtworkID,
		"page_index": task.PageIndex,
		"file_path":  savedPath,
		"file_size":  fileSize,
	})

	// Check if all tasks for this artwork are completed
	e.checkAndUpdateArtworkCompletion(task.ArtworkID)
}

func (e *DownloadEngine) checkAndUpdateArtworkCompletion(artworkID string) {
	tasks, err := e.taskRepo.ListByArtworkID(artworkID)
	if err != nil {
		return
	}

	allCompleted := true
	anyFailed := false
	for _, t := range tasks {
		if t.Status != model.StatusTaskCompleted {
			allCompleted = false
		}
		if t.Status == model.StatusTaskFailed {
			anyFailed = true
		}
	}

	if allCompleted {
		_ = e.artworkRepo.UpdateStatus(artworkID, model.StatusArtworkCompleted, "")
	} else if anyFailed {
		_ = e.artworkRepo.UpdateStatus(artworkID, model.StatusArtworkPartial, "some pages failed")
	} else {
		_ = e.artworkRepo.UpdateStatus(artworkID, model.StatusArtworkProcessing, "")
	}
}
