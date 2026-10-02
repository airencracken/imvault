// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"imvault/internal/testutil"
)

func TestRealSandboxedServerLifecycle(t *testing.T) {
	if os.Getenv("COMFYWARE_SANDBOX_TEST") != "1" {
		t.Skip("set COMFYWARE_SANDBOX_TEST=1 to require native sandbox integration")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "imvault")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	data := filepath.Join(root, "data with spaces")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	testutil.Close(t, listener)
	environment := append(os.Environ(), "IMVAULT_DATA_DIR="+data, "IMVAULT_ADDR="+address)
	environment = append(environment, "IMVAULT_MEDIA_SANDBOX=true")
	check := exec.Command(binary, "sandbox", "--check")
	check.Env = environment
	if out, err := check.CombinedOutput(); err != nil || !strings.Contains(string(out), "sandbox is ready") {
		t.Fatalf("check: %s %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(data, "imvault.db")); !os.IsNotExist(err) {
		t.Fatal("check created a database")
	}
	failed := exec.Command(binary, "sandbox", "--bwrap", "/usr/bin/false")
	failed.Env = environment
	if out, err := failed.CombinedOutput(); err == nil {
		t.Fatalf("sandbox failure started server: %s", out)
	}
	if _, err := os.Stat(filepath.Join(data, "imvault.db")); !os.IsNotExist(err) {
		t.Fatal("failed sandbox created a database")
	}
	command := exec.Command(binary, "sandbox")
	command.Env = environment
	var log bytes.Buffer
	command.Stdout, command.Stderr = &log, &log
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() }) // normally already stopped
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	ready := false
	client := &http.Client{Timeout: time.Second}
	deadline := time.After(10 * time.Second)
	for !ready {
		response, err := client.Get("http://" + address + "/healthz")
		if err == nil {
			ready = response.StatusCode == 200
			testutil.Close(t, response.Body)
		}
		if ready {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("server stopped: %v\n%s", err, &log)
		case <-deadline:
			_ = command.Process.Kill() // the start failure is what is reported
			<-done
			t.Fatalf("server did not start:\n%s", &log)
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful shutdown: %v\n%s", err, &log)
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill() // the slow shutdown is what is reported
		<-done
		t.Fatalf("shutdown timed out:\n%s", &log)
	}
	if info, err := os.Stat(filepath.Join(data, "imvault.db")); err != nil || info.Size() == 0 {
		t.Fatalf("no persistent database: %v", err)
	}
}
