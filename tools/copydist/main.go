// Command copydist recursively copies a directory tree, overwriting dst if
// it already exists. Used by the Makefile in place of `rm -rf dst && cp -r
// src dst`, since that fails when make is invoked from plain Windows
// cmd.exe/PowerShell instead of a POSIX shell (make shells out via
// CreateProcess there, which has no rm/cp) — go run works identically
// regardless of which shell invoked make.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: copydist <src> <dst>")
		os.Exit(2)
	}
	src, dst := os.Args[1], os.Args[2]

	if err := os.RemoveAll(dst); err != nil {
		fmt.Fprintf(os.Stderr, "copydist: remove %s: %v\n", dst, err)
		os.Exit(1)
	}
	if err := copyDir(src, dst); err != nil {
		fmt.Fprintf(os.Stderr, "copydist: copy %s -> %s: %v\n", src, dst, err)
		os.Exit(1)
	}
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
