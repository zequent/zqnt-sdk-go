package domains

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
)

// CommandRisk is how much a command changes the world. The platform's safety gate, approvals and
// the agent go by it: MOVE and CRITICAL are high-risk. The values match zqnt.capability.v3.CommandRisk.
type CommandRisk int

const (
	CommandRiskUnspecified CommandRisk = 0
	// CommandRiskObserve reads only (status, a photo of the current view).
	CommandRiskObserve CommandRisk = 1
	// CommandRiskAdjust changes settings; nothing moves (zoom, lens, stream).
	CommandRiskAdjust CommandRisk = 2
	// CommandRiskMove moves the asset or a part of it (takeoff, go-to).
	CommandRiskMove CommandRisk = 3
	// CommandRiskCritical is irreversible or dangerous (jamming, a reboot in flight, landing anywhere).
	CommandRiskCritical CommandRisk = 4
)

// CommandSafety is what a command declares about its risk.
type CommandSafety struct {
	Risk       CommandRisk
	Reversible bool
	// UndoCommandID is the command that undoes this one, e.g. "dock.close_cover" for "dock.open_cover".
	UndoCommandID string
	// Preconditions are plain-language conditions, e.g. "aircraft is airborne".
	Preconditions []string
	// Effects are plain-language effects, e.g. "aircraft climbs to the given altitude".
	Effects []string
}

//go:embed command_catalog.json
var commandCatalogJSON []byte

// CommandCatalogVersion is the version of zqnt-api's command_catalog.json the SDK carries.
var CommandCatalogVersion = commandCatalog().version

type catalogEntry struct {
	CommandID     string `json:"command_id"`
	Risk          string `json:"risk"`
	UndoCommandID string `json:"undo_command_id"`
}

type parsedCatalog struct {
	version  string
	exact    map[string]CommandSafety
	patterns []catalogPattern
}

type catalogPattern struct {
	match  *regexp.Regexp
	safety CommandSafety
}

var (
	catalogOnce   sync.Once
	catalogParsed parsedCatalog
	placeholder   = regexp.MustCompile(`<[a-z_]+>`)
	risks         = map[string]CommandRisk{
		"OBSERVE":  CommandRiskObserve,
		"ADJUST":   CommandRiskAdjust,
		"MOVE":     CommandRiskMove,
		"CRITICAL": CommandRiskCritical,
	}
)

func commandCatalog() parsedCatalog {
	catalogOnce.Do(func() {
		var file struct {
			Version  string         `json:"version"`
			Commands []catalogEntry `json:"commands"`
		}
		if err := json.Unmarshal(commandCatalogJSON, &file); err != nil {
			panic("edge sdk: embedded command_catalog.json: " + err.Error())
		}
		catalogParsed = parsedCatalog{version: file.Version, exact: map[string]CommandSafety{}}
		for _, e := range file.Commands {
			risk, ok := risks[e.Risk]
			if !ok {
				risk = CommandRiskCritical
			}
			safety := CommandSafety{Risk: risk, Reversible: e.UndoCommandID != "", UndoCommandID: e.UndoCommandID}
			if !placeholder.MatchString(e.CommandID) {
				catalogParsed.exact[e.CommandID] = safety
				continue
			}
			parts := placeholder.Split(e.CommandID, -1)
			for i, p := range parts {
				parts[i] = regexp.QuoteMeta(p)
			}
			catalogParsed.patterns = append(catalogParsed.patterns, catalogPattern{
				match:  regexp.MustCompile("^" + strings.Join(parts, "[A-Za-z0-9_]+") + "$"),
				safety: safety,
			})
		}
	})
	return catalogParsed
}

// CatalogSafety is the platform catalog's rating of a standard command id. An id the catalog
// lists without a rating counts as CRITICAL; an id it does not list has none, so the adapter
// declares it.
func CatalogSafety(commandID string) (CommandSafety, bool) {
	c := commandCatalog()
	if s, ok := c.exact[commandID]; ok {
		return s, true
	}
	for _, p := range c.patterns {
		if p.match.MatchString(commandID) {
			return p.safety, true
		}
	}
	return CommandSafety{}, false
}
