# linksame
Replace identical files with links to one file

[![GoDoc](https://godoc.org/github.com/gammazero/linksame?status.png)](https://godoc.org/github.com/gammazero/linksame)

## Command-line utility

The `lnsame` command is a command-line utility that finds identical files, in one or more directory trees, and replaces the identical files with hardlinks or symlinks to a single file.

## Library

The `"github.com/gammazero/linksame"` library lets you build into you software the functionality to find and link identical files.  The `lnsame` utility is a thin wrapper for this library.
