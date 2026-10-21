// theme-example regenerates the downloadable theme and its standalone guide.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	themeexample "siplatform/examples/themes"
)

func main() {
	destination := flag.String("out", "web/static/themes", "directory for example.zip and guide.html")
	flag.Parse()
	if err := writeExample(*destination); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func writeExample(directory string) error {
	archive, err := themeexample.Build()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	for _, file := range []struct {
		name string
		data []byte
	}{{"example.zip", archive}, {"guide.html", themeexample.Guide()}} {
		destination := filepath.Join(directory, file.name)
		if err = os.WriteFile(destination, file.data, 0644); err != nil {
			return err
		}
		fmt.Printf("%s: %d bytes\n", destination, len(file.data))
	}
	return nil
}
