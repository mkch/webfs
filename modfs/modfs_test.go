package modfs_test

import (
	"io"
	"io/fs"
	"testing"
	"time"

	"github.com/mkch/webfs/modfs"
)

type fakeFileInfo fakeFile

func (info fakeFileInfo) Name() string {
	return info.name
}

func (info fakeFileInfo) Size() int64 {
	return 0
}

func (info fakeFileInfo) Mode() fs.FileMode {
	return 0777
}

func (info fakeFileInfo) ModTime() time.Time {
	return info.modTime
}

func (info fakeFileInfo) IsDir() bool {
	return false
}

func (info fakeFileInfo) Sys() any {
	return nil
}

type fakeFile struct {
	name    string
	modTime time.Time
}

func (f fakeFile) Stat() (fs.FileInfo, error) {
	return fakeFileInfo(f), nil
}

func (f fakeFile) Read(b []byte) (int, error) {
	return 0, io.EOF
}

func (f fakeFile) Close() error {
	return nil
}

type fakeFS struct {
	file fakeFile
}

func (f fakeFS) Open(name string) (fs.File, error) {
	if name != string(f.file.name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return f.file, nil
}

func Test_FS(t *testing.T) {
	var fs = fakeFS{file: fakeFile{
		name:    "file1",
		modTime: time.Unix(1, 2),
	}}
	var newTime = time.Unix(3, 4)
	var modfs = modfs.FS{FS: fs, LastModified: newTime}
	if f, err := modfs.Open("file1"); err != nil {
		t.Fatal(err)
	} else if fi, err := f.Stat(); err != nil {
		t.Fatal(err)
	} else if modTime := fi.ModTime(); modTime != newTime {
		t.Fatalf("%v expected, but got %v", newTime, modTime)
	}
}
