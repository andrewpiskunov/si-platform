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
	Visibility string `json:"visibility"`
	VoiceURL   string `json:"voice_url"`
}

// UnmarshalJSON distinguishes omitted optional strings from explicit null and
// keeps nested settings strict even when decoded through an outer HTTP payload.
func (s *RoomSettings) UnmarshalJSON(raw []byte) error {
	visibility, voice := "", ""
	fields := struct {
		Visibility *string `json:"visibility"`
		VoiceURL   *string `json:"voice_url"`
	}{Visibility: &visibility, VoiceURL: &voice}
	data := bytes.TrimSpace(raw)
	if len(data) == 0 || data[0] != '{' {
		return Fault("invalid_settings")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&fields) != nil || decoder.Decode(new(any)) != io.EOF || fields.Visibility == nil || fields.VoiceURL == nil {
		return Fault("invalid_settings")
	}
	*s = RoomSettings{Visibility: *fields.Visibility, VoiceURL: *fields.VoiceURL}
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
