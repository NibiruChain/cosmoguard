package firewall

import (
	"bytes"
	"io"
	"net/http"
)

type reusableReader struct {
	io.Reader
	readBuf *bytes.Buffer
	backBuf *bytes.Buffer
}

func ReusableReader(r io.ReadCloser) io.ReadCloser {
	readBuf := bytes.Buffer{}
	readBuf.ReadFrom(r)
	r.Close()
	backBuf := bytes.Buffer{}

	return reusableReader{
		io.TeeReader(&readBuf, &backBuf),
		&readBuf,
		&backBuf,
	}
}

func (r reusableReader) Close() error {
	return nil
}

func (r reusableReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.reset()
	}
	return n, err
}

func (r reusableReader) reset() {
	io.Copy(r.readBuf, r.backBuf)
}

type ResponseWriterWrapper struct {
	http.ResponseWriter
	buf        *bytes.Buffer
	multi      io.Writer
	statusCode int
}

func WrapResponseWriter(w http.ResponseWriter) *ResponseWriterWrapper {
	buffer := &bytes.Buffer{}
	multi := io.MultiWriter(buffer, w)
	return &ResponseWriterWrapper{
		ResponseWriter: w,
		buf:            buffer,
		multi:          multi,
	}
}

func (w *ResponseWriterWrapper) Write(p []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	return w.multi.Write(p)
}

func (w *ResponseWriterWrapper) WriterHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *ResponseWriterWrapper) GetStatusCode() int {
	return w.statusCode
}

func (w *ResponseWriterWrapper) GetWrittenBytes() ([]byte, error) {
	return io.ReadAll(w.buf)
}
