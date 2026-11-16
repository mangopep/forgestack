// Package playground manages isolated, local-only ForgeStack environments.
package playground

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const maxTTL = 24 * time.Hour

var safeID = regexp.MustCompile(`^env-[a-f0-9]{16}$`)

type Environment struct {
	ID        string    `json:"id"`
	Owner     string    `json:"owner"`
	Mode      string    `json:"mode"`
	Modules   []string  `json:"modules"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Status    string    `json:"status"`
}

type Result struct {
	Environment Environment `json:"environment"`
	Cleanup     string      `json:"cleanup,omitempty"`
}

type Manager struct{ removeAll func(string) error }

func NewManager() Manager { return Manager{removeAll: os.RemoveAll} }

func validateCreateInputs(owner, mode string, ttl time.Duration, modules []string) error {
	if strings.TrimSpace(owner) == "" {
		return errors.New("owner is required")
	}
	if mode != "playground" && mode != "product" {
		return errors.New("mode must be playground or product")
	}
	if mode == "playground" && (ttl <= 0 || ttl > maxTTL) {
		return fmt.Errorf("playground TTL must be between 1s and %s", maxTTL)
	}
	if mode == "product" && ttl != 0 {
		return errors.New("product environments cannot have an expiration")
	}
	for _, module := range modules {
		if strings.TrimSpace(module) == "" {
			return errors.New("module names cannot be empty")
		}
	}
	return nil
}

// realRootDir creates root if needed and returns its absolute path, rejecting
// a symlinked root so cleanup's ownership checks cannot be redirected.
func realRootDir(root string) (string, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", fmt.Errorf("create environment root: %w", err)
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootInfo, err := os.Lstat(rootPath)
	if err != nil {
		return "", err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("environment root must be a real directory, not a symlink")
	}
	return rootPath, nil
}

func (m Manager) Create(root, owner, mode string, ttl time.Duration, modules []string) (Environment, error) {
	if err := validateCreateInputs(owner, mode, ttl, modules); err != nil {
		return Environment{}, err
	}
	rootPath, err := realRootDir(root)
	if err != nil {
		return Environment{}, err
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Environment{}, fmt.Errorf("generate environment ID: %w", err)
	}
	now := time.Now().UTC()
	env := Environment{ID: "env-" + hex.EncodeToString(random[:]), Owner: owner, Mode: mode, Modules: append([]string(nil), modules...), CreatedAt: now, Status: "active"}
	if mode == "playground" {
		env.ExpiresAt = now.Add(ttl)
	}
	sort.Strings(env.Modules)
	dir := filepath.Join(rootPath, env.ID)
	if err := os.Mkdir(dir, 0700); err != nil {
		return Environment{}, fmt.Errorf("create environment: %w", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "resources"), 0700); err != nil {
		_ = os.Remove(dir)
		return Environment{}, fmt.Errorf("create owned resource directory: %w", err)
	}
	if err := writeMetadata(filepath.Join(dir, "environment.json"), env); err != nil {
		_ = m.removeAll(dir)
		return Environment{}, fmt.Errorf("write environment ownership record: %w", err)
	}
	return env, nil
}

// cleanupRootState validates root is a real, non-symlinked directory and
// snapshots its entries before any removal, so a later inventory comparison
// can detect anything outside the owned environment changing.
func cleanupRootState(root string) (rootPath string, entriesBefore []os.DirEntry, err error) {
	rootPath, err = filepath.Abs(root)
	if err != nil {
		return "", nil, err
	}
	rootInfo, err := os.Lstat(rootPath)
	if err != nil {
		return "", nil, fmt.Errorf("inspect environment root: %w", err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", nil, errors.New("environment root must be a real directory")
	}
	entriesBefore, err = os.ReadDir(rootPath)
	if err != nil {
		return "", nil, err
	}
	return rootPath, entriesBefore, nil
}

// loadOwnedEnvironment reads and validates the ownership record at
// metadataPath, confirming the caller-supplied id and owner match it and
// that the environment is a playground (cleanup is forbidden for product).
func loadOwnedEnvironment(metadataPath, id, owner string) (Environment, error) {
	metaInfo, err := os.Lstat(metadataPath)
	if err != nil {
		return Environment{}, errors.New("environment ownership record is missing")
	}
	if !metaInfo.Mode().IsRegular() {
		return Environment{}, errors.New("environment ownership record is not a regular file")
	}
	var env Environment
	data, err := os.ReadFile(metadataPath) //nolint:gosec // G304: path is built from a regex-validated environment ID, not arbitrary input
	if err != nil {
		return Environment{}, err
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return Environment{}, fmt.Errorf("read environment ownership record: %w", err)
	}
	if env.ID != id || env.Owner != owner {
		return Environment{}, errors.New("environment ID or owner does not match ownership record")
	}
	if env.Mode != "playground" {
		return Environment{}, errors.New("automatic cleanup is forbidden for persistent product environments")
	}
	return env, nil
}

// removeOwnedResources removes dir's resources subdirectory (if present) and
// verifies its absence afterward, recording cleanup_incomplete or
// cleanup_unverified on env rather than ever inferring success.
func (m Manager) removeOwnedResources(dir, metadataPath string, env Environment) (Result, bool, error) {
	resources := filepath.Join(dir, "resources")
	resourceInfo, err := os.Lstat(resources)
	alreadyAbsent := errors.Is(err, fs.ErrNotExist)
	if err != nil && !alreadyAbsent {
		return Result{}, false, err
	}
	if !alreadyAbsent {
		if !resourceInfo.IsDir() || resourceInfo.Mode()&os.ModeSymlink != 0 {
			return Result{}, false, errors.New("owned resources path is not a real directory")
		}
		if err := m.removeAll(resources); err != nil {
			env.Status = "cleanup_incomplete"
			_ = writeMetadata(metadataPath, env)
			return Result{Environment: env, Cleanup: "CLEANUP INCOMPLETE"}, false, fmt.Errorf("remove owned local resources: %w", err)
		}
	}
	if _, err := os.Lstat(resources); !errors.Is(err, fs.ErrNotExist) {
		env.Status = "cleanup_unverified"
		_ = writeMetadata(metadataPath, env)
		return Result{Environment: env, Cleanup: "CLEANUP UNVERIFIED"}, false, errors.New("owned resources remain or cannot be verified absent")
	}
	return Result{Environment: env}, alreadyAbsent, nil
}

// finalizeCleanup compares the root's sibling inventory against its
// pre-removal snapshot and only then records verified_clean.
func finalizeCleanup(rootPath, id, metadataPath string, env Environment, entriesBefore []os.DirEntry, alreadyAbsent bool) (Result, error) {
	entriesAfter, err := os.ReadDir(rootPath)
	if err != nil {
		return Result{Environment: env, Cleanup: "CLEANUP UNVERIFIED"}, fmt.Errorf("verify environment inventory: %w", err)
	}
	if !sameSiblingInventory(entriesBefore, entriesAfter, id) {
		return Result{Environment: env, Cleanup: "CLEANUP UNVERIFIED"}, errors.New("environment inventory changed outside the owned resource directory")
	}
	env.Status = "verified_clean"
	if err := writeMetadata(metadataPath, env); err != nil {
		return Result{Environment: env, Cleanup: "CLEANUP UNVERIFIED"}, fmt.Errorf("persist cleanup evidence: %w", err)
	}
	status := "VERIFIED CLEAN"
	if alreadyAbsent {
		status += " (already absent)"
	}
	return Result{Environment: env, Cleanup: status}, nil
}

// Cleanup removes only the verified environment's resources directory. It retains
// the ownership record as evidence and never traverses a caller-selected resource path.
func (m Manager) Cleanup(root, id, owner string) (Result, error) {
	if !safeID.MatchString(id) {
		return Result{}, errors.New("invalid environment ID")
	}
	if strings.TrimSpace(owner) == "" {
		return Result{}, errors.New("owner is required")
	}
	rootPath, entriesBefore, err := cleanupRootState(root)
	if err != nil {
		return Result{}, err
	}
	dir := filepath.Join(rootPath, id)
	dirInfo, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return Result{Cleanup: "VERIFIED CLEAN (already absent)"}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("inspect environment: %w", err)
	}
	if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return Result{}, errors.New("environment path is not a real directory")
	}
	metadataPath := filepath.Join(dir, "environment.json")
	env, err := loadOwnedEnvironment(metadataPath, id, owner)
	if err != nil {
		return Result{}, err
	}
	removed, alreadyAbsent, err := m.removeOwnedResources(dir, metadataPath, env)
	if err != nil {
		return removed, err
	}
	return finalizeCleanup(rootPath, id, metadataPath, removed.Environment, entriesBefore, alreadyAbsent)
}

func sameSiblingInventory(before, after []os.DirEntry, id string) bool {
	toNames := func(entries []os.DirEntry) []string {
		var names []string
		for _, entry := range entries {
			if entry.Name() != id {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)
		return names
	}
	a, b := toNames(before), toNames(after)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func writeMetadata(path string, env Environment) error {
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
