// siq validates and converts SIQuester packages without a running server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"siplatform/examples"
	"siplatform/internal/packs"
	"siplatform/internal/siq"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string, output io.Writer) error {
	if len(args) < 2 {
		return errors.New("usage: siq validate INPUT | convert INPUT OUTPUT | sample OUTPUT")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if args[0] == "sample" {
		if len(args) != 2 {
			return errors.New("sample requires one output path")
		}
		return writeNew(args[1], func(w io.Writer) error { return siq.Write(ctx, w, examples.Demo(), nil) })
	}
	if (args[0] != "validate" || len(args) != 2) && (args[0] != "convert" || len(args) != 3) {
		return errors.New("invalid command or argument count")
	}
	f, err := os.Open(args[1])
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	a, err := siq.Read(ctx, f, stat.Size(), siq.DefaultLimits())
	if err != nil {
		return err
	}
	if args[0] == "convert" {
		return writeNew(args[2], func(w io.Writer) error { return siq.Write(ctx, w, a.Document, a.Assets) })
	}
	questions := 0
	for _, r := range a.Document.Rounds {
		for _, t := range r.Themes {
			questions += len(t.Questions)
		}
	}
	report := struct {
		Name      string        `json:"name"`
		Playable  bool          `json:"playable"`
		Questions int           `json:"questions"`
		Assets    int           `json:"assets"`
		Issues    []packs.Issue `json:"issues"`
	}{a.Document.Name, packs.Playable(a.Issues), questions, len(a.Assets), a.Issues}
	enc := json.NewEncoder(output)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return err
	}
	if !report.Playable {
		return errors.New("package has blocking compatibility diagnostics")
	}
	return nil
}
func writeNew(path string, write func(io.Writer) error) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(path)
		}
	}()
	if err = write(f); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return f.Close()
}
