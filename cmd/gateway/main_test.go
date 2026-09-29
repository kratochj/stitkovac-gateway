package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentReadinessHandshake(t *testing.T) {
	dir := t.TempDir()
	password := filepath.Join(dir, "password")
	if err := os.WriteFile(password, []byte("private-test-password"), 0600); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "state")
	if err := run([]string{"init", "--data-dir", data, "--password-file", password}); err != nil {
		t.Fatal(err)
	}
	reserved, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reserved.Addr().String()
	reserved.Close()
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readyRead.Close()
	defer readyWrite.Close()
	goRead, goWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer goRead.Close()
	defer goWrite.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestAgentProcessHelper$", "--", "serve", "--data-dir", data, "--admin-address", address, "--launcher-ready-fd=3", "--launcher-continue-fd=4")
	cmd.Env = append(os.Environ(), "GATEWAY_AGENT_TEST_CHILD=1")
	cmd.ExtraFiles = []*os.File{readyWrite, goRead}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	readyWrite.Close()
	goRead.Close()
	finished := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(finished) }()
	defer func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-finished
			t.Error("agent did not shut down")
		}
	}()
	readiness := make(chan string, 1)
	go func() { b, _ := io.ReadAll(io.LimitReader(readyRead, 128)); readiness <- string(b) }()
	select {
	case b := <-readiness:
		if b != "READY 1.2.3\n" {
			t.Fatalf("bad readiness: %q", b)
		}
	case <-finished:
		t.Fatalf("agent exited before ready: %v", waitErr)
	case <-time.After(10 * time.Second):
		t.Fatal("agent readiness timeout")
	}
	// The actual HTTPS listener must already be working before confirmation.
	cert, err := os.ReadFile(filepath.Join(data, "tls.crt"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("invalid local certificate")
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	defer client.CloseIdleConnections()
	response, err := client.Get("https://" + address + "/login")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("web not ready: %d", response.StatusCode)
	}
	if _, err := io.WriteString(goWrite, "CONTINUE\n"); err != nil {
		t.Fatal(err)
	}
	goWrite.Close()
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
		if waitErr != nil {
			t.Fatal(waitErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not stop after confirmation")
	}
}

func TestAgentProcessHelper(t *testing.T) {
	if os.Getenv("GATEWAY_AGENT_TEST_CHILD") != "1" {
		return
	}
	version = "1.2.3"
	index := -1
	for i, arg := range os.Args {
		if arg == "--" {
			index = i
			break
		}
	}
	if index < 0 {
		os.Exit(2)
	}
	if err := run(os.Args[index+1:]); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
