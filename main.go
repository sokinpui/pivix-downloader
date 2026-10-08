package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"
)

func main() {
	sessionID := flag.String("session", "83027400_4AolHBitIh2q27HPRU5orhmt8nnWopUy", "Pixiv PHPSESSID")
	userID := flag.String("user-id", "83027400", "Pixiv User ID")
	limit := flag.Int("limit", 1, "Number of bookmarks to process")
	proxyAddr := flag.String("proxy", "", "Optional HTTP/SOCKS5 proxy, e.g. http://127.0.0.1:7890")
	outputDir := flag.String("output-dir", "./downloads", "Directory to save downloaded artworks")
	timeout := flag.Duration("timeout", 45*time.Second, "Network timeout duration")
	flag.Parse()

	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		log.Fatalf("failed to create output dir: %v", err)
	}

	client, err := NewPixivClient(*sessionID, *proxyAddr, *timeout)
	if err != nil {
		log.Fatalf("failed to initialize client: %v", err)
	}

	ctx := context.Background()

	fmt.Printf("[1/3] Fetching bookmarks for user %s...\n", *userID)
	works, err := client.FetchBookmarks(ctx, *userID, 0, *limit)
	if err != nil {
		log.Fatalf("bookmarks request failed: %v", err)
	}

	if len(works) == 0 {
		fmt.Println("no bookmarks found.")
		return
	}

	fmt.Printf("found %d bookmark(s). Starting validation...\n\n", len(works))

	for i, work := range works {
		illustID := work.ArtworkID()
		fmt.Printf("[%d/%d] Processing Illust ID: %s | Title: %s\n", i+1, len(works), illustID, work.Title)

		pages, err := client.FetchPages(ctx, illustID)
		if err != nil {
			log.Printf("failed to fetch pages for %s: %v", illustID, err)
			continue
		}

		if len(pages) == 0 {
			log.Printf("no pages found for %s", illustID)
			continue
		}

		for pageIdx, page := range pages {
			origURL := page.Urls.Original
			if origURL == "" {
				log.Printf("artwork %s page %d has empty original url", illustID, pageIdx)
				continue
			}

			fmt.Printf("      Downloading Page %d: %s\n", pageIdx, origURL)
			savedPath, bytesCount, err := client.DownloadImage(ctx, origURL, *outputDir)
			if err != nil {
				log.Printf("      download failed: %v", err)
				continue
			}

			fmt.Printf("      ✓ Saved: %s (%.2f MB)\n", savedPath, float64(bytesCount)/(1024*1024))
		}
	}

	fmt.Println("\nVerification complete.")
}
