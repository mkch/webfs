package main

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"time"

	"github.com/mkch/webfs/token"
)

type FileTaskContent struct {
	downloadStarted chan struct{} // Closed when downloading started.
	downloadDone    chan struct{} // Closed when downloading done.

	filename string    // Filename of the task.
	fileSize int64     // File size of the task. -1 if unavailable.
	reader   io.Reader // File content of task.

	l             sync.RWMutex
	downloadError error // The error of download.
}

// File returns the file of the task.
func (c *FileTaskContent) File() (filename string, fileSize int64, reader io.Reader) {
	filename = c.filename
	fileSize = c.fileSize
	reader = c.reader
	return
}

func (c *FileTaskContent) DownloadDone() <-chan struct{} {
	return c.downloadDone
}

// Call after DownloadDone is closed.
func (c *FileTaskContent) DownloadErr() error {
	c.l.RLock()
	defer c.l.RUnlock()
	return c.downloadError
}

func (c *FileTaskContent) SetDownloadDone(err error) {
	c.l.Lock()
	c.downloadError = err
	c.l.Unlock()
	close(c.downloadDone)
}

func (c *FileTaskContent) DownloadStarted() <-chan struct{} {
	return c.downloadStarted
}

func (c *FileTaskContent) SetDownloadStarted() {
	close(c.downloadStarted)
}

func newFileTaskContent(filename string, fileSize int64, reader io.Reader) *FileTaskContent {
	if reader == nil {
		panic(reader)
	}
	return &FileTaskContent{
		downloadStarted: make(chan struct{}),
		downloadDone:    make(chan struct{}),
		filename:        filename,
		fileSize:        fileSize,
		reader:          reader,
	}
}

type fileTask struct {
	id     string
	secret string // Secret to cancel task.

	ctxDone   func() <-chan struct{} // The Done method of task context.
	ctxErr    func() error           // The Err method of task context.
	ctxCancel func()                 // The cancel function of task context.

	content chan (*FileTaskContent) // The content of uploading/downloading.
}

// All pending tasks indexed by ID.
var tasks map[string]*fileTask = make(map[string]*fileTask)
var tasksLock sync.RWMutex

func (t *fileTask) ID() string {
	return t.id
}

func (t *fileTask) Secret() string {
	return t.secret
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

func (t *fileTask) Content() chan *FileTaskContent {
	return t.content
}

const maxTask = 10240

// newTask creates a new file task.
func newTask(idLen int, timeout time.Duration, secret string) (*fileTask, error) {
	tasksLock.Lock()
	defer tasksLock.Unlock()

	if len(tasks) >= maxTask {
		return nil, errors.New("too many tasks")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	task := &fileTask{
		secret:    secret,
		ctxDone:   ctx.Done,
		ctxErr:    ctx.Err,
		ctxCancel: cancel,
		content:   make(chan *FileTaskContent),
	}

	for i := 0; i < 9999; i++ {
		id := token.New(idLen)
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

	// Remove timeout/cancelled task.
	go func() {
		<-task.CtxDone()
		removeTask(task.ID())
		log.Printf("Removed task [%v]", task.ID())
	}()

	log.Printf("New task: [%v]", task.ID())
	return task, nil
}

func removeTask(id string) {
	tasksLock.Lock()
	defer tasksLock.Unlock()

	delete(tasks, id)
}

func queryTask(id string) *fileTask {
	tasksLock.RLock()
	defer tasksLock.RUnlock()
	return tasks[id]
}
