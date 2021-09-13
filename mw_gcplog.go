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

func defaultLogBuilder(r *http.Request) string {
	log := r.Method + " " + r.URL.Path
	if r.Header.Get("X-Request-ID") != "" {
		log = "[" + r.Header.Get("X-Request-ID") + "] " + log
	}
	return log
}

func defaultErrorBuilder(r *http.Request, status int, size int, body *bytes.Buffer) error {
	var err error
	if body != nil {
		err = fmt.Errorf(body.String())
	} else {
		err = fmt.Errorf(r.Method + " " + r.URL.Path)
	}
	return err
}

func defaultExtractUserFromRequest(r *http.Request) string {
	return ""
}

type options struct {
	logBuilder             func(r *http.Request) string
	errorBuilder           func(r *http.Request, status int, size int, body *bytes.Buffer) error
	extractUserFromRequest func(r *http.Request) string
}

func NewOptions(logBuilder func(r *http.Request) string, errorBuilder func(r *http.Request, status int, size int, body *bytes.Buffer) error, extractUserFromRequest func(r *http.Request) string) options {
	options := options{}

	if logBuilder != nil {
		options.logBuilder = logBuilder
	} else {
		options.logBuilder = defaultLogBuilder
	}

	if errorBuilder != nil {
		options.errorBuilder = errorBuilder
	} else {
		options.errorBuilder = defaultErrorBuilder
	}

	if extractUserFromRequest != nil {
		options.extractUserFromRequest = extractUserFromRequest
	} else {
		options.extractUserFromRequest = defaultExtractUserFromRequest
	}

	return options
}

func Middleware(gcplog *GcpLog) func(http.Handler) http.Handler {
	return middleware(
		gcplog,
		options{
			logBuilder:             defaultLogBuilder,
			errorBuilder:           defaultErrorBuilder,
			extractUserFromRequest: defaultExtractUserFromRequest,
		},
	)
}

func MiddlewareCustom(
	gcplog *GcpLog,
	options options,
) func(http.Handler) http.Handler {
	return middleware(gcplog, options)
}

func middleware(
	gcplog *GcpLog,
	options options,
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
			log := options.logBuilder(r)
			err := options.errorBuilder(r, wrapped.status, wrapped.size, wrapped.body)
			request := parseRequest(*wrapped, r, start)
			trace := parseTrace(r, gcplog.projectId)
			user := options.extractUserFromRequest(r)

			if status < 400 {
				gcplog.LogWithMeta(
					log,
					LogMetadata{
						trace:   trace,
						request: &request,
						user:    user,
					},
				)
				return
			}
			if status >= 400 && status < 500 {
				gcplog.Warn(ErrorEntry{
					err:        err,
					stackTrace: debug.Stack(),
					meta: &LogMetadata{
						trace:   trace,
						request: &request,
						user:    user,
					},
				})
			} else {
				gcplog.Error(ErrorEntry{
					err:        err,
					stackTrace: debug.Stack(),
					meta: &LogMetadata{
						trace:   trace,
						request: &request,
						user:    user,
					},
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
