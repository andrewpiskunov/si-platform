// Package media prepares untrusted media in an isolated, resource-limited process.
package media

// Variant describes a verified immutable output by its SHA-256 content key.
type Variant struct {
	Key  string `json:"key"`
	MIME string `json:"mime"`
	Size int64  `json:"size"`
}

type Prepared struct {
	Kind       string  `json:"kind"`
	DurationMS int64   `json:"duration_ms"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	Standard   Variant `json:"standard"`
	Economy    Variant `json:"economy"`
}

// Options selects trusted executables and a private temporary-file parent.
// Empty executable options are resolved from PATH; isolation is never optional.
type Options struct {
	FFmpeg    string
	FFprobe   string
	Sandbox   string
	Prlimit   string
	Directory string
}

// Output exists until its owning Result is closed.
type Output struct {
	Key  string
	Path string
	MIME string
	Size int64
}

type Result struct {
	Prepared  Prepared
	Files     []Output
	directory string
}
