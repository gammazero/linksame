/*
Replace identical files with links to one file.

Search recursively through one or more directory trees to find identical files.
For each set of identical files, keep only the file with the longest name and
replace all other copies with hardlinks or symlinks to the longest-named file.

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
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// LinkSame replaces copies of files with links to a single file.
//
// Search all regular files in the specified directory trees, with names
// matching pattern if specified.  Hardlinks are created by default; symlinks
// are requested by setting symlinks = true.  Symlinks are used if hardlinks
// fail.
//
// Relative (default) or absolute symlinks can be specified.  Generally,
// relative symlinks are preferred as this permits links to maintain their
// validity regardless of the mount point used for the file system.
//
// If safe mode is enabled, then links are only created for files that have
// same permission and ownership.
//
// Set quiet to suppress output about links created and size saved.  Set
// verbose to print output about individual link creation.
func LinkSame(roots []string, pattern string, writeLinks, symlink, absolute, safe, quiet, verbose bool) error {
	roots, err := normalizeRoots(roots, quiet)
	if err != nil {
		return err
	}
	if !quiet {
		fmt.Println("Linking identical files in", strings.Join(roots, ", "))
	}

	// Walk directories and create map that maps a size to the list of all
	// files of that size.  Only keep lists of files with more than one file.
	sizeFileMap := map[int64][]string{}
	for _, rootDir := range roots {
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
			hashMap := createHashMap(filePaths)
			for _, files := range hashMap {
				if len(files) < 2 {
					continue
				}
				l, s := linkFiles(files, writeLinks, symlink, absolute, safe,
					verbose)
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

	if !quiet {
		fmt.Println()
		if !writeLinks {
			fmt.Println("If writing links (-w), would have...")
		}
		fmt.Println("Replaced", linkCount, "files with links")
		fmt.Println("Reduced storage by", sizeStr(sizeSaved))
	}
	return nil
}

// LinkSameUpdate replaces copies of a file with links to a single file.
//
// Other then the updateFile parameter, all other parameter are that same as
// for LinkSame()
func LinkSameUpdate(updateFile string, roots []string, pattern string, writeLinks, symlink, absolute, safe, quiet, verbose bool) error {
	if updateFile == "" {
		return errors.New("Update file not specified")
	}
	roots, err := normalizeRoots(roots, quiet)
	if err != nil {
		return err
	}
	updateInfo, err := os.Stat(updateFile)
	if err != nil {
		return err
	}
	if !updateInfo.Mode().IsRegular() {
		return fmt.Errorf("%s is not a file", updateFile)
	}
	if updateInfo.Size() == 0 {
		return fmt.Errorf("%s is empty", updateFile)
	}
	updateHash, err := hashFile(updateFile)
	if err != nil {
		return err
	}
	if !quiet {
		fmt.Println("Linking", updateFile, "to identical files in",
			strings.Join(roots, ", "))
	}

	// Walk directories and find files that are identical to the update file.
	same := []string{updateFile}
	for _, rootDir := range roots {
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
	}
	var linkCount int
	var sizeSaved int64
	if len(same) > 1 {
		linkCount, sizeSaved = linkFiles(same, writeLinks, symlink, absolute, safe,
			verbose)
	}

	if !quiet {
		fmt.Println()
		if !writeLinks {
			fmt.Println("If writing links (-w), would have...")
		}
		fmt.Println("Replaced", linkCount, "files with links")
		fmt.Println("Reduced storage by", sizeStr(sizeSaved))
	}
	return nil
}

func normalizeRoots(roots []string, quiet bool) ([]string, error) {
	for i := range roots {
		rootDir := path.Clean(roots[i])
		rootInfo, err := os.Stat(rootDir)
		if err != nil {
			return nil, err
		}
		if !rootInfo.IsDir() {
			return nil, fmt.Errorf("%s is not a directory", rootDir)
		}
		roots[i] = rootDir
	}

	if len(roots) == 0 {
		return []string{"."}, nil
	}

	if len(roots) > 1 {
		// Remove any root that is the same or a subdirectory of another.
	outerLoop:
		for i := 0; i < len(roots); {
			for j := range roots {
				if j == i {
					continue
				}
				if strings.HasPrefix(roots[i], roots[j]) {
					if !quiet {
						fmt.Fprintln(os.Stderr, roots[i],
							"already included in", roots[j])
					}
					// This root is a subdirectory of another, so skip it.
					roots[i] = roots[len(roots)-1]
					roots = roots[:len(roots)-1]
					continue outerLoop
				}
			}
			i++
		}
	}
	return roots, nil
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

// createHashMap returns a map of sha1 hash to a slice of identical files.
func createHashMap(fpaths []string) map[string][]string {
	var sameAs []string
	hashMap := make(map[string][]string, len(fpaths))
	for i := range fpaths {
		if fpaths[i] == "" {
			continue
		}
		f1Info, err := os.Stat(fpaths[i])
		if err != nil {
			// Cannot stat file, so skip.
			continue
		}

		// Calculate sha1 hash of file.
		h, err := hashFile(fpaths[i])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}

		// Find hardlinks to current file, and reuse hash for these.
		for j := i + 1; j < len(fpaths); j++ {
			if fpaths[j] == "" {
				continue
			}
			f2Info, err := os.Stat(fpaths[j])
			if err != nil {
				// Cannot stat file, so mark as bad.
				fpaths[j] = ""
				continue
			}
			if os.SameFile(f1Info, f2Info) {
				sameAs = append(sameAs, fpaths[j])
				fpaths[j] = ""
			}
		}

		if len(sameAs) == 0 {
			hashMap[h] = append(hashMap[h], fpaths[i])
		} else {
			// Reuse hash for additional hardlinked files.
			hashMap[h] = append(hashMap[h], append(sameAs, fpaths[i])...)
			sameAs = nil
		}
	}
	return hashMap
}

// linkFiles links the files in the given list, which have been determined to
// be identical.
func linkFiles(files []string, writeLinks, symlink, absolute, safe, verbose bool) (int, int64) {
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
		// Skip files until one does not give error.
		fmt.Fprintln(os.Stderr, err)
		files = files[1:]
		baseFile = files[0]
		baseInfo, err = os.Stat(baseFile)
	}

	for _, f := range files[1:] {
		fInfo, err := os.Stat(f)
		if err != nil {
			if verbose {
				fmt.Fprintln(os.Stderr, err)
			}
			// Cannot stat file, maybe removed, so skip.
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

		if !writeLinks {
			sizeSaved += baseInfo.Size()
			linkCount++
			if !verbose {
				continue
			}
			if symlink {
				var source string
				if absolute {
					source = baseFile
				} else {
					rp, err := filepath.Rel(path.Dir(f), path.Dir(baseFile))
					if err != nil {
						// Cannot make relative symlink.
						source = baseFile
					} else if rp == "." {
						source = path.Base(baseFile)
					} else {
						source = path.Join(rp, path.Base(baseFile))
					}
				}
				fmt.Println("symlink:", f, "--->", source)
			} else {
				fmt.Println("link:", f, "<-->", baseFile)
			}
			continue
		}

		if err = os.Remove(f); err != nil {
			if verbose {
				fmt.Fprintln(os.Stderr, "cannot remove file:", f)
			}
			continue
		}

		createSymlink := symlink
		if !symlink {
			if err = os.Link(baseFile, f); err != nil {
				createSymlink = true
				if verbose {
					fmt.Fprintln(os.Stderr,
						"could not create hardlink, creating symlink")
				}
			} else if verbose {
				fmt.Println("hardlink:", f, "<-->", baseFile)
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
				rp, err := filepath.Rel(path.Dir(f), path.Dir(baseFile))
				if err != nil {
					if verbose {
						fmt.Fprintln(os.Stderr, err)
					}
					// Cannot make relative symlink.
					source = baseFile
				} else if rp == "." {
					source = path.Base(baseFile)
				} else {
					source = path.Join(rp, path.Base(baseFile))
				}
			}

			if err = os.Symlink(source, f); err != nil {
				fmt.Fprintf(os.Stderr, "failed to create symlink for %s: %s",
					baseFile, err)
				// Restore file.
				if err = copyFile(baseFile, f, fInfo.Mode()); err != nil {
					fmt.Fprintln(os.Stderr, "failed to restore file:", err)
				}
				continue // skip stats update
			}
			if verbose {
				fmt.Println("symlink:", f, "--->", source)
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
	tmp, err := os.CreateTemp(filepath.Dir(dst), "")
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
