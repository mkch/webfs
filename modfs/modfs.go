package modfs

import (
	"io/fs"
	"time"
)

// FS is a fs.FS but ModTime of all files in it will be a fix value.
type FS struct {
	fs.FS
	LastModified time.Time // ModTime of all files will be this value.
}

func (fs *FS) Open(name string) (fs.File, error) {
	f, err := fs.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return &file{f, fs.LastModified}, nil
}

type file struct {
	fs.File
	lastModified time.Time
}

func (f *file) Stat() (fs.FileInfo, error) {
	info, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return &fileInfo{info, f.lastModified}, nil
}

type fileInfo struct {
	fs.FileInfo
	LastModified time.Time
}

func (info *fileInfo) ModTime() time.Time {
	return info.LastModified
}
