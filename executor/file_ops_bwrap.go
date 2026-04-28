package executor

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// BwrapFileOperator executes file operations inside a bwrap sandbox,
// preventing symlink escapes by only mounting allowed paths.
type BwrapFileOperator struct {
	exec *BwrapExecutor
}

// run executes a command inside bwrap and returns stdout/stderr.
func (b *BwrapFileOperator) run(ctx context.Context, opts FileOpOptions, stdin io.Reader, name string, args ...string) ([]byte, []byte, error) {
	execOpts := ExecOptions{
		Ctx:        ctx,
		WorkingDir: "/",
		RWBinds:    opts.RWBinds,
		ROBinds:    opts.ROBinds,
	}
	cmd := b.exec.Prepare(execOpts, name, args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil && stderr.Len() > 0 {
		log.Printf("[bwrap-stderr] fileop=%s err=%v stderr=%s", name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

// mapError converts bwrap command errors to standard os errors where possible.
func mapError(stderr []byte, err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(string(stderr))
	if strings.Contains(msg, "no such file or directory") {
		return os.ErrNotExist
	}
	if strings.Contains(msg, "permission denied") {
		return os.ErrPermission
	}
	if strings.Contains(msg, "is a directory") {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(stderr)), os.ErrInvalid)
	}
	if len(stderr) > 0 {
		return fmt.Errorf("%s", strings.TrimSpace(string(stderr)))
	}
	return err
}

func (b *BwrapFileOperator) ReadFile(ctx context.Context, opts FileOpOptions, path string) ([]byte, error) {
	stdout, stderr, err := b.run(ctx, opts, nil, "base64", path)
	if err != nil {
		return nil, mapError(stderr, err)
	}
	// base64 output may contain newlines; decode it
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(stdout)))
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	return decoded, nil
}

func (b *BwrapFileOperator) WriteFile(ctx context.Context, opts FileOpOptions, path string, data []byte, perm os.FileMode) error {
	encoded := base64.StdEncoding.EncodeToString(data)
	stdin := strings.NewReader(encoded)
	script := fmt.Sprintf("base64 -d > %s && chmod %o %s", shellQuote(path), perm, shellQuote(path))
	_, stderr, err := b.run(ctx, opts, stdin, "bash", "-c", script)
	return mapError(stderr, err)
}

func (b *BwrapFileOperator) AppendFile(ctx context.Context, opts FileOpOptions, path string, data []byte, perm os.FileMode) (int, error) {
	encoded := base64.StdEncoding.EncodeToString(data)
	stdin := strings.NewReader(encoded)
	// Create file if not exists, then append
	script := fmt.Sprintf("touch %s && chmod %o %s && base64 -d >> %s", shellQuote(path), perm, shellQuote(path), shellQuote(path))
	_, stderr, err := b.run(ctx, opts, stdin, "bash", "-c", script)
	if err != nil {
		return 0, mapError(stderr, err)
	}
	return len(data), nil
}

func (b *BwrapFileOperator) Stat(ctx context.Context, opts FileOpOptions, path string) (*FileInfo, error) {
	return b.statImpl(ctx, opts, path, true)
}

func (b *BwrapFileOperator) Lstat(ctx context.Context, opts FileOpOptions, path string) (*FileInfo, error) {
	return b.statImpl(ctx, opts, path, false)
}

// statImpl runs stat inside bwrap. followSymlinks=true uses -L flag.
func (b *BwrapFileOperator) statImpl(ctx context.Context, opts FileOpOptions, path string, followSymlinks bool) (*FileInfo, error) {
	// Format: name|size|mode_hex|mtime_epoch|file_type
	args := []string{"-c", "%n|%s|%f|%Y|%F"}
	if followSymlinks {
		args = append([]string{"-L"}, args...)
	}
	args = append(args, path)
	stdout, stderr, err := b.run(ctx, opts, nil, "stat", args...)
	if err != nil {
		return nil, mapError(stderr, err)
	}
	return parseStat(strings.TrimSpace(string(stdout)))
}

func parseStat(line string) (*FileInfo, error) {
	parts := strings.SplitN(line, "|", 5)
	if len(parts) < 5 {
		return nil, fmt.Errorf("unexpected stat output: %s", line)
	}
	size, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse size %q: %w", parts[1], err)
	}
	modeHex, err := strconv.ParseUint(parts[2], 16, 32)
	if err != nil {
		return nil, fmt.Errorf("parse mode %q: %w", parts[2], err)
	}
	mtimeEpoch, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse mtime %q: %w", parts[3], err)
	}
	fileType := strings.TrimSpace(parts[4])
	isDir := strings.Contains(fileType, "directory")
	isSymlink := strings.Contains(fileType, "symbolic link")

	mode := os.FileMode(modeHex & 0o7777)
	if isDir {
		mode |= os.ModeDir
	}
	if isSymlink {
		mode |= os.ModeSymlink
	}

	return &FileInfo{
		Name:    filepath.Base(parts[0]),
		Size:    size,
		Mode:    mode,
		ModTime: time.Unix(mtimeEpoch, 0),
		IsDir:   isDir,
	}, nil
}

