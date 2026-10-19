# link2epub: Implementation Plan

## 1. Goal

link2epub is a command-line program written in Go. You give it a web page URL and a path on your computer. It downloads the page, extracts the readable article content (dropping navigation, ads, comments and similar clutter), and writes a valid EPUB 3 file to that path.

```bash
link2epub -URL https://example.com/some-article -SAVE ~/Books/some-article.epub
link2epub -URL https://example.com/some-article -SAVE ~/Books/      # a folder (created if missing): filename comes from the article title
link2epub -URL https://example.com/some-article -SAVE ~/Books/a.epub -FORCE   # replace an existing file
link2epub -URL https://example.com/some-article -SAVE /media/$USER/KOBOeReader/Articles/ -KEPUB   # Kobo-optimised
```

### Target devices

The primary targets are two grayscale e-ink readers. Every output decision (image format, image size, CSS, cover) is tuned for them first:

| | reMarkable 2 | Kobo Clara HD |
|---|---|---|
| Screen | 10.3", 1872×1404 px, 226 PPI | 6", 1448×1072 px, 300 PPI |
| Colour | Grayscale only | Grayscale only (16 levels, E Ink Carta) |
| Aspect ratio | 3:4 | ≈3:4 |
| EPUB rendering | Reflows EPUB with its own renderer; limited CSS support | Plain `.epub` uses the older Adobe RMSDK engine; `.kepub.epub` uses Kobo's faster WebKit engine |
| Getting the book onto it | You upload the saved file yourself (desktop or mobile app, or the USB web interface at `http://10.11.99.1`). link2epub only makes the file | USB mass storage (FAT32): `-SAVE` straight onto the device, or copy the file |

Both screens share a 3:4 aspect ratio, so one image size and one cover size fit both. The default plain EPUB must work on both devices. `-KEPUB` produces a Kobo-optimised `.kepub.epub` instead (§5.7). Use it for the Clara HD, and plain EPUB for the reMarkable.

### Scope for v1
- One URL in, one EPUB out.
- Extracts the main content plus metadata: title, author, date, site name, language.
- Images are downloaded and embedded, so the book works offline. They are converted to grayscale and sized for the target screens.
- Never overwrites an existing file unless `-FORCE` is given. Missing output folders are created.
- `-KEPUB` writes a Kobo KEPUB (`.kepub.epub`) instead of a plain EPUB.
- The output passes `epubcheck` (the standard EPUB validator) with zero errors.
- Ships as a single static binary with no runtime dependencies.

### Out of scope for v1 (see §9)
- Several URLs in one book (added in M7, §8). Crawling a site and following "next page" links stay out of scope.
- Pages that only show content after JavaScript runs.
- Pages behind a login or paywall.
- Uploading to a device. link2epub stays focused on making the file; you copy it over yourself.
- A web UI or hosted service.

---

## 2. Command-line interface

The program uses Go's standard-library `flag` package. Flag names are case-sensitive: exactly `-URL`, `-SAVE`, `-FORCE` and `-KEPUB`. (`flag` also accepts `--URL` and the other double-dash forms.)

```
Usage: link2epub -URL <url> -SAVE <path> [-FORCE] [-KEPUB]

  -URL string
        Web page to convert (required). Must be http:// or https://.
  -SAVE string
        Where to write the EPUB (required). Missing folders are created.
          - Ends in "/"            → a folder: <folder>/<slug-of-article-title>.epub
          - An existing folder     → same as above
          - Ends in .epub          → written to exactly that file
          - Anything else          → treated as a file path; ".epub" is appended
        With -KEPUB, the extension is ".kepub.epub" instead (see below).
  -FORCE
        Overwrite the output file if it already exists. Without it, an
        existing file is never touched and the program exits with code 5.
  -KEPUB
        Write a Kobo KEPUB (.kepub.epub) instead of a plain EPUB. Kobo
        devices open it with their faster WebKit renderer. Use it for the
        Kobo Clara HD; leave it off for the reMarkable.
```

How the flags behave:
- **Validation**:
  - Missing `-URL` or `-SAVE` prints the usage text and exits with code `2`.
  - A URL that doesn't parse, or isn't http/https, is rejected before anything is fetched.
  - Any positional arguments left over are an error, which catches typos like `-url`.
