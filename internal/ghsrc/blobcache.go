package ghsrc

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// BlobCache keeps file contents fetched for comparisons on disk, by their
// git object id. An id names exactly one content, so an entry never goes
// stale: the next review round finds the versions the last one fetched, and
// reopening a pull request fetches nothing at all.
//
// Entries are checked against their id on the way out as on the way in, so
// a damaged file is a miss, never wrong content.
type BlobCache struct {
	dir   string
	prune sync.Once
}

// cacheAge is how long an entry nobody has read survives.
const cacheAge = 30 * 24 * time.Hour

// NewBlobCache keeps its entries under dir, creating it when first written.
func NewBlobCache(dir string) *BlobCache { return &BlobCache{dir: dir} }

func (b *BlobCache) path(sha string) string { return filepath.Join(b.dir, sha[:2], sha) }

// get returns the content for sha, if cached and intact.
func (b *BlobCache) get(sha string) ([]byte, bool) {
	if b == nil {
		return nil, false
	}
	path := b.path(sha)
	data, err := os.ReadFile(path)
	if err != nil || blobID(data) != sha {
		return nil, false
	}
	t := time.Now()
	_ = os.Chtimes(path, t, t) // read recently: keep
	return data, true
}

// put stores content already checked against sha. A cache that cannot be
// written is only slower, so failures are ignored.
func (b *BlobCache) put(sha string, data []byte) {
	if b == nil {
		return
	}
	b.prune.Do(b.pruneOld)
	path := b.path(sha)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// pruneOld removes entries unread for cacheAge, once per process, so the
// cache holds what recent reviews use rather than growing forever.
func (b *BlobCache) pruneOld() {
	cutoff := time.Now().Add(-cacheAge)
	_ = filepath.WalkDir(b.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
		}
		return nil
	})
}

// blobID is the git object id of content: what hash-object would print.
func blobID(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
