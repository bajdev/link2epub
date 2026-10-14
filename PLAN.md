# link2epub: Implementation Plan

## 1. Goal

link2epub is a command-line program written in Go. You give it a web page URL and a path on your computer. It downloads the page, extracts the readable article content (dropping navigation, ads, comments and similar clutter), and writes a valid EPUB 3 file to that path.

```bash
link2epub -URL https://example.com/some-article -SAVE ~/Books/some-article.epub
link2epub -URL https://example.com/some-article -SAVE ~/Books/      # a directory: filename comes from the article title
```

### Scope for v1
- One URL in, one EPUB out.
- Extracts the main content plus metadata: title, author, date, site name, language.
- Images are downloaded and embedded, so the book works offline.
- The output passes `epubcheck` (the standard EPUB validator) with zero errors.
- Ships as a single static binary with no runtime dependencies.

### Out of scope for v1 (see §9)
- Several URLs in one book, crawling a site, following "next page" links.
- Pages that only show content after JavaScript runs.
- Pages behind a login or paywall.
- A web UI or hosted service.

---

## 2. Command-line interface

The program uses Go's standard-library `flag` package. Flag names are case-sensitive, exactly `-URL` and `-SAVE`. (`flag` also accepts `--URL` and `--SAVE`.)

```
Usage: link2epub -URL <url> -SAVE <path>

  -URL string
        Web page to convert (required). Must be http:// or https://.
  -SAVE string
        Where to write the EPUB (required).
          - Ends in .epub        → written to exactly that file
          - Existing directory   → <dir>/<slug-of-article-title>.epub
          - Anything else        → treated as a file path; ".epub" is appended if missing
```

How the flags behave:
- **Validation**:
  - Missing `-URL` or `-SAVE` prints the usage text and exits with code `2`.
  - A URL that doesn't parse, or isn't http/https, is rejected before anything is fetched.
  - Any positional arguments left over are an error, which catches typos like `-url`.
- **The `-SAVE` path**:
  - A leading `~` is expanded, in case the shell didn't do it (for example, when the path is quoted).
  - The parent directory must already exist. The program doesn't create directory trees on its own.
  - The file is written to a temporary file in the same directory, then renamed into place. A failed run never leaves a half-written `.epub` behind.
  - If the target file already exists, it is overwritten. Adding a `-force` flag later is an option; see §10.
