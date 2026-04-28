package scraper

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"inconsistencyfixer/internal/models"
	"inconsistencyfixer/internal/story"
)

const (
	baseURL   = "https://freewebnovel.com"
	rateLimit = 2500 * time.Millisecond
	maxRetry  = 3
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

// Run scrapes all chapters from novelURL and writes them to outputDir/chapters/.
// Already-downloaded chapters are skipped, so the command is safe to re-run.
func Run(novelURL, outputDir string) error {
	chaptersDir := filepath.Join(outputDir, "chapters")
	if err := os.MkdirAll(chaptersDir, 0755); err != nil {
		return fmt.Errorf("creating chapters dir: %w", err)
	}

	log.Printf("Fetching chapter list from: %s", novelURL)
	chapterURLs, err := fetchChapterList(novelURL)
	if err != nil {
		return fmt.Errorf("fetching chapter list: %w", err)
	}
	log.Printf("Found %d chapters", len(chapterURLs))

	for i, chURL := range chapterURLs {
		chNum := i + 1
		chPath := filepath.Join(chaptersDir, fmt.Sprintf("chapter_%04d.txt", chNum))

		if _, err := os.Stat(chPath); err == nil {
			log.Printf("[%d/%d] Already downloaded, skipping", chNum, len(chapterURLs))
			continue
		}

		log.Printf("[%d/%d] %s", chNum, len(chapterURLs), chURL)

		var ch models.Chapter
		var fetchErr error
		for attempt := 1; attempt <= maxRetry; attempt++ {
			ch, fetchErr = fetchChapter(chURL, chNum)
			if fetchErr == nil {
				break
			}
			log.Printf("  Attempt %d/%d failed: %v", attempt, maxRetry, fetchErr)
			time.Sleep(time.Duration(attempt) * 4 * time.Second)
		}
		if fetchErr != nil {
			log.Printf("  Giving up on chapter %d", chNum)
			continue
		}

		if err := story.SaveChapter(chaptersDir, ch); err != nil {
			log.Printf("  Failed to save chapter %d: %v", chNum, err)
		}

		time.Sleep(rateLimit)
	}

	// Rebuild combined story.txt
	log.Println("Assembling story.txt...")
	chapters, err := story.LoadChapters(chaptersDir)
	if err != nil {
		return fmt.Errorf("loading chapters: %w", err)
	}

	storyPath := filepath.Join(outputDir, "story.txt")
	if err := story.WriteStory(storyPath, chapters); err != nil {
		return fmt.Errorf("writing story: %w", err)
	}

	log.Printf("Done. %d chapters saved to %s", len(chapters), storyPath)
	return nil
}

func fetchChapterList(novelURL string) ([]string, error) {
	doc, err := fetchDoc(novelURL)
	if err != nil {
		return nil, err
	}

	var urls []string
	doc.Find("ul#idData li a.con").Each(func(_ int, s *goquery.Selection) {
		if href, ok := s.Attr("href"); ok && href != "" {
			urls = append(urls, baseURL+href)
		}
	})

	if len(urls) == 0 {
		return nil, fmt.Errorf("no chapter links found — site structure may have changed")
	}
	return urls, nil
}

func fetchChapter(url string, num int) (models.Chapter, error) {
	doc, err := fetchDoc(url)
	if err != nil {
		return models.Chapter{}, err
	}

	title := strings.TrimSpace(doc.Find("div#article h4").First().Text())
	if title == "" {
		title = fmt.Sprintf("Chapter %d", num)
	}

	var paragraphs []string
	doc.Find("div#article p").Each(func(_ int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if text != "" {
			paragraphs = append(paragraphs, text)
		}
	})

	if len(paragraphs) == 0 {
		return models.Chapter{}, fmt.Errorf("no content found in chapter %d", num)
	}

	return models.Chapter{
		Number:  num,
		Title:   title,
		Content: strings.Join(paragraphs, "\n\n"),
	}, nil
}

func fetchDoc(url string) (*goquery.Document, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Referer", "https://freewebnovel.com/")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return nil, fmt.Errorf("access denied (403) — site may block bots; try again later")
	default:
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	return goquery.NewDocumentFromReader(resp.Body)
}
