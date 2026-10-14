# link2epub: Implementation Plan

## 1. Goal

Give link2epub one or more URLs. It fetches each page, pulls out the readable article content (dropping navigation, ads and comments), and writes a valid EPUB 3 file. The file should open cleanly in Apple Books, Kobo, Calibre, KOReader and Send-to-Kindle.

```bash
link2epub https://example.com/some-article -o article.epub
link2epub URL1 URL2 URL3 --title "Weekend Reading" -o reading.epub
```

### Scope for v1
- Single URL produces a single-chapter book; multiple URLs produce one chapter each.
- Main-content extraction with metadata: title, author, date, site name, language.
- Images are downloaded and embedded, so the book works offline.
- Output passes `epubcheck` with no errors.

### Out of scope for v1 (see §9)
- Web UI or hosted service, browser extension.
- Crawling a whole site, or automatically following "next page" links.
- Pages behind a login or paywall.

---

## 2. Technology choices

**Language: Python 3.11+.** Python has the most mature libraries for every stage of this pipeline: extraction, HTML cleanup and EPUB packaging.

| Concern | Library | Why |
|---|---|---|
| HTTP | `httpx` | Timeouts, redirects, HTTP/2, async for parallel image downloads |
| JS-rendered pages (optional) | `playwright` | Only used with `--render`; kept as an optional extra |
| Content extraction | `trafilatura` (primary), `readability-lxml` (fallback) | trafilatura scores best on extraction benchmarks and also returns metadata |
| HTML parsing / XHTML | `lxml` | Fast; serializes well-formed XHTML, which EPUB requires |
| Sanitizing | `nh3` | Allow-list sanitizer (Rust `ammonia` bindings) |
| Images | `Pillow` | Converts unsupported formats, resizes, generates a cover |
| EPUB packaging | `ebooklib` | Writes the OPF, nav doc, NCX and manifest |
| CLI | `typer` | Typed CLI with little code |
| Config / models | `pydantic` | Typed `Article` / `Book` models |
| Testing | `pytest`, `respx` (mocks httpx), `epubcheck` (Java, run in CI) | |
| Packaging | `uv` + `pyproject.toml` | Install with `uvx link2epub` / `pipx install link2epub` |

---

## 3. Architecture

A linear pipeline of small, separately testable stages:

```
 URL(s)
   │
   ▼
┌──────────┐   ┌───────────┐   ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌──────────┐
│  Fetch   │──▶│  Extract  │──▶│  Clean   │──▶│  Assets  │──▶│  Build   │──▶│ Validate │──▶ .epub
│ (httpx / │   │(trafilat. │   │(sanitize,│   │(download,│   │(ebooklib │   │(optional │
│playwright)│  │+metadata) │   │ XHTML)   │   │ convert) │   │ OPF/nav) │   │ epubcheck)│
└──────────┘   └───────────┘   └──────────┘   └──────────┘   └──────────┘   └──────────┘
```

### Core data models (`models.py`)

```python
class FetchedPage(BaseModel):
    url: str            # final URL after redirects
    html: str
    content_type: str
    encoding: str

class Image(BaseModel):
    src_url: str
    filename: str       # e.g. images/3fa2c1.jpg (content hash)
    media_type: str
    data: bytes

class Article(BaseModel):
    url: str
    title: str
    author: str | None
    published: datetime | None
    site_name: str | None
    language: str       # BCP-47, default "en"
    lead_image_url: str | None
    content_xhtml: str  # sanitized XHTML fragment
    images: list[Image]

class Book(BaseModel):
    identifier: str     # urn:uuid, uuid5 of the sorted URLs, so rebuilds are stable
    title: str
    authors: list[str]
    language: str
    chapters: list[Article]
    cover: bytes | None
```

### Project layout

