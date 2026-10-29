package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Sound struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Source   string `json:"source"`
}

var idSeparators = regexp.MustCompile(`[^a-z0-9_-]+`)

func filenameID(name string) string {
	name = filepath.Base(name)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	name = idSeparators.ReplaceAllString(strings.ToLower(name), "_")
	name = strings.Trim(name, "_-")
	if name == "" {
		name = "sound"
	}
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

func suffixedID(base, key string, length int) string {
	hash := sha256.Sum256([]byte(key))
	suffix := hex.EncodeToString(hash[:])[:length]
	if len(base) > 63-length {
		base = base[:63-length]
	}
	return base + "-" + suffix
}

// Re-read directory metadata on each lookup: additions and removals do not
// require a restart or a filesystem watcher. Explicit mappings take priority.
func (a *App) sounds() ([]Sound, error) {
	result := make([]Sound, 0, len(a.cfg.Sounds))
	used := map[string]bool{}
	mapped := map[string]bool{}
	for id, name := range a.cfg.Sounds {
		result = append(result, Sound{id, name, "mapping"})
		used[id] = true
		mapped[filepath.Clean(name)] = true
	}
	if a.cfg.Audio.ScanDirectory {
		entries, err := os.ReadDir(a.cfg.Audio.Directory)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		names := []string{}
		counts := map[string]int{}
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || mapped[name] {
				continue
			}
			ext := strings.ToLower(filepath.Ext(name))
			if ext != ".mp3" && ext != ".wav" && ext != ".ogg" {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return nil, err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			names = append(names, name)
			counts[filenameID(name)]++
		}
		// os.ReadDir is sorted. Colliding file stems all receive suffixes, so a
		// bare ID is never silently reassigned to a different file on collision.
		for _, name := range names {
			base := filenameID(name)
			id := base
			if counts[base] > 1 || used[id] {
				for n := 8; ; n += 4 {
					if n > 32 {
						return nil, errors.New("could not assign unique sound ID")
					}
					id = suffixedID(base, name, n)
					if !used[id] && counts[id] == 0 {
						break
					}
				}
			}
			used[id] = true
			result = append(result, Sound{id, name, "directory"})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}