- **The `-SAVE` path**:
  - A leading `~` is expanded, in case the shell didn't do it (for example, when the path is quoted).
  - A path that doesn't exist yet is a file unless it ends in `/`. So `-SAVE ~/Books/new/` creates the folder `new`, while `-SAVE ~/Books/new` writes `~/Books/new.epub`.
  - **Folders are created automatically** (`os.MkdirAll`, mode `0755`), including any missing parent folders. They are created only at save time, so a run that fails earlier (for example, the download fails) doesn't leave empty folders behind.
  - If part of the path exists as a regular file (for example, `~/Books` is a file), the program fails with exit code `4` and a clear message.
- **Filenames with `-KEPUB`**: Kobo picks its renderer from the file extension. A KEPUB only gets the faster renderer if the file ends in `.kepub.epub`, so the extension is set automatically:
  - Folder targets get `<slug>.kepub.epub`.
  - `-SAVE x.kepub.epub` is used as is.
  - `-SAVE x.epub` becomes `x.kepub.epub`, with a note on stderr. Otherwise the Kobo would open the file with the old renderer and the conversion would be wasted.
  - Any other path gets `.kepub.epub` appended.
  - A `-SAVE` path ending in `.kepub.epub` **without** `-KEPUB` is an error (exit code `2`, "add -KEPUB or use a .epub filename"). A plain EPUB with that name would be opened by the Kobo's KEPUB renderer and display badly.
  - The overwrite check below always uses the final name, after the extension is adjusted.
- **Overwrite protection (`-FORCE`)**:
  - **Without `-FORCE`**:
    - An existing file at the output path is never modified. The program prints `error: <path> already exists (use -FORCE to overwrite)` and exits with code `5`.
    - The check runs as early as possible, to avoid wasted downloads. When `-SAVE` names a file, the check runs before the page is fetched. When `-SAVE` names a folder, the filename depends on the article title, so the check runs right after extraction and before any images are downloaded.
    - The final write is also race-free, in case another program creates the file mid-run. The name is first reserved with `O_CREATE|O_EXCL`, which fails if the file exists. The book is then written to a temp file in the same folder and renamed over the reservation. If anything fails, both the reservation and the temp file are removed.
  - **With `-FORCE`**:
    - The existing file is replaced atomically (written to a temp file, then renamed into place). Until the new file is fully written, the old one stays intact.
    - `-FORCE` never deletes a folder. If the output path is an existing folder, the "existing folder" rule from `-SAVE` applies instead.
  - Either way, a failed run never leaves a half-written `.epub` behind.
