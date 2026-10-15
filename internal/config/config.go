package config

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	capabilityContext   = "tdesktop-web-proxy-bridge-v1\n"
	capabilityContextV2 = "tdesktop-web-proxy-bridge-v2\n"
)

// MaxBasePathLength bounds the optional relay prefix. See BASE_PATH.md.
const MaxBasePathLength = 128

// MaxCarrierBatchBytes is the largest downlink body a carrier may deliver:
// the desktop client's browser-fallback loopback WebSocket rejects messages
// above 2 MiB, so a larger relay batch would kill that carrier.
const MaxCarrierBatchBytes = 2 * 1024 * 1024

type CarrierMode string

const (
	CarrierHTTPS          CarrierMode = "https"
	CarrierHTTPSLanes     CarrierMode = "https-lanes"
	CarrierWebSocket      CarrierMode = "websocket"
	CarrierWebSocketLanes CarrierMode = "websocket-lanes"
)

func (m CarrierMode) Valid() bool {
	return m == CarrierHTTPS ||
		m == CarrierHTTPSLanes ||
		m == CarrierWebSocket ||
		m == CarrierWebSocketLanes
}

func (m CarrierMode) SupportsCallsTCP() bool {
	return m == CarrierHTTPSLanes ||
		m == CarrierWebSocketLanes
}

func (m CarrierMode) WithDefault() CarrierMode {
	if m == "" {
		return CarrierHTTPS
	}
	return m
}

type Duration time.Duration

func (d *Duration) UnmarshalJSON(input []byte) error {
	var text string
	if err := json.Unmarshal(input, &text); err != nil {
		return errors.New("duration must be a string such as \"5s\"")
	}
	value, err := time.ParseDuration(text)
	if err != nil {
		return err
	}
	*d = Duration(value)
	return nil
}

func (d Duration) Value() time.Duration {
	return time.Duration(d)
}

type Limits struct {
	MaxHeaderBytes            int `json:"max_header_bytes"`
	MaxBodyBytes              int `json:"max_body_bytes"`
	MaxFramePayload           int `json:"max_frame_payload"`
	CarrierBatchBytes         int `json:"carrier_batch_bytes"`
	MaxStreamsPerSession      int `json:"max_streams_per_session"`
	MaxClosedStreamIDs        int `json:"max_closed_stream_ids"`
	MaxPendingPerSession      int `json:"max_pending_per_session"`
	MaxPendingGlobal          int `json:"max_pending_global"`
	MaxPendingItemsPerSession int `json:"max_pending_items_per_session"`
	MaxPendingItemsGlobal     int `json:"max_pending_items_global"`
	MaxSessionsPerIP          int `json:"max_sessions_per_ip"`
	MaxSessionsGlobal         int `json:"max_sessions_global"`
	MaxStreamsGlobal          int `json:"max_streams_global"`
	MaxBackendDialsInFlight   int `json:"max_backend_dials_in_flight"`
	NewSessionsPerMinute      int `json:"new_sessions_per_minute"`
	NewSessionsBurst          int `json:"new_sessions_burst"`
	NewStreamsPerMinute       int `json:"new_streams_per_minute"`
	NewStreamsBurst           int `json:"new_streams_burst"`
	MaxBootstrapsPerIP        int `json:"max_bootstraps_per_ip"`
	MaxBootstrapsGlobal       int `json:"max_bootstraps_global"`
	NewBootstrapsPerMinute    int `json:"new_bootstraps_per_minute"`
	NewBootstrapsBurst        int `json:"new_bootstraps_burst"`
	MaxProfiles               int `json:"max_profiles"`
}

type Timeouts struct {
	BackendDial       Duration `json:"backend_dial"`
	LongPoll          Duration `json:"long_poll"`
	ReconnectGrace    Duration `json:"reconnect_grace"`
	BootstrapLifetime Duration `json:"bootstrap_lifetime"`
	ReadHeader        Duration `json:"read_header"`
	Idle              Duration `json:"idle"`
	Shutdown          Duration `json:"shutdown"`
}

