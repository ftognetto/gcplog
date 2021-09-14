package gcplog

import (
	"context"
	"log"
	"net/http"
	"os"
	"regexp"
	"runtime/debug"

	"cloud.google.com/go/errorreporting"
	"cloud.google.com/go/logging"
)

/*
	Structs
*/

type GcpLogOptions struct {
	ExtractUserFromRequest func(r *http.Request) string
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

func (g *GcpLog) Log(log interface{}) {
	g.log(log, nil, logging.Info)
}

func (g *GcpLog) LogR(log interface{}, request *http.Request) {
	g.log(log, request, logging.Info)
}

func (g *GcpLog) Warn(err error) {
	g.log(err, nil, logging.Warning)

	if os.Getenv("GO_ENV") == "production" {
		g.err(err, nil)
	}
}

func (g *GcpLog) WarnR(err error, request *http.Request) {
	g.log(err, request, logging.Warning)

	if os.Getenv("GO_ENV") == "production" {
		g.err(err, request)
	}
}

func (g *GcpLog) Error(err error) {
	g.log(err, nil, logging.Error)

	if os.Getenv("GO_ENV") == "production" {
		g.err(err, nil)
	}
}

func (g *GcpLog) ErrorR(err error, request *http.Request) {
	g.log(err, request, logging.Error)

	if os.Getenv("GO_ENV") == "production" {
		g.err(err, request)
	}
}

/*
	Internal methods
*/

func (g *GcpLog) log(payload interface{}, request *http.Request, severity logging.Severity) {
	defer g.logger.Flush()
	entry := logging.Entry{
		// Log anything that can be marshaled to JSON.
		Payload:  payload,
		Severity: severity,
	}
	// if g.resource != nil {
	// 	entry.Resource = g.resource
	// }
	if request != nil {
		httpRequest := parseRequest(request)
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

func parseRequest(r *http.Request) logging.HTTPRequest {

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
		Status:      r.Response.StatusCode,

		ResponseSize: r.Response.ContentLength,

		LocalIP:  localIp,
		RemoteIP: r.RemoteAddr,
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

	if spanId == "0" {
		spanId = ""
	}

	return
}
