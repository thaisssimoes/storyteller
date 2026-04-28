package writer

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"inconsistencyfixer/internal/claude"
	"inconsistencyfixer/internal/fixer"
	"inconsistencyfixer/internal/models"
	"inconsistencyfixer/internal/reader"
	"inconsistencyfixer/internal/story"
)

//go:embed writer_prompt.md
var systemPrompt string

const (
	outlineMaxTokens = 12288
	chapterMaxTokens = 6144

	outlineTimeout = 30 * time.Minute
	chapterTimeout = 15 * time.Minute
)

// ─── Data types ───────────────────────────────────────────────────────────────

// StoryPrefs holds everything collected during the interview.
type StoryPrefs struct {
	Language        string
	Genre           string
	Protagonist     string
	Setting         string
	CentralConflict string
	Tone            string
	ChapterCount    int
	DramaLevel      int
	TwistLevel      int
	SpecialRequests string
}

// ChapterOutline is the plan for one chapter.
type ChapterOutline struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	Summary        string `json:"summary"`
	Purpose        string `json:"purpose"`
	DramaticMoment string `json:"dramaticMoment"`
	Hook           string `json:"hook"`
}

// StoryOutline is the full story plan.
type StoryOutline struct {
	Title          string             `json:"title"`
	Logline        string             `json:"logline"`
	DramaticArc    string             `json:"dramaticArc"`
	MainCharacters []models.Character `json:"mainCharacters"`
	KeyTwists      []string           `json:"keyTwists"`
	Chapters       []ChapterOutline   `json:"chapters"`
}

// ─── Entry point ──────────────────────────────────────────────────────────────

// Run drives the full writer pipeline: interview → outline → write chapters →
// consistency check → fix → final story.
func Run(outputDir string, pair *claude.Pair) error {
	writerDir := filepath.Join(outputDir, "writer")
	chaptersDir := filepath.Join(writerDir, "chapters")
	if err := os.MkdirAll(chaptersDir, 0755); err != nil {
		return fmt.Errorf("creating writer dir: %w", err)
	}

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║      InconsistencyFixer — Escritor       ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()
	log.Printf("Provider: %s | Robust: %s | Fast: %s",
		pair.Provider(), pair.Robust.Model(), pair.Fast.Model())

	prefs, err := interview()
	if err != nil {
		return fmt.Errorf("entrevista: %w", err)
	}

	outline, err := getOrCreateOutline(pair, prefs, writerDir)
	if err != nil {
		return fmt.Errorf("esboço: %w", err)
	}

	if err := writeAllChapters(pair, prefs, outline, writerDir, chaptersDir); err != nil {
		return fmt.Errorf("escrevendo capítulos: %w", err)
	}

	// ── Self-check: run reader + fixer over the writer's own output ──
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║     Verificação de consistência final     ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()

	log.Println("Rodando o leitor sobre os capítulos gerados (3 passes)...")
	if err := reader.RunDir(writerDir, "chapters", pair); err != nil {
		log.Printf("Aviso: leitor falhou: %v", err)
		log.Println("Continuando — você pode rodar 'go run . read' manualmente apontando para writer/")
	} else {
		log.Println("Rodando o fixer para resolver as inconsistências encontradas...")
		if err := fixer.RunDir(writerDir, "chapters", pair); err != nil {
			log.Printf("Aviso: fixer falhou: %v", err)
		} else {
			log.Println("✓ História final corrigida em " + filepath.Join(writerDir, "story_fixed.txt"))
		}
	}

	return nil
}

// ─── Interview ────────────────────────────────────────────────────────────────

