package main

import (
	"fmt"
	"log"
	"os"

	"github.com/canavan-a/fleetman/internal/api"
)

// cmdUpload uploads one or more local files to the server's file store from
// the plain CLI (no TUI), e.g. for scripting: fleetman upload ./build.tar.gz
func cmdUpload(cfg *Config, args []string) {
	if len(args) == 0 {
		log.Fatal("FATAL: usage: fleetman upload <local-path> [<local-path> ...]")
	}

	client := api.New(cfg.BaseURL(), cfg.MasterKey, cfg.ExtraHeaders)
	tty := IsTTY()

	failed := false
	for _, path := range args {
		onProgress := func(sent, total int64) {}
		if tty {
			onProgress = func(sent, total int64) {
				pct := int64(0)
				if total > 0 {
					pct = sent * 100 / total
				}
				fmt.Fprintf(os.Stderr, "\r%s: %d/%d bytes (%d%%)", path, sent, total, pct)
			}
		}

		info, err := client.UploadFile(path, onProgress)
		if tty {
			fmt.Fprint(os.Stderr, "\r\033[K")
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "upload %s: %v\n", path, err)
			failed = true
			continue
		}
		fmt.Printf("uploaded %s (%d bytes, sha256 %s)\n", info.Name, info.Size, info.SHA256)
	}

	if failed {
		os.Exit(1)
	}
}