type Config struct {
	PublicHostname   string    `json:"public_hostname"`
	BasePath         string    `json:"base_path"`
	Listen           string    `json:"listen"`
	AdminListen      string    `json:"admin_listen"`
	PublicDir        string    `json:"public_dir"`
	PublicUpstream   string    `json:"public_upstream"`
	StaticRoutes     string    `json:"static_routes"`
	TokenKeyFile     string    `json:"token_key_file"`
	ProfilesFile     string    `json:"profiles_file"`
	EnablePprof      bool      `json:"enable_pprof"`
	Limits           Limits    `json:"limits"`
	Timeouts         Timeouts  `json:"timeouts"`
	Profiles         []Profile `json:"-"`
	LegacyTokenDrain bool      `json:"-"`
}

type profileFile struct {
	Profiles []profileInput `json:"profiles"`
}

type profileInput struct {
	Name              string        `json:"name"`
	Secret            string        `json:"secret"`
	Backend           string        `json:"backend"`
	CarrierMode       CarrierMode   `json:"carrier_mode"`
	CallsTCP          bool          `json:"calls_tcp"`
	CallAllowlist     []string      `json:"call_allowlist"`
	CallPorts         []uint16      `json:"call_ports"`
	CallPadBuckets    []int         `json:"call_pad_buckets"`
	CallPadQuantum    int           `json:"call_pad_quantum"`
	CallFlushJitterMs *int          `json:"call_flush_jitter_ms"`
	Limits            ProfileLimits `json:"limits"`
}

type ProfileLimits struct {
	MaxSessions             int `json:"max_sessions"`
	MaxStreams              int `json:"max_streams"`
	MaxBackendDialsInFlight int `json:"max_backend_dials_in_flight"`
	NewSessionsPerMinute    int `json:"new_sessions_per_minute"`
	NewSessionsBurst        int `json:"new_sessions_burst"`
	NewStreamsPerMinute     int `json:"new_streams_per_minute"`
	NewStreamsBurst         int `json:"new_streams_burst"`
	MaxStreamsPerSession    int `json:"max_streams_per_session"`
	MaxPendingPerSession    int `json:"max_pending_per_session"`
}

type Profile struct {
	Name              string
	Backend           string
	CarrierMode       CarrierMode
	CallsTCP          bool
	CallAllowlist     []netip.Prefix
	CallPorts         []uint16
	CallPadBuckets    []int
	CallFlushJitterMs int
	Capability        [sha256.Size]byte
	Limits            ProfileLimits
}

func (limits ProfileLimits) WithDefaults(global Limits) ProfileLimits {
	result := limits
	if result.MaxSessions == 0 {
		result.MaxSessions = global.MaxSessionsGlobal
	}
	if result.MaxStreams == 0 {
		result.MaxStreams = global.MaxStreamsGlobal
	}
	if result.MaxBackendDialsInFlight == 0 {
		result.MaxBackendDialsInFlight = minInt(
			global.MaxBackendDialsInFlight,
			result.MaxStreams)
	}
	if result.NewSessionsPerMinute == 0 {
		result.NewSessionsPerMinute = global.NewSessionsPerMinute
	}
	if result.NewSessionsBurst == 0 {
		result.NewSessionsBurst = global.NewSessionsBurst
	}
	if result.NewStreamsPerMinute == 0 {
		result.NewStreamsPerMinute = global.NewStreamsPerMinute
	}
	if result.NewStreamsBurst == 0 {
		result.NewStreamsBurst = global.NewStreamsBurst
	}
	if result.MaxStreamsPerSession == 0 {
		result.MaxStreamsPerSession = minInt(
			global.MaxStreamsPerSession,
			result.MaxStreams)
	}
	if result.MaxPendingPerSession == 0 {
		result.MaxPendingPerSession = global.MaxPendingPerSession
	}
	return result
}

