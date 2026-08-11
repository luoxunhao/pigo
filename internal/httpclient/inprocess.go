package httpclient

import (
	"io"
	"net/http"
	"sync"
)

// InProcessClient returns a ClientWithResponses whose HTTP requests are
// routed directly to handler without opening a network listener.
func InProcessClient(handler http.Handler) (*ClientWithResponses, error) {
	client := &http.Client{Transport: &inProcessTransport{handler: handler}}
	return NewClientWithResponses("http://pigo.inprocess", WithHTTPClient(client))
}

type inProcessTransport struct {
	handler http.Handler
}

func (t *inProcessTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	pr, pw := io.Pipe()
	w := &streamingResponseWriter{
		header: make(http.Header),
		ready:  make(chan struct{}),
		done:   make(chan struct{}),
		pw:     pw,
	}
	go func() {
		t.handler.ServeHTTP(w, req)
		_ = pw.Close()
		close(w.done)
	}()
	select {
	case <-w.ready:
	case <-w.done:
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	res := &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Header:        w.header.Clone(),
		Body:          pr,
		ContentLength: -1,
		Request:       req,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
	}
	return res, nil
}

// streamingResponseWriter bridges a handler to io.Pipe so SSE responses can
// stream through the in-process HTTP client in real time instead of being
// buffered until the handler returns.
type streamingResponseWriter struct {
	header http.Header
	status int
	ready  chan struct{}
	done   chan struct{}
	pw     *io.PipeWriter
	once   sync.Once
}

func (w *streamingResponseWriter) Header() http.Header {
	return w.header
}

func (w *streamingResponseWriter) WriteHeader(status int) {
	w.status = status
	w.once.Do(func() { close(w.ready) })
}

func (w *streamingResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.pw.Write(b)
}

func (w *streamingResponseWriter) Flush() {}
