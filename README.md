# link2epub

Turn any web article into a clean EPUB for your e-ink reader.

`link2epub` is a small command-line tool written in Go. You give it a URL. It downloads the page, keeps the article itself (dropping navigation, ads, pop-ups and comments) and writes an EPUB tuned for grayscale e-ink screens. It is built for the **reMarkable 2** and the **Kobo Clara HD**.

```bash
link2epub -URL https://example.com/some-article -SAVE ~/Books/
```

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

### Download (recommended)

Download the archive for your computer from the [Releases](https://github.com/bajdev/link2epub/releases) page:

| Computer | File |
|---|---|
| Windows (most PCs) | `link2epub_<version>_windows_amd64.zip` |
| Windows on Arm (e.g. Snapdragon laptops) | `link2epub_<version>_windows_arm64.zip` |
| Mac with Apple silicon (M1 and later) | `link2epub_<version>_darwin_arm64.tar.gz` |
| Mac with Intel | `link2epub_<version>_darwin_amd64.tar.gz` |
| Linux | `link2epub_<version>_linux_amd64.tar.gz` (or `_arm64`) |

Unpack it and put `link2epub` (`link2epub.exe` on Windows) somewhere convenient. There's nothing to install and nothing else to download. It's a command-line program, so run it from Command Prompt, PowerShell or Terminal rather than by double-clicking. `checksums.txt` on the release page lets you check the download.

The binaries aren't code-signed, so the first run may show a warning:

- **Windows:** SmartScreen may say "Windows protected your PC". Choose **More info → Run anyway**.
- **macOS:** Gatekeeper may refuse to open it. Run `xattr -d com.apple.quarantine link2epub` once, in the folder where you unpacked it.

### With Go (1.26 or newer)

```bash
go install github.com/bajdev/link2epub/cmd/link2epub@latest
```

The binary is installed into `$(go env GOPATH)/bin`. Make sure that folder is on your `PATH`.

### From source

```bash
git clone https://github.com/bajdev/link2epub.git
cd link2epub
go build -o link2epub ./cmd/link2epub
```

### From source on Windows

Install [Go](https://go.dev/dl/) 1.26 or newer, then run `build.cmd` from the repository folder:

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
link2epub -URL <url> -SAVE <path> [-FORCE] [-KEPUB]
link2epub -VERSION
```

| Flag | Required | Description |
|---|---|---|
| `-URL` | yes | The web page to convert. Must start with `http://` or `https://`. |
| `-SAVE` | yes | Where to write the book: a file or a folder (see below). Missing folders are created. |
| `-FORCE` | no | Overwrite the output file if it already exists. |
| `-KEPUB` | no | Write a Kobo KEPUB (`.kepub.epub`) instead of a plain EPUB. |
| `-VERSION` | no | Print the version and exit. Include it when reporting a problem. |

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
| `130` | Stopped with Ctrl-C |

Example in a script:

```bash
while read -r url; do
  link2epub -URL "$url" -SAVE ~/Books/ || echo "failed: $url" >&2
done < urls.txt
```

---

## Troubleshooting

| Message | What it means |
|---|---|
| `can't find the server …` | The address is mistyped, or the computer is offline. |
| `… refused the connection` / `… took too long to respond` | The site is down or very slow. Try again later. |
| `… security certificate isn't valid` | The connection can't be trusted, so link2epub won't use it. Check the address; on a work or school network, a proxy may be intercepting secure connections. |
| `… returned 403 Forbidden; the site may need a login or may block automated downloads` | The site refused the request. Some sites block anything that isn't a full web browser. |
| `… returned 404 Not Found; check the address` | The page doesn't exist at that address. |
| `no readable article content found` | The page has no article text link2epub can find. Usually the page builds its content with JavaScript, or it's a paywall or cookie wall. |
| `warning: image … skipped: …` | One image couldn't be downloaded or read. The book is still made, with the image's description in its place. |
| `warning: cover image … skipped, using a plain cover` | The page's preview image couldn't be used, so the book gets a typographic cover instead. |
| `… is a local or private address …` | The page tried to send link2epub to your own computer or network, through a redirect or an image. That's refused for pages on the internet (see [Privacy and security](#privacy-and-security)). To convert a page on your own network, give its local address as `-URL`. |

To report a problem with a particular page, include the output of `link2epub -VERSION`, the full command, everything it printed, and a saved copy of the page:

```bash
curl -L -A "Mozilla/5.0" -o page.html "https://example.com/the-article"
```

(On Windows, use `curl.exe`, which comes with Windows 10 and 11.)

---

## Privacy and security

- **Only web pages are read.** link2epub downloads the page given as `-URL`, its images and its preview image, over `http://` or `https://` only. It never reads files on your computer: `file:` links and local paths in a page are ignored, and the only file it writes is the book.
- **Pages on the internet can't reach your network.** A web page chooses where its images come from, and can redirect. So when `-URL` is on the internet, link2epub refuses to connect to your own computer (`localhost`), your home or office network (such as `192.168.x.x` or `10.x.x.x`) and similar private addresses. If `-URL` itself is a local address, such as a wiki on your network, its local images are allowed. With a proxy set in `HTTP_PROXY` or `HTTPS_PROXY`, the proxy makes the connections, so link2epub can't check them.
- **What sites see.** Requests look like a desktop browser and include "link2epub" in the user agent. Image requests send the article's address as the referrer, as browsers do. link2epub sends no cookies, never logs in and runs no JavaScript.
- **Your files are safe.** Existing files are only replaced with `-FORCE`. Filenames made from article titles contain only letters, digits and dashes, so a title can't place the book outside the folder you chose.

To report a security problem, see [`SECURITY.md`](SECURITY.md).

---

## Limitations

- **Pages that need JavaScript.** Single-page apps that only render content in the browser aren't supported in v1. link2epub detects these and exits with code `3`.
- **Paywalls and logins.** Only content the server sends without logging in can be converted.
- **One URL per book.** Combining several articles into one book is a possible future feature.
- **Colour-coded charts.** These can be harder to read after grayscale conversion. The image's caption and alt text are kept.
- **AVIF and JPEG XL images** aren't supported. When a page offers the image in another format too, that one is used; otherwise the image's description is shown instead.
- **Tables that merge cells** (`colspan`/`rowspan`) may come out with uneven rows, because the extraction library drops those attributes.

link2epub is meant for saving articles for your own offline reading. Every book records its source URL on the first page and in its metadata.

---

## Development

```bash
go test ./...                 # unit and end-to-end tests
go test -race ./...           # with the race detector
go vet ./... && staticcheck ./...
```

The tests also check the generated books with [epubcheck](https://github.com/w3c/epubcheck), which needs Java. Set `EPUBCHECK_JAR` to the path of `epubcheck.jar` to turn those checks on; without it they're skipped. CI always runs them, on Linux, macOS and Windows.

On Windows, `build.cmd` builds `link2epub.exe` and `test-examples.cmd` converts a set of real articles into `test-examples\` for checking on the devices (see [From source on Windows](#from-source-on-windows)).

### Project layout

```
cmd/link2epub/      CLI entry point (flags, exit codes, -VERSION)
internal/pipeline/  Runs the stages below in order
internal/fetch/     HTTP download, local-network guard, character-set decoding, friendly network errors
internal/extract/   Article and metadata extraction (go-trafilatura, page metadata)
internal/clean/     Pre-extraction fixes, sanitising, XHTML conversion
internal/assets/    Image download and e-ink processing
internal/cover/     Cover drawing
internal/device/    e-ink device profile (image and cover sizes, gray levels)
internal/build/     EPUB packaging (go-epub), stylesheet, contents and metadata
internal/kepub/     KEPUB conversion (kepubify)
internal/save/      Output path rules, folder creation, -FORCE, atomic writes
internal/epubtest/  Test helpers for checking generated books
testdata/           Saved HTML pages and images used by the tests
```

### Releasing

Releases are built by [GoReleaser](https://goreleaser.com) from `.goreleaser.yaml`: self-contained binaries for Windows, macOS and Linux on amd64 and arm64, plus checksums. Every pull request runs a test build of all of them.

To make a release, tag the commit on `main` and push the tag:

```bash
git tag v1.0.0
git push origin v1.0.0
```

The Release workflow then builds everything with the newest Go release (so the binaries include its security fixes), runs [govulncheck](https://go.dev/doc/security/vuln/), and creates a **draft** release on GitHub. Check the files and the notes, then press **Publish release**. CI also runs govulncheck weekly, so a newly published vulnerability in Go or a dependency shows up even when nothing has changed here; when one does, make a new release.

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
| M6 | Polish and **v1 release** | Done |
| M7 | Several URLs in one book: repeat `-URL`, or list them in a file with `-URLS`; optional `-TITLE` | Planned |

Possible later features: JavaScript rendering, author and cover overrides, and profiles for colour e-ink devices.

---

## Acknowledgements

link2epub builds on these open-source projects:

- [go-trafilatura](https://github.com/markusmobius/go-trafilatura): content extraction
- [go-epub](https://github.com/go-shiori/go-epub): EPUB packaging
- [kepubify](https://github.com/pgaskin/kepubify): KEPUB conversion
- [bluemonday](https://github.com/microcosm-cc/bluemonday): HTML sanitising
- [oksvg](https://github.com/srwiley/oksvg): SVG rendering
- The Go [x/image](https://pkg.go.dev/golang.org/x/image) and [x/net](https://pkg.go.dev/golang.org/x/net) packages: WebP decoding, image scaling, the Go fonts used on covers, and HTML parsing

## License

link2epub is released under the [MIT License](LICENSE).