func interview() (StoryPrefs, error) {
	fmt.Println("Vou fazer algumas perguntas antes de escrever sua história.")
	fmt.Println("Para cada pergunta, veja os exemplos e responda livremente.")
	fmt.Println()

	ask := func(question string, examples []string) (string, error) {
		fmt.Println("──────────────────────────────────────────")
		fmt.Println(question)
		if len(examples) > 0 {
			fmt.Println("Exemplos:")
			for _, e := range examples {
				fmt.Println("  •", e)
			}
		}
		fmt.Print("\nSua resposta: ")
		r := bufio.NewReader(os.Stdin)
		line, err := r.ReadString('\n')
		fmt.Println()
		return strings.TrimSpace(line), err
	}

	askInt := func(question string, examples []string, min, max int) (int, error) {
		for {
			s, err := ask(question, examples)
			if err != nil {
				return 0, err
			}
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || n < min || n > max {
				fmt.Printf("  ⚠ Por favor, digite um número entre %d e %d.\n\n", min, max)
				continue
			}
			return n, nil
		}
	}

	var p StoryPrefs
	var err error

	p.Language, err = ask(
		"1. Em qual idioma a história deve ser escrita?",
		[]string{
			"Português (Brasil)",
			"English",
			"Español",
		},
	)
	if err != nil {
		return p, err
	}

	p.Genre, err = ask(
		"2. Qual é o gênero da história?",
		[]string{
			"Romantasia — romance + fantasia (lobos, vampiros, fae, magia)",
			"Dark romance — romance possessivo, anti-herói, stakes pesadas",
			"Romance contemporâneo — sem fantasia, drama emocional",
			"Fantasia épica — mundo inventado, magia, política",
			"Thriller / suspense — perigo e mistério",
			"Drama — conflitos pessoais e emocionais intensos",
		},
	)
	if err != nil {
		return p, err
	}

	p.Protagonist, err = ask(
		"3. Descreva brevemente o(a) protagonista:",
		[]string{
			"Mulher de 22 anos, ômega rejeitada que descobre ser luna marcada",
			"Herdeira deserdada de um reino fae caçada pelo próprio meio-irmão",
			"Mestiça meio-vampira que não conhecia sua linhagem",
			"Advogada de Nova York que herda um castelo... e o lorde dele",
		},
	)
	if err != nil {
		return p, err
	}

	p.Setting, err = ask(
		"4. Onde e quando a história acontece?",
		[]string{
			"Reino lobisomem moderno escondido nos EUA — alcateias, território, hierarquia",
			"Corte fae sazonal, intrigas entre os Tronos da Primavera e do Inverno",
			"Império vampiro vitoriano — bailes, contratos de sangue, casamentos políticos",
			"Mundo medieval com dragões — ordens de cavaleiros, magia ancestral",
		},
	)
	if err != nil {
		return p, err
	}

	p.CentralConflict, err = ask(
		"5. Qual é o conflito central da história?",
		[]string{
			"Ela é dada como noiva ao Alpha rival que destruiu o clã do pai dela",
			"Ele é seu mate destinado e seu inimigo público — ambos têm que escolher",
			"Ela descobre que é a herdeira que o conselho jurou matar",
			"Casamento arranjado com um lorde que ela odeia... e que está apaixonado por ela há anos",
		},
	)
	if err != nil {
		return p, err
	}

	p.Tone, err = ask(
		"6. Qual o tom geral da história?",
		[]string{
			"Sombrio e intenso — possessivo, pesado, poucos momentos leves",
			"Equilibrado — drama real misturado com momentos de leveza e humor",
			"Slow-burn — tensão construída devagar, química lenta e crescente",
			"Spicy / steamy — química física presente desde o começo",
		},
	)
	if err != nil {
		return p, err
	}

	p.ChapterCount, err = askInt(
		"7. Quantos capítulos você quer? (digite um número)",
		[]string{
			"15 capítulos — história focada, ritmo rápido, sem subplots",
			"40 capítulos — desenvolvimento completo com 2-3 subplots",
			"80 capítulos — webnovel padrão com múltiplos arcos",
			"150 capítulos — saga longa, espaço para tensão lenta",
		},
		5, 200,
	)
	if err != nil {
		return p, err
	}

	p.DramaLevel, err = askInt(
		"8. Nível de drama (1 a 10):",
		[]string{
			"1-3 → conflitos suaves; ex: casal briga e faz as pazes no mesmo capítulo",
			"4-6 → tensão crescente; ex: traição revelada que abala tudo, mas deixa esperança",
			"7-9 → intensidade alta; ex: personagem amado morre, protagonista perde tudo",
			"10  → tragédia; perdas permanentes, vitória vem com cicatrizes profundas",
		},
		1, 10,
	)
	if err != nil {
		return p, err
	}

	p.TwistLevel, err = askInt(
		"9. Nível de plot twists (1 a 10):",
		[]string{
			"1-3 → previsível e confortável; tropes executados de forma confiável",
			"4-6 → algumas reviravoltas; ex: o mentor estava do lado errado desde o início",
			"7-9 → reviravoltas frequentes; identidades falsas, alianças que se invertem",
			"10  → nada é o que parece; a realidade do mundo pode ser subvertida",
		},
		1, 10,
	)
	if err != nil {
		return p, err
	}

	p.SpecialRequests, err = ask(
		"10. Há algo específico que você quer incluir ou EVITAR? (deixe em branco se não houver)",
		[]string{
			"Incluir: cena de baile imperial onde ela aparece transformada",
			"Incluir: triângulo amoroso com o melhor amigo do alpha",
			"Evitar: violência sexual gráfica",
			"Incluir: revelação de herança real no terceiro ato",
		},
	)
	if err != nil {
		return p, err
	}

	return p, nil
}

