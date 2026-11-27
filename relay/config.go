package relay

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// TLS modes.
const (
	TLSModeProxy    = "proxy"    // plain HTTP behind a reverse proxy
	TLSModeAutocert = "autocert" // terminate TLS via Let's Encrypt
)

// Config is read exclusively from environment variables (see ConfigFromEnv).
// Fields without an env variable are tunables with sane defaults, mainly
// overridden in tests.
type Config struct {
	ListenAddr     string         // ACAT_LISTEN_ADDR, default ":8080" (proxy mode)
	MetricsAddr    string         // ACAT_METRICS_ADDR, e.g. "127.0.0.1:9090"; empty = disabled
	TLSMode        string         // ACAT_TLS_MODE, default "proxy"
	Domain         string         // ACAT_DOMAIN, required for autocert
	CertDir        string         // ACAT_CERT_DIR, default "/certs" (autocert cache)
	TrustedProxies []netip.Prefix // ACAT_TRUSTED_PROXIES, comma-separated CIDRs/IPs
	MaxBodyBytes   int64          // ACAT_MAX_BODY_BYTES, default 64 KiB
	MaxConnsPerIP  int            // ACAT_MAX_CONNS_PER_IP, default 256 (corporate NAT)

	ACSConnectionString string   // ACAT_ACS_CONNECTION_STRING; empty = /acs/token disabled
	AllowedTenants      []string // ACAT_ALLOWED_TENANTS, comma-separated; empty = any
	AllowedAppIDs       []string // ACAT_ALLOWED_APP_IDS, comma-separated; empty = any
	ACSRatePerMin       float64  // token exchanges per minute per IP

	WSConnectRate  float64       // new WS connections per second per IP
	WSConnectBurst int           //
	HookRate       float64       // forwarded notifications per second per routing token
	HookBurst      int           //
	SendBuffer     int           // queued frames per client before it counts as slow
	HelloTimeout   time.Duration // time a client has to send hello
	PingInterval   time.Duration // WS keepalive
	WriteTimeout   time.Duration // per-frame write timeout
}

// DefaultConfig returns the defaults applied before env overrides.
func DefaultConfig() Config {
	return Config{
		ListenAddr:     ":8080",
		TLSMode:        TLSModeProxy,
		CertDir:        "/certs",
		MaxBodyBytes:   64 << 10,
		MaxConnsPerIP:  256,
		WSConnectRate:  5,
		WSConnectBurst: 20,
		HookRate:       50,
		HookBurst:      200,
		SendBuffer:     64,
		HelloTimeout:   10 * time.Second,
		PingInterval:   30 * time.Second,
		WriteTimeout:   10 * time.Second,
		ACSRatePerMin:  20,
	}
}

// ConfigFromEnv builds a Config from a getenv function (os.Getenv in prod).
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	c := DefaultConfig()
	setStr := func(dst *string, key string) {
		if v := getenv(key); v != "" {
			*dst = v
		}
	}
	setStr(&c.ListenAddr, "ACAT_LISTEN_ADDR")
	setStr(&c.MetricsAddr, "ACAT_METRICS_ADDR")
	setStr(&c.TLSMode, "ACAT_TLS_MODE")
	setStr(&c.Domain, "ACAT_DOMAIN")
	setStr(&c.CertDir, "ACAT_CERT_DIR")
	setStr(&c.ACSConnectionString, "ACAT_ACS_CONNECTION_STRING")
	c.AllowedTenants = splitList(getenv("ACAT_ALLOWED_TENANTS"))
	c.AllowedAppIDs = splitList(getenv("ACAT_ALLOWED_APP_IDS"))
	if c.ACSConnectionString != "" {
		if _, err := ParseACSConnectionString(c.ACSConnectionString); err != nil {
			return c, fmt.Errorf("ACAT_ACS_CONNECTION_STRING: %w", err)
		}
	}

	if v := getenv("ACAT_MAX_BODY_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1024 {
			return c, fmt.Errorf("ACAT_MAX_BODY_BYTES: want integer >= 1024, got %q", v)
		}
		c.MaxBodyBytes = n
	}
	if v := getenv("ACAT_MAX_CONNS_PER_IP"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return c, fmt.Errorf("ACAT_MAX_CONNS_PER_IP: want integer >= 1, got %q", v)
		}
		c.MaxConnsPerIP = n
	}
	if v := getenv("ACAT_TRUSTED_PROXIES"); v != "" {
		p, err := parsePrefixes(v)
		if err != nil {
			return c, fmt.Errorf("ACAT_TRUSTED_PROXIES: %w", err)
		}
		c.TrustedProxies = p
	}

	switch c.TLSMode {
	case TLSModeProxy:
	case TLSModeAutocert:
		if c.Domain == "" {
			return c, errors.New("ACAT_DOMAIN is required when ACAT_TLS_MODE=autocert")
		}
	default:
		return c, fmt.Errorf("unknown ACAT_TLS_MODE %q (want %q or %q)", c.TLSMode, TLSModeProxy, TLSModeAutocert)
	}
	return c, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parsePrefixes(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			a, err := netip.ParseAddr(part)
			if err != nil {
				return nil, err
			}
			out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Masked())
	}
	return out, nil
}
