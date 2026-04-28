package main

import (
	"fmt"
	"log"
	"os"
	"strconv"

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
  write    Entrevista interativa e escreve uma história original do zero
  scrape   Download all chapters from NOVEL_URL → output/chapters/
  read     Analyse chapters with AI  → output/inconsistencies.json
  fix      Rewrite inconsistent chapters → output/story_fixed.txt
  all      Run scrape → read → fix in sequence

.env variables:
  NOVEL_URL          Chapter-list page URL
  OUTPUT_DIR         Where to store files (default: ./output)

  # Anthropic (default)
  PROVIDER           "anthropic" (default) or "ollama"
  ANTHROPIC_API_KEY  Required when PROVIDER=anthropic

  # Ollama
  PROVIDER           Set to "ollama"
  OLLAMA_MODEL       Model name (default: gemma2:27b)
  OLLAMA_URL         Server URL (default: http://localhost:11434)
  OLLAMA_NUM_CTX     Context window size (default: 16384)
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
		if err := writer.Run(outputDir, buildClient()); err != nil {
			log.Fatalf("Write error: %v", err)
		}

	case "scrape":
		if err := scraper.Run(requireEnv("NOVEL_URL"), outputDir); err != nil {
			log.Fatalf("Scrape error: %v", err)
		}

	case "read":
		if err := reader.Run(outputDir, buildClient()); err != nil {
			log.Fatalf("Read error: %v", err)
		}

	case "fix":
		if err := fixer.Run(outputDir, buildClient()); err != nil {
			log.Fatalf("Fix error: %v", err)
		}

	case "all":
		novelURL := requireEnv("NOVEL_URL")
		client := buildClient()

		log.Println("=== Step 1/3: Scraping ===")
		if err := scraper.Run(novelURL, outputDir); err != nil {
			log.Fatalf("Scrape error: %v", err)
		}

		log.Println("\n=== Step 2/3: Analysing ===")
		if err := reader.Run(outputDir, client); err != nil {
			log.Fatalf("Read error: %v", err)
		}

		log.Println("\n=== Step 3/3: Fixing ===")
		if err := fixer.Run(outputDir, client); err != nil {
			log.Fatalf("Fix error: %v", err)
		}

		log.Println("\nAll done! Files are in", outputDir)

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %q\n\n", os.Args[1])
		fmt.Print(help)
		os.Exit(1)
	}
}

// buildClient creates the right AI client based on the PROVIDER env var.
func buildClient() *claude.Client {
	provider := os.Getenv("PROVIDER")
	if provider == "" {
		provider = "anthropic"
	}

	switch provider {
	case "ollama":
		model := os.Getenv("OLLAMA_MODEL")
		if model == "" {
			model = "gemma2:27b"
		}
		numCtx, _ := strconv.Atoi(os.Getenv("OLLAMA_NUM_CTX"))
		log.Printf("Using Ollama: model=%s url=%s", model, ollamaURL())
		return claude.NewOllama(model, ollamaURL(), numCtx)

	default:
		log.Println("Using Anthropic API")
		return claude.New(requireEnv("ANTHROPIC_API_KEY"))
	}
}

func ollamaURL() string {
	if u := os.Getenv("OLLAMA_URL"); u != "" {
		return u
	}
	return "http://localhost:11434"
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("%s is not set — add it to your .env file", key)
	}
	return v
}
