package directclient

import (
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type qbitFake struct {
	torrents               []TorrentView
	addedFile, addedMagnet string
	category               string
	removed                string
	deleted                bool
}

func (f *qbitFake) AddTorrent(_ context.Context, b []byte, n, c string) error {
	f.addedFile = n + ":" + string(b) + ":" + c
	return nil
}
func (f *qbitFake) AddMagnet(_ context.Context, m, c string) error {
	f.addedMagnet = m + ":" + c
	return nil
}
func (f *qbitFake) ListTorrents(c string) []TorrentView {
	if c == "" {
		return f.torrents
	}
	var out []TorrentView
	for _, t := range f.torrents {
		if t.Category == c {
			out = append(out, t)
		}
	}
	return out
}
func (f *qbitFake) RemoveTorrent(h string, d bool) error { f.removed = h; f.deleted = d; return nil }
func (f *qbitFake) SetCategory(h, c string) error        { f.category = h + ":" + c; return nil }
func login(t *testing.T, h http.Handler) string {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/v2/auth/login", strings.NewReader("username=arr&password=secret"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("login status %d", w.Code)
	}
	return w.Result().Cookies()[0].String()
}
func request(h http.Handler, method, path, body, contentType, cookie string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestQBitAuthAndInfo(t *testing.T) {
	if w := request(NewQBitHandler(&qbitFake{}, "arr", ""), "POST", "/api/v2/auth/login", "username=arr&password=", "application/x-www-form-urlencoded", ""); w.Code != http.StatusForbidden {
		t.Fatalf("empty configured password should fail closed, got %d", w.Code)
	}
	f := &qbitFake{torrents: []TorrentView{{Hash: "abc", Name: "Film", Category: "movies", State: "downloading", Progress: .5, Size: 100, AmountLeft: 50}}}
	h := NewQBitHandler(f, "arr", "secret")
	if w := request(h, "GET", "/api/v2/torrents/info", "", "", ""); w.Code != 401 {
		t.Fatalf("unauth status=%d", w.Code)
	}
	bad := request(h, "POST", "/api/v2/auth/login", "username=arr&password=bad", "application/x-www-form-urlencoded", "")
	if bad.Code != 403 {
		t.Fatalf("bad login status=%d", bad.Code)
	}
	c := login(t, h)
	w := request(h, "GET", "/api/v2/torrents/info?category=movies", "", "", c)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0]["hash"] != "abc" || got[0]["state"] != "downloading" {
		t.Fatalf("bad info: %s", w.Body.String())
	}
}
func TestQBitAddMagnetAndTorrent(t *testing.T) {
	f := &qbitFake{}
	h := NewQBitHandler(f, "arr", "secret")
	c := login(t, h)
	form := url.Values{"urls": {"magnet:?xt=urn:btih:abc"}, "category": {"tv"}}
	w := request(h, "POST", "/api/v2/torrents/add", form.Encode(), "application/x-www-form-urlencoded", c)
	if w.Code != 200 || f.addedMagnet != "magnet:?xt=urn:btih:abc:tv" {
		t.Fatalf("magnet: code %d; %q", w.Code, f.addedMagnet)
	}
	var body strings.Builder
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("category", "movies")
	part, err := mw.CreateFormFile("torrents", "movie.torrent")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("payload"))
	_ = mw.Close()
	w = request(h, "POST", "/api/v2/torrents/add", body.String(), mw.FormDataContentType(), c)
	if w.Code != 200 || f.addedFile != "movie.torrent:payload:movies" {
		t.Fatalf("file: code %d; %q body=%s", w.Code, f.addedFile, w.Body.String())
	}
}
func TestQBitRemovalAndCategory(t *testing.T) {
	f := &qbitFake{}
	h := NewQBitHandler(f, "arr", "secret")
	c := login(t, h)
	w := request(h, "POST", "/api/v2/torrents/delete", "hashes=abc&deleteFiles=true", "application/x-www-form-urlencoded", c)
	if w.Code != 200 || f.removed != "abc" || !f.deleted {
		t.Fatalf("remove: %d %+v", w.Code, f)
	}
	w = request(h, "POST", "/api/v2/torrents/setCategory", "hashes=abc&category=tv", "application/x-www-form-urlencoded", c)
	if w.Code != 200 || f.category != "abc:tv" {
		t.Fatalf("category: %d %q", w.Code, f.category)
	}
}
