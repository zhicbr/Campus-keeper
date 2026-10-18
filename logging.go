package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// rotatingLog keeps the active log small. Archived logs remain available for
// diagnostics; the program does not permanently delete user files.
type rotatingLog struct {
	mu     sync.Mutex
	path   string
	file   *os.File
	size   int64
	max    int64
	silent bool
}

func newRotatingLog(path string, max int64) (*rotatingLog, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &rotatingLog{path: path, file: f, size: info.Size(), max: max}, nil
}

func (w *rotatingLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.silent {
		writeConsole(p)
	}
	if w.size+int64(len(p)) > w.max {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingLog) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	archive := fmt.Sprintf("%s.%s", w.path, time.Now().Format("20060102-150405.000"))
	if err := os.Rename(w.path, archive); err != nil {
		// Another process may briefly hold the file. Keep logging to the
		// active file and retry rotation on a later write.
		if reopened, reopenErr := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); reopenErr == nil {
			w.file = reopened
		}
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	w.file = f
	w.size = 0
	return nil
}

func (w *rotatingLog) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	return w.file.Close()
}

var _ io.WriteCloser = (*rotatingLog)(nil)

var consoleSink = struct {
	sync.RWMutex
	writer io.Writer
}{writer: io.Discard}

func writeConsole(p []byte) {
	consoleSink.RLock()
	_, _ = consoleSink.writer.Write(p)
	consoleSink.RUnlock()
}

func setConsoleWriter(w io.Writer) {
	consoleSink.Lock()
	consoleSink.writer = w
	consoleSink.Unlock()
}