// ─── Outline ──────────────────────────────────────────────────────────────────

func getOrCreateOutline(pair *claude.Pair, prefs StoryPrefs, writerDir string) (StoryOutline, error) {
	outlinePath := filepath.Join(writerDir, "outline.json")

	if data, err := os.ReadFile(outlinePath); err == nil {
		var existing StoryOutline
		if json.Unmarshal(data, &existing) == nil && len(existing.Chapters) > 0 {
			fmt.Printf("Encontrei um esboço existente: %q (%d capítulos)\n", existing.Title, len(existing.Chapters))
			if askYesNo("Usar esse esboço e continuar de onde parou?") {
				return existing, nil
			}
		}
	}

	outline, err := generateAndApproveOutline(pair, prefs)
	if err != nil {
		return StoryOutline{}, err
	}

	data, _ := json.MarshalIndent(outline, "", "  ")
	_ = os.WriteFile(outlinePath, data, 0644)
	return outline, nil
}

func generateAndApproveOutline(pair *claude.Pair, prefs StoryPrefs) (StoryOutline, error) {
	for {
		fmt.Println("\nGerando o esboço da história...")
		fmt.Println("(Isso pode levar vários minutos para histórias longas. Vou imprimir progresso.)")

		outline, err := generateOutline(pair, prefs)
		if err != nil {
			return StoryOutline{}, fmt.Errorf("gerando esboço: %w", err)
		}

		displayOutline(outline)

		fmt.Println("\nO que deseja fazer?")
		fmt.Println("  s — Aprovar e começar a escrever")
		fmt.Println("  e — Pedir ajustes no esboço")
		fmt.Println("  r — Gerar um esboço completamente diferente")
		fmt.Print("\nEscolha: ")

		r := bufio.NewReader(os.Stdin)
		choice, _ := r.ReadString('\n')
		choice = strings.ToLower(strings.TrimSpace(choice))
		fmt.Println()

		switch choice {
		case "s", "sim", "y", "yes":
			return outline, nil
		case "e", "editar", "ajuste", "ajustar":
			fmt.Print("O que deve ser alterado no esboço? ")
			feedback, _ := r.ReadString('\n')
			prefs.SpecialRequests += "\n[AJUSTE SOLICITADO]: " + strings.TrimSpace(feedback)
		case "r", "refazer", "regenerar":
			// loop again
		default:
			fmt.Println("Digite 's', 'e' ou 'r'.")
		}
	}
}

// generateOutline produces the full StoryOutline. For long stories (>30 chapters)
// the chapter list is built in chunks to avoid hitting the model's max-tokens
// ceiling. Model split:
//   - The "spine" (title, logline, characters, twists, first 20 chapters) is the
//     creative foundation → ROBUST model.
//   - Subsequent chunks just extend the chapter list with the spine as anchor →
//     FAST model handles this well and saves time/money.
func generateOutline(pair *claude.Pair, p StoryPrefs) (StoryOutline, error) {
	const chunkSize = 20

	if p.ChapterCount <= 30 {
		log.Println("  Gerando esboço completo em uma única chamada (Robust)...")
		return generateOutlineSingle(pair.Robust, p)
	}

	log.Printf("  Esboço longo (%d capítulos). Spine no Robust, chunks no Fast.", p.ChapterCount)

	// 1. Spine + first chunk of chapters — Robust
	spine, err := generateOutlineSpine(pair.Robust, p, chunkSize)
	if err != nil {
		return StoryOutline{}, fmt.Errorf("spine: %w", err)
	}
	log.Printf("  ✓ Spine + capítulos 1-%d (%q)", len(spine.Chapters), spine.Title)

	// 2. Continue chapters in chunks — Fast
	for len(spine.Chapters) < p.ChapterCount {
		startCh := len(spine.Chapters) + 1
		endCh := startCh + chunkSize - 1
		if endCh > p.ChapterCount {
			endCh = p.ChapterCount
		}
		log.Printf("  [Fast] Gerando capítulos %d-%d/%d...", startCh, endCh, p.ChapterCount)

		moreCh, err := generateOutlineChapters(pair.Fast, p, spine, startCh, endCh)
		if err != nil {
			return StoryOutline{}, fmt.Errorf("capítulos %d-%d: %w", startCh, endCh, err)
		}
		spine.Chapters = append(spine.Chapters, moreCh...)
		log.Printf("  ✓ Capítulos %d-%d gerados", startCh, endCh)
	}

	return spine, nil
}

