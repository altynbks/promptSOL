package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

func NewOpenAIProxy(targetURL, apiKey, fallbackURL, fallbackKey, fallbackModel string) (*httputil.ReverseProxy, error) {
	target, err := url.Parse(targetURL)
	if err != nil {
		return nil, err
	}
	if target.Scheme == "" || target.Host == "" {
		return nil, &url.Error{Op: "parse", URL: targetURL, Err: errInvalidTarget{}}
	}
	p := httputil.NewSingleHostReverseProxy(target)
	transport := retryTransport{base: http.DefaultTransport, maxRetries: 2, primaryPathPrefix: target.Path}
	if fallbackKey != "" {
		fallback, err := url.Parse(fallbackURL)
		if err != nil {
			return nil, err
		}
		if fallback.Scheme == "" || fallback.Host == "" {
			return nil, &url.Error{Op: "parse", URL: fallbackURL, Err: errInvalidTarget{}}
		}
		transport.fallbackURL = fallback
		transport.fallbackKey = fallbackKey
		transport.fallbackModel = fallbackModel
	}
	p.Transport = transport
	original := p.Director
	p.Director = func(req *http.Request) {
		original(req)
		req.Host = target.Host
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Del("X-Payment-Signature")
	}
	p.ModifyResponse = func(resp *http.Response) error {
		if resp.Header.Get("X-AI-Provider") == "" {
			resp.Header.Set("X-AI-Provider", "Gemini")
		}
		if resp.StatusCode >= http.StatusBadRequest {
			log.Printf("AI upstream returned status=%d", resp.StatusCode)
		}
		// Keep the upstream's SSE content type and prevent intermediaries buffering it.
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			resp.Header.Set("Cache-Control", "no-cache")
		}
		return nil
	}
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("AI upstream request failed: %v", err)
		http.Error(w, "AI provider could not be reached. Please retry; your payment remains available.", http.StatusBadGateway)
	}
	return p, nil
}

type retryTransport struct {
	base              http.RoundTripper
	maxRetries        int
	primaryPathPrefix string
	fallbackURL       *url.URL
	fallbackKey       string
	fallbackModel     string
}

func (t retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		body := req.Body
		if req.GetBody != nil {
			body, err = req.GetBody()
			if err != nil {
				return nil, err
			}
		}
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			return nil, err
		}
		_ = body.Close()
		if req.GetBody == nil {
			_ = req.Body.Close()
		}
	}

	var resp *http.Response
	for attempt := 0; ; attempt++ {
		attemptReq := req.Clone(req.Context())
		if req.Body != nil {
			attemptReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		var err error
		resp, err = t.base.RoundTrip(attemptReq)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusServiceUnavailable && resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}
		if attempt >= t.maxRetries {
			break
		}

		log.Printf("retrying AI upstream after status=%d attempt=%d", resp.StatusCode, attempt+1)
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		delay := time.Duration(500*(1<<attempt)) * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, context.Cause(req.Context())
		case <-timer.C:
		}
	}

	if t.fallbackURL == nil {
		return resp, nil
	}
	log.Printf("primary AI provider unavailable (status=%d); falling back to Groq model %s", resp.StatusCode, t.fallbackModel)
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()

	fallbackBody, err := replaceModel(bodyBytes, t.fallbackModel)
	if err != nil {
		return nil, fmt.Errorf("prepare Groq fallback request: %w", err)
	}
	fallbackReq := req.Clone(req.Context())
	fallbackReq.URL.Scheme = t.fallbackURL.Scheme
	fallbackReq.URL.Host = t.fallbackURL.Host
	routePath := strings.TrimPrefix(req.URL.Path, t.primaryPathPrefix)
	if !strings.HasPrefix(routePath, "/") {
		routePath = "/" + routePath
	}
	fallbackReq.URL.Path = strings.TrimSuffix(t.fallbackURL.Path, "/") + routePath
	fallbackReq.URL.RawPath = ""
	fallbackReq.Host = t.fallbackURL.Host
	fallbackReq.Header = req.Header.Clone()
	fallbackReq.Header.Set("Authorization", "Bearer "+t.fallbackKey)
	fallbackReq.Header.Del("Content-Length")
	fallbackReq.ContentLength = int64(len(fallbackBody))
	fallbackReq.Body = io.NopCloser(bytes.NewReader(fallbackBody))
	fallbackReq.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(fallbackBody)), nil
	}
	fallbackResp, err := t.base.RoundTrip(fallbackReq)
	if err != nil {
		return nil, fmt.Errorf("Groq fallback request: %w", err)
	}
	fallbackResp.Header.Set("X-AI-Provider", "Groq")
	fallbackResp.Header.Set("X-AI-Model", t.fallbackModel)
	return fallbackResp, nil
}

func replaceModel(body []byte, model string) ([]byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	encodedModel, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	payload["model"] = encodedModel
	return json.Marshal(payload)
}

type errInvalidTarget struct{}

func (errInvalidTarget) Error() string { return "target URL must include scheme and host" }
