package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Called after validation and queue admission, under the producer lock. Publish
// with Link rather than Rename: an existing file must never be overwritten.
func (a *App) saveOriginal(job *Job) error {
	if job.originalPath == "" {
		return nil
	}
	source, err := os.Open(job.originalPath)
	if err != nil {
		return err
	}
	defer source.Close()
	h := sha256.New()
	if _, err = io.Copy(h, source); err != nil {
		return err
	}
	digest := hex.EncodeToString(h.Sum(nil))
	source.Seek(0, 0)
	if err = os.MkdirAll(a.cfg.Audio.Directory, 0755); err != nil {
		return fmt.Errorf("create sounds directory: %w", err)
	}
	root, err := os.OpenRoot(a.cfg.Audio.Directory)
	if err != nil {
		return err
	}
	defer root.Close()
	tmp := ".snapnotify-" + newID()
	out, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("write sounds directory: %w", err)
	}
	defer root.Remove(tmp)
	if _, err = io.Copy(out, source); err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	base := filenameID(job.Label)
	if len(base) > 48 {
		base = base[:48]
	}
	filename := ""
	created := false
	for _, length := range []int{12, 24, 40, 64} {
		candidate := base + "-" + digest[:length] + job.originalExtension
		err = root.Link(tmp, candidate)
		if err == nil {
			filename = candidate
			created = true
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("publish saved audio: %w", err)
		}
		info, e := root.Lstat(candidate)
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			continue
		}
		existing, e := root.Open(candidate)
		if e != nil {
			return e
		}
		hash := sha256.New()
		_, e = io.Copy(hash, existing)
		existing.Close()
		if e != nil {
			return e
		}
		if strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), digest) {
			filename = candidate
			break
		}
	}
	if filename == "" {
		return errors.New("could not allocate a saved sound filename")
	}
	sounds, err := a.sounds()
	if err == nil {
		for _, sound := range sounds {
			if sound.Filename == filename {
				job.SavedSound = &sound
				break
			}
		}
		if job.SavedSound == nil {
			err = errors.New("saved audio was not discovered")
		}
	}
	if err != nil {
		if created {
			root.Remove(filename)
		}
		return err
	}
	os.Remove(job.originalPath)
	job.originalPath = ""
	return nil
}
