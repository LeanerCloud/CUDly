package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

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

// handleLambdaHTTPEvent processes HTTP requests from Lambda Function URL
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

	return app.API.HandleRequest(ctx, &request)
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
