package server

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func Run(assets fs.FS, seed []byte) error {
	startup, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	data, err := openStore(startup, seed)
	if err != nil {
		return err
	}
	defer data.db.Close()
	api, err := newAPI(data)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", ":8000")
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: routes(api, assets), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 20 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("Приложение открыто по адресу http://127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port)
	err = srv.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
