# Diagnóstico — Reader, Fixer e Writer

Análise dos três sintomas que você descreveu, com a causa raiz de cada um e as mudanças concretas para resolver.

---

## 1. `JSON parse error: invalid character 'T' looking for beginning of value`

**Onde:** `internal/reader/reader.go` → `parseResponse` (linha 215), chamado por `analyseFirstBatch` e `analyseNextBatch`.

**O que está acontecendo:** o modelo (Ollama, pelo seu `PROVIDER=ollama`) está devolvendo JSON que **não bate com o schema esperado**. No trecho que você colou dá pra ver dois desvios já no começo:

```json
"characters": {            // ← deveria ser ARRAY, veio como OBJETO map
  "Elara": {
    "role": "Empress",
    "abilities": [...],    // ← campo que não existe no struct Character
```

O struct em `internal/models/models.go` espera:

```go
Characters []Character `json:"characters"`     // array
```

Quando o `json.Unmarshal` recebe um objeto onde esperava array, ele já falharia. Mas o erro específico `invalid character 'T'` vem de outro problema simultâneo: **truncamento**. `maxTokens = 8192` é pouco para um lote de 10 capítulos (no Anthropic) ou 3 capítulos (no Ollama) somado a um World Bible inteiro + lista de inconsistências. Quando a resposta corta no meio, `extractJSON` faz `LastIndex("}")` e devolve um pedaço malformado — o `T` é provavelmente um literal do tipo `True` (estilo Python, comum em saídas de modelos locais) ou um pedaço de palavra dentro de uma string que ficou aberta.

**Por que isso derruba TUDO:** o loop em `Run` faz `continue` em cada batch que falha (linha 60). Resultado: você termina com `0 inconsistencies` no relatório final mesmo quando todos os lotes falharam — não é que o modelo "não achou nada", é que **nenhum lote foi parseado com sucesso**. Bate exatamente com o seu output: "Warning: batch failed" → "Analysis complete — 0 inconsistencies found".

**Correções recomendadas (em ordem de impacto):**

1. **Aceitar `characters` como array OU objeto.** Modelos locais teimam em devolver mapa por nome. Crie um `UnmarshalJSON` custom em `WorldBible` que tenta os dois formatos. Mesmo tratamento para `locations` e `plotEvents`.

2. **Tornar o schema flexível.** Use `json:",omitempty"` e ignore campos extra silenciosamente (já é o default), mas adicione um campo `Extra map[string]any \`json:"-"\`` se quiser preservar dados que o modelo inventou (ex.: `abilities`).

3. **Aumentar `maxTokens` ou reduzir batch size.** Hoje `maxTokens = 8192` constante. Para o Ollama (batch=3) provavelmente está ok, mas a saída pode estourar quando há muitos personagens. Dois caminhos:
   - Subir para 12288–16384 quando `provider == "ollama"` e o `numCtx` permitir.
   - Reduzir o batch size do Anthropic de 10 para 5 — economiza a janela de saída.

4. **Não engolir erros de batch.** Hoje um batch quebrado é só um `log.Printf` de warning. Acumule erros e, se mais de N% dos batches falharem, retorne erro fatal em vez de gerar relatório vazio. Sugestão:

   ```go
   var batchErrs []error
   // ... dentro do loop:
   if callErr != nil {
       batchErrs = append(batchErrs, fmt.Errorf("batch %d-%d: %w", batch[0].Number, batch[len(batch)-1].Number, callErr))
       continue
   }
   // ... depois do loop:
   if len(batchErrs) > len(chapters)/batchSize/2 {
       return fmt.Errorf("mais da metade dos batches falhou:\n%v", errors.Join(batchErrs...))
   }
   ```

5. **Pedir JSON mode ao Ollama.** A request OpenAI-compat aceita `"response_format": {"type": "json_object"}`. Modelos como `qwen2.5`, `llama3.1`, `gemma2` respeitam isso e param de inventar markdown/comentário ao redor.

6. **Validar antes de aceitar a resposta.** Depois do `json.Unmarshal`, cheque pelo menos: `len(result.WorldBible.Characters) > 0` no primeiro batch (se vier zero personagens em 3+ capítulos, algo está errado). Faça retry uma vez com prompt reforçado antes de desistir.

---

## 2. "0 inconsistencies found" — mas o livro tem muitas

Mesmo se o problema #1 sumir, o detector não vai pegar o tipo de inconsistência que você descreveu (vestidos que mudam, personagens que somem da sala, "sentir o lobo" aparecendo do nada, falas que nunca aconteceram). O motivo é estrutural, não só técnico:

**Problema A — Granularidade errada.** O prompt analisa de **capítulo em capítulo**, batch por batch. As inconsistências que você cita são **intra-cena** (vestido muda no meio de uma conversa) ou **transição de cena** (Riley estava na sala, sumiu, voltou). O prompt atual não pede isso explicitamente — fala em "mudanças entre capítulos".

**Problema B — Categorias ausentes.** Olha as categorias que o prompt lista hoje:

```
character_description | character_name | plot_continuity | relationship |
timeline | setting | ability | other
```

Não tem nenhuma para: **vestuário/aparência momentânea**, **presença em cena** (quem está/não está na sala), **continuidade de diálogo** (alguém responder sobre algo que nunca foi dito), **descontinuidade de poderes** (sentir o lobo só às vezes).

**Problema C — Sem checagem cena-a-cena.** O modelo tem que ler todo o capítulo e cuspir tudo de uma vez. Para um capítulo longo, ele vai pular detalhes pequenos. Você precisa de um **passe de scan focado** depois do passe estrutural.

**Correções recomendadas:**

