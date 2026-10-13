package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Exit immediately after Serve, just like the actual CLI entry point. This
// catches shutdown races that an in-process httptest server cannot expose.
func TestServerSubprocess(t *testing.T) {
	if os.Getenv("CHECKIN_TEST_SERVER") != "1" {
		return
	}
	port, err := strconv.Atoi(os.Getenv("CHECKIN_TEST_PORT"))
	if err != nil {
		os.Exit(2)
	}
	if err := Serve(context.Background(), port, false); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestQuitFlushesResponseBeforeProcessExits(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	listener, port, err := Bind(0)
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestServerSubprocess$")
	cmd.Env = append(os.Environ(), "CHECKIN_TEST_SERVER=1", fmt.Sprintf("CHECKIN_TEST_PORT=%d", port))
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	waited := false
	defer func() {
		if !waited {
			cmd.Process.Kill()
			<-exited
		}
	}()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	ready := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			waited = true
			t.Fatalf("server exited during startup: %v %s", err, &output)
		default:
		}
		response, err := client.Get(url + "/api/auth/status")
		if err == nil {
			response.Body.Close()
			ready = response.StatusCode == 200
			if ready {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("server did not start")
	}
	client.CloseIdleConnections()
	request, _ := http.NewRequest("POST", url+"/api/quit", strings.NewReader(`{}`))
	request.Header.Set("X-Checkin", "1")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("quit response lost during process exit: " + err.Error())
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil || payload["ok"] != true {
		t.Fatalf("quit response truncated: %v %+v", err, payload)
	}
	response.Body.Close()
	select {
	case err := <-exited:
		waited = true
		if err != nil {
			t.Fatalf("server exited with error: %v %s", err, &output)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("quit left server process running")
	}
	if response, err := client.Get(url + "/api/auth/status"); err == nil {
		response.Body.Close()
		t.Fatal("HTTP service remained available after process exit")
	}
}
