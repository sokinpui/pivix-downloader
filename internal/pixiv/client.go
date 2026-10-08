package pixiv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type BookmarkWork struct {
	ID         json.RawMessage `json:"id"`
	Title      string          `json:"title"`
	UserID     json.RawMessage `json:"userId"`
	UserName   string          `json:"userName"`
	PageCount  int             `json:"pageCount"`
	IllustType int             `json:"illustType"`
}

func (w BookmarkWork) ArtworkID() string {
	raw := strings.TrimSpace(string(w.ID))
	return strings.Trim(raw, `"`)
}

func (w BookmarkWork) AuthorID() string {
	raw := strings.TrimSpace(string(w.UserID))
	return strings.Trim(raw, `"`)
}

type BookmarksResponse struct {
	Error   bool   `json:"error"`
	Message string `json:"message"`
	Body    struct {
		Total int            `json:"total"`
		Works []BookmarkWork `json:"works"`
	} `json:"body"`
}

type PageInfo struct {
	Urls struct {
		Original string `json:"original"`
	} `json:"urls"`
}

type PagesResponse struct {
	Error   bool       `json:"error"`
	Message string     `json:"message"`
	Body    []PageInfo `json:"body"`
}

type IllustDetailResponse struct {
	Error   bool   `json:"error"`
	Message string `json:"message"`
	Body    struct {
		ID         json.RawMessage `json:"id"`
		Title      string          `json:"title"`
		UserID     json.RawMessage `json:"userId"`
		UserName   string          `json:"userName"`
		PageCount  int             `json:"pageCount"`
		IllustType int             `json:"illustType"`
	} `json:"body"`
}

type PixivClient struct {
	httpClient *http.Client
	sessionID  string
	userAgent  string
}

func NewPixivClient(sessionID, proxyAddr string, timeout time.Duration) (*PixivClient, error) {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
	}

	if proxyAddr != "" {
		parsedProxy, err := url.Parse(proxyAddr)
		if err != nil {
			return nil, fmt.Errorf("parse proxy url: %w", err)
		}
		transport.Proxy = http.ProxyURL(parsedProxy)
	}

	return &PixivClient{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		sessionID: sessionID,
		userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
	}, nil
}

func (c *PixivClient) throttle() {
	ms := 300 + rand.Intn(300)
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

func (c *PixivClient) setCommonHeaders(req *http.Request, referer string) {
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Referer", referer)
	req.Header.Set("Cookie", fmt.Sprintf("PHPSESSID=%s", c.sessionID))
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,ja;q=0.8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
}

func (c *PixivClient) FetchBookmarks(ctx context.Context, userID string, offset, limit int) ([]BookmarkWork, error) {
	c.throttle()
	endpoint := fmt.Sprintf(
		"https://www.pixiv.net/ajax/user/%s/illusts/bookmarks?tag=&offset=%d&limit=%d&rest=show",
		userID,
		offset,
		limit,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	c.setCommonHeaders(req, fmt.Sprintf("https://www.pixiv.net/en/users/%s/bookmarks/artworks", userID))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d fetching bookmarks: %s", resp.StatusCode, string(bodyBytes))
	}

	var data BookmarksResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode bookmarks response: %w", err)
	}

	if data.Error {
		return nil, fmt.Errorf("pixiv api returned error: %s", data.Message)
	}

	return data.Body.Works, nil
}

func (c *PixivClient) FetchIllustDetail(ctx context.Context, illustID string) (*BookmarkWork, error) {
	c.throttle()
	endpoint := fmt.Sprintf("https://www.pixiv.net/ajax/illust/%s", illustID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	c.setCommonHeaders(req, fmt.Sprintf("https://www.pixiv.net/artworks/%s", illustID))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d fetching illust detail: %s", resp.StatusCode, string(bodyBytes))
	}

	var data IllustDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode illust detail response: %w", err)
	}

	if data.Error {
		return nil, fmt.Errorf("pixiv api returned error: %s", data.Message)
	}

	work := BookmarkWork{
		ID:         data.Body.ID,
		Title:      data.Body.Title,
		UserID:     data.Body.UserID,
		UserName:   data.Body.UserName,
		PageCount:  data.Body.PageCount,
		IllustType: data.Body.IllustType,
	}

	return &work, nil
}

func (c *PixivClient) FetchPages(ctx context.Context, illustID string) ([]PageInfo, error) {
	c.throttle()
	endpoint := fmt.Sprintf("https://www.pixiv.net/ajax/illust/%s/pages", illustID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	c.setCommonHeaders(req, fmt.Sprintf("https://www.pixiv.net/artworks/%s", illustID))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d fetching pages: %s", resp.StatusCode, string(bodyBytes))
	}

	var data PagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode pages response: %w", err)
	}

	if data.Error {
		return nil, fmt.Errorf("pixiv api returned error: %s", data.Message)
	}

	return data.Body, nil
}

func (c *PixivClient) DownloadImage(ctx context.Context, imgURL, outputDir string) (string, int64, error) {
	c.throttle()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err != nil {
		return "", 0, err
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Referer", "https://www.pixiv.net/")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	parsedURL, err := url.Parse(imgURL)
	if err != nil {
		return "", 0, fmt.Errorf("parse image url: %w", err)
	}

	fileName := path.Base(parsedURL.Path)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", 0, fmt.Errorf("create output dir: %w", err)
	}

	destPath := filepath.Join(outputDir, fileName)
	tmpPath := destPath + ".tmp"

	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return "", 0, fmt.Errorf("create temp file: %w", err)
	}

	written, copyErr := io.Copy(tmpFile, resp.Body)
	tmpFile.Close()

	if copyErr != nil {
		os.Remove(tmpPath)
		return "", 0, fmt.Errorf("write stream to temp file: %w", copyErr)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return "", 0, fmt.Errorf("atomic rename failed: %w", err)
	}

	return destPath, written, nil
}
