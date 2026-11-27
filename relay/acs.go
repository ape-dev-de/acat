package relay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ACSAPIVersion is the Communication Identity API version used for the
// Teams-user token exchange.
const ACSAPIVersion = "2023-10-01"

const (
	acsExchangePath  = "/teamsUser/:exchangeAccessToken"
	maxACSBody       = 16 << 10
	acsUpstreamLimit = 15 * time.Second
)

// ACSConfig holds the ACS resource credentials.
type ACSConfig struct {
	Endpoint  *url.URL // https://<name>.communication.azure.com
	AccessKey []byte   // decoded HMAC key
}

// ParseACSConnectionString parses "endpoint=https://…/;accesskey=…".
func ParseACSConnectionString(s string) (ACSConfig, error) {
	var c ACSConfig
	for _, part := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch strings.ToLower(k) {
		case "endpoint":
			u, err := url.Parse(v)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return c, errors.New("acs: endpoint must be an https URL")
			}
			u.Path, u.RawQuery = "", ""
			c.Endpoint = u
		case "accesskey":
			key, err := base64.StdEncoding.DecodeString(v)
			if err != nil || len(key) == 0 {
				return c, errors.New("acs: accesskey is not valid base64")
			}
			c.AccessKey = key
		}
	}
	if c.Endpoint == nil || c.AccessKey == nil {
		return c, errors.New("acs: connection string needs endpoint and accesskey")
	}
	return c, nil
}

// signACS adds the HMAC-SHA256 authentication headers ACS expects.
// See "Sign an HTTP request" in the ACS docs.
func signACS(req *http.Request, body []byte, key []byte, now time.Time) {
	sum := sha256.Sum256(body)
	contentHash := base64.StdEncoding.EncodeToString(sum[:])
	date := now.UTC().Format(http.TimeFormat)
	host := req.URL.Host
	toSign := req.Method + "\n" + req.URL.RequestURI() + "\n" + date + ";" + host + ";" + contentHash
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(toSign))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	req.Header.Set("x-ms-date", date)
	req.Header.Set("x-ms-content-sha256", contentHash)
	req.Header.Set("Authorization", "HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature="+sig)
}

// acsRequest is what the client sends to /acs/token.
type acsRequest struct {
	Token string `json:"token"` // Entra token, audience ACS, scope Teams.ManageCalls
}

// acsResponse is passed through from ACS.
type acsResponse struct {
	Token     string    `json:"token"`
	ExpiresOn time.Time `json:"expiresOn"`
}

// tokenClaims are read WITHOUT verification: only to fill appId/userId
// for ACS and to apply the allowlists. ACS verifies the token itself, so
// a forged token gains nothing.
type tokenClaims struct {
	OID   string `json:"oid"`
	TID   string `json:"tid"`
	AppID string `json:"appid"` // v1 tokens
	AZP   string `json:"azp"`   // v2 tokens
}

func parseClaims(jwt string) (tokenClaims, error) {
	var c tokenClaims
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return c, errors.New("not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, errors.New("bad JWT payload")
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return c, errors.New("bad JWT claims")
	}
	if c.AppID == "" {
		c.AppID = c.AZP
	}
	if c.OID == "" || c.AppID == "" || c.TID == "" {
		return c, errors.New("token lacks oid/appid/tid")
	}
	return c, nil
}

// handleACSToken exchanges a Teams user's Entra token for an ACS token.
// Nothing is stored or logged.
func (s *Server) handleACSToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if s.acs == nil {
		http.Error(w, "acs token exchange not configured", http.StatusNotImplemented)
		return
	}
	if !s.acsLim.allow(clientIP(r, s.cfg.TrustedProxies)) {
		s.m.acs.inc(acsRateLimited)
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxACSBody))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var req acsRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Token == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	claims, err := parseClaims(req.Token)
	if err != nil {
		s.m.acs.inc(acsRejected)
		http.Error(w, "invalid token", http.StatusBadRequest)
		return
	}
	if !allowed(s.cfg.AllowedTenants, claims.TID) || !allowed(s.cfg.AllowedAppIDs, claims.AppID) {
		s.m.acs.inc(acsRejected)
		http.Error(w, "tenant or app not allowed on this relay", http.StatusForbidden)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), acsUpstreamLimit)
	defer cancel()
	res, status, err := s.exchange(ctx, req.Token, claims)
	if err != nil {
		s.m.acs.inc(acsUpstreamError)
		http.Error(w, "token exchange failed", status)
		return
	}
	s.m.acs.inc(acsOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func allowed(list []string, v string) bool {
	if len(list) == 0 {
		return true
	}
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// exchange calls ACS. On error it returns the status for our client:
// 403 when ACS rejected the user token, 502 otherwise.
func (s *Server) exchange(ctx context.Context, token string, c tokenClaims) (acsResponse, int, error) {
	payload, _ := json.Marshal(map[string]string{"token": token, "appId": c.AppID, "userId": c.OID})
	u := *s.acs.Endpoint
	u.Path = acsExchangePath
	u.RawQuery = "api-version=" + ACSAPIVersion
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return acsResponse{}, http.StatusBadGateway, err
	}
	req.Header.Set("Content-Type", "application/json")
	signACS(req, payload, s.acs.AccessKey, s.now())

	resp, err := s.acsHTTP.Do(req)
	if err != nil {
		return acsResponse{}, http.StatusBadGateway, err
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest:
		return acsResponse{}, http.StatusForbidden, fmt.Errorf("acs: HTTP %d", resp.StatusCode)
	default:
		return acsResponse{}, http.StatusBadGateway, fmt.Errorf("acs: HTTP %d", resp.StatusCode)
	}
	var out acsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil || out.Token == "" {
		return acsResponse{}, http.StatusBadGateway, errors.New("acs: bad response")
	}
	return out, 0, nil
}
