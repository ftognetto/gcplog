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

// responseWriter is a minimal wrapper for http.responseWriter that allows the
// written HTTP status code to be captured for logging.
type responseWriter struct {
	http.ResponseWriter
	status      int
	size        int
	body        *bytes.Buffer
	wroteHeader bool
}

func wrapResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{ResponseWriter: w, body: &bytes.Buffer{}}
}

func (rw *responseWriter) Status() int {
	return rw.status
}

func (rw *responseWriter) Size() int {
	return rw.size
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	rw.body.Write(b)
	return rw.ResponseWriter.Write(b)
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

func defaultLog(r *http.Request) string {
	log := r.Method + " " + r.URL.Path
	if r.Header.Get("X-Request-ID") != "" {
		log = "[" + r.Header.Get("X-Request-ID") + "] " + log
	}
	return log
}

func defaultError(r *http.Request, status int, size int, body *bytes.Buffer) error {
	var err error
	if body != nil {
		err = fmt.Errorf(body.String())
	} else {
		err = fmt.Errorf(r.Method + " " + r.URL.Path)
	}
	return err
}

func Middleware(gcplog *GcpLog) func(http.Handler) http.Handler {
	return middleware(gcplog, defaultLog, defaultError)
}

func MiddlewareCustom(
	gcplog *GcpLog,
	logBuilder func(r *http.Request) string,
	errorBuilder func(r *http.Request, status int, size int, body *bytes.Buffer) error,
) func(http.Handler) http.Handler {
	return middleware(gcplog, logBuilder, errorBuilder)
}

func middleware(
	gcplog *GcpLog,
	logBuilder func(r *http.Request) string,
	errorBuilder func(r *http.Request, status int, size int, body *bytes.Buffer) error,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {

			defer func() {

				if err := recover(); err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					gcplog.Error(ErrorEntry{
						err:        err.(error),
						stackTrace: debug.Stack(),
					})

				}
			}()

			start := time.Now()
			wrapped := wrapResponseWriter(w)
			next.ServeHTTP(wrapped, r)

			// after request
			status := wrapped.status
			log := logBuilder(r)
			err := errorBuilder(r, wrapped.status, wrapped.size, wrapped.body)
			request := parseRequest(*wrapped, r, start)
			trace := parseTrace(r, gcplog.projectId)

			if status < 400 {
				gcplog.Log(LogEntry{
					log:     log,
					trace:   trace,
					request: &request,
				})
				return
			}
			if status >= 400 && status < 500 {
				gcplog.Warn(ErrorEntry{
					err:        err,
					trace:      trace,
					request:    &request,
					stackTrace: debug.Stack(),
				})
			} else {
				gcplog.Error(ErrorEntry{
					err:        err,
					trace:      trace,
					request:    &request,
					stackTrace: debug.Stack(),
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
