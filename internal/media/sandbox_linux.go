//go:build linux

package media

import (
	"context"
	"debug/elf"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// New verifies that the configured tools actually run under isolation. A missing
// namespace, runtime library or executable disables processing; there is no
// unsandboxed fallback. Executable configuration is trusted operator input.
func New(options Options) (*Processor, error) {
	values := []*string{&options.FFmpeg, &options.FFprobe, &options.Sandbox, &options.Prlimit}
	defaults := []string{"ffmpeg", "ffprobe", "bwrap", "prlimit"}
	for i, value := range values {
		if *value == "" {
			*value = defaults[i]
		}
		path, err := exec.LookPath(*value)
		if err != nil {
			return nil, mediaError("unavailable")
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return nil, mediaError("unavailable")
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return nil, mediaError("unavailable")
		}
		stat, err := os.Stat(path)
		if err != nil || !stat.Mode().IsRegular() || stat.Mode()&0111 == 0 {
			return nil, mediaError("unavailable")
		}
		*value = path
	}
	if options.Directory == "" {
		options.Directory = os.TempDir()
	}
	absolute, err := filepath.Abs(options.Directory)
	if err != nil {
		return nil, mediaError("unavailable")
	}
	options.Directory = absolute
	envTool, err := exec.LookPath("env")
	if err != nil {
		return nil, mediaError("unavailable")
	}
	envTool, err = filepath.Abs(envTool)
	if err != nil {
		return nil, mediaError("unavailable")
	}
	runtime, err := runtimeFiles(options.FFmpeg, options.FFprobe, options.Prlimit, envTool)
	if err != nil {
		return nil, mediaError("unavailable")
	}
	p := &Processor{options: options, envTool: envTool, slots: make(chan struct{}, 2), runtime: runtime, timeout: maxProcessing}
	dir, err := os.MkdirTemp(options.Directory, "media-check-")
	if err != nil {
		return nil, mediaError("unavailable")
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, tool := range []string{options.FFmpeg, options.FFprobe} {
		b, err := p.run(ctx, tool, dir, dir, []string{"-version"})
		if err != nil || !strings.Contains(string(b), "version") {
			return nil, mediaError("unavailable")
		}
	}
	return p, nil
}

// runtimeFiles inspects trusted ELF headers instead of running ldd or mounting a
// host directory. Only the dynamic loader and exact linked libraries are exposed.
func runtimeFiles(paths ...string) ([]runtimeFile, error) {
	found := map[string]runtimeFile{}
	visited := map[string]bool{}
	var inspect func(string) error
	inspect = func(path string) error {
		if visited[path] {
			return nil
		}
		visited[path] = true
		f, err := elf.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		for _, prog := range f.Progs {
			if prog.Type == elf.PT_INTERP {
				b, err := io.ReadAll(io.LimitReader(prog.Open(), 4096))
				if err != nil {
					return err
				}
				loader := strings.TrimRight(string(b), "\x00")
				if !filepath.IsAbs(loader) {
					return errors.New("invalid loader")
				}
				found[loader] = runtimeFile{loader, loader}
				if err = inspect(loader); err != nil {
					return err
				}
			}
		}
		libs, err := f.ImportedLibraries()
		if err != nil {
			return err
		}
		dirs := []string{"/lib/x86_64-linux-gnu", "/usr/lib/x86_64-linux-gnu", "/lib/aarch64-linux-gnu", "/usr/lib/aarch64-linux-gnu", "/lib64", "/usr/lib64", "/lib", "/usr/lib"}
		for _, lib := range libs {
			if filepath.Base(lib) != lib {
				return errors.New("invalid library")
			}
			resolved := ""
			for _, dir := range dirs {
				path := filepath.Join(dir, lib)
				if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
					resolved = path
					break
				}
			}
			if resolved == "" {
				return errors.New("missing library")
			}
			found[resolved] = runtimeFile{resolved, resolved}
			if err = inspect(resolved); err != nil {
				return err
			}
		}
		return nil
	}
	for _, path := range paths {
		if err := inspect(path); err != nil {
			return nil, err
		}
	}
	result := make([]runtimeFile, 0, len(found))
	for _, file := range found {
		result = append(result, file)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].target < result[j].target })
	return result, nil
}

func (p *Processor) run(ctx context.Context, tool, input, output string, args []string) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, mediaError(Code(ctx.Err()))
	}
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	argv := []string{"--unshare-all", "--die-with-parent", "--cap-drop", "ALL", "--clearenv", "--proc", "/proc", "--dev", "/dev"}
	for _, file := range p.runtime {
		argv = append(argv, "--ro-bind", file.source, file.target)
	}
	toolPath := "/tools/" + filepath.Base(tool)
	argv = append(argv, "--ro-bind", tool, toolPath, "--ro-bind", p.envTool, "/env", "--ro-bind", p.options.Prlimit, "/prlimit", "--ro-bind", input, "/input", "--bind", output, "/output", "--chdir", "/output", "--remount-ro", "/", "--", "/env", "-i", "MALLOC_ARENA_MAX=2", "/prlimit", "--as=536870912:536870912", "--cpu=180:180", "--fsize=104857600:104857600", "--core=0:0", "--nofile=64:64", "--", toolPath)
	argv = append(argv, args...)
	cmd := exec.CommandContext(commandCtx, p.options.Sandbox, argv...)
	cmd.Env = []string{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	stdout := &boundedBuffer{limit: 256 << 10, cancel: cancel}
	stderr := &boundedBuffer{limit: 64 << 10, cancel: cancel}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()

	if stdout.exceeded || stderr.exceeded {
		return nil, mediaError("too_large")
	}
	if ctx.Err() != nil {
		return nil, mediaError(Code(ctx.Err()))
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, mediaError("unavailable")
		}
		return nil, mediaError("invalid")
	}
	return stdout.Bytes(), nil
}
