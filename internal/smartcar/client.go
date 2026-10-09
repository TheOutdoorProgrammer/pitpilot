package smartcar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type providerError struct {
	Code    string
	RetryAt time.Time
	AppWide bool
}

func (e *providerError) Error() string { return "smartcar " + e.Code }

type client struct {
	clientID, secret, tokenURL, apiURL string
	http                               *http.Client
	mu                                 sync.Mutex
	token                              string
	expires                            time.Time
}

func newClient(id, secret string) *client {
	return &client{clientID: id, secret: secret, tokenURL: "https://iam.smartcar.com/oauth2/token", apiURL: "https://vehicle.api.smartcar.com/v3", http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *client) send(ctx context.Context, op, method, address string, body []byte, token, userID string) (data []byte, status int, header http.Header, err error) {
	ctx, span := otel.Tracer("pitpilot/smartcar").Start(ctx, "smartcar."+op, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attribute.String("server.address", "smartcar.com")))
	defer span.End()
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, "provider request failed")
		}
	}()
	req, e := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if e != nil {
		return nil, 0, nil, &providerError{Code: "invalid_request"}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if userID != "" {
		req.Header.Set("sc-user-id", userID)
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	resp, e := c.http.Do(req)
	if e != nil {
		return nil, 0, nil, &providerError{Code: "temporarily_unavailable"}
	}
	defer resp.Body.Close()
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	data, e = io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
	if e != nil || len(data) > 4<<20 {
		return nil, resp.StatusCode, nil, &providerError{Code: "invalid_response"}
	}
	if resp.StatusCode >= 400 {
		span.SetStatus(codes.Error, "provider rejected request")
	}
	return data, resp.StatusCode, resp.Header, nil
}

func (c *client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Add(time.Minute).Before(c.expires) {
		return c.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {c.clientID}, "client_secret": {c.secret}}
	raw, status, h, err := c.send(ctx, "token", "POST", c.tokenURL, []byte(form.Encode()), "", "")
	if err != nil {
		return "", err
	}
	if status != 200 {
		return "", classify(status, h, raw, true)
	}
	var result struct {
		Token   string `json:"access_token"`
		Expires int    `json:"expires_in"`
		Type    string `json:"token_type"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Token == "" || len(result.Token) > 16384 || strings.ContainsAny(result.Token, " \t\r\n") || result.Expires < 120 || result.Expires > 86400 || !strings.EqualFold(result.Type, "bearer") {
		return "", &providerError{Code: "invalid_response"}
	}
	c.token = result.Token
	c.expires = time.Now().Add(time.Duration(result.Expires) * time.Second)
	return c.token, nil
}

func (c *client) get(ctx context.Context, op, path, user string, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.accessToken(ctx)
		if err != nil {
			return err
		}
		raw, status, h, err := c.send(ctx, op, "GET", c.apiURL+path, nil, token, user)
		if err != nil {
			return err
		}
		if status == 401 && attempt == 0 {
			c.mu.Lock()
			if c.token == token {
				c.token = ""
			}
			c.mu.Unlock()
			continue
		}
		if status != 200 {
			return classify(status, h, raw, false)
		}
		if json.Unmarshal(raw, out) != nil {
			return &providerError{Code: "invalid_response"}
		}
		return nil
	}
	return &providerError{Code: "application_authentication"}
}

func classify(status int, h http.Header, raw []byte, token bool) error {
	e := &providerError{Code: "temporarily_unavailable"}
	var body struct {
		Errors []struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(raw, &body)
	if status == 429 {
		e.Code = "rate_limited"
		e.RetryAt = time.Now().Add(5 * time.Minute)
		e.AppWide = token
		if n, err := strconv.Atoi(h.Get("Retry-After")); err == nil && n >= 0 && n <= 7*24*3600 {
			e.RetryAt = time.Now().Add(time.Duration(n) * time.Second)
		} else if t, err := http.ParseTime(h.Get("Retry-After")); err == nil && t.After(time.Now()) {
			e.RetryAt = t
		}
		for _, p := range body.Errors {
			if p.Code == "SMARTCAR_API" {
				e.AppWide = true
			}
		}
		return e
	}
	if status == 401 || token && (status == 400 || status == 403) {
		e.Code = "application_authentication"
		return e
	}
	if status == 404 {
		e.Code = "provisioning"
		return e
	}
	for _, p := range body.Errors {
		if p.Code == "AUTHENTICATION_FAILED" || p.Code == "PERMISSION_DENIED" || p.Code == "NO_VEHICLES" || p.Code == "ACCOUNT_LOCKED" {
			e.Code = "reconnect_required"
			return e
		}
	}
	if status == 403 {
		e.Code = "permission_denied"
	}
	return e
}

type remoteConnection struct {
	ID         string `json:"id"`
	Attributes struct {
		Permissions []string `json:"permissions"`
		Vehicle     struct {
			Make  string `json:"make"`
			Model string `json:"model"`
			Year  int    `json:"year"`
			Mode  string `json:"mode"`
		} `json:"vehicle"`
		User struct {
			ID         string `json:"id"`
			ExternalID string `json:"externalId"`
		} `json:"user"`
	} `json:"attributes"`
	Relationships struct {
		Vehicle struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		} `json:"vehicle"`
	} `json:"relationships"`
}

func (c *client) connections(ctx context.Context, user, external, mode string) ([]remoteConnection, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out := []remoteConnection{}
	seen := map[string]bool{}
	for page := 1; page <= 20; page++ {
		q := url.Values{"page[number]": {strconv.Itoa(page)}, "page[size]": {"50"}, "filter[vehicle.mode]": {mode}}
		if user != "" {
			q.Set("filter[userId]", user)
		}
		if external != "" {
			q.Set("filter[user.externalId]", external)
		}
		var response struct {
			Data []remoteConnection `json:"data"`
			Meta struct {
				Total int `json:"totalCount"`
				Page  int `json:"pageNumber"`
			} `json:"meta"`
		}
		if err := c.get(ctx, "connections", "/connections?"+q.Encode(), "", &response); err != nil {
			return nil, err
		}
		if response.Meta.Total < 0 || response.Meta.Total > 1000 || response.Meta.Page != page {
			return nil, &providerError{Code: "invalid_response"}
		}
		for _, v := range response.Data {
			if !validProviderID(v.ID) || !validProviderID(v.Attributes.User.ID) || !validProviderID(v.Relationships.Vehicle.Data.ID) || v.Attributes.Vehicle.Mode != mode || user != "" && v.Attributes.User.ID != user || external != "" && v.Attributes.User.ExternalID != external || seen[v.ID] {
				return nil, &providerError{Code: "invalid_response"}
			}
			seen[v.ID] = true
			out = append(out, v)
		}
		if len(out) == response.Meta.Total {
			return out, nil
		}
		if len(response.Data) == 0 || len(out) > response.Meta.Total {
			return nil, &providerError{Code: "invalid_response"}
		}
	}
	return nil, &providerError{Code: "invalid_response"}
}

type remoteSignal struct {
	ID         string `json:"id"`
	Attributes struct {
		Code   string `json:"code"`
		Status struct {
			Value string `json:"value"`
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
		} `json:"status"`
		Body json.RawMessage `json:"body"`
	} `json:"attributes"`
	Meta struct {
		OEMUpdatedAt string `json:"oemUpdatedAt"`
		IngestedAt   string `json:"ingestedAt"`
		RetrievedAt  string `json:"retrievedAt"`
	} `json:"meta"`
}

func (c *client) signals(ctx context.Context, vehicle, user string) ([]remoteSignal, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if !validProviderID(vehicle) || !validProviderID(user) {
		return nil, errors.New("invalid provider identity")
	}
	var r struct {
		Data []remoteSignal `json:"data"`
		Meta struct {
			Total int `json:"totalCount"`
		} `json:"meta"`
	}
	if err := c.get(ctx, "signals", "/vehicles/"+vehicle+"/signals", user, &r); err != nil {
		return nil, err
	}
	if r.Meta.Total < len(r.Data) || r.Meta.Total > 1000 || len(r.Data) > 1000 {
		return nil, &providerError{Code: "invalid_response"}
	}
	if len(r.Data) < r.Meta.Total {
		// The provider documents pagination metadata but no signal-list page parameters.
		// Fetch the finite supported catalog individually instead of silently losing later pages.
		out := []remoteSignal{}
		for _, code := range supportedCodes() {
			var one struct {
				Data remoteSignal `json:"data"`
			}
			err := c.get(ctx, "signal", "/vehicles/"+vehicle+"/signals/"+code, user, &one)
			if err != nil {
				var p *providerError
				if errors.As(err, &p) && p.Code == "provisioning" {
					continue
				}
				return nil, err
			}
			out = append(out, one.Data)
		}
		return out, nil
	}
	return r.Data, nil
}

func validProviderID(s string) bool {
	if len(s) < 8 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