func Defaults() Config {
	return Config{
		Listen:       "127.0.0.1:8080",
		AdminListen:  "127.0.0.1:8081",
		StaticRoutes: "legacy",
		TokenKeyFile: "token.key",
		Limits: Limits{
			MaxHeaderBytes:            16 * 1024,
			MaxBodyBytes:              2 * 1024 * 1024,
			MaxFramePayload:           1024 * 1024,
			CarrierBatchBytes:         2 * 1024 * 1024,
			MaxStreamsPerSession:      128,
			MaxClosedStreamIDs:        4096,
			MaxPendingPerSession:      32 * 1024 * 1024,
			MaxPendingGlobal:          512 * 1024 * 1024,
			MaxPendingItemsPerSession: 16 * 1024,
			MaxPendingItemsGlobal:     256 * 1024,
			MaxSessionsPerIP:          0,
			MaxSessionsGlobal:         128,
			MaxStreamsGlobal:          4096,
			MaxBackendDialsInFlight:   256,
			NewSessionsPerMinute:      600,
			NewSessionsBurst:          128,
			NewStreamsPerMinute:       6000,
			NewStreamsBurst:           512,
			MaxBootstrapsPerIP:        0,
			MaxBootstrapsGlobal:       512,
			NewBootstrapsPerMinute:    1200,
			NewBootstrapsBurst:        256,
			MaxProfiles:               32,
		},
		Timeouts: Timeouts{
			BackendDial:       Duration(5 * time.Second),
			LongPoll:          Duration(25 * time.Second),
			ReconnectGrace:    Duration(2 * time.Minute),
			BootstrapLifetime: Duration(2 * time.Minute),
			ReadHeader:        Duration(10 * time.Second),
			Idle:              Duration(75 * time.Second),
			Shutdown:          Duration(15 * time.Second),
		},
	}
}

func Load(path string, profilesOverride ...string) (Config, error) {
	result := Defaults()
	input, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Config{}, errors.New("decode config: trailing data")
	}
	if result.PublicDir != "" && !filepath.IsAbs(result.PublicDir) {
		result.PublicDir = filepath.Join(filepath.Dir(path), result.PublicDir)
	}
	if result.ProfilesFile != "" && !filepath.IsAbs(result.ProfilesFile) {
		result.ProfilesFile = filepath.Join(filepath.Dir(path), result.ProfilesFile)
	}
	if !filepath.IsAbs(result.TokenKeyFile) {
		result.TokenKeyFile = filepath.Join(filepath.Dir(path), result.TokenKeyFile)
	}
	if len(profilesOverride) > 1 {
		return Config{}, errors.New("only one profiles override is allowed")
	}
	if len(profilesOverride) == 1 && profilesOverride[0] != "" {
		result.ProfilesFile = profilesOverride[0]
	}
	if err := result.validate(); err != nil {
		return Config{}, err
	}
	profiles, err := loadProfiles(result.ProfilesFile, result.PublicHostname, result.BasePath, result.Limits)
	if err != nil {
		return Config{}, err
	}
	result.Profiles = profiles
	return result, nil
}

