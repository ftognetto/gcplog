package gcplog

import (
	"context"
	"log"
	"os"

	"cloud.google.com/go/errorreporting"
	"cloud.google.com/go/logging"
	"google.golang.org/genproto/googleapis/api/monitoredres"
)

type LogEntry struct {
	log     interface{}
	trace   string
	request *logging.HTTPRequest
}
type ErrorEntry struct {
	err        error
	trace      string
	stackTrace []byte
	request    *logging.HTTPRequest
}

// type GcpLog interface {
// 	Log(log LogEntry)
// 	Warn(err ErrorEntry)
// 	Error(err ErrorEntry)
// 	Close()
// }

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

func (g *GcpLog) Close() {
	errLogging := g.loggingClient.Close()
	errError := g.errorClient.Close()
	if errLogging != nil || errError != nil {
		log.Printf("Failed to close client: %v, %v", errLogging, errError)
	}
}

func (g *GcpLog) Log(log LogEntry) {
	defer g.logger.Flush()
	entry := logging.Entry{
		// Log anything that can be marshaled to JSON.
		Payload:  log.log,
		Severity: logging.Info,
	}
	if g.resource != nil {
		entry.Resource = g.resource
	}
	if log.trace != "" {
		entry.Trace = log.trace
	}
	if log.request != nil {
		entry.HTTPRequest = log.request
	}
	g.logger.Log(entry)
}
func (g *GcpLog) Warn(err ErrorEntry) {
	defer g.logger.Flush()
	loggingEntry := logging.Entry{
		// Log anything that can be marshaled to JSON.
		Payload:  err.err,
		Severity: logging.Warning,
	}
	if g.resource != nil {
		loggingEntry.Resource = g.resource
	}
	if err.trace != "" {
		loggingEntry.Trace = err.trace
	}
	if err.request != nil {
		loggingEntry.HTTPRequest = err.request
	}
	g.logger.Log(loggingEntry)

	if os.Getenv("GO_ENV") == "production" {
		defer g.errorClient.Flush()
		errorEntry := errorreporting.Entry{
			Error: err.err,
		}
		if err.request != nil {
			errorEntry.Req = err.request.Request
		}
		if err.stackTrace != nil {
			errorEntry.Stack = err.stackTrace
		}
		if err.request != nil {
			errorEntry.Req = err.request.Request
		}
		g.errorClient.Report(errorEntry)
	}
}
func (g *GcpLog) Error(err ErrorEntry) {
	defer g.logger.Flush()
	entry := logging.Entry{
		// Log anything that can be marshaled to JSON.
		Payload:  err.err,
		Severity: logging.Error,
	}
	if g.resource != nil {
		entry.Resource = g.resource
	}
	if err.trace != "" {
		entry.Trace = err.trace
	}
	if err.request != nil {
		entry.HTTPRequest = err.request
	}
	g.logger.Log(entry)

	if os.Getenv("GO_ENV") == "production" {
		defer g.errorClient.Flush()
		errorEntry := errorreporting.Entry{
			Error: err.err,
		}
		if err.stackTrace != nil {
			errorEntry.Stack = err.stackTrace
		}
		if err.request != nil {
			errorEntry.Req = err.request.Request
		}
		g.errorClient.Report(errorEntry)
	}
}
