package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/template"
	"time"
)

const ServeAddr = ":8080"

var t *template.Template

func main() {
	t = template.Must(template.ParseGlob("t/*.html"))

	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/new_task", handleNewTask)
	http.HandleFunc("/send_file", handleSendFile)
	http.HandleFunc("/receive_file", handleReceiveFile)
	http.HandleFunc("/send", handleSend)
	http.HandleFunc("/receive", handleReceive)
	http.ListenAndServe(ServeAddr, nil)
}

func execTemplate(w io.Writer, name string, value any) {
	if err := t.ExecuteTemplate(w, name, value); err != nil {
		log.Panic(err)
	}
}

func queryTask(id string) *fileTask {
	tasksLock.RLock()
	defer tasksLock.RUnlock()
	return tasks[id]
}

func cancelTask(id string) {
	tasksLock.Lock()
	defer tasksLock.Unlock()

	task := tasks[id]
	if task == nil {
		return
	}
	task.CtxCancel()
	delete(tasks, id)
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	execTemplate(w, "index.html", nil)
}

const defaultTaskTimeout = time.Minute * 10
const maxTaskTimeout = time.Minute * 30

func handleNewTask(w http.ResponseWriter, r *http.Request) {
	var query = r.URL.Query()
	timeout := defaultTaskTimeout
	if query.Has("timeout") {
		if i, err := strconv.Atoi(query.Get("timeout")); err != nil || i <= 0 {
			http.Error(w, "invalid timeout", http.StatusBadRequest)
			return
		} else if d := time.Second * time.Duration(i); d > maxTaskTimeout {
			http.Error(w, "invalid timeout", http.StatusBadRequest)
			return
		} else {
			timeout = d
		}
	}
	task, err := newTask(timeout)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_, err = io.WriteString(w, task.ID())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// in case of timeout.
	go func() {
		<-task.CtxDone()
		cancelTask(task.ID())
	}()
}

func handleSend(w http.ResponseWriter, r *http.Request) {
	execTemplate(w, "send.html", nil)
}

func handleReceive(w http.ResponseWriter, r *http.Request) {
	execTemplate(w, "receive.html", nil)
}

func handleSendFile(w http.ResponseWriter, r *http.Request) {
	var query = r.URL.Query()
	taskID := query.Get("task")
	task := queryTask(taskID)
	if task == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	filename := query.Get("filename")
	if len(filename) == 0 {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}

	fileSize, err := strconv.ParseInt(query.Get("size"), 10, 64)
	if err != nil || fileSize < 0 && fileSize != -1 {
		http.Error(w, "invalid size", http.StatusBadRequest)
		return
	}

	task.SetFile(filename, fileSize, r.Body)

	defer cancelTask(task.ID())

	select {
	case err = <-task.DownloadDone():
	case <-task.ctxDone():
		err = task.ctxErr()
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
}

func handleReceiveFile(w http.ResponseWriter, r *http.Request) {
	var query = r.URL.Query()
	taskId := query.Get("id")
	task := queryTask(taskId)
	if task == nil {
		http.Error(w, "no such task", http.StatusNotFound)
		return
	}

	filename, fileSize, reader := task.File()
	header := w.Header()
	if fileSize >= 0 {
		header.Set("Content-Length", strconv.FormatInt(fileSize, 10))
	}
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Disposition
	header.Set("Content-Disposition", fmt.Sprintf(`attachment; filename*=utf-8''%v; filename="%v"`, url.QueryEscape(filename), strings.ReplaceAll(filename, `"`, `_`)))
	header.Set("Content-Type", "application/octet-stream")

	_, err := io.Copy(w, reader)

	task.SetDownloadDone(err)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}