func (c Config) validate() error {
	if c.StaticRoutes != "exact" && c.StaticRoutes != "legacy" {
		return errors.New("static_routes must be exact or legacy")
	}
	if err := ValidateHostname(c.PublicHostname); err != nil {
		return fmt.Errorf("public_hostname: %w", err)
	}
	if c.PublicHostname != strings.ToLower(c.PublicHostname) {
		return errors.New("public_hostname must already be lowercase ASCII/IDNA")
	}
	if err := ValidateBasePath(c.BasePath); err != nil {
		return fmt.Errorf("base_path: %w", err)
	}
	if err := validateLoopbackAddress(c.Listen); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if err := validateLoopbackAddress(c.AdminListen); err != nil {
		return fmt.Errorf("admin_listen: %w", err)
	}
	if c.Listen == c.AdminListen {
		return errors.New("listen and admin_listen must differ")
	}
	if (c.PublicDir == "") == (c.PublicUpstream == "") {
		return errors.New("exactly one of public_dir or public_upstream is required")
	}
	if c.PublicDir != "" {
		info, err := os.Stat(c.PublicDir)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("public_dir is not a directory: %s", c.PublicDir)
		}
		if _, err := os.Stat(filepath.Join(c.PublicDir, "index.html")); err != nil {
			return errors.New("public_dir must contain index.html")
		}
	} else if err := validatePublicUpstream(c.PublicUpstream); err != nil {
		return fmt.Errorf("public_upstream: %w", err)
	}
	if c.ProfilesFile == "" {
		return errors.New("profiles_file is required")
	}
	if c.Limits.MaxHeaderBytes < 4096 || c.Limits.MaxBodyBytes < 1024 || c.Limits.MaxFramePayload <= 0 || c.Limits.MaxFramePayload > 1024*1024 || c.Limits.CarrierBatchBytes < 256*1024 || c.Limits.CarrierBatchBytes > c.Limits.MaxBodyBytes {
		return errors.New("invalid HTTP or frame limits")
	}
	if c.Limits.CarrierBatchBytes > MaxCarrierBatchBytes {
		return errors.New("carrier_batch_bytes must not exceed the 2 MiB desktop loopback message cap")
	}
	values := []int{
		c.Limits.MaxStreamsPerSession, c.Limits.MaxClosedStreamIDs,
		c.Limits.MaxPendingPerSession, c.Limits.MaxPendingGlobal,
		c.Limits.MaxPendingItemsPerSession, c.Limits.MaxPendingItemsGlobal,
		c.Limits.MaxSessionsGlobal, c.Limits.MaxStreamsGlobal,
		c.Limits.MaxBackendDialsInFlight,
		c.Limits.NewSessionsPerMinute, c.Limits.NewSessionsBurst,
		c.Limits.NewStreamsPerMinute, c.Limits.NewStreamsBurst,
		c.Limits.MaxBootstrapsGlobal,
		c.Limits.NewBootstrapsPerMinute, c.Limits.NewBootstrapsBurst,
		c.Limits.MaxProfiles,
	}
	for _, value := range values {
		if value <= 0 {
			return errors.New("all resource limits must be positive")
		}
	}
	if c.Limits.MaxSessionsPerIP < 0 || c.Limits.MaxBootstrapsPerIP < 0 {
		return errors.New("per-IP limits must not be negative")
	}
	if c.Limits.MaxPendingGlobal < c.Limits.MaxPendingPerSession ||
		c.Limits.MaxPendingItemsGlobal < c.Limits.MaxPendingItemsPerSession ||
		c.Limits.MaxSessionsGlobal < c.Limits.MaxSessionsPerIP ||
		c.Limits.MaxStreamsGlobal < c.Limits.MaxStreamsPerSession ||
		c.Limits.MaxStreamsGlobal < c.Limits.MaxBackendDialsInFlight ||
		c.Limits.MaxBootstrapsGlobal < c.Limits.MaxBootstrapsPerIP {
		return errors.New("global limits must not be smaller than per-session or per-IP limits")
	}
	durations := []time.Duration{
		c.Timeouts.BackendDial.Value(), c.Timeouts.LongPoll.Value(),
		c.Timeouts.ReconnectGrace.Value(), c.Timeouts.BootstrapLifetime.Value(),
		c.Timeouts.ReadHeader.Value(), c.Timeouts.Idle.Value(), c.Timeouts.Shutdown.Value(),
	}
	for _, value := range durations {
		if value <= 0 {
			return errors.New("all timeouts must be positive")
		}
	}
	return nil
}

func ValidateHostname(host string) error {
	if host == "" || len(host) > 253 || strings.HasSuffix(host, ".") || strings.ContainsAny(host, ":/@?#[]") {
		return errors.New("must be a DNS hostname without scheme, port, path, query, fragment, or trailing dot")
	}
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return errors.New("IP addresses and single-label names are not allowed")
	}
	for _, character := range host {
		if character > 127 {
			return errors.New("use the lowercase ASCII IDNA A-label form")
		}
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("invalid DNS label")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return errors.New("hostname must be lowercase ASCII/IDNA")
			}
		}
	}
	return nil
}

