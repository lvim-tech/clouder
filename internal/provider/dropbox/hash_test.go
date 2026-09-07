package dropbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// refHash is an independent, in-memory implementation of Dropbox's content_hash,
// used to check the streaming HashReader at block boundaries.
func refHash(data []byte) string {
	outer := sha256.New()
	for off := 0; off < len(data); off += blockSize {
		end := off + blockSize
		if end > len(data) {
			end = len(data)
		}
		d := sha256.Sum256(data[off:end])
		outer.Write(d[:])
	}
	return hex.EncodeToString(outer.Sum(nil))
}

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + 7)
	}
	return b
}

func TestHashEmpty(t *testing.T) {
	// Zero blocks: SHA-256 of the empty concatenation.
	const want = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	got, err := HashReader(bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("empty hash = %s, want %s", got, want)
	}
}

func TestHashBoundaries(t *testing.T) {
	sizes := []int{1, blockSize - 1, blockSize, blockSize + 1, 3 * blockSize, 3*blockSize + 123}
	for _, n := range sizes {
		data := pattern(n)
		got, err := HashReader(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("size %d: %v", n, err)
		}
		if want := refHash(data); got != want {
			t.Fatalf("size %d: got %s want %s", n, got, want)
		}
	}
}
