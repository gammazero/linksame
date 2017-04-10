/*
Replace identical files with links to one file.

Search recursively through the top level directory to find identical files.
For each set of identical files, keep only the file with the longest name and
replace all other copies with hardlinks or symlinks to the longest-named file.

The use of hardlinks or symlinks can be specified; the default is hardlinks.
Symlinks will be used when hardlinks fail.

Use of relative (the default) or absolute symlink can be specified.  Generally,
relative symlinks are preferred as this permits links to maintain their
validity regardless of the mount point used for the file system.

This is useful when there are multiple copies of files in different in
different locations of a directory tree, and all copies of each file should
remain identical.  Converting all the files into links to the same file ensures
that the files remain the same as well as saves the space used by multiple
copies.

The linksame utility is also useful when different names for a shared lib
should be links, but were perhaps turned into files.  Each copy has a different
name.  For example:

    libexample.so.1.0
    libexample.so.1
    libexample.so

will be changed so that there is only one instance of the file:

    libexample.so.1.0
    libexample.so.1 --> libexample.so.1.0
    libexample.so --> libexample.so.1.0
*/
package linksame

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"sort"
	"syscall"
)

// LinkSame links identical files in the specified directory tree.
func LinkSame(root, pattern string, link, symlink, absolute, safe, q, qq bool) error {
	rootDir := path.Clean(root)
	if !q {
		fmt.Println("Linking identical files in", rootDir)
	}

	// Walk directory and create map that maps a size to the list of all files
	// of that size.  Only keep lists of files that have more than one file.
	sizeFileMap := map[int64][]string{}
	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return nil
		}
		if pattern != "" {
			ok, err := filepath.Match(pattern, info.Name())
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}
		sizeFileMap[info.Size()] = append(sizeFileMap[info.Size()], path)
		return nil
	})
	if err != nil {
		return err
	}

	type stats struct {
		links int
		saved int64
	}

	// Calculate hash of files that have the same size.
	statsChan := make(chan stats, 1)
	var waitCount int
	for i := range sizeFileMap {
		if len(sizeFileMap[i]) < 2 {
			continue
		}
		waitCount++
		// Hash and link each list of same-sized files concurrently.
		go func(filePaths []string) {
			var links int
			var saved int64
			sameLists := checkSame(filePaths)
			for j := range sameLists {
				l, s := linkFiles(sameLists[j], rootDir, link, symlink,
					absolute, safe, q)
				links += l
				saved += s
			}
			statsChan <- stats{links, saved}
		}(sizeFileMap[i])
	}

	var linkCount int
	var sizeSaved int64
	for waitCount > 0 {
		s := <-statsChan
		waitCount--
		linkCount += s.links
		sizeSaved += s.saved
	}

	if !qq {
		fmt.Println()
		if !link {
			fmt.Println("If writing links (-w), would have...")
		}
		fmt.Println("Replaced", linkCount, "files with links")
		fmt.Println("Reduced storage by", sizeStr(sizeSaved))
	}
	return nil
}

// LinkSameUpdate takes a single file and links files in the specified
// directory tree that are identical to it.
func LinkSameUpdate(updateFile, root, pattern string, link, symlink, absolute, safe, q, qq bool) error {
	if updateFile == "" {
		return errors.New("Update file not specified")
	}
	rootDir := path.Clean(root)
	if !q {
		fmt.Println("Linking identical files in", rootDir)
	}

	updateInfo, err := os.Stat(updateFile)
	if err != nil {
		return err
	}
	if updateInfo.Size() == 0 {
		return errors.New("Update file is empty")
	}
	updateHash, err := hashFile(updateFile)
	if err != nil {
		return err
	}

	// Walk directory and create map that maps a size to the list of all files
	// of that size.  Only keep lists of files that have more than one file.
	same := []string{updateFile}
	err = filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() != updateInfo.Size() {
			return nil
		}
		if pattern != "" {
			ok, err := filepath.Match(pattern, info.Name())
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}
		h, err := hashFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return nil
		}
		if h != updateHash {
			return nil
		}
		same = append(same, path)
		return nil
	})
	if err != nil {
		return err
	}

	var linkCount int
	var sizeSaved int64
	if len(same) > 1 {
		linkCount, sizeSaved = linkFiles(same, rootDir, link, symlink,
			absolute, safe, q)
	}

	if !qq {
		fmt.Println()
		if !link {
			fmt.Println("If writing links (-w), would have...")
		}
		fmt.Println("Replaced", linkCount, "files with links")
		fmt.Println("Reduced storage by", sizeStr(sizeSaved))
	}
	return nil
}

// sizeStr returns a string representation of the rounded bytes
func sizeStr(size int64) string {
	const (
		_        = iota
		kilobyte = 1 << (10 * iota)
		megabyte
		gigabyte
	)

	round1 := func(num float64) float64 {
		return float64(int((num*10.0)+0.5)) / 10.0
	}

	if size > gigabyte {
		return fmt.Sprintf("%.1fG", round1(float64(size)/gigabyte))
	}
	if size > megabyte {
		return fmt.Sprintf("%.1fM", round1(float64(size)/megabyte))
	}
	if size > kilobyte {
		return fmt.Sprintf("%.1fK", round1(float64(size)/kilobyte))
	}
	return fmt.Sprint(size, " bytes")
}

