// assistant-backup provides offline backup and restore for portable deployments.
package main

import (
	"flag"
	"fmt"
	"github.com/Tencent/WeKnora/internal/portable"
	"os"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Usage: assistant-backup [flags] backup|restore\n\nStop the server first. Backups include database, encryption keys and files.\nRestore requires a destination directory that does not exist. Archives contain private data.")
		flag.PrintDefaults()
	}
	data := flag.String("data-dir", "", "Application data directory (new directory for restore)")
	archive := flag.String("archive", "", "Backup .tar.gz path")
	flag.Parse()
	if *data == "" || *archive == "" || flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	var err error
	switch flag.Arg(0) {
	case "backup":
		err = portable.Backup(*data, *archive)
	case "restore":
		err = portable.Restore(*archive, *data)
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(flag.Arg(0) + " completed")
}