- **Exit codes**:

  | Code | Meaning |
  |---|---|
  | `0` | Success. The absolute path of the written file is printed to stdout. |
  | `1` | Fetch failed (network error, non-200 status, page isn't HTML). |
  | `2` | Bad arguments. |
  | `3` | No readable content could be extracted. |
  | `4` | Building or writing the EPUB failed (including folder creation). |
  | `5` | The output file already exists and `-FORCE` was not given. |

- **Output**: progress and warnings go to stderr (for example, `warning: image https://… failed: 404`), so stdout stays clean for scripting.
- **Saving straight to a Kobo**: `-SAVE /media/<user>/KOBOeReader/Articles/` works, because the Kobo mounts as a FAT32 USB drive. Filenames are already restricted to ASCII letters, digits and `-`, which FAT32 accepts. The folder is created on the device if it's missing.

---

## 3. Technology choices

The project targets Go 1.26 or newer. The module path is `github.com/bajdev/link2epub`.

| Concern | Package | Why |
|---|---|---|
| CLI | `flag` (stdlib) | Matches the `-URL` / `-SAVE` / `-FORCE` style with no extra dependency |
| HTTP | `net/http` (stdlib) | Timeouts, redirects and proxy support are built in |
| Charset decoding | `golang.org/x/net/html/charset` | Picks the encoding from the HTTP header, then the `<meta>` tag, then detection |
| HTML parsing / DOM work | `golang.org/x/net/html` | Standard HTML5 parser; the cleanup passes walk its node tree directly (goquery turned out to be unnecessary) |
| Content extraction | `github.com/markusmobius/go-trafilatura` | Go port of trafilatura. Returns metadata (title, author, date, site name, language, image), and already falls back to readability and DOM-distiller internally |
| Sanitizing | `github.com/microcosm-cc/bluemonday` | Mature allow-list HTML sanitizer |
| Images | `image/jpeg`, `image/png`, `image/gif`, `image/color/palette` (stdlib), `golang.org/x/image/webp`, `golang.org/x/image/draw` | Decoding (WebP included), high-quality resizing, grayscale conversion and 16-level quantizing |
| SVG images | `github.com/srwiley/oksvg` + `github.com/srwiley/rasterx` | Pure-Go SVG rasterizer. SVGs are turned into grayscale PNGs, because neither target renders SVG reliably |
| AVIF images (optional) | `github.com/gen2brain/avif` | Pure Go (WASM-based), so the binary still needs no cgo |
| EPUB packaging | `github.com/go-shiori/go-epub` | Maintained fork of `bmaupin/go-epub`. Writes EPUB 3 with a nav document, an NCX, cover, CSS and images |
| Cover text | `golang.org/x/image/font/opentype` + the Go fonts (`golang.org/x/image/font/gofont`, BSD licence) | Draws the title on generated covers. The fonts ship inside `x/image`, so there is no font file to add |
| KEPUB output (`-KEPUB`, §5.7) | `github.com/pgaskin/kepubify/v4/kepub` | The standard Go library for converting EPUB to Kobo's KEPUB format |
| Book identifier | `github.com/google/uuid` | `uuid.NewSHA1` gives a stable ID for each URL |
| Testing | `testing` + `net/http/httptest` (stdlib), `epubcheck` in CI | A real local HTTP server instead of mocks |
| Lint | `go vet`, `staticcheck` or `golangci-lint` | |
| Release | `goreleaser` v2 | Static binaries for linux, macOS and windows on amd64 and arm64, with checksums; a version tag creates a draft GitHub release |

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
                                                                  │  -KEPUB  ▲
                                                                  └─▶ kepub ─┘
                                                                     (kepubify)
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
│   ├── device/profile.go    # e-ink target constants (image box, gray levels, cover size)
│   ├── assets/assets.go     # concurrent image download, grayscale conversion, resizing
│   ├── build/
│   │   ├── build.go         # go-epub packaging
│   │   ├── cover.go         # generated cover when the article has no lead image
│   │   ├── chapter.tmpl     # title / byline / source header
│   │   └── style.css        # embedded with //go:embed
│   ├── kepub/kepub.go       # -KEPUB conversion via kepubify (§5.7)
│   └── save/save.go         # path resolution, folder creation, -FORCE handling, atomic write
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

### Device profile (`internal/device`)

The e-ink tuning lives in one place, so a different device can be supported later by adding a second profile:

```go
// EInk targets reMarkable 2 (1404×1872) and Kobo Clara HD (1072×1448).
// Both are 3:4 grayscale, so images are sized to the larger screen and
// the Kobo scales them down.
var EInk = Profile{
    MaxImageWidth:  1404,
    MaxImageHeight: 1872,
    CoverWidth:     1404,
    CoverHeight:    1872,
    Grayscale:      true,
    GrayLevels:     16,   // both panels display 16 shades
    JPEGQuality:    80,
}
```

The `main` function stays thin:

```go
func main() {
    urlFlag  := flag.String("URL", "", "web page to convert (required)")
    saveFlag := flag.String("SAVE", "", "file or folder to write the EPUB to (required)")
    force    := flag.Bool("FORCE", false, "overwrite the output file if it already exists")
    kepubOut := flag.Bool("KEPUB", false, "write a Kobo KEPUB (.kepub.epub) instead of a plain EPUB")
    flag.Parse()
    // validate → resolve -SAVE (extension depends on -KEPUB) → early exists-check (unless -FORCE)
    // → ctx with signal.NotifyContext(SIGINT) → pipeline.Run → build EPUB
    // → if -KEPUB: kepub.Convert → save.Write(path, bytes, force) → print path
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
- When trafilatura leaves a metadata field empty, it is filled from the page's own declarations (read before extraction, which modifies the page):
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
   - Pick the smallest candidate from `srcset` or `<picture><source>` that is at least 1404 px wide (the reMarkable's screen width). If none is that wide, take the largest.
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
- **Every image is processed for e-ink**, using the values in `device.EInk`:
  1. **Decode** JPEG, PNG, GIF and WebP (and AVIF, if that optional decoder is included). SVG is rasterized with `oksvg` at the target width. GIFs keep only their first frame, since e-ink can't animate. Images in any other format are dropped, and their alt text is kept as a placeholder.
  2. **Flatten transparency onto white.** Some readers draw transparent areas as black, which ruins logos and diagrams.
  3. **Convert to grayscale** using the luminance formula (BT.601). Both devices are grayscale anyway, so the image looks the same, and the file is about a third of the size. Doing it ahead of time also keeps each device's own conversion from altering the result.
  4. **Resize** to fit inside 1404×1872, keeping the aspect ratio, with `draw.CatmullRom`. Images are never upscaled.
  5. **Encode**:
     - Photos (many distinct tones) become 8-bit grayscale JPEG at quality 80.
     - Line art, screenshots, charts and diagrams (few distinct tones) become PNG, quantized to a 16-level gray palette. That matches the panels exactly and keeps text in screenshots crisp, with no JPEG artifacts.
     - The choice is made per image by counting distinct gray levels after conversion.
  - WebP and SVG are never written into the book. Kobo's RMSDK renderer can't display WebP, and SVG support on both devices is unreliable.
- A failed download or decode is a warning on stderr, never a fatal error. The `<img>` becomes `<span class="missing-image">[Image: alt text]</span>` (or is removed if it has no alt text); a `<span>` is valid everywhere an image is.
- Files stay small: a typical article with 10 images should come out well under 5 MB. That matters for the reMarkable, which converts each EPUB on import, and for page-turn speed on the Clara HD.

### 5.5 build
Uses `go-epub`:
- `epub.NewEpub(title)`, then sets:
  - author, language, description, and a UUID identifier (`uuid.NewSHA1` of the URL, so rebuilding the same URL gives the same ID);
  - `dc:source` set to the URL, and the publisher set to the site name.
- `AddCSS` with the embedded `style.css`, written for e-ink and for the limited CSS support of RMSDK and the reMarkable:
  - **No colour and no gray text.** Body text, bylines, captions and links are all pure black. Links are shown by an underline, not colour, because gray or coloured text looks faint on e-ink.
  - **No background colours or shading**, including on `pre`, `blockquote` and table headers. Light gray fills turn into dithered noise on e-ink. Blockquotes get a solid black left border instead. Table headers are bold with 1px solid black borders.
  - **Let the reader control layout.** No fixed font sizes, no `font-family` beyond generic `serif`/`monospace`, and no page margins or `max-width` in px. Both devices apply the user's own font, size and margin settings.
  - **Images**: `img { max-width: 100%; height: auto; }` and figures set to `page-break-inside: avoid`, so a figure isn't split across two pages.
  - **Code**: `pre { white-space: pre-wrap; word-wrap: break-word; font-size: 0.85em; }`. E-ink can't scroll sideways, so long lines must wrap.
  - Only simple, widely supported CSS is used: no flexbox, grid, `calc()` or CSS variables, which RMSDK ignores or mis-renders.
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
  - write the EPUB 3 `nav.xhtml` and the NCX ourselves, with the `<h2>` sections nested under the article and `<h3>` subsections under those. go-epub can only list whole files (its `AddSubSection` creates new files), and its NCX lacks `dtb:uid`, which Kobo's RMSDK renderer reads. The finished zip is rewritten to replace both files and to add `dc:publisher`, `dc:source` and `dc:date` to the OPF, which go-epub has no setters for.
- The **cover** is always 1404×1872 grayscale JPEG. That is the reMarkable's exact screen size and the same 3:4 shape as the Clara HD, so it fills the screen on both devices and in their library views.
  - With a lead image: the image is cropped to fill the top three-fifths, with the title, site name and date set in large black text on a white band below. Library thumbnails are tiny on e-ink, so the title must be readable at thumbnail size.
  - Without one: a plain typographic cover with the title, site name and date in black on white, with a thin black rule.
  - Text is drawn with `x/image/font/opentype` and the embedded font. Long titles are wrapped and shrunk to fit.
  - It is set with `SetCover`. go-epub also writes the cover page and the `cover-image` property that Kobo and reMarkable use for thumbnails.

### 5.6 save
- `save.Resolve(saveFlag, title)` turns `-SAVE` into the final file path, using the rules in §2.
  - The filename slug comes from the title: lowercase ASCII, with runs of other characters collapsed to `-`, at most 80 characters. If the slug comes out empty, the filename falls back to `article.epub`.
  - Because the slug never contains path separators, nothing from the page can steer the file outside the chosen folder.
- `save.CheckExists(path, force)` is the early check from §2. It is called by `main` before fetching when `-SAVE` names a file, and by the pipeline right after extraction when it names a folder.
- `save.Write(path, book, force)`:
  1. `os.MkdirAll(filepath.Dir(path), 0o755)`.
  2. Without `force`: reserve the name with `os.OpenFile(path, O_CREATE|O_EXCL|O_WRONLY, 0o644)`. If that returns `fs.ErrExist`, stop with exit code 5.
  3. Write the book with `e.WriteTo()` into `os.CreateTemp(dir, ".link2epub-*")`, then `Sync` and `Close` it.
  4. `os.Rename(temp, path)`. This replaces either the reservation or, with `-FORCE`, the old file.
  5. On any error, remove the temp file and the reservation, but never a file that existed before the run.
- Rename-into-place works on FAT32 too, so saving straight onto a Kobo is safe.

### 5.7 KEPUB for Kobo (`-KEPUB`)
The Clara HD renders plain `.epub` files with Adobe's older RMSDK engine. Files ending in `.kepub.epub` use Kobo's own WebKit-based engine instead, which gives:
- faster page turns and better CSS support;
- reading statistics (time left in the chapter or book), and more reliable highlights and bookmarks.

How the conversion works:
1. `build` always produces a normal EPUB first, into an in-memory buffer. This is the same book the reMarkable gets.
2. `internal/kepub` opens that buffer with `zip.NewReader`. It then calls kepubify's `kepub.NewConverterWithOptions(...).Convert(ctx, w, zipReader)`, writing straight into the temp file that `save` provides.
   - kepubify wraps every sentence in `<span class="koboSpan">`. Kobo needs these spans for reading statistics and highlights.
   - It adds the `kobostylehacks` wrapper and makes sure the `cover-image` property is set.
3. `save` renames the result into place, applying the same `-FORCE` rules as any other output.

Converter options:
- `ConverterOptionHyphenate(true)`: turns on hyphenation, which helps justified text on the 6" screen.
- `ConverterOptionSmartypants()`: left **off**. It rewrites quotes and dashes, which would change the article's text.
- `ConverterOptionAddCSS`: not used. The e-ink stylesheet from §5.5 already suits Kobo.

Validation:
- `epubcheck` runs on the plain EPUB before conversion.
- In practice kepubify's output also passes epubcheck with no errors or warnings, so the KEPUB tests run epubcheck too, alongside structural tests (sentence spans, unchanged text, cover and contents kept) and checks on a real device.

---

## 6. Edge cases and risks

| Issue | Handling in v1 |
|---|---|
| Pages that need JavaScript (SPAs) | Detect near-empty extraction, exit with code `3` and a clear message. JavaScript rendering with `chromedp` is a future option (§9) |
| Paywalls and cookie or consent walls | Overlays are stripped by sanitizing. Content the server never sends is out of scope |
| Bot protection (Cloudflare and similar) | Use a realistic UA. Otherwise report the 403 clearly. Never try to get around it |
| Malformed HTML | `x/net/html` follows the HTML5 parsing algorithm, so it always produces a tree |
| Non-UTF-8 pages | Decoded with `x/net/html/charset`. Output is always UTF-8 |
| Huge pages or images | 20 MB page cap, 10 MB per image, downscaling to 1404×1872 |
| Charts that rely on colour | Grayscale makes some colours look identical, so a legend can become unreadable. v1 keeps the image as is and the alt text or caption. Raising contrast is a possible later improvement |
| Wide tables on a 6" screen | CSS lets tables wrap their cells. A table wider than the screen stays readable on the reMarkable but may be cramped on the Clara HD. Turning wide tables into images is out of scope |
| Long code lines | `pre-wrap`, because e-ink readers can't scroll sideways |
| go-trafilatura quirks | Found while building M2, and worked around in `internal/clean`:<br>• It resolves relative links as if the page URL were a folder, so links are made absolute before extraction.<br>• It joins `mailto:`/`tel:` links onto the page path; these are repaired afterwards.<br>• It turns `<pre>` code blocks into `<code>`; block-level `<code>` is turned back into `<pre><code>`. |
| Table cell spans | go-trafilatura drops `colspan`/`rowspan`, so a table that relies on them can come out with uneven rows. Known limitation in v1 |
| Image URLs without a file extension | go-trafilatura keeps an image only when `filepath.Ext` of its whole URL is an image type, so it drops URLs with a query string (`photo.jpg?utm_source=…`, as on every Wikipedia image), extensionless CDN URLs and `data:` URLs. `Prepare` replaces each image URL with a stand-in (`https://link2epub.invalid/img/<encoded URL>.png`) that passes the check, and `Content` restores the original |
| Images in zoom links | go-trafilatura drops an image wrapped in a link (such as a "click to zoom" lightbox link) when it sits in a `<div>` or `<p>`. WordPress themes, including Ars Technica's, do this for every image. `Prepare` unwraps links that contain only an image |
| Figure captions | go-trafilatura drops `<figcaption>`. `Prepare` turns each caption into an italic paragraph, which survives extraction, and `Content` rebuilds the `<figure>`. Captions laid out with `<div>`s are reduced to their text first |
| Invented dates | go-trafilatura's default date search scans body text and can invent a date from a stray number (an image `width="2000"` became 1 January 2000). It runs in `Fast` mode, which only trusts metadata, URLs and date markup |
| AVIF and JPEG XL images | Not decoded in v1. `<picture>` sources in those formats are skipped in favour of a JPEG, PNG, GIF or WebP alternative; a lone AVIF image is replaced by its alt text |
| Math (MathJax/KaTeX) | Keep MathML if the page has it (EPUB 3 supports MathML). Otherwise keep the TeX source as text |
| Right-to-left languages | Carry `lang` and `dir` through, and set the spine's `page-progression-direction` |
| Copyright | For personal offline reading. Every book records the source URL in `dc:source` and in the chapter header |

**Security**
- Page scripts never run.
- Everything that goes into the EPUB is sanitized, because some e-readers run JavaScript inside books.
- Download sizes and times are capped.
- `-SAVE` is used exactly as given. Nothing taken from the page (such as the title) can push the output outside the chosen directory, because the slug never contains path separators.
- Only `http` and `https` URLs are fetched, for the page, its redirects, images and cover. Local files are never read: `file:` URLs and local paths in a page are refused (tests in `cmd/link2epub`, `assets` and `fetch` cover each route).
- When `-URL` is on the internet, connections to loopback, private, link-local, carrier-grade NAT and reserved addresses are refused. The check runs on the address actually dialled, after DNS lookup, so redirects and DNS rebinding can't get around it. If `-URL` is itself local, local addresses are allowed. Configured proxies are exempt.
- Release binaries are built with the newest Go release, and govulncheck runs on every PR, weekly and before each release.

---

## 7. Testing strategy

- **Unit tests per package**, written as table-driven tests in Go:
  - `fetch`: `httptest.Server` covering redirects, 404, 429 retry, wrong content type, oversized body, Shift-JIS and ISO-8859-1 pages.
  - `extract`: golden tests over `testdata/pages/*.html`, compared with `testdata/golden/*.json`. A `-update` flag regenerates the golden files.
  - `clean`: lazy-image and `srcset` selection, sanitizing, heading changes, and an XHTML well-formedness check on every fixture.
  - `assets`: every output image is grayscale and fits 1404×1872. A photo becomes JPEG, line art becomes a 16-level PNG. Transparency is flattened to white. WebP and SVG input never appear in the output. The first frame is taken from animated GIFs. Images are never upscaled. Also covers deduplication and failed downloads.
  - `kepub`: convert a fixture EPUB, then check that the result is a valid zip with `mimetype` first, the content contains `koboSpan` spans, the OPF still marks the cover with `cover-image`, and the text is unchanged once the spans are removed.
  - `save`, all in `t.TempDir()`:
    - `-SAVE` resolution: `.epub` file, existing folder, trailing `/`, missing extension, `~`.
    - Folder creation: nested missing folders, and part of the path being an existing file (exit code 4).
    - **Without `-FORCE`**: an existing file means exit code 5, with the file's bytes and modification time unchanged. A file created by someone else between the early check and the write is also left untouched.
    - **With `-FORCE`**: the file is replaced, and the old file survives if the build fails part-way.
    - No temp or reservation files are left behind after any failure.
- **End-to-end** (`cmd/link2epub`): an `httptest.Server` serves a fixture page and its images. The test runs the CLI with `-URL` and `-SAVE`, then checks:
  - the exit code;
  - the zip layout: `mimetype` is the first entry and stored uncompressed, `META-INF/container.xml` exists, every manifest item is present;
  - `epubcheck` reports zero errors.
- **CLI flag tests**: missing flags, bad URL, extra arguments, `-FORCE`, `-KEPUB`, and the exit code for each. `-KEPUB` cases: a folder target gets `.kepub.epub`, `x.epub` is rewritten to `x.kepub.epub` with a note, a `.kepub.epub` path without `-KEPUB` exits with code 2, and the overwrite check uses the adjusted name. An existing output file must fail before any HTTP request is made: the test server counts its requests.
- **Device checks (manual, at each milestone from M3 onward)**: sideload a set of reference books onto a real reMarkable 2 and Kobo Clara HD. The set covers a long text article, an image-heavy post, code and tables, and a non-Latin script. Check:
  - the cover and its thumbnail in the library;
  - the table of contents;
  - image sharpness;
  - page-turn speed;
  - that the user's font and size settings still apply;
  - on the Clara HD, the `-KEPUB` version too: it should open in the KEPUB renderer and show time-left statistics.

  Record the results in `docs/device-testing.md`.
- **CI (GitHub Actions)**:
  - `go vet`, `staticcheck` and `go test -race ./...`;
  - epubcheck (Java) for the end-to-end test;
  - a `goreleaser` dry run.

---

## 8. Milestones

| # | Milestone | Deliverable |
|---|---|---|
| M0 | Scaffold | `go.mod`, layout, `main.go` with `-URL`/`-SAVE`/`-FORCE`/`-KEPUB` parsing and validation, the `save` package (path rules, folder creation, `-FORCE`, atomic writes), CI workflow. `-KEPUB` conversion itself arrives in M5 |
| M1 | Basic working version | fetch, trafilatura and go-epub produce a text-only EPUB |
| M2 | Clean XHTML and e-ink CSS | Sanitizer, URL resolution, heading changes, chapter template, e-ink stylesheet. epubcheck passes in CI. First check on real devices |
| M3 | Images for e-ink | Lazy-image fixes, concurrent download, grayscale, resizing to 1404×1872, JPEG or 16-level PNG choice, SVG rasterizing, deduplication |
| M4 | Metadata and cover | Metadata fallbacks, 1404×1872 grayscale cover (lead image or typographic), heading-based table of contents |
| M5 | KEPUB | `-KEPUB` flag via kepubify, extension handling, tested on the Clara HD |
| M6 | Polish and v1 release | Error messages and exit codes, README with per-device instructions (reMarkable: plain EPUB plus manual upload; Kobo: `-KEPUB`), goreleaser binaries |
| M7 | *(post-v1)* Several URLs in one book | Repeatable `-URL`, a `-URLS` list file and an optional `-TITLE`; one chapter per article (design below) |

Each milestone is a separate PR. After M1 the core job already works from end to end. M0–M6 make up v1.

**Status:** M0–M6 are done. M6 added:
- friendlier messages for common network failures (unknown host, refused connection, bad certificate, timeout) and hints on 403/404/429 responses;
- exit code 130 for Ctrl-C;
- a `-VERSION` flag, stamped by release builds and `build.cmd`;
- the GoReleaser config, a tag-triggered release workflow that creates a draft release, and a test build of every release binary on each PR;
- CI actions moved off the retiring Node 20 runtime;
- the README rewritten for v1, with downloads, troubleshooting and releasing sections.

### M7 design: several URLs in one book

**Input.** Both ways can be used, and combined, in one command:
- `-URL` may be repeated: `-URL https://a.example/one -URL https://b.example/two`. A custom `flag.Value` collects them.
- `-URLS <file>` reads a text file with one URL per line. Surrounding spaces are trimmed, and blank lines and lines starting with `#` are ignored. `-URLS -` reads the list from standard input. A file avoids shell quoting problems (an unquoted `&` breaks a Windows `cmd` command) and keeps a reading list reusable.
- Articles appear in the order given on the command line. A `-URLS` file's lines go where the flag appears.
- Every URL is checked before anything is fetched, with the same rules as today. A bad entry is a usage error (exit `2`) naming it, with its line number for a list file. An empty list is an error too.
- A URL given twice is included once. URLs that differ only in their `#fragment` count as the same.
- At most 100 URLs, to catch mistakes such as passing the wrong file.

**Title.** `-TITLE` is optional.
- With one URL, the book uses the article's title as today; `-TITLE` overrides it.
- With several URLs, the book is titled `-TITLE`, or "Reading list, 19 October 2026" (today's date) when it's missing.
- For folder targets the filename is the slug of that title. With several URLs the title is known before fetching, so an existing file is caught before anything is downloaded.
- One URL without `-TITLE` behaves exactly as v1.

**Fetching and failures.**
- Articles are fetched one at a time, in order, so sites aren't flooded and progress reads naturally. Each article's images download concurrently as today. Progress goes to stderr: `[2/5] https://…`.
- A URL that can't be fetched, or has no readable content, is skipped with a warning, and the book is made from the rest. A summary follows: `made a book from 4 of 5 articles`. If every URL fails, the exit code is that of the first failure (`1` or `3`) and no file is written.
- The local-network guard is decided per URL: an article may use local addresses only if its own URL is local.

**Book structure.**
- One chapter file per article (`chapter-01.xhtml`, …), each starting with its own header: title, byline and source link, as in v1. Each chapter keeps its article's language in `xml:lang`.
- The table of contents lists the articles, with each article's section headings nested under it.
- Images are stored once across the whole book, since files are already named by content hash.
- The cover is typographic: the book title, "5 articles", and the first few article titles.
- Metadata: `dc:title` is the book title. `dc:creator` lists the distinct authors, or "Various authors" when there are more than three. There's one `dc:source` per article. `dc:language` is the most common article language, and `dc:date` is the day the book was made. The identifier is derived from the list of URLs, so rebuilding the same list gives the same book identity.
- `-KEPUB` works the same as for one article.

**Code changes.**
- `pipeline` splits into an article stage (fetch, extract, clean, images) and a book stage that takes the list of articles.
- `build` generalises from one chapter to several, and the generated nav and NCX gain a level.
- `cover` gets a reading-list layout.
- `main` parses the new flags; the single-URL path stays as it is.

**Tests.**
- Flag and list-file parsing: comments, blank lines, stdin, duplicates, the limit, line numbers in errors.
- End-to-end with three local pages, one of which returns 404.
- Nested table of contents and per-chapter language.
- epubcheck on multi-article EPUB and KEPUB output.

---

## 9. Future extensions

- **JavaScript rendering**: a `-RENDER` flag using `chromedp` to drive a local Chrome/Chromium for SPA pages.
- **More flags**:
  - an `-AUTHOR` override (`-TITLE` arrives in M7);
  - `-COVER` for a custom cover image;
  - `-NOIMAGES`;
  - `-TIMEOUT`.
- **Follow pagination**: follow `rel="next"` links for multi-page articles.
- **More device profiles**: a `-DEVICE` flag for colour e-ink (e.g. Kobo Libra Colour) or tablets, so colour images are kept. The `device.Profile` type already allows for this.
- **Better contrast for e-ink**: optionally raise image contrast and gamma, since images often look washed out on e-ink.
- **RSS/Atom digest**: turn the last N feed items into one EPUB.
- **Web service**: an `http.Handler` around `pipeline.Run`. This would need SSRF protection: refuse private and loopback IP addresses, and re-check after each redirect.

---

## 10. Decisions made

| Question | Decision |
|---|---|
| Language | Go |
| Interface | CLI with `-URL` and `-SAVE` |
| Existing output file | Never overwritten unless `-FORCE` is given (exit code 5) |
| Missing output folders | Created automatically |
| Target devices | reMarkable 2 and Kobo Clara HD (grayscale e-ink) |
| Kobo-optimised output | `-KEPUB` flag in v1 (M5) |
| Getting books onto the reMarkable | You upload the saved file by hand. Direct upload was dropped, so link2epub stays focused on making the file |
| Several URLs in one book (M7) | Repeatable `-URL` plus a `-URLS` list file; optional `-TITLE`; failed URLs are skipped with a warning |
