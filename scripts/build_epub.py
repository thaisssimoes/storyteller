"""Build an EPUB from the Wrong Kind of Magic chapter files."""

import os
import re
from pathlib import Path

from ebooklib import epub

BOOK_DIR = Path(r"C:\Users\tsuba\GolandProjects\InconsistencyFixer\output\writer\Completed-stories\wrong-kind-of-magic")
CHAPTERS_DIR = BOOK_DIR / "chapters"
OUTPUT_PATH = BOOK_DIR / "wrong-kind-of-magic.epub"

TITLE = "Wrong Kind of Magic"
AUTHOR = "Thais Simoes"
LANGUAGE = "en"
IDENTIFIER = "wrong-kind-of-magic-v1"
PUBLISHER = "Self-published"

CSS = """
@namespace epub "http://www.idpf.org/2007/ops";

body {
    font-family: Georgia, "Times New Roman", serif;
    line-height: 1.65;
    margin: 0 1em;
}

h1.chapter-title {
    font-size: 1.6em;
    text-align: center;
    margin-top: 2.5em;
    margin-bottom: 0.2em;
    font-variant: small-caps;
    letter-spacing: 0.05em;
}

p.pov {
    text-align: center;
    font-style: italic;
    color: #555;
    margin-top: 0;
    margin-bottom: 2em;
    font-size: 0.95em;
}

p {
    text-indent: 1.25em;
    margin: 0 0 0.4em 0;
    text-align: justify;
}

p.first {
    text-indent: 0;
}

p.scene-break {
    text-indent: 0;
    text-align: center;
    margin: 1.4em 0;
    letter-spacing: 0.7em;
    color: #888;
}
"""


def escape_html(text: str) -> str:
    return (
        text.replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
    )


def convert_italics(text: str) -> str:
    """Convert *italic* markup to <em>italic</em>."""
    return re.sub(r"\*(.+?)\*", r"<em>\1</em>", text)


def paragraph_to_html(line: str, is_first: bool) -> str:
    line = line.strip()
    if not line:
        return ""
    if line == "* * *":
        return '<p class="scene-break">* * *</p>'
    safe = escape_html(line)
    safe = convert_italics(safe)
    cls = "first" if is_first else None
    if cls:
        return f'<p class="{cls}">{safe}</p>'
    return f"<p>{safe}</p>"


def chapter_to_html(chapter_num: int, raw_text: str) -> tuple[str, str]:
    lines = raw_text.split("\n")
    pov_line = None
    body_lines = []

    started = False
    for line in lines:
        stripped = line.strip()
        if not started and stripped == "":
            continue
        if not started:
            pov_match = re.match(r"^([A-Za-z]+)'s POV$", stripped)
            if pov_match:
                pov_line = stripped
                started = True
                continue
            started = True
            body_lines.append(line)
        else:
            body_lines.append(line)

    title = f"Chapter {chapter_num}"
    html_parts = [f'<h1 class="chapter-title">{title}</h1>']
    if pov_line:
        html_parts.append(f'<p class="pov">{escape_html(pov_line)}</p>')

    paragraphs_done = 0
    after_break = False
    for line in body_lines:
        if not line.strip():
            after_break = True
            continue
        is_first = paragraphs_done == 0 or after_break
        html = paragraph_to_html(line, is_first)
        if html:
            html_parts.append(html)
            paragraphs_done += 1
            after_break = False

    return title, "\n".join(html_parts)


def build_book() -> None:
    book = epub.EpubBook()
    book.set_identifier(IDENTIFIER)
    book.set_title(TITLE)
    book.set_language(LANGUAGE)
    book.add_author(AUTHOR)
    book.add_metadata("DC", "publisher", PUBLISHER)
    book.add_metadata(
        "DC",
        "description",
        "A paranormal romance set inside a secret society of magic users within a regular university.",
    )

    css_item = epub.EpubItem(
        uid="style_main",
        file_name="style/main.css",
        media_type="text/css",
        content=CSS,
    )
    book.add_item(css_item)

    chapter_files = sorted(CHAPTERS_DIR.glob("chapter_*.txt"))
    spine = ["nav"]
    toc = []

    for idx, path in enumerate(chapter_files, start=1):
        raw = path.read_text(encoding="utf-8")
        title, body_html = chapter_to_html(idx, raw)
        filename = f"chap_{idx:04d}.xhtml"
        ch = epub.EpubHtml(
            title=title,
            file_name=filename,
            lang=LANGUAGE,
        )
        ch.content = (
            f"<html xmlns=\"http://www.w3.org/1999/xhtml\">"
            f"<head><title>{title}</title>"
            "<link rel=\"stylesheet\" type=\"text/css\" href=\"style/main.css\"/>"
            "</head><body>"
            f"{body_html}"
            "</body></html>"
        ).encode("utf-8")
        ch.add_item(css_item)
        book.add_item(ch)
        spine.append(ch)
        toc.append(ch)

    book.toc = tuple(toc)
    book.add_item(epub.EpubNcx())
    book.add_item(epub.EpubNav())
    book.spine = spine

    epub.write_epub(str(OUTPUT_PATH), book, {})
    print(f"EPUB written to: {OUTPUT_PATH}")
    print(f"Chapters: {len(chapter_files)}")


if __name__ == "__main__":
    build_book()
