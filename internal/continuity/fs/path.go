/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package fs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

var errTooManyLinks = errors.New("too many links")

type File struct {
	Path     string
	FullPath string
	os.FileInfo
}

func pathChange(lower, upper *File) (ChangeKind, string) {
	if lower == nil {
		if upper == nil {
			panic("cannot compare nil paths")
		}
		return ChangeKindAdd, upper.Path
	}
	if upper == nil {
		return ChangeKindDelete, lower.Path
	}

	switch i := directoryCompare(lower.Path, upper.Path); {
	case i < 0:
		// File in lower that is not in upper
		return ChangeKindDelete, lower.Path
	case i > 0:
		// File in upper that is not in lower
		return ChangeKindAdd, upper.Path
	default:
		return ChangeKindModify, upper.Path
	}
}

func directoryCompare(a, b string) int {
	l := len(a)
	if len(b) < l {
		l = len(b)
	}
	for i := 0; i < l; i++ {
		c1, c2 := a[i], b[i]
		if c1 == filepath.Separator {
			c1 = byte(0)
		}
		if c2 == filepath.Separator {
			c2 = byte(0)
		}
		if c1 < c2 {
			return -1
		}
		if c1 > c2 {
			return +1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return +1
	}
	return 0
}

// SameFile || !SysStat || !Capabilities || IsDir || !Size || !Unix || !UnixNano || Symlink || ZeroSize || Content
func sameFile(f1, f2 *File) (bool, error) {
	if os.SameFile(f1.FileInfo, f2.FileInfo) {
		return true, nil
	}

	equalStat, err := CompareSysStat(f1, f2)
	if err != nil || !equalStat {
		return equalStat, err
	}

	if eq, err := CompareCapabilities(f1, f2); err != nil || !eq {
		return eq, err
	}

	// If not a directory also check size, modtime, and content
	if !f1.IsDir() {
		if f1.Size() != f2.Size() {
			return false, nil
		}
		t1 := f1.ModTime()
		t2 := f2.ModTime()

		if t1.Unix() != t2.Unix() {
			return false, nil
		}

		// If the timestamp may have been truncated in both of the
		// files, check content of file to determine difference
		if t1.Nanosecond() == 0 && t2.Nanosecond() == 0 {
			if (f1.Mode() & os.ModeSymlink) == os.ModeSymlink {
				return compareSymlinkTarget(f1.FullPath, f2.FullPath)
			}
			if f1.Size() == 0 { // if file sizes are zero length, the files are the same by definition
				return true, nil
			}
			return SameFileContent(f1, f2)
		} else if t1.Nanosecond() != t2.Nanosecond() {
			return false, nil
		}
	}

	return true, nil
}

func compareSymlinkTarget(p1, p2 string) (bool, error) {
	t1, err := os.Readlink(p1)
	if err != nil {
		return false, err
	}
	t2, err := os.Readlink(p2)
	if err != nil {
		return false, err
	}
	return t1 == t2, nil
}

const compareChuckSize = 32 * 1024

// SameFileContent compares the content of 2 same sized files
// by comparing each byte.
func SameFileContent(f1, f2 *File) (bool, error) {
	p1, p2 := f1.FullPath, f2.FullPath
	osf1, err := os.Open(p1)
	if err != nil {
		return false, err
	}
	defer osf1.Close()
	osf2, err := os.Open(p2)
	if err != nil {
		return false, err
	}
	defer osf2.Close()

	b1 := make([]byte, compareChuckSize)
	b2 := make([]byte, compareChuckSize)
	for {
		n1, err1 := osf1.Read(b1)
		if err1 != nil && err1 != io.EOF {
			return false, err1
		}
		n2, err2 := osf2.Read(b2)
		if err2 != nil && err2 != io.EOF {
			return false, err2
		}
		if n1 != n2 || !bytes.Equal(b1[:n1], b2[:n2]) {
			return false, nil
		}
		if err1 == io.EOF && err2 == io.EOF {
			return true, nil
		}
	}
}

func pathWalk(ctx context.Context, root string, pathC chan<- *File) error {
	return filepath.Walk(root, func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Rebase path
		path, err = filepath.Rel(root, path)
		if err != nil {
			return err
		}

		path = filepath.Join(string(os.PathSeparator), path)

		// Skip root
		if path == string(os.PathSeparator) {
			return nil
		}

		p := &File{
			path,
			filepath.Join(root, path),
			f,
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case pathC <- p:
			return nil
		}
	})
}

func nextPath(ctx context.Context, pathC <-chan *File) (*File, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case p := <-pathC:
		return p, nil
	}
}

// RootPath joins a path with a root, evaluating and bounding any
// symlink to the root directory.
func RootPath(root, path string) (string, error) {
	if path == "" {
		return root, nil
	}
	var linksWalked int // to protect against cycles
	for {
		i := linksWalked
		newpath, err := walkLinks(root, path, &linksWalked)
		if err != nil {
			return "", err
		}
		path = newpath
		if i == linksWalked {
			newpath = filepath.Join("/", newpath)
			if path == newpath {
				return filepath.Join(root, newpath), nil
			}
			path = newpath
		}
	}
}

func walkLink(root, path string, linksWalked *int) (newpath string, islink bool, err error) {
	if *linksWalked > 255 {
		return "", false, errTooManyLinks
	}

	path = filepath.Join("/", path)
	if path == "/" {
		return path, false, nil
	}
	realPath := filepath.Join(root, path)

	fi, err := os.Lstat(realPath)
	if err != nil {
		// If path does not yet exist, treat as non-symlink
		if os.IsNotExist(err) {
			return path, false, nil
		}
		return "", false, err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return path, false, nil
	}
	newpath, err = os.Readlink(realPath)
	if err != nil {
		return "", false, err
	}
	*linksWalked++
	return newpath, true, nil
}

func walkLinks(root, path string, linksWalked *int) (string, error) {
	switch dir, file := filepath.Split(path); {
	case dir == "":
		newpath, _, err := walkLink(root, file, linksWalked)
		return newpath, err
	case file == "":
		if os.IsPathSeparator(dir[len(dir)-1]) {
			if dir == "/" {
				return dir, nil
			}
			return walkLinks(root, dir[:len(dir)-1], linksWalked)
		}
		newpath, _, err := walkLink(root, dir, linksWalked)
		return newpath, err
	default:
		newdir, err := walkLinks(root, dir, linksWalked)
		if err != nil {
			return "", err
		}
		newpath, islink, err := walkLink(root, filepath.Join(newdir, file), linksWalked)
		if err != nil {
			return "", err
		}
		if !islink {
			return newpath, nil
		}
		if filepath.IsAbs(newpath) {
			return newpath, nil
		}
		return filepath.Join(newdir, newpath), nil
	}
}
