package main

import (
	"flag"
	"fmt"
	"os"
	"path"

	"github.com/gammazero/linksame"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, " ", path.Base(os.Args[0]), "[options] [root]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Options:")
		flag.PrintDefaults()
	}

	var symlink = flag.Bool("symlink", false, "Link files using only symlinks")
	var absolute = flag.Bool("absolute", false,
		"Use absolute instead of relative symlinks")
	var update = flag.String("update", "",
		"Only link files identical to specified update file")
	var pattern = flag.String("pattern", "",
		"Only link files matching pattern")
	var link = flag.Bool("w", false, "Write links to file system")
	var safe = flag.Bool("safe", false,
		"Do not link files with different permissions or ownership")
	var quiet = flag.Bool("q", false,
		"Do not print individual link creation messages")
	var veryQuiet = flag.Bool("qq", false, "Do not print results, implies -q")
	var help = flag.Bool("help", false, "Show help")
	flag.Parse()

	if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "too many arguments given")
		flag.Usage()
		os.Exit(1)
	}

	root := flag.Arg(0)
	if root == "" {
		root = "."
	}

	if *help {
		fmt.Fprintln(os.Stderr, path.Base(os.Args[0]),
			"- Replace identical files with links to one real file.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Description:")
		fmt.Fprintln(os.Stderr, " ",
			"Search recursively through the top level directory to find identical files.")
		fmt.Fprintln(os.Stderr, " ",
			"For each set of identical files, keep only the file with the longest name and")
		fmt.Fprintln(os.Stderr, " ",
			"replace all other copies with hardlinks or symlinks to that file.")

		fmt.Fprintln(os.Stderr)
		flag.Usage()
		os.Exit(0)
	}

	if *veryQuiet {
		flag.Set("q", "true")
	}

	var err error
	if *update != "" {
		err = linksame.LinkSameUpdate(*update, root, *pattern, *link,
			*symlink, *absolute, *safe, *quiet, *veryQuiet)
	} else {
		err = linksame.LinkSame(root, *pattern, *link, *symlink, *absolute,
			*safe, *quiet, *veryQuiet)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
