package smartcar

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
)

var (
	ErrInvalid   = errors.New("invalid smartcar request")
	ErrSession   = errors.New("smartcar session expired or already used")
	ErrCancelled = errors.New("smartcar authorization was not completed")
	ErrReconnect = errors.New("smartcar reconnection is required")
)

type Config struct {
	ApplicationID, ClientID, ClientSecret, Mode, RedirectURI string
	EncryptionKey                                            []byte
	PollInterval                                             time.Duration
}
type Service struct {
	store                            *garage.Store
	client                           *client
	logger                           *slog.Logger
	aead                             cipher.AEAD
	key                              []byte
	applicationID, mode, redirectURI string
	interval                         time.Duration
	wake                             chan struct{}
}
type Configuration struct {
	Configured          bool   `json:"configured"`
	Mode                string `json:"mode,omitempty"`
	ConnectAvailable    bool   `json:"connectAvailable"`
	PollIntervalSeconds int    `json:"pollIntervalSeconds"`
}

func New(store *garage.Store, cfg Config, logger *slog.Logger) (*Service, error) {
	if store == nil || logger == nil || len(cfg.EncryptionKey) != 32 || !validApplicationID(cfg.ApplicationID) || !validProviderID(cfg.ClientID) || len(cfg.ClientSecret) < 16 || strings.ContainsAny(cfg.ClientSecret, "\r\n\x00") {
		return nil, errors.New("invalid smartcar configuration")
	}
	if cfg.Mode == "" {
		cfg.Mode = "live"
	}
	if cfg.Mode != "live" && cfg.Mode != "simulated" {
		return nil, errors.New("invalid smartcar mode")
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = time.Hour
	}
	if cfg.PollInterval < 5*time.Minute || cfg.PollInterval > 24*time.Hour {
		return nil, errors.New("smartcar poll interval must be between five minutes and one day")
	}
	expected := "sc" + cfg.ApplicationID + "://callback"
	if cfg.RedirectURI == "" {
		cfg.RedirectURI = expected
	}
	if cfg.RedirectURI != expected {
		return nil, errors.New("smartcar redirect must use the configured application's callback scheme")
	}
	block, err := aes.NewCipher(cfg.EncryptionKey)
	if err != nil {
		return nil, errors.New("invalid encryption key")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	s := &Service{store: store, client: newClient(cfg.ClientID, cfg.ClientSecret), logger: logger, aead: aead, key: append([]byte(nil), cfg.EncryptionKey...), applicationID: cfg.ApplicationID, mode: cfg.Mode, redirectURI: cfg.RedirectURI, interval: cfg.PollInterval, wake: make(chan struct{}, 1)}
	connections, err := store.SmartcarConnections(context.Background())
	if err != nil {
		return nil, err
	}
	for _, connection := range connections {
		if _, err = s.identity(connection); err != nil {
			return nil, errors.New("smartcar encryption key or application does not match stored connections")
		}
	}
	return s, nil
}

func validApplicationID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	raw, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil && len(raw) == 16
}

func (s *Service) Configuration() Configuration {
	return Configuration{true, s.mode, true, int(s.interval.Seconds())}
}
func (s *Service) Status(ctx context.Context, vehicleID string) (garage.SmartcarStatus, error) {
	if _, err := s.store.Vehicle(ctx, vehicleID); err != nil {
		return garage.SmartcarStatus{}, err
	}
	v, err := s.store.SmartcarConnection(ctx, vehicleID)
	if errors.Is(err, sql.ErrNoRows) {
		return garage.SmartcarStatus{State: "disconnected", SupportedMetrics: []string{}}, nil
	}
	if err != nil {
		return garage.SmartcarStatus{}, err
	}
	if v.Status.State == "reconnect_required" {
		v.Status.NextAttemptAt = nil
	} else {
		v.Status.NextAttemptAt = &v.NextAttemptAt
	}
	return v.Status, nil
}

