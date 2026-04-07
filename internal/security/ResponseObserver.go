package security

import (
	"bufio"
	"io"
	"net"
	"net/http"
)

type responseObserver struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func newResponseObserver(w http.ResponseWriter) *responseObserver {
	if existing, ok := w.(*responseObserver); ok {
		return existing
	}
	return &responseObserver{
		ResponseWriter: w,
		status:         http.StatusOK,
	}
}

func (o *responseObserver) Status() int {
	if o == nil {
		return http.StatusOK
	}
	return o.status
}

func (o *responseObserver) WriteHeader(statusCode int) {
	if o.wroteHeader {
		return
	}
	o.status = statusCode
	o.wroteHeader = true
	o.ResponseWriter.WriteHeader(statusCode)
}

func (o *responseObserver) Write(p []byte) (int, error) {
	if !o.wroteHeader {
		o.WriteHeader(http.StatusOK)
	}
	return o.ResponseWriter.Write(p)
}

func (o *responseObserver) ReadFrom(r io.Reader) (int64, error) {
	if !o.wroteHeader {
		o.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := o.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(r)
	}
	return io.Copy(o.ResponseWriter, r)
}

func (o *responseObserver) Flush() {
	if flusher, ok := o.ResponseWriter.(http.Flusher); ok {
		if !o.wroteHeader {
			o.WriteHeader(http.StatusOK)
		}
		flusher.Flush()
	}
}

func (o *responseObserver) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := o.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (o *responseObserver) Push(target string, opts *http.PushOptions) error {
	pusher, ok := o.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

func (o *responseObserver) Unwrap() http.ResponseWriter {
	if o == nil {
		return nil
	}
	return o.ResponseWriter
}
