package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestLocalServiceStartsAndCancelsCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := serve(ctx, []string{"--data-dir", t.TempDir()}, writer)
		done <- errors.Join(err, writer.Close())
	}()
	defer func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	}()
	scanner := bufio.NewScanner(reader)
	if !scanner.Scan() {
		cancel()
		t.Fatal("startup record missing")
	}
	var ready map[string]string
	if err := json.Unmarshal(scanner.Bytes(), &ready); err != nil {
		cancel()
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(ready["url"] + "/healthz")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatal("health failed")
	}
	if err = response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("service shutdown leaked")
	}
}
