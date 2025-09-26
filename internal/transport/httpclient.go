package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/sabadia/svc-downloader/internal/models"
)

type HTTPClient struct{ c *http.Client }

func NewHTTPClient(timeout time.Duration) *HTTPClient {
	if timeout == 0 {
		timeout = 30 * time.Minute
	}
	return &HTTPClient{c: &http.Client{Timeout: timeout}}
}

func (h *HTTPClient) Head(ctx context.Context, req models.RequestOptions, cfg models.DownloadConfig) (*models.ResponseMetadata, map[string][]string, error) {
	if cfg.DisableHead {
		return h.headFallback(ctx, req, cfg)
	}
	client := h.clientFor(cfg)
	urls := withMirrors(req)
	for _, u := range urls {
		r, err := http.NewRequestWithContext(ctx, http.MethodHead, addQuery(u, req), nil)
		if err != nil {
			continue
		}
		applyHeaders(r, req, cfg)
		resp, err := client.Do(r)
		if err != nil {
			continue
		}
		// ensure body closed even for HEAD
		if resp.Body != nil {
			resp.Body.Close()
		}
		if resp.StatusCode >= 400 {
			continue
		}
		md := parseMeta(resp)
		return md, resp.Header, nil
	}
	return h.headFallback(ctx, req, cfg)
}

func (h *HTTPClient) GetRange(ctx context.Context, req models.RequestOptions, cfg models.DownloadConfig, startInclusive int64, endInclusive int64) (io.ReadCloser, *models.ResponseMetadata, map[string][]string, int, error) {
	client := h.clientFor(cfg)
	urls := withMirrors(req)
	var lastErr error
	var lastStatus int
	for _, u := range urls {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, addQuery(u, req), nil)
		if err != nil {
			lastErr = err
			continue
		}
		rangeVal := "bytes=" + strconv.FormatInt(startInclusive, 10) + "-"
		if endInclusive >= 0 {
			rangeVal += strconv.FormatInt(endInclusive, 10)
		}
		r.Header.Set("Range", rangeVal)
		applyHeaders(r, req, cfg)
		resp, err := client.Do(r)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 400 {
			lastStatus = resp.StatusCode
			if resp.Body != nil {
				resp.Body.Close()
			}
			lastErr = errors.New("get range failed: " + resp.Status)
			continue
		}
		md := parseMeta(resp)
		return resp.Body, md, resp.Header, resp.StatusCode, nil
	}
	return nil, nil, nil, lastStatus, lastErr
}

func (h *HTTPClient) headFallback(ctx context.Context, req models.RequestOptions, cfg models.DownloadConfig) (*models.ResponseMetadata, map[string][]string, error) {
	client := h.clientFor(cfg)
	urls := withMirrors(req)
	var lastErr error
	for _, u := range urls {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, addQuery(u, req), nil)
		if err != nil {
			lastErr = err
			continue
		}
		r.Header.Set("Range", "bytes=0-")
		applyHeaders(r, req, cfg)
		resp, err := client.Do(r)
		if err != nil {
			lastErr = err
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			lastErr = errors.New("head fallback failed: " + resp.Status)
			continue
		}
		md := parseMeta(resp)
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if i := lastSlash(cr); i >= 0 {
				if n, err := strconv.ParseInt(cr[i+1:], 10, 64); err == nil {
					md.ContentLength = n
				}
			}
		}
		return md, resp.Header, nil
	}
	return nil, nil, lastErr
}

