package main

import (
	"flag"
	"fmt"
	"github.com/crappycrypto/go_fs_playground/v2/ext4"
	"log"
	"os"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

func check(e error) {
	if e != nil {
		panic(e)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s MOUNTPOINT ext4.img\n", os.Args[0])
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() != 2 {
		usage()
		os.Exit(2)
	}
	mountpoint := flag.Arg(0)
	fsFilename := flag.Arg(1)

	fsFile, err := os.Open(fsFilename)
	check(err)

	ext4fs := go_fs_playground.ReadSuperBlock(fsFile)

	c, err := fuse.Mount(
		mountpoint,
		fuse.FSName("ext4fuse"),
		fuse.Subtype("ext4"),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	err = fs.Serve(c, &go_fs_playground.Ext4Fuse{Ext4fs: ext4fs})
	if err != nil {
		log.Fatal(err)
	}
}