// DeriveCapability binds the capability to the host, and to the base path when
// one is configured. A root deployment keeps the frozen v1 context byte for
// byte, so a capability minted for one prefix authenticates nothing at another
// prefix or at the root.
// ValidateBasePath accepts only the canonical form: one or more "/"-separated
// segments, each starting with an ASCII letter or digit and continuing with
// those, "-" or "_". An empty value serves the host root. "." is not in the
// alphabet, so "." and ".." segments cannot occur and no dot-segment resolution
// is ever needed. Noncanonical values are rejected rather than repaired, so the
// configured value and the wire path stay the same string.
func ValidateBasePath(path string) error {
	if path == "" {
		return nil
	}
	if len(path) > MaxBasePathLength {
		return fmt.Errorf("must be at most %d characters", MaxBasePathLength)
	}
	if strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return errors.New("must not start or end with \"/\"")
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" {
			return errors.New("must not contain an empty segment")
		}
		for i := 0; i < len(segment); i++ {
			character := segment[i]
			switch {
			case character >= 'a' && character <= 'z',
				character >= 'A' && character <= 'Z',
				character >= '0' && character <= '9':
			case (character == '-' || character == '_') && i > 0:
			default:
				return errors.New("segments must match [A-Za-z0-9][A-Za-z0-9_-]*")
			}
		}
	}
	return nil
}

// Base is the wire prefix every relay endpoint sits under. It always begins and
// ends with "/", so an endpoint is Base() + "api/v1/up".
func Base(basePath string) string {
	if basePath == "" {
		return "/"
	}
	return "/" + basePath + "/"
}

func (c Config) Base() string {
	return Base(c.BasePath)
}

func DeriveCapability(host, basePath string, secret []byte) [sha256.Size]byte {
	mac := hmac.New(sha256.New, secret)
	if basePath == "" {
		_, _ = mac.Write([]byte(capabilityContext + host))
	} else {
		_, _ = mac.Write([]byte(capabilityContextV2 + host + "\n" + basePath))
	}
	var result [sha256.Size]byte
	copy(result[:], mac.Sum(nil))
	return result
}

func CapabilityString(capability [sha256.Size]byte) string {
	return base64.RawURLEncoding.EncodeToString(capability[:])
}

func DecodeSecret(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	var decoded []byte
	var err error
	if len(value) == 32 || len(value) == 34 {
		decoded, err = hex.DecodeString(value)
	} else {
		encodings := []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding}
		for _, encoding := range encodings {
			decoded, err = encoding.DecodeString(value)
			if err == nil {
				break
			}
		}
	}
	if err != nil || (len(decoded) != 16 && len(decoded) != 17) {
		return nil, errors.New("secret must decode to 16 bytes, optionally prefixed with dd")
	}
	if len(decoded) == 17 && decoded[0] != 0xdd {
		return nil, errors.New("17-byte secret must use the dd prefix")
	}
	return decoded, nil
}

func validateLoopbackAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("must use a numeric loopback address")
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return errors.New("invalid port")
	}
	return nil
}

func validatePublicUpstream(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" {
		return errors.New("must use http on a numeric loopback address")
	}
	if parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("must contain only scheme, loopback address, and port")
	}
	if err := validateLoopbackAddress(parsed.Host); err != nil {
		return err
	}
	return nil
}

