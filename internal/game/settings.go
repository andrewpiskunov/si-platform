package game

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// RoomSettings controls admission and an optional external voice-room link.
type RoomSettings struct {
	RequireReady bool   `json:"require_ready,omitempty"`
	Visibility   string `json:"visibility"`
	VoiceURL     string `json:"voice_url"`
}

// UnmarshalJSON distinguishes omitted optional strings from explicit null and
// keeps nested settings strict even when decoded through an outer HTTP payload.
func (s *RoomSettings) UnmarshalJSON(raw []byte) error {
	visibility, voice := "", ""
	ready := false
	fields := struct {
		RequireReady *bool   `json:"require_ready"`
		Visibility   *string `json:"visibility"`
		VoiceURL     *string `json:"voice_url"`
	}{Visibility: &visibility, VoiceURL: &voice, RequireReady: &ready}
	data := bytes.TrimSpace(raw)
	if len(data) == 0 || data[0] != '{' {
		return Fault("invalid_settings")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&fields) != nil || decoder.Decode(new(any)) != io.EOF || fields.Visibility == nil || fields.VoiceURL == nil || fields.RequireReady == nil {
		return Fault("invalid_settings")
	}
	*s = RoomSettings{Visibility: *fields.Visibility, VoiceURL: *fields.VoiceURL, RequireReady: *fields.RequireReady}
	return nil
}

// Normalized preserves the private admission of snapshots predating settings.
func (s RoomSettings) Normalized() RoomSettings {
	if s.Visibility == "" {
		s.Visibility = "private"
	}
	return s
}

// Validate requires explicit visibility; callers default only omitted settings.
func (s RoomSettings) Validate() error {
	if s.Visibility != "private" && s.Visibility != "public" {
		return Fault("invalid_settings")
	}
	if s.VoiceURL == "" {
		return nil
	}
	if len(s.VoiceURL) > 2048 || !utf8.ValidString(s.VoiceURL) || strings.ContainsFunc(s.VoiceURL, func(r rune) bool {
		return r == '\\' || unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return Fault("invalid_settings")
	}
	u, err := url.Parse(s.VoiceURL)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" || u.User != nil {
		return Fault("invalid_settings")
	}
	return nil
}

type SettingsPayload struct {
	Settings RoomSettings `json:"settings"`
}

type LobbyReadyPayload struct {
	Ready *bool `json:"ready"`
}

func lobbyCanStart(s State) bool {
	if s.Phase != Lobby || !s.Settings.RequireReady {
		return true
	}
	count := 0
	for _, p := range s.Participants {
		if p.Role == Player && !p.Removed {
			count++
			if !p.Ready {
				return false
			}
		}
	}
	return count >= 2
}
