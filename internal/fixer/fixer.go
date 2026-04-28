package fixer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"inconsistencyfixer/internal/claude"
	"inconsistencyfixer/internal/models"
	"inconsistencyfixer/internal/story"
)

const (
	maxTokens   = 8192
	fixTimeout  = 15 * time.Minute
)

// Run fixes all inconsistent chapters and writes story_fixed.txt to outputDir.
// Always uses the Robust model — rewriting prose without losing the author's
// voice is exactly the kind of work the cheap model does poorly.
func Run(outputDir string, pair *claude.Pair) error {
	return RunDir(outputDir, "chapters", pair)
}

// RunDir lets the writer point the fixer at a different chapters subdirectory.
func RunDir(outputDir, chaptersSubdir string, pair *claude.Pair) error {
	client := pair.Robust
	log.Printf("Fixer: provider=%s model=%s", client.Provider(), client.Model())
	chaptersDir := filepath.Join(outputDir, chaptersSubdir)
	fixedDir := filepath.Join(outputDir, "chapters_fixed")
	reportPath := filepath.Join(outputDir, "inconsistencies.json")
	storyFixedPath := filepath.Join(outputDir, "story_fixed.txt")

	if err := os.MkdirAll(fixedDir, 0755); err != nil {
		return fmt.Errorf("creating chapters_fixed dir: %w", err)
	}

	// Load report
	reportData, err := os.ReadFile(reportPath)
	if err != nil {
		return fmt.Errorf("reading report (run 'read' first): %w", err)
	}
	var report models.Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		return fmt.Errorf("parsing report: %w", err)
	}

	// Load original chapters
	chapters, err := story.LoadChapters(chaptersDir)
	if err != nil {
		return fmt.Errorf("loading chapters: %w", err)
	}

	if len(report.Inconsistencies) == 0 {
		log.Println("No inconsistencies to fix — writing story as-is")
		return story.WriteStory(storyFixedPath, chapters)
	}

	log.Printf("Fixing %d inconsistencies...", len(report.Inconsistencies))

	// Group inconsistencies by chapter
	byChapter := make(map[int][]models.Inconsistency)
	for _, inc := range report.Inconsistencies {
		byChapter[inc.Chapter] = append(byChapter[inc.Chapter], inc)
	}

	// Index chapters by number
	chapterMap := make(map[int]models.Chapter, len(chapters))
	for _, ch := range chapters {
		chapterMap[ch.Number] = ch
	}

	// Copy all originals to fixedDir first; fixed ones will overwrite
	for _, ch := range chapters {
		if err := story.SaveChapter(fixedDir, ch); err != nil {
			log.Printf("Warning: could not copy chapter %d: %v", ch.Number, err)
		}
	}

	bibleJSON, _ := json.MarshalIndent(report.WorldBible, "", "  ")
	fixedCount := 0

	for chNum, incs := range byChapter {
		ch, ok := chapterMap[chNum]
		if !ok {
			log.Printf("Warning: chapter %d not found in downloaded chapters", chNum)
			continue
		}

		log.Printf("  Fixing chapter %d (%d issue(s))...", chNum, len(incs))

		fixedContent, err := fixChapter(client, string(bibleJSON), ch, incs)
		if err != nil {
			log.Printf("  Warning: failed to fix chapter %d: %v", chNum, err)
			continue
		}

		fixedCh := models.Chapter{Number: ch.Number, Title: ch.Title, Content: fixedContent}
		if err := story.SaveChapter(fixedDir, fixedCh); err != nil {
			log.Printf("  Warning: could not save fixed chapter %d: %v", chNum, err)
			continue
		}
		fixedCount++
	}

	// Assemble final fixed story
	fixedChapters, err := story.LoadChapters(fixedDir)
	if err != nil {
		return fmt.Errorf("loading fixed chapters: %w", err)
	}
	if err := story.WriteStory(storyFixedPath, fixedChapters); err != nil {
		return fmt.Errorf("writing fixed story: %w", err)
	}

	log.Printf("Fixed %d chapters", fixedCount)
	log.Printf("Fixed story → %s", storyFixedPath)
	return nil
}

func fixChapter(client *claude.Client, bibleJSON string, ch models.Chapter, incs []models.Inconsistency) (string, error) {
	incList := formatInconsistencies(incs)

	fixPrompt := fmt.Sprintf(`You are a professional editor fixing inconsistencies in a chapter of a web novel.

CHAPTER %d: %s

INCONSISTENCIES TO FIX:
%s

ORIGINAL CHAPTER TEXT:
%s

Rewrite the chapter fixing ONLY the listed inconsistencies. Rules:
- Keep the author's writing style exactly as-is
- Keep all plot events, structure, and dialogue intact
- Make the smallest possible change that resolves each inconsistency
- Do NOT introduce new problems

Return ONLY the fixed chapter body text — no chapter title/header line, no commentary.`,
		ch.Number, ch.Title, incList, ch.Content)

	ctx, cancel := context.WithTimeout(context.Background(), fixTimeout)
	defer cancel()
	resp, err := client.Complete(ctx, maxTokens, []claude.Message{
		claude.UserMessage(
			// Cache the world bible — same across all chapter fix calls
			claude.CachedTextBlock(fmt.Sprintf("WORLD BIBLE (use this as the source of truth for all facts):\n%s", bibleJSON)),
			claude.TextBlock(fixPrompt),
		),
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp), nil
}

func formatInconsistencies(incs []models.Inconsistency) string {
	parts := make([]string, len(incs))
	for i, inc := range incs {
		facts := strings.Join(inc.ConflictingFacts, "\n     → ")
		parts[i] = fmt.Sprintf("%d. [%s | %s]\n   Problem: %s\n   Facts:   %s\n   Fix:     %s",
			i+1, inc.Type, strings.ToUpper(inc.Severity),
			inc.Description, facts, inc.SuggestedFix)
	}
	return strings.Join(parts, "\n\n")
}