func loadProfiles(path, host, basePath string, limits Limits) ([]Profile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("profiles_file: %w", err)
	}
	extraPermissions := info.Mode().Perm() & 0077
	credentialReadOnly := isSystemdCredential(path) &&
		extraPermissions&0033 == 0
	if extraPermissions != 0 && !credentialReadOnly {
		return nil, errors.New("profiles_file must not be readable or writable by group or others")
	}
	input, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		for i := range input {
			input[i] = 0
		}
	}()
	var source profileFile
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&source); err != nil {
		return nil, fmt.Errorf("decode profiles: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("decode profiles: trailing data")
	}
	if len(source.Profiles) == 0 || len(source.Profiles) > limits.MaxProfiles {
		return nil, fmt.Errorf("profiles must contain between 1 and %d entries", limits.MaxProfiles)
	}
	names := make(map[string]struct{})
	capabilities := make(map[[sha256.Size]byte]struct{})
	result := make([]Profile, 0, len(source.Profiles))
	for _, input := range source.Profiles {
		if input.Name == "" || len(input.Name) > 64 {
			return nil, errors.New("profile name must contain 1-64 characters")
		}
		if _, exists := names[input.Name]; exists {
			return nil, fmt.Errorf("duplicate profile name %q", input.Name)
		}
		if err := validateLoopbackAddress(input.Backend); err != nil {
			return nil, fmt.Errorf("profile %q backend: %w", input.Name, err)
		}
		carrierMode := input.CarrierMode.WithDefault()
		if !carrierMode.Valid() {
			return nil, fmt.Errorf("profile %q carrier_mode must be https, https-lanes, websocket, or websocket-lanes", input.Name)
		}
		if input.CallsTCP && !carrierMode.SupportsCallsTCP() {
			return nil, fmt.Errorf("profile %q calls_tcp requires https-lanes or websocket-lanes carrier_mode", input.Name)
		}
		callAllowlist, err := resolveCallAllowlist(input.CallsTCP, input.CallAllowlist)
		if err != nil {
			return nil, fmt.Errorf("profile %q call_allowlist: %w", input.Name, err)
		}
		callPorts, err := resolveCallPorts(input.CallsTCP, input.CallPorts)
		if err != nil {
			return nil, fmt.Errorf("profile %q call_ports: %w", input.Name, err)
		}
		callPadBuckets, err := resolveCallPadBuckets(input.CallsTCP, input.CallPadBuckets, input.CallPadQuantum)
		if err != nil {
			return nil, fmt.Errorf("profile %q call_pad_buckets: %w", input.Name, err)
		}
		callFlushJitterMs, err := resolveCallFlushJitterMs(input.CallsTCP, input.CallFlushJitterMs)
		if err != nil {
			return nil, fmt.Errorf("profile %q call_flush_jitter_ms: %w", input.Name, err)
		}
		if err := validateProfileLimits(input.Limits, limits); err != nil {
			return nil, fmt.Errorf("profile %q: %w", input.Name, err)
		}
		profileLimits := input.Limits.WithDefaults(limits)
		secret, err := DecodeSecret(input.Secret)
		if err != nil {
			return nil, fmt.Errorf("profile %q: %w", input.Name, err)
		}
		capability := DeriveCapability(host, basePath, secret)
		for i := range secret {
			secret[i] = 0
		}
		if _, exists := capabilities[capability]; exists {
			return nil, fmt.Errorf("duplicate capability for profile %q", input.Name)
		}
		result = append(result, Profile{
			Name:              input.Name,
			Backend:           input.Backend,
			CarrierMode:       carrierMode,
			CallsTCP:          input.CallsTCP,
			CallAllowlist:     callAllowlist,
			CallPorts:         callPorts,
			CallPadBuckets:    callPadBuckets,
			CallFlushJitterMs: callFlushJitterMs,
			Capability:        capability,
			Limits:            profileLimits,
		})
		names[input.Name] = struct{}{}
		capabilities[capability] = struct{}{}
	}
	return result, nil
}

var defaultTelegramCallCIDRs = []string{
	"91.105.192.0/23",
	"91.108.4.0/22",
	"91.108.8.0/22",
	"91.108.12.0/22",
	"91.108.16.0/22",
	"91.108.20.0/22",
	"91.108.56.0/22",
	"95.161.64.0/20",
	"149.154.160.0/20",
	"185.76.151.0/24",
	"2001:67c:4e8::/48",
	"2001:b28:f23c::/47",
	"2001:b28:f23f::/48",
	"2a0a:f280::/32",
}

var forbiddenCallPrefixes = func() []netip.Prefix {
	cidrs := []string{
		"0.0.0.0/8",
		"10.0.0.0/8",
		"100.64.0.0/10",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"172.16.0.0/12",
		"192.0.0.0/24",
		"192.0.2.0/24",
		"192.88.99.0/24",
		"192.168.0.0/16",
		"198.18.0.0/15",
		"198.51.100.0/24",
		"203.0.113.0/24",
		"224.0.0.0/4",
		"240.0.0.0/4",
		"::/128",
		"::1/128",
		"::ffff:0:0/96",
		"64:ff9b::/96",
		"64:ff9b:1::/48",
		"100::/64",
		"2001::/32",
		"2001:2::/48",
		"2001:db8::/32",
		"2002::/16",
		"fc00::/7",
		"fe80::/10",
		"fec0::/10",
		"ff00::/8",
	}
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		prefixes = append(prefixes, netip.MustParsePrefix(cidr))
	}
	return prefixes
}()

