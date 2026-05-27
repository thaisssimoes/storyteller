# Storyteller

A Go CLI that detects and fixes worldbuilding inconsistencies in web novels, and can also write a fresh story from scratch and run the same consistency pipeline on the result.

Supports two LLM providers:

- **Anthropic Claude** (default)
- **Ollama** (local models)

Uses a two-tier model setup: a fast model for per-chapter scans and a robust model for macro analysis, fixer rewrites, and chapter writing.

## Commands

| Command  | What it does |
|---|---|
| `scrape` | Download all chapters from a chapter-list URL into `output/chapters/` |
| `read`   | Analyse chapters in three passes (macro / scene / continuity) into `output/inconsistencies.json` |
| `fix`    | Rewrite inconsistent chapters into `output/story_fixed.txt` |
| `write`  | Interactive interview, write an original story from scratch, then run read + fix on the result |
| `all`    | Run scrape, read, and fix in sequence. Each step is skipped if its output is already on disk (set `FORCE=1` to redo). |

## Setup

Requirements:

- Go 1.22 or higher
- An Anthropic API key, or a local Ollama install

```bash
git clone https://github.com/thaisssimoes/storyteller
cd storyteller
go mod download
go build
```

## Configuration

The CLI reads from a `.env` file in the current directory.

```env
NOVEL_URL=https://example.com/novel/chapters
OUTPUT_DIR=./output

PROVIDER=anthropic
ANTHROPIC_API_KEY=sk-ant-...

# Optional model overrides
ANTHROPIC_FAST_MODEL=
ANTHROPIC_ROBUST_MODEL=

# Ollama
OLLAMA_FAST_MODEL=qwen2.5:14b
OLLAMA_ROBUST_MODEL=gemma2:27b
OLLAMA_URL=http://localhost:11434
OLLAMA_NUM_CTX=16384
```

## Recommended Ollama model pairs (by VRAM)

| VRAM | Fast | Robust | Time on 94 chapters |
|---|---|---|---|
| 8 GB | qwen2.5:7b | gemma2:9b | ~2 h |
| 12 GB | qwen2.5:7b | qwen2.5:14b | ~3 h |
| 16 GB | qwen2.5:14b | gemma2:27b | ~5 h |
| 24 GB+ | qwen2.5:14b | gemma2:27b | ~2 h |

Using a model larger than your VRAM forces CPU offload and runs 5 to 10 times slower.

## Architecture

```
internal/
├── scraper/   Download chapters from a URL
├── reader/    Three-pass analysis: macro / scene / continuity
├── fixer/     Rewrite inconsistent passages
├── writer/    Interactive writer (story from scratch)
├── models/    WorldBible, Character, Chapter, etc.
└── claude/    Provider abstraction (Anthropic / Ollama), Fast/Robust pair
```

The `scripts/` directory contains Python utilities for packaging the output as EPUB, PDF, and DOCX, plus a few one-off helpers for renumbering and renaming chapter files.

## License

MIT