- **Exit codes**:

  | Code | Meaning |
  |---|---|
  | `0` | Success. The absolute path of the written file is printed to stdout. |
  | `1` | Fetch failed (network error, non-200 status, page isn't HTML). |
  | `2` | Bad arguments. |
  | `3` | No readable content could be extracted. |
  | `4` | Building or writing the EPUB failed. |

- **Output**: progress and warnings go to stderr (for example, `warning: image https://… failed: 404`), so stdout stays clean for scripting.

---

## 3. Technology choices

The project targets Go 1.24 or newer. The module path is `github.com/bajdev/link2epub`.

| Concern | Package | Why |
|---|---|---|
| CLI | `flag` (stdlib) | Matches the `-URL` / `-SAVE` style with no extra dependency |
| HTTP | `net/http` (stdlib) | Timeouts, redirects and proxy support are built in |
| Charset decoding | `golang.org/x/net/html/charset` | Picks the encoding from the HTTP header, then the `<meta>` tag, then detection |
| HTML parsing / DOM work | `golang.org/x/net/html` + `github.com/PuerkitoBio/goquery` | Standard parser, plus jQuery-style selection for the cleanup passes |
| Content extraction | `github.com/markusmobius/go-trafilatura` | Go port of trafilatura. Returns metadata (title, author, date, site name, language, image), and already falls back to readability and DOM-distiller internally |
| Sanitizing | `github.com/microcosm-cc/bluemonday` | Mature allow-list HTML sanitizer |
| Images | `image/jpeg`, `image/png`, `image/gif` (stdlib), `golang.org/x/image/webp`, `golang.org/x/image/draw` | Decoding, WebP support and high-quality resizing |
| AVIF images (optional) | `github.com/gen2brain/avif` | Pure Go (WASM-based), so the binary still needs no cgo |
| EPUB packaging | `github.com/go-shiori/go-epub` | Maintained fork of `bmaupin/go-epub`. Writes EPUB 3 with a nav document, an NCX, cover, CSS and images |
| Book identifier | `github.com/google/uuid` | `uuid.NewSHA1` gives a stable ID for each URL |
| Testing | `testing` + `net/http/httptest` (stdlib), `epubcheck` in CI | A real local HTTP server instead of mocks |
| Lint | `go vet`, `staticcheck` or `golangci-lint` | |
| Release | `goreleaser` | Static binaries for linux, macOS and windows on amd64 and arm64 |

The build uses no cgo (`CGO_ENABLED=0`), so `go install github.com/bajdev/link2epub/cmd/link2epub@latest` works everywhere.

---

## 4. Architecture

The program is a straight pipeline of small packages under `internal/`. Each package can be tested on its own:

```
 -URL
   │
   ▼
┌─────────┐   ┌──────────┐   ┌─────────┐   ┌─────────┐   ┌─────────┐   ┌─────────┐
│  fetch  │──▶│ extract  │──▶│  clean  │──▶│ assets  │──▶│  build  │──▶│  save   │──▶ -SAVE path
│net/http │   │trafilatura│  │bluemonday│  │download,│   │ go-epub │   │temp file│
│+charset │   │+metadata │   │→ XHTML  │   │convert  │   │         │   │+ rename │
└─────────┘   └──────────┘   └─────────┘   └─────────┘   └─────────┘   └─────────┘
```

### Project layout

```
link2epub/
├── go.mod
├── go.sum
├── README.md
├── PLAN.md
├── cmd/link2epub/
│   └── main.go              # flag parsing, -SAVE resolution, exit codes
├── internal/
│   ├── pipeline/pipeline.go # Run(ctx, url) → *Book; wires the stages together
│   ├── fetch/fetch.go       # HTTP client, size limits, charset decoding
│   ├── extract/extract.go   # go-trafilatura + metadata fallbacks
│   ├── clean/clean.go       # URL resolution, lazy images, sanitizing, XHTML output
│   ├── assets/assets.go     # concurrent image download, conversion, resizing
│   ├── build/
│   │   ├── build.go         # go-epub packaging
│   │   ├── cover.go         # generated cover when the article has no lead image
│   │   ├── chapter.tmpl     # title / byline / source header
│   │   └── style.css        # embedded with //go:embed
│   └── save/save.go         # path resolution, slugify, atomic write
└── testdata/
    ├── pages/               # saved HTML: blog, news, Substack, docs, lazy images, Shift-JIS, RTL
    └── golden/              # expected extraction output (title, author, text)
```

### Core types (`internal/pipeline`)

```go
type Page struct {
    URL         *url.URL // final URL after redirects
    HTML        string   // decoded to UTF-8
    ContentType string
}

type Image struct {
    SourceURL string
    Filename  string // "images/3fa2c1d9e0ab.jpg" (content hash)
    MediaType string // image/jpeg, image/png, ...
    Data      []byte
}

type Article struct {
    URL          string
    Title        string
    Author       string
    Published    time.Time
    SiteName     string
    Language     string // BCP-47, default "en"
    LeadImageURL string
    ContentXHTML string // sanitized, well-formed XHTML fragment
    Images       []Image
}
```

The `main` function stays thin:

```go
func main() {
    urlFlag  := flag.String("URL", "", "web page to convert (required)")
    saveFlag := flag.String("SAVE", "", "file or directory to write the EPUB to (required)")
    flag.Parse()
    // validate → ctx with signal.NotifyContext(SIGINT) → pipeline.Run → save.Write → print path
}
```

---

## 5. Stage details

### 5.1 fetch
- Uses one `http.Client` with:
  - a 60 s overall timeout, plus dial and TLS-handshake timeouts;
  - redirects followed up to 10 times;
  - the proxy taken from the environment (`http.ProxyFromEnvironment`).
- Sends request headers:
  - a realistic `User-Agent`, because some sites return 403 to Go's default UA;
  - `Accept: text/html,application/xhtml+xml`.
- Rejects any status other than 2xx, and any content type other than `text/html` or `application/xhtml+xml`.
- Caps the body at 20 MB (`io.LimitReader`), with a clear error if the page is larger.
- Converts the body to UTF-8 with `charset.NewReader(body, contentType)`.
- Retries twice, with backoff, on 429 and 5xx responses and on temporary network errors.
- Every request uses the `context.Context`, so Ctrl-C cancels cleanly.

### 5.2 extract
- Calls `trafilatura.Extract(reader, trafilatura.Options{OriginalURL: u, IncludeImages: true, IncludeLinks: true, EnableFallback: true})`.
  - The result's `ContentNode` (an `*html.Node`) is the article body.
  - The result's `Metadata` holds the title, author, date, site name, language and image.
- When trafilatura leaves a metadata field empty, it is filled from the page with goquery:
  - `og:title`, `og:site_name`, `article:author`, `article:published_time`;
  - JSON-LD `Article` / `NewsArticle` / `BlogPosting`;
  - `<title>` and `<html lang>`.
- `LeadImageURL` comes from `og:image`, then `twitter:image`.
- If the extracted text is under about 200 characters, extraction fails with exit code `3`. The error message suggests the page may need JavaScript, which is not supported in v1.

### 5.3 clean
EPUB content files must be **well-formed XHTML**, and e-readers are strict about it. This stage works on the `*html.Node` tree:
1. **Make URLs absolute**: resolve every `href` and `src` against the final page URL with `url.ResolveReference`.
2. **Fix lazy-loaded images**:
   - Move `data-src`, `data-original` or `data-lazy-src` into `src` when one is present.
   - Pick the candidate from `srcset` or `<picture><source>` that is closest to 1600 px wide.
   - Remove 1×1 tracking pixels, and images without a usable `src`.
3. **Replace embeds**: turn YouTube, Vimeo and Twitter/X iframes into a paragraph link (`▶ Video: <link>`), so the reader knows something was there.
4. **Sanitize** with a `bluemonday` policy built from an allow-list:
   - Tags: `p h1–h6 blockquote pre code em strong b i u s sub sup a img figure figcaption ul ol li dl dt dd table thead tbody tfoot tr th td hr br span div aside abbr cite q small`.
   - Attributes: `href` on `a`; `src`, `alt` and `title` on `img`; `colspan` and `rowspan` on cells; `lang` and `dir` globally.
   - Everything else is removed: `script`, `style`, `iframe`, `form`, `video`, `on*` handlers, `class`, `id` and inline styles.
5. **Normalize headings**: the article title becomes the chapter's only `<h1>`. Body headings shift down so none sits above `<h2>`.
6. **Serialize to XHTML**: render with `html.Render`, which closes void elements (`<br/>`), quotes attributes and escapes `& < >`. Then check the result with `encoding/xml`'s decoder. If it isn't well-formed, fail loudly in tests rather than producing an EPUB that e-readers refuse to open. HTML named entities such as `&nbsp;` are never emitted, because the content is written as raw UTF-8.

### 5.4 assets
- Collects every `<img src>` in the cleaned content, plus the lead image.
- Downloads them concurrently with an `errgroup` and a limit of 8 at a time. Each request sends `Referer: <article URL>`, because some CDNs require it, and each image is capped at 10 MB.
- Names files by content hash (`images/<sha1[:12]>.<ext>`), so duplicate images are stored once. Each `src` is rewritten to its local filename.
- **Formats**:
  - JPEG, PNG and GIF stay as they are. These are EPUB core media types.
  - WebP and AVIF are decoded and re-encoded: as JPEG at quality 85 for opaque images, or as PNG when the image has transparency. WebP is allowed in EPUB 3.3, but many older e-readers can't show it.
  - SVG is kept only after a basic cleanup: `<script>`, `on*` attributes and external references are removed.
  - Images in any other format are dropped, and their alt text is kept as a placeholder.
- **Size**: images larger than 1600 px on the long edge are downscaled with `draw.CatmullRom`.
- A failed image download is a warning on stderr, never a fatal error. The `<img>` becomes `<p class="missing-image">[Image: alt text]</p>`.

### 5.5 build
Uses `go-epub`:
- `epub.NewEpub(title)`, then sets:
  - author, language, description, and a UUID identifier (`uuid.NewSHA1` of the URL, so rebuilding the same URL gives the same ID);
  - `dc:source` set to the URL, and the publisher set to the site name.
- `AddCSS` with the embedded `style.css`:
  - a serif body font, `img { max-width: 100% }`, `pre { white-space: pre-wrap }`, simple table borders;
  - no fixed font sizes, so the reader's own font settings still apply.
- Adds every image with `AddImage`, from a local temp file or data URL, so go-epub never downloads anything itself.
- Adds one chapter with `AddSection(body, title, "chapter.xhtml", cssPath)`. The body is `chapter.tmpl` filled in:
  ```html
  <h1>{{.Title}}</h1>
  <p class="byline">{{.Author}} · {{.SiteName}} · {{.Published}}</p>
  <p class="source"><a href="{{.URL}}">{{.URL}}</a></p>
  {{.ContentXHTML}}
  ```
- Builds the **table of contents** from the article's `<h2>` and `<h3>` headings:
  - inject `id` anchors into those headings;
  - add `AddSubSection` entries pointing to them.
  - go-epub writes both the EPUB 3 `nav.xhtml` and an NCX for older readers.
- The **cover** uses the lead image when one exists. Otherwise a simple typographic cover is generated: 1600×2560, showing the title and site name, drawn with `image/draw` and `golang.org/x/image/font` using an embedded open-licensed font. It is set with `SetCover`.

### 5.6 save
- Resolves the final path using the `-SAVE` rules in §2.
- Builds the filename slug from the title: lowercase ASCII, with runs of other characters collapsed to `-`, at most 80 characters. If the slug comes out empty, the filename falls back to `article.epub`.
- Calls `e.WriteTo()` into an `os.CreateTemp(dir, ".link2epub-*")` file, then `fsync`, then `os.Rename` to the final path. The temp file is removed on any error.

---

## 6. Edge cases and risks

| Issue | Handling in v1 |
|---|---|
| Pages that need JavaScript (SPAs) | Detect near-empty extraction, exit with code `3` and a clear message. JavaScript rendering with `chromedp` is a future option (§9) |
| Paywalls and cookie or consent walls | Overlays are stripped by sanitizing. Content the server never sends is out of scope |
| Bot protection (Cloudflare and similar) | Use a realistic UA. Otherwise report the 403 clearly. Never try to get around it |
| Malformed HTML | `x/net/html` follows the HTML5 parsing algorithm, so it always produces a tree |
| Non-UTF-8 pages | Decoded with `x/net/html/charset`. Output is always UTF-8 |
| Huge pages or images | 20 MB page cap, 10 MB per image, downscaling |
| Math (MathJax/KaTeX) | Keep MathML if the page has it (EPUB 3 supports MathML). Otherwise keep the TeX source as text |
| Code blocks | Keep `<pre><code>`, with CSS `pre-wrap` |
| Right-to-left languages | Carry `lang` and `dir` through, and set the spine's `page-progression-direction` |
| Copyright | For personal offline reading. Every book records the source URL in `dc:source` and in the chapter header |

**Security**
- Page scripts never run.
- Everything that goes into the EPUB is sanitized, because some e-readers run JavaScript inside books.
- Download sizes and times are capped.
- `-SAVE` is used exactly as given. Nothing taken from the page (such as the title) can push the output outside the chosen directory, because the slug never contains path separators.

---

## 7. Testing strategy

- **Unit tests per package**, written as table-driven tests in Go:
  - `fetch`: `httptest.Server` covering redirects, 404, 429 retry, wrong content type, oversized body, Shift-JIS and ISO-8859-1 pages.
  - `extract`: golden tests over `testdata/pages/*.html`, compared with `testdata/golden/*.json`. A `-update` flag regenerates the golden files.
  - `clean`: lazy-image and `srcset` selection, sanitizing, heading changes, and an XHTML well-formedness check on every fixture.
  - `assets`: WebP→JPEG, transparent→PNG, resizing, deduplication, failed downloads.
  - `save`: `-SAVE` resolution (`.epub` file, existing directory, missing extension, missing parent directory, `~`), slug generation, the atomic write leaving nothing behind on error.
- **End-to-end** (`cmd/link2epub`): an `httptest.Server` serves a fixture page and its images. The test runs the CLI with `-URL` and `-SAVE`, then checks:
  - the exit code;
  - the zip layout: `mimetype` is the first entry and stored uncompressed, `META-INF/container.xml` exists, every manifest item is present;
  - `epubcheck` reports zero errors.
- **CLI flag tests**: missing flags, bad URL, extra arguments, and the exit code for each.
- **CI (GitHub Actions)**:
  - `go vet`, `staticcheck` and `go test -race ./...`;
  - epubcheck (Java) for the end-to-end test;
  - a `goreleaser` dry run.

---

## 8. Milestones

| # | Milestone | Deliverable |
|---|---|---|
| M0 | Scaffold | `go.mod`, layout, `main.go` with `-URL`/`-SAVE` parsing and validation, CI workflow |
| M1 | Basic working version | fetch, trafilatura and go-epub produce a text-only EPUB saved to `-SAVE` |
| M2 | Clean XHTML | Sanitizer, URL resolution, heading changes, chapter template and CSS. epubcheck passes in CI |
| M3 | Images | Lazy-image fixes, concurrent download, WebP/AVIF conversion, resizing, deduplication |
| M4 | Metadata and cover | Metadata fallbacks, lead-image or generated cover, heading-based table of contents |
| M5 | Polish and release | Atomic save, error messages and exit codes, README, goreleaser binaries |

Each milestone is a separate PR. After M1 the core job already works from end to end.

---

## 9. Future extensions

- **JavaScript rendering**: a `-RENDER` flag using `chromedp` to drive a local Chrome/Chromium for SPA pages.
- **Several URLs in one book**: allow `-URL` to be repeated (a custom `flag.Value`), with one chapter per URL, or read URLs from a file.
- **More flags**:
  - `-TITLE` and `-AUTHOR` overrides;
  - `-COVER` for a custom cover image;
  - `-NOIMAGES`;
  - `-GRAYSCALE` for e-ink;
  - `-TIMEOUT`;
  - `-FORCE` to refuse overwrites unless set.
- **Follow pagination**: follow `rel="next"` links for multi-page articles.
- **Send to Kindle**: email the result over SMTP.
- **RSS/Atom digest**: turn the last N feed items into one EPUB.
- **Web service**: an `http.Handler` around `pipeline.Run`. This would need SSRF protection: refuse private and loopback IP addresses, and re-check after each redirect.

---

## 10. Open questions

1. **Overwriting**: when the `-SAVE` file already exists, should link2epub overwrite it (the current plan) or refuse unless `-FORCE` is given?
2. **Missing directories**: should a `-SAVE` path whose parent directory doesn't exist be created automatically (`os.MkdirAll`), or rejected (the current plan)?
3. **Target devices**: if Kindle or e-ink readers are the main target, images could default to grayscale and smaller sizes.
