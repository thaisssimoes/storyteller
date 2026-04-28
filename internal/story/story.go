package story

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"inconsistencyfixer/internal/models"
)

var chapterHeaderRe = regexp.MustCompile(`^=== Chapter (\d+):(.+) ===$`)

// SaveChapter writes a single chapter to dir/chapter_NNNN.txt.
func SaveChapter(dir string, ch models.Chapter) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("chapter_%04d.txt", ch.Number))
	content := fmt.Sprintf("=== Chapter %d: %s ===\n\n%s\n", ch.Number, ch.Title, ch.Content)
	return os.WriteFile(path, []byte(content), 0644)
}

// LoadChapters reads all chapter_NNNN.txt files from dir, sorted by number.
func LoadChapters(dir string) ([]models.Chapter, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var chapters []models.Chapter
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		ch, err := parseChapterFile(path, string(data))
		if err != nil {
			continue
		}
		chapters = append(chapters, ch)
	}

	sort.Slice(chapters, func(i, j int) bool {
		return chapters[i].Number < chapters[j].Number
	})
	return chapters, nil
}

// WriteStory combines all chapters into a single story.txt file.
func WriteStory(path string, chapters []models.Chapter) error {
	return os.WriteFile(path, []byte(CombineChapters(chapters)), 0644)
}

// CombineChapters joins chapters into a single string with separator headers.
func CombineChapters(chapters []models.Chapter) string {
	var sb strings.Builder
	for i, ch := range chapters {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(fmt.Sprintf("=== Chapter %d: %s ===\n\n%s", ch.Number, ch.Title, ch.Content))
	}
	return sb.String()
}

func parseChapterFile(path, raw string) (models.Chapter, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")

	newline := strings.Index(raw, "\n")
	if newline < 0 {
		return models.Chapter{}, fmt.Errorf("no newline in %s", path)
	}

	header := strings.TrimSpace(raw[:newline])
	body := strings.TrimSpace(raw[newline:])

	m := chapterHeaderRe.FindStringSubmatch(header)
	if m == nil {
		return models.Chapter{}, fmt.Errorf("unrecognised header %q in %s", header, path)
	}

	num, _ := strconv.Atoi(m[1])
	title := strings.TrimSpace(m[2])

	return models.Chapter{
		Number:  num,
		Title:   title,
		Content: body,
		Path:    path,
	}, nil
}
