package executor

import (
	"context"
	"io"
	"log"
	"os"
	"time"
)

// FileOpOptions specifies bind mount paths for sandboxed file operations.
type FileOpOptions struct {
	RWBinds []BindMount
	ROBinds []BindMount
}

// FileInfo is a portable file metadata struct used by FileOperator.
type FileInfo struct {
	Name    string
	Size    int64
	Mode    os.FileMode
	ModTime time.Time
	IsDir   bool
}

// WalkFunc is the callback for FileOperator.Walk.
type WalkFunc func(path string, info FileInfo, err error) error

// FileOperator abstracts file-system operations so they can be executed
// either directly (os.*) or inside a bwrap sandbox.
type FileOperator interface {
	ReadFile(ctx context.Context, opts FileOpOptions, path string) ([]byte, error)
	WriteFile(ctx context.Context, opts FileOpOptions, path string, data []byte, perm os.FileMode) error
	AppendFile(ctx context.Context, opts FileOpOptions, path string, data []byte, perm os.FileMode) (int, error)
	Stat(ctx context.Context, opts FileOpOptions, path string) (*FileInfo, error)
	Lstat(ctx context.Context, opts FileOpOptions, path string) (*FileInfo, error)
	ReadDir(ctx context.Context, opts FileOpOptions, path string) ([]FileInfo, error)
	Walk(ctx context.Context, opts FileOpOptions, root string, walkFn WalkFunc) error
	CreateFile(ctx context.Context, opts FileOpOptions, path string, reader io.Reader) (int64, error)
	MkdirAll(ctx context.Context, opts FileOpOptions, path string, perm os.FileMode) error
	// ServeFile returns a local path suitable for gin's c.File() and a cleanup function.
	ServeFile(ctx context.Context, opts FileOpOptions, path string) (localPath string, cleanup func(), err error)
}

// NewFileOperator returns a BwrapFileOperator when the executor is bwrap-based,
// otherwise a DirectFileOperator.
func NewFileOperator(cmdExec CommandExecutor) FileOperator {
	if bwrap, ok := cmdExec.(*BwrapExecutor); ok {
		log.Println("File operator: bwrap")
		return &BwrapFileOperator{exec: bwrap}
	}
	log.Println("File operator: direct")
	return &DirectFileOperator{}
}
