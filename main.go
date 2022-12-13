package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/mkch/webfs/modfs"
	"github.com/mkch/webfs/token"

	"embed"
)

//go:embed static
var staticFiles embed.FS

var staticFileServer = http.FileServer(http.FS(
	// Add a valid ModTime to embed.FS, so the response can be cached by client.
	// The original ModTime of the file returned by embed.FS.Open is 0.
	&modfs.FS{
		FS:           staticFiles,
		LastModified: time.Now(),
	}))

const DefaultServeAddr = ":8080"

const DefaultIDLen = 3
const MaxIDLen = 64

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

	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/new_task", handleNewTask)
	http.HandleFunc("/cancel_task", handleCancelTask)
	http.HandleFunc("/send_file", handleSendFile)
	http.HandleFunc("/r/", handleReceiveFile)
	http.HandleFunc("/send", handleSend)
	http.HandleFunc("/receive", handleReceive)
	http.HandleFunc("/res/", handleRes)

	log.Printf("Starting server %v", serveAddr)
	if err := http.ListenAndServe(serveAddr, nil); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

const defaultTaskTimeout = time.Minute * 10
const maxTaskTimeout = time.Minute * 30
const taskSecretLen = 16

// handleCancelTask cancels a fileTask.
func handleCancelTask(w http.ResponseWriter, r *http.Request) {
	var query = r.URL.Query()
	id, secret := query.Get("task"), query.Get("secret")
	if id == "" || secret == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	task := queryTask(id)
	if task == nil || task.Secret() != secret {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	task.CtxCancel()
}

// handleNewTask generates a new fileTask and responds the ID and secret.
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
	task, err := newTask(idLen, timeout, token.New(taskSecretLen))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err = json.NewEncoder(w).Encode(struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}{ID: task.ID(), Secret: task.Secret()})
	if err != nil {
		log.Println(err)
		return
	}
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	r.URL.Path = "static/home.html"
	staticFileServer.ServeHTTP(w, r)
}

// handleSend renders /send page.
func handleSend(w http.ResponseWriter, r *http.Request) {
	r.URL.Path = "static/send.html"
	staticFileServer.ServeHTTP(w, r)
}

// handleReceive renders /receive page.
func handleReceive(w http.ResponseWriter, r *http.Request) {
	r.URL.Path = "static/receive.html"
	staticFileServer.ServeHTTP(w, r)
}

// handleSendFile uploads a file to the fileTask.
func handleSendFile(w http.ResponseWriter, r *http.Request) {
	var query = r.URL.Query()
	task := queryTask(query.Get("task"))
	if task == nil || task.Secret() != query.Get("secret") {
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

	var content = newFileTaskContent(filename, fileSize, r.Body)
	select {
	case <-task.CtxDone():
		http.Error(w, task.CtxErr().Error(), http.StatusBadRequest)
		return
	case task.Content() <- content:
	}

	select {
	case <-content.DownloadStarted():
		// If the downloading started before task timeout/cancellation,
		// task timeout/cancellation is ignored.
		select {
		case <-content.DownloadDone():
			err = content.DownloadErr()
		case <-r.Context().Done(): // Upload cancelled by client.
			err = r.Context().Err()
		}
	case <-content.DownloadDone(): // Finish downloading.
		err = content.DownloadErr()
	case <-task.ctxDone(): // Task timeout/cancelled.
		err = task.ctxErr()
	case <-r.Context().Done(): // Upload cancelled by client.
		err = r.Context().Err()
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
}

// recvFailDelay is the delay after failure of
// finding a task, for security.
const recvFailDelay = time.Second * 2

// handleReceiveFile download a file from the fileTask.
func handleReceiveFile(w http.ResponseWriter, r *http.Request) {
	task := queryTask(path.Base(r.URL.Path))
	if task == nil {
		// Increase the cost of brute force.
		time.Sleep(recvFailDelay)
		http.Error(w, "no such task", http.StatusNotFound)
		return
	}

	var content *FileTaskContent
	select {
	case content = <-task.Content():
	case <-task.CtxDone():
		http.Error(w, task.ctxErr().Error(), http.StatusNotFound)
		return
	case <-r.Context().Done():
		// The request connection is closed.
		// No need to write any response.
		return
	}

	filename, fileSize, reader := content.File()
	header := w.Header()
	if fileSize >= 0 {
		header.Set("Content-Length", strconv.FormatInt(fileSize, 10))
	}
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Disposition
	header.Set("Content-Disposition", fmt.Sprintf(`attachment; filename*=utf-8''%v`, url.PathEscape(filename)))
	header.Set("Content-Type", "application/octet-stream")

	content.SetDownloadStarted()
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
	content.SetDownloadDone(err)
}

func handleRes(w http.ResponseWriter, r *http.Request) {
	newPath, err := url.JoinPath("static", r.URL.Path)
	if err != nil {
		log.Panic(err)
	}
	r.URL.Path = newPath
	staticFileServer.ServeHTTP(w, r)
}
