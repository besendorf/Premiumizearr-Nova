package directclient

import (
	"crypto/sha1"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func magnetHash(magnet string) (string, error) {
	u, err := url.Parse(magnet)
	if err != nil || u.Scheme != "magnet" {
		return "", errors.New("invalid magnet link")
	}
	for _, xt := range u.Query()["xt"] {
		if !strings.HasPrefix(strings.ToLower(xt), "urn:btih:") {
			continue
		}
		hash := xt[len("urn:btih:"):]
		if len(hash) == 40 {
			if _, err := hex.DecodeString(hash); err == nil {
				return strings.ToLower(hash), nil
			}
		}
		if len(hash) == 32 {
			decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(hash))
			if err == nil && len(decoded) == sha1.Size {
				return hex.EncodeToString(decoded), nil
			}
		}
	}
	return "", errors.New("magnet link has no valid BTIH hash")
}

func magnetDisplayName(magnet string) (string, error) {
	u, err := url.Parse(magnet)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(u.Query().Get("dn"))
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	if len(name) > 255 {
		name = name[:255]
	}
	return name, nil
}

// torrentHash hashes the original bencoded "info" dictionary, as required
// by the BitTorrent infohash contract. Re-encoding that dictionary could
// change its bytes and therefore its hash.
func torrentHash(data []byte) (string, error) {
	if len(data) == 0 || data[0] != 'd' {
		return "", errors.New("torrent has no top-level dictionary")
	}
	pos := 1
	for pos < len(data) && data[pos] != 'e' {
		key, end, err := bstring(data, pos)
		if err != nil {
			return "", err
		}
		pos = end
		start := pos
		pos, err = bend(data, pos, 0)
		if err != nil {
			return "", err
		}
		if key == "info" {
			if data[start] != 'd' {
				return "", errors.New("torrent info is not a dictionary")
			}
			sum := sha1.Sum(data[start:pos])
			return hex.EncodeToString(sum[:]), nil
		}
	}
	return "", errors.New("torrent has no info dictionary")
}

func bstring(data []byte, pos int) (string, int, error) {
	start := pos
	for pos < len(data) && data[pos] >= '0' && data[pos] <= '9' {
		pos++
	}
	if pos == start || pos >= len(data) || data[pos] != ':' || pos-start > 12 {
		return "", 0, errors.New("invalid bencoded string")
	}
	n, err := strconv.Atoi(string(data[start:pos]))
	if err != nil || n < 0 || n > len(data)-pos-1 {
		return "", 0, errors.New("invalid bencoded string length")
	}
	return string(data[pos+1 : pos+1+n]), pos + 1 + n, nil
}

func bend(data []byte, pos, depth int) (int, error) {
	if pos >= len(data) || depth > 64 {
		return 0, errors.New("invalid bencoded value")
	}
	switch data[pos] {
	case 'i':
		end := pos + 1
		for end < len(data) && data[end] != 'e' {
			end++
		}
		if end == len(data) || end == pos+1 {
			return 0, errors.New("invalid bencoded integer")
		}
		if _, err := strconv.ParseInt(string(data[pos+1:end]), 10, 64); err != nil {
			return 0, fmt.Errorf("invalid bencoded integer: %w", err)
		}
		return end + 1, nil
	case 'l', 'd':
		isDict := data[pos] == 'd'
		pos++
		for pos < len(data) && data[pos] != 'e' {
			if isDict {
				_, next, err := bstring(data, pos)
				if err != nil {
					return 0, err
				}
				pos = next
			}
			next, err := bend(data, pos, depth+1)
			if err != nil {
				return 0, err
			}
			pos = next
		}
		if pos == len(data) {
			return 0, errors.New("unterminated bencoded collection")
		}
		return pos + 1, nil
	default:
		_, end, err := bstring(data, pos)
		return end, err
	}
}