func (b *BwrapFileOperator) ReadDir(ctx context.Context, opts FileOpOptions, path string) ([]FileInfo, error) {
	// find -maxdepth 1 -mindepth 1 to list immediate children
	stdout, stderr, err := b.run(ctx, opts, nil, "find", path, "-maxdepth", "1", "-mindepth", "1", "-printf", "%p|%s|%m|%T@|%y\n")
	if err != nil {
		return nil, mapError(stderr, err)
	}
	return parseFindOutput(string(stdout))
}

func (b *BwrapFileOperator) Walk(ctx context.Context, opts FileOpOptions, root string, walkFn WalkFunc) error {
	stdout, stderr, err := b.run(ctx, opts, nil, "find", root, "-printf", "%p|%s|%m|%T@|%y\n")
	if err != nil {
		return mapError(stderr, err)
	}
	entries, err := parseWalkOutput(string(stdout))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := walkFn(entry.Path, entry.Info, nil); err != nil {
			return err
		}
	}
	return nil
}

type walkEntry struct {
	Path string
	Info FileInfo
}

// parseFindOutput parses lines of "path|size|mode_octal|mtime_float|type_char".
func parseFindOutput(output string) ([]FileInfo, error) {
	var results []FileInfo
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		_, info, ok := parseFindLine(line)
		if !ok {
			continue
		}
		results = append(results, info)
	}
	return results, nil
}

func parseWalkOutput(output string) ([]walkEntry, error) {
	var results []walkEntry
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		path, info, ok := parseFindLine(line)
		if !ok {
			continue
		}
		results = append(results, walkEntry{Path: path, Info: info})
	}
	return results, nil
}

func parseFindLine(line string) (string, FileInfo, bool) {
	parts := strings.SplitN(line, "|", 5)
	if len(parts) < 5 {
		return "", FileInfo{}, false
	}
	size, _ := strconv.ParseInt(parts[1], 10, 64)
	modeOctal, _ := strconv.ParseUint(parts[2], 8, 32)
	mtimeFloat, _ := strconv.ParseFloat(parts[3], 64)
	typeChar := strings.TrimSpace(parts[4])

	isDir := typeChar == "d"
	mode := os.FileMode(modeOctal & 0o7777)
	if isDir {
		mode |= os.ModeDir
	}
	if typeChar == "l" {
		mode |= os.ModeSymlink
	}

	path := parts[0]
	return path, FileInfo{
		Name:    filepath.Base(path),
		Size:    size,
		Mode:    mode,
		ModTime: time.Unix(int64(mtimeFloat), int64((mtimeFloat-float64(int64(mtimeFloat)))*1e9)),
		IsDir:   isDir,
	}, true
}

func (b *BwrapFileOperator) CreateFile(ctx context.Context, opts FileOpOptions, path string, reader io.Reader) (int64, error) {
	// Read all data, base64 encode, pipe through bwrap
	data, err := io.ReadAll(reader)
	if err != nil {
		return 0, err
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	stdin := strings.NewReader(encoded)
	script := fmt.Sprintf("base64 -d > %s", shellQuote(path))
	_, stderr, errRun := b.run(ctx, opts, stdin, "bash", "-c", script)
	if errRun != nil {
		return 0, mapError(stderr, errRun)
	}
	return int64(len(data)), nil
}

func (b *BwrapFileOperator) MkdirAll(ctx context.Context, opts FileOpOptions, path string, _ os.FileMode) error {
	_, stderr, err := b.run(ctx, opts, nil, "mkdir", "-p", path)
	return mapError(stderr, err)
}

func (b *BwrapFileOperator) ServeFile(ctx context.Context, opts FileOpOptions, path string) (string, func(), error) {
	tmp, err := os.CreateTemp("", "bwrap-serve-*")
	if err != nil {
		return "", nil, err
	}

	execOpts := ExecOptions{
		Ctx:        ctx,
		WorkingDir: "/",
		RWBinds:    opts.RWBinds,
		ROBinds:    opts.ROBinds,
	}
	cmd := b.exec.Prepare(execOpts, "cat", path)
	var stderr bytes.Buffer
	cmd.Stdout = tmp
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	closeErr := tmp.Close()
	if runErr == nil && closeErr != nil {
		runErr = closeErr
	}
	if runErr != nil {
		if stderr.Len() > 0 {
			log.Printf("[bwrap-stderr] fileop=serve-file err=%v stderr=%s", runErr, strings.TrimSpace(stderr.String()))
		}
		os.Remove(tmp.Name())
		return "", nil, mapError(stderr.Bytes(), runErr)
	}

	cleanup := func() { os.Remove(tmp.Name()) }
	return tmp.Name(), cleanup, nil
}

// shellQuote wraps a string in single quotes, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
