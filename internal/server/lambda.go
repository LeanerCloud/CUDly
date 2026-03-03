package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

// StartLambdaHandler starts the AWS Lambda handler
func StartLambdaHandler(app *Application) {
	log.Println("Starting Lambda handler mode...")
	lambda.Start(func(ctx context.Context, rawEvent json.RawMessage) (any, error) {
		return app.HandleLambdaEvent(ctx, rawEvent)
	})
}

// HandleLambdaEvent processes any Lambda event type
func (app *Application) HandleLambdaEvent(ctx context.Context, rawEvent json.RawMessage) (any, error) {
	// Ensure database connection is established (lazy initialization)
	// Safe to call on every request - mutex guards connection and allows retry on transient failures
	if err := app.ensureDB(ctx); err != nil {
		log.Printf("Failed to establish database connection: %v", err)
		return nil, fmt.Errorf("database connection failed: %w", err)
	}

	eventType := detectLambdaEventType(rawEvent)
	log.Printf("Received %s event (size: %d bytes)", eventType, len(rawEvent))

	switch eventType {
	case "http":
		return app.handleLambdaHTTPEvent(ctx, rawEvent)
	case "sqs":
		return app.handleLambdaSQSEvent(ctx, rawEvent)
	case "scheduled":
		return app.handleLambdaScheduledEvent(ctx, rawEvent)
	default:
		log.Printf("Unknown event type, treating as scheduled event")
		return app.handleLambdaScheduledEvent(ctx, rawEvent)
	}
}

// detectLambdaEventType determines the type of Lambda event
func detectLambdaEventType(rawEvent json.RawMessage) string {
	// Check for Lambda Function URL / API Gateway event
	var httpEvent struct {
		RequestContext struct {
			HTTP struct {
				Method string `json:"method"`
			} `json:"http"`
		} `json:"requestContext"`
		HTTPMethod string `json:"httpMethod"`
	}
	if err := json.Unmarshal(rawEvent, &httpEvent); err == nil {
		if httpEvent.RequestContext.HTTP.Method != "" || httpEvent.HTTPMethod != "" {
			return "http"
		}
	}

	// Check for SQS event
	var sqsEvent struct {
		Records []struct {
			EventSource string `json:"eventSource"`
		} `json:"Records"`
	}
	if err := json.Unmarshal(rawEvent, &sqsEvent); err == nil {
		if len(sqsEvent.Records) > 0 && sqsEvent.Records[0].EventSource == "aws:sqs" {
			return "sqs"
		}
	}

	// Check for EventBridge scheduled event
	var scheduledEvent struct {
		Source     string `json:"source"`
		DetailType string `json:"detail-type"`
		Action     string `json:"action"`
	}
	if err := json.Unmarshal(rawEvent, &scheduledEvent); err == nil {
		if scheduledEvent.Source == "aws.events" || scheduledEvent.Action != "" {
			return "scheduled"
		}
	}

	return "unknown"
}

// handleLambdaHTTPEvent processes HTTP requests from Lambda Function URL.
// When STATIC_DIR is set, serves static files for non-API paths directly
// from the container filesystem, enabling Lambda Function URL to serve
// the full frontend without S3/CloudFront.
func (app *Application) handleLambdaHTTPEvent(ctx context.Context, rawEvent json.RawMessage) (*events.LambdaFunctionURLResponse, error) {
	var request events.LambdaFunctionURLRequest
	if err := json.Unmarshal(rawEvent, &request); err != nil {
		log.Printf("Failed to parse HTTP event: %v", err)
		return &events.LambdaFunctionURLResponse{
			StatusCode: 400,
			Body:       `{"error": "Invalid request format"}`,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
		}, nil
	}

	// Serve static files for non-API paths when STATIC_DIR is configured
	reqPath := request.RawPath
	if app.staticDir != "" && isStaticPath(reqPath) {
		return app.serveLambdaStatic(reqPath)
	}

	return app.API.HandleRequest(ctx, &request)
}

// serveLambdaStatic serves a static file as a Lambda Function URL response.
func (app *Application) serveLambdaStatic(urlPath string) (*events.LambdaFunctionURLResponse, error) {
	content, contentType, cacheControl, found := serveStaticForLambda(app.staticDir, urlPath)
	if !found {
		return &events.LambdaFunctionURLResponse{
			StatusCode: 404,
			Body:       "Not Found",
			Headers:    map[string]string{"Content-Type": "text/plain"},
		}, nil
	}

	// Text-based content types can be sent as plain body
	if isTextContentType(contentType) {
		return &events.LambdaFunctionURLResponse{
			StatusCode:      200,
			Body:            string(content),
			IsBase64Encoded: false,
			Headers: map[string]string{
				"Content-Type":  contentType,
				"Cache-Control": cacheControl,
			},
		}, nil
	}

	// Binary content must be base64-encoded
	return &events.LambdaFunctionURLResponse{
		StatusCode:      200,
		Body:            base64.StdEncoding.EncodeToString(content),
		IsBase64Encoded: true,
		Headers: map[string]string{
			"Content-Type":  contentType,
			"Cache-Control": cacheControl,
		},
	}, nil
}

// isTextContentType returns true for content types that can be sent as plain text
// in Lambda Function URL responses (not base64-encoded).
func isTextContentType(ct string) bool {
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	textTypes := []string{
		"application/json",
		"application/javascript",
		"application/xml",
		"image/svg+xml",
	}
	for _, t := range textTypes {
		if strings.HasPrefix(ct, t) {
			return true
		}
	}
	return false
}

// handleLambdaSQSEvent processes SQS messages (for async purchase processing)
func (app *Application) handleLambdaSQSEvent(ctx context.Context, rawEvent json.RawMessage) (any, error) {
	var sqsEvent events.SQSEvent
	if err := json.Unmarshal(rawEvent, &sqsEvent); err != nil {
		log.Printf("Failed to parse SQS event: %v", err)
		return nil, err
	}

	var failures []string
	for _, record := range sqsEvent.Records {
		log.Printf("Processing SQS message: %s", record.MessageId)
		if err := app.HandleSQSMessage(ctx, record.Body); err != nil {
			log.Printf("Failed to process message %s: %v", record.MessageId, err)
			failures = append(failures, record.MessageId)
		}
	}

	if len(failures) > 0 {
		return nil, fmt.Errorf("failed to process %d SQS message(s): %v", len(failures), failures)
	}

	return map[string]string{"status": "processed"}, nil
}

// handleLambdaScheduledEvent processes scheduled/cron events
func (app *Application) handleLambdaScheduledEvent(ctx context.Context, rawEvent json.RawMessage) (any, error) {
	taskType, err := ParseScheduledEvent(rawEvent)
	if err != nil {
		return nil, fmt.Errorf("failed to parse scheduled event: %w", err)
	}

	return app.HandleScheduledTask(ctx, taskType)
}