func generateOutlineSingle(client *claude.Client, p StoryPrefs) (StoryOutline, error) {
	prompt := fmt.Sprintf(`Crie um esboço completo para uma história com estas especificações:

Idioma da história: %s
Gênero: %s
Protagonista: %s
Cenário: %s
Conflito central: %s
Tom: %s
Número de capítulos: %d
Nível de drama: %d/10
Nível de plot twists: %d/10
Pedidos especiais: %s

Siga rigorosamente as regras do seu script de escritor. Abrace os tropes do gênero
sem medo — leitoras de romantasia querem o pacote conhecido bem executado.

Distribua o arco dramático em três atos proporcionais ao número de capítulos.
Para o nível de twist %d/10, plante sementes visíveis antes de cada revelação.
Para o nível de drama %d/10, calibre as perdas e stakes de acordo.

Responda SOMENTE com JSON válido, sem markdown, sem explicações:
%s`,
		p.Language, p.Genre, p.Protagonist, p.Setting,
		p.CentralConflict, p.Tone, p.ChapterCount,
		p.DramaLevel, p.TwistLevel, p.SpecialRequests,
		p.TwistLevel, p.DramaLevel,
		outlineSchema,
	)

	resp, err := callOutline(client, prompt, outlineMaxTokens)
	if err != nil {
		return StoryOutline{}, err
	}

	jsonStr := extractJSON(resp)
	var outline StoryOutline
	if err := json.Unmarshal([]byte(jsonStr), &outline); err != nil {
		return StoryOutline{}, fmt.Errorf("parse do esboço: %w\nResposta: %.500s", err, resp)
	}
	return outline, nil
}

// generateOutlineSpine produces title/logline/characters/twists + the first N
// chapters. Saves tokens vs. asking for everything at once.
func generateOutlineSpine(client *claude.Client, p StoryPrefs, firstChunk int) (StoryOutline, error) {
	if firstChunk > p.ChapterCount {
		firstChunk = p.ChapterCount
	}
	prompt := fmt.Sprintf(`Crie a ESPINHA do esboço (título, sinopse, personagens, twists) e o
primeiro bloco de capítulos (1 a %d) para uma história com estas especificações:

Idioma: %s
Gênero: %s
Protagonista: %s
Cenário: %s
Conflito central: %s
Tom: %s
Número TOTAL de capítulos da história: %d
Nível de drama: %d/10
Nível de plot twists: %d/10
Pedidos especiais: %s

Abrace os tropes do gênero. O Ato 1 deve ocupar aproximadamente os primeiros 20%%
dos capítulos totais — calibre o ritmo do bloco 1-%d sabendo que ainda há %d
capítulos depois.

Responda SOMENTE com JSON válido seguindo este schema:
%s`,
		firstChunk,
		p.Language, p.Genre, p.Protagonist, p.Setting,
		p.CentralConflict, p.Tone, p.ChapterCount,
		p.DramaLevel, p.TwistLevel, p.SpecialRequests,
		firstChunk, p.ChapterCount-firstChunk,
		outlineSchema,
	)

	resp, err := callOutline(client, prompt, outlineMaxTokens)
	if err != nil {
		return StoryOutline{}, err
	}
	jsonStr := extractJSON(resp)
	var outline StoryOutline
	if err := json.Unmarshal([]byte(jsonStr), &outline); err != nil {
		return StoryOutline{}, fmt.Errorf("parse do spine: %w\nResposta: %.500s", err, resp)
	}
	return outline, nil
}

