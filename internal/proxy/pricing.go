package proxy

import (
	"encoding/json"
	"errors"
	"unicode/utf8"
)

const maxOutputTokens = 4096

type TokenQuote struct {
	InputTokens    int    `json:"input_tokens"`
	OutputTokens   int    `json:"output_tokens"`
	TotalTokens    int    `json:"total_tokens"`
	InputAmount    uint64 `json:"input_amount"`
	OutputAmount   uint64 `json:"output_amount"`
	Amount         uint64 `json:"amount"`
	MinimumApplied bool   `json:"minimum_applied"`
}

type chatPricingRequest struct {
	Messages      []json.RawMessage `json:"messages"`
	Tools         json.RawMessage   `json:"tools"`
	MaxTokens     int               `json:"max_tokens"`
	MaxCompletion int               `json:"max_completion_tokens"`
}

// EstimateQuote uses a conservative character-based token estimate. Pricing is
// charged upfront for the input estimate and the full requested output budget.
func EstimateQuote(body []byte, inputRate, outputRate, minimum uint64) (TokenQuote, error) {
	var request chatPricingRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return TokenQuote{}, errors.New("request must be valid chat-completions JSON")
	}
	if len(request.Messages) == 0 {
		return TokenQuote{}, errors.New("request must include at least one message")
	}
	outputTokens := request.MaxTokens
	if request.MaxCompletion > 0 {
		outputTokens = request.MaxCompletion
	}
	if outputTokens <= 0 || outputTokens > maxOutputTokens {
		return TokenQuote{}, errors.New("max_tokens must be between 1 and 4096")
	}

	inputChars := 0
	for _, message := range request.Messages {
		inputChars += utf8.RuneCountInString(string(message))
	}
	inputChars += utf8.RuneCountInString(string(request.Tools))
	inputTokens := (inputChars + 2) / 3 // Conservative approximation; exact tokenizers vary by model.
	if inputTokens < 1 {
		inputTokens = 1
	}

	inputCost, ok := tokenCost(uint64(inputTokens), inputRate)
	if !ok {
		return TokenQuote{}, errors.New("token price is too large")
	}
	outputCost, ok := tokenCost(uint64(outputTokens), outputRate)
	if !ok || ^uint64(0)-inputCost < outputCost {
		return TokenQuote{}, errors.New("token price is too large")
	}
	amount := inputCost + outputCost
	minimumApplied := amount < minimum
	if amount < minimum {
		amount = minimum
	}
	return TokenQuote{InputTokens: inputTokens, OutputTokens: outputTokens, TotalTokens: inputTokens + outputTokens, InputAmount: inputCost, OutputAmount: outputCost, Amount: amount, MinimumApplied: minimumApplied}, nil
}

func tokenCost(tokens, rate uint64) (uint64, bool) {
	if rate == 0 || tokens > (^uint64(0)-999)/rate {
		return 0, false
	}
	return (tokens*rate + 999) / 1000, true
}
