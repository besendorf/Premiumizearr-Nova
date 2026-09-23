package directclient

import (
	"crypto/sha1"
	"encoding/base32"
	"encoding/hex"
	"testing"
)

func TestMagnetHashHexAndBase32(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	decoded, err := hex.DecodeString(hash)
	if err != nil {
		t.Fatal(err)
	}
	for _, magnet := range []string{
		"magnet:?xt=urn:btih:" + hash + "&dn=Episode",
		"magnet:?xt=urn:btih:" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(decoded),
	} {
		got, err := magnetHash(magnet)
		if err != nil || got != hash {
			t.Fatalf("magnetHash(%q) = %q, %v; want %q", magnet, got, err, hash)
		}
	}
	if _, err := magnetHash("magnet:?xt=urn:btih:invalid"); err == nil {
		t.Fatal("accepted invalid BTIH hash")
	}
}

func TestTorrentHashUsesOriginalInfoBytes(t *testing.T) {
	// Hash the exact bencoded info dictionary; neither the surrounding
	// announce URL nor a decoded/re-encoded dictionary belongs in the hash.
	info := []byte("d4:name7:episode6:lengthi12ee")
	torrent := append([]byte("d8:announce7:tracker4:info"), info...)
	torrent = append(torrent, 'e')
	want := sha1.Sum(info)
	got, err := torrentHash(torrent)
	if err != nil || got != hex.EncodeToString(want[:]) {
		t.Fatalf("torrentHash = %q, %v; want %x", got, err, want)
	}
	if _, err := torrentHash([]byte("d4:infod4:name10:truncated")); err == nil {
		t.Fatal("accepted truncated torrent")
	}
}
