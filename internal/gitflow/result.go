package gitflow

// Status distinguishes a preview from a finished or stopped Git flow.
type Status string

const (
	StatusPreview  Status = "preview"
	StatusSuccess  Status = "success"
	StatusBlocked  Status = "blocked"
	StatusConflict Status = "conflict"
	StatusPartial  Status = "partial"
)

const (
	ModePreview = "preview"
	ModeExecute = "execute"
)

// Result is the structured outcome of every flow.
// A preview describes effects and carries a confirmation string.
// That string authorizes execution only when it still matches the repository.
type Result struct {
	Status       Status      `json:"status"`
	Operation    string      `json:"operation"`
	Repository   string      `json:"repository,omitempty"`
	Head         string      `json:"head,omitempty"`
	Message      string      `json:"message"`
	Reasons      []string    `json:"reasons,omitempty"`
	Effects      []string    `json:"effects,omitempty"`
	Confirmation string      `json:"confirmation,omitempty"`
	RemoteOID    string      `json:"remote_oid,omitempty"`
	LocalOID     string      `json:"local_oid,omitempty"`
	SourceOID    string      `json:"source_oid,omitempty"`
	Branches     []string    `json:"branches,omitempty"`
	Dropped      []string    `json:"dropped,omitempty"`
	Worktree     string      `json:"worktree,omitempty"`
	NextSteps    []string    `json:"next_steps,omitempty"`
	Changes      []string    `json:"changes,omitempty"`
	Details      *Inspection `json:"details,omitempty"`
}

// Inspection is a read-only snapshot of one work tree.
type Inspection struct {
	Repository string         `json:"repository"`
	Head       string         `json:"head,omitempty"`
	Detached   bool           `json:"detached,omitempty"`
	Dirty      bool           `json:"dirty,omitempty"`
	Changes    []string       `json:"changes,omitempty"`
	InProgress string         `json:"in_progress,omitempty"`
	Worktrees  []WorktreeInfo `json:"worktrees,omitempty"`
	Base       *Divergence    `json:"base,omitempty"`
}

// WorktreeInfo is one linked checkout of the repository.
type WorktreeInfo struct {
	Path     string `json:"path"`
	Branch   string `json:"branch,omitempty"`
	Detached bool   `json:"detached,omitempty"`
	Head     string `json:"head,omitempty"`
}

// Divergence compares one local branch with the branch of the same name on a remote.
type Divergence struct {
	Branch    string `json:"branch"`
	Remote    string `json:"remote"`
	LocalOID  string `json:"local_oid,omitempty"`
	RemoteOID string `json:"remote_oid,omitempty"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	Relation  string `json:"relation"`
}
