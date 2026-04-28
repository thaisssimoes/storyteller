package reader

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"inconsistencyfixer/internal/claude"
	"inconsistencyfixer/internal/models"
	"inconsistencyfixer/internal/story"
)

const maxTokens = 8192

// Run analyses all chapters and writes an inconsistency report to outputDir.
func Run(outputDir string, client *claude.Client) error {
	chaptersDir := filepath.Join(outputDir, "chapters")
	reportPath := filepath.Join(outputDir, "inconsistencies.json")
	humanPath := filepath.Join(outputDir, "inconsistencies_report.txt")

	chapters, err := story.LoadChapters(chaptersDir)
	if err != nil {
		return fmt.Errorf("loading chapters: %w", err)
	}
	if len(chapters) == 0 {
		return fmt.Errorf("no chapters found in %s — run 'scrape' first", chaptersDir)
	}

	batchSize := client.BatchSize()
	log.Printf("Analysing %d chapters in batches of %d (provider: %s)...", len(chapters), batchSize, client.Provider())

	var bible models.WorldBible
	var allInc []models.Inconsistency
	nextID := 1

	for i := 0; i < len(chapters); i += batchSize {
		end := i + batchSize
		if end > len(chapters) {
			end = len(chapters)
		}
		batch := chapters[i:end]

		log.Printf("  Batch: chapters %d–%d / %d", batch[0].Number, batch[len(batch)-1].Number, len(chapters))

		var updatedBible models.WorldBible
		var newInc []models.Inconsistency
		var callErr error

		if i == 0 {
			updatedBible, newInc, callErr = analyseFirstBatch(client, batch)
		} else {
			updatedBible, newInc, callErr = analyseNextBatch(client, bible, batch)
		}

		if callErr != nil {
			log.Printf("  Warning: batch failed: %v", callErr)
			continue
		}

		bible = updatedBible
		for _, inc := range newInc {
			inc.ID = nextID
			nextID++
			allInc = append(allInc, inc)
		}
		log.Printf("  Found %d new inconsistencies (total: %d)", len(newInc), len(allInc))
	}

	report := models.Report{WorldBible: bible, Inconsistencies: allInc}

	// Write JSON report
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling report: %w", err)
	}
	if err := os.WriteFile(reportPath, data, 0644); err != nil {
		return fmt.Errorf("writing JSON report: %w", err)
	}

	// Write human-readable report
	if err := os.WriteFile(humanPath, []byte(buildTextReport(report)), 0644); err != nil {
		log.Printf("Warning: could not write text report: %v", err)
	}

	log.Printf("Analysis complete — %d inconsistencies found", len(allInc))
	log.Printf("  JSON:  %s", reportPath)
	log.Printf("  Text:  %s", humanPath)
	return nil
}

// --- batch analysis ---

func analyseFirstBatch(client *claude.Client, chapters []models.Chapter) (models.WorldBible, []models.Inconsistency, error) {
	prompt := fmt.Sprintf(`You are analysing a web novel for inconsistencies. Read the chapters below carefully.

Your tasks:
1. Build a "World Bible" documenting ALL established facts:
   - Characters: full name, aliases/nicknames, physical description, personality, role, status, relationships
   - Locations: name, description, significance
   - World mechanics: magic systems, special abilities, supernatural rules
   - Key plot events with chapter numbers

2. Identify ANY inconsistencies you find:
   - Character descriptions that change (eye colour, hair, age, title)
   - Names used inconsistently
   - Events that contradict each other
   - Characters knowing things they could not know
   - Timeline impossibilities
   - Relationship dynamics that shift without cause

Respond with ONLY valid JSON — no markdown, no explanation, no preamble:
{
  "worldBible": {
    "characters": [
      {
        "name": "string",
        "aliases": ["string"],
        "description": "physical appearance and personality",
        "role": "protagonist|antagonist|supporting",
        "relationships": ["relationship descriptions"],
        "status": "alive|dead|missing|unknown",
        "firstAppearance": 1
      }
    ],
    "locations": [{"name": "string", "description": "string"}],
    "worldMechanics": ["string"],
    "plotEvents": [{"chapter": 1, "description": "string"}]
  },
  "inconsistencies": [
    {
      "chapter": 1,
      "type": "character_description|character_name|plot_continuity|relationship|timeline|setting|ability|other",
      "severity": "high|medium|low",
      "description": "Clear, specific description of the inconsistency",
      "conflictingFacts": ["fact from chapter X: quote", "contradicting fact from chapter Y: quote"],
      "originalText": "The exact problematic text quoted from the chapter",
      "suggestedFix": "Specific suggestion for how to resolve this"
    }
  ]
}

CHAPTERS:
%s`, renderChapters(chapters))

	resp, err := client.Complete(context.Background(), maxTokens, []claude.Message{
		claude.UserMessage(claude.TextBlock(prompt)),
	})
	if err != nil {
		return models.WorldBible{}, nil, err
	}
	return parseResponse(resp)
}

