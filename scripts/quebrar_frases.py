"""
Quebra frases muito longas alterando apenas pontuação.
Estratégia: divide em fronteiras de cláusula (", and ", " — ", ", but ", ", so ")
quando a frase tem mais de THRESHOLD caracteres.
"""
import re
from pathlib import Path

CHAPTERS_DIR = Path(
    r"c:\Users\tsuba\GolandProjects\InconsistencyFixer\output\writer"
    r"\Completed-stories\vow-of-the-bloodthorne\chapters-revised"
)
THRESHOLD = 200   # frases acima disso são candidatas
MIN_PART = 60     # cada parte deve ter pelo menos N chars após o split

# Patterns de split em ordem de preferência
# (pattern, replacer) — o replacer recebe o match e retorna a string substituída
SPLIT_PATTERNS = [
    # Vírgula + and/but/so conectando cláusulas
    (r',\s+and\s+(?=[A-Z])',       '. '),
    (r',\s+and\s+(?=I\s)',         '. '),
    (r',\s+and\s+(?=he\s)',        '. '),
    (r',\s+and\s+(?=she\s)',       '. '),
    (r',\s+and\s+(?=it\s)',        '. '),
    (r',\s+and\s+(?=the\s)',       '. The '),
    (r',\s+and\s+(?=a\s)',         '. A '),
    (r',\s+but\s+(?=[A-Za-z])',    '. '),
    # Em-dash conectando duas cláusulas longas
    (r'\s+[—\-]{1,2}\s+(?=[A-Z])',  '. '),
    (r'\s+[—\-]{1,2}\s+(?=I\s)',    '. '),
    (r'\s+[—\-]{1,2}\s+(?=he\s)',   '. '),
    (r'\s+[—\-]{1,2}\s+(?=she\s)',  '. '),
    (r'\s+[—\-]{1,2}\s+(?=the\s)',  '. The '),
    (r'\s+[—\-]{1,2}\s+(?=a\s)',    '. A '),
]

def read_chapter(path):
    raw = path.read_bytes()
    if raw[:2] in (b'\xff\xfe', b'\xfe\xff'):
        return raw.decode('utf-16')
    elif raw[:3] == b'\xef\xbb\xbf':
        return raw.decode('utf-8-sig')
    return raw.decode('utf-8', errors='replace')

def find_best_split(sentence):
    """
    Encontra o melhor ponto de split numa frase longa.
    Retorna (pre, post) ou None se não encontrou split adequado.
    """
    L = len(sentence)
    best = None
    best_score = 0

    for pat, replacement in SPLIT_PATTERNS:
        for m in re.finditer(pat, sentence, re.IGNORECASE):
            start = m.start()
            end = m.end()
            pre = sentence[:start].strip()
            # Reconstruct post: capitalize first char
            post_raw = sentence[end:]
            if not post_raw:
                continue
            post = post_raw[0].upper() + post_raw[1:]

            if len(pre) < MIN_PART or len(post) < MIN_PART:
                continue

            # Prefer splits near the middle of the sentence
            balance = 1 - abs(start / L - 0.45)
            score = balance + (start > L * 0.25) + (start < L * 0.75)

            if score > best_score:
                best_score = score
                best = (pre, replacement, post, m.group())

    return best

def process_sentence(sentence):
    """Quebra a frase se for muito longa. Retorna lista de frases."""
    if len(sentence) < THRESHOLD:
        return [sentence]

    result = find_best_split(sentence)
    if result is None:
        return [sentence]  # Não encontrou split adequado

    pre, replacement, post, matched = result

    # Determine the actual replacement
    # For ", and the " we want ". The "
    if 'the' in replacement.lower() and 'the' not in matched.lower():
        # The "the" is coming from the lookahead, which is already in `post`
        pass

    new_sentence_1 = pre + '.'
    new_sentence_2 = post

    # Recursively process if still too long
    parts = []
    parts.extend(process_sentence(new_sentence_1))
    parts.extend(process_sentence(new_sentence_2))
    return parts

def process_paragraph(para):
    """Processa um parágrafo, quebrando frases longas."""
    if para == '* * *' or para.startswith('<'):
        return para, False

    # Split into sentences
    # Keep trailing punctuation with its sentence
    SENT_SPLIT = re.compile(r'(?<=[.!?])\s+(?=[A-Z"*\'])')

    sentences = re.split(SENT_SPLIT, para)
    new_sentences = []
    changed = False

    for sent in sentences:
        stripped = sent.strip()
        if len(stripped) >= THRESHOLD:
            parts = process_sentence(stripped)
            if len(parts) > 1:
                changed = True
            new_sentences.extend(parts)
        else:
            new_sentences.append(sent)

    return ' '.join(s.strip() for s in new_sentences if s.strip()), changed

def process_chapter(content):
    lines = content.strip().split('\n')
    pov_line = lines[0] if lines else ''
    body_lines = lines[1:]

    paragraphs = []
    current = []
    total_changes = 0

    for line in body_lines:
        if line.strip() == '':
            if current:
                paragraphs.append('\n'.join(current))
                current = []
            paragraphs.append('')
        else:
            current.append(line)
    if current:
        paragraphs.append('\n'.join(current))

    new_paragraphs = []
    for para in paragraphs:
        if not para.strip():
            new_paragraphs.append('')
            continue
        new_para, changed = process_paragraph(para)
        if changed:
            total_changes += 1
        new_paragraphs.append(new_para)

    body_out = '\n'.join(new_paragraphs)
    return pov_line + '\n' + body_out, total_changes

def main():
    import sys
    dry_run = '--dry-run' in sys.argv

    files = sorted(CHAPTERS_DIR.glob('chapter_*.txt'))
    total_changed_files = 0
    total_changes = 0

    for f in files:
        content = read_chapter(f)
        new_content, changes = process_chapter(content)

        if changes > 0:
            total_changed_files += 1
            total_changes += changes
            print(f'  {f.name}: {changes} parágrafo(s) alterado(s)')
            if not dry_run:
                f.write_text(new_content, encoding='utf-8')

    print(f'\nTotal: {total_changes} alterações em {total_changed_files} arquivos.')
    if dry_run:
        print('(modo dry-run — nenhum arquivo alterado)')

if __name__ == '__main__':
    main()
