package ghsrc

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blobServer answers tree and blob calls for n files, each changed between
// base and head, counting blob fetches and the most in flight at once.
type blobServer struct {
	trees          map[string]string
	blobs          map[string]string
	fetched        atomic.Int64
	inFlight, most atomic.Int64
	mu             sync.Mutex
}

func newBlobServer(t *testing.T, n int) *blobServer {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	s := &blobServer{trees: map[string]string{}, blobs: map[string]string{}}
	for _, ref := range []string{revisionBase, revisionHead} {
		var entries []revisionEntry
		for i := range n {
			body := fmt.Sprintf("file %d at %s\n", i, ref[:1])
			id := blobID([]byte(body))
			s.blobs[id] = body
			entries = append(entries, revisionEntry{Path: fmt.Sprintf("f%02d.go", i), Mode: "100644", Type: "blob", SHA: id})
		}
		data, _ := json.Marshal(map[string]any{"sha": ref, "tree": entries, "truncated": false})
		s.trees[ref] = string(data)
	}
	return s
}

func (s *blobServer) client(cache *BlobCache) Client {
	return Client{Blobs: cache, runOverride: func(_ []byte, args ...string) (string, error) {
		path := args[len(args)-1]
		if i := strings.Index(path, "/git/trees/"); i >= 0 {
			return s.trees[strings.TrimSuffix(path[i+len("/git/trees/"):], "?recursive=1")], nil
		}
		if i := strings.Index(path, "/git/blobs/"); i >= 0 {
			s.fetched.Add(1)
			cur := s.inFlight.Add(1)
			defer s.inFlight.Add(-1)
			for m := s.most.Load(); cur > m && !s.most.CompareAndSwap(m, cur); m = s.most.Load() {
			}
			time.Sleep(10 * time.Millisecond)
			sha := path[i+len("/git/blobs/"):]
			body := s.blobs[sha]
			data, _ := json.Marshal(map[string]any{"sha": sha, "encoding": "base64", "size": len(body), "content": base64.StdEncoding.EncodeToString([]byte(body))})
			return string(data), nil
		}
		return "", fmt.Errorf("unexpected: %v", args)
	}}
}

func TestCompareFetchesVersionsSideBySide(t *testing.T) {
	s := newBlobServer(t, 20)
	got, err := s.client(nil).CompareRevisions("acme/repo", revisionBase, revisionHead)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 20 || !strings.Contains(got.Diff, "+file 19 at 2") {
		t.Fatalf("comparison lost files: %d files\n%s", len(got.Files), got.Diff)
	}
	if n := s.fetched.Load(); n != 40 {
		t.Errorf("fetched %d versions, want 40", n)
	}
	if m := s.most.Load(); m < 2 || m > fetchWorkers {
		t.Errorf("at most %d fetches in flight, want between 2 and %d", m, fetchWorkers)
	}
}

func TestCompareReusesCachedVersions(t *testing.T) {
	s := newBlobServer(t, 5)
	cache := NewBlobCache(t.TempDir())
	first, err := s.client(cache).CompareRevisions("acme/repo", revisionBase, revisionHead)
	if err != nil {
		t.Fatal(err)
	}
	s.fetched.Store(0)
	var rec recorder
	c := s.client(cache)
	c.Trace = rec.tracer()
	again, err := c.CompareRevisions("acme/repo", revisionBase, revisionHead)
	if err != nil {
		t.Fatal(err)
	}
	if n := s.fetched.Load(); n != 0 {
		t.Errorf("reopening fetched %d versions, want none", n)
	}
	if again.Diff != first.Diff {
		t.Error("the cached comparison differs from the fetched one")
	}
	if !strings.Contains(strings.Join(rec.steps(), ","), "file versions: 10 of 10 cached") {
		t.Errorf("the panel does not say the versions were cached: %q", rec.steps())
	}
}

func TestCacheRefetchesADamagedEntry(t *testing.T) {
	s := newBlobServer(t, 1)
	cache := NewBlobCache(t.TempDir())
	if _, err := s.client(cache).CompareRevisions("acme/repo", revisionBase, revisionHead); err != nil {
		t.Fatal(err)
	}
	for sha := range s.blobs {
		if err := os.WriteFile(cache.path(sha), []byte("tampered"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s.fetched.Store(0)
	got, err := s.client(cache).CompareRevisions("acme/repo", revisionBase, revisionHead)
	if err != nil {
		t.Fatal(err)
	}
	if s.fetched.Load() != 2 || strings.Contains(got.Diff, "tampered") {
		t.Errorf("damaged entries were used (fetched %d):\n%s", s.fetched.Load(), got.Diff)
	}
}

func TestCachePrunesEntriesUnreadForAMonth(t *testing.T) {
	cache := NewBlobCache(t.TempDir())
	old, fresh := blobID([]byte("old")), blobID([]byte("fresh"))
	cache.put(old, []byte("old"))
	past := time.Now().Add(-cacheAge - time.Hour)
	if err := os.Chtimes(cache.path(old), past, past); err != nil {
		t.Fatal(err)
	}
	cache.prune = sync.Once{} // as a new process would
	cache.put(fresh, []byte("fresh"))
	if _, ok := cache.get(old); ok {
		t.Error("an entry unread for a month survived")
	}
	if _, ok := cache.get(fresh); !ok {
		t.Error("a new entry is missing")
	}
}
