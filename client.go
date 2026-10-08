package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	PageCount  int             `json:"pageCount"`
	IllustType int             `json:"illustType"`
}

func (w BookmarkWork) ArtworkID() string {
	raw := strings.TrimSpace(string(w.ID))
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

func (c *PixivClient) FetchBookmarks(ctx context.Context, userID string, offset, limit int) ([]BookmarkWork, error) {
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

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Referer", fmt.Sprintf("https://www.pixiv.net/en/users/%s/bookmarks/artworks", userID))
	req.Header.Set("Cookie", fmt.Sprintf("PHPSESSID=%s", c.sessionID))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d fetching bookmarks", resp.StatusCode)
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

func (c *PixivClient) FetchPages(ctx context.Context, illustID string) ([]PageInfo, error) {
	endpoint := fmt.Sprintf("https://www.pixiv.net/ajax/illust/%s/pages", illustID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Referer", fmt.Sprintf("https://www.pixiv.net/artworks/%s", illustID))
	req.Header.Set("Cookie", fmt.Sprintf("PHPSESSID=%s", c.sessionID))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d fetching pages", resp.StatusCode)
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
	filePath := filepath.Join(outputDir, fileName)

	file, err := os.Create(filePath)
	if err != nil {
		return "", 0, fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	written, err := io.Copy(file, resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("write stream to file: %w", err)
	}

	return filePath, written, nil
}