func (h *HTTPClient) clientFor(cfg models.DownloadConfig) *http.Client {
	tr := &http.Transport{}
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: cfg.AllowInsecureTLS}
	if cfg.TLS != nil {
		tr.TLSClientConfig.InsecureSkipVerify = cfg.TLS.InsecureSkipVerify || tr.TLSClientConfig.InsecureSkipVerify
		tr.TLSClientConfig.ServerName = cfg.TLS.ServerName
		if cfg.TLS.MinVersion != 0 {
			tr.TLSClientConfig.MinVersion = cfg.TLS.MinVersion
		}
		if cfg.TLS.MaxVersion != 0 {
			tr.TLSClientConfig.MaxVersion = cfg.TLS.MaxVersion
		}
	}
	if cfg.Proxy != nil && cfg.Proxy.IP != "" && cfg.Proxy.Port != 0 {
		u := &url.URL{Scheme: "http", Host: cfg.Proxy.IP + ":" + strconv.Itoa(cfg.Proxy.Port)}
		if cfg.Proxy.Username != "" {
			u.User = url.UserPassword(cfg.Proxy.Username, cfg.Proxy.Password)
		}
		tr.Proxy = http.ProxyURL(u)
	}
	client := &http.Client{Transport: tr}
	follow := cfg.FollowRedirects
	limit := cfg.RedirectsLimit
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !follow {
			return http.ErrUseLastResponse
		}
		if limit > 0 && len(via) >= limit {
			return errors.New("stopped after too many redirects")
		}
		return nil
	}
	if cfg.Timeout > 0 {
		client.Timeout = cfg.Timeout
	}
	return client
}

func parseMeta(resp *http.Response) *models.ResponseMetadata {
	md := &models.ResponseMetadata{
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		AcceptRanges: resp.Header.Get("Accept-Ranges") == "bytes" || resp.Header.Get("Content-Range") != "",
		ContentType:  resp.Header.Get("Content-Type"),
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
			md.ContentLength = n
		}
	}
	return md
}

func applyHeaders(r *http.Request, req models.RequestOptions, cfg models.DownloadConfig) {
	if r.Header.Get("User-Agent") == "" {
		r.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36")
	}
	if r.Header.Get("Accept") == "" {
		r.Header.Set("Accept", "*/*")
	}
	if r.Header.Get("Accept-Language") == "" {
		r.Header.Set("Accept-Language", "en-GB,en-US;q=0.9,en;q=0.8,bn;q=0.7")
	}
	if r.Header.Get("Connection") == "" {
		r.Header.Set("Connection", "keep-alive")
	}
	if r.Header.Get("DNT") == "" {
		r.Header.Set("DNT", "1")
	}
	if r.Header.Get("sec-ch-ua") == "" {
		r.Header.Set("sec-ch-ua", `"Chromium";v="140", "Not=A?Brand";v="24", "Google Chrome";v="140"`)
	}
	for k, v := range cfg.Headers {
		r.Header.Set(k, v)
	}
	for k, v := range cfg.Cookies {
		r.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	if req.Extra != nil {
		for k, v := range req.Extra.Headers {
			r.Header.Set(k, v)
		}
		for k, v := range req.Extra.Cookies {
			r.AddCookie(&http.Cookie{Name: k, Value: v})
		}
		if r.Header.Get("Referer") == "" && req.URL != "" {
			if u, _ := url.Parse(req.URL); u != nil {
				r.Header.Set("Referer", u.Scheme+"://"+u.Host)
			}
		}
	}
	if cfg.Auth != nil {
		if cfg.Auth.BearerToken != "" {
			r.Header.Set("Authorization", "Bearer "+cfg.Auth.BearerToken)
		} else if cfg.Auth.Username != "" {
			r.SetBasicAuth(cfg.Auth.Username, cfg.Auth.Password)
		}
	}
}

func addQuery(u string, req models.RequestOptions) string {
	if req.Extra == nil || len(req.Extra.QueryParams) == 0 {
		return u
	}
	u2, err := url.Parse(u)
	if err != nil {
		return u
	}
	q := u2.Query()
	for k, v := range req.Extra.QueryParams {
		q.Set(k, v)
	}
	u2.RawQuery = q.Encode()
	return u2.String()
}

func withMirrors(req models.RequestOptions) []string {
	urls := []string{req.URL}
	for _, m := range req.MirrorURLs {
		if m != "" {
			urls = append(urls, m)
		}
	}
	return urls
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}
