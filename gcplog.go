package gcplog

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"cloud.google.com/go/errorreporting"
	"cloud.google.com/go/logging"
	"google.golang.org/genproto/googleapis/api/monitoredres"
)

/*
	Structs
*/

type LogMetadata struct {
	user         string
	trace        string
	traceSampled bool
	span         string
	request      *logging.HTTPRequest
}

type ErrorEntry struct {
	err        error
	stackTrace []byte
	meta       *LogMetadata
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
	resource      *monitoredres.MonitoredResource
}

func NewGcpLog(projectId string, serviceName string, resource string) GcpLog {

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
	}
	if resource != "" {
		instance.resource = &monitoredres.MonitoredResource{
			Type: resource,
			Labels: map[string]string{
				"project_id":   projectId,
				"service_name": serviceName,
			},
		}
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

func (g *GcpLog) LogWithMeta(log interface{}, meta LogMetadata) {
	g.log(log, &meta, logging.Info)
}

func (g *GcpLog) Warn(err ErrorEntry) {
	g.log(err.err, err.meta, logging.Warning)

	if os.Getenv("GO_ENV") == "production" {
		g.err(err.err, err.stackTrace, err.meta)
	}
}

func (g *GcpLog) Error(err ErrorEntry) {
	g.log(err.err, err.meta, logging.Error)

	if os.Getenv("GO_ENV") == "production" {
		g.err(err.err, err.stackTrace, err.meta)
	}
}

/*
	Internal methods
*/
type logEntry struct {
	Severity       string            `json:"severity"`
	Message        interface{}       `json:"message"`
	HttpRequest    *httpRequest      `json:"httpRequest,omitempty"`
	Timestamp      time.Time         `json:"timestamp"`
	Labels         map[string]string `json:"logging.googleapis.com/labels,omitempty"`
	Operation      *operation        `json:"logging.googleapis.com/operation,omitempty"`
	SourceLocation *sourceLocation   `json:"logging.googleapis.com/sourceLocation,omitempty"`
	SpanID         string            `json:"logging.googleapis.com/spanId,omitempty"`
	TraceID        string            `json:"logging.googleapis.com/trace,omitempty"`
	TraceSampled   bool              `json:"logging.googleapis.com/trace_sampled,omitempty"`
}

type httpRequest struct {
	RequestMethod                  string `json:"requestMethod,omitempty"`
	RequestUrl                     string `json:"requestUrl,omitempty"`
	RequestSize                    string `json:"requestSize,omitempty"`
	Status                         int    `json:"status,omitempty"`
	ResponseSize                   string `json:"responseSize,omitempty"`
	UserAgent                      string `json:"userAgent,omitempty"`
	RemoteIp                       string `json:"remoteIp,omitempty"`
	ServerIp                       string `json:"serverIp,omitempty"`
	Referer                        string `json:"referer,omitempty"`
	Latency                        string `json:"latency,omitempty"`
	CacheLookup                    bool   `json:"cacheLookup,omitempty"`
	CacheHit                       bool   `json:"cacheHit,omitempty"`
	CacheValidatedWithOriginServer bool   `json:"cacheValidatedWithOriginServer,omitempty"`
	CacheFillBytes                 string `json:"cacheFillBytes,omitempty"`
	Protocol                       string `json:"protocol,omitempty"`
}
type operation struct {
	Id       string `json:"id,omitempty"`
	Producer string `json:"producer,omitempty"`
	First    string `json:"first,omitempty"`
	Last     string `json:"last,omitempty"`
}

type sourceLocation struct {
	File     string `json:"file,omitempty"`
	Line     string `json:"line,omitempty"`
	Function string `json:"function,omitempty"`
}

func (g *GcpLog) log(payload interface{}, metadata *LogMetadata, severity logging.Severity) {
	entry := logEntry{
		Severity:  severity.String(),
		Message:   payload,
		Timestamp: time.Now(),
	}
	if metadata.request != nil {
		entry.HttpRequest = &httpRequest{
			RequestMethod: metadata.request.Request.Method,
			RequestUrl:    metadata.request.Request.URL.String(),
			UserAgent:     metadata.request.Request.UserAgent(),
			RemoteIp:      metadata.request.Request.RemoteAddr,
			Referer:       metadata.request.Request.Referer(),
		}
	}
	if metadata.trace != "" {
		entry.TraceID = metadata.trace
		entry.TraceSampled = metadata.traceSampled
	}
	if metadata.span != "" {
		entry.SpanID = metadata.span
	}
	if err := json.NewEncoder(os.Stderr).Encode(entry); err != nil {
		fmt.Printf("failure to write structured log entry: %v", err)
	}

	// entry := logging.Entry{
	// 	// Log anything that can be marshaled to JSON.
	// 	Payload:  payload,
	// 	Severity: severity,
	// }
	// if g.resource != nil {
	// 	entry.Resource = g.resource
	// }
	// if metadata != nil {
	// 	if metadata.trace != "" {
	// 		entry.Trace = metadata.trace
	// 		entry.TraceSampled = metadata.traceSampled
	// 	}
	// 	if metadata.span != "" {
	// 		entry.SpanID = metadata.span
	// 	}
	// 	if metadata.request != nil {
	// 		entry.HTTPRequest = metadata.request
	// 	}
	// 	if metadata.user != "" {
	// 		entry.Labels = map[string]string{"user": metadata.user}
	// 	}
	// }

	// defer g.logger.Flush()
	// g.logger.Log(entry)
}

func (g *GcpLog) err(err error, stacktrace []byte, metadata *LogMetadata) {
	defer g.errorClient.Flush()
	errorEntry := errorreporting.Entry{
		Error: err,
	}
	if stacktrace != nil {
		errorEntry.Stack = stacktrace
	}
	if metadata != nil && metadata.request != nil {
		errorEntry.Req = metadata.request.Request
	}
	g.errorClient.Report(errorEntry)
}
