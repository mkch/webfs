package task

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"time"

	"github.com/mkch/webfs/token"
)

// Content is the content of a file task.
type Content struct {
	downloadStarted chan struct{} // Closed when downloading started.
	downloadDone    chan struct{} // Closed when downloading done.

	filename string    // Filename of the task.
	fileSize int64     // File size of the task. -1 if unavailable.
	reader   io.Reader // File content of task.

	l             sync.RWMutex
	downloadError error // The error of download.
}

// File returns the file of the task.
func (c *Content) File() (filename string, fileSize int64, reader io.Reader) {
	filename = c.filename
	fileSize = c.fileSize
	reader = c.reader
	return
}

// DownloadDone returns a channel that's closed by calling SetDownloadDone.
func (c *Content) DownloadDone() <-chan struct{} {
	return c.downloadDone
}

// If SetDownloadDone is not yet called, DownloadErr returns nil.
// If DownloadDone is closed,  DownloadErr returns the error set by SetDownloadDone.
func (c *Content) DownloadErr() error {
	c.l.RLock()
	defer c.l.RUnlock()
	return c.downloadError
}

// SetDownloadDone marks the downloading is done by closing DownloadDone.
// err is the error occurred during downloading, nil if none.
func (c *Content) SetDownloadDone(err error) {
	c.l.Lock()
	c.downloadError = err
	c.l.Unlock()
	close(c.downloadDone)
}

// DownloadStarted returns a channel that's closed by calling SetDownloadStarted.
func (c *Content) DownloadStarted() <-chan struct{} {
	return c.downloadStarted
}

// SetDownloadStarted marks the downloading is started by closing DownloadStarted.
func (c *Content) SetDownloadStarted() {
	close(c.downloadStarted)
}

// NewContent creates a new Content.
// fileSize can be -1 if unavailable.
func NewContent(filename string, fileSize int64, reader io.Reader) *Content {
	if reader == nil {
		panic(reader)
	}
	return &Content{
		downloadStarted: make(chan struct{}),
		downloadDone:    make(chan struct{}),
		filename:        filename,
		fileSize:        fileSize,
		reader:          reader,
	}
}

type Task struct {
	id     string
	secret string // Secret to cancel task.

	ctxDone   func() <-chan struct{} // The Done method of task context.
	ctxErr    func() error           // The Err method of task context.
	ctxCancel func()                 // The cancel function of task context.

	content chan (*Content) // The content of uploading/downloading.
}

// All pending tasks indexed by ID.
var tasks map[string]*Task = make(map[string]*Task)
var tasksLock sync.RWMutex

func (t *Task) ID() string {
	return t.id
}

func (t *Task) Secret() string {
	return t.secret
}

func (t *Task) CtxDone() <-chan struct{} {
	return t.ctxDone()
}

func (t *Task) CtxCancel() {
	t.ctxCancel()
}

func (t *Task) CtxErr() error {
	return t.ctxErr()
}

func (t *Task) Content() chan *Content {
	return t.content
}

const maxTask = 10240

// New creates a new file task.
func New(idLen int, timeout time.Duration, secret string) (*Task, error) {
	tasksLock.Lock()
	defer tasksLock.Unlock()

	if len(tasks) >= maxTask {
		return nil, errors.New("too many tasks")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	task := &Task{
		secret:    secret,
		ctxDone:   ctx.Done,
		ctxErr:    ctx.Err,
		ctxCancel: cancel,
		content:   make(chan *Content),
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
		remove(task.ID())
		log.Printf("Removed task [%v]", task.ID())
	}()

	log.Printf("New task: [%v]", task.ID())
	return task, nil
}

func remove(id string) {
	tasksLock.Lock()
	defer tasksLock.Unlock()

	delete(tasks, id)
}

func Query(id string) *Task {
	tasksLock.RLock()
	defer tasksLock.RUnlock()
	return tasks[id]
}
