package arena

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/akemon/akemon-relay/internal/relay"
	"github.com/google/uuid"
)

// sendTaskToAgent sends a submit_task MCP call to an agent and returns the text response.
// It reuses the relay's AddPending/Send pattern, bypassing HTTP.
func sendTaskToAgent(agent *relay.ConnectedAgent, task string, timeout time.Duration) (string, time.Duration, error) {
	// Step 1: MCP initialize
	initBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":   map[string]any{},
			"clientInfo":     map[string]any{"name": "akemon-arena", "version": "1.0"},
		},
	})

	initReqID := uuid.New().String()
	ch := agent.AddPending(initReqID)
	err := agent.Send(&relay.RelayMessage{
		Type:      relay.TypeMCPRequest,
		RequestID: initReqID,
		Method:    "POST",
		Headers:   map[string]string{"content-type": "application/json"},
		Body:      initBody,
	})
	if err != nil {
		agent.RemovePending(initReqID)
		return "", 0, fmt.Errorf("send initialize: %w", err)
	}

	var sessionID string
	select {
	case resp := <-ch:
		if resp.Type == relay.TypeMCPError {
			return "", 0, fmt.Errorf("initialize error: %s", resp.Error)
		}
		// Extract mcp-session-id from response headers
		sessionID = resp.ResponseHeaders["mcp-session-id"]
	case <-time.After(timeout):
		agent.RemovePending(initReqID)
		return "", 0, fmt.Errorf("initialize timeout")
	}

	// Step 2: tools/call submit_task
	callBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "submit_task",
			"arguments": map[string]any{"task": task},
		},
	})

	callHeaders := map[string]string{"content-type": "application/json"}
	if sessionID != "" {
		callHeaders["mcp-session-id"] = sessionID
	}

	callReqID := uuid.New().String()
	ch2 := agent.AddPending(callReqID)
	start := time.Now()
	err = agent.Send(&relay.RelayMessage{
		Type:      relay.TypeMCPRequest,
		RequestID: callReqID,
		SessionID: sessionID,
		Method:    "POST",
		Headers:   callHeaders,
		Body:      callBody,
	})
	if err != nil {
		agent.RemovePending(callReqID)
		return "", 0, fmt.Errorf("send submit_task: %w", err)
	}

	var resp *relay.RelayMessage
	select {
	case resp = <-ch2:
	case <-time.After(timeout):
		agent.RemovePending(callReqID)
		return "", time.Since(start), fmt.Errorf("submit_task timeout")
	}
	elapsed := time.Since(start)

	if resp.Type == relay.TypeMCPError {
		return "", elapsed, fmt.Errorf("submit_task error: %s", resp.Error)
	}

	// Parse response body to extract content[].text
	text, err := extractResponseText(resp.Body)
	if err != nil {
		return "", elapsed, fmt.Errorf("parse response: %w", err)
	}
	return text, elapsed, nil
}

// extractResponseText pulls text from MCP tools/call response body.
func extractResponseText(body json.RawMessage) (string, error) {
	var envelope struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return string(body), nil // fallback: return raw
	}
	if envelope.Error != nil {
		return "", fmt.Errorf("mcp error: %s", envelope.Error.Message)
	}
	var text string
	for _, c := range envelope.Result.Content {
		if c.Text != "" {
			if text != "" {
				text += "\n"
			}
			text += c.Text
		}
	}
	return text, nil
}
