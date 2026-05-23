package research

import "github.com/hu-quan-er/eino_research/internal/search"

type ResearchResult struct {
	Question          string             `json:"question"`
	Answer            Answer             `json:"answer"`
	Plan              ResearchTodoPlan   `json:"plan"`
	SectionExecutions []SectionExecution `json:"section_executions"`
	TodoExecutions    []TodoExecution    `json:"todo_executions"`
	Sources           []search.Source    `json:"sources"`
	Documents         []SourceDocument   `json:"documents,omitempty"`
	Metadata          Metadata           `json:"metadata"`
	Error             *RunError          `json:"error,omitempty"`

	LegacyPlan    *ResearchPlan   `json:"legacy_plan,omitempty"`
	ExecutedSteps []StepExecution `json:"executed_steps,omitempty"`
}

type Answer struct {
	Markdown    string   `json:"markdown"`
	Summary     string   `json:"summary"`
	KeyFindings []string `json:"key_findings"`
	Limitations []string `json:"limitations"`
}

type StepExecution struct {
	Step              ResearchStep       `json:"step"`
	ResearcherResults []ResearcherResult `json:"researcher_results"`
	Summary           string             `json:"summary"`
	Gaps              []string           `json:"gaps,omitempty"`
	Sources           []search.Source    `json:"sources"`
	Documents         []SourceDocument   `json:"documents,omitempty"`
}

type ResearcherResult struct {
	Role      string           `json:"role"`
	Focus     string           `json:"focus"`
	Queries   []string         `json:"queries"`
	Findings  []Finding        `json:"findings"`
	Sources   []search.Source  `json:"sources"`
	Documents []SourceDocument `json:"documents,omitempty"`
	Errors    []string         `json:"errors,omitempty"`
}

type Finding struct {
	Claim        string        `json:"claim"`
	Rationale    string        `json:"rationale"`
	SourceIDs    []string      `json:"source_ids"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type EvidenceRef struct {
	SourceID string `json:"source_id"`
	ChunkID  string `json:"chunk_id,omitempty"`
	Quote    string `json:"quote,omitempty"`
}

type SourceDocument struct {
	ID       string        `json:"id"`
	SourceID string        `json:"source_id"`
	Title    string        `json:"title,omitempty"`
	URL      string        `json:"url"`
	Provider string        `json:"provider,omitempty"`
	Query    string        `json:"query,omitempty"`
	Chunks   []SourceChunk `json:"chunks"`
}

type SourceChunk struct {
	ID         string `json:"id"`
	DocumentID string `json:"document_id"`
	SourceID   string `json:"source_id"`
	Text       string `json:"text"`
	StartChar  int    `json:"start_char"`
	EndChar    int    `json:"end_char"`
}

type Metadata struct {
	Model          string `json:"model"`
	SearchProvider string `json:"search_provider"`
	MaxIterations  int    `json:"max_iterations"`
	StartedAt      string `json:"started_at"`
	CompletedAt    string `json:"completed_at"`
	DurationMS     int64  `json:"duration_ms"`
}

type RunError struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
}
