package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"siplatform/internal/catalogueimport"
	"syscall"
)

func main() {
	manifest := flag.String("manifest", "", "local JSON manifest")
	check := flag.Bool("check", false, "validate all local files without network")
	flag.Parse()
	if *manifest == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: catalogue-import -manifest manifest.json [-check]")
		os.Exit(2)
	}
	if *check {
		n, e := catalogueimport.Check(*manifest)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		json.NewEncoder(os.Stdout).Encode(map[string]int{"validated": n})
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	report, e := catalogueimport.Run(ctx, *manifest, os.Getenv("SI_API_URL"), os.Getenv("SI_API_TOKEN"))
	if report != nil {
		json.NewEncoder(os.Stdout).Encode(report)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