func DefaultTelegramCallPrefixes() []netip.Prefix {
	result := make([]netip.Prefix, 0, len(defaultTelegramCallCIDRs))
	for _, cidr := range defaultTelegramCallCIDRs {
		result = append(result, netip.MustParsePrefix(cidr))
	}
	return result
}

func DefaultTelegramCallPorts() []uint16 {
	return []uint16{443, 5222}
}

var globalUnicastIPv6Prefix = netip.MustParsePrefix("2000::/3")

func IsForbiddenCallAddr(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return true
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() ||
		addr.IsPrivate() ||
		addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified() {
		return true
	}
	if addr.Is6() && !globalUnicastIPv6Prefix.Contains(addr) {
		return true
	}
	for _, forbidden := range forbiddenCallPrefixes {
		if forbidden.Contains(addr) {
			return true
		}
	}
	return false
}

func resolveCallAllowlist(enabled bool, raw []string) ([]netip.Prefix, error) {
	if !enabled {
		if len(raw) != 0 {
			return nil, errors.New("requires calls_tcp to be enabled")
		}
		return nil, nil
	}
	if len(raw) == 0 {
		return DefaultTelegramCallPrefixes(), nil
	}
	result := make([]netip.Prefix, 0, len(raw))
	for _, entry := range raw {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil || !prefix.IsValid() {
			return nil, fmt.Errorf("invalid CIDR %q", entry)
		}
		if prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("IPv4-mapped IPv6 CIDR %q is not allowed", entry)
		}
		if prefix.Masked() != prefix {
			return nil, fmt.Errorf("CIDR %q must be canonical (%s)", entry, prefix.Masked())
		}
		if prefix.Bits() <= 0 {
			return nil, fmt.Errorf("wildcard CIDR %q is not allowed", entry)
		}
		if IsForbiddenCallAddr(prefix.Addr()) {
			return nil, fmt.Errorf("CIDR %q lies in a reserved or non-public range", entry)
		}
		for _, forbidden := range forbiddenCallPrefixes {
			if prefix.Overlaps(forbidden) {
				return nil, fmt.Errorf("CIDR %q overlaps reserved range %s", entry, forbidden)
			}
		}
		result = append(result, prefix)
	}
	return result, nil
}

func resolveCallPorts(enabled bool, raw []uint16) ([]uint16, error) {
	if !enabled {
		if len(raw) != 0 {
			return nil, errors.New("requires calls_tcp to be enabled")
		}
		return nil, nil
	}
	if len(raw) == 0 {
		return DefaultTelegramCallPorts(), nil
	}
	seen := make(map[uint16]struct{}, len(raw))
	result := make([]uint16, 0, len(raw))
	for _, port := range raw {
		if port == 0 {
			return nil, errors.New("port 0 is not allowed")
		}
		if _, exists := seen[port]; exists {
			continue
		}
		seen[port] = struct{}{}
		result = append(result, port)
	}
	return result, nil
}

const (
	DefaultCallFlushJitterMs = 8
	MaxCallFlushJitterMs     = 15
)

func DefaultCallPadBuckets() []int {
	return []int{256, 384, 512, 768}
}