```
link2epub/
├── pyproject.toml
├── README.md
├── src/link2epub/
│   ├── __init__.py
│   ├── cli.py          # typer entry point
│   ├── pipeline.py     # orchestrates the stages
│   ├── models.py
│   ├── fetch.py        # httpx + optional playwright
│   ├── extract.py      # trafilatura → readability fallback, metadata
│   ├── clean.py        # sanitize, URL resolution, lazy-load fixups, XHTML
│   ├── assets.py       # image download/convert/dedupe
│   ├── build.py        # ebooklib packaging, nav, cover
│   ├── cover.py        # generated cover when no lead image exists
│   └── templates/
│       ├── chapter.xhtml   # header with title/byline/source link
│       └── style.css       # minimal, reader-friendly CSS
└── tests/
    ├── fixtures/       # saved HTML pages (blog, news, docs, Medium-style, Substack)
    ├── test_fetch.py
    ├── test_extract.py
    ├── test_clean.py
    ├── test_assets.py
    ├── test_build.py
    └── test_e2e.py     # fixture → .epub → epubcheck
```

---

## 4. Stage details

### 4.1 Fetch (`fetch.py`)
- Use `httpx.Client` with: redirects followed (max 10), timeouts (connect 10 s, read 30 s), a realistic `User-Agent` that `--user-agent` can override, and `Accept-Language` set from `--lang`.
- Reject responses that are not `text/html` or `application/xhtml+xml`, and give a clear error. (PDF input could be a later feature.)
- Cap the body size (default 20 MB) by streaming and aborting past the limit.
- Decode with the charset from the HTTP header, then `<meta charset>`, then charset detection (`charset-normalizer`, which httpx already depends on).
- Retry 429 and 5xx responses twice with backoff.
- `--render`: load the page in headless Chromium (Playwright), wait for `networkidle`, then return `page.content()`. This handles SPA and JS-only sites. Install it as an extra: `pip install link2epub[render]`.
- Auto-fallback: if extraction from the static HTML yields fewer than ~200 characters and Playwright is installed, retry with `--render`.

### 4.2 Extract (`extract.py`)
- `trafilatura.bare_extraction(html, url=..., include_images=True, include_tables=True, include_links=True, output_format="xml")` returns the main content plus metadata.
- If trafilatura returns nothing or very short content, fall back to `readability.Document(html).summary()`.
- Metadata comes from trafilatura first, then falls back to `og:title`, `og:site_name`, `article:author`, `article:published_time`, JSON-LD `Article`/`NewsArticle`, `<title>`, and `<html lang>`.
- `lead_image_url` comes from `og:image` or `twitter:image`.
- `--raw` / `--no-extract`: skip extraction and use the full `<body>`. This helps pages the extractor handles badly, such as documentation and pages that are mostly tables.

### 4.3 Clean (`clean.py`)
EPUB content documents must be **well-formed XHTML**, and reading systems are strict about it. This stage does that work:
1. **Resolve URLs**: make every `href` and `src` absolute against the page's final URL.
2. **Fix lazy-loaded images**: promote `data-src`, `data-original` and `data-lazy-src` to `src`. Pick the best candidate from `srcset`/`<picture><source>`, aiming for about 1600 px wide. Drop 1×1 tracking pixels.
3. **Sanitize** with `nh3`, using an allow-list:
   - Tags: `p h1–h6 blockquote pre code em strong b i u s sub sup a img figure figcaption ul ol li dl dt dd table thead tbody tr th td hr br span div aside`.
   - Attributes: `href`, `src`, `alt`, `title`, `colspan`, `rowspan`, `lang`.
   - Remove `script style iframe form input object embed video audio` and all `on*` handlers.
4. **Replace embeds**: turn YouTube and Twitter iframes into a linked paragraph, e.g. "▶ Video: <title> (link)", so the content isn't silently lost.
5. **Normalize headings**: the chapter title becomes the `<h1>`; demote headings in the body so none are above `<h2>`.
6. **Convert to XHTML**: parse with `lxml.html`, then serialize with `lxml.etree.tostring(method="xml")`. This guarantees closed tags, escaped entities and valid IDs, and avoids HTML-only named entities such as `&nbsp;`, which become numeric references.
7. **Links**: keep external links absolute. Rewrite links that point to another URL in the same book into internal `chapter_N.xhtml#...` links.

