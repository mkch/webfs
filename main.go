package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/template"
	"time"
)

const DefaultServeAddr = ":8080"

const DefaultIDLen = 3
const MaxIDLen = 64

var t *template.Template

var idLen int // length of task code.

func main() {
	var serveAddr string

	flag.StringVar(&serveAddr, "http", DefaultServeAddr, "HTTP service address")
	flag.IntVar(&idLen, "code_len", DefaultIDLen, fmt.Sprintf("Length of the task code, [%v,%v]", DefaultIDLen, MaxIDLen))
	if idLen < DefaultIDLen || idLen > MaxIDLen {
		fmt.Fprintln(os.Stderr, "Invalid code_len")
		os.Exit(1)
	}
	flag.Parse()

	t = template.Must(template.ParseGlob("t/*.html"))

	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/new_task", handleNewTask)
	http.HandleFunc("/send_file", handleSendFile)
	http.HandleFunc("/receive_file", handleReceiveFile)
	http.HandleFunc("/send", handleSend)
	http.HandleFunc("/receive", handleReceive)

	log.Printf("Starting server %v", serveAddr)
	if err := http.ListenAndServe(serveAddr, nil); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func execTemplate(w io.Writer, name string, value any) {
	if err := t.ExecuteTemplate(w, name, value); err != nil {
		log.Panic(err)
	}
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

// handleNewTask generates a new fileTask and responds the ID.
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
	task, err := newTask(idLen, timeout)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_, err = io.WriteString(w, task.ID())
	if err != nil {
		log.Println(err)
		return
	}
}

// handleSend renders /send page.
func handleSend(w http.ResponseWriter, r *http.Request) {
	execTemplate(w, "send.html", nil)
}

// handleReceive renders /receive page.
func handleReceive(w http.ResponseWriter, r *http.Request) {
	execTemplate(w, "receive.html", nil)
}

// handleSendFile uploads a file to the fileTask.
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

	// This task is already uploading.
	if !task.SetFile(filename, fileSize, r.Body) {
		http.Error(w, "duplicated upload", http.StatusBadRequest)
		return
	}

	select {
	case <-task.DownloadStarted():
		// If the downloading started before task timeout/cancellation,
		// task timeout/cancellation is ignored.
		err = <-task.DownloadDone()
	case err = <-task.DownloadDone(): // Finish downloading.
	case <-task.ctxDone(): // Task timeout/cancelled.
		err = task.ctxErr()
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
}

// handleReceiveFile download a file from the fileTask.
func handleReceiveFile(w http.ResponseWriter, r *http.Request) {
	var query = r.URL.Query()
	taskId := query.Get("id")
	task := fetchTask(taskId) // fetch: only one download per task.
	if task == nil {
		http.Error(w, "no such task", http.StatusNotFound)
		return
	}
	defer task.CtxCancel()

	filename, fileSize, reader := task.File()
	header := w.Header()
	if fileSize >= 0 {
		header.Set("Content-Length", strconv.FormatInt(fileSize, 10))
	}
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Disposition
	header.Set("Content-Disposition", fmt.Sprintf(`attachment; filename*=utf-8''%v; filename="%v"`, url.QueryEscape(filename), strings.ReplaceAll(filename, `"`, `_`)))
	header.Set("Content-Type", "application/octet-stream")

	task.SetDownloadStarted()
	_, err := io.Copy(w, reader)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) {
			log.Println(err)
			err = errors.New("network error occurred")
		} else {
			log.Panic(err)
		}
	}
	task.SetDownloadDone(err)
}
