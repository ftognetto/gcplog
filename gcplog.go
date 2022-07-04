package gcplog

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"runtime/debug"
	"time"

	"cloud.google.com/go/errorreporting"
	"cloud.google.com/go/logging"
)

/*
	Structs
*/

type GcpLogOptions struct {
	ExtractUserFromRequest func(r *http.Request) string
}

type ResponseMetadata struct {
	Status  int
	Size    int
	Latency time.Duration
}

// type GcpLog interface {
// 	Log(log LogEntry)
// 	Warn(err ErrorEntry)
// 	Error(err ErrorEntry)
// 	Close()
// }

/*
	Constructor
*/

type GcpLog struct {
	projectId     string
	serviceName   string
	loggingClient *logging.Client
	errorClient   *errorreporting.Client
	logger        *logging.Logger
	options       *GcpLogOptions
}

func NewGcpLog(projectId string, serviceName string, options GcpLogOptions) GcpLog {

	if projectId == "" || serviceName == "" {
		panic("Gcp log not correctly initialized.")
	}

	ctx := context.Background()

	// Creates a Logging client.
	loggingClient, err := logging.NewClient(ctx, projectId)
	if err != nil {
		log.Fatalf("Failed to create logging client: %v", err)
	}
	// Selects the log to write to.
	logger := loggingClient.Logger(serviceName)

	// Creates a Error reporting client.
	errorClient, err := errorreporting.NewClient(ctx, projectId, errorreporting.Config{
		ServiceName: serviceName,
		OnError: func(err error) {
			log.Printf("Could not log error: %v", err)
		},
	})
	if err != nil {
		log.Fatalf("Failed to create error reporting client: %v", err)
	}

	instance := GcpLog{
		projectId:     projectId,
		serviceName:   serviceName,
		loggingClient: loggingClient,
		errorClient:   errorClient,
		logger:        logger,
		options:       &options,
	}
	return instance
}

/*
	Public methods
*/

func (g *GcpLog) Close() {
	errLogging := g.loggingClient.Close()
	errError := g.errorClient.Close()
	if errLogging != nil || errError != nil {
		log.Printf("Failed to close client: %v, %v", errLogging, errError)
	}
}

// LOG

func (g *GcpLog) Log(log interface{}) {
	go g.log(log, nil, nil, logging.Info)
}

func (g *GcpLog) LogR(log interface{}, request *http.Request) {
	go g.log(log, request, nil, logging.Info)
}

func (g *GcpLog) LogRM(log interface{}, request *http.Request, responseMeta *ResponseMetadata) {
	go g.log(log, request, responseMeta, logging.Info)
}

// WARN

func (g *GcpLog) Warn(err error) {
	go g.log(err, nil, nil, logging.Warning)

	if os.Getenv("GO_ENV") == "production" {
		go g.err(err, nil)
	}
}

func (g *GcpLog) WarnR(err error, request *http.Request) {
	go g.log(err, request, nil, logging.Warning)

	if os.Getenv("GO_ENV") == "production" {
		go g.err(err, request)
	}
}

func (g *GcpLog) WarnRM(err error, request *http.Request, responseMeta *ResponseMetadata) {
	go g.log(err, request, responseMeta, logging.Warning)

	if os.Getenv("GO_ENV") == "production" {
		go g.err(err, request)
	}
}

// ERROR

func (g *GcpLog) Error(err error) {
	go g.log(err, nil, nil, logging.Error)

	if os.Getenv("GO_ENV") == "production" {
		go g.err(err, nil)
	}
}

func (g *GcpLog) ErrorR(err error, request *http.Request) {
	go g.log(err, request, nil, logging.Error)

	if os.Getenv("GO_ENV") == "production" {
		go g.err(err, request)
	}
}

func (g *GcpLog) ErrorRM(err error, request *http.Request, responseMeta *ResponseMetadata) {
	go g.log(err, request, responseMeta, logging.Error)

	if os.Getenv("GO_ENV") == "production" {
		go g.err(err, request)
	}
}

/*
	Internal methods
*/

func (g *GcpLog) log(payload interface{}, request *http.Request, responseMeta *ResponseMetadata, severity logging.Severity) {
	defer g.logger.Flush()
	entry := logging.Entry{
		Payload:  payload,
		Severity: severity,
	}
	if request != nil {
		httpRequest := parseRequest(request, responseMeta)
		entry.HTTPRequest = &httpRequest
		trace, span, traceSampled := parseTrace(request, g.projectId)
		entry.Trace = trace
		entry.SpanID = span
		entry.TraceSampled = traceSampled
		if g.options.ExtractUserFromRequest != nil {
			user := g.options.ExtractUserFromRequest(request)
			entry.Labels = map[string]string{"user": user}
		}
	}
	g.logger.Log(entry)
}

func (g *GcpLog) err(err error, request *http.Request) {
	defer g.errorClient.Flush()
	errorEntry := errorreporting.Entry{
		Error: err,
		Stack: debug.Stack(),
	}
	if request != nil {
		errorEntry.Req = request
	}
	g.errorClient.Report(errorEntry)
}

func parseRequest(r *http.Request, w *ResponseMetadata) logging.HTTPRequest {

	localIp := r.Header.Get("X-Real-Ip")
	if localIp == "" {
		localIp = r.Header.Get("X-Forwarded-For")
	}
	if localIp == "" {
		localIp = r.RemoteAddr
	}

	request := logging.HTTPRequest{
		Request:     r,
		RequestSize: r.ContentLength,
		LocalIP:     localIp,
		RemoteIP:    r.RemoteAddr,
	}
	if w != nil {
		request.Status = w.Status
		request.ResponseSize = int64(w.Size)
		request.Latency = w.Latency
	}

	return request
}

func parseTrace(r *http.Request, projectId string) (traceId string, spanId string, traceSampled bool) {
	var traceRegex = regexp.MustCompile(
		// Matches on "TRACE_ID"
		`([a-f\d]+)?` +
			// Matches on "/SPAN_ID"
			`(?:/([a-f\d]+))?` +
			// Matches on ";0=TRACE_TRUE"
			`(?:;o=(\d))?`)
	matches := traceRegex.FindStringSubmatch(r.Header.Get("X-Cloud-Trace-Context"))

	traceId, spanId, traceSampled = matches[1], matches[2], matches[3] == "1"

	if traceId != "" {
		traceId = fmt.Sprintf("projects/%s/traces/%s", projectId, traceId)
	}
	if spanId == "0" {
		spanId = ""
	}

	return
}
