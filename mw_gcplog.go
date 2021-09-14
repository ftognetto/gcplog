package gcplog

import (
	"bytes"
	"fmt"
	"net/http"
	"time"
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
					gcplog.ErrorR(err.(error), r)

				}
			}()

			begin := time.Now()
			wrapped := wrapResponseWriter(w)
			next.ServeHTTP(wrapped, r)

			// after request
			status := wrapped.status
			log := options.logBuilder(r)
			err := options.errorBuilder(r, wrapped.status, wrapped.size, wrapped.body)
			responseMeta := ResponseMeta{
				Size:    wrapped.Size(),
				Status:  wrapped.Status(),
				Latency: time.Since(begin),
			}

			if status < 400 {
				gcplog.LogRM(log, r, &responseMeta)
			} else if status >= 400 && status < 500 {
				gcplog.WarnRM(err, r, &responseMeta)
			} else {
				gcplog.ErrorRM(err, r, &responseMeta)
			}
		}

		return http.HandlerFunc(fn)
	}
}
