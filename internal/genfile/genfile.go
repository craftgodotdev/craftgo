// Package genfile puts generated files on disk. While [Collect] runs, a write
// is held in memory; [Commit] then writes every file beside its target under a
// temporary name and renames the temporaries into place once all of them are
// written, so a pass that fails to render, or to write a file, leaves the
// tree as it was.
package genfile

import (
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
)

// File is one file a pass writes.
type File struct {
	Path string
	Body []byte
	// Once marks a gen-once file, written only while nothing is at Path.
	Once bool
}

var (
	// collecting keeps [Collect] calls from overlapping.
	collecting sync.Mutex
	// pending holds the files of the running [Collect], nil outside one.
	pending atomic.Pointer[set]
)

// set is the files of one [Collect], in the order first written.
type set struct {
	mu    sync.Mutex
	files []File
	at    map[string]int
}

// add records f: a regenerated file replaces what the set holds at its path,
// a gen-once file never does.
func (s *set) add(f File) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := filepath.Clean(f.Path)
	if i, ok := s.at[key]; ok {
		if !f.Once {
			s.files[i] = f
		}
		return
	}
	s.at[key] = len(s.files)
	s.files = append(s.files, f)
}

func (s *set) has(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.at[filepath.Clean(path)]
	return ok
}

// Collect runs render with every [Write] and [WriteOnce] held in memory, and
// returns the files they received, in the order first written; nothing
// reaches the disk. One Collect runs at a time, and a write from another
// goroutine meanwhile lands in it too.
func Collect(render func() error) ([]File, error) {
	collecting.Lock()
	defer collecting.Unlock()
	s := &set{at: map[string]int{}}
	pending.Store(s)
	defer pending.Store(nil)
	if err := render(); err != nil {
		return nil, err
	}
	return s.files, nil
}

// Write writes body to path: into the running [Collect], else at once,
// through [Commit].
func Write(path string, body []byte) error {
	return put(File{Path: path, Body: body})
}

// WriteOnce writes the file render returns to path unless [Exists] finds one
// there, rendering nothing then.
func WriteOnce(path string, render func() ([]byte, error)) error {
	if Exists(path) {
		return nil
	}
	body, err := render()
	if err != nil {
		return err
	}
	return put(File{Path: path, Body: body, Once: true})
}

// put hands f to the running [Collect], or commits it alone.
func put(f File) error {
	if s := pending.Load(); s != nil {
		s.add(f)
		return nil
	}
	return Commit([]File{f})
}

// Exists reports whether something is at path, on disk or in the running
// [Collect]; a symbolic link counts, whatever it leads to.
func Exists(path string) bool {
	if s := pending.Load(); s != nil && s.has(path) {
		return true
	}
	_, err := os.Lstat(path)
	return err == nil
}

// Commit writes files, each through a temporary file in its target's
// directory: once every temporary is written, each is renamed over its
// target. A gen-once file is skipped while something is at its path. An
// existing file keeps its mode, a link is written through, and a file that
// cannot be opened for writing stops the commit as an in-place write would.
// A failure before the renames removes the temporaries and the directories
// made for them, leaving the tree as it was.
func Commit(files []File) error {
	var c commit
	for _, f := range files {
		if err := c.stage(f); err != nil {
			c.abort()
			return err
		}
	}
	return c.apply()
}

// commit is the state of one [Commit].
type commit struct {
	staged []staged
	// made lists the directories made for the temporaries, parents first.
	made []string
}

// staged is a temporary written for a file.
type staged struct {
	tmp, target string
	file        File
}

// stage writes f's temporary.
func (c *commit) stage(f File) error {
	target := f.Path
	mode, keepMode := fs.FileMode(0), false
	info, err := os.Lstat(target)
	switch {
	case err == nil && f.Once:
		return nil
	case err == nil:
		if info.Mode()&fs.ModeSymlink != 0 {
			if resolved, rerr := filepath.EvalSymlinks(target); rerr == nil {
				target = resolved
				if info, err = os.Stat(resolved); err != nil {
					return writeError(f.Path, err)
				}
			}
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			if err := checkReplaceable(target, info); err != nil {
				return writeError(f.Path, err)
			}
			mode, keepMode = info.Mode().Perm(), true
		}
	case !errors.Is(err, fs.ErrNotExist):
		return writeError(f.Path, err)
	}
	made, err := mkdirAll(filepath.Dir(target))
	c.made = append(c.made, made...)
	if err != nil {
		return writeError(f.Path, err)
	}
	tmp, err := writeTemp(target, f.Body, mode, keepMode, f.Once)
	if err != nil {
		return writeError(f.Path, err)
	}
	c.staged = append(c.staged, staged{tmp: tmp, target: target, file: f})
	return nil
}

// checkReplaceable refuses a target that is not a file, or that the user
// cannot write.
func checkReplaceable(target string, info fs.FileInfo) error {
	if info.IsDir() {
		return errors.New("a directory is in the way")
	}
	if !info.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	w, err := os.OpenFile(target, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	return w.Close()
}

// apply renames every temporary over its target.
func (c *commit) apply() error {
	for i, s := range c.staged {
		if s.file.Once {
			if _, err := os.Lstat(s.target); err == nil {
				_ = os.Remove(s.tmp)
				continue
			}
		}
		if err := os.Rename(s.tmp, s.target); err != nil {
			for _, rest := range c.staged[i:] {
				_ = os.Remove(rest.tmp)
			}
			err = writeError(s.file.Path, err)
			if i > 0 {
				err = fmt.Errorf("%w - the %d file(s) before it were written", err, i)
			}
			return err
		}
	}
	return nil
}

// abort removes the temporaries and the directories made for them.
func (c *commit) abort() {
	for _, s := range c.staged {
		_ = os.Remove(s.tmp)
	}
	for i := len(c.made) - 1; i >= 0; i-- {
		_ = os.Remove(c.made[i])
	}
}

// writeError reports a file that could not be written, with the cause of a
// path error alone, since path is the file the user knows.
func writeError(path string, err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return fmt.Errorf("cannot write %s: %w", path, err)
}

// mkdirAll makes dir and its missing parents, and returns the directories it
// made, parents first.
func mkdirAll(dir string) ([]string, error) {
	var missing []string
	for d := dir; ; d = filepath.Dir(d) {
		info, err := os.Stat(d)
		if err == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("%s is not a directory", d)
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		missing = append(missing, d)
		if filepath.Dir(d) == d {
			break
		}
	}
	var made []string
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0o755); err != nil {
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			return made, err
		}
		made = append(made, missing[i])
	}
	return made, nil
}

// writeTemp writes body to a new file beside target and returns its path. A
// new file takes 0o644 less the umask, as os.WriteFile would; keepMode gives
// it mode instead. sync flushes it to the device, for a gen-once file no
// later run rewrites.
func writeTemp(target string, body []byte, mode fs.FileMode, keepMode, sync bool) (string, error) {
	dir, base := filepath.Split(target)
	for attempt := 0; ; attempt++ {
		tmp := filepath.Join(dir, "."+base+"."+strconv.FormatUint(uint64(rand.Uint32()), 36)+".tmp")
		f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) && attempt < 100 {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = f.Write(body)
		if err == nil && sync {
			err = f.Sync()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil && keepMode {
			err = os.Chmod(tmp, mode)
		}
		if err != nil {
			_ = os.Remove(tmp)
			return "", err
		}
		return tmp, nil
	}
}
