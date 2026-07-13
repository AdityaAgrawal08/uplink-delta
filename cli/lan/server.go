package lan

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
)

var ActiveConnections int32

func GetActiveConnections() int32 {
	return atomic.LoadInt32(&ActiveConnections)
}

func ServeFileLAN(ctx context.Context, path string, port int, cert tls.Certificate, onComplete func()) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr: net.JoinHostPort("0.0.0.0", strconv.Itoa(port)),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
		},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&ActiveConnections, 1)

		currentInfo, err := os.Stat(path)
		if err != nil || currentInfo.ModTime().Unix() != info.ModTime().Unix() || currentInfo.Size() != info.Size() {
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte("File modified since server start"))
			return
		}

		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		w.Header().Set("ETag", fmt.Sprintf(`"%x-%x"`, info.ModTime().Unix(), info.Size()))
		
		http.ServeFile(w, r, path)

		// Serve completed, run completion callback
		if r.Method == "GET" {
			go func() {
				if onComplete != nil {
					onComplete()
				}
			}()
		}
	})

	server.Handler = handler

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	err = server.ListenAndServeTLS("", "")
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
