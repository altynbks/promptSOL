package solana

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Validator struct {
	rpcURL string
	client *http.Client
}

func NewValidator(rpcURL string, timeout time.Duration) *Validator {
	return &Validator{rpcURL: rpcURL, client: &http.Client{Timeout: timeout}}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}
type rpcResponse struct {
	Result *struct {
		Meta *struct {
			Err json.RawMessage `json:"err"`
		} `json:"meta"`
		Transaction struct {
			Message struct {
				Instructions []struct {
					Program string `json:"program"`
					Parsed  struct {
						Type string `json:"type"`
						Info struct {
							Destination string `json:"destination"`
							Lamports    uint64 `json:"lamports"`
						} `json:"info"`
					} `json:"parsed"`
				} `json:"instructions"`
			} `json:"message"`
		} `json:"transaction"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// VerifyTransaction accepts a confirmed successful System Program SOL transfer
// to recipient for at least the configured price.
func (v *Validator) VerifyTransaction(signature, recipient string, price uint64) (bool, error) {
	if signature == "" {
		return false, errors.New("empty transaction signature")
	}
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: "getTransaction", Params: []any{signature, map[string]any{"encoding": "jsonParsed", "commitment": "confirmed", "maxSupportedTransactionVersion": 0}}})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, v.rpcURL, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := v.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("Solana RPC request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("Solana RPC returned %s", resp.Status)
	}
	var result rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("decode Solana RPC response: %w", err)
	}
	if result.Error != nil {
		return false, fmt.Errorf("Solana RPC: %s", result.Error.Message)
	}
	if result.Result == nil {
		return false, nil
	} // not yet confirmed/available
	if result.Result.Meta == nil || !isNull(result.Result.Meta.Err) {
		return false, nil
	}
	for _, ix := range result.Result.Transaction.Message.Instructions {
		if ix.Program == "system" && ix.Parsed.Type == "transfer" && ix.Parsed.Info.Destination == recipient && ix.Parsed.Info.Lamports >= price {
			return true, nil
		}
	}
	return false, nil
}

func isNull(raw json.RawMessage) bool {
	return len(raw) == 0 || strings.TrimSpace(string(raw)) == "null"
}
