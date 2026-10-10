package engine

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sokinpui/pixiv-downloader/internal/model"
	"github.com/sokinpui/pixiv-downloader/internal/pixiv"
	"github.com/sokinpui/pixiv-downloader/internal/repository"
	"github.com/sokinpui/pixiv-downloader/internal/service"
)

type DownloadEngine struct {
	client      *pixiv.PixivClient
	taskRepo    *repository.TaskRepository
	artworkRepo *repository.ArtworkRepository
	eventHub    *service.EventHub
	outputDir   string
	maxWorkers  int

	mu        sync.Mutex
	taskQueue chan int64
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
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
		taskQueue:   make(chan int64, 2000),
		ctx:         ctx,
		cancel:      cancel,
	}
}

func (e *DownloadEngine) Start() {
	e.mu.Lock()
	defer e.mu.Unlock()

	for i := 0; i < e.maxWorkers; i++ {
		e.wg.Add(1)
		go e.worker(i + 1)
	}
}

func (e *DownloadEngine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.cancel()
	close(e.taskQueue)
	e.wg.Wait()
}

func (e *DownloadEngine) CancelActiveDownloads() {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 1. Cancel ongoing HTTP requests in workers
	e.cancel()

	// 2. Drain channel
	for {
		select {
		case <-e.taskQueue:
		default:
			goto DRAINED
		}
	}
DRAINED:

	// 3. Recreate context and channel so engine can accept new tasks later if needed
	e.ctx, e.cancel = context.WithCancel(context.Background())
	e.taskQueue = make(chan int64, 2000)

	// 4. Restart workers
	for i := 0; i < e.maxWorkers; i++ {
		e.wg.Add(1)
		go e.worker(i + 1)
	}

	log.Println("[Engine] All active downloads and queue cancelled and cleared.")
}

func (e *DownloadEngine) EnqueueTask(taskID int64) {
	e.mu.Lock()
	q := e.taskQueue
	ctx := e.ctx
	e.mu.Unlock()

	select {
	case q <- taskID:
	case <-ctx.Done():
	}
}

func (e *DownloadEngine) worker(id int) {
	defer e.wg.Done()

	for {
		e.mu.Lock()
		q := e.taskQueue
		ctx := e.ctx
		e.mu.Unlock()

		if ctx.Err() != nil {
			return
		}

		taskID, ok := <-q
		if !ok {
			return
		}

		if ctx.Err() != nil {
			return
		}

		e.processTask(ctx, taskID)
	}
}

func (e *DownloadEngine) processTask(ctx context.Context, taskID int64) {
	task, err := e.taskRepo.GetByID(taskID)
	if err != nil || task == nil {
		return
	}

	if task.Status == model.StatusTaskCompleted {
		return
	}

	_ = e.taskRepo.UpdateStatus(task.ID, model.StatusTaskDownloading, "", 0, "")
	e.eventHub.Publish(service.EventTaskStarted, map[string]any{
		"task_id":    task.ID,
		"artwork_id": task.ArtworkID,
		"page_index": task.PageIndex,
	})

	var savedPath string
	var fileSize int64
	var downloadErr error
	dirName := task.ArtworkID
	if art, err := e.artworkRepo.GetByID(task.ArtworkID); err == nil && art != nil {
		if sanitizedTitle := sanitizeDirectoryName(art.Title); sanitizedTitle != "" {
			dirName = fmt.Sprintf("%s-%s", task.ArtworkID, sanitizedTitle)
		}
	}
	artworkDir := filepath.Join(e.outputDir, dirName)

	for attempt := 1; attempt <= 3; attempt++ {
		if ctx.Err() != nil {
			return
		}
		savedPath, fileSize, downloadErr = e.client.DownloadImage(ctx, task.ImageURL, artworkDir)
		if downloadErr == nil {
			break
		}
		time.Sleep(time.Duration(attempt) * 1 * time.Second)
	}

	if downloadErr != nil {
		if ctx.Err() != nil {
			// Cancelled
			_ = e.taskRepo.UpdateStatus(task.ID, model.StatusTaskPending, "", 0, "")
			return
		}
		errMsg := downloadErr.Error()
		_ = e.taskRepo.UpdateStatus(task.ID, model.StatusTaskFailed, "", 0, errMsg)
		_ = e.artworkRepo.UpdateStatus(task.ArtworkID, model.StatusArtworkFailed, errMsg)

		e.eventHub.Publish(service.EventTaskFailed, map[string]any{
			"task_id":       task.ID,
			"artwork_id":    task.ArtworkID,
			"page_index":    task.PageIndex,
			"error_message": errMsg,
		})
		return
	}

	_ = e.taskRepo.UpdateStatus(task.ID, model.StatusTaskCompleted, savedPath, fileSize, "")
	e.eventHub.Publish(service.EventTaskCompleted, map[string]any{
		"task_id":    task.ID,
		"artwork_id": task.ArtworkID,
		"page_index": task.PageIndex,
		"file_path":  savedPath,
		"file_size":  fileSize,
	})

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

func sanitizeDirectoryName(name string) string {
	const invalidChars = `/\:*?"<>|`
	var b strings.Builder
	for _, r := range name {
		if strings.ContainsRune(invalidChars, r) || r < 32 {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	res := strings.Trim(strings.TrimSpace(b.String()), ".")
	if runes := []rune(res); len(runes) > 100 {
		res = strings.TrimSpace(string(runes[:100]))
	}
	return res
}
