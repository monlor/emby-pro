package openlist

import (
	"net/url"
	"testing"
)

func TestDownloadURL(t *testing.T) {
	baseURL, err := url.Parse("http://openlist:5244/base")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	client := &Client{baseURL: baseURL}
	got := client.DownloadURL(Entry{Sign: "abc"}, "/movies/电影.mkv")
	want := "http://openlist:5244/base/d/movies/%E7%94%B5%E5%BD%B1.mkv?sign=abc"
	if got != want {
		t.Fatalf("DownloadURL() = %s, want %s", got, want)
	}
}
