package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"
)

const (
	maxLogSize  = 5 << 20
	maxLogFiles = 3
)

type rotatingWriter struct {
	path string
	file *os.File
	size int64
	mu   sync.Mutex
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	if w.size+int64(len(p)) > maxLogSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *rotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	for i := maxLogFiles - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", w.path, i)
		to := fmt.Sprintf("%s.%d", w.path, i+1)
		if _, err := os.Stat(from); err == nil {
			_ = os.Rename(from, to)
		}
	}
	_ = os.Rename(w.path, w.path+".1")
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w.file = file
	w.size = 0
	return nil
}

func openLogger() (*log.Logger, func(), error) {
	dir := os.Getenv("TRIM_PKGVAR")
	if dir == "" {
		return log.New(os.Stderr, "", log.LstdFlags|log.LUTC), func() {}, nil
	}
	path := dir + string(os.PathSeparator) + "proxy.log"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	writer := &rotatingWriter{path: path, file: file, size: info.Size()}
	logger := log.New(writer, "", log.LstdFlags|log.LUTC)
	return logger, func() { _ = writer.Close() }, nil
}

var _ io.WriteCloser = (*rotatingWriter)(nil)