func analyseNextBatch(client *claude.Client, bible models.WorldBible, chapters []models.Chapter) (models.WorldBible, []models.Inconsistency, error) {
	bibleJSON, _ := json.MarshalIndent(bible, "", "  ")

	continuationPrompt := fmt.Sprintf(`Using the World Bible provided, analyse these new chapters (starting at chapter %d):

1. Update the World Bible with any new information
2. Identify inconsistencies between these chapters and the established World Bible, or within the new chapters themselves

Respond with ONLY valid JSON:
{
  "worldBible": { /* complete updated world bible */ },
  "inconsistencies": [
    {
      "chapter": %d,
      "type": "character_description|character_name|plot_continuity|relationship|timeline|setting|ability|other",
      "severity": "high|medium|low",
      "description": "string",
      "conflictingFacts": ["string"],
      "originalText": "string",
      "suggestedFix": "string"
    }
  ]
}

NEW CHAPTERS:
%s`, chapters[0].Number, chapters[0].Number, renderChapters(chapters))

	resp, err := client.Complete(context.Background(), maxTokens, []claude.Message{
		claude.UserMessage(
			claude.CachedTextBlock(fmt.Sprintf("WORLD BIBLE (established facts from chapters read so far):\n%s", string(bibleJSON))),
			claude.TextBlock(continuationPrompt),
		),
	})
	if err != nil {
		return bible, nil, err
	}
	return parseResponse(resp)
}

// --- helpers ---

func renderChapters(chapters []models.Chapter) string {
	parts := make([]string, len(chapters))
	for i, ch := range chapters {
		parts[i] = fmt.Sprintf("=== Chapter %d: %s ===\n\n%s", ch.Number, ch.Title, ch.Content)
	}
	return strings.Join(parts, "\n\n---\n\n")
}

func parseResponse(resp string) (models.WorldBible, []models.Inconsistency, error) {
	jsonStr := extractJSON(resp)

	var result struct {
		WorldBible      models.WorldBible      `json:"worldBible"`
		Inconsistencies []models.Inconsistency `json:"inconsistencies"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		preview := jsonStr
		if len(preview) > 600 {
			preview = preview[:600] + "..."
		}
		return models.WorldBible{}, nil, fmt.Errorf("JSON parse error: %w\nResponse start: %s", err, preview)
	}
	return result.WorldBible, result.Inconsistencies, nil
}

// extractJSON strips markdown fences and finds the outermost JSON object.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)

	// Remove ``` fences
	if strings.HasPrefix(s, "```") {
		if nl := strings.Index(s, "\n"); nl >= 0 {
			s = s[nl+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}

	// Find outermost { ... }
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		s = s[start : end+1]
	}

	return s
}

// --- human-readable report ---

func buildTextReport(report models.Report) string {
	var sb strings.Builder

	sb.WriteString("=== INCONSISTENCY REPORT ===\n\n")
	total := len(report.Inconsistencies)
	sb.WriteString(fmt.Sprintf("Total inconsistencies: %d\n", total))

	if total == 0 {
		sb.WriteString("No inconsistencies found!\n")
		return sb.String()
	}

	high := countBySeverity(report.Inconsistencies, "high")
	medium := countBySeverity(report.Inconsistencies, "medium")
	low := countBySeverity(report.Inconsistencies, "low")
	sb.WriteString(fmt.Sprintf("  High:   %d\n  Medium: %d\n  Low:    %d\n\n", high, medium, low))

	sb.WriteString("=== DETAILS ===\n\n")
	for _, inc := range report.Inconsistencies {
		sb.WriteString(fmt.Sprintf("--- #%d  Chapter %d  [%s]  [%s] ---\n",
			inc.ID, inc.Chapter, inc.Type, strings.ToUpper(inc.Severity)))
		sb.WriteString(fmt.Sprintf("Description:   %s\n", inc.Description))
		if len(inc.ConflictingFacts) > 0 {
			sb.WriteString("Conflicts:\n")
			for _, f := range inc.ConflictingFacts {
				sb.WriteString(fmt.Sprintf("  • %s\n", f))
			}
		}
		if inc.OriginalText != "" {
			sb.WriteString(fmt.Sprintf("Original text: %q\n", inc.OriginalText))
		}
		sb.WriteString(fmt.Sprintf("Suggested fix: %s\n\n", inc.SuggestedFix))
	}

	sb.WriteString("=== WORLD BIBLE SUMMARY ===\n\n")
	sb.WriteString(fmt.Sprintf("Characters (%d):\n", len(report.WorldBible.Characters)))
	for _, c := range report.WorldBible.Characters {
		sb.WriteString(fmt.Sprintf("  • %s (%s, %s)\n    %s\n", c.Name, c.Role, c.Status, c.Description))
	}
	sb.WriteString(fmt.Sprintf("\nLocations (%d):\n", len(report.WorldBible.Locations)))
	for _, l := range report.WorldBible.Locations {
		sb.WriteString(fmt.Sprintf("  • %s: %s\n", l.Name, l.Description))
	}

	return sb.String()
}

func countBySeverity(incs []models.Inconsistency, s string) int {
	n := 0
	for _, inc := range incs {
		if inc.Severity == s {
			n++
		}
	}
	return n
}
