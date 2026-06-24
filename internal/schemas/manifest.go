package schemas

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
)

// ManifestName is the bookkeeping file recording what was pulled and from which
// remote content, so `schema status` can tell local edits from upstream drift.
const ManifestName = ".manifest.json"

// ManifestEntry records the canonical content hash of a schema as it was last
// pulled, plus when. Comparing this against the current local file detects local
// edits; comparing it against the current remote detects upstream changes.
type ManifestEntry struct {
	SHA256   string `json:"sha256"`
	PulledAt string `json:"pulled_at"`
}

// Manifest is the decoded .manifest.json, keyed by repo-relative schema path.
type Manifest struct {
	Schemas map[string]ManifestEntry `json:"schemas"`
}

func manifestPath(dir string) string { return LocalPath(dir, ManifestName) }

// LoadManifest reads the store manifest; a missing manifest yields an empty one.
func LoadManifest(dir string) (Manifest, error) {
	m := Manifest{Schemas: map[string]ManifestEntry{}}
	data, err := os.ReadFile(manifestPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.Schemas == nil {
		m.Schemas = map[string]ManifestEntry{}
	}
	return m, nil
}

// Save writes the manifest. json.MarshalIndent emits map keys in sorted order,
// so the file stays diff-friendly across pulls.
//
// The write is atomic (temp file + rename) so a crash mid-write cannot leave a
// truncated, unparseable manifest behind. It is NOT locked, however: two
// concurrent `schema pull` runs against the same store still race, and the last
// writer wins, dropping the other's just-recorded entries (they then read as
// untracked until re-pulled). Running one pull at a time per store avoids this;
// a documented CLI limitation, not a guarantee.
func (m Manifest) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ManifestName+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, manifestPath(dir))
}

// Set records (or updates) the entry for a schema path.
func (m Manifest) Set(schemaPath, sha, pulledAt string) {
	m.Schemas[schemaPath] = ManifestEntry{SHA256: sha, PulledAt: pulledAt}
}

// Remove drops the entry for a schema path.
func (m Manifest) Remove(schemaPath string) {
	delete(m.Schemas, schemaPath)
}

// CanonicalSHA returns the hex SHA-256 of the canonical JSON form of data, so
// formatting-only differences do not change the hash. Non-JSON input is hashed
// as raw bytes.
func CanonicalSHA(data []byte) string {
	c, err := canonical(data)
	if err != nil {
		c = data
	}
	sum := sha256.Sum256(c)
	return hex.EncodeToString(sum[:])
}
