package config

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/zeebo/blake3"
)

// EncodeCWDDirname turns a workspace path into a filesystem-safe sessions
// directory name. Short paths stay URL-encoded; long paths use slug-hash form.
func EncodeCWDDirname(cwd string) string {
	encoded := url.PathEscape(cwd)
	if len(encoded) <= maxDirnameBytes {
		return encoded
	}
	sum := blake3.Sum256([]byte(cwd))
	hash16 := hex16(sum[:])
	leaf := filepath.Base(cwd)
	if leaf == "" || leaf == "." || leaf == string(filepath.Separator) {
		leaf = "workspace"
	}
	slug := slugify(leaf, 40)
	if slug == "" {
		slug = "workspace"
	}
	return slug + "-" + hash16
}

func hex16(sum []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 0; i < 8; i++ {
		out[i*2] = hexdigits[sum[i]>>4]
		out[i*2+1] = hexdigits[sum[i]&0x0f]
	}
	return string(out)
}

func slugify(input string, maxLen int) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(input) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > maxLen {
		// Truncate on rune boundaries.
		n := 0
		for i := range s {
			if n == maxLen {
				return s[:i]
			}
			n++
		}
	}
	_ = unicode.ReplacementChar
	return s
}

// DecodeCWDFromDirname recovers the original CWD from a sessions directory.
func DecodeCWDFromDirname(dir string) (string, bool) {
	name := filepath.Base(dir)
	if decoded, err := url.PathUnescape(name); err == nil {
		if strings.HasPrefix(decoded, "/") || (len(decoded) >= 2 && decoded[1] == ':') {
			return decoded, true
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, ".cwd"))
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(data))
	return s, s != ""
}

// SessionsCWDDirIn is grokHome/sessions/{encoded cwd}.
func SessionsCWDDirIn(grokHome, cwd string) string {
	return filepath.Join(grokHome, "sessions", EncodeCWDDirname(cwd))
}

// EnsureSessionsCWDDirIn creates the CWD-level session directory with owner-only
// permissions and writes a .cwd metadata file for hash-encoded names.
func EnsureSessionsCWDDirIn(grokHome, cwd string) (string, error) {
	encoded := EncodeCWDDirname(cwd)
	dir := SessionsCWDDirIn(grokHome, cwd)
	if err := createDirAllOwnerOnly(dir); err != nil {
		return "", err
	}
	setOwnerOnly(filepath.Join(grokHome, "sessions"))
	if encoded != url.PathEscape(cwd) {
		cwdFile := filepath.Join(dir, ".cwd")
		f, err := os.OpenFile(cwdFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, _ = f.WriteString(cwd)
			_ = f.Close()
		} else if !os.IsExist(err) {
			return "", err
		}
	}
	return dir, nil
}
