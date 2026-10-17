package clean

import (
	"reflect"
	"testing"
)

func TestParseSrcset(t *testing.T) {
	tests := []struct {
		in   string
		want []candidate
	}{
		{"a.jpg 400w, b.jpg 1500w", []candidate{{url: "a.jpg", width: 400}, {url: "b.jpg", width: 1500}}},
		{"a.jpg, b.jpg 2x", []candidate{{url: "a.jpg"}, {url: "b.jpg", density: 2}}},
		{"  a.jpg  1x ,\n b.jpg 2x  ", []candidate{{url: "a.jpg", density: 1}, {url: "b.jpg", density: 2}}},
		// Commas inside URLs, as some image CDNs use.
		{"https://cdn.example/w_400,h_300/a.jpg 400w, https://cdn.example/w_1600,h_1200/a.jpg 1600w",
			[]candidate{{url: "https://cdn.example/w_400,h_300/a.jpg", width: 400}, {url: "https://cdn.example/w_1600,h_1200/a.jpg", width: 1600}}},
		{"single.jpg", []candidate{{url: "single.jpg"}}},
		{"data:image/gif;base64,R0lG 1x, real.jpg 2x", []candidate{{url: "real.jpg", density: 2}}},
		{"", nil},
	}
	for _, tt := range tests {
		if got := parseSrcset(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseSrcset(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestPickCandidate(t *testing.T) {
	tests := []struct {
		srcset, want string
	}{
		{"a 400w, b 1500w, c 3000w", "b"}, // smallest that is at least 1404 wide
		{"c 3000w, b 1500w, a 400w", "b"}, // order doesn't matter
		{"a 400w, b 800w", "b"},           // none wide enough: the widest
		{"a 1x, b 2x, c 1.5x", "b"},       // densities: the highest
		{"a, b", "a"},                     // no descriptors: the first
		{"", ""},
	}
	for _, tt := range tests {
		if got := pickCandidate(parseSrcset(tt.srcset)); got != tt.want {
			t.Errorf("pickCandidate(%q) = %q, want %q", tt.srcset, got, tt.want)
		}
	}
}

func TestPrepareImages(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"relative src made absolute",
			`<img src="photo.jpg" alt="p">`,
			`<img src="https://example.com/posts/photo.jpg" alt="p"/>`},
		{"lazy data-src replaces placeholder",
			`<img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" data-src="/img/real.png" alt="p">`,
			`<img src="https://example.com/img/real.png" data-src="/img/real.png" alt="p"/>`},
		{"data-original",
			`<img data-original="/img/real.png">`,
			`<img data-original="/img/real.png" src="https://example.com/img/real.png"/>`},
		{"srcset picks the right width",
			`<img src="/s.jpg" srcset="/s.jpg 400w, /m.jpg 1500w, /l.jpg 3000w">`,
			`<img src="https://example.com/m.jpg" srcset="/s.jpg 400w, /m.jpg 1500w, /l.jpg 3000w"/>`},
		{"picture skips AVIF sources and uses WebP",
			`<picture><source srcset="/p.avif" type="image/avif"><source srcset="/p-800.webp 800w, /p-1600.webp 1600w" type="image/webp"><img src="/p.jpg"></picture>`,
			`<picture><source srcset="/p.avif" type="image/avif"/><source srcset="/p-800.webp 800w, /p-1600.webp 1600w" type="image/webp"/><img src="https://example.com/p-1600.webp"/></picture>`},
		{"picture with only AVIF falls back to the img",
			`<picture><source srcset="/p.avif" type="image/avif"><img src="/p.jpg"></picture>`,
			`<picture><source srcset="/p.avif" type="image/avif"/><img src="https://example.com/p.jpg"/></picture>`},
		{"tracking pixels are removed",
			`<p>a<img src="https://t.example/p.gif" width="1" height="1">b<img src="/x.gif" height="2px"></p>`,
			`<p>ab</p>`},
		{"captions laid out with divs are reduced to text",
			`<figure><img src="/a.jpg"><figcaption class="hidden"><div class="caption"><div class="icon"></div><div class="caption-content">The new dialog.<span class="credit">Credit: KDE</span></div></div></figcaption></figure>`,
			`<figure><img src="https://example.com/a.jpg"/><p><i>The new dialog. Credit: KDE</i></p></figure>`},
		{"zoom links around images are unwrapped",
			`<div><a href="/full.jpg" class="cursor-zoom-in"><img src="/small.jpg" alt="x"></a></div>`,
			`<div><img src="https://example.com/small.jpg" alt="x"/></div>`},
		{"links with text around images are kept",
			`<p><a href="/page"><img src="/icon.png" alt=""> Read more</a></p>`,
			`<p><a href="https://example.com/page"><img src="https://example.com/icon.png" alt=""/> Read more</a></p>`},
		{"figcaption becomes an italic paragraph",
			`<figure><img src="/a.jpg"><figcaption class="c">A <b>bold</b> caption</figcaption></figure>`,
			`<figure><img src="https://example.com/a.jpg"/><p><i>A <b>bold</b> caption</i></p></figure>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := prepareString(t, tt.in); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestBuildFigures(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"standalone image with caption",
			`<img src="https://example.com/a.jpg" alt="A"><p><i>The caption</i></p><p>Text.</p>`,
			`<figure><img src="https://example.com/a.jpg" alt="A"/><figcaption>The caption</figcaption></figure><p>Text.</p>`},
		{"em caption",
			`<img src="https://example.com/a.jpg"><p><em>Caption</em></p>`,
			`<figure><img src="https://example.com/a.jpg"/><figcaption>Caption</figcaption></figure>`},
		{"standalone image without caption",
			`<img src="https://example.com/a.jpg"><p>Just a paragraph with <i>some</i> italics.</p>`,
			`<figure><img src="https://example.com/a.jpg"/></figure><p>Just a paragraph with <i>some</i> italics.</p>`},
		{"image inside a paragraph stays inline",
			`<p>Look <img src="https://example.com/a.jpg"> here.</p>`,
			`<p>Look <img src="https://example.com/a.jpg"/> here.</p>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanString(t, tt.in); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}
