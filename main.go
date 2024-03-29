package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
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
	"github.com/mkch/webfs/task"
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

//go:embed template
var templateFiles embed.FS

var templates = template.Must(template.ParseFS(templateFiles, "template/*.html"))

const DefaultServeAddr = ":8080"

const DefaultIDLen = 3
const MaxIDLen = 64

var idLen int   // length of task code.
var showQR bool // Whether show QR code when sending file.

func main() {
	var serveAddr string

	flag.StringVar(&serveAddr, "http", DefaultServeAddr, "HTTP service address")
	flag.IntVar(&idLen, "code-len", DefaultIDLen, fmt.Sprintf("Length of the task code, [%v,%v]", DefaultIDLen, MaxIDLen))
	flag.BoolVar(&showQR, "show-qr", false, "Show QR code of downloading URL in sending page")
	flag.Parse()

	if idLen < DefaultIDLen || idLen > MaxIDLen {
		fmt.Fprintln(os.Stderr, "Invalid code-len")
		os.Exit(1)
	}

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
	t := task.Query(id)
	if t == nil || t.Secret() != secret {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	t.CtxCancel()
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

	var files []task.FileInfo
	if err := json.NewDecoder(r.Body).Decode(&files); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	for _, f := range files {
		if f.Name == "" || f.Size == 0 || (f.Size < 0 && f.Size != -1) {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
	}

	t, err := task.New(idLen, timeout, token.New(taskSecretLen), files)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err = json.NewEncoder(w).Encode(struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
		ShowQR bool   `json:"show_qr"`
	}{ID: t.ID(), Secret: t.Secret(), ShowQR: showQR})
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
	t := task.Query(query.Get("task"))
	if t == nil || t.Secret() != query.Get("secret") {
		// Increase the cost of brute force.
		time.Sleep(taskFailDelay)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	index, err := strconv.Atoi(query.Get("index"))
	if err != nil || index < 0 || index > t.NFiles()-1 {
		http.Error(w, "invalid index", http.StatusBadRequest)
		return
	}

	file := t.File(index)
	content := task.NewFileContent(r.Body)
	select {
	case <-t.CtxDone():
		http.Error(w, t.CtxErr().Error(), http.StatusBadRequest)
		return
	case file.Content() <- content:
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
	case <-t.CtxDone(): // Task timeout/cancelled.
		err = t.CtxErr()
	case <-r.Context().Done(): // Upload cancelled by client.
		err = r.Context().Err()
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
}

// taskFailDelay is the delay after failure of
// finding a task, for security.
const taskFailDelay = time.Second * 2

// handleReceiveFile download a file from the fileTask.
func handleReceiveFile(w http.ResponseWriter, r *http.Request) {
	t := task.Query(path.Base(r.URL.Path))
	if t == nil {
		// Increase the cost of brute force.
		time.Sleep(taskFailDelay)
		http.Error(w, "no such task", http.StatusNotFound)
		return
	}

	var err error
	query := r.URL.Query()
	index := 0
	if !query.Has("index") {
		if t.NFiles() > 1 {
			// Show file list.
			data := &struct {
				ID        string
				Indexes   []int
				Filenames []string
			}{ID: t.ID()}
			for i := 0; i < t.NFiles(); i++ {
				data.Indexes = append(data.Indexes, i)
				data.Filenames = append(data.Filenames, t.File(i).Info().Name)
			}
			err = templates.ExecuteTemplate(w, "file_list.html", data)
			if err != nil {
				log.Panic(err)
			}
			return
		}
	} else {
		index, err = strconv.Atoi(r.URL.Query().Get("index"))
	}

	if err != nil || index < 0 || index > t.NFiles()-1 {
		http.Error(w, "invalid index", http.StatusBadRequest)
		return
	}

	file := t.File(index)
	fileInfo := file.Info()

	var content *task.FileContent
	select {
	case content = <-file.Content():
	case <-t.CtxDone():
		http.Error(w, t.CtxErr().Error(), http.StatusNotFound)
		return
	case <-r.Context().Done():
		// The request connection is closed.
		// No need to write any response.
		return
	}

	header := w.Header()
	if fileInfo.Size >= 0 {
		header.Set("Content-Length", strconv.FormatInt(fileInfo.Size, 10))
	}
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Disposition
	header.Set("Content-Disposition", fmt.Sprintf(`attachment; filename*=utf-8''%v`, url.PathEscape(fileInfo.Name)))
	header.Set("Content-Type", "application/octet-stream")

	content.SetDownloadStarted()
	_, err = io.Copy(w, content.Reader())
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
