package schemas

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// hidden reports whether a stored file is internal bookkeeping (manifest, index
// cache) rather than a schema. Such files begin with a dot.
func hidden(name string) bool { return strings.HasPrefix(name, ".") }

// ListLocal returns the repo-relative paths of every schema stored under dir,
// sorted. Internal files (manifest, index cache) are skipped, and a missing
// store directory yields an empty list rather than an error.
func ListLocal(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil // missing store root: empty list
			}
			return err
		}
		if d.IsDir() || hidden(d.Name()) || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// LocalPath joins a storage directory with a repo-relative schema path. The
// repo layout (category/name.json) is mirrored on disk so a pulled schema keeps
// a stable identity for later comparison.
func LocalPath(dir, schemaPath string) string {
	return filepath.Join(dir, filepath.FromSlash(schemaPath))
}

// Read returns the bytes of a locally stored schema. The bool reports whether
// the file exists; a missing file is not an error.
func Read(dir, schemaPath string) ([]byte, bool, error) {
	data, err := os.ReadFile(LocalPath(dir, schemaPath))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// Write stores a schema under dir, creating parent directories as needed.
// Schemas are not secrets, so we use ordinary 0644/0755 permissions (unlike the
// 0600 config file).
func Write(dir, schemaPath string, data []byte) error {
	p := LocalPath(dir, schemaPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// FindLocal resolves a selector to a stored schema's repo-relative path by
// scanning dir. It is the offline counterpart to Index.Resolve: it works against
// already-pulled files without touching the network. A selector may be a
// repo-relative path (trailing .json optional) or a bare schema name; a bare
// name that matches more than one stored file returns an ambiguity error.
func FindLocal(dir, selector string) (string, error) {
	q := strings.TrimSpace(selector)
	if q == "" {
		return "", fmt.Errorf("empty schema name")
	}

	if strings.Contains(q, "/") {
		rel := q
		if !strings.HasSuffix(rel, ".json") {
			rel += ".json"
		}
		if _, err := os.Stat(LocalPath(dir, rel)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("no stored schema at %q; pull it with `tabstack schema pull %s`", rel, q)
			}
			return "", err
		}
		return rel, nil
	}

	name := strings.TrimSuffix(q, ".json")
	locals, err := ListLocal(dir)
	if err != nil {
		return "", err
	}
	if len(locals) == 0 {
		return "", fmt.Errorf("no schemas stored yet; pull one with `tabstack schema pull %s`", q)
	}
	var matches []string
	for _, rel := range locals {
		if strings.TrimSuffix(path.Base(rel), ".json") == name {
			matches = append(matches, rel)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("no stored schema named %q; pull it with `tabstack schema pull %s`", q, q)
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("%q is ambiguous, matches %s; use a full path", q, strings.Join(matches, ", "))
	}
}

// Equal reports whether two schema documents are semantically equal, ignoring
// formatting differences (whitespace, key order). This keeps a re-pull from
// flagging a conflict when only the on-disk formatting differs from the remote.
// If either side is not valid JSON we fall back to a raw byte comparison.
func Equal(a, b []byte) bool {
	ca, ea := canonical(a)
	cb, eb := canonical(b)
	if ea != nil || eb != nil {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(ca, cb)
}

// canonical normalises a JSON document: json.Marshal emits map keys in sorted
// order, so re-marshalling a decoded value yields a stable canonical form.
func canonical(data []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
