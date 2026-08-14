package agentcore

// CustomMessage is a projector-produced message whose role is "custom". It is
// kept distinct from a user message so TUI/ACP can render it differently; the
// provider conversion turns it into a user message.
type CustomMessage struct {
	RoleField  string      `json:"role"`
	CustomType string      `json:"customType"`
	Content    ContentList `json:"content"`
	Display    bool        `json:"display,omitempty"`
	Details    any         `json:"details,omitempty"`
	Timestamp  int64       `json:"timestamp"`
}

func (CustomMessage) isMessage()     {}
func (m CustomMessage) Role() string { return RoleCustom }

// BranchSummaryMessage records the summary of a branch the conversation came
// back from. It is converted to a user text block for the LLM.
type BranchSummaryMessage struct {
	RoleField string `json:"role"`
	Summary   string `json:"summary"`
	FromID    string `json:"fromId,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

func (BranchSummaryMessage) isMessage()     {}
func (m BranchSummaryMessage) Role() string { return RoleBranchSummary }

const (
	// RoleCustom is the projector-produced custom-message role.
	RoleCustom = "custom"
	// RoleBranchSummary marks a persisted branch-summary checkpoint.
	RoleBranchSummary = "branchSummary"
)

// BranchSummaryPrefix / BranchSummarySuffix wrap a branch summary when it is
// rendered into an LLM user message, matching pi's constants.
const (
	BranchSummaryPrefix = "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n"
	BranchSummarySuffix = "</summary>"
)

// AsUserMessage renders a branch summary checkpoint as the user text message
// that stands in for the branch history when building the LLM request.
func (m BranchSummaryMessage) AsUserMessage() UserMessage {
	return UserMessage{
		RoleField: RoleUser,
		Content:   ContentList{NewTextContent(BranchSummaryPrefix + m.Summary + BranchSummarySuffix)},
		Timestamp: m.Timestamp,
	}
}