func (s *Service) seal(kind, id string, v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{1}, nonce...)
	return s.aead.Seal(out, nonce, raw, []byte("pitpilot.smartcar.v1:"+kind+":"+id)), nil
}
func (s *Service) open(kind, id string, encrypted []byte, v any) error {
	n := s.aead.NonceSize()
	if len(encrypted) < 1+n+s.aead.Overhead() || encrypted[0] != 1 {
		return errors.New("invalid encrypted integration state")
	}
	raw, err := s.aead.Open(nil, encrypted[1:1+n], encrypted[1+n:], []byte("pitpilot.smartcar.v1:"+kind+":"+id))
	if err != nil {
		return errors.New("invalid encrypted integration state")
	}
	if json.Unmarshal(raw, v) != nil {
		return errors.New("invalid encrypted integration state")
	}
	return nil
}
func (s *Service) remoteKey(vehicle string) string {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(s.applicationID + ":" + vehicle))
	return hex.EncodeToString(h.Sum(nil))
}
func randomValue() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("secure randomness unavailable")
	}
	return hex.EncodeToString(b)
}

type identity struct{ ApplicationID, UserID, VehicleID, ExternalID, Mode string }

func (s *Service) identity(v garage.SmartcarConnection) (i identity, err error) {
	err = s.open("connection", v.ID, v.Encrypted, &i)
	if err == nil && (i.ApplicationID != s.applicationID || i.Mode != s.mode || !validProviderID(i.UserID) || !validProviderID(i.VehicleID)) {
		err = errors.New("smartcar connection configuration mismatch")
	}
	return
}

