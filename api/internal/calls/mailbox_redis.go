package calls

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Redis is the production Mailbox. It exists so that two App API instances agree about a
// call: the browser that placed it and the browser that answered may well be talking to
// different processes, and they must not each believe they won the race to answer.
//
// Everything it stores expires. A call leaves no trace.
type Redis struct {
	client *redis.Client
	prefix string
}

func NewRedis(client *redis.Client, prefix string) *Redis {
	if prefix == "" {
		prefix = "pheme:call"
	}
	return &Redis{client: client, prefix: prefix}
}

func (r *Redis) signalsKey(callID string) string { return fmt.Sprintf("%s:%s:sig", r.prefix, callID) }
func (r *Redis) winnerKey(callID string) string  { return fmt.Sprintf("%s:%s:win", r.prefix, callID) }

// appendSignal pushes a signal and refreshes the call's expiry in one round trip. The
// RPUSH's return value is the new list length, which IS the sequence number — so the
// sequence is assigned by Redis and two concurrent signals can never collide on one.
var appendSignal = redis.NewScript(`
local seq = redis.call('RPUSH', KEYS[1], ARGV[1])
redis.call('EXPIRE', KEYS[1], ARGV[2])
return seq
`)

func (r *Redis) Append(ctx context.Context, callID string, ciphertext []byte) (Signal, error) {
	seq, err := appendSignal.Run(
		ctx, r.client,
		[]string{r.signalsKey(callID)},
		ciphertext, int(TTL.Seconds()),
	).Int()
	if err != nil {
		return Signal{}, err
	}
	return Signal{Seq: seq, Ciphertext: ciphertext}, nil
}

func (r *Redis) Since(ctx context.Context, callID string, seq int) ([]Signal, error) {
	if seq < 0 {
		seq = 0
	}
	// Sequence numbers are 1-based, so everything after `seq` starts at index `seq`.
	blobs, err := r.client.LRange(ctx, r.signalsKey(callID), int64(seq), -1).Result()
	if err != nil {
		return nil, err
	}
	out := make([]Signal, 0, len(blobs))
	for i, b := range blobs {
		out = append(out, Signal{Seq: seq + i + 1, Ciphertext: []byte(b)})
	}
	return out, nil
}

// Claim is a SETNX with an expiry: the first device to ask becomes the winner, and every
// later ask is told who that was. Atomic on the server, so of two devices answering in the
// same instant exactly one is told it won — which is the point, because the loser has a
// microphone open and needs a definite answer rather than a hopeful broadcast.
func (r *Redis) Claim(ctx context.Context, callID, deviceID string) (string, bool, error) {
	key := r.winnerKey(callID)
	won, err := r.client.SetNX(ctx, key, deviceID, TTL).Result()
	if err != nil {
		return "", false, err
	}
	if won {
		return deviceID, true, nil
	}
	winner, err := r.client.Get(ctx, key).Result()
	if err != nil {
		// The lock expired between the SETNX and the GET. Whoever is asking is not the
		// winner as far as this process can tell, and saying so is the safe answer: the
		// worst case is a device that stops ringing a moment early.
		if err == redis.Nil {
			return "", false, nil
		}
		return "", false, err
	}
	// Claiming twice from the same device is not losing a race against yourself.
	return winner, winner == deviceID, nil
}