### 4.4 Assets (`assets.py`)
- Collect every `<img src>` and the lead image.
- Download them concurrently with `httpx.AsyncClient`, with a semaphore of 8 and `Referer` set to the article URL, since some CDNs need it.
- Name files by content hash (`images/<sha1[:12]>.<ext>`) so duplicates collapse into one file.
- **Formats**:
  - Keep JPEG, PNG, GIF and SVG as they are. These are EPUB core media types.
  - Convert WebP, AVIF, HEIC, BMP and TIFF to JPEG, or to PNG when the image has transparency. WebP is a core media type in EPUB 3.3, but many older e-readers can't display it.
  - Sanitize SVGs: strip scripts and external references.
- **Size**: downscale to at most 1600 px on the long edge and re-encode JPEG at quality 85. The `--max-image-size` and `--grayscale` flags help e-ink devices and Kindle email size limits.
- If a download fails, keep the `<img>` alt text as a placeholder and log a warning rather than failing the whole book.
- `--no-images` skips this stage and removes all `<img>` elements.

### 4.5 Build (`build.py`)
Using `ebooklib`:
- **Metadata**: `dc:identifier` (urn:uuid), `dc:title`, `dc:creator`, `dc:language`, `dc:date`, `dc:source` (the URL), `dc:publisher` (site name), and `dcterms:modified`.
- **One XHTML file per chapter**, rendered from `templates/chapter.xhtml`:
  ```
  <h1>Title</h1>
  <p class="byline">Author · Site · Date</p>
  <p class="source"><a href="URL">URL</a></p>
  …content…
  ```
- A shared `style.css` with readable defaults: serif body, `max-width` images, `pre` wrapping, simple table borders. Don't hard-code font sizes, so the reader's own font settings still apply.
- **Navigation**: write an EPUB 3 `nav.xhtml` and an NCX (for EPUB 2 readers and older Kindles). With one article, build the TOC from its `<h2>`/`<h3>` headings. With several, use one entry per article.
- **Cover**:
  - Single article: use the lead image when it exists, with the title overlaid by Pillow.
  - Otherwise generate a plain typographic cover (title, site, date) at 1600×2560.
  - `--cover PATH` overrides both.
- **Spine**: cover, nav (optional, `--toc-page`), then chapters in input order.
- The default filename is a slug of the title, e.g. `the-article-title.epub`.

### 4.6 Validate
- `--validate` runs `epubcheck` when it is on `PATH` and reports any errors.
- CI always runs epubcheck on the end-to-end fixture outputs.

---

## 5. CLI design (`cli.py`)

```
link2epub [OPTIONS] URL [URL...]

  -o, --output PATH         Output file (default: <slug-of-title>.epub)
  -t, --title TEXT          Book title (default: article title, or "Collection" for multiple)
  -a, --author TEXT         Override author
      --lang TEXT           Language code (default: detected)
      --cover PATH          Custom cover image
      --render              Render with headless Chromium (needs link2epub[render])
      --raw                 Skip readability extraction; use full page body
      --no-images           Don't download or embed images
      --max-image-size INT  Longest edge in px (default 1600)
      --grayscale           Convert images to grayscale (e-ink)
      --user-agent TEXT
      --cookies PATH        Netscape cookies.txt for pages behind simple logins
      --from-file PATH      Read URLs from a file, one per line
      --validate            Run epubcheck on the result
  -v, --verbose
```

Exit codes: `0` success; `1` fetch error; `2` no extractable content; `3` build or validation error. When several URLs are given, a failure on one URL is reported, the rest of the book is still built, and the exit code is non-zero.

`pipeline.py` exposes `build_epub(urls: list[str], options: Options) -> Path`, so a web front end or other caller can reuse the pipeline without going through the CLI.

---

## 6. Edge cases and risks

| Issue | Mitigation |
|---|---|
| JS-only pages (SPAs) | `--render` with Playwright, plus auto-fallback when content is empty |
| Paywalls, cookie walls, consent banners | Sanitizing removes overlays; `--cookies` for sites the user is logged into; otherwise out of scope |
| Bot protection (Cloudflare and similar) | Realistic UA, or `--render`; report a clear error and don't try to bypass protections |
| Malformed HTML | lxml recovers; XHTML serialization guarantees well-formedness |
| Non-UTF-8 pages | Decode via header, then meta tag, then detection; always write UTF-8 |
| Huge pages and images | Size caps on the page and per image; downscaling |
| Math (MathJax/KaTeX) | v1: keep the rendered MathML if present (EPUB 3 supports MathML), otherwise the TeX source as text |
| Code blocks | Keep `<pre><code>`; CSS `white-space: pre-wrap` |
| Right-to-left languages | Carry `lang`/`dir` into the XHTML and set `page-progression-direction` in the OPF |
| Multi-page articles | v2: follow `rel="next"` or an explicit `--follow-next` |
| Copyright | The tool is for personal offline reading; say so in the README. Keep `dc:source` attribution in every chapter |

