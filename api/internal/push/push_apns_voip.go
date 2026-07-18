package push

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/token"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// APNsVoIPSender rings an iPhone that is asleep.
//
// It exists because FCM cannot do this, and cannot be made to. A ringing call on iOS means PushKit,
// and PushKit means three things FCM does not have:
//
//   - a different TOKEN. The PushKit token comes from PKPushRegistry and is not the token an FCM
//     registration wraps. FCM has no API to be handed one.
//   - a different TOPIC. A VoIP push goes to `<bundleID>.voip`. FCM sets apns-topic to the bundle id
//     and does not expose it.
//   - a different PUSH TYPE. `apns-push-type: voip` is not part of FCM's contract, and never has been.
//
// So iOS calls get their own connection straight to Apple, and FCM keeps everything else: Android,
// web push, and iOS message notifications. The two live side by side rather than one replacing the
// other — see MultiSender.
//
// The one rule that governs the client side of this: iOS requires that EVERY VoIP push report an
// incoming call to CallKit, immediately and synchronously. A push that does not is a push that gets
// the app killed, and then gets VoIP delivery revoked for it entirely. That is why a cancelled call
// is still sent as a VoIP push rather than silently dropped — the client reports it and then ends it,
// which is the sanctioned way to take a ring back.
type APNsVoIPSender struct {
	client   *apns2.Client
	bundleID string
}

// APNsConfig is what a token-authenticated APNs connection needs. All four are required; a VoIP push
// cannot be sent without them.
type APNsConfig struct {
	// KeyFile is the path to the .p8 signing key downloaded from the Apple Developer portal.
	KeyFile string
	KeyID   string
	TeamID  string
	// BundleID is the app's bundle identifier. The VoIP topic is this plus ".voip".
	BundleID string
	// Production selects Apple's production gateway rather than the sandbox. A token minted by a debug
	// build is only valid against the sandbox, and vice versa: get this wrong and every push comes back
	// BadDeviceToken.
	Production bool
}

// NewAPNsVoIPSender opens a token-authenticated connection to APNs.
//
// Token auth rather than a certificate: the JWT is signed with an ES256 key that does not expire, so
// there is no annual certificate rotation to forget, and one key covers every app on the team.
func NewAPNsVoIPSender(cfg APNsConfig) (*APNsVoIPSender, error) {
	if cfg.KeyFile == "" || cfg.KeyID == "" || cfg.TeamID == "" || cfg.BundleID == "" {
		return nil, fmt.Errorf("apns: key file, key id, team id and bundle id are all required")
	}

	pem, err := os.ReadFile(cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("apns: read key: %w", err)
	}
	key, err := token.AuthKeyFromBytes(pem)
	if err != nil {
		return nil, fmt.Errorf("apns: parse key: %w", err)
	}

	client := apns2.NewTokenClient(&token.Token{
		AuthKey: key,
		KeyID:   cfg.KeyID,
		TeamID:  cfg.TeamID,
	})
	if cfg.Production {
		client = client.Production()
	} else {
		client = client.Development()
	}

	return &APNsVoIPSender{client: client, bundleID: cfg.BundleID}, nil
}

// voipPayload is what the device gets. Deliberately small: it says WHO is calling and WHICH call, and
// nothing else. The SDP is not here and could not be — it is sealed to the conversation's MLS group,
// which the server does not have the key to. The client fetches it from the call's mailbox once it
// has reported the call to CallKit.
type voipPayload struct {
	CallID         string `json:"callId"`
	ConversationID string `json:"conversationId"`
	CallerName     string `json:"callerName"`
	// Kind is "call" or "call-cancel". A cancel is still a VoIP push, and the client still has to
	// report it to CallKit before ending it — iOS gives no exemption for "this one does not need to
	// ring".
	Kind string `json:"kind"`
}

// voipPayloadFor builds the body of a VoIP push.
//
// Split out from SendCall so the privacy rule below can be tested without an APNs connection —
// it was untestable, and it was wrong, and those two facts were not a coincidence.
func voipPayloadFor(n ChatNotification) voipPayload {
	return voipPayload{
		CallID:         n.CallID,
		ConversationID: n.ConversationID,
		// Via displayName, never SenderName. This is a CallKit screen: it takes over the whole
		// device, ahead of the lock screen, and announcing the caller there is the loudest thing
		// this app can do. A recipient who asked not to be told who is messaging them has, if
		// anything, asked harder not to be told this. They get the real name once they answer.
		CallerName: n.displayName(),
		Kind:       string(n.Kind),
	}
}

// SendCall delivers a VoIP push to every device that has a PushKit token.
//
// Devices without one are SKIPPED, not failed: an Android phone or a browser has no PushKit token and
// was never supposed to get this. They are reached by the FCM sender instead.
func (s *APNsVoIPSender) SendCall(
	ctx context.Context,
	n ChatNotification,
	devices []domain.Device,
) ([]Result, error) {
	payload, err := json.Marshal(voipPayloadFor(n))
	if err != nil {
		return nil, fmt.Errorf("apns: marshal payload: %w", err)
	}

	results := make([]Result, 0, len(devices))
	var firstErr error

	for _, d := range devices {
		if d.VoIPToken == "" {
			results = append(results, Result{DeviceID: d.ID, Status: domain.DeliverySkipped})
			continue
		}

		res, err := s.client.PushWithContext(ctx, &apns2.Notification{
			DeviceToken: d.VoIPToken,
			Topic:       s.bundleID + ".voip",
			PushType:    apns2.PushTypeVOIP,
			Priority:    apns2.PriorityHigh,
			// A call is worthless once it has stopped ringing. Apple should stop trying rather than
			// deliver it to a phone that then shows an incoming call nobody is on.
			Expiration: time.Now().Add(time.Duration(n.ttl()) * time.Second),
			Payload:    payload,
		})

		switch {
		case err != nil:
			results = append(results, Result{
				DeviceID: d.ID,
				Status:   domain.DeliveryFailed,
				Error:    err.Error(),
			})
			if firstErr == nil {
				firstErr = err
			}
		case !res.Sent():
			results = append(results, Result{
				DeviceID: d.ID,
				Status:   domain.DeliveryFailed,
				Error:    fmt.Sprintf("apns %d: %s", res.StatusCode, res.Reason),
			})
		default:
			results = append(results, Result{DeviceID: d.ID, Status: domain.DeliverySent})
		}
	}

	return results, firstErr
}
