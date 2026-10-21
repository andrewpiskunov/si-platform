package game

// Appearance pins an immutable theme revision, or names a platform builtin.
// No colors, file URLs, or author credentials travel in room snapshots.
type Appearance struct {
	Builtin  string `json:"builtin,omitempty"`
	ThemeID  string `json:"theme_id,omitempty"`
	Revision int    `json:"revision,omitempty"`
	Hash     string `json:"hash,omitempty"`
}

func (a Appearance) Validate() error {
	if a.Builtin != "" {
		if (a.Builtin == "classic" || a.Builtin == "pirate" || a.Builtin == "anime") && a.ThemeID == "" && a.Revision == 0 && a.Hash == "" {
			return nil
		}
	} else if appearanceHex(a.ThemeID, 32) && a.Revision >= 1 && a.Revision <= 100 && appearanceHex(a.Hash, 64) {
		return nil
	}
	return Fault("invalid_appearance")
}
func appearanceHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

type AppearancePayload struct {
	Appearance Appearance `json:"appearance"`
}
