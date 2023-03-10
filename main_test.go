package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestNewTask(t *testing.T) {
	w := httptest.NewRecorder()
	idLen = 3
	handleNewTask(w, httptest.NewRequest("GET", "/new_task", nil))
	resp := w.Result()
	if code := resp.StatusCode; code != http.StatusOK {
		t.Fatal(code)
	}
	var task struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		t.Fatal(err)
	}
	if len(task.ID) != idLen {
		t.Fatal(task.ID)
	}
	if len(task.Secret) != taskSecretLen {
		t.Fatal(task.Secret)
	}
}

func TestSendFile(t *testing.T) {
	idLen = 6
	mux := http.NewServeMux()
	mux.HandleFunc("/new_task", handleNewTask)
	mux.HandleFunc("/send_file", handleSendFile)
	mux.HandleFunc("/r/", handleReceiveFile)

	server := httptest.NewServer(mux)

	resp, err := http.Get(fmt.Sprintf("%v/new_task", server.URL))
	if err != nil {
		t.Fatal(err)
	}
	var task struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		t.Fatal(err)
	}

	const fileContent = "abc"
	type recv struct {
		Resp *http.Response
		Err  error
	}
	recvChan := make(chan *recv)
	go func() {
		r, e := http.Get(fmt.Sprintf("%v/r/%v", server.URL, url.PathEscape(task.ID)))
		recvChan <- &recv{r, e}
	}()

	resp, err = http.Post(fmt.Sprintf("%v/send_file?task=%v&secret=%v&filename=%v&size=%v",
		server.URL,
		url.QueryEscape(task.ID), url.QueryEscape(task.Secret), url.QueryEscape("file1"), len(fileContent)),
		"", strings.NewReader(fileContent))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatal(resp.StatusCode, resp.Status)
	}

	recvResult := <-recvChan
	if err = recvResult.Err; err != nil {
		t.Fatal(err)
	}
	recvResp := recvResult.Resp
	if recvResp.StatusCode != http.StatusOK {
		t.Fatal(recvResp.StatusCode, recvResp.Status)
	}
	if cl := recvResp.Header.Get("Content-Length"); cl != strconv.Itoa(len(fileContent)) {
		t.Fatal(cl)
	}
	if cd := recvResp.Header.Get("Content-Disposition"); cd != `attachment; filename*=utf-8''file1` {
		t.Fatal(cd)
	}
	if ct := recvResp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatal(ct)
	}
	var recvFile []byte
	recvFile, err = io.ReadAll(recvResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(recvFile) != fileContent {
		t.Fatal(recvFile)
	}
}
