package extract

import (
	"net/url"
	"testing"
	"time"
)

func TestReadPageMetaTags(t *testing.T) {
	base, _ := url.Parse("https://news.example/2025/story")
	m := readPageMeta(parse(t, `<html><head>
		<title>Fallback Title | News</title>
		<meta property="og:title" content="The Story">
		<meta property="og:site_name" content="Example News">
		<meta property="og:image" content="/img/lead.jpg">
		<meta property="og:image" content="/img/second.jpg">
		<meta name="author" content="Ada Lovelace">
		<meta name="description" content="What happened.">
		<meta property="article:published_time" content="2025-03-14T09:30:00+01:00">
	</head><body></body></html>`), base)

	want := pageMeta{
		Title: "The Story", Author: "Ada Lovelace", SiteName: "Example News", Description: "What happened.",
		Image:     "https://news.example/img/lead.jpg",
		Published: time.Date(2025, 3, 14, 9, 30, 0, 0, time.FixedZone("", 3600)),
	}
	if m.Title != want.Title || m.Author != want.Author || m.SiteName != want.SiteName ||
		m.Description != want.Description || m.Image != want.Image || !m.Published.Equal(want.Published) {
		t.Errorf("got  %+v\nwant %+v", m, want)
	}
}

func TestReadPageMetaJSONLD(t *testing.T) {
	base, _ := url.Parse("https://blog.example/post")
	m := readPageMeta(parse(t, `<html><head>
		<title>Title Tag</title>
		<meta property="article:author" content="https://blog.example/authors/grace">
		<script type="application/ld+json">{"@context":"https://schema.org","@graph":[
			{"@type":"WebSite","name":"Not an article"},
			{"@type":["BlogPosting","Article"],"headline":"JSON-LD Headline",
			 "author":[{"@type":"Person","name":"Grace Hopper"},{"name":"Alan Turing"}],
			 "publisher":{"@type":"Organization","name":"The Blog"},
			 "image":{"@type":"ImageObject","url":"https://cdn.example/hero.png"},
			 "datePublished":"2024-11-02"}
		]}</script>
		<script type="application/ld+json">this is not JSON</script>
	</head><body></body></html>`), base)

	if m.Title != "JSON-LD Headline" {
		t.Errorf("Title = %q", m.Title)
	}
	if m.Author != "Grace Hopper, Alan Turing" {
		t.Errorf("Author = %q (profile URLs must not count as names)", m.Author)
	}
	if m.SiteName != "The Blog" {
		t.Errorf("SiteName = %q", m.SiteName)
	}
	if m.Image != "https://cdn.example/hero.png" {
		t.Errorf("Image = %q", m.Image)
	}
	if !m.Published.Equal(time.Date(2024, 11, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Published = %v", m.Published)
	}
}

func TestReadPageMetaImages(t *testing.T) {
	base, _ := url.Parse("https://site.example/a")
	tests := map[string]string{
		`<meta name="twitter:image" content="https://site.example/t.jpg">`: "https://site.example/t.jpg",
		`<meta property="og:image" content="//cdn.example/x.jpg">`:         "https://cdn.example/x.jpg",
		`<meta property="og:image" content="data:image/png;base64,AAAA">`:  "",
		`<meta property="og:image" content="javascript:alert(1)">`:         "",
		``: "",
	}
	for head, want := range tests {
		if got := readPageMeta(parse(t, "<html><head>"+head+"</head></html>"), base).Image; got != want {
			t.Errorf("%s: Image = %q, want %q", head, got, want)
		}
	}
}

func TestExtractCoverImageAndFallbacks(t *testing.T) {
	a, err := extractFile(t, "photo-essay.html", "https://letterpress.example/essays/print-shop")
	if err != nil {
		t.Fatal(err)
	}
	if a.ImageURL != "https://letterpress.example/img/press.jpg" {
		t.Errorf("ImageURL = %q", a.ImageURL)
	}
	if a.SiteName != "Letterpress Weekly" || a.Author != "Grace Hopper" {
		t.Errorf("SiteName = %q, Author = %q", a.SiteName, a.Author)
	}
}
