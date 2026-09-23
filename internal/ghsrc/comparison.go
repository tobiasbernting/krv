package ghsrc

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// RevisionComparison compares two immutable snapshots, even when a force push
// made their commits unrelated. File maps include unchanged paths. Their values,
// like OldBlob and NewBlob, are mode:object-ID fingerprints; a missing path has
// the empty fingerprint. Including modes also invalidates verification on chmod.
type RevisionComparison struct {
	BaseSHA, HeadSHA string
	Diff             string
	Files            []RevisionFile
	BaseFiles        map[string]string
	HeadFiles        map[string]string
}

type RevisionFile struct {
	Path, PreviousPath string
	OldBlob, NewBlob   string
	Status             string
}

type revisionEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

func (e revisionEntry) fingerprint() string { return e.Mode + ":" + e.SHA }

// CompareRevisions reads tree metadata and downloads only changed blobs. It
// constructs disposable Git trees without checking out or executing PR files.
// GitHub's compare endpoint cannot substitute for this: it uses a merge base,
// which loses changes reverted by a rebase or force push since the last review.
// Any unavailable object or incomplete API response fails the entire comparison.
func (c Client) CompareRevisions(repo, baseSHA, headSHA string) (*RevisionComparison, error) {
	if !revisionObjectID(baseSHA) || !revisionObjectID(headSHA) {
		return nil, fmt.Errorf("revision comparison requires full immutable commit IDs")
	}
	trees := c.traced("compare trees")
	base, err := trees.revisionTree(repo, baseSHA)
	if err != nil {
		return nil, fmt.Errorf("review baseline %s unavailable: %w", baseSHA, err)
	}
	head := base
	if baseSHA != headSHA {
		head, err = trees.revisionTree(repo, headSHA)
		if err != nil {
			return nil, fmt.Errorf("review head %s unavailable: %w", headSHA, err)
		}
	}
	result := &RevisionComparison{BaseSHA: baseSHA, HeadSHA: headSHA,
		BaseFiles: make(map[string]string, len(base)), HeadFiles: make(map[string]string, len(head))}
	oldChanged, newChanged := map[string]revisionEntry{}, map[string]revisionEntry{}
	for path, entry := range base {
		result.BaseFiles[path] = entry.fingerprint()
		if entry != head[path] {
			oldChanged[path] = entry
		}
	}
	for path, entry := range head {
		result.HeadFiles[path] = entry.fingerprint()
		if entry != base[path] {
			newChanged[path] = entry
		}
	}
	if len(oldChanged)+len(newChanged) == 0 {
		return result, nil
	}
	dir, err := os.MkdirTemp("", "krv-revision-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	git := revisionGit{dir: dir, trace: c.Trace, group: c.Trace.group()}
	if _, err := git.run(nil, "init", "--bare", "--quiet", "--template=", "."); err != nil {
		return nil, err
	}
	var blobs []revisionEntry
	listed := map[string]bool{}
	for _, entries := range []map[string]revisionEntry{oldChanged, newChanged} {
		for _, entry := range entries {
			if entry.Type != "blob" || listed[entry.SHA] {
				continue
			}
			blobs = append(blobs, entry)
			listed[entry.SHA] = true
		}
	}
	files := c.traced("compare files")
	for k, entry := range blobs {
		label := fmt.Sprintf("compare files %d/%d", k+1, len(blobs))
		data, err := files.relabel(label).revisionBlob(repo, entry.SHA)
		if err != nil {
			return nil, fmt.Errorf("cannot compare %q: %w", entry.Path, err)
		}
		id, err := git.run(data, "hash-object", "-w", "--stdin")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(id) != entry.SHA {
			return nil, fmt.Errorf("cannot compare %q: downloaded blob does not match %s", entry.Path, entry.SHA)
		}
	}
	oldTree, err := git.tree(oldChanged)
	if err != nil {
		return nil, err
	}
	newTree, err := git.tree(newChanged)
	if err != nil {
		return nil, err
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--find-renames", "--src-prefix=a/", "--dst-prefix=b/"}
	result.Diff, err = git.run(nil, append(append([]string{}, args...), "--binary", oldTree, newTree, "--")...)
	if err != nil {
		return nil, err
	}
	status, err := git.run(nil, append(args, "--name-status", "-z", oldTree, newTree, "--")...)
	if err != nil {
		return nil, err
	}
	result.Files, err = revisionFiles(status, result.BaseFiles, result.HeadFiles)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func revisionObjectID(id string) bool {
	if len(id) != 40 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && strings.ToLower(id) == id
}

func (c Client) revisionTree(repo, sha string) (map[string]revisionEntry, error) {
	out, err := c.run("api", fmt.Sprintf("repos/%s/git/trees/%s?recursive=1", repo, sha))
	if err != nil {
		return nil, err
	}
	var response struct {
		SHA       string           `json:"sha"`
		Tree      *[]revisionEntry `json:"tree"`
		Truncated *bool            `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		return nil, fmt.Errorf("invalid tree response: %w", err)
	}
	if response.Tree == nil || response.Truncated == nil || !revisionObjectID(response.SHA) {
		return nil, fmt.Errorf("incomplete tree response")
	}
	if *response.Truncated {
		return nil, fmt.Errorf("GitHub truncated the repository tree; a complete comparison is unavailable")
	}
	entries := make(map[string]revisionEntry, len(*response.Tree))
	seen := map[string]bool{}
	for _, entry := range *response.Tree {
		valid := entry.Type == "blob" && (entry.Mode == "100644" || entry.Mode == "100755" || entry.Mode == "120000") ||
			entry.Type == "commit" && entry.Mode == "160000" || entry.Type == "tree" && entry.Mode == "040000"
		if !valid || !revisionObjectID(entry.SHA) || !revisionPath(entry.Path) || seen[entry.Path] {
			return nil, fmt.Errorf("invalid tree entry %q (%s/%s)", entry.Path, entry.Type, entry.Mode)
		}
		seen[entry.Path] = true
		if entry.Type != "tree" {
			entries[entry.Path] = entry
		}
	}
	return entries, nil
}

func revisionPath(path string) bool {
	if strings.ContainsRune(path, '\x00') {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func (c Client) revisionBlob(repo, sha string) ([]byte, error) {
	out, err := c.run("api", fmt.Sprintf("repos/%s/git/blobs/%s", repo, sha))
	if err != nil {
		return nil, err
	}
	var response struct {
		Encoding string  `json:"encoding"`
		Content  *string `json:"content"`
		Size     *int    `json:"size"`
		SHA      string  `json:"sha"`
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		return nil, fmt.Errorf("invalid blob response: %w", err)
	}
	if response.Encoding != "base64" || response.Content == nil || response.Size == nil || response.SHA != sha {
		return nil, fmt.Errorf("incomplete or unsupported blob response for %s", sha)
	}
	data, err := base64.StdEncoding.DecodeString(*response.Content)
	if err != nil {
		return nil, fmt.Errorf("invalid blob encoding: %w", err)
	}
	if len(data) != *response.Size {
		return nil, fmt.Errorf("incomplete blob content for %s", sha)
	}
	return data, nil
}

// revisionGit ignores caller Git overrides and user config. Every object write
// stays in its temporary bare repository; filenames are only mktree stdin data.
type revisionGit struct {
	dir   string
	trace *Tracer
	group string // every git command is one "build compare" line
}

func (g revisionGit) run(input []byte, args ...string) (_ string, err error) {
	id := g.trace.start("build compare", g.group, "git", args)
	defer func() { g.trace.end(id, err) }()
	cmd := exec.Command("git", args...)
	cmd.Dir = g.dir
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_ATTR_NOSYSTEM=1")
	cmd.Stdin = bytes.NewReader(input)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("cannot construct revision comparison: git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out.String(), nil
}

func (g revisionGit) tree(entries map[string]revisionEntry) (string, error) {
	dirs := map[string]map[string]revisionEntry{}
	var records []string
	for path, entry := range entries {
		first, rest, nested := strings.Cut(path, "/")
		if nested {
			if dirs[first] == nil {
				dirs[first] = map[string]revisionEntry{}
			}
			dirs[first][rest] = entry
		} else {
			records = append(records, fmt.Sprintf("%s %s %s\t%s\x00", entry.Mode, entry.Type, entry.SHA, first))
		}
	}
	for name, children := range dirs {
		id, err := g.tree(children)
		if err != nil {
			return "", err
		}
		records = append(records, fmt.Sprintf("040000 tree %s\t%s\x00", id, name))
	}
	sort.Strings(records)
	out, err := g.run([]byte(strings.Join(records, "")), "mktree", "-z", "--missing")
	return strings.TrimSpace(out), err
}

func revisionFiles(raw string, base, head map[string]string) ([]RevisionFile, error) {
	parts := strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00")
	var files []RevisionFile
	for len(parts) >= 2 {
		status, path := parts[0], parts[1]
		parts = parts[2:]
		file := RevisionFile{Path: path, OldBlob: base[path], NewBlob: head[path]}
		switch {
		case status == "A":
			file.Status = "added"
		case status == "D":
			file.Status = "removed"
		case status == "M" || status == "T":
			file.Status = "modified"
		case strings.HasPrefix(status, "R") && len(parts) > 0:
			file.Status, file.PreviousPath, file.Path = "renamed", path, parts[0]
			file.NewBlob = head[file.Path]
			parts = parts[1:]
		default:
			return nil, fmt.Errorf("unsupported revision status %q", status)
		}
		files = append(files, file)
	}
	if len(parts) > 0 && !(len(parts) == 1 && parts[0] == "") {
		return nil, fmt.Errorf("incomplete revision file listing")
	}
	return files, nil
}