**Security**, which matters most if a web service is ever added:
- SSRF: in service mode, resolve hostnames and refuse private, loopback and link-local IPs, re-checking after each redirect.
- Never run page scripts except in sandboxed Playwright. Sanitize everything that goes into the EPUB, since some reading systems execute JS.
- Enforce download size and time limits to prevent resource exhaustion.

---

## 7. Testing strategy

- **Unit tests per stage** using saved HTML fixtures. The fixtures cover a WordPress blog, a news site, a Medium/Substack post, a documentation page, a page with lazy images and `srcset`, a non-UTF-8 page, and an RTL page.
- **Fetch tests** mock HTTP with `respx`: redirects, 404, 429 retry, wrong content type, oversized body, charset handling.
- **Golden tests**: compare extracted text and metadata against expected JSON snapshots.
- **End-to-end**: fixture → `.epub`, then assert the zip structure (`mimetype` is the first entry and stored uncompressed, `META-INF/container.xml` exists, all manifest items are present) and run **epubcheck with zero errors**.
- **Optional live smoke tests** (marked `@pytest.mark.network`, not run in CI by default) against a few stable real URLs.
- **CI**: GitHub Actions on Python 3.11–3.13 with Java available for epubcheck; `ruff` for lint and format, `mypy` for types.

---

## 8. Milestones

| # | Milestone | Deliverable |
|---|---|---|
| M0 | Scaffold | `pyproject.toml`, `src/` layout, typer CLI stub, ruff/mypy/pytest, CI workflow |
| M1 | Walking skeleton | Fetch + trafilatura + ebooklib produce a text-only EPUB for one URL |
| M2 | Clean XHTML | Sanitizer, URL resolution, XHTML serialization, chapter template and CSS; passes epubcheck |
| M3 | Images | Lazy-load fixes, async download, format conversion, resizing, dedupe |
| M4 | Metadata and cover | Full metadata fallbacks, generated and lead-image covers, heading-based TOC |
| M5 | Multiple URLs | One chapter per URL, cross-links between chapters, `--from-file`, partial-failure handling |
| M6 | JS rendering | `--render` extra and auto-fallback |
| M7 | Polish and release | README, error messages, `--validate`, publish to PyPI |

Each milestone is a separate PR, and each ends with something usable. M1 alone already does the core job end to end.

---

## 9. Future extensions

- **Web UI / API**: a FastAPI endpoint `POST /convert {urls: [...]}` that returns the `.epub`, plus a minimal HTML form. It reuses `pipeline.build_epub`. Needs the SSRF protection from §6.
- **Send to Kindle / e-reader**: email the EPUB to a Send-to-Kindle address via SMTP, or upload it to Dropbox or Kobo.
- **Browser extension / bookmarklet**: send the current tab's rendered DOM to the service. This handles logged-in pages without sharing cookies.
- **RSS/Atom feeds**: turn the last N feed items into a digest EPUB, possibly scheduled daily.
- **Pagination following**: `--follow-next` for multi-page articles and forum threads.
- **Other outputs**: KEPUB for Kobo, PDF via the same pipeline.
- **Caching**: an on-disk HTTP cache so rebuilding a collection doesn't download everything again.

---

## 10. Open questions

1. **Language**: this plan assumes Python. A Node/TypeScript stack (`@mozilla/readability` + `jsdom` + `epub-gen-memory`) would be the alternative if you want it to live in a JS ecosystem.
2. **Interface**: is a CLI the right first deliverable, or is a web UI needed sooner?
3. **Target devices**: are Kindle or e-ink the main targets? If so, grayscale, smaller images and avoiding WebP should be the defaults.
4. **JS-heavy sites**: how important are they? This decides whether Playwright stays an optional extra or becomes a core dependency.
