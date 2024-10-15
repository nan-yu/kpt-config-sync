package pubsub

import (
	"context"
	"encoding/json"
	"fmt"

	"cloud.google.com/go/pubsub"
	"k8s.io/klog/v2"
)

type Status string

const (
	ApplySucceeded Status = "applySucceeded"
	ApplyFailed    Status = "applyFailed"

	ReconcileSucceeded Status = "reconcileSucceeded"
	ReconcileFailed    Status = "reconcileFailed"
)

type Message struct {
	ProjectID   string `json:"projectID"`
	ClusterName string `json:"clusterName"`
	NodeName    string `json:"nodeName"`
	Topic       string `json:"topic"`
	RSNamespace string `json:"RSNamespace"`
	RSName      string `json:"RSName"`
	Commit      string `json:"commit,omitempty"`
	Status      Status `json:"status"`
	Error       string `json:"error,omitempty"`
}

// Publish publishes a JSON message to a topic in the provided project
func Publish(projectID, topicID string, msg Message) error {
	// projectID := "my-project-id"
	// topicID := "my-topic"
	// msg := "Hello World"
	ctx := context.Background()
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return fmt.Errorf("pubsub: NewClient: %w", err)
	}
	defer client.Close()

	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encoding message: %v", err)
	}
	t := client.Topic(topicID)
	result := t.Publish(ctx, &pubsub.Message{
		Data: b,
	})
	// Block until the result is returned and a server-generated
	// ID is returned for the published message.
	id, err := result.Get(ctx)
	if err != nil {
		return fmt.Errorf("pubsub: result.Get: %w", err)
	}
	klog.Infof("Published a message; msg ID: %v", id)
	return nil
}
