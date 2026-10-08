package picollector

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Drain never opens the adapter, so retained deliveries can finish after a reader handoff.
func Drain(ctx context.Context, c Config) error {
	id, err := ReadIdentity(c.StateDirectory)
	if err != nil {
		return err
	}
	legacyID := ""
	if c.Legacy != nil {
		legacyID = c.Legacy.DeviceID
	}
	q, err := OpenQueue(c.StateDirectory, id.DeviceID, c.QueueLimit, legacyID)
	if err != nil {
		return errors.New("stop collector before draining its queue")
	}
	defer q.Close()
	client, err := NewClient(c.Server, id.Token, c.AllowLoopbackHTTP)
	if err != nil {
		return err
	}
	legacy, err := NewLegacyClient(c)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	state := &runtimeState{}
	wg.Add(1)
	go func() { defer wg.Done(); upload(ctx, q, client, state) }()
	if legacy != nil {
		wg.Add(1)
		go func() { defer wg.Done(); uploadLegacy(ctx, q, legacy) }()
	}
	defer func() { cancel(); wg.Wait() }()
	for ctx.Err() == nil {
		n, _, err := q.Status()
		if err != nil {
			return errors.New("queue read failed")
		}
		if n == 0 {
			return nil
		}
		if _, paused := state.state(); paused {
			return errors.New("device authorization invalid; pending deliveries preserved")
		}
		if !wait(ctx, time.Second) {
			break
		}
	}
	return ctx.Err()
}
