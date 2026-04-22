package executor

import (
	"context"
	"io"
	"os"
	"path/filepath"
)

// DirectFileOperator delegates all file operations to the standard os/filepath packages.
// This preserves the current (non-bwrap) behavior.
type DirectFileOperator struct{}

func (d *DirectFileOperator) ReadFile(_ context.Context, _ FileOpOptions, path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (d *DirectFileOperator) WriteFile(_ context.Context, _ FileOpOptions, path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

func (d *DirectFileOperator) AppendFile(_ context.Context, _ FileOpOptions, path string, data []byte, perm os.FileMode) (int, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, perm)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Write(data)
}

func (d *DirectFileOperator) Stat(_ context.Context, _ FileOpOptions, path string) (*FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return osToFileInfo(info), nil
}

func (d *DirectFileOperator) Lstat(_ context.Context, _ FileOpOptions, path string) (*FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	return osToFileInfo(info), nil
}

func (d *DirectFileOperator) ReadDir(_ context.Context, _ FileOpOptions, path string) ([]FileInfo, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var result []FileInfo
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		result = append(result, *osToFileInfo(info))
	}
	return result, nil
}

func (d *DirectFileOperator) Walk(_ context.Context, _ FileOpOptions, root string, walkFn WalkFunc) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return walkFn(path, FileInfo{}, err)
		}
		return walkFn(path, *osToFileInfo(info), nil)
	})
}

func (d *DirectFileOperator) CreateFile(_ context.Context, _ FileOpOptions, path string, reader io.Reader) (int64, error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, reader)
}

func (d *DirectFileOperator) MkdirAll(_ context.Context, _ FileOpOptions, path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (d *DirectFileOperator) ServeFile(_ context.Context, _ FileOpOptions, path string) (string, func(), error) {
	return path, func() {}, nil
}

// osToFileInfo converts os.FileInfo to our portable FileInfo.
func osToFileInfo(info os.FileInfo) *FileInfo {
	return &FileInfo{
		Name:    info.Name(),
		Size:    info.Size(),
		Mode:    info.Mode(),
		ModTime: info.ModTime(),
		IsDir:   info.IsDir(),
	}
}
