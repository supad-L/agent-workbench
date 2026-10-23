package main

import (
	"agent-workbench/internal/app"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"time"
)

func serve(ctx context.Context, args []string, w io.Writer) (returnErr error) {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	data := flags.String("data-dir", "", "absolute local data directory")
	port := flags.Int("port", 0, "loopback service port; zero chooses an available port")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *data == "" || !filepath.IsAbs(*data) || flags.NArg() != 0 || *port < 0 || *port > 65535 {
		return errors.New("serve requires an absolute --data-dir and valid loopback port")
	}
	backend, err := app.Open(ctx, *data)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, backend.Close()) }()
	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(*port))
	if err != nil {
		return err
	}
	server := &http.Server{Handler: backend.Handler(), ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	if err = json.NewEncoder(w).Encode(map[string]string{"url": "http://" + listener.Addr().String(), "human_token": backend.HumanToken}); err != nil {
		return errors.Join(err, listener.Close())
	}
	done := make(chan struct{})
	shutdown := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := server.Shutdown(stop)
		if err != nil {
			err = errors.Join(err, server.Close())
		}
		shutdown <- err
	}()
	err = server.Serve(listener)
	close(done)
	shutdownErr := <-shutdown
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return errors.Join(err, shutdownErr)
}
