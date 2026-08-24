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
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const capabilityContext = "tdesktop-web-proxy-bridge-v1\n"

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
	PublicHostname string    `json:"public_hostname"`
	Listen         string    `json:"listen"`
	AdminListen    string    `json:"admin_listen"`
	PublicDir      string    `json:"public_dir"`
	PublicUpstream string    `json:"public_upstream"`
	ProfilesFile   string    `json:"profiles_file"`
	EnablePprof    bool      `json:"enable_pprof"`
	Limits         Limits    `json:"limits"`
	Timeouts       Timeouts  `json:"timeouts"`
	Profiles       []Profile `json:"-"`
}

type profileFile struct {
	Profiles []profileInput `json:"profiles"`
}

type profileInput struct {
	Name        string        `json:"name"`
	Secret      string        `json:"secret"`
	Backend     string        `json:"backend"`
	CarrierMode CarrierMode   `json:"carrier_mode"`
	Limits      ProfileLimits `json:"limits"`
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
	Name        string
	Backend     string
	CarrierMode CarrierMode
	Capability  [sha256.Size]byte
	Limits      ProfileLimits
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
		Listen:      "127.0.0.1:8080",
		AdminListen: "127.0.0.1:8081",
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
	if len(profilesOverride) > 1 {
		return Config{}, errors.New("only one profiles override is allowed")
	}
	if len(profilesOverride) == 1 && profilesOverride[0] != "" {
		result.ProfilesFile = profilesOverride[0]
	}
	if err := result.validate(); err != nil {
		return Config{}, err
	}
	profiles, err := loadProfiles(result.ProfilesFile, result.PublicHostname, result.Limits)
	if err != nil {
		return Config{}, err
	}
	result.Profiles = profiles
	return result, nil
}

func (c Config) validate() error {
	if err := ValidateHostname(c.PublicHostname); err != nil {
		return fmt.Errorf("public_hostname: %w", err)
	}
	if c.PublicHostname != strings.ToLower(c.PublicHostname) {
		return errors.New("public_hostname must already be lowercase ASCII/IDNA")
	}
	if err := validateListenAddress(c.Listen, true); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if err := validateListenAddress(c.AdminListen, false); err != nil {
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

func DeriveCapability(host string, secret []byte) [sha256.Size]byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(capabilityContext + host))
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
	return validateListenAddress(address, false)
}

func validateListenAddress(address string, allowUnspecified bool) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsLoopback() && !(allowUnspecified && ip.IsUnspecified())) {
		if allowUnspecified {
			return errors.New("must use a numeric loopback or unspecified address")
		}
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

func loadProfiles(path, host string, limits Limits) ([]Profile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("profiles_file: %w", err)
	}
	extraPermissions := info.Mode().Perm() & 0077
	credentialReadOnly := isSystemdCredential(path) &&
		extraPermissions&0033 == 0
	dockerSecretReadOnly := filepath.Clean(filepath.Dir(path)) == "/run/secrets" &&
		info.Mode().Perm()&0022 == 0
	if extraPermissions != 0 && !credentialReadOnly && !dockerSecretReadOnly {
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
		if err := validateProfileLimits(input.Limits, limits); err != nil {
			return nil, fmt.Errorf("profile %q: %w", input.Name, err)
		}
		profileLimits := input.Limits.WithDefaults(limits)
		secret, err := DecodeSecret(input.Secret)
		if err != nil {
			return nil, fmt.Errorf("profile %q: %w", input.Name, err)
		}
		capability := DeriveCapability(host, secret)
		for i := range secret {
			secret[i] = 0
		}
		if _, exists := capabilities[capability]; exists {
			return nil, fmt.Errorf("duplicate capability for profile %q", input.Name)
		}
		result = append(result, Profile{
			Name:        input.Name,
			Backend:     input.Backend,
			CarrierMode: carrierMode,
			Capability:  capability,
			Limits:      profileLimits,
		})
		names[input.Name] = struct{}{}
		capabilities[capability] = struct{}{}
	}
	return result, nil
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
