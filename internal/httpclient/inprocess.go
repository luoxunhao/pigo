package httpclient

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
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
	rec := httptest.NewRecorder()
	t.handler.ServeHTTP(rec, req)
	res := &http.Response{
		StatusCode:    rec.Code,
		Status:        http.StatusText(rec.Code),
		Header:        rec.Header(),
		Body:          io.NopCloser(bytes.NewReader(rec.Body.Bytes())),
		ContentLength: int64(rec.Body.Len()),
		Request:       req,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
	}
	return res, nil
}
