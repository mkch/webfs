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

type fileTask struct {
	id string

	ctxDone         func() <-chan struct{} // The Done method of task context.
	ctxErr          func() error           // The Err method of task context.
	ctxCancel       func()                 // The cancel function of task context.
	downloadStarted chan error             // Closed when downloading started.
	downloadDone    chan error             // Be sent to when downloading done.

	// l locks the following members.
	l        sync.RWMutex
	filename string    // Filename of the task.
	fileSize int64     // File size of the task. -1 if unavailable.
	reader   io.Reader // File content of task.
}

// All pending tasks indexed by ID.
var tasks map[string]*fileTask = make(map[string]*fileTask)
var tasksLock sync.RWMutex

func (t *fileTask) ID() string {
	return t.id
}

// SetFile sets the file information of this task.
// Returns false if a file is already set.
func (t *fileTask) SetFile(filename string, fileSize int64, reader io.Reader) bool {
	if reader == nil {
		panic(reader)
	}
	t.l.Lock()
	defer t.l.Unlock()
	if t.reader != nil {
		return false
	}
	t.filename = filename
	t.fileSize = fileSize
	t.reader = reader
	return true
}

// File returns the file of the task.
func (t *fileTask) File() (filename string, fileSize int64, reader io.Reader) {
	t.l.RLock()
	defer t.l.RUnlock()
	filename = t.filename
	fileSize = t.fileSize
	reader = t.reader
	return
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

func (t *fileTask) DownloadStarted() <-chan error {
	return t.downloadStarted
}

func (t *fileTask) SetDownloadStarted() {
	close(t.downloadStarted)
}

const maxTask = 10240

// The length of file task ID.
const idLength = 3

// newTask creates a new file task.
func newTask(timeout time.Duration) (*fileTask, error) {
	tasksLock.Lock()
	defer tasksLock.Unlock()

	if len(tasks) >= maxTask {
		return nil, errors.New("too many tasks")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	task := &fileTask{
		ctxDone:         ctx.Done,
		ctxErr:          ctx.Err,
		ctxCancel:       cancel,
		downloadStarted: make(chan error),
		downloadDone:    make(chan error),
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

// fetchTask returns the task with id
// and remove it from task list, if any.
func fetchTask(id string) *fileTask {
	tasksLock.Lock()
	defer tasksLock.Unlock()

	task := tasks[id]
	delete(tasks, id)
	return task
}
