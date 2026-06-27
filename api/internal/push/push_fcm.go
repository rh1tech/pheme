package push

import (
	"context"
	"fmt"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// FCMSender delivers notifications to devices that have an FCM registration
// token (Android, iOS via APNs, and Chrome/Firefox web when using FCM).
type FCMSender struct {
	client        *messaging.Client
	publicBaseURL string
}

// NewFCMSender initialises the Firebase Admin messaging client from a
// service-account credentials file. publicBaseURL (may be empty) is the base used
// to build absolute image URLs for notifications.
func NewFCMSender(ctx context.Context, credentialsFile, publicBaseURL string) (*FCMSender, error) {
	app, err := firebase.NewApp(ctx, nil, option.WithCredentialsFile(credentialsFile))
	if err != nil {
		return nil, fmt.Errorf("firebase init: %w", err)
	}
	client, err := app.Messaging(ctx)
	if err != nil {
		return nil, fmt.Errorf("firebase messaging: %w", err)
	}
	return &FCMSender{client: client, publicBaseURL: publicBaseURL}, nil
}

// Send delivers msg to each device with an FCM token using a single batch
// request. Devices without an FCM token are reported as skipped.
func (s *FCMSender) Send(ctx context.Context, msg domain.Message, devices []domain.Device) ([]Result, error) {
	results := make([]Result, 0, len(devices))
	var batch []*messaging.Message
	var batched []string // device IDs aligned with batch order

	img := imageURL(s.publicBaseURL, msg)
	for _, d := range devices {
		if d.FCMToken == "" {
			results = append(results, Result{DeviceID: d.ID, Status: domain.DeliverySkipped})
			continue
		}
		batch = append(batch, &messaging.Message{
			Token:        d.FCMToken,
			Notification: &messaging.Notification{Title: msg.Title, Body: msg.Body, ImageURL: img},
			Data:         notificationData(msg),
		})
		batched = append(batched, d.ID)
	}

	if len(batch) == 0 {
		return results, nil
	}

	resp, err := s.client.SendEach(ctx, batch)
	if err != nil {
		// Whole batch failed; mark every batched device as failed.
		for _, id := range batched {
			results = append(results, Result{DeviceID: id, Status: domain.DeliveryFailed, Error: err.Error()})
		}
		return results, err
	}

	for i, r := range resp.Responses {
		res := Result{DeviceID: batched[i], Status: domain.DeliverySent}
		if !r.Success {
			res.Status = domain.DeliveryFailed
			if r.Error != nil {
				res.Error = r.Error.Error()
			}
		}
		results = append(results, res)
	}
	return results, nil
}

var _ Sender = (*FCMSender)(nil)