// hashFile calculates a sha1 hash of the specified file.
func hashFile(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return string(h.Sum(nil)), nil
}

// checkSame returns sets of identical files.
func checkSame(filepaths []string) [][]string {
	hashFileMap := make(map[string][]string, len(filepaths))
	for _, fpath := range filepaths {
		h, err := hashFile(fpath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		hashFileMap[h] = append(hashFileMap[h], fpath)
	}
	var sames [][]string
	for _, files := range hashFileMap {
		if len(files) > 1 {
			sames = append(sames, files)
		}
	}
	return sames
}

// linkFiles links the files in the given list, which have been determined to
// be identical.
func linkFiles(files []string, rootDir string, link, symlink, absolute, safe, q bool) (int, int64) {
	if len(files) < 2 {
		return 0, 0
	}

	// Sort files and get file with longest name, or longest path if names
	// are the same.  This only matters for symlinks, but since a failed
	// hardlink can result in a symlink, do it anyway.
	sort.Sort(sort.Reverse(pathSlice(files)))

	var linkCount int
	var sizeSaved int64
	baseFile := files[0]
	baseInfo, err := os.Stat(baseFile)
	for err != nil {
		fmt.Fprintln(os.Stderr, err)
		files = files[1:]
		baseFile = files[0]
		baseInfo, err = os.Stat(baseFile)
	}
	baseRel, err := filepath.Rel(rootDir, baseFile)
	if err != nil {
		baseRel = baseFile
	}

	for _, f := range files[1:] {
		fInfo, err := os.Stat(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		// If the files are already the same (hardlinked), then skip.
		if os.SameFile(baseInfo, fInfo) {
			continue
		}

		// If safe mode enabled, check that files have same permissions and
		// ownership.
		if safe {
			// Check that permissions are the same.
			if fInfo.Mode() != baseInfo.Mode() {
				continue
			}
			// Check that ownership is the same.
			fSysStat := fInfo.Sys().(*syscall.Stat_t)
			baseSysStat := baseInfo.Sys().(*syscall.Stat_t)
			if fSysStat.Uid != baseSysStat.Uid || fSysStat.Gid != baseSysStat.Gid {
				continue
			}
		}

		if !link {
			sizeSaved += baseInfo.Size()
			linkCount++
			if !q {
				tgtRel, err := filepath.Rel(rootDir, f)
				if err != nil {
					tgtRel = f
				}
				fmt.Println("link:", tgtRel, "<-->", baseRel)
			}
			continue
		}

		if err = os.Remove(f); err != nil {
			fmt.Fprintln(os.Stderr, "cannot unlink file:", f)
			continue
		}

		tgtRel, err := filepath.Rel(rootDir, f)
		if err != nil {
			tgtRel = f
		}

		createSymlink := symlink
		if !symlink {
			if err = os.Link(baseFile, f); err != nil {
				createSymlink = true
				if !q {
					fmt.Fprintln(os.Stderr,
						"could not create hardlink, creating symlink")
				}
			} else if !q {
				fmt.Println("hardlink:", tgtRel, "<-->", baseRel)
				if err = os.Chmod(f, baseInfo.Mode()); err != nil {
					fmt.Fprintln(os.Stderr,
						"failed to set mode on hardlink:", err)
				}
			}
		}

		if createSymlink {
			var source string
			if absolute {
				source = baseFile
			} else {
				rp, err := filepath.Rel(path.Dir(baseFile), path.Dir(f))
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					source = baseFile
				} else if rp == "." {
					source = path.Base(baseFile)
				} else {
					source = path.Join(rp, path.Base(baseFile))
				}
			}

			if err = os.Symlink(source, f); err != nil {
				fmt.Fprintf(os.Stderr, "failed to create symlink for %s: %s",
					baseRel, err)
				// Restore file.
				if err = copyFile(baseFile, f, fInfo.Mode()); err != nil {
					fmt.Fprintln(os.Stderr, "failed to restore file:", err)
				}
				continue // skip stats update

				if !q {
					fmt.Println("symlink:", tgtRel, "--->", baseRel)
				}
			}
		}
		sizeSaved += baseInfo.Size()
		linkCount++
	}
	return linkCount, sizeSaved
}

func copyFile(dst, src string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := ioutil.TempFile(filepath.Dir(dst), "")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, in)
	if err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err = tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err = os.Chmod(tmp.Name(), perm); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err = os.Rename(tmp.Name(), dst); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

type pathSlice []string

func (s pathSlice) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s pathSlice) Len() int      { return len(s) }

func (s pathSlice) Less(i, j int) bool {
	// Sort by shortest-basename, shortest-path
	pBaseLen := len(path.Base(s[i]))
	qBaseLen := len(path.Base(s[j]))
	if pBaseLen < qBaseLen {
		return true
	}
	if qBaseLen < pBaseLen {
		return false
	}
	// Base names are the same, so look at path.
	return len(s[i]) < len(s[j])
}
