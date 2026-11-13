package domain

// Browse statuses. "expired" is an active record whose expiry has passed; it is hidden from search and counts but remains retrievable.
const (
	StatusActive     = "active"
	StatusSuperseded = "superseded"
	StatusArchived   = "archived"
	StatusExpired    = "expired"
)

// MaxBrowseLimit bounds one List page.
const MaxBrowseLimit = 50

// SummaryInput selects one project, or global records when Scope is "global".
type SummaryInput struct {
	Scope      string `json:"scope"`
	ProjectKey string `json:"project_key,omitempty"`
}

// Summary counts one scope's records. Total, ByType and ByHandoffStatus cover active, unexpired records only;
// ByStatus covers every record and always contains all four statuses.
type Summary struct {
	Total           int            `json:"total"`
	ByType          map[string]int `json:"by_type"`
	ByStatus        map[string]int `json:"by_status"`
	ByHandoffStatus map[string]int `json:"by_handoff_status"`
}

// ListInput filters one scope's records. An empty Status means active and unexpired.
// HandoffStatus implies type handoff. Limit defaults to 20 and is capped at MaxBrowseLimit.
type ListInput struct {
	Scope         string `json:"scope"`
	ProjectKey    string `json:"project_key,omitempty"`
	Type          string `json:"type,omitempty"`
	Status        string `json:"status,omitempty"`
	HandoffStatus string `json:"handoff_status,omitempty"`
	After         string `json:"after,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

// MemoryRef is a content-free reference to one memory.
type MemoryRef struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Type          string `json:"type"`
	Status        string `json:"status"`
	Version       int64  `json:"version"`
	UpdatedAt     string `json:"updated_at"`
	Evidence      string `json:"evidence"`
	HandoffStatus string `json:"handoff_status,omitempty"`
}

type ListResult struct {
	Items      []MemoryRef `json:"items"`
	NextCursor string      `json:"next_cursor,omitempty"`
}
