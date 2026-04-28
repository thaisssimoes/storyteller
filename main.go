package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"

	"inconsistencyfixer/internal/claude"
	"inconsistencyfixer/internal/fixer"
	"inconsistencyfixer/internal/reader"
	"inconsistencyfixer/internal/scraper"
	"inconsistencyfixer/internal/writer"
)

const help = `InconsistencyFixer — Web Novel Inconsistency Detector, Fixer and Writer

Usage:
  inconsistencyfixer <command>

Commands:
  write    Entrevista interativa, escreve uma história original do zero, e roda
           o leitor + fixer sobre o resultado antes de entregar a versão final
  scrape   Download all chapters from NOVEL_URL → output/chapters/
  read     Analyse chapters in three passes (macro / scene / continuity)
           → output/inconsistencies.json
  fix      Rewrite inconsistent chapters → output/story_fixed.txt
  all      Run scrape → read → fix in sequence. Each step is skipped if its
           output is already present (delete the file/folder to force redo).

Two-tier model setup (defaults baked in):
  - Fast model handles per-chapter scene/continuity scans, outline chunk
    extension. Optimised for structured JSON output.
  - Robust model handles the macro analysis, fixer rewrites, and chapter
    writing. Optimised for quality.

.env variables:
  NOVEL_URL                  Chapter-list page URL (for scrape/all)
  OUTPUT_DIR                 Where to store files (default: ./output)

  PROVIDER                   "anthropic" (default) or "ollama"
  FORCE                      "1" to ignore skip-if-done in 'all'

  # Anthropic
  ANTHROPIC_API_KEY          required when PROVIDER=anthropic
  ANTHROPIC_FAST_MODEL       default: ` + claude.DefaultAnthropicFast + `
  ANTHROPIC_ROBUST_MODEL     default: ` + claude.DefaultAnthropicRobust + `

  # Ollama
  OLLAMA_FAST_MODEL          default: ` + claude.DefaultOllamaFast + `
  OLLAMA_ROBUST_MODEL        default: ` + claude.DefaultOllamaRobust + `
  OLLAMA_MODEL               legacy override — sets BOTH fast and robust to
                             the same model (kept for backward compatibility)
  OLLAMA_URL                 default: http://localhost:11434
  OLLAMA_NUM_CTX             default: 16384 (recommend 32768+ for the reader)
`

func main() {
	log.SetFlags(0)

	if err := godotenv.Load(); err != nil {
		log.Println("Note: no .env file found — reading from environment")
	}

	if len(os.Args) < 2 {
		fmt.Print(help)
		os.Exit(1)
	}

	outputDir := os.Getenv("OUTPUT_DIR")
	if outputDir == "" {
		outputDir = "./output"
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("Cannot create output dir: %v", err)
	}

	switch os.Args[1] {

	case "write":
		if err := writer.Run(outputDir, buildPair()); err != nil {
			log.Fatalf("Write error: %v", err)
		}

	case "scrape":
		if err := scraper.Run(requireEnv("NOVEL_URL"), outputDir); err != nil {
			log.Fatalf("Scrape error: %v", err)
		}

	case "read":
		if err := reader.Run(outputDir, buildPair()); err != nil {
			log.Fatalf("Read error: %v", err)
		}

	case "fix":
		if err := fixer.Run(outputDir, buildPair()); err != nil {
			log.Fatalf("Fix error: %v", err)
		}

	case "all":
		runAll(outputDir, buildPair())

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %q\n\n", os.Args[1])
		fmt.Print(help)
		os.Exit(1)
	}
}

// runAll executes scrape → read → fix in order, but skips any step whose
// expected output is already on disk. Set FORCE=1 to redo everything.
func runAll(outputDir string, pair *claude.Pair) {
	force := os.Getenv("FORCE") == "1"

	chaptersDir := filepath.Join(outputDir, "chapters")
	reportPath := filepath.Join(outputDir, "inconsistencies.json")
	fixedPath := filepath.Join(outputDir, "story_fixed.txt")

	// --- Step 1: scrape ---
	log.Println("=== Step 1/3: Scraping ===")
	if !force && hasChapters(chaptersDir) {
		n := countChapters(chaptersDir)
		log.Printf("  ↺ Skipping — %d capítulo(s) já em %s (use FORCE=1 ou apague a pasta para baixar de novo)", n, chaptersDir)
	} else {
		if err := scraper.Run(requireEnv("NOVEL_URL"), outputDir); err != nil {
			log.Fatalf("Scrape error: %v", err)
		}
	}

	// --- Step 2: read ---
	log.Println("\n=== Step 2/3: Analysing ===")
	if !force && fileExists(reportPath) {
		log.Printf("  ↺ Skipping — relatório já em %s (use FORCE=1 ou apague o arquivo para reanalisar)", reportPath)
	} else {
		if err := reader.Run(outputDir, pair); err != nil {
			log.Fatalf("Read error: %v", err)
		}
	}

	// --- Step 3: fix ---
	log.Println("\n=== Step 3/3: Fixing ===")
	if !force && fileExists(fixedPath) {
		log.Printf("  ↺ Skipping — story_fixed.txt já existe (use FORCE=1 ou apague para refazer)")
	} else {
		if err := fixer.Run(outputDir, pair); err != nil {
			log.Fatalf("Fix error: %v", err)
		}
	}

	log.Println("\nAll done! Files are in", outputDir)
}

// hasChapters returns true if the chapters dir exists and contains at least
// one chapter_NNNN.txt file.
func hasChapters(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "chapter_") && strings.HasSuffix(name, ".txt") {
			return true
		}
	}
	return false
}

func countChapters(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "chapter_") && strings.HasSuffix(name, ".txt") {
			n++
		}
	}
	return n
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// buildPair builds the Fast/Robust model pair from environment variables, with
// sensible defaults baked in. Backward compatible with the old single-model env.
func buildPair() *claude.Pair {
	provider := os.Getenv("PROVIDER")
	if provider == "" {
		provider = "anthropic"
	}

	switch provider {
	case "ollama":
		fastModel := os.Getenv("OLLAMA_FAST_MODEL")
		robustModel := os.Getenv("OLLAMA_ROBUST_MODEL")
		// Legacy: if OLLAMA_MODEL is set and the new vars are not, use it for both.
		if legacy := os.Getenv("OLLAMA_MODEL"); legacy != "" {
			if fastModel == "" {
				fastModel = legacy
			}
			if robustModel == "" {
				robustModel = legacy
			}
		}
		baseURL := os.Getenv("OLLAMA_URL")
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		numCtx, _ := strconv.Atoi(os.Getenv("OLLAMA_NUM_CTX"))

		pair := claude.NewOllamaPair(fastModel, robustModel, baseURL, numCtx)
		log.Printf("Using Ollama: fast=%s robust=%s url=%s",
			pair.Fast.Model(), pair.Robust.Model(), baseURL)
		return pair

	default:
		fastModel := os.Getenv("ANTHROPIC_FAST_MODEL")
		robustModel := os.Getenv("ANTHROPIC_ROBUST_MODEL")
		pair := claude.NewAnthropicPair(requireEnv("ANTHROPIC_API_KEY"), fastModel, robustModel)
		log.Printf("Using Anthropic: fast=%s robust=%s",
			pair.Fast.Model(), pair.Robust.Model())
		return pair
	}
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("%s is not set — add it to your .env file", key)
	}
	return v
}
