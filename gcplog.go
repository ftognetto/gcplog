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
	trace   *string
	request *logging.HTTPRequest
}
type ErrorEntry struct {
	err        error
	trace      *string
	stackTrace []byte
	request    *logging.HTTPRequest
}

type GcpLog interface {
	Log(log LogEntry)
	Warn(err ErrorEntry)
	Error(err ErrorEntry)
	Close()
}

type gcpLog struct {
	projectId     string
	serviceName   string
	loggingClient *logging.Client
	errorClient   *errorreporting.Client
	logger        *logging.Logger
	resource      *monitoredres.MonitoredResource
}

var _projectId string
var _serviceName string
var _resource string

func Init(projectId string, serviceName string) {
	_projectId = projectId
	_serviceName = serviceName
}

func NewGcpLog(resource string) GcpLog {

	if _projectId == "" || _serviceName == "" {
		panic("Gcp log not correctly initialized. Call Init before")
	}

	ctx := context.Background()

	// Creates a Logging client.
	loggingClient, err := logging.NewClient(ctx, _projectId)
	if err != nil {
		log.Fatalf("Failed to create logging client: %v", err)
	}
	// Selects the log to write to.
	logger := loggingClient.Logger(_serviceName)

	// Creates a Error reporting client.
	errorClient, err := errorreporting.NewClient(ctx, _projectId, errorreporting.Config{
		ServiceName: _serviceName,
		OnError: func(err error) {
			log.Printf("Could not log error: %v", err)
		},
	})
	if err != nil {
		log.Fatalf("Failed to create error reporting client: %v", err)
	}

	instance := &gcpLog{
		projectId:     _projectId,
		serviceName:   _serviceName,
		loggingClient: loggingClient,
		errorClient:   errorClient,
		logger:        logger,
	}
	if resource != "" {
		_resource = resource
		instance.resource = &monitoredres.MonitoredResource{
			Type: resource,
			Labels: map[string]string{
				"project_id":   _projectId,
				"service_name": _serviceName,
			},
		}
	}
	return instance
}

func (g *gcpLog) Close() {
	errLogging := g.loggingClient.Close()
	errError := g.errorClient.Close()
	if errLogging != nil || errError != nil {
		log.Printf("Failed to close client: %v, %v", errLogging, errError)
	}
}

func Log(log interface{}) {
	instance := NewGcpLog(_resource)
	defer instance.Close()
	instance.Log(LogEntry{
		log: log,
	})
}

func (g *gcpLog) Log(log LogEntry) {
	entry := logging.Entry{
		// Log anything that can be marshaled to JSON.

		Payload:  log.log,
		Severity: logging.Info,
	}
	if g.resource != nil {
		entry.Resource = g.resource
	}
	if log.trace != nil {
		entry.Trace = *log.trace
	}
	if log.request != nil {
		entry.HTTPRequest = log.request
	}
	g.logger.Log(entry)
}
func (g *gcpLog) Warn(err ErrorEntry) {
	loggingEntry := logging.Entry{
		// Log anything that can be marshaled to JSON.
		Payload:  err.err,
		Severity: logging.Warning,
	}
	if g.resource != nil {
		loggingEntry.Resource = g.resource
	}
	if err.trace != nil {
		loggingEntry.Trace = *err.trace
	}
	if err.request != nil {
		loggingEntry.HTTPRequest = err.request
	}
	g.logger.Log(loggingEntry)

	if os.Getenv("GO_ENV") == "production" {
		errorEntry := errorreporting.Entry{
			Error: err.err,
		}
		if err.request != nil {
			errorEntry.Req = err.request.Request
		}
		g.errorClient.Report(errorEntry)
	}
}
func (g *gcpLog) Error(err ErrorEntry) {
	entry := logging.Entry{
		// Log anything that can be marshaled to JSON.
		Payload:  err.err,
		Severity: logging.Error,
	}
	if g.resource != nil {
		entry.Resource = g.resource
	}
	if err.trace != nil {
		entry.Trace = *err.trace
	}
	if err.request != nil {
		entry.HTTPRequest = err.request
	}
	g.logger.Log(entry)

	if os.Getenv("GO_ENV") == "production" {
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
