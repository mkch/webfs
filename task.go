package main

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/mkch/webfs/token"
)

type fileTask struct {
	id string

	ctxDone      func() <-chan struct{}
	ctxErr       func() error
	ctxCancel    func()
	downloadDone chan error

	l        sync.RWMutex
	filename string
	fileSize int64
	reader   io.Reader
}

var tasks map[string]*fileTask = make(map[string]*fileTask)
var tasksLock sync.RWMutex

func (t *fileTask) ID() string {
	return t.id
}

func (t *fileTask) SetFile(filename string, fileSize int64, reader io.Reader) {
	t.l.Lock()
	defer t.l.Unlock()
	t.filename = filename
	t.fileSize = fileSize
	t.reader = reader
}

func (t *fileTask) File() (filename string, fileSize int64, reader io.Reader) {
	t.l.RLock()
	defer t.l.RUnlock()
	filename = t.filename
	fileSize = t.fileSize
	reader = t.reader
	return
}

func (t *fileTask) SetReader(reader io.Reader) {
	t.l.Lock()
	defer t.l.Unlock()
	t.reader = reader
}

func (t *fileTask) CtxDone() <-chan struct{} {
	return t.ctxDone()
}

func (t *fileTask) CtxCancel() {
	t.ctxCancel()
}

func (t *fileTask) CtxErr() error {
	return t.ctxErr()
}

func (t *fileTask) DownloadDone() <-chan error {
	return t.downloadDone
}

func (t *fileTask) SetDownloadDone(err error) {
	t.downloadDone <- err
}

const maxTask = 1024
const idLength = 3

func newTask(timeout time.Duration) (*fileTask, error) {
	tasksLock.Lock()
	defer tasksLock.Unlock()

	if len(tasks) >= maxTask {
		return nil, errors.New("too many tasks")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	task := &fileTask{
		ctxDone:      ctx.Done,
		ctxErr:       ctx.Err,
		ctxCancel:    cancel,
		downloadDone: make(chan error),
	}

	for i := 0; i < 9999; i++ {
		id := token.New(idLength)
		if _, ok := tasks[id]; ok {
			continue
		}
		task.id = id
		tasks[id] = task
		break
	}

	if task.id == "" {
		return nil, errors.New("can't generate a unique task ID")
	}

	return task, nil
}
