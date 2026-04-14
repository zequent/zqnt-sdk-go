package domains

import "time"

// Capability describes a single command that an asset may or may not support.
type Capability struct {
	Command           string
	Description       string
	Available         bool
	UnavailableReason *string
	Metadata          map[string]string
}

// CurrentCapabilities is the response from GetCapabilities.
type CurrentCapabilities struct {
	SN           string
	AssetType    string
	Capabilities []Capability
	Timestamp    time.Time
}

// EmptyCapabilities returns an empty CurrentCapabilities for a given serial number.
// Used by the default UnimplementedEdgeAdapter.GetCapabilities implementation.
func EmptyCapabilities(sn string) *CurrentCapabilities {
	return &CurrentCapabilities{
		SN:           sn,
		Capabilities: []Capability{},
		Timestamp:    time.Now(),
	}
}