1. **Reescrever o prompt com checklist explícita** das categorias que importam pra esse tipo de história:

   ```text
   Procure especificamente por:
   - SCENE_PRESENCE: personagem mencionado como estando na cena, depois ignorado, depois falando — sem ter saído ou voltado
   - WARDROBE: descrição de roupa/aparência muda dentro da mesma cena sem troca explícita
   - DIALOGUE_RETCON: personagem responde a algo que ninguém disse, ou cita evento que não aconteceu
   - ABILITY_DRIFT: poder/sentido (ex.: "sentir o lobo", aura, magia) aparece, some e reaparece sem explicação
   - PERSONALITY_SHIFT: tom/voz do personagem muda bruscamente sem trigger narrativo
   - PLOT_CONTRADICTION: fatos do mundo se contradizem entre capítulos
   ```

2. **Adicionar um segundo passe "fine-grained"** rodando por capítulo (não por batch) com foco em detecção de detalhes de cena. Algo como:

   ```go
   func analyseChapterFineGrained(client, bible, chapter) ([]Inconsistency, error)
   ```

   Esse passe recebe o World Bible já consolidado e UMA cena/capítulo por vez, com prompt focado só em "scene_presence + wardrobe + dialogue_retcon".

3. **Pipeline de dois estágios:**
   - Estágio 1 (atual): batch grande → World Bible + inconsistências macro.
   - Estágio 2 (novo): cap-a-cap com bible já pronta → inconsistências micro.

   Sai mais caro em chamadas, mas resolve o "0 found".

4. **Pedir citações exatas.** O schema já tem `originalText`, mas o prompt não obriga a preencher com texto **literal** do capítulo. Force isso ("originalText DEVE ser cópia textual de até 200 caracteres do capítulo, entre aspas") — isso ancora o modelo na cena e reduz alucinação.

5. **Aumentar `severity` para forçar volume.** Hoje o modelo pode estar reportando só `high`. Adicione no prompt: "Reporte TUDO, mesmo low. Erros pequenos repetidos viram erros grandes — não filtre."

---

## 3. Writer → Ollama timeout: `context deadline exceeded (Client.Timeout)`

**Onde:** `internal/writer/writer.go` → `generateOutline` (linha 380), via `client.CompleteWithSystem`.

**Causa direta:** o `http.Client.Timeout` está em **600s** (`internal/claude/client.go` linha 58). Mas:

- `Stream: false` faz o servidor processar tudo antes de mandar o primeiro byte.
- O outline para 80 capítulos com `max_tokens: 8192` pode demorar **mais de 10 min** num modelo local grande (`gemma2:27b` no default).
- O erro `context deadline exceeded (Client.Timeout exceeded while awaiting headers)` é exatamente isso: o servidor ainda nem começou a responder.

**Causa secundária:** o `ctx` passado é `context.Background()` — sem cancelamento. O timeout só vem do `http.Client`. Para tarefas longas o ideal é o **inverso**: client com timeout grande/zero, e `ctx` com timeout/cancelamento explícito.

**Correções recomendadas:**

1. **Habilitar streaming.** Trocar `Stream: false` por `true` no `ollamaRequest` e ler chunked. Streaming evita o "awaiting headers" porque o servidor manda dados conforme produz. Bônus: você consegue mostrar progresso ao usuário em vez de tela parada.

2. **Aumentar timeout para tarefas de geração longa.** Outline e capítulo são as chamadas mais pesadas. Sugestão:

   ```go
   // No NewOllama:
   http: &http.Client{Timeout: 0}, // sem deadline — controla via context
   ```

   E no caller:

   ```go
   ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
   defer cancel()
   resp, err := client.CompleteWithSystem(ctx, ...)
   ```

3. **Quebrar a geração de outline em duas chamadas** quando `ChapterCount > 30`:
   - Primeira chamada: `title`, `logline`, `dramaticArc`, `mainCharacters`, `keyTwists` + os primeiros 20 capítulos.
   - Segunda chamada: continuar do capítulo 21 em diante, em blocos de 20.

   Isso evita o monstro de 8192 tokens de saída de uma vez só. Já tem precedente no `reader.go` (batches), só falta no writer.

4. **Trocar default do modelo.** `gemma2:27b` é muito pesado pra desktop comum. Para outline (texto estruturado, JSON) `qwen2.5:14b` ou `llama3.1:8b-instruct-q4` rodam **3-5x mais rápido** com qualidade aceitável. Para escrever capítulos sim, manter um modelo maior. Considere dois modelos no `.env`:

   ```env
   OLLAMA_MODEL_OUTLINE=qwen2.5:14b
   OLLAMA_MODEL_WRITING=gemma2:27b
   ```

5. **Retry com backoff.** Se o primeiro pedido der timeout, tentar de novo com `max_tokens` menor antes de desistir. Outline grande tende a estourar; um retry com 4096 normalmente passa.

---

## Prioridade sugerida de implementação

1. **Reader: aceitar object-or-array no Unmarshal + falhar alto se >50% dos batches quebrarem.** Resolve o "0 inconsistências" silencioso e dá visibilidade real do que está falhando.
2. **Writer: streaming + timeout via context + outline em chunks.** Resolve o `context deadline exceeded` de imediato.
3. **Reader: prompt v2 com checklist explícita (SCENE_PRESENCE, WARDROBE, DIALOGUE_RETCON, ABILITY_DRIFT) + segundo passe fine-grained.** Resolve a qualidade da detecção pro caso real do seu livro.
4. **Cliente: JSON mode no Ollama + retry com max_tokens reduzido.** Reduz erro residual de parse.

Quer que eu já implemente alguma dessas? Dá pra começar pela #1 (mudança pequena em `models.go` + `reader.go`) que destrava o resto do diagnóstico — depois disso você consegue ver onde os batches estão realmente travando.
