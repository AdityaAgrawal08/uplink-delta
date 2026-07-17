package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type HeartbeatPayload struct {
	PeerID string   `json:"peerId,omitempty"`
	Addrs  []string `json:"addrs,omitempty"`
}

func StartHeartbeat(ctx context.Context, sessionId, username, server string, peerId string, addrs []string, onStrikeError func(error)) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	strikes := 0

	// Fire immediately on connect
	err := sendHeartbeat(ctx, sessionId, username, server, peerId, addrs)
	if err != nil {
		strikes++
		if strikes >= 3 && onStrikeError != nil {
			onStrikeError(err)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := sendHeartbeat(ctx, sessionId, username, server, peerId, addrs)
			if err != nil {
				strikes++
				if strikes >= 3 && onStrikeError != nil {
					onStrikeError(err)
				}
			} else {
				strikes = 0 // Reset strikes on success
			}
		}
	}
}

func sendHeartbeat(ctx context.Context, sessionId, username, server string, peerId string, addrs []string) error {
	url := fmt.Sprintf("%s/api/v1/session/%s/heartbeat", server, sessionId)
	
	payload := HeartbeatPayload{
		PeerID: peerId,
		Addrs:  addrs,
	}
	
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return err
	}
	
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Uplink-Username", username)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("heartbeat failed with status %d", resp.StatusCode)
	}

	return nil
}
