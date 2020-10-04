package go_fs_playground

import (
	"flag"
	"fmt"
	"os"
)

func usage() {
	_, _ = fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	_, _ = fmt.Fprintf(os.Stderr, "  %s EXT4.IMG filename\n", os.Args[0])
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() != 2 {
		usage()
		os.Exit(2)
	}

	filename := flag.Arg(0)
	filename2 := flag.Arg(1)
	f, err := os.Open(filename)
	check(err)

	readFile(f, filename2, os.Stdout)

	err = f.Close()
	check(err)

}
