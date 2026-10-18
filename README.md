# link2epub

Turn any web article into a clean EPUB for your e-ink reader.

`link2epub` is a small command-line tool written in Go. You give it a URL. It downloads the page, keeps the article itself (dropping navigation, ads, pop-ups and comments) and writes an EPUB tuned for grayscale e-ink screens. It is built for the **reMarkable 2** and the **Kobo Clara HD**.

```bash
link2epub -URL https://example.com/some-article -SAVE ~/Books/
```

> **Status: in development.** The tool is being built from [`PLAN.md`](PLAN.md). All v1 features work, including `-KEPUB`. What's left before the v1 release is polish (milestone M6). See [Roadmap](#roadmap) for progress.

---

## Features

- **Readable content only.** link2epub finds the main article text and removes menus, ads, cookie banners, share buttons and comments.
- **Metadata included.** Title, author, publish date, site name and language are filled in automatically.
- **Works offline.** Images are downloaded and embedded in the book.
- **Tuned for e-ink.**
  - Images are converted to grayscale and sized for the screen (no larger than 1404×1872).
  - Diagrams and screenshots are kept crisp as 16-shade PNGs.
  - The stylesheet uses no colour and no gray text, and leaves fonts and margins to your reader's settings.
- **A cover every time.** Each book gets a 3:4 grayscale cover whose title is readable at library-thumbnail size.
- **Kobo-optimised output.** `-KEPUB` writes a `.kepub.epub` file, which Kobo opens with its faster renderer and shows reading stats for.
- **Safe saving.**
  - An existing file is never overwritten unless you pass `-FORCE`.
  - Missing folders are created.
  - A failed run never leaves a half-written file behind.
- **Single binary.** No runtime dependencies. It runs on Linux, macOS and Windows.

---

## Installation

### With Go (1.24 or newer)

```bash
go install github.com/bajdev/link2epub/cmd/link2epub@latest
```

The binary is installed into `$(go env GOPATH)/bin`. Make sure that folder is on your `PATH`.

### Pre-built binaries

