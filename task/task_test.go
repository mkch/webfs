package task_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mkch/webfs/task"
)

func TestContent(t *testing.T) {
	content := task.NewFileContent(strings.NewReader("abc"))
	reader := content.Reader()

	if b, err := io.ReadAll(reader); err != nil {
		t.Fatal(err)
	} else if str := string(b); str != "abc" {
		t.Fatal(str)
	}

	if err := content.DownloadErr(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-content.DownloadStarted():
		t.Fatal("should not started")
	default:
	}

	select {
	case <-content.DownloadDone():
		t.Fatal("should not done")
	default:
	}

	content.SetDownloadStarted()
	select {
	case <-content.DownloadStarted():
	default:
		t.Fatal("should be started")
	}

	downloadErr := errors.New("some download error")
	errorRecv := make(chan error)
	go func() {
		<-content.DownloadDone()
		errorRecv <- content.DownloadErr()
	}()
	content.SetDownloadDone(downloadErr)
	if err := <-errorRecv; err != downloadErr {
		t.Fatal(err)
	}
}

func TestTask(t *testing.T) {
	ft, err := task.New(3, time.Millisecond*100, "abc", []task.FileInfo{{"abc.txt", 100}})
	if err != nil {
		t.Fatal(err)
	}
	if task := task.Query(ft.ID()); task == nil {
		t.Fatal(task)
	}
	if id := ft.ID(); len(id) != 3 {
		t.Fatal(id)
	}
	if secret := ft.Secret(); secret != "abc" {
		t.Fatal(secret)
	}
	if content := ft.File(0).Content(); content == nil {
		t.Fatal(content)
	}
	if done := ft.CtxDone(); done == nil {
		t.Fatal(done)
	}
	if err := ft.CtxErr(); err != nil {
		t.Fatal(err)
	}

	time.Sleep(time.Millisecond * 110)
	<-ft.CtxDone()
	if err := ft.CtxErr(); err != context.DeadlineExceeded {
		t.Fatal(err)
	}
}
