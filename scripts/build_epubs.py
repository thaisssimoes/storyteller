"""Build EPUB files for wolfsbane-bride and vow-of-the-bloodthorne."""
import os
import re
import glob
import uuid
import html

from ebooklib import epub


ROOT = r"c:\Users\tsuba\GolandProjects\InconsistencyFixer\output\writer"
OUT_DIR = ROOT
JOBS = [
    {
        "title": "Wolfsbane Bride",
        "slug": "wolfsbane-bride",
        "src": os.path.join(ROOT, "Completed-stories", "wolfsbane-bride", "chapters"),
        "out": os.path.join(OUT_DIR, "Wolfsbane-Bride.epub"),
        "cover": None,
    },
    {
        "title": "Vow of the Bloodthorne",
        "slug": "vow-of-the-bloodthorne",
        "src": os.path.join(ROOT, "Completed-stories", "vow-of-the-bloodthorne", "chapters"),
        "out": os.path.join(OUT_DIR, "Vow-of-the-Bloodthorne.epub"),
        "cover": os.path.join(ROOT, "Completed-stories", "vow-of-the-bloodthorne", "cover.jpg"),
    },
]


CSS = """\
body { font-family: Georgia, serif; line-height: 1.55; margin: 0 1em; }
h1 { text-align: center; margin: 1.5em 0 0.8em 0; font-size: 1.6em; }
p { text-indent: 1.2em; margin: 0.15em 0; text-align: justify; }
p.pov { text-align: center; font-style: italic; text-indent: 0; margin: 0.5em 0 1.2em 0; }
p.scene { text-align: center; text-indent: 0; margin: 1em 0; letter-spacing: 0.3em; }
p.first { text-indent: 0; }
"""


def collect_chapters(src_dir):
    files = sorted(
        f for f in glob.glob(os.path.join(src_dir, "chapter_*.txt"))
        if "~$" not in os.path.basename(f)
    )
    out = []
    for path in files:
        m = re.search(r"chapter_(\d+)\.txt$", os.path.basename(path))
        if not m:
            continue
        num = int(m.group(1))
        with open(path, "r", encoding="utf-8") as fh:
            text = fh.read()
        out.append((num, text))
    return out


def parse_chapter(num, raw_text):
    lines = raw_text.splitlines()
    title = None
    i = 0
    while i < len(lines) and not lines[i].strip():
        i += 1
    if i < len(lines):
        first = lines[i].strip()
        if first.startswith("#"):
            title = re.sub(r"^#+\s*", "", first)
            i += 1
    if not title or not title.strip():
        title = f"Chapter {num}"

    body_text = "\n".join(lines[i:]).strip()
    blocks = re.split(r"\n\s*\n", body_text)
    out = []
    for block in blocks:
        block = block.strip()
        if not block:
            continue
        if re.fullmatch(r"\*\s*\*\s*\*", block):
            out.append(("scene", "* * *"))
            continue
        joined = " ".join(line.strip() for line in block.splitlines() if line.strip())
        if re.fullmatch(r"[A-Z][A-Za-z'’\- ]+'s POV", joined):
            out.append(("pov", joined))
            continue
        out.append(("para", joined))
    return title, out


def chapter_html(num, title, blocks):
    head = title
    if not re.match(rf"^Chapter\s+{num}\b", title, re.IGNORECASE):
        head = f"Chapter {num}: {title}"

    body = [f"<h1>{html.escape(head)}</h1>"]
    first_para_emitted = False
    for kind, text in blocks:
        esc = html.escape(text)
        if kind == "pov":
            body.append(f'<p class="pov">{esc}</p>')
            first_para_emitted = False
        elif kind == "scene":
            body.append(f'<p class="scene">{esc}</p>')
            first_para_emitted = False
        else:
            cls = "" if first_para_emitted else ' class="first"'
            body.append(f"<p{cls}>{esc}</p>")
            first_para_emitted = True
    return head, "\n".join(body)


def build_epub(job):
    chapters = collect_chapters(job["src"])
    if not chapters:
        print(f"  No chapters found in {job['src']}")
        return

    book = epub.EpubBook()
    book.set_identifier(str(uuid.uuid4()))
    book.set_title(job["title"])
    book.set_language("en")
    book.add_author("")

    cover_path = job.get("cover")
    if cover_path and os.path.exists(cover_path):
        with open(cover_path, "rb") as fh:
            book.set_cover("cover.jpg", fh.read())

    css_item = epub.EpubItem(
        uid="style_main", file_name="style/main.css",
        media_type="text/css", content=CSS,
    )
    book.add_item(css_item)

    spine = ["nav"]
    toc = []
    for num, raw in chapters:
        title, blocks = parse_chapter(num, raw)
        head, body_html = chapter_html(num, title, blocks)
        c = epub.EpubHtml(
            title=head,
            file_name=f"chap_{num:04d}.xhtml",
            lang="en",
        )
        c.content = (
            '<?xml version="1.0" encoding="utf-8"?>\n'
            '<!DOCTYPE html>\n'
            '<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en" lang="en">\n'
            f'<head><title>{html.escape(head)}</title>'
            '<link rel="stylesheet" type="text/css" href="style/main.css"/></head>\n'
            f'<body>\n{body_html}\n</body>\n</html>\n'
        ).encode("utf-8")
        c.add_item(css_item)
        book.add_item(c)
        spine.append(c)
        toc.append(c)

    book.toc = tuple(toc)
    book.add_item(epub.EpubNcx())
    book.add_item(epub.EpubNav())
    book.spine = spine

    epub.write_epub(job["out"], book)
    size_kb = os.path.getsize(job["out"]) / 1024
    print(f"  Wrote {job['out']} ({len(chapters)} chapters, {size_kb:,.0f} KB)")


def main():
    for job in JOBS:
        print(f"Building: {job['title']}")
        build_epub(job)


if __name__ == "__main__":
    main()
