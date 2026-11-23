package replyfilter

import (
	"bufio"
	"net"
	"net/http"
)

// ResponseWriter describes the optional host bookkeeping already used by the
// gateway. It is structural: the engine imports no web framework. New accepts
// any net/http writer and supplies the same bookkeeping when it is absent.
type ResponseWriter interface {
	http.ResponseWriter
	http.Flusher
	http.Hijacker
	http.CloseNotifier
	Status() int
	Size() int
	Written() bool
	WriteHeaderNow()
	WriteString(string) (int, error)
	Pusher() http.Pusher
}

func adaptWriter(writer http.ResponseWriter) ResponseWriter {
	if existing, ok := writer.(ResponseWriter); ok {
		return existing
	}
	return &httpWriter{ResponseWriter: writer, status: http.StatusOK, size: -1}
}

// httpWriter is request-owned. It delays header commitment just like the
// original gateway writer, so a buffered policy rejection can replace 200.
type httpWriter struct {
	http.ResponseWriter
	status, size int
}

func (w *httpWriter) WriteHeader(status int) {
	if !w.Written() && status > 0 {
		w.status = status
	}
}

func (w *httpWriter) WriteHeaderNow() {
	if !w.Written() {
		w.ResponseWriter.WriteHeader(w.status)
		w.size = 0
	}
}

func (w *httpWriter) Write(body []byte) (int, error) {
	w.WriteHeaderNow()
	n, err := w.ResponseWriter.Write(body)
	w.size += n
	return n, err
}

func (w *httpWriter) WriteString(body string) (int, error) { return w.Write([]byte(body)) }
func (w *httpWriter) Status() int                          { return w.status }
func (w *httpWriter) Size() int                            { return w.size }
func (w *httpWriter) Written() bool                        { return w.size >= 0 }
func (w *httpWriter) Unwrap() http.ResponseWriter          { return w.ResponseWriter }

func (w *httpWriter) Flush() {
	w.WriteHeaderNow()
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *httpWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *httpWriter) CloseNotify() <-chan bool {
	if notifier, ok := w.ResponseWriter.(http.CloseNotifier); ok {
		return notifier.CloseNotify()
	}
	// net/http applications should use Request.Context for cancellation.
	// No goroutine is created to emulate this deprecated optional method.
	return nil
}

func (w *httpWriter) Pusher() http.Pusher {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher
	}
	return nil
}