// generateOutlineChapters produces only ChapterOutline entries [startCh, endCh],
// using the existing spine as anchor.
func generateOutlineChapters(client *claude.Client, p StoryPrefs, spine StoryOutline, startCh, endCh int) ([]ChapterOutline, error) {
	spineJSON, _ := json.MarshalIndent(spine, "", "  ")

	prompt := fmt.Sprintf(`Continue o esboço da história "%s". Use a espinha abaixo como verdade absoluta —
NÃO invente novos personagens principais nem mude twists planejados.

[ESPINHA EXISTENTE]
%s

Agora gere os capítulos %d a %d (de %d totais). Calibre o ritmo sabendo onde
estes capítulos caem dentro do arco de três atos:
  - Ato 1 (capítulos 1 a %d): apresentação
  - Ato 2 (capítulos %d a %d): escalada (ponto mais sombrio nos últimos 20%% deste ato)
  - Ato 3 (capítulos %d a %d): confronto e resolução

Cada capítulo deve incluir um gancho que torne o próximo irresistível.
Para o nível de twist %d/10, plante e/ou pague twists prometidos na espinha.

Responda SOMENTE com JSON válido neste formato:
{
  "chapters": [
    {
      "number": %d,
      "title": "string",
      "summary": "2-3 frases sobre o que acontece",
      "purpose": "função narrativa",
      "dramaticMoment": "momento mais tenso (ou vazio)",
      "hook": "gancho para o próximo"
    }
  ]
}`,
		spine.Title,
		string(spineJSON),
		startCh, endCh, p.ChapterCount,
		p.ChapterCount/5,
		p.ChapterCount/5+1, 4*p.ChapterCount/5,
		4*p.ChapterCount/5+1, p.ChapterCount,
		p.TwistLevel,
		startCh,
	)

	resp, err := callOutline(client, prompt, outlineMaxTokens)
	if err != nil {
		return nil, err
	}
	jsonStr := extractJSON(resp)

	var wrap struct {
		Chapters []ChapterOutline `json:"chapters"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &wrap); err != nil {
		return nil, fmt.Errorf("parse de capítulos: %w\nResposta: %.500s", err, resp)
	}

	// Force-correct numbering in case the model drifts
	for i := range wrap.Chapters {
		expected := startCh + i
		if wrap.Chapters[i].Number != expected {
			wrap.Chapters[i].Number = expected
		}
	}
	return wrap.Chapters, nil
}

// callOutline wraps the LLM call with the long timeout used for outline-style
// generations and tries once more with halved tokens if the first response
// doesn't parse.
func callOutline(client *claude.Client, prompt string, maxTokens int) (string, error) {
	tryOnce := func(tokens int) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), outlineTimeout)
		defer cancel()
		return client.CompleteEx(ctx, systemPrompt, []claude.Message{
			claude.UserMessage(claude.TextBlock(prompt)),
		}, claude.Options{MaxTokens: tokens, JSONMode: true})
	}

	resp, err := tryOnce(maxTokens)
	if err == nil && jsonParseable(resp) {
		return resp, nil
	}
	if err != nil {
		log.Printf("    primeira tentativa falhou (%v) — tentando de novo com max_tokens menor", err)
	} else {
		log.Println("    resposta não passou no parse — tentando de novo com max_tokens menor")
	}
	resp2, err2 := tryOnce(maxTokens / 2)
	if err2 != nil {
		if err != nil {
			return "", fmt.Errorf("duas tentativas falharam: %v; %v", err, err2)
		}
		return "", err2
	}
	return resp2, nil
}

func jsonParseable(s string) bool {
	js := extractJSON(s)
	var any any
	return json.Unmarshal([]byte(js), &any) == nil
}

const outlineSchema = `{
  "title": "título da história",
  "logline": "uma frase: quem é a protagonista, o que está em jogo, o que impede",
  "dramaticArc": "descrição do arco dramático em 2-3 frases",
  "mainCharacters": [
    {
      "name": "nome completo",
      "aliases": ["apelidos"],
      "description": "aparência física e personalidade em 2-3 frases",
      "role": "protagonist|antagonist|supporting",
      "relationships": ["descrição dos relacionamentos"],
      "status": "alive",
      "firstAppearance": 1
    }
  ],
  "keyTwists": ["descrição do twist — não revele em qual capítulo"],
  "chapters": [
    {
      "number": 1,
      "title": "título do capítulo",
      "summary": "o que acontece neste capítulo — 2-3 frases",
      "purpose": "função narrativa: apresentar X, revelar Y, escalar Z",
      "dramaticMoment": "o momento mais tenso ou emocional do capítulo (ou vazio)",
      "hook": "o que fará a leitora precisar do próximo capítulo"
    }
  ]
}`

// ─── Chapter writing ──────────────────────────────────────────────────────────

// writeAllChapters writes each planned chapter using the ROBUST model — prose
// quality is the deliverable, this is not a place for the fast model.
func writeAllChapters(pair *claude.Pair, prefs StoryPrefs, outline StoryOutline, writerDir, chaptersDir string) error {
	client := pair.Robust
	outlineText := buildOutlineText(outline)
	outlineJSON, _ := json.MarshalIndent(outline, "", "  ")

	total := len(outline.Chapters)
	written := 0
	var prevContent string

	for _, chOutline := range outline.Chapters {
		chPath := filepath.Join(chaptersDir, fmt.Sprintf("chapter_%04d.txt", chOutline.Number))

		// Resume: load existing content if already written
		if data, err := os.ReadFile(chPath); err == nil {
			log.Printf("[%d/%d] Capítulo %d já existe — pulando", chOutline.Number, total, chOutline.Number)
			if ch, parseErr := parseChapterFile(chPath, string(data)); parseErr == nil {
				prevContent = ch.Content
			}
			written++
			continue
		}

		log.Printf("[%d/%d] Escrevendo capítulo %d: %s...", chOutline.Number, total, chOutline.Number, chOutline.Title)

		content, err := writeChapter(client, prefs, outline, outlineText, string(outlineJSON), prevContent, chOutline)
		if err != nil {
			log.Printf("  Erro no capítulo %d: %v", chOutline.Number, err)
			continue
		}

		ch := models.Chapter{
			Number:  chOutline.Number,
			Title:   chOutline.Title,
			Content: content,
		}
		if err := story.SaveChapter(chaptersDir, ch); err != nil {
			log.Printf("  Falha ao salvar capítulo %d: %v", chOutline.Number, err)
			continue
		}

		prevContent = content
		written++
		log.Printf("  ✓ Capítulo %d escrito (%d palavras aprox.)", chOutline.Number, wordCount(content))
	}

	// Assemble final story
	chapters, err := story.LoadChapters(chaptersDir)
	if err != nil {
		return fmt.Errorf("carregando capítulos: %w", err)
	}

	storyPath := filepath.Join(writerDir, "story.txt")
	if err := story.WriteStory(storyPath, chapters); err != nil {
		return fmt.Errorf("salvando história: %w", err)
	}

	// Save world bible from outline for use with reader/fixer
	biblePath := filepath.Join(writerDir, "world_bible.json")
	bible := models.WorldBible{
		Characters: outline.MainCharacters,
	}
	if bdata, err := json.MarshalIndent(bible, "", "  "); err == nil {
		_ = os.WriteFile(biblePath, bdata, 0644)
	}

	fmt.Printf("\n✓ Primeira passada concluída: %d/%d capítulos escritos.\n", written, total)
	fmt.Printf("  História bruta    → %s\n", storyPath)
	fmt.Printf("  Capítulos         → %s\n", chaptersDir)
	return nil
}

func writeChapter(
	client *claude.Client,
	prefs StoryPrefs,
	outline StoryOutline,
	outlineText, outlineJSON string,
	prevContent string,
	ch ChapterOutline,
) (string, error) {
	prev := ""
	if prevContent != "" {
		prev = fmt.Sprintf("\n\n[CAPÍTULO ANTERIOR — para continuidade imediata, especialmente se este capítulo continuar a mesma cena]\n%s", prevContent)
	}

	prompt := fmt.Sprintf(`Escreva o capítulo %d de "%s".

ESPECIFICAÇÕES:
- Idioma: %s
- Gênero: %s
- Nível de drama: %d/10
- Nível de plot twists: %d/10
- Tom: %s

PLANO DO CAPÍTULO %d — "%s":
Resumo: %s
Função narrativa: %s
Momento dramático: %s
Gancho para o próximo: %s

[ESBOÇO COMPLETO DA HISTÓRIA — para consistência de longo prazo]
%s
%s

Lembretes de continuidade:
- Se este capítulo continua a mesma cena do anterior, abra no mesmo lugar, com
  os mesmos personagens presentes, mesmas roupas, mesmo tempo. Continue o diálogo.
- Se este capítulo é uma cena nova, abra com uma frase que ancore tempo + lugar
  + presença.
- Personagens estabelecidos como presentes em uma cena permanecem na cena até
  saírem on-page.
- Vestuário só muda quando o personagem se troca on-page.

Escreva agora o capítulo %d. Siga o plano, aplique os níveis de drama e twist,
e retorne APENAS o texto do capítulo (sem título, sem comentários).`,
		ch.Number, outline.Title,
		prefs.Language, prefs.Genre,
		prefs.DramaLevel, prefs.TwistLevel, prefs.Tone,
		ch.Number, ch.Title,
		ch.Summary, ch.Purpose, ch.DramaticMoment, ch.Hook,
		outlineText,
		prev,
		ch.Number,
	)

	ctx, cancel := context.WithTimeout(context.Background(), chapterTimeout)
	defer cancel()
	resp, err := client.CompleteEx(ctx, systemPrompt, []claude.Message{
		claude.UserMessage(
			// Cache the outline JSON for reuse across all chapter calls
			claude.CachedTextBlock(fmt.Sprintf("[PERSONAGENS E TWISTS — fonte da verdade]\n%s", outlineJSON)),
			claude.TextBlock(prompt),
		),
	}, claude.Options{MaxTokens: chapterMaxTokens})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp), nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func buildOutlineText(o StoryOutline) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Título: %s\nSinopse: %s\n\nCapítulos:\n", o.Title, o.Logline))
	for _, ch := range o.Chapters {
		sb.WriteString(fmt.Sprintf("  %02d. %s: %s\n", ch.Number, ch.Title, ch.Summary))
	}
	return sb.String()
}

func displayOutline(o StoryOutline) {
	fmt.Printf("\n╔══ ESBOÇO: %s ══╗\n", o.Title)
	fmt.Printf("Sinopse: %s\n", o.Logline)
	fmt.Printf("Arco dramático: %s\n\n", o.DramaticArc)

	fmt.Printf("PERSONAGENS (%d):\n", len(o.MainCharacters))
	for _, c := range o.MainCharacters {
		fmt.Printf("  • %s (%s): %s\n", c.Name, c.Role, c.Description)
	}

	if len(o.KeyTwists) > 0 {
		fmt.Printf("\nPLOT TWISTS PLANEJADOS (%d):\n", len(o.KeyTwists))
		for i, t := range o.KeyTwists {
			fmt.Printf("  %d. %s\n", i+1, t)
		}
	}

	fmt.Printf("\nCAPÍTULOS (%d):\n", len(o.Chapters))
	for _, ch := range o.Chapters {
		fmt.Printf("  %02d. %-35s %s\n", ch.Number, ch.Title, ch.Summary)
		if ch.Hook != "" {
			fmt.Printf("      ↳ Gancho: %s\n", ch.Hook)
		}
	}
	fmt.Println()
}

func askYesNo(question string) bool {
	r := bufio.NewReader(os.Stdin)
	fmt.Printf("%s (s/n): ", question)
	line, _ := r.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	fmt.Println()
	return line == "s" || line == "sim" || line == "y" || line == "yes"
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if nl := strings.Index(s, "\n"); nl >= 0 {
			s = s[nl+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	if start := strings.Index(s, "{"); start >= 0 {
		if end := strings.LastIndex(s, "}"); end > start {
			return s[start : end+1]
		}
	}
	return s
}

func wordCount(s string) int {
	return len(strings.Fields(s))
}

func parseChapterFile(path, raw string) (models.Chapter, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	nl := strings.Index(raw, "\n")
	if nl < 0 {
		return models.Chapter{}, fmt.Errorf("sem newline em %s", path)
	}
	body := strings.TrimSpace(raw[nl:])
	return models.Chapter{Content: body, Path: path}, nil
}
