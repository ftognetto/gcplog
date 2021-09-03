package gcplog

import (
	"bytes"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"cloud.google.com/go/logging"
)

// responseWriter is a minimal wrapper for http.ResponseWriter that allows the
// written HTTP status code to be captured for logging.
type responseWriter struct {
	http.ResponseWriter
	status      int
	size        int
	wroteHeader bool
}

func wrapResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{ResponseWriter: w}
}

func (rw *responseWriter) Status() int {
	return rw.status
}

func (rw *responseWriter) Size() int {
	return rw.size
}

func (rw *responseWriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.status = code

	var buf bytes.Buffer
	rw.Header().Write(&buf)
	rw.size += buf.Len()

	rw.ResponseWriter.WriteHeader(code)
	rw.wroteHeader = true

}

func Middleware(projectId string, serviceName string, resource string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {

			Init(projectId, serviceName)
			gcplog := NewGcpLog(resource)

			defer func() {

				if err := recover(); err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					gcplog.Error(ErrorEntry{
						err:        err.(error),
						stackTrace: debug.Stack(),
					})

				}
			}()
			defer gcplog.Close()

			start := time.Now()
			wrapped := wrapResponseWriter(w)
			next.ServeHTTP(wrapped, r)

			// after request
			status := wrapped.status
			log := r.Method + " " + r.URL.Path

			request := parseRequest(*wrapped, r, start)
			trace := parseTrace(r, projectId)

			if status < 400 {
				gcplog.Log(LogEntry{
					log:     log,
					trace:   trace,
					request: &request,
				})
				return
			}

			var err error

			if status >= 400 && status < 500 {
				gcplog.Warn(ErrorEntry{
					err:     err,
					trace:   trace,
					request: &request,
				})
			} else {
				gcplog.Error(ErrorEntry{
					err:     err,
					trace:   trace,
					request: &request,
				})
			}
		}

		return http.HandlerFunc(fn)
	}
}

func parseRequest(w responseWriter, r *http.Request, start time.Time) logging.HTTPRequest {

	localIp := r.Header.Get("X-Real-Ip")
	if localIp == "" {
		localIp = r.Header.Get("X-Forwarded-For")
	}
	if localIp == "" {
		localIp = r.RemoteAddr
	}

	request := logging.HTTPRequest{
		Request:      r,
		RequestSize:  r.ContentLength,
		Status:       w.Status(),
		ResponseSize: int64(w.Size()),
		Latency:      time.Since(start),

		LocalIP:  localIp,
		RemoteIP: r.RemoteAddr,
	}

	return request
}

func parseTrace(r *http.Request, projectId string) string {
	var trace string
	traceHeader := r.Header.Get("X-Cloud-Trace-Context")
	traceParts := strings.Split(traceHeader, "/")
	if len(traceParts) > 0 && len(traceParts[0]) > 0 {
		trace = fmt.Sprintf("projects/%s/traces/%s", projectId, traceParts[0])
	}
	return trace
}
