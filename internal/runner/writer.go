package runner

import (
	"context"
	"log/slog"
	"time"

	"github.com/metalgeekhub/llmbench/internal/store"
)

const (
	writerBatchSize = 200
	writerInterval  = 250 * time.Millisecond
	writerBuffer    = 2048
)

// writer persists request records in batches from a single goroutine, so
// virtual users never wait on SQLite between requests.
type writer struct {
	st   store.Store
	ch   chan store.RequestRecord
	done chan struct{}
}

func newWriter(st store.Store) *writer {
	w := &writer{st: st, ch: make(chan store.RequestRecord, writerBuffer), done: make(chan struct{})}
	go w.loop()
	return w
}

func (w *writer) add(rec store.RequestRecord) { w.ch <- rec }

// close flushes everything and waits for the writer to finish.
func (w *writer) close() {
	close(w.ch)
	<-w.done
}

func (w *writer) loop() {
	defer close(w.done)
	batch := make([]store.RequestRecord, 0, writerBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := w.st.SaveRequests(context.Background(), batch); err != nil {
			slog.Error("saving benchmark requests", "count", len(batch), "err", err)
		}
		batch = batch[:0]
	}
	tick := time.NewTicker(writerInterval)
	defer tick.Stop()
	for {
		select {
		case rec, ok := <-w.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, rec)
			if len(batch) >= writerBatchSize {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}
