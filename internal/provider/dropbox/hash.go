package dropbox

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

// blockSize is Dropbox's content_hash block: exactly 4 MiB.
const blockSize = 4 * 1024 * 1024

// HashReader computes Dropbox's content_hash of r: split into 4 MiB blocks,
// SHA-256 each block, concatenate the raw 32-byte digests, SHA-256 that, and
// hex-encode. An empty stream hashes the empty concatenation.
func HashReader(r io.Reader) (string, error) {
	outer := sha256.New()
	buf := make([]byte, blockSize)
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			d := sha256.Sum256(buf[:n])
			outer.Write(d[:])
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(outer.Sum(nil)), nil
}

// HashLocal computes the content_hash of a file on disk.
func HashLocal(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return HashReader(f)
}