Once v1 is released, download the binary for your platform from the [Releases](https://github.com/bajdev/link2epub/releases) page. Builds are planned for Linux, macOS and Windows on amd64 and arm64.

### From source

```bash
git clone https://github.com/bajdev/link2epub.git
cd link2epub
go build -o link2epub ./cmd/link2epub
```

### From source on Windows

Install [Go](https://go.dev/dl/) 1.24 or newer, then run `build.cmd` from the repository folder:

```bat
git clone https://github.com/bajdev/link2epub.git
cd link2epub
build.cmd
```

In PowerShell, run `.\build.cmd` instead. The script builds `link2epub.exe` in the repository folder, whichever folder you start it from. To update, run `git pull`, then `build.cmd` again.

To try it on real articles, run `test-examples.cmd` (`.\test-examples.cmd` in PowerShell). It rebuilds `link2epub.exe`, converts a handful of sample articles into `test-examples\`, replacing books from earlier runs, and prints how many succeeded. Run `test-examples.cmd kepub` to make Kobo KEPUBs (`.kepub.epub`) instead. Edit the list of URLs in the script to test other pages.

---

## Usage

```
link2epub -URL <url> -SAVE <path> [-FORCE] [-KEPUB] [-COOKIES file]
```

| Flag | Required | Description |
|---|---|---|
| `-URL` | yes | The web page to convert. Must start with `http://` or `https://`. |
| `-SAVE` | yes | Where to write the book: a file or a folder (see below). Missing folders are created. |
| `-FORCE` | no | Overwrite the output file if it already exists. |
| `-KEPUB` | no | Write a Kobo KEPUB (`.kepub.epub`) instead of a plain EPUB. |
| `-COOKIES` | no | Cookies exported from your browser, for pages you can only read when logged in. See [Pages behind a login or paywall](#pages-behind-a-login-or-paywall). |

Flag names are case-sensitive: `-URL` works, `-url` does not.

### How `-SAVE` is interpreted

| `-SAVE` value | Result |
|---|---|
| Ends in `/`, e.g. `~/Books/` | A folder (created if missing). The filename comes from the article title: `~/Books/<article-title>.epub` |
| An existing folder, e.g. `~/Books` | Same as above |
| Ends in `.epub`, e.g. `~/Books/a.epub` | Written to exactly that file |
| Anything else, e.g. `~/Books/a` | Treated as a file name, and `.epub` is appended: `~/Books/a.epub` |

Watch the trailing slash when the folder doesn't exist yet. `-SAVE ~/Books/new/` creates a folder called `new`, while `-SAVE ~/Books/new` writes a file called `new.epub`.

### Overwriting

link2epub never replaces an existing file unless you ask it to:

```bash
$ link2epub -URL https://example.com/a -SAVE ~/Books/a.epub
error: /home/me/Books/a.epub already exists (use -FORCE to overwrite)

$ link2epub -URL https://example.com/a -SAVE ~/Books/a.epub -FORCE
/home/me/Books/a.epub
```

When the output path is known up front, this check happens before anything is downloaded. With `-FORCE`, the old file stays intact until the new one has been fully written.

### KEPUB output (`-KEPUB`)

Kobo only uses its faster renderer for files ending in `.kepub.epub`, so `-KEPUB` adjusts the extension for you:

| `-SAVE` value | Saved as |
|---|---|
| `~/Books/` (folder) | `~/Books/<article-title>.kepub.epub` |
| `~/Books/a.kepub.epub` | `~/Books/a.kepub.epub` |
| `~/Books/a.epub` | `~/Books/a.kepub.epub` (with a note on stderr) |
| `~/Books/a` | `~/Books/a.kepub.epub` |

Passing a `.kepub.epub` path **without** `-KEPUB` is an error, because a plain EPUB with that name would display badly on a Kobo.

### Pages behind a login or paywall

To convert an article you can read in your browser because you're logged in, such as one from a site you subscribe to, give link2epub your browser's cookies for that site. It then downloads the page exactly as your logged-in browser would, so you get what your account can see; it doesn't get around paywalls.

1. Log in to the site in your browser.
2. Export the site's cookies with a browser extension, for example "Get cookies.txt LOCALLY" (saves a Netscape `cookies.txt`) or "Cookie-Editor" (Export → JSON, saved to a file). link2epub reads both formats.
3. Pass the file with `-COOKIES`:

   ```bat
   link2epub.exe -URL https://news.example.com/2026/10/story -SAVE %USERPROFILE%\Books\ -COOKIES cookies.txt
   ```

Things to know:

- **Keep the cookie file private.** It holds your login session: anyone with it can use your account until you log out. Delete it when you're done, or store it somewhere only you can read.
- **Cookies only go to their own site.** Each cookie is sent only to the site it came from (and its subdomains, if the browser would), exactly as a browser does. A file with cookies for several sites is fine.
- **Sessions expire.** If a site starts serving the logged-out version again, log in and export the cookies again.
- If none of the file's cookies belong to the page's site, link2epub warns you, because the page will be fetched as a logged-out visitor.
- Some sites tie a login to the browser itself, or build the article with JavaScript after the page loads. Cookies alone can't help with those.

### Output

- On success, the absolute path of the saved file is printed to **stdout**, so you can use it in scripts.
- Progress messages and warnings, such as an image that failed to download, go to **stderr**.

---

## Device guides

### Kobo Clara HD

Use `-KEPUB` for the best experience: faster page turns, time-left statistics and more reliable highlights.

1. Connect the Kobo by USB and tap **Connect** on the device.
2. Save straight to it:
   ```bash
   # Linux
   link2epub -URL https://example.com/article -SAVE /media/$USER/KOBOeReader/Articles/ -KEPUB
   # macOS
   link2epub -URL https://example.com/article -SAVE /Volumes/KOBOeReader/Articles/ -KEPUB
   # Windows (the drive letter may differ)
   link2epub.exe -URL https://example.com/article -SAVE E:\Articles\ -KEPUB
   ```
3. Eject the Kobo. The article appears in your library.

A plain EPUB (without `-KEPUB`) also works on the Kobo, but it opens in Kobo's older renderer.

### reMarkable 2

Use plain EPUB, without `-KEPUB`.

1. Save the book locally:
   ```bash
   link2epub -URL https://example.com/article -SAVE ~/reMarkable/
   ```
2. Upload it with any of these:
   - **reMarkable desktop or mobile app:** drag the file in.
   - **USB web interface:** connect by USB, turn on *Settings → Storage → USB web interface*, open <http://10.11.99.1> and upload the file.

Direct upload from link2epub is planned for a later release (see [Roadmap](#roadmap)).

---

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | The page couldn't be fetched (network error, non-200 response, not an HTML page) |
| `2` | Invalid arguments |
| `3` | No readable article content was found on the page |
| `4` | The book couldn't be built or written (including folder creation) |
| `5` | The output file already exists and `-FORCE` was not given |

Example in a script:

```bash
while read -r url; do
  link2epub -URL "$url" -SAVE ~/Books/ || echo "failed: $url" >&2
done < urls.txt
```

---

## Limitations

- **Pages that need JavaScript.** Single-page apps that only render content in the browser aren't supported in v1. link2epub detects these and exits with code `3`.
- **Paywalls and logins.** Without `-COOKIES`, only what a logged-out visitor sees can be converted. With it, you get what your own account can read; link2epub never tries to get around a paywall.
- **One URL per book.** Combining several articles into one book is a possible future feature.
- **Colour-coded charts.** These can be harder to read after grayscale conversion. The image's caption and alt text are kept.

link2epub is meant for saving articles for your own offline reading. Every book records its source URL on the first page and in its metadata.

---

## Development

```bash
go test ./...                 # unit and end-to-end tests
go test -race ./...           # with the race detector
go vet ./... && staticcheck ./...
```

The end-to-end tests check the generated books with [epubcheck](https://github.com/w3c/epubcheck), which needs Java. If `epubcheck` isn't on your `PATH`, those checks are skipped locally. They always run in CI.

### Project layout

```
cmd/link2epub/      CLI entry point (flags, exit codes)
internal/fetch/     HTTP download and character-set decoding
internal/extract/   Article and metadata extraction (go-trafilatura)
internal/clean/     Sanitising and XHTML conversion
internal/assets/    Image download and e-ink processing
internal/device/    e-ink device profile (image and cover sizes, gray levels)
internal/build/     EPUB packaging (go-epub), cover and stylesheet
internal/kepub/     KEPUB conversion (kepubify)
internal/save/      Output path rules, folder creation, -FORCE, atomic writes
testdata/           Saved HTML pages and expected results
```

See [`PLAN.md`](PLAN.md) for the full design.

---

## Roadmap

| Milestone | Scope | Status |
|---|---|---|
| M0 | Project setup, flag handling, saving rules (`-SAVE`, `-FORCE`, folders) | Done |
| M1 | Basic working version: text-only EPUB | Done |
| M2 | Clean XHTML and the e-ink stylesheet | Done |
| M3 | Images processed for e-ink | Done |
| M4 | Metadata, cover and table of contents | Done |
| M5 | `-KEPUB` for Kobo | Done |
| M6 | Polish and **v1 release** | Planned |
| M7 | Direct upload to reMarkable | Post-v1 |

Possible later features: JavaScript rendering, several URLs per book, title, author and cover overrides, and profiles for colour e-ink devices.

---

## Acknowledgements

link2epub builds on these open-source projects:

- [go-trafilatura](https://github.com/markusmobius/go-trafilatura): content extraction
- [go-epub](https://github.com/go-shiori/go-epub): EPUB packaging
- [kepubify](https://github.com/pgaskin/kepubify): KEPUB conversion
- [bluemonday](https://github.com/microcosm-cc/bluemonday): HTML sanitising
- [goquery](https://github.com/PuerkitoBio/goquery): HTML handling

## License

link2epub is released under the [MIT License](LICENSE).
