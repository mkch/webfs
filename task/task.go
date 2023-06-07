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

type FileInfo struct {
	Name string `json:"name"` // Filename of the task.
	Size int64  `json:"size"` // File size of the task. -1 if unavailable.
}

type FileContent struct {
	reader io.Reader // File data.

	downloadStarted chan struct{} // Closed when downloading started.
	downloadDone    chan struct{} // Closed when downloading done.

	l             sync.RWMutex
	downloadError error // The error of download.
}

// NewFileContent creates a new FileContent.
func NewFileContent(reader io.Reader) *FileContent {
	return &FileContent{
		downloadStarted: make(chan struct{}),
		downloadDone:    make(chan struct{}),
		reader:          reader,
	}
}

func (c *FileContent) Reader() io.Reader {
	return c.reader
}

// DownloadDone returns a channel that's closed by calling SetDownloadDone.
func (c *FileContent) DownloadDone() <-chan struct{} {
	return c.downloadDone
}

// If SetDownloadDone is not yet called, DownloadErr returns nil.
// If DownloadDone is closed,  DownloadErr returns the error set by SetDownloadDone.
func (c *FileContent) DownloadErr() error {
	c.l.RLock()
	defer c.l.RUnlock()
	return c.downloadError
}

// SetDownloadDone marks the downloading is done by closing DownloadDone.
// err is the error occurred during downloading, nil if none.
func (c *FileContent) SetDownloadDone(err error) {
	c.l.Lock()
	c.downloadError = err
	c.l.Unlock()
	close(c.downloadDone)
}

// DownloadStarted returns a channel that's closed by calling SetDownloadStarted.
func (c *FileContent) DownloadStarted() <-chan struct{} {
	return c.downloadStarted
}

// SetDownloadStarted marks the downloading is started by closing DownloadStarted.
func (c *FileContent) SetDownloadStarted() {
	close(c.downloadStarted)
}

// File is the content of a file task.
type File struct {
	info    FileInfo
	content chan (*FileContent)
}

func (c *File) Content() chan (*FileContent) {
	return c.content
}

// Info returns the information of file.
func (c *File) Info() FileInfo {
	return c.info
}

// newFiles creates a slice of *File.
func newFiles(info []FileInfo) (files []*File) {
	files = make([]*File, 0, len(info))
	for _, f := range info {
		files = append(files, &File{info: f, content: make(chan *FileContent)})
	}
	return
}

type Task struct {
	id     string
	secret string // Secret to cancel task.

	ctxDone   func() <-chan struct{} // The Done method of task context.
	ctxErr    func() error           // The Err method of task context.
	ctxCancel func()                 // The cancel function of task context.

	files []*File
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

func (t *Task) NFiles() int {
	return len(t.files)
}

func (t *Task) File(n int) *File {
	return t.files[n]
}

const maxTask = 10240

// New creates a new file task.
func New(idLen int, timeout time.Duration, secret string, files []FileInfo) (*Task, error) {
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
		files:     newFiles(files),
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