type Candidate struct {
	CandidateID string `json:"candidateId"`
	Make        string `json:"make"`
	Model       string `json:"model"`
	Year        int    `json:"year"`
}
type privateCandidate struct {
	Public   Candidate
	Identity identity
}
type sessionData struct {
	Phase, Intent, StateHash, ExternalID, ExpectedConnectionID, RemoteVehicleID string
	Candidates                                                                  []privateCandidate
}
type SessionResult struct {
	SessionID        string      `json:"sessionId"`
	State            string      `json:"state"`
	AuthorizationURL string      `json:"authorizationUrl,omitempty"`
	CallbackScheme   string      `json:"callbackScheme,omitempty"`
	ExpiresAt        time.Time   `json:"expiresAt"`
	Candidates       []Candidate `json:"candidates"`
}
type Completion struct {
	State      string `json:"state"`
	UserID     string `json:"userId,omitempty"`
	VehicleID  string `json:"vehicleId,omitempty"`
	ExternalID string `json:"externalId,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (s *Service) Begin(ctx context.Context, vehicleID, intent string) (SessionResult, error) {
	if intent != "connect" && intent != "reconnect" {
		return SessionResult{}, ErrInvalid
	}
	if _, err := s.store.Vehicle(ctx, vehicleID); err != nil {
		return SessionResult{}, err
	}
	state := randomValue()
	data := sessionData{Phase: "awaiting_authorization", Intent: intent, StateHash: fingerprint(state), ExternalID: randomValue()}
	connection, err := s.store.SmartcarConnection(ctx, vehicleID)
	if err == nil {
		if intent != "reconnect" {
			return SessionResult{}, garage.ErrSmartcarConflict
		}
		i, e := s.identity(connection)
		if e != nil {
			return SessionResult{}, e
		}
		data.ExpectedConnectionID = connection.ID
		data.RemoteVehicleID = i.VehicleID
	} else if !errors.Is(err, sql.ErrNoRows) {
		return SessionResult{}, err
	} else if intent == "reconnect" {
		return SessionResult{}, ErrInvalid
	}
	q := url.Values{"application_id": {s.applicationID}, "redirect_uri": {s.redirectURI}, "response_type": {"none"}, "state": {state}, "external_id": {data.ExternalID}, "mode": {s.mode}}
	path := "/oauth/authorize"
	if intent == "reconnect" {
		path = "/oauth/reauthenticate"
		q.Set("vehicle_id", data.RemoteVehicleID)
		q.Set("response_type", "vehicle_id")
	} else {
		q.Set("scope", "read_vehicle_info read_odometer read_fuel read_engine_oil read_tires read_location read_speedometer")
	}
	result, err := s.saveNewSession(ctx, vehicleID, data)
	if err != nil {
		return result, err
	}
	result.AuthorizationURL = "https://connect.smartcar.com" + path + "?" + q.Encode()
	result.CallbackScheme = "sc" + s.applicationID
	return result, nil
}
func (s *Service) saveNewSession(ctx context.Context, vehicleID string, data sessionData) (SessionResult, error) {
	v := garage.SmartcarSession{ID: garage.NewID(), VehicleID: vehicleID, ExpiresAt: time.Now().UTC().Add(15 * time.Minute)}
	var err error
	v.Encrypted, err = s.seal("session", v.ID, data)
	if err != nil {
		return SessionResult{}, err
	}
	if err = s.store.CreateSmartcarSession(ctx, v); err != nil {
		return SessionResult{}, err
	}
	return sessionResult(v, data), nil
}
func sessionResult(v garage.SmartcarSession, data sessionData) SessionResult {
	r := SessionResult{SessionID: v.ID, State: data.Phase, ExpiresAt: v.ExpiresAt, Candidates: []Candidate{}}
	for _, c := range data.Candidates {
		r.Candidates = append(r.Candidates, c.Public)
	}
	return r
}
func (s *Service) session(ctx context.Context, vehicleID, id string) (v garage.SmartcarSession, data sessionData, err error) {
	v, err = s.store.SmartcarSession(ctx, id, vehicleID)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSession
	}
	if err != nil {
		return
	}
	if !v.ExpiresAt.After(time.Now()) {
		err = ErrSession
		return
	}
	err = s.open("session", id, v.Encrypted, &data)
	return
}
func (s *Service) Session(ctx context.Context, vehicleID, id string) (SessionResult, error) {
	v, data, err := s.session(ctx, vehicleID, id)
	if err != nil {
		return SessionResult{}, err
	}
	return sessionResult(v, data), nil
}
func (s *Service) Complete(ctx context.Context, vehicleID, id string, input Completion) (SessionResult, error) {
	v, data, err := s.session(ctx, vehicleID, id)
	if err != nil {
		return SessionResult{}, err
	}
	actual := fingerprint(input.State)
	if data.Phase != "awaiting_authorization" || len(input.State) > 128 || subtle.ConstantTimeCompare([]byte(actual), []byte(data.StateHash)) != 1 {
		return SessionResult{}, ErrSession
	}
	if input.Error != "" {
		data.Phase = "cancelled"
		cancelled := input.Error == "access_denied" || input.Error == "oem_login_cancelled" || input.Error == "user_cancelled"
		if !cancelled {
			data.Phase = "failed"
		}
		v.Encrypted, err = s.seal("session", id, data)
		if err == nil {
			err = s.store.UpdateSmartcarSession(ctx, v)
		}
		if err != nil {
			return SessionResult{}, err
		}
		if cancelled {
			return sessionResult(v, data), nil
		}
		return SessionResult{}, ErrCancelled
	}
	if input.ExternalID != "" && input.ExternalID != data.ExternalID {
		return SessionResult{}, ErrInvalid
	}
	if data.Intent == "reconnect" {
		if input.VehicleID != data.RemoteVehicleID {
			return SessionResult{}, ErrInvalid
		}
	} else if !validProviderID(input.UserID) {
		return SessionResult{}, ErrInvalid
	}
	remote, err := s.client.connections(ctx, input.UserID, data.ExternalID, s.mode)
	if err != nil {
		return SessionResult{}, err
	}
	data.Candidates = s.candidates(remote, data.RemoteVehicleID)
	if len(data.Candidates) == 0 {
		return SessionResult{}, &providerError{Code: "provisioning"}
	}
	data.Phase = "awaiting_selection"
	data.StateHash = ""
	v.Encrypted, err = s.seal("session", id, data)
	if err != nil {
		return SessionResult{}, err
	}
	if err = s.store.UpdateSmartcarSession(ctx, v); err != nil {
		return SessionResult{}, err
	}
	return sessionResult(v, data), nil
}
func (s *Service) candidates(remote []remoteConnection, only string) []privateCandidate {
	out := []privateCandidate{}
	for _, r := range remote {
		vehicle := r.Relationships.Vehicle.Data.ID
		if only != "" && vehicle != only {
			continue
		}
		out = append(out, privateCandidate{Candidate{garage.NewID(), r.Attributes.Vehicle.Make, r.Attributes.Vehicle.Model, r.Attributes.Vehicle.Year}, identity{s.applicationID, r.Attributes.User.ID, vehicle, r.Attributes.User.ExternalID, s.mode}})
	}
	return out
}
func (s *Service) Adopt(ctx context.Context, vehicleID, userID string) (SessionResult, error) {
	if !validProviderID(userID) {
		return SessionResult{}, ErrInvalid
	}
	if _, err := s.store.Vehicle(ctx, vehicleID); err != nil {
		return SessionResult{}, err
	}
	if _, err := s.store.SmartcarConnection(ctx, vehicleID); err == nil {
		return SessionResult{}, garage.ErrSmartcarConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return SessionResult{}, err
	}
	remote, err := s.client.connections(ctx, userID, "", s.mode)
	if err != nil {
		return SessionResult{}, err
	}
	if len(remote) == 0 {
		return SessionResult{}, &providerError{Code: "provisioning"}
	}
	return s.saveNewSession(ctx, vehicleID, sessionData{Phase: "awaiting_selection", Intent: "adopt", Candidates: s.candidates(remote, "")})
}
func (s *Service) Bind(ctx context.Context, vehicleID, id, candidateID string) (garage.SmartcarStatus, error) {
	v, data, err := s.session(ctx, vehicleID, id)
	if err != nil {
		return garage.SmartcarStatus{}, err
	}
	if data.Phase != "awaiting_selection" {
		return garage.SmartcarStatus{}, ErrSession
	}
	var selected *privateCandidate
	for _, c := range data.Candidates {
		if c.Public.CandidateID == candidateID {
			copy := c
			selected = &copy
			break
		}
	}
	if selected == nil {
		return garage.SmartcarStatus{}, ErrInvalid
	}
	remote, err := s.client.connections(ctx, selected.Identity.UserID, "", s.mode)
	if err != nil {
		return garage.SmartcarStatus{}, err
	}
	verified := false
	for _, r := range remote {
		if r.Relationships.Vehicle.Data.ID == selected.Identity.VehicleID && (data.Intent == "adopt" || r.Attributes.User.ExternalID == data.ExternalID) {
			verified = true
		}
	}
	if !verified {
		return garage.SmartcarStatus{}, ErrSession
	}
	connection := garage.SmartcarConnection{ID: garage.NewID(), VehicleID: vehicleID, RemoteKey: s.remoteKey(selected.Identity.VehicleID), NextAttemptAt: time.Now().UTC()}
	connection.Status = garage.SmartcarStatus{State: "provisioning", ConnectionID: connection.ID, SupportedMetrics: []string{}, NextAttemptAt: &connection.NextAttemptAt}
	connection.Encrypted, err = s.seal("connection", connection.ID, selected.Identity)
	if err != nil {
		return garage.SmartcarStatus{}, err
	}
	if err = s.store.BindSmartcar(ctx, v, data.ExpectedConnectionID, connection); err != nil {
		return garage.SmartcarStatus{}, err
	}
	s.notify()
	return connection.Status, nil
}
func (s *Service) Detach(ctx context.Context, vehicleID string) error {
	if _, err := s.store.Vehicle(ctx, vehicleID); err != nil {
		return err
	}
	return s.store.DetachSmartcar(ctx, vehicleID)
}
func (s *Service) Sync(ctx context.Context, vehicleID string) (garage.SmartcarStatus, error) {
	status, err := s.Status(ctx, vehicleID)
	if err != nil {
		return garage.SmartcarStatus{}, err
	}
	if status.State == "reconnect_required" {
		return status, ErrReconnect
	}
	if err := s.store.RequestSmartcarSync(ctx, vehicleID, time.Now()); err != nil {
		return garage.SmartcarStatus{}, err
	}
	s.notify()
	return s.Status(ctx, vehicleID)
}
func (s *Service) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// PublicError returns bounded vocabulary only; provider messages can contain vehicle and account data.
func PublicError(err error) (int, string) {
	switch {
	case errors.Is(err, ErrInvalid):
		return 400, "invalid smartcar request"
	case errors.Is(err, ErrSession):
		return 409, "smartcar session expired or already used"
	case errors.Is(err, ErrCancelled):
		return 400, "smartcar authorization was not completed"
	case errors.Is(err, ErrReconnect):
		return 409, "smartcar reconnection is required"
	case errors.Is(err, garage.ErrSmartcarConflict):
		return 409, "smartcar connection changed or vehicle already paired"
	case errors.Is(err, garage.ErrNotFound), errors.Is(err, sql.ErrNoRows):
		return 404, "smartcar connection or vehicle not found"
	}
	var p *providerError
	if errors.As(err, &p) {
		if p.Code == "rate_limited" {
			return 429, "smartcar rate limited; retry later"
		}
		if p.Code == "provisioning" {
			return 503, "smartcar connection is still provisioning"
		}
		return 502, "smartcar temporarily unavailable"
	}
	return 500, "smartcar operation failed"
}
