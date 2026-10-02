package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

func NewOpenAIProxy(targetURL, apiKey string) (*httputil.ReverseProxy, error) {
	target, err := url.Parse(targetURL)
	if err != nil {
		return nil, err
	}
	if target.Scheme == "" || target.Host == "" {
		return nil, &url.Error{Op: "parse", URL: targetURL, Err: errInvalidTarget{}}
	}
	p := httputil.NewSingleHostReverseProxy(target)
	original := p.Director
	p.Director = func(req *http.Request) {
		original(req)
		req.Host = target.Host
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Del("X-Payment-Signature")
	}
	p.ModifyResponse = func(resp *http.Response) error {
		// Keep the upstream's SSE content type and prevent intermediaries buffering it.
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			resp.Header.Set("Cache-Control", "no-cache")
		}
		return nil
	}
	return p, nil
}

type errInvalidTarget struct{}

func (errInvalidTarget) Error() string { return "target URL must include scheme and host" }
