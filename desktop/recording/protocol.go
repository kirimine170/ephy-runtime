package recording

const Version = "2.0"

type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
type Auth struct {
	KeyID string `json:"key_id"`
	MAC   string `json:"mac"`
}
type Target struct {
	DocID    string `json:"doc_id"`
	Revision int64  `json:"revision"`
	SHA256   string `json:"sha256"`
}
type EventRef struct {
	EventID       string `json:"event_id"`
	EventRevision int64  `json:"event_revision"`
}
type SourceRef struct {
	DocID          string     `json:"doc_id"`
	Revision       int64      `json:"revision"`
	SHA256         string     `json:"sha256"`
	ConversationID string     `json:"conversation_id"`
	TurnIDs        []string   `json:"turn_ids"`
	Events         []EventRef `json:"events"`
}
type ASR struct {
	Provider      string `json:"provider"`
	ModelRevision string `json:"model_revision"`
	FinalRevision int64  `json:"final_revision"`
}
type SpeechUnit struct {
	UnitID string `json:"unit_id"`
	State  string `json:"state"`
}
type Assistant struct {
	Generation  string       `json:"generation"`
	Display     string       `json:"display"`
	Playback    string       `json:"playback"`
	SpeechUnits []SpeechUnit `json:"speech_units"`
}
type Event struct {
	ConversationID   string     `json:"conversation_id"`
	ScopeID          string     `json:"scope_id"`
	ProducerID       string     `json:"producer_instance_id"`
	EventID          string     `json:"event_id"`
	Seq              int64      `json:"event_seq"`
	Revision         int64      `json:"event_revision"`
	TurnID           string     `json:"turn_id"`
	Type             string     `json:"event_type"`
	InputKind        string     `json:"input_kind,omitempty"`
	Text             string     `json:"text"`
	OccurredAt       string     `json:"occurred_at"`
	Timezone         string     `json:"timezone"`
	LocalDate        string     `json:"local_date"`
	ASR              *ASR       `json:"asr,omitempty"`
	Assistant        *Assistant `json:"assistant,omitempty"`
	ConsentEpoch     int64      `json:"consent_epoch"`
	Corrects         *EventRef  `json:"corrects,omitempty"`
	CorrectionReason string     `json:"correction_reason,omitempty"`
}
type Claim struct {
	Class      string      `json:"class"`
	Text       string      `json:"text"`
	SourceRefs []SourceRef `json:"source_refs"`
}
type Derivation struct {
	Kind             string      `json:"kind"`
	InputRefs        []SourceRef `json:"input_refs"`
	ModelID          string      `json:"model_id"`
	ModelRevision    string      `json:"model_revision"`
	TemplateID       string      `json:"template_id"`
	TemplateRevision string      `json:"template_revision"`
	GeneratedAt      string      `json:"generated_at"`
	JobID            string      `json:"job_id"`
	Claims           []Claim     `json:"claims"`
}
type RecordSpec struct {
	Type           string `json:"record_type"`
	Title          string `json:"title"`
	ConversationID string `json:"conversation_id,omitempty"`
	Segment        int64  `json:"segment_no,omitempty"`
	Timezone       string `json:"timezone"`
	LocalDate      string `json:"local_date"`
}
type Proposal struct {
	SchemaVersion   string      `json:"schema_version"`
	CandidateID     string      `json:"candidate_id"`
	Operation       string      `json:"operation"`
	LogicalKey      string      `json:"logical_record_key"`
	ScopeID         string      `json:"scope_id"`
	ProducerID      string      `json:"producer_instance_id"`
	Actor           Actor       `json:"actor"`
	PolicyID        string      `json:"policy_id"`
	PolicyRevision  int64       `json:"policy_revision"`
	ConsentEpoch    int64       `json:"consent_epoch"`
	ScopeGeneration int64       `json:"scope_generation"`
	Record          RecordSpec  `json:"record"`
	Target          *Target     `json:"target"`
	Events          []Event     `json:"events,omitempty"`
	Derivation      *Derivation `json:"derivation,omitempty"`
	CreatedAt       string      `json:"created_at"`
	Auth            Auth        `json:"auth"`
}
type Adoption struct {
	Mode           string `json:"mode"`
	ActorID        string `json:"actor_id"`
	PolicyID       string `json:"policy_id"`
	PolicyRevision int64  `json:"policy_revision"`
	Decision       string `json:"decision"`
}
type Receipt struct {
	SchemaVersion string   `json:"schema_version"`
	CandidateID   string   `json:"candidate_id"`
	ProposalHash  string   `json:"proposal_hash"`
	Status        string   `json:"status"`
	Applied       Target   `json:"applied"`
	EventIDs      []string `json:"applied_event_ids"`
	Adoption      Adoption `json:"adoption"`
}