func resolveCallPadBuckets(enabled bool, rawBuckets []int, rawQuantum int) ([]int, error) {
	if !enabled {
		if len(rawBuckets) != 0 || rawQuantum != 0 {
			return nil, errors.New("requires calls_tcp to be enabled")
		}
		return nil, nil
	}
	if len(rawBuckets) != 0 && rawQuantum != 0 {
		return nil, errors.New("cannot specify both call_pad_buckets and call_pad_quantum")
	}
	if rawQuantum < 0 || (len(rawBuckets) == 1 && rawBuckets[0] == 0) {
		return nil, nil
	}
	if rawQuantum > 0 {
		if !validCallPadBucketSize(rawQuantum) {
			return nil, fmt.Errorf("call_pad_quantum %d must be between 64 and 1024 and a multiple of 128 (or 64)", rawQuantum)
		}
		if rawQuantum*2 <= 1024 {
			return []int{rawQuantum, rawQuantum * 2}, nil
		}
		return []int{rawQuantum}, nil
	}
	if len(rawBuckets) == 0 {
		return DefaultCallPadBuckets(), nil
	}
	if len(rawBuckets) > 8 {
		return nil, errors.New("call_pad_buckets may contain at most 8 buckets")
	}
	result := make([]int, 0, len(rawBuckets))
	prev := 0
	for _, bucket := range rawBuckets {
		if !validCallPadBucketSize(bucket) {
			return nil, fmt.Errorf("bucket %d must be between 64 and 1024 and a multiple of 128 (or 64)", bucket)
		}
		if bucket <= prev {
			return nil, errors.New("call_pad_buckets must be sorted in strictly ascending order")
		}
		prev = bucket
		result = append(result, bucket)
	}
	return result, nil
}

func validCallPadBucketSize(bucket int) bool {
	if bucket < 64 || bucket > 1024 {
		return false
	}
	return bucket == 64 || bucket%128 == 0
}

func resolveCallFlushJitterMs(enabled bool, raw *int) (int, error) {
	if !enabled {
		if raw != nil {
			return 0, errors.New("requires calls_tcp to be enabled")
		}
		return 0, nil
	}
	if raw == nil {
		return DefaultCallFlushJitterMs, nil
	}
	if *raw < 0 || *raw > MaxCallFlushJitterMs {
		return 0, fmt.Errorf("must be between 0 and %d", MaxCallFlushJitterMs)
	}
	return *raw, nil
}

func (p Profile) AllowsCallTarget(target netip.AddrPort) bool {
	if !p.CallsTCP || !p.CarrierMode.WithDefault().SupportsCallsTCP() || !target.IsValid() || target.Port() == 0 {
		return false
	}
	addr := target.Addr().Unmap()
	if IsForbiddenCallAddr(addr) {
		return false
	}
	portAllowed := false
	for _, allowedPort := range p.CallPorts {
		if allowedPort == target.Port() {
			portAllowed = true
			break
		}
	}
	if !portAllowed {
		return false
	}
	for _, prefix := range p.CallAllowlist {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func validateProfileLimits(value ProfileLimits, global Limits) error {
	checks := []struct {
		name  string
		value int
		limit int
	}{
		{"max_sessions", value.MaxSessions, global.MaxSessionsGlobal},
		{"max_streams", value.MaxStreams, global.MaxStreamsGlobal},
		{"max_backend_dials_in_flight", value.MaxBackendDialsInFlight, global.MaxBackendDialsInFlight},
		{"new_sessions_per_minute", value.NewSessionsPerMinute, global.NewSessionsPerMinute},
		{"new_sessions_burst", value.NewSessionsBurst, global.NewSessionsBurst},
		{"new_streams_per_minute", value.NewStreamsPerMinute, global.NewStreamsPerMinute},
		{"new_streams_burst", value.NewStreamsBurst, global.NewStreamsBurst},
		{"max_streams_per_session", value.MaxStreamsPerSession, global.MaxStreamsPerSession},
		{"max_pending_per_session", value.MaxPendingPerSession, global.MaxPendingPerSession},
	}
	for _, check := range checks {
		if check.value < 0 || check.value > check.limit {
			return fmt.Errorf("%s must be between 0 and %d", check.name, check.limit)
		}
	}
	resolved := value.WithDefaults(global)
	if resolved.MaxStreamsPerSession > resolved.MaxStreams {
		return errors.New("max_streams_per_session must not exceed max_streams")
	}
	if resolved.MaxBackendDialsInFlight > resolved.MaxStreams {
		return errors.New("max_backend_dials_in_flight must not exceed max_streams")
	}
	return nil
}

func minInt(first, second int) int {
	if first < second {
		return first
	}
	return second
}

func isSystemdCredential(path string) bool {
	directory := os.Getenv("CREDENTIALS_DIRECTORY")
	if directory == "" || !filepath.IsAbs(directory) {
		return false
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	return filepath.Dir(absolute) == filepath.Clean(directory)
}
