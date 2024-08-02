package cosmoguard

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

func GetSourceIP(r *http.Request) string {
	IPAddress := r.Header.Get("X-Real-Ip")
	if IPAddress == "" {
		IPAddress = r.Header.Get("X-Forwarded-For")
	}
	if IPAddress == "" {
		IPAddress = r.RemoteAddr
	}
	return IPAddress
}

func WriteData(w http.ResponseWriter, code int, data []byte, headers ...string) {
	if len(headers)%2 == 0 {
		for i := 0; i < len(headers); i += 2 {
			w.Header().Set(headers[i], headers[i+1])
		}
	}
	w.WriteHeader(code)
	w.Write(data)
}

func WriteError(w http.ResponseWriter, code int, msg string, headers ...string) {
	if len(headers)%2 == 0 {
		for i := 0; i < len(headers); i += 2 {
			w.Header().Set(headers[i], headers[i+1])
		}
	}
	w.WriteHeader(code)
	w.Write([]byte(msg))
}
