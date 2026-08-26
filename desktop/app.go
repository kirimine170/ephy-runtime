package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"gopkg.in/yaml.v3"
)

type App struct {
	ctx        context.Context
	baseURL    string
	httpClient *http.Client

	mu                 sync.Mutex
	modelLifecycleMu   sync.Mutex
	closing            bool
	workspaceRoot      string
	fastCmd            *exec.Cmd
	fastRunning        bool
	fastLogs           []string
	workCmd            *exec.Cmd
	workRunning        bool
	workLogs           []string
	codeCmd            *exec.Cmd
	codeRunning        bool
	codeLogs           []string
	gatewayCmd         *exec.Cmd
	gatewayRunning     bool
	gatewayLogs        []string
	embeddingCmd       *exec.Cmd
	embeddingRunning   bool
	embeddingLogs      []string
	qdrantLogs         []string
	watchCmd           *exec.Cmd
	watchRunning       bool
	watchLogs          []string
	batchWorkflowState *BatchWorkflowState
}

type HealthResponse struct {
	Status           string   `json:"status"`
	Service          string   `json:"service"`
	ConfiguredModels []string `json:"configured_models"`
	WebSearchEnabled bool     `json:"web_search_enabled"`
}

type ModelListResponse struct {
	Object string      `json:"object"`
	Data   []ModelItem `json:"data"`
}

type ModelItem struct {
	ID           string `json:"id"`
	Object       string `json:"object"`
	OwnedBy      string `json:"owned_by"`
	BackendModel string `json:"backend_model"`
}

type ChatRequest struct {
	Mode        string   `json:"mode"`
	Prompt      string   `json:"prompt"`
	Project     string   `json:"project,omitempty"`
	SourcePath  string   `json:"source_path,omitempty"`
	SourceScope string   `json:"source_scope,omitempty"`
	TopK        int      `json:"top_k,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Temperature float64  `json:"temperature"`
	MaxTokens   int      `json:"max_tokens"`
	RequestID   string   `json:"request_id,omitempty"`
	Stream      bool     `json:"stream,omitempty"`
	WebSearch   bool     `json:"web_search,omitempty"`
	WebPlanID   string   `json:"web_search_plan_id,omitempty"`
}

type GatewayChatRequest struct {
	Model       string           `json:"model"`
	Messages    []GatewayMessage `json:"messages"`
	Temperature *float64         `json:"temperature,omitempty"`
	MaxTokens   *int             `json:"max_tokens,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
	Metadata    GatewayMetadata  `json:"metadata"`
}

type GatewayMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type GatewayMetadata struct {
	Mode        string   `json:"mode"`
	Project     string   `json:"project,omitempty"`
	SourcePath  string   `json:"source_path,omitempty"`
	SourceScope string   `json:"source_scope,omitempty"`
	TopK        int      `json:"top_k,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	WebSearch   bool     `json:"web_search,omitempty"`
	WebPlanID   string   `json:"web_search_plan_id,omitempty"`
}

type ChatResponse struct {
	Answer          string           `json:"answer"`
	Thinking        string           `json:"thinking,omitempty"`
	Sources         []SearchItem     `json:"sources,omitempty"`
	FinishReason    string           `json:"finish_reason,omitempty"`
	Raw             any              `json:"raw"`
	WebSearchStatus *WebSearchStatus `json:"web_search_status,omitempty"`
}

type WebSearchPlanResponse struct {
	PlanID         string   `json:"plan_id"`
	Decision       string   `json:"decision"`
	OutboundQuery  string   `json:"outbound_query"`
	RiskCategories []string `json:"risk_categories"`
	ExpiresAt      string   `json:"expires_at"`
}

type WebSearchStatus struct {
	Status      string `json:"status"`
	Detail      string `json:"detail,omitempty"`
	SourceCount int    `json:"source_count"`
}

type RoutePlanRequest struct {
	Mode   string `json:"mode"`
	Prompt string `json:"prompt"`
}

type RoutePlanResponse struct {
	Mode         string `json:"mode"`
	ModelAlias   string `json:"model_alias"`
	Provider     string `json:"provider"`
	BackendModel string `json:"backend_model"`
	BaseURL      string `json:"base_url"`
	MaxContext   int    `json:"max_context"`
}

type IngestRequest struct {
	Paths     []string `json:"paths"`
	Project   string   `json:"project,omitempty"`
	Recursive bool     `json:"recursive"`
	Tags      []string `json:"tags,omitempty"`
}

type EmbeddingRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type IndexBrowseRequest struct {
	Project     string `json:"project,omitempty"`
	SourceQuery string `json:"source_query,omitempty"`
	Limit       int    `json:"limit"`
}

type IndexSourceRequest struct {
	Project    string `json:"project,omitempty"`
	SourcePath string `json:"source_path"`
	Limit      int    `json:"limit"`
}

type SearchRequest struct {
	Query      string   `json:"query"`
	Project    string   `json:"project,omitempty"`
	SourcePath string   `json:"source_path,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	TopK       int      `json:"top_k"`
}

type QueryRequest struct {
	Query      string   `json:"query"`
	Project    string   `json:"project,omitempty"`
	SourcePath string   `json:"source_path,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	TopK       int      `json:"top_k"`
	Answer     bool     `json:"answer"`
	RequestID  string   `json:"request_id,omitempty"`
	Stream     bool     `json:"stream,omitempty"`
}

type EvalRequest struct {
	DatasetPath string `json:"dataset_path"`
	Project     string `json:"project,omitempty"`
	SourcePath  string `json:"source_path,omitempty"`
	TopK        int    `json:"top_k"`
	WithAnswer  bool   `json:"with_answer"`
}

type SearchResponse struct {
	Query   string       `json:"query"`
	Results []SearchItem `json:"results"`
}

type SearchItem struct {
	ChunkID            string   `json:"chunk_id"`
	SourcePath         string   `json:"source_path"`
	HeadingPath        []string `json:"heading_path"`
	Project            string   `json:"project"`
	Tags               []string `json:"tags"`
	ChunkText          string   `json:"chunk_text"`
	Score              float64  `json:"score"`
	SourceType         string   `json:"source_type,omitempty"`
	SourceID           string   `json:"source_id,omitempty"`
	Title              string   `json:"title,omitempty"`
	URL                string   `json:"url,omitempty"`
	Snippet            string   `json:"snippet,omitempty"`
	TrustLevel         string   `json:"trust_level,omitempty"`
	InjectionSuspected bool     `json:"injection_suspected,omitempty"`
}

type QueryResponse struct {
	Answer       string       `json:"answer"`
	Thinking     string       `json:"thinking,omitempty"`
	Sources      []SearchItem `json:"sources"`
	FinishReason string       `json:"finish_reason,omitempty"`
}

type ChatStreamEvent struct {
	RequestID       string           `json:"request_id"`
	Kind            string           `json:"kind"`
	Channel         string           `json:"channel,omitempty"`
	Delta           string           `json:"delta,omitempty"`
	Thinking        string           `json:"thinking,omitempty"`
	Answer          string           `json:"answer,omitempty"`
	Sources         []SearchItem     `json:"sources,omitempty"`
	FinishReason    string           `json:"finish_reason,omitempty"`
	Error           string           `json:"error,omitempty"`
	WebSearchStatus *WebSearchStatus `json:"web_search_status,omitempty"`
}

type EvalResponse struct {
	DatasetPath    string         `json:"dataset_path"`
	TotalCases     int            `json:"total_cases"`
	SourceHitRate  float64        `json:"source_hit_rate"`
	KeywordHitRate *float64       `json:"keyword_hit_rate"`
	AverageLatency *float64       `json:"average_latency_ms"`
	TotalPrompt    *int           `json:"total_prompt_tokens"`
	TotalComplete  *int           `json:"total_completion_tokens"`
	TotalTokens    *int           `json:"total_tokens"`
	Results        []EvalCaseItem `json:"results"`
}

type EvalCaseItem struct {
	ID             string   `json:"id"`
	Query          string   `json:"query"`
	MatchedSources []string `json:"matched_sources"`
	SourceHit      bool     `json:"source_hit"`
	KeywordHit     *bool    `json:"keyword_hit"`
	Answer         string   `json:"answer"`
	TopSource      string   `json:"top_source"`
	LatencyMS      *float64 `json:"latency_ms"`
	PromptTokens   *int     `json:"prompt_tokens"`
	CompletionTok  *int     `json:"completion_tokens"`
	TotalTokens    *int     `json:"total_tokens"`
}

type RuntimeStatus struct {
	WorkspaceRoot       string   `json:"workspace_root"`
	FastRunning         bool     `json:"fast_running"`
	FastPID             int      `json:"fast_pid"`
	FastLogs            []string `json:"fast_logs"`
	WorkRunning         bool     `json:"work_running"`
	WorkPID             int      `json:"work_pid"`
	WorkLogs            []string `json:"work_logs"`
	CodeRunning         bool     `json:"code_running"`
	CodePID             int      `json:"code_pid"`
	CodeLogs            []string `json:"code_logs"`
	GatewayRunning      bool     `json:"gateway_running"`
	GatewayPID          int      `json:"gateway_pid"`
	GatewayLogs         []string `json:"gateway_logs"`
	EmbeddingRunning    bool     `json:"embedding_running"`
	EmbeddingPID        int      `json:"embedding_pid"`
	EmbeddingLogs       []string `json:"embedding_logs"`
	QdrantRunning       bool     `json:"qdrant_running"`
	QdrantDetail        string   `json:"qdrant_detail"`
	QdrantLogs          []string `json:"qdrant_logs"`
	WatchRunning        bool     `json:"watch_running"`
	WatchPID            int      `json:"watch_pid"`
	WatchLogs           []string `json:"watch_logs"`
	ModelsLocalOverride bool     `json:"models_local_override"`
	RagLocalOverride    bool     `json:"rag_local_override"`
	ModelsLocalPath     string   `json:"models_local_path"`
	RagLocalPath        string   `json:"rag_local_path"`
	ConfigSummary       any      `json:"config_summary"`
	RequiredServices    []string `json:"required_services"`
	OptionalServices    []string `json:"optional_services"`
	Warnings            []string `json:"warnings"`
}

type BatchWorkflowResultItem struct {
	PresetName string `json:"preset_name"`
	Status     string `json:"status"`
	Detail     string `json:"detail"`
}

type BatchWorkflowState struct {
	WorkflowLabel   string                    `json:"workflow_label"`
	Status          string                    `json:"status"`
	Running         bool                      `json:"running"`
	CancelRequested bool                      `json:"cancel_requested"`
	Results         []BatchWorkflowResultItem `json:"results"`
}

type BatchPresetWorkflowRequest struct {
	PresetNames []string `json:"preset_names"`
}

type WorkflowStep struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type WorkflowRunResponse struct {
	Workflow   string         `json:"workflow"`
	PresetName string         `json:"preset_name"`
	Status     string         `json:"status"`
	Detail     string         `json:"detail"`
	Steps      []WorkflowStep `json:"steps"`
}

type PresetRecoveryActionRequest struct {
	Preset          ProjectPreset `json:"preset"`
	ActionKind      string        `json:"action_kind"`
	ServiceName     string        `json:"service_name"`
	StepName        string        `json:"step_name"`
	SourceHistoryID string        `json:"source_history_id"`
	SourceWorkflow  string        `json:"source_workflow"`
}

type RuntimeStackActionRequest struct {
	Action string `json:"action"`
}

type RuntimeConfigActionRequest struct {
	Action  string `json:"action"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

type RuntimeServiceActionRequest struct {
	Action string       `json:"action"`
	Watch  WatchRequest `json:"watch"`
}

type runtimeModelsFile struct {
	Models map[string]runtimeModelConfig `yaml:"models"`
}

type runtimeModelConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	BaseURL  string `yaml:"base_url"`
}

type runtimeRagFile struct {
	Rag      runtimeRagConfig      `yaml:"rag"`
	VectorDB runtimeVectorDBConfig `yaml:"vector_db"`
}

type runtimeRagConfig struct {
	EmbeddingProvider string `yaml:"embedding_provider"`
	EmbeddingAlias    string `yaml:"embedding_model_alias"`
	RerankerProvider  string `yaml:"reranker_provider"`
	RerankerAlias     string `yaml:"reranker_model_alias"`
}

type runtimeVectorDBConfig struct {
	Provider   string `yaml:"provider"`
	URL        string `yaml:"url"`
	Collection string `yaml:"collection"`
	StorePath  string `yaml:"store_path"`
}

type RuntimeConfigSummary struct {
	EmbeddingProvider string `json:"embedding_provider"`
	EmbeddingAlias    string `json:"embedding_alias"`
	EmbeddingModel    string `json:"embedding_model"`
	RerankerProvider  string `json:"reranker_provider"`
	RerankerAlias     string `json:"reranker_alias"`
	RerankerModel     string `json:"reranker_model"`
	VectorDBProvider  string `json:"vector_db_provider"`
	VectorDBURL       string `json:"vector_db_url"`
	VectorDBStorePath string `json:"vector_db_store_path"`
}

type SmokeRequest struct {
	GatewayURL    string `json:"gateway_url"`
	SkipQdrant    bool   `json:"skip_qdrant"`
	SkipEmbedding bool   `json:"skip_embedding"`
	SkipReranker  bool   `json:"skip_reranker"`
}

type SmokeCheckItem struct {
	Name   string `json:"name"`
	Ok     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type SmokeResponse struct {
	Ok     bool             `json:"ok"`
	Checks []SmokeCheckItem `json:"checks"`
}

type ReloadConfigResponse struct {
	Status           string   `json:"status"`
	ConfiguredModels []string `json:"configured_models"`
}

type StackActionResponse struct {
	Status string         `json:"status"`
	Steps  map[string]any `json:"steps"`
}

type WatchRequest struct {
	Paths     []string `json:"paths"`
	Project   string   `json:"project,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Interval  float64  `json:"interval"`
	Recursive bool     `json:"recursive"`
}

type LocalConfigFile struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Content string `json:"content"`
}

type SaveLocalConfigRequest struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type LocalConfigNameRequest struct {
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
}

type ProjectPreset struct {
	Name                 string  `json:"name"`
	RuntimeProfile       string  `json:"runtime_profile"`
	WatchPaths           string  `json:"watch_paths"`
	WatchProject         string  `json:"watch_project"`
	WatchInterval        float64 `json:"watch_interval"`
	IngestPaths          string  `json:"ingest_paths"`
	IngestProject        string  `json:"ingest_project"`
	ChatRequestName      string  `json:"chat_request_name"`
	ChatExpectContains   string  `json:"chat_expect_contains"`
	IngestRequestName    string  `json:"ingest_request_name"`
	RagProject           string  `json:"rag_project"`
	RagSourcePath        string  `json:"rag_source_path"`
	RagTopK              int     `json:"rag_top_k"`
	RagRequestName       string  `json:"rag_request_name"`
	RagExpectContains    string  `json:"rag_expect_contains"`
	EvalDataset          string  `json:"eval_dataset"`
	EvalProject          string  `json:"eval_project"`
	EvalSourcePath       string  `json:"eval_source_path"`
	EvalTopK             int     `json:"eval_top_k"`
	EvalWithAnswer       bool    `json:"eval_with_answer"`
	EvalRequestName      string  `json:"eval_request_name"`
	EvalMinSourceHitRate float64 `json:"eval_min_source_hit_rate"`
	WorkflowRunSmoke     bool    `json:"workflow_run_smoke"`
	SmokeSkipQdrant      bool    `json:"smoke_skip_qdrant"`
	SmokeSkipEmbedding   bool    `json:"smoke_skip_embedding"`
	SmokeSkipReranker    bool    `json:"smoke_skip_reranker"`
}

type PresetPathCheck struct {
	Label        string `json:"label"`
	Path         string `json:"path"`
	ResolvedPath string `json:"resolved_path"`
	Kind         string `json:"kind"`
	Required     bool   `json:"required"`
	Exists       bool   `json:"exists"`
	Detail       string `json:"detail"`
}

type PresetServiceCheck struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
}

type PresetValidationResponse struct {
	PresetName       string               `json:"preset_name"`
	Valid            bool                 `json:"valid"`
	Ready            bool                 `json:"ready"`
	Warnings         []string             `json:"warnings"`
	ConfigWarnings   []string             `json:"config_warnings"`
	RequiredServices []string             `json:"required_services"`
	OptionalServices []string             `json:"optional_services"`
	PathChecks       []PresetPathCheck    `json:"path_checks"`
	ServiceChecks    []PresetServiceCheck `json:"service_checks"`
}

type RegressionWatchSettings struct {
	SourceHitDrop  float64 `json:"source_hit_drop"`
	IncludePreset  bool    `json:"include_preset"`
	IncludeDataset bool    `json:"include_dataset"`
}

type RegressionWatchProfile struct {
	Label          string  `json:"label"`
	SourceHitDrop  float64 `json:"sourceHitDrop"`
	IncludePreset  bool    `json:"includePreset"`
	IncludeDataset bool    `json:"includeDataset"`
	Builtin        bool    `json:"builtin"`
}

type SavedRequest struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Model       string `json:"model,omitempty"`
	Input       string `json:"input,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
	Query       string `json:"query,omitempty"`
	Project     string `json:"project,omitempty"`
	SourceQuery string `json:"source_query,omitempty"`
	SourcePath  string `json:"source_path,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	TopK        int    `json:"top_k,omitempty"`
	Answer      bool   `json:"answer,omitempty"`
	Paths       string `json:"paths,omitempty"`
	Recursive   bool   `json:"recursive,omitempty"`
	DatasetPath string `json:"dataset_path,omitempty"`
	WithAnswer  bool   `json:"with_answer,omitempty"`
}

type ExecutionHistoryItem struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Summary   string `json:"summary"`
	Detail    string `json:"detail,omitempty"`
	Payload   string `json:"payload,omitempty"`
}

type ExportResultRequest struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	FileStem string `json:"file_stem,omitempty"`
}

type ExportResultResponse struct {
	Path string `json:"path"`
}

type ExportedFileItem struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	ModTime string `json:"mod_time"`
}

type ExportedFileRequest struct {
	Path string `json:"path"`
}

type ExportedFileContent struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

func NewApp() *App {
	return &App{
		baseURL: "http://127.0.0.1:8000",
		httpClient: &http.Client{
			Timeout: 90 * time.Second,
		},
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.workspaceRoot = detectWorkspaceRoot()
	if os.Getenv("EPHY_START_CONVERSATION") == "1" {
		go func() {
			if _, err := a.startConversation(); err != nil {
				a.mu.Lock()
				a.appendGatewayLog("conversation startup failed: " + err.Error())
				a.mu.Unlock()
			}
		}()
	}
}

func (a *App) GetGatewayURL() string {
	return a.baseURL
}

func (a *App) SetGatewayURL(url string) string {
	trimmed := strings.TrimSpace(url)
	if trimmed != "" {
		a.baseURL = strings.TrimRight(trimmed, "/")
	}
	return a.baseURL
}

func (a *App) Health() (*HealthResponse, error) {
	var response HealthResponse
	err := a.getJSON("/health", &response)
	return &response, err
}

func (a *App) Models() (*ModelListResponse, error) {
	var response ModelListResponse
	err := a.getJSON("/v1/models", &response)
	return &response, err
}

func (a *App) Chat(request ChatRequest) (*ChatResponse, error) {
	mode := request.Mode
	if strings.TrimSpace(mode) == "" {
		mode = "auto"
	}

	payload := GatewayChatRequest{
		Model: "auto",
		Messages: []GatewayMessage{
			{Role: "user", Content: request.Prompt},
		},
		Metadata: GatewayMetadata{
			Mode:        mode,
			Project:     request.Project,
			SourcePath:  request.SourcePath,
			SourceScope: request.SourceScope,
			TopK:        request.TopK,
			Tags:        request.Tags,
			WebSearch:   request.WebSearch,
			WebPlanID:   request.WebPlanID,
		},
	}
	if request.Temperature > 0 {
		payload.Temperature = &request.Temperature
	}
	if request.MaxTokens > 0 {
		payload.MaxTokens = &request.MaxTokens
	}
	if request.Stream {
		payload.Stream = true
		return a.chatStream(payload, request.RequestID)
	}

	var raw map[string]any
	if err := a.postJSON("/v1/chat/completions", payload, &raw); err != nil {
		return nil, err
	}

	return &ChatResponse{
		Answer:          extractChatAnswer(raw),
		Thinking:        extractChatReasoning(raw),
		Sources:         extractChatSources(raw),
		FinishReason:    extractFinishReason(raw),
		Raw:             raw,
		WebSearchStatus: extractWebSearchStatus(raw),
	}, nil
}

func (a *App) PlanWebSearch(query string) (*WebSearchPlanResponse, error) {
	var response WebSearchPlanResponse
	err := a.postJSON("/v1/web/search/plan", map[string]any{"query": query}, &response)
	return &response, err
}

func (a *App) ApproveWebSearch(planID string) (map[string]any, error) {
	var response map[string]any
	err := a.postJSON("/v1/web/search/approve", map[string]any{"plan_id": planID}, &response)
	return response, err
}

func (a *App) OpenWebSource(rawURL string) error {
	parsed, err := validateWebSourceURL(rawURL)
	if err != nil {
		return err
	}
	runtime.BrowserOpenURL(a.ctx, parsed.String())
	return nil
}

func validateWebSourceURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid web source URL")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("web source URL must not contain credentials")
	}
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if hostname == "localhost" || strings.HasSuffix(hostname, ".local") || strings.HasSuffix(hostname, ".internal") || strings.HasSuffix(hostname, ".lan") {
		return nil, fmt.Errorf("private web source URL is not allowed")
	}
	if address := net.ParseIP(hostname); address != nil && (!address.IsGlobalUnicast() || address.IsPrivate()) {
		return nil, fmt.Errorf("private web source URL is not allowed")
	}
	return parsed, nil
}

func (a *App) RoutePlan(request RoutePlanRequest) (*RoutePlanResponse, error) {
	mode := request.Mode
	if strings.TrimSpace(mode) == "" {
		mode = "auto"
	}

	payload := GatewayChatRequest{
		Model: "auto",
		Messages: []GatewayMessage{
			{Role: "user", Content: request.Prompt},
		},
		Metadata: GatewayMetadata{Mode: mode},
	}

	var response RoutePlanResponse
	if err := a.postJSON("/v1/router/plan", payload, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (a *App) Ingest(request IngestRequest) (map[string]any, error) {
	var response map[string]any
	err := a.postJSON("/v1/ingest", request, &response)
	return response, err
}

func (a *App) Embeddings(request EmbeddingRequest) (map[string]any, error) {
	model := strings.TrimSpace(request.Model)
	if model == "" {
		model = "auto"
	}

	payload := map[string]any{
		"model": model,
		"input": request.Input,
	}

	var response map[string]any
	err := a.postJSON("/v1/embeddings", payload, &response)
	return response, err
}

func (a *App) BrowseIndex(request IndexBrowseRequest) (map[string]any, error) {
	limit := request.Limit
	if limit <= 0 {
		limit = 20
	}

	payload := map[string]any{
		"project":      request.Project,
		"source_query": request.SourceQuery,
		"limit":        limit,
	}

	var response map[string]any
	err := a.postJSON("/v1/rag/index", payload, &response)
	return response, err
}

func (a *App) GetIndexSource(request IndexSourceRequest) (map[string]any, error) {
	limit := request.Limit
	if limit <= 0 {
		limit = 100
	}

	payload := map[string]any{
		"project":     request.Project,
		"source_path": request.SourcePath,
		"limit":       limit,
	}

	var response map[string]any
	err := a.postJSON("/v1/rag/source", payload, &response)
	return response, err
}

func (a *App) Search(request SearchRequest) (*SearchResponse, error) {
	var response SearchResponse
	err := a.postJSON("/v1/rag/search", request, &response)
	return &response, err
}

func (a *App) Query(request QueryRequest) (*QueryResponse, error) {
	if request.Stream {
		return a.queryStream(request)
	}
	var response QueryResponse
	err := a.postJSON("/v1/rag/query", request, &response)
	return &response, err
}

func (a *App) chatStream(payload GatewayChatRequest, requestID string) (*ChatResponse, error) {
	var answerBuilder strings.Builder
	var thinkingBuilder strings.Builder
	sources := []SearchItem{}
	finishReason := ""
	var webSearchStatus *WebSearchStatus

	err := a.streamGatewayResponse("/v1/chat/completions", payload, func(eventType string, data string) error {
		if data == "[DONE]" {
			return nil
		}
		if eventType == "error" {
			return parseStreamError(data)
		}
		if eventType == "web_search_status" {
			var status WebSearchStatus
			if err := json.Unmarshal([]byte(data), &status); err != nil {
				return nil
			}
			webSearchStatus = &status
			a.emitChatStreamEvent(ChatStreamEvent{
				RequestID:       requestID,
				Kind:            "web_search_status",
				WebSearchStatus: &status,
			})
			return nil
		}
		if eventType == "sources" {
			var payload struct {
				Sources []SearchItem `json:"sources"`
			}
			if err := json.Unmarshal([]byte(data), &payload); err != nil {
				return nil
			}
			sources = payload.Sources
			a.emitChatStreamEvent(ChatStreamEvent{
				RequestID: requestID,
				Kind:      "sources",
				Sources:   sources,
			})
			return nil
		}
		chunk, err := parseStreamChunk(eventType, data)
		if err != nil {
			return nil
		}
		if chunk.Thinking != "" {
			thinkingBuilder.WriteString(chunk.Thinking)
			a.emitChatStreamEvent(ChatStreamEvent{
				RequestID: requestID,
				Kind:      "delta",
				Channel:   "thinking",
				Delta:     chunk.Thinking,
			})
		}
		if chunk.Answer != "" {
			answerBuilder.WriteString(chunk.Answer)
			a.emitChatStreamEvent(ChatStreamEvent{
				RequestID: requestID,
				Kind:      "delta",
				Channel:   "answer",
				Delta:     chunk.Answer,
			})
		}
		if chunk.FinishReason != "" {
			finishReason = chunk.FinishReason
		}
		return nil
	})
	if err != nil {
		a.emitChatStreamEvent(ChatStreamEvent{RequestID: requestID, Kind: "error", Error: err.Error()})
		return nil, err
	}

	response := &ChatResponse{
		Answer:       answerBuilder.String(),
		Thinking:     thinkingBuilder.String(),
		Sources:      sources,
		FinishReason: finishReason,
		Raw: map[string]any{
			"stream":        true,
			"finish_reason": finishReason,
			"sources":       sources,
		},
		WebSearchStatus: webSearchStatus,
	}
	a.emitChatStreamEvent(ChatStreamEvent{
		RequestID:    requestID,
		Kind:         "done",
		Thinking:     response.Thinking,
		Answer:       response.Answer,
		Sources:      response.Sources,
		FinishReason: response.FinishReason,
	})
	return response, nil
}

func (a *App) queryStream(request QueryRequest) (*QueryResponse, error) {
	var answerBuilder strings.Builder
	var thinkingBuilder strings.Builder
	sources := []SearchItem{}
	finishReason := ""

	err := a.streamGatewayResponse("/v1/rag/query", request, func(eventType string, data string) error {
		if data == "[DONE]" {
			return nil
		}
		if eventType == "error" {
			return parseStreamError(data)
		}
		if eventType == "sources" {
			var payload struct {
				Sources []SearchItem `json:"sources"`
			}
			if err := json.Unmarshal([]byte(data), &payload); err != nil {
				return nil
			}
			sources = payload.Sources
			a.emitChatStreamEvent(ChatStreamEvent{
				RequestID: request.RequestID,
				Kind:      "sources",
				Sources:   sources,
			})
			return nil
		}

		chunk, err := parseStreamChunk(eventType, data)
		if err != nil {
			return nil
		}
		if chunk.Thinking != "" {
			thinkingBuilder.WriteString(chunk.Thinking)
			a.emitChatStreamEvent(ChatStreamEvent{
				RequestID: request.RequestID,
				Kind:      "delta",
				Channel:   "thinking",
				Delta:     chunk.Thinking,
			})
		}
		if chunk.Answer != "" {
			answerBuilder.WriteString(chunk.Answer)
			a.emitChatStreamEvent(ChatStreamEvent{
				RequestID: request.RequestID,
				Kind:      "delta",
				Channel:   "answer",
				Delta:     chunk.Answer,
			})
		}
		if chunk.FinishReason != "" {
			finishReason = chunk.FinishReason
		}
		return nil
	})
	if err != nil {
		a.emitChatStreamEvent(ChatStreamEvent{RequestID: request.RequestID, Kind: "error", Error: err.Error()})
		return nil, err
	}

	response := &QueryResponse{
		Answer:       answerBuilder.String(),
		Thinking:     thinkingBuilder.String(),
		Sources:      sources,
		FinishReason: finishReason,
	}
	a.emitChatStreamEvent(ChatStreamEvent{
		RequestID:    request.RequestID,
		Kind:         "done",
		Thinking:     response.Thinking,
		Answer:       response.Answer,
		Sources:      response.Sources,
		FinishReason: response.FinishReason,
	})
	return response, nil
}

func (a *App) Eval(request EvalRequest) (*EvalResponse, error) {
	var response EvalResponse
	err := a.postJSON("/v1/eval/run", request, &response)
	return &response, err
}

func (a *App) ReloadGatewayConfig() (*ReloadConfigResponse, error) {
	var response ReloadConfigResponse
	if err := a.postJSON("/v1/admin/reload", map[string]any{}, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (a *App) StartCoreStack() (*StackActionResponse, error) {
	steps := map[string]any{}

	if _, err := a.StartFast(); err != nil {
		steps["fast"] = err.Error()
		return &StackActionResponse{Status: "failed", Steps: steps}, err
	}
	steps["fast"] = "started"

	if _, err := a.StartWork(); err != nil {
		steps["work"] = err.Error()
		return &StackActionResponse{Status: "failed", Steps: steps}, err
	}
	steps["work"] = "started"

	if _, err := a.StartCode(); err != nil {
		steps["code"] = err.Error()
		return &StackActionResponse{Status: "failed", Steps: steps}, err
	}
	steps["code"] = "started"

	if _, err := a.StartEmbedding(); err != nil {
		steps["embedding"] = err.Error()
		return &StackActionResponse{Status: "failed", Steps: steps}, err
	}
	steps["embedding"] = "started"

	if _, err := a.StartGateway(); err != nil {
		steps["gateway"] = err.Error()
		return &StackActionResponse{Status: "failed", Steps: steps}, err
	}
	steps["gateway"] = "started"

	return &StackActionResponse{Status: "started", Steps: steps}, nil
}

func (a *App) StartRecommendedStack() (*StackActionResponse, error) {
	steps := map[string]any{}
	configSummary, requiredServices, _, warnings := a.runtimeConfigSummary()
	requiredSet := map[string]struct{}{}
	for _, service := range requiredServices {
		requiredSet[service] = struct{}{}
	}

	if _, err := a.StartFast(); err != nil {
		steps["fast"] = err.Error()
		return &StackActionResponse{Status: "failed", Steps: steps}, err
	}
	steps["fast"] = "started"

	if _, err := a.StartWork(); err != nil {
		steps["work"] = err.Error()
		return &StackActionResponse{Status: "failed", Steps: steps}, err
	}
	steps["work"] = "started"

	if _, err := a.StartCode(); err != nil {
		steps["code"] = err.Error()
		return &StackActionResponse{Status: "failed", Steps: steps}, err
	}
	steps["code"] = "started"

	if _, ok := requiredSet["embedding"]; ok {
		if _, err := a.StartEmbedding(); err != nil {
			steps["embedding"] = err.Error()
			return &StackActionResponse{Status: "failed", Steps: steps}, err
		}
		steps["embedding"] = "started"
	} else {
		steps["embedding"] = "skipped (not required by current config)"
	}

	if _, ok := requiredSet["qdrant"]; ok {
		if _, err := a.StartQdrant(); err != nil {
			steps["qdrant"] = err.Error()
			return &StackActionResponse{Status: "failed", Steps: steps}, err
		}
		steps["qdrant"] = "started"
	} else {
		steps["qdrant"] = "skipped (not required by current config)"
	}

	if _, err := a.StartGateway(); err != nil {
		steps["gateway"] = err.Error()
		return &StackActionResponse{Status: "failed", Steps: steps}, err
	}
	steps["gateway"] = "started"

	status := "started"
	if _, ok := requiredSet["reranker_endpoint"]; ok {
		status = "started_with_manual_steps"
		steps["reranker_endpoint"] = fmt.Sprintf(
			"manual external endpoint required for provider=%s alias=%s model=%s",
			configSummary.RerankerProvider,
			configSummary.RerankerAlias,
			configSummary.RerankerModel,
		)
	}
	if len(warnings) > 0 {
		status = "started_with_warnings"
		steps["warnings"] = warnings
	}

	return &StackActionResponse{Status: status, Steps: steps}, nil
}

func (a *App) StopCoreStack() (*StackActionResponse, error) {
	steps := map[string]any{}
	var firstErr error

	if _, err := a.StopGateway(); err != nil {
		steps["gateway"] = err.Error()
		if firstErr == nil {
			firstErr = err
		}
	} else {
		steps["gateway"] = "stopped"
	}

	if _, err := a.StopEmbedding(); err != nil {
		steps["embedding"] = err.Error()
		if firstErr == nil {
			firstErr = err
		}
	} else {
		steps["embedding"] = "stopped"
	}

	if _, err := a.StopCode(); err != nil {
		steps["code"] = err.Error()
		if firstErr == nil {
			firstErr = err
		}
	} else {
		steps["code"] = "stopped"
	}

	if _, err := a.StopWork(); err != nil {
		steps["work"] = err.Error()
		if firstErr == nil {
			firstErr = err
		}
	} else {
		steps["work"] = "stopped"
	}

	if _, err := a.StopFast(); err != nil {
		steps["fast"] = err.Error()
		if firstErr == nil {
			firstErr = err
		}
	} else {
		steps["fast"] = "stopped"
	}

	status := "stopped"
	if firstErr != nil {
		status = "partial_failure"
	}
	return &StackActionResponse{Status: status, Steps: steps}, firstErr
}

func (a *App) StopRecommendedStack() (*StackActionResponse, error) {
	steps := map[string]any{}
	_, requiredServices, _, warnings := a.runtimeConfigSummary()
	requiredSet := map[string]struct{}{}
	for _, service := range requiredServices {
		requiredSet[service] = struct{}{}
	}
	var firstErr error

	if _, err := a.StopGateway(); err != nil {
		steps["gateway"] = err.Error()
		if firstErr == nil {
			firstErr = err
		}
	} else {
		steps["gateway"] = "stopped"
	}

	if _, ok := requiredSet["qdrant"]; ok {
		if _, err := a.StopQdrant(); err != nil {
			steps["qdrant"] = err.Error()
			if firstErr == nil {
				firstErr = err
			}
		} else {
			steps["qdrant"] = "stopped"
		}
	} else {
		steps["qdrant"] = "skipped (not required by current config)"
	}

	if _, ok := requiredSet["embedding"]; ok {
		if _, err := a.StopEmbedding(); err != nil {
			steps["embedding"] = err.Error()
			if firstErr == nil {
				firstErr = err
			}
		} else {
			steps["embedding"] = "stopped"
		}
	} else {
		steps["embedding"] = "skipped (not required by current config)"
	}

	if _, err := a.StopCode(); err != nil {
		steps["code"] = err.Error()
		if firstErr == nil {
			firstErr = err
		}
	} else {
		steps["code"] = "stopped"
	}

	if _, err := a.StopWork(); err != nil {
		steps["work"] = err.Error()
		if firstErr == nil {
			firstErr = err
		}
	} else {
		steps["work"] = "stopped"
	}

	if _, err := a.StopFast(); err != nil {
		steps["fast"] = err.Error()
		if firstErr == nil {
			firstErr = err
		}
	} else {
		steps["fast"] = "stopped"
	}

	status := "stopped"
	if firstErr != nil {
		status = "partial_failure"
	}
	if len(warnings) > 0 && firstErr == nil {
		status = "stopped_with_warnings"
		steps["warnings"] = warnings
	}
	return &StackActionResponse{Status: status, Steps: steps}, firstErr
}

func (a *App) GetLocalConfigFiles() ([]LocalConfigFile, error) {
	files := []LocalConfigFile{
		a.readLocalConfigFile("models.local.yaml"),
		a.readLocalConfigFile("rag.local.yaml"),
	}
	return files, nil
}

func (a *App) SaveLocalConfigFile(request SaveLocalConfigRequest) ([]LocalConfigFile, error) {
	name := strings.TrimSpace(request.Name)
	if name != "models.local.yaml" && name != "rag.local.yaml" {
		return nil, fmt.Errorf("unsupported local config file: %s", name)
	}
	path := filepath.Join(a.workspaceRoot, "configs", name)
	if err := os.WriteFile(path, []byte(request.Content), 0o644); err != nil {
		return nil, err
	}
	return a.GetLocalConfigFiles()
}

func (a *App) LoadLocalConfigExample(request LocalConfigNameRequest) (LocalConfigFile, error) {
	name := strings.TrimSpace(request.Name)
	if name != "models.local.yaml" && name != "rag.local.yaml" {
		return LocalConfigFile{}, fmt.Errorf("unsupported local config file: %s", name)
	}
	return a.readExampleConfigFile(name), nil
}

func (a *App) DeleteLocalConfigFile(request LocalConfigNameRequest) ([]LocalConfigFile, error) {
	name := strings.TrimSpace(request.Name)
	if name != "models.local.yaml" && name != "rag.local.yaml" {
		return nil, fmt.Errorf("unsupported local config file: %s", name)
	}
	path := filepath.Join(a.workspaceRoot, "configs", name)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return a.GetLocalConfigFiles()
}

func (a *App) GetProjectPresets() ([]ProjectPreset, error) {
	return a.readProjectPresets()
}

func (a *App) SaveProjectPreset(preset ProjectPreset) ([]ProjectPreset, error) {
	name := strings.TrimSpace(preset.Name)
	if name == "" {
		return nil, fmt.Errorf("preset name is required")
	}
	preset.Name = name
	presets, err := a.readProjectPresets()
	if err != nil {
		return nil, err
	}
	replaced := false
	for index, existing := range presets {
		if existing.Name == name {
			presets[index] = preset
			replaced = true
			break
		}
	}
	if !replaced {
		presets = append(presets, preset)
	}
	if err := a.writeProjectPresets(presets); err != nil {
		return nil, err
	}
	return presets, nil
}

func (a *App) DeleteProjectPreset(request LocalConfigNameRequest) ([]ProjectPreset, error) {
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return nil, fmt.Errorf("preset name is required")
	}
	presets, err := a.readProjectPresets()
	if err != nil {
		return nil, err
	}
	filtered := make([]ProjectPreset, 0, len(presets))
	for _, preset := range presets {
		if preset.Name != name {
			filtered = append(filtered, preset)
		}
	}
	if err := a.writeProjectPresets(filtered); err != nil {
		return nil, err
	}
	return filtered, nil
}

func (a *App) ValidateProjectPreset(preset ProjectPreset) (*PresetValidationResponse, error) {
	preset.Name = strings.TrimSpace(preset.Name)

	status := a.GetRuntimeStatus()
	_, requiredServices, optionalServices, configWarnings := a.runtimeConfigSummary()

	response := &PresetValidationResponse{
		PresetName:       fallbackString(preset.Name, "(unnamed preset)"),
		Valid:            true,
		Ready:            true,
		Warnings:         []string{},
		ConfigWarnings:   configWarnings,
		RequiredServices: requiredServices,
		OptionalServices: optionalServices,
		PathChecks:       []PresetPathCheck{},
		ServiceChecks:    []PresetServiceCheck{},
	}

	if preset.Name == "" {
		response.Valid = false
		response.Warnings = append(response.Warnings, "preset name is empty")
	}
	if preset.WatchInterval <= 0 {
		response.Valid = false
		response.Warnings = append(response.Warnings, "watch interval must be greater than 0")
	}
	if preset.RagTopK <= 0 {
		response.Valid = false
		response.Warnings = append(response.Warnings, "rag top_k must be greater than 0")
	}
	if preset.EvalTopK <= 0 {
		response.Valid = false
		response.Warnings = append(response.Warnings, "eval top_k must be greater than 0")
	}

	pathChecks := []PresetPathCheck{}
	addPathCheck := func(label string, rawPath string, kind string, required bool) {
		check := PresetPathCheck{
			Label:    label,
			Path:     strings.TrimSpace(rawPath),
			Kind:     kind,
			Required: required,
			Exists:   false,
			Detail:   "",
		}

		if check.Path == "" {
			if required {
				response.Valid = false
				check.Detail = "required path is empty"
			} else {
				check.Detail = "not configured"
			}
			pathChecks = append(pathChecks, check)
			return
		}

		check.ResolvedPath = a.resolveWorkspacePath(check.Path)
		info, err := os.Stat(check.ResolvedPath)
		if err != nil {
			if os.IsNotExist(err) {
				check.Detail = "path does not exist"
			} else {
				check.Detail = err.Error()
			}
			if required {
				response.Valid = false
			}
			pathChecks = append(pathChecks, check)
			return
		}

		check.Exists = true
		switch kind {
		case "file":
			if info.IsDir() {
				check.Detail = "expected file but found directory"
				if required {
					response.Valid = false
				}
			} else {
				check.Detail = "file exists"
			}
		case "source":
			if info.IsDir() {
				check.Detail = "expected source file but found directory"
				if required {
					response.Valid = false
				}
			} else {
				check.Detail = "source file exists"
			}
		default:
			if info.IsDir() {
				check.Detail = "directory exists"
			} else {
				check.Detail = "file exists"
			}
		}
		pathChecks = append(pathChecks, check)
	}

	watchPaths := splitLines(preset.WatchPaths)
	if len(watchPaths) == 0 {
		response.Valid = false
		response.Warnings = append(response.Warnings, "watch paths are empty")
		pathChecks = append(pathChecks, PresetPathCheck{
			Label:    "watch_paths",
			Path:     "",
			Kind:     "path",
			Required: true,
			Exists:   false,
			Detail:   "required path list is empty",
		})
	} else {
		for index, path := range watchPaths {
			addPathCheck(fmt.Sprintf("watch_path_%d", index+1), path, "path", true)
		}
	}

	ingestPaths := splitLines(preset.IngestPaths)
	if len(ingestPaths) == 0 {
		response.Valid = false
		response.Warnings = append(response.Warnings, "ingest paths are empty")
		pathChecks = append(pathChecks, PresetPathCheck{
			Label:    "ingest_paths",
			Path:     "",
			Kind:     "path",
			Required: true,
			Exists:   false,
			Detail:   "required path list is empty",
		})
	} else {
		for index, path := range ingestPaths {
			addPathCheck(fmt.Sprintf("ingest_path_%d", index+1), path, "path", true)
		}
	}

	addPathCheck("eval_dataset", preset.EvalDataset, "file", true)
	addPathCheck("rag_source_path", preset.RagSourcePath, "source", false)
	addPathCheck("eval_source_path", preset.EvalSourcePath, "source", false)

	response.PathChecks = pathChecks

	serviceStatuses := map[string]string{
		"fast":      "stopped",
		"work":      "stopped",
		"code":      "stopped",
		"gateway":   "stopped",
		"embedding": "stopped",
		"qdrant":    "stopped",
	}
	if status.FastRunning {
		serviceStatuses["fast"] = "running"
	}
	if status.WorkRunning {
		serviceStatuses["work"] = "running"
	}
	if status.CodeRunning {
		serviceStatuses["code"] = "running"
	}
	if status.GatewayRunning {
		serviceStatuses["gateway"] = "running"
	}
	if status.EmbeddingRunning {
		serviceStatuses["embedding"] = "running"
	}
	if status.QdrantRunning {
		serviceStatuses["qdrant"] = "running"
	}

	seenServices := map[string]struct{}{}
	appendServiceCheck := func(name string, required bool) {
		if _, exists := seenServices[name]; exists {
			return
		}
		seenServices[name] = struct{}{}

		serviceStatus := serviceStatuses[name]
		if serviceStatus == "" {
			serviceStatus = "unknown"
		}
		detail := "runtime state unavailable"
		switch serviceStatus {
		case "running":
			detail = "service is running"
		case "stopped":
			detail = "service is not running"
		case "unknown":
			detail = "runtime state is not tracked by the desktop app"
		}
		response.ServiceChecks = append(response.ServiceChecks, PresetServiceCheck{
			Name:     name,
			Required: required,
			Status:   serviceStatus,
			Detail:   detail,
		})
		if required && serviceStatus != "running" {
			response.Ready = false
		}
	}

	for _, service := range requiredServices {
		appendServiceCheck(service, true)
	}
	for _, service := range optionalServices {
		appendServiceCheck(service, false)
	}

	if len(configWarnings) > 0 {
		response.Ready = false
	}
	if !response.Valid {
		response.Ready = false
	}

	return response, nil
}

func (a *App) GetSavedRequests() ([]SavedRequest, error) {
	return a.readSavedRequests()
}

func (a *App) SaveRequest(request SavedRequest) ([]SavedRequest, error) {
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return nil, fmt.Errorf("request name is required")
	}
	kind := strings.TrimSpace(request.Kind)
	if kind != "chat" && kind != "rag" && kind != "ingest" && kind != "eval" && kind != "route" && kind != "embedding" && kind != "index" {
		return nil, fmt.Errorf("unsupported request kind: %s", kind)
	}
	request.Name = name
	request.Kind = kind
	items, err := a.readSavedRequests()
	if err != nil {
		return nil, err
	}
	replaced := false
	for index, existing := range items {
		if existing.Name == name && existing.Kind == kind {
			items[index] = request
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, request)
	}
	if err := a.writeSavedRequests(items); err != nil {
		return nil, err
	}
	return items, nil
}

func (a *App) DeleteSavedRequest(request LocalConfigNameRequest) ([]SavedRequest, error) {
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return nil, fmt.Errorf("request name is required")
	}
	kind := strings.TrimSpace(request.Kind)
	items, err := a.readSavedRequests()
	if err != nil {
		return nil, err
	}
	filtered := make([]SavedRequest, 0, len(items))
	for _, item := range items {
		if item.Name != name || (kind != "" && item.Kind != kind) {
			filtered = append(filtered, item)
		}
	}
	if err := a.writeSavedRequests(filtered); err != nil {
		return nil, err
	}
	return filtered, nil
}

func (a *App) GetExecutionHistory() ([]ExecutionHistoryItem, error) {
	return a.readExecutionHistory()
}

func (a *App) GetBatchWorkflowState() *BatchWorkflowState {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.batchWorkflowState == nil {
		state, err := a.readBatchWorkflowState()
		if err == nil {
			a.batchWorkflowState = state
		}
	}
	return cloneBatchWorkflowState(a.batchWorkflowState)
}

func (a *App) GetBatchPresetSelection() []string {
	items, err := a.readBatchPresetSelection()
	if err != nil {
		return []string{}
	}
	if len(items) == 0 {
		a.mu.Lock()
		state := cloneBatchWorkflowState(a.batchWorkflowState)
		a.mu.Unlock()
		if state == nil {
			state, _ = a.readBatchWorkflowState()
		}
		items = batchPresetSelectionFromState(state)
		if len(items) > 0 {
			_ = a.writeBatchPresetSelection(items)
		}
	}
	return append([]string(nil), items...)
}

func (a *App) SetBatchPresetSelection(presetNames []string) []string {
	normalized := normalizeBatchPresetSelection(presetNames)
	_ = a.writeBatchPresetSelection(normalized)
	return append([]string(nil), normalized...)
}

func (a *App) ClearBatchPresetSelection() []string {
	_ = a.clearBatchPresetSelectionFile()
	return []string{}
}

func (a *App) SetBatchWorkflowState(state BatchWorkflowState) *BatchWorkflowState {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.batchWorkflowState = cloneBatchWorkflowState(&state)
	_ = a.writeBatchWorkflowState(a.batchWorkflowState)
	selection := batchPresetSelectionFromState(a.batchWorkflowState)
	if len(selection) > 0 {
		_ = a.writeBatchPresetSelection(selection)
	}
	return cloneBatchWorkflowState(a.batchWorkflowState)
}

func (a *App) ClearBatchWorkflowState() *BatchWorkflowState {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.batchWorkflowState = nil
	_ = a.clearBatchWorkflowStateFile()
	return nil
}

func (a *App) GetRegressionWatchSettings() RegressionWatchSettings {
	settings, err := a.readRegressionWatchSettings()
	if err != nil {
		return RegressionWatchSettings{
			SourceHitDrop:  0,
			IncludePreset:  true,
			IncludeDataset: true,
		}
	}
	return settings
}

func (a *App) SetRegressionWatchSettings(settings RegressionWatchSettings) RegressionWatchSettings {
	normalized := normalizeRegressionWatchSettings(settings)
	_ = a.writeRegressionWatchSettings(normalized)
	return normalized
}

func (a *App) GetRegressionWatchProfiles() map[string]RegressionWatchProfile {
	profiles, err := a.readRegressionWatchProfiles()
	if err != nil {
		return map[string]RegressionWatchProfile{}
	}
	return cloneRegressionWatchProfiles(profiles)
}

func (a *App) SetRegressionWatchProfiles(profiles map[string]RegressionWatchProfile) map[string]RegressionWatchProfile {
	normalized := normalizeRegressionWatchProfiles(profiles)
	_ = a.writeRegressionWatchProfiles(normalized)
	return cloneRegressionWatchProfiles(normalized)
}

func (a *App) CancelBatchWorkflow() *BatchWorkflowState {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.batchWorkflowState != nil {
		a.batchWorkflowState.CancelRequested = true
		if a.batchWorkflowState.Running {
			a.batchWorkflowState.Status = "cancelling"
		}
	}
	_ = a.writeBatchWorkflowState(a.batchWorkflowState)
	return cloneBatchWorkflowState(a.batchWorkflowState)
}

func (a *App) StartBatchPresetVerification(request BatchPresetWorkflowRequest) (*BatchWorkflowState, error) {
	selectedPresets, state, err := a.startBatchWorkflowState(request.PresetNames, "Batch Preset Verification")
	if err != nil {
		return state, err
	}

	go a.runBatchPresetVerification(selectedPresets)

	return state, nil
}

func (a *App) StartBatchPresetValidate(request BatchPresetWorkflowRequest) (*BatchWorkflowState, error) {
	selectedPresets, state, err := a.startBatchWorkflowState(request.PresetNames, "Batch Preset Validate")
	if err != nil {
		return state, err
	}

	go a.runBatchPresetValidate(selectedPresets)

	return state, nil
}

func (a *App) StartBatchPresetSmoke(request BatchPresetWorkflowRequest) (*BatchWorkflowState, error) {
	selectedPresets, state, err := a.startBatchWorkflowState(request.PresetNames, "Batch Preset Smoke")
	if err != nil {
		return state, err
	}

	go a.runBatchPresetSmoke(selectedPresets)

	return state, nil
}

func (a *App) StartBatchPresetWatch(request BatchPresetWorkflowRequest) (*BatchWorkflowState, error) {
	selectedPresets, state, err := a.startBatchWorkflowState(request.PresetNames, "Batch Preset Watch")
	if err != nil {
		return state, err
	}

	go a.runBatchPresetWatch(selectedPresets)

	return state, nil
}

func (a *App) StartBatchPresetRuntimeStackPrepare(request BatchPresetWorkflowRequest) (*BatchWorkflowState, error) {
	selectedPresets, state, err := a.startBatchWorkflowState(request.PresetNames, "Batch Preset Runtime + Stack Prepare")
	if err != nil {
		return state, err
	}

	go a.runBatchPresetRuntimeStackPrepare(selectedPresets)

	return state, nil
}

func (a *App) StartBatchPresetStackIngestEval(request BatchPresetWorkflowRequest) (*BatchWorkflowState, error) {
	selectedPresets, state, err := a.startBatchWorkflowState(request.PresetNames, "Batch Preset Stack + Ingest + Eval")
	if err != nil {
		return state, err
	}

	go a.runBatchPresetStackIngestEval(selectedPresets)

	return state, nil
}

func (a *App) StartBatchPresetIngestEval(request BatchPresetWorkflowRequest) (*BatchWorkflowState, error) {
	selectedPresets, state, err := a.startBatchWorkflowState(request.PresetNames, "Batch Preset Ingest + Eval")
	if err != nil {
		return state, err
	}

	go a.runBatchPresetIngestEval(selectedPresets)

	return state, nil
}

func (a *App) StartBatchPresetEval(request BatchPresetWorkflowRequest) (*BatchWorkflowState, error) {
	selectedPresets, state, err := a.startBatchWorkflowState(request.PresetNames, "Batch Preset Eval")
	if err != nil {
		return state, err
	}

	go a.runBatchPresetEval(selectedPresets)

	return state, nil
}

func (a *App) StartBatchPresetIngest(request BatchPresetWorkflowRequest) (*BatchWorkflowState, error) {
	selectedPresets, state, err := a.startBatchWorkflowState(request.PresetNames, "Batch Preset Ingest")
	if err != nil {
		return state, err
	}

	go a.runBatchPresetIngest(selectedPresets)

	return state, nil
}

func (a *App) RunPresetVerification(preset ProjectPreset) (*WorkflowRunResponse, error) {
	status, detail, historyStatus, steps := a.runPresetVerificationJob(preset)
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_verification)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("verification | %s", preset.Name),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"workflow":    "preset_verification",
			"preset_name": preset.Name,
			"preset":      preset,
			"steps":       steps,
		}),
	})
	return &WorkflowRunResponse{
		Workflow:   "preset_verification",
		PresetName: preset.Name,
		Status:     status,
		Detail:     detail,
		Steps:      steps,
	}, nil
}

func (a *App) RunPresetValidate(preset ProjectPreset) (*WorkflowRunResponse, error) {
	status, detail, historyStatus, steps := a.runPresetValidateJob(preset)
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_validate)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("validate | %s", preset.Name),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"workflow":    "preset_validate",
			"preset_name": preset.Name,
			"preset":      preset,
			"steps":       steps,
		}),
	})
	return &WorkflowRunResponse{
		Workflow:   "preset_validate",
		PresetName: preset.Name,
		Status:     status,
		Detail:     detail,
		Steps:      steps,
	}, nil
}

func (a *App) RunPresetSmoke(preset ProjectPreset) (*WorkflowRunResponse, error) {
	status, detail, historyStatus, steps := a.runPresetSmokeJob(preset)
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_smoke)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("smoke | %s", preset.Name),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"workflow":    "preset_smoke",
			"preset_name": preset.Name,
			"preset":      preset,
			"steps":       steps,
		}),
	})
	return &WorkflowRunResponse{
		Workflow:   "preset_smoke",
		PresetName: preset.Name,
		Status:     status,
		Detail:     detail,
		Steps:      steps,
	}, nil
}

func (a *App) RunPresetStackIngestEval(preset ProjectPreset) (*WorkflowRunResponse, error) {
	status, detail, historyStatus, steps := a.runPresetStackIngestEvalJob(preset)
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_stack_ingest_eval)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("stack+ingest+eval | %s", preset.Name),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"workflow":    "preset_stack_ingest_eval",
			"preset_name": preset.Name,
			"preset":      preset,
			"steps":       steps,
		}),
	})
	return &WorkflowRunResponse{
		Workflow:   "preset_stack_ingest_eval",
		PresetName: preset.Name,
		Status:     status,
		Detail:     detail,
		Steps:      steps,
	}, nil
}

func (a *App) RunPresetWatch(preset ProjectPreset) (*WorkflowRunResponse, error) {
	status, detail, historyStatus, steps := a.runPresetWatchJob(preset)
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_watch)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("watch | %s", preset.Name),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"workflow":    "preset_watch",
			"preset_name": preset.Name,
			"preset":      preset,
			"steps":       steps,
		}),
	})
	return &WorkflowRunResponse{Workflow: "preset_watch", PresetName: preset.Name, Status: status, Detail: detail, Steps: steps}, nil
}

func (a *App) RunPresetIngest(preset ProjectPreset) (*WorkflowRunResponse, error) {
	status, detail, historyStatus, steps := a.runPresetIngestJob(preset)
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_ingest)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("ingest | %s", preset.Name),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"workflow":    "preset_ingest",
			"preset_name": preset.Name,
			"preset":      preset,
			"steps":       steps,
		}),
	})
	return &WorkflowRunResponse{Workflow: "preset_ingest", PresetName: preset.Name, Status: status, Detail: detail, Steps: steps}, nil
}

func (a *App) RunPresetEval(preset ProjectPreset) (*WorkflowRunResponse, error) {
	status, detail, historyStatus, steps := a.runPresetEvalJob(preset)
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_eval)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("eval | %s", preset.Name),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"workflow":    "preset_eval",
			"preset_name": preset.Name,
			"preset":      preset,
			"steps":       steps,
		}),
	})
	return &WorkflowRunResponse{Workflow: "preset_eval", PresetName: preset.Name, Status: status, Detail: detail, Steps: steps}, nil
}

func (a *App) RunPresetIngestEval(preset ProjectPreset) (*WorkflowRunResponse, error) {
	status, detail, historyStatus, steps := a.runPresetIngestEvalJob(preset)
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_ingest_eval)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("ingest+eval | %s", preset.Name),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"workflow":    "preset_ingest_eval",
			"preset_name": preset.Name,
			"preset":      preset,
			"steps":       steps,
		}),
	})
	return &WorkflowRunResponse{Workflow: "preset_ingest_eval", PresetName: preset.Name, Status: status, Detail: detail, Steps: steps}, nil
}

func (a *App) RunPresetRuntimeStackPrepare(preset ProjectPreset) (*WorkflowRunResponse, error) {
	status, detail, historyStatus, steps := a.runPresetRuntimeStackPrepareJob(preset)
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_runtime_stack_prepare)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("runtime+stack prepare | %s", preset.Name),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"workflow":    "preset_runtime_stack_prepare",
			"preset_name": preset.Name,
			"preset":      preset,
			"steps":       steps,
		}),
	})
	return &WorkflowRunResponse{
		Workflow:   "preset_runtime_stack_prepare",
		PresetName: preset.Name,
		Status:     status,
		Detail:     detail,
		Steps:      steps,
	}, nil
}

func (a *App) RunPresetRecoveryAction(request PresetRecoveryActionRequest) (*WorkflowRunResponse, error) {
	preset := request.Preset
	status, detail, historyStatus, steps := a.runPresetRecoveryActionJob(request)
	summaryAction := strings.TrimSpace(request.StepName)
	if summaryAction == "" {
		summaryAction = fallbackString(strings.TrimSpace(request.ServiceName), strings.TrimSpace(request.ActionKind))
	}
	if summaryAction == "" {
		summaryAction = "recovery"
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_recovery)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("recovery | %s | %s", fallbackString(preset.Name, "(unnamed preset)"), summaryAction),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"workflow":                "preset_recovery",
			"preset_name":             preset.Name,
			"preset":                  preset,
			"steps":                   steps,
			"recovery_action":         request.ActionKind,
			"recovery_service_name":   request.ServiceName,
			"recovery_step_name":      request.StepName,
			"recovery_for_history_id": request.SourceHistoryID,
			"recovery_for_workflow":   request.SourceWorkflow,
		}),
	})
	return &WorkflowRunResponse{
		Workflow:   "preset_recovery",
		PresetName: preset.Name,
		Status:     status,
		Detail:     detail,
		Steps:      steps,
	}, nil
}

func (a *App) RunRuntimeSmoke(request SmokeRequest) (*WorkflowRunResponse, error) {
	response, err := a.Smoke(request)
	steps := smokeResponseToWorkflowSteps(response, err)
	status := "ok"
	historyStatus := "ok"
	detail := "Smoke checks passed."
	if err != nil {
		status = "error"
		historyStatus = "error"
		detail = err.Error()
	} else if response == nil || !response.Ok {
		status = "error"
		historyStatus = "error"
		detail = "Smoke checks need attention."
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (runtime_smoke)",
		Status:  historyStatus,
		Summary: "runtime smoke",
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"workflow": "runtime_smoke",
			"request":  request,
			"steps":    steps,
		}),
	})
	return &WorkflowRunResponse{
		Workflow:   "runtime_smoke",
		PresetName: "",
		Status:     status,
		Detail:     detail,
		Steps:      steps,
	}, nil
}

func (a *App) RunRuntimeStackAction(request RuntimeStackActionRequest) (*WorkflowRunResponse, error) {
	action := strings.TrimSpace(request.Action)
	var (
		response *StackActionResponse
		err      error
	)

	switch action {
	case "start_conversation":
		response, err = a.startConversation()
	case "start_recommended_stack":
		response, err = a.StartRecommendedStack()
	case "stop_recommended_stack":
		response, err = a.StopRecommendedStack()
	case "start_core_stack":
		response, err = a.StartCoreStack()
	case "stop_core_stack":
		response, err = a.StopCoreStack()
	default:
		err = fmt.Errorf("unsupported runtime stack action: %s", action)
	}

	steps := stackActionToWorkflowSteps(response)
	if err != nil && response == nil {
		steps = []WorkflowStep{{Name: fallbackString(action, "runtime_stack"), Status: "failed", Detail: err.Error()}}
	}
	status := "ok"
	historyStatus := "ok"
	detail := fallbackString(responseStatusOrEmpty(response), "stack action completed")
	if err != nil {
		status = "error"
		historyStatus = "error"
		detail = err.Error()
	}

	workflowName := "runtime_stack_action"
	if action != "" {
		workflowName = fmt.Sprintf("runtime_%s", action)
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   fmt.Sprintf("Workflow (%s)", workflowName),
		Status:  historyStatus,
		Summary: strings.ReplaceAll(workflowName, "_", " "),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"workflow": workflowName,
			"action":   action,
			"steps":    steps,
		}),
	})
	return &WorkflowRunResponse{
		Workflow:   workflowName,
		PresetName: "",
		Status:     status,
		Detail:     detail,
		Steps:      steps,
	}, nil
}

func (a *App) RunRuntimeConfigAction(request RuntimeConfigActionRequest) (*WorkflowRunResponse, error) {
	action := strings.TrimSpace(request.Action)
	status, detail, historyStatus, steps := a.runRuntimeConfigActionJob(request)
	workflowName := "runtime_config_action"
	if action != "" {
		workflowName = fmt.Sprintf("runtime_%s", action)
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   fmt.Sprintf("Workflow (%s)", workflowName),
		Status:  historyStatus,
		Summary: strings.ReplaceAll(workflowName, "_", " "),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"workflow": workflowName,
			"action":   action,
			"name":     request.Name,
			"content":  request.Content,
			"steps":    steps,
		}),
	})
	return &WorkflowRunResponse{
		Workflow:   workflowName,
		PresetName: "",
		Status:     status,
		Detail:     detail,
		Steps:      steps,
	}, nil
}

func (a *App) RunRuntimeServiceAction(request RuntimeServiceActionRequest) (*WorkflowRunResponse, error) {
	action := strings.TrimSpace(request.Action)
	status, detail, historyStatus, steps := a.runRuntimeServiceActionJob(request)
	workflowName := "runtime_service_action"
	if action != "" {
		workflowName = fmt.Sprintf("runtime_%s", action)
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   fmt.Sprintf("Workflow (%s)", workflowName),
		Status:  historyStatus,
		Summary: strings.ReplaceAll(workflowName, "_", " "),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"workflow": workflowName,
			"action":   action,
			"watch":    request.Watch,
			"steps":    steps,
		}),
	})
	return &WorkflowRunResponse{
		Workflow:   workflowName,
		PresetName: "",
		Status:     status,
		Detail:     detail,
		Steps:      steps,
	}, nil
}

func (a *App) RunChatAction(request ChatRequest) (*ChatResponse, error) {
	response, err := a.Chat(request)
	status := "ok"
	detail := ""
	if response != nil {
		detail = strings.TrimSpace(response.Answer)
	}
	if err != nil {
		status = "error"
		detail = err.Error()
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "chat",
		Title:   fmt.Sprintf("Chat (%s)", fallbackString(strings.TrimSpace(request.Mode), "auto")),
		Status:  status,
		Summary: truncateString(strings.TrimSpace(request.Prompt), 120),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"mode":   request.Mode,
			"prompt": request.Prompt,
		}),
	})
	return response, err
}

func (a *App) RunIngestAction(request IngestRequest) (map[string]any, error) {
	response, err := a.Ingest(request)
	status := "ok"
	detail := summarizeIngestResponseDetail(response)
	if err != nil {
		status = "error"
		detail = err.Error()
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "ingest",
		Title:   fmt.Sprintf("Ingest (%s)", fallbackString(strings.TrimSpace(request.Project), "default")),
		Status:  status,
		Summary: truncateString(strings.Join(request.Paths, ", "), 180),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"paths":     strings.Join(request.Paths, "\n"),
			"project":   request.Project,
			"tags":      request.Tags,
			"recursive": request.Recursive,
		}),
	})
	return response, err
}

func (a *App) RunEmbeddingAction(request EmbeddingRequest) (map[string]any, error) {
	response, err := a.Embeddings(request)
	status := "ok"
	detail := "Embedding request completed."
	if err != nil {
		status = "error"
		detail = err.Error()
	} else if embedding, ok := response["data"].([]any); ok && len(embedding) > 0 {
		if first, ok := embedding[0].(map[string]any); ok {
			if vector, ok := first["embedding"].([]any); ok {
				detail = fmt.Sprintf("%d dims", len(vector))
			}
		}
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "embedding",
		Title:   fmt.Sprintf("Embedding (%s)", fallbackString(strings.TrimSpace(request.Model), "auto")),
		Status:  status,
		Summary: truncateString(strings.TrimSpace(request.Input), 120),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"model": request.Model,
			"input": request.Input,
		}),
	})
	return response, err
}

func (a *App) RunIndexBrowseAction(request IndexBrowseRequest) (map[string]any, error) {
	response, err := a.BrowseIndex(request)
	status := "ok"
	detail := ""
	if err != nil {
		status = "error"
		detail = err.Error()
	} else {
		detail = fmt.Sprintf("%v chunks matched", response["filtered_chunks"])
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "index",
		Title:   fmt.Sprintf("Index Browser (%s)", fallbackString(strings.TrimSpace(request.Project), "all projects")),
		Status:  status,
		Summary: fallbackString(strings.TrimSpace(request.SourceQuery), strings.TrimSpace(request.Project)),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"project":      request.Project,
			"source_query": request.SourceQuery,
			"limit":        request.Limit,
		}),
	})
	return response, err
}

func (a *App) RunEvalAction(request EvalRequest) (*EvalResponse, error) {
	response, err := a.Eval(request)
	status := "ok"
	detail := summarizeEvalResponseDetail(response)
	if err != nil {
		status = "error"
		detail = err.Error()
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "eval",
		Title:   fmt.Sprintf("Eval (%s)", fallbackString(strings.TrimSpace(request.Project), "default")),
		Status:  status,
		Summary: strings.TrimSpace(request.DatasetPath),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"dataset_path": request.DatasetPath,
			"project":      request.Project,
			"source_path":  request.SourcePath,
			"top_k":        request.TopK,
			"with_answer":  request.WithAnswer,
		}),
	})
	return response, err
}

func (a *App) RunRagSearchAction(request SearchRequest) (*SearchResponse, error) {
	response, err := a.Search(request)
	status := "ok"
	detail := ""
	if err != nil {
		status = "error"
		detail = err.Error()
	} else {
		detail = fmt.Sprintf("%d results", len(response.Results))
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "rag",
		Title:   fmt.Sprintf("RAG Search (%s)", fallbackString(strings.TrimSpace(request.Project), "default")),
		Status:  status,
		Summary: truncateString(strings.TrimSpace(request.Query), 120),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"query":       request.Query,
			"project":     request.Project,
			"source_path": request.SourcePath,
			"tags":        request.Tags,
			"top_k":       request.TopK,
			"answer":      false,
		}),
	})
	return response, err
}

func (a *App) RunRagQueryAction(request QueryRequest) (*QueryResponse, error) {
	response, err := a.Query(request)
	status := "ok"
	detail := ""
	if err != nil {
		status = "error"
		detail = err.Error()
	} else {
		detail = strings.TrimSpace(response.Answer)
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "rag",
		Title:   fmt.Sprintf("RAG Query (%s)", fallbackString(strings.TrimSpace(request.Project), "default")),
		Status:  status,
		Summary: truncateString(strings.TrimSpace(request.Query), 120),
		Detail:  detail,
		Payload: marshalJSONString(map[string]any{
			"query":       request.Query,
			"project":     request.Project,
			"source_path": request.SourcePath,
			"tags":        request.Tags,
			"top_k":       request.TopK,
			"answer":      request.Answer,
			"stream":      request.Stream,
		}),
	})
	return response, err
}

func (a *App) RecordExecution(item ExecutionHistoryItem) ([]ExecutionHistoryItem, error) {
	item.Kind = strings.TrimSpace(item.Kind)
	item.Title = strings.TrimSpace(item.Title)
	item.Status = strings.TrimSpace(item.Status)
	item.Summary = strings.TrimSpace(item.Summary)
	if item.Kind == "" || item.Title == "" || item.Status == "" {
		return nil, fmt.Errorf("kind, title, and status are required")
	}
	if item.ID == "" {
		item.ID = fmt.Sprintf("%d-%s", time.Now().UnixNano(), item.Kind)
	}
	if item.Timestamp == "" {
		item.Timestamp = time.Now().Format(time.RFC3339)
	}
	items, err := a.readExecutionHistory()
	if err != nil {
		return nil, err
	}
	items = append([]ExecutionHistoryItem{item}, items...)
	if len(items) > 40 {
		items = items[:40]
	}
	if err := a.writeExecutionHistory(items); err != nil {
		return nil, err
	}
	return items, nil
}

func (a *App) ClearExecutionHistory() ([]ExecutionHistoryItem, error) {
	if err := a.writeExecutionHistory([]ExecutionHistoryItem{}); err != nil {
		return nil, err
	}
	return []ExecutionHistoryItem{}, nil
}

func (a *App) ExportResult(request ExportResultRequest) (*ExportResultResponse, error) {
	kind := strings.TrimSpace(request.Kind)
	title := strings.TrimSpace(request.Title)
	content := strings.TrimSpace(request.Content)
	if kind == "" || title == "" || content == "" {
		return nil, fmt.Errorf("kind, title, and content are required")
	}

	stem := strings.TrimSpace(request.FileStem)
	if stem == "" {
		stem = fmt.Sprintf("%s-%d", kind, time.Now().Unix())
	}
	stem = sanitizeFileName(stem)
	if stem == "" {
		stem = fmt.Sprintf("%s-%d", kind, time.Now().Unix())
	}

	path := filepath.Join(a.workspaceRoot, "data", "exports", stem+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	body := fmt.Sprintf("# %s\n\n%s\n", title, content)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return nil, err
	}
	return &ExportResultResponse{Path: path}, nil
}

func (a *App) ListExportedResults() ([]ExportedFileItem, error) {
	root := filepath.Join(a.workspaceRoot, "data", "exports")
	if !fileExists(root) {
		return []ExportedFileItem{}, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	items := make([]ExportedFileItem, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		items = append(items, ExportedFileItem{
			Name:    entry.Name(),
			Path:    path,
			ModTime: info.ModTime().Format(time.RFC3339),
		})
	}
	slices.SortFunc(items, func(a ExportedFileItem, b ExportedFileItem) int {
		return strings.Compare(b.ModTime, a.ModTime)
	})
	return items, nil
}

func (a *App) ReadExportedResult(request ExportedFileRequest) (*ExportedFileContent, error) {
	path := strings.TrimSpace(request.Path)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	root := filepath.Join(a.workspaceRoot, "data", "exports")
	cleanRoot := filepath.Clean(root)
	cleanPath := filepath.Clean(path)
	if !strings.HasPrefix(cleanPath, cleanRoot+string(os.PathSeparator)) && cleanPath != cleanRoot {
		return nil, fmt.Errorf("path must stay within %s", cleanRoot)
	}
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, err
	}
	return &ExportedFileContent{
		Name:    filepath.Base(cleanPath),
		Path:    cleanPath,
		Content: string(data),
	}, nil
}

func (a *App) GetRuntimeStatus() *RuntimeStatus {
	qdrantRunning, qdrantDetail := a.getQdrantRuntimeState()

	a.mu.Lock()
	defer a.mu.Unlock()

	pid := 0
	if a.gatewayCmd != nil && a.gatewayCmd.Process != nil {
		pid = a.gatewayCmd.Process.Pid
	}
	fastPID := 0
	if a.fastCmd != nil && a.fastCmd.Process != nil {
		fastPID = a.fastCmd.Process.Pid
	}
	workPID := 0
	if a.workCmd != nil && a.workCmd.Process != nil {
		workPID = a.workCmd.Process.Pid
	}
	codePID := 0
	if a.codeCmd != nil && a.codeCmd.Process != nil {
		codePID = a.codeCmd.Process.Pid
	}
	embeddingPID := 0
	if a.embeddingCmd != nil && a.embeddingCmd.Process != nil {
		embeddingPID = a.embeddingCmd.Process.Pid
	}
	watchPID := 0
	if a.watchCmd != nil && a.watchCmd.Process != nil {
		watchPID = a.watchCmd.Process.Pid
	}
	modelsLocalPath := filepath.Join(a.workspaceRoot, "configs", "models.local.yaml")
	ragLocalPath := filepath.Join(a.workspaceRoot, "configs", "rag.local.yaml")
	configSummary, requiredServices, optionalServices, warnings := a.runtimeConfigSummary()
	return &RuntimeStatus{
		WorkspaceRoot:       a.workspaceRoot,
		FastRunning:         a.fastRunning,
		FastPID:             fastPID,
		FastLogs:            append([]string(nil), a.fastLogs...),
		WorkRunning:         a.workRunning,
		WorkPID:             workPID,
		WorkLogs:            append([]string(nil), a.workLogs...),
		CodeRunning:         a.codeRunning,
		CodePID:             codePID,
		CodeLogs:            append([]string(nil), a.codeLogs...),
		GatewayRunning:      a.gatewayRunning,
		GatewayPID:          pid,
		GatewayLogs:         append([]string(nil), a.gatewayLogs...),
		EmbeddingRunning:    a.embeddingRunning,
		EmbeddingPID:        embeddingPID,
		EmbeddingLogs:       append([]string(nil), a.embeddingLogs...),
		QdrantRunning:       qdrantRunning,
		QdrantDetail:        qdrantDetail,
		QdrantLogs:          append([]string(nil), a.qdrantLogs...),
		WatchRunning:        a.watchRunning,
		WatchPID:            watchPID,
		WatchLogs:           append([]string(nil), a.watchLogs...),
		ModelsLocalOverride: fileExists(modelsLocalPath),
		RagLocalOverride:    fileExists(ragLocalPath),
		ModelsLocalPath:     modelsLocalPath,
		RagLocalPath:        ragLocalPath,
		ConfigSummary:       configSummary,
		RequiredServices:    requiredServices,
		OptionalServices:    optionalServices,
		Warnings:            warnings,
	}
}

func (a *App) StartFast() (*RuntimeStatus, error) {
	return a.startModelProcess("scripts/start_llama_fast.sh", "fast", &a.fastCmd, &a.fastRunning, a.appendFastLog, a.captureFastStream, a.waitFastProcess)
}

func (a *App) StopFast() (*RuntimeStatus, error) {
	return a.stopModelProcess("fast", &a.fastCmd, &a.fastRunning, a.appendFastLog)
}

func (a *App) StartWork() (*RuntimeStatus, error) {
	return a.startModelProcess("scripts/start_llama_work.sh", "work", &a.workCmd, &a.workRunning, a.appendWorkLog, a.captureWorkStream, a.waitWorkProcess)
}

func (a *App) StopWork() (*RuntimeStatus, error) {
	return a.stopModelProcess("work", &a.workCmd, &a.workRunning, a.appendWorkLog)
}

func (a *App) StartCode() (*RuntimeStatus, error) {
	return a.startModelProcess("scripts/start_llama_code.sh", "code", &a.codeCmd, &a.codeRunning, a.appendCodeLog, a.captureCodeStream, a.waitCodeProcess)
}

func (a *App) StopCode() (*RuntimeStatus, error) {
	return a.stopModelProcess("code", &a.codeCmd, &a.codeRunning, a.appendCodeLog)
}

func (a *App) StartGateway() (*RuntimeStatus, error) {
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return nil, fmt.Errorf("Desktop is shutting down")
	}
	if a.gatewayRunning {
		a.mu.Unlock()
		return a.GetRuntimeStatus(), nil
	}

	pythonPath := filepath.Join(a.workspaceRoot, ".venv", "bin", "python")
	if _, err := os.Stat(pythonPath); err != nil {
		a.appendGatewayLog("missing .venv/bin/python; create the virtualenv first")
		a.mu.Unlock()
		return a.GetRuntimeStatus(), fmt.Errorf("missing gateway runtime at %s", pythonPath)
	}

	cmd := exec.Command(
		pythonPath,
		"-m",
		"uvicorn",
		"apps.gateway.main:app",
		"--host",
		"127.0.0.1",
		"--port",
		"8000",
	)
	cmd.Dir = a.workspaceRoot
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		a.appendGatewayLog("failed to start gateway: " + err.Error())
		a.mu.Unlock()
		return nil, err
	}

	a.gatewayCmd = cmd
	a.gatewayRunning = true
	a.appendGatewayLog("gateway started")
	a.mu.Unlock()

	go a.captureGatewayStream(stdout)
	go a.captureGatewayStream(stderr)
	go a.waitGatewayProcess(cmd)

	time.Sleep(300 * time.Millisecond)
	return a.GetRuntimeStatus(), nil
}

func (a *App) StopGateway() (*RuntimeStatus, error) {
	a.mu.Lock()
	cmd := a.gatewayCmd
	if cmd == nil || cmd.Process == nil || !a.gatewayRunning {
		a.gatewayRunning = false
		a.gatewayCmd = nil
		a.mu.Unlock()
		return a.GetRuntimeStatus(), nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		return nil, err
	}
	time.Sleep(300 * time.Millisecond)
	a.mu.Lock()
	a.gatewayRunning = false
	a.gatewayCmd = nil
	a.appendGatewayLog("gateway stop requested")
	a.mu.Unlock()
	return a.GetRuntimeStatus(), nil
}

func (a *App) StartEmbedding() (*RuntimeStatus, error) {
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return nil, fmt.Errorf("Desktop is shutting down")
	}
	if a.embeddingRunning {
		a.mu.Unlock()
		return a.GetRuntimeStatus(), nil
	}

	scriptPath := filepath.Join(a.workspaceRoot, "scripts", "start_llama_embedding.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		a.appendEmbeddingLog("missing scripts/start_llama_embedding.sh")
		a.mu.Unlock()
		return a.GetRuntimeStatus(), fmt.Errorf("missing embedding runtime script at %s", scriptPath)
	}

	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = a.workspaceRoot
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		a.appendEmbeddingLog("failed to start embedding: " + err.Error())
		a.mu.Unlock()
		return nil, err
	}

	a.embeddingCmd = cmd
	a.embeddingRunning = true
	a.appendEmbeddingLog("embedding started")
	a.mu.Unlock()

	go a.captureEmbeddingStream(stdout)
	go a.captureEmbeddingStream(stderr)
	go a.waitEmbeddingProcess(cmd)

	time.Sleep(300 * time.Millisecond)
	return a.GetRuntimeStatus(), nil
}

func (a *App) StopEmbedding() (*RuntimeStatus, error) {
	a.mu.Lock()
	cmd := a.embeddingCmd
	if cmd == nil || cmd.Process == nil || !a.embeddingRunning {
		a.embeddingRunning = false
		a.embeddingCmd = nil
		a.mu.Unlock()
		return a.GetRuntimeStatus(), nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		return nil, err
	}
	time.Sleep(300 * time.Millisecond)
	a.mu.Lock()
	a.embeddingRunning = false
	a.embeddingCmd = nil
	a.appendEmbeddingLog("embedding stop requested")
	a.mu.Unlock()
	return a.GetRuntimeStatus(), nil
}

func (a *App) StartQdrant() (*RuntimeStatus, error) {
	output, err := a.runWorkspaceCommand("bash scripts/start_qdrant.sh")
	a.mu.Lock()
	if strings.TrimSpace(output) != "" {
		a.appendQdrantLog(output)
	}
	if err != nil {
		a.appendQdrantLog("failed to start qdrant: " + err.Error())
		a.mu.Unlock()
		return a.GetRuntimeStatus(), err
	}
	a.appendQdrantLog("qdrant start requested")
	a.mu.Unlock()
	return a.GetRuntimeStatus(), nil
}

func (a *App) StopQdrant() (*RuntimeStatus, error) {
	output, err := a.runWorkspaceCommand("bash scripts/stop_qdrant.sh")
	a.mu.Lock()
	if strings.TrimSpace(output) != "" {
		a.appendQdrantLog(output)
	}
	if err != nil {
		a.appendQdrantLog("failed to stop qdrant: " + err.Error())
		a.mu.Unlock()
		return a.GetRuntimeStatus(), err
	}
	a.appendQdrantLog("qdrant stop requested")
	a.mu.Unlock()
	return a.GetRuntimeStatus(), nil
}

func (a *App) Smoke(request SmokeRequest) (*SmokeResponse, error) {
	args := []string{"scripts/run_cli.sh", "smoke"}
	if strings.TrimSpace(request.GatewayURL) != "" {
		args = append(args, "--gateway-url", request.GatewayURL)
	}
	if request.SkipQdrant {
		args = append(args, "--skip-qdrant")
	}
	if request.SkipEmbedding {
		args = append(args, "--skip-embedding")
	}
	if request.SkipReranker {
		args = append(args, "--skip-reranker")
	}

	cmd := exec.Command("bash", args...)
	cmd.Dir = a.workspaceRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		var partial SmokeResponse
		if jsonErr := json.Unmarshal(output, &partial); jsonErr == nil {
			return &partial, err
		}
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(output)))
	}

	var response SmokeResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (a *App) StartWatch(request WatchRequest) (*RuntimeStatus, error) {
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return nil, fmt.Errorf("Desktop is shutting down")
	}
	if a.watchRunning {
		a.mu.Unlock()
		return a.GetRuntimeStatus(), nil
	}
	if len(request.Paths) == 0 {
		a.mu.Unlock()
		return nil, fmt.Errorf("watch requires at least one path")
	}

	args := []string{"scripts/run_cli.sh", "watch"}
	args = append(args, request.Paths...)
	if strings.TrimSpace(request.Project) != "" {
		args = append(args, "--project", request.Project)
	}
	if len(request.Tags) > 0 {
		args = append(args, "--tags")
		args = append(args, request.Tags...)
	}
	if request.Interval > 0 {
		args = append(args, "--interval", fmt.Sprintf("%.2f", request.Interval))
	}
	if !request.Recursive {
		args = append(args, "--no-recursive")
	}

	cmd := exec.Command("bash", args...)
	cmd.Dir = a.workspaceRoot
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		a.appendWatchLog("failed to start watch: " + err.Error())
		a.mu.Unlock()
		return nil, err
	}

	a.watchCmd = cmd
	a.watchRunning = true
	a.appendWatchLog("watch started")
	a.mu.Unlock()

	go a.captureWatchStream(stdout)
	go a.captureWatchStream(stderr)
	go a.waitWatchProcess(cmd)

	time.Sleep(300 * time.Millisecond)
	return a.GetRuntimeStatus(), nil
}

func (a *App) StopWatch() (*RuntimeStatus, error) {
	a.mu.Lock()
	cmd := a.watchCmd
	if cmd == nil || cmd.Process == nil || !a.watchRunning {
		a.watchRunning = false
		a.watchCmd = nil
		a.mu.Unlock()
		return a.GetRuntimeStatus(), nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		return nil, err
	}
	time.Sleep(300 * time.Millisecond)
	a.mu.Lock()
	a.watchRunning = false
	a.watchCmd = nil
	a.appendWatchLog("watch stop requested")
	a.mu.Unlock()
	return a.GetRuntimeStatus(), nil
}

const modelsLocalExternalPreset = `models:
  embedding:
    provider: llama_cpp
    model: qwen3-embedding-0.6b
    base_url: http://localhost:8090/v1

  reranker:
    provider: openai_compatible
    model: qwen3-reranker-0.6b
    base_url: http://localhost:8100/v1
`

const ragLocalOnlyPreset = `rag:
  embedding_provider: local_hash
  embedding_model_alias: embedding
  reranker_provider: local_overlap
  reranker_model_alias: reranker

vector_db:
  provider: local_json
  collection: local_docs
  store_path: data/index/local_docs.json
`

const ragExternalPreset = `rag:
  embedding_provider: openai_compatible
  embedding_model_alias: embedding
  reranker_provider: openai_compatible
  reranker_model_alias: reranker
  reranker_endpoint_path: /rerank

vector_db:
  provider: qdrant
  url: http://localhost:6333
  collection: local_docs
  store_path: data/index/local_docs.json
`

func (a *App) runBatchPresetVerification(presets []ProjectPreset) {
	results := make([]BatchWorkflowResultItem, len(presets))
	for index, preset := range presets {
		results[index] = BatchWorkflowResultItem{
			PresetName: preset.Name,
			Status:     "queued",
			Detail:     "Waiting to start.",
		}
	}
	workflowLabel := "Batch Preset Verification"

	for index, preset := range presets {
		if a.isBatchWorkflowCancelRequested() {
			for skipped := index; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested before this preset started."
					a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
				}
			}
			break
		}

		results[index].Status = "running"
		results[index].Detail = "Running verification..."
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		status, detail, historyStatus, steps := a.runPresetVerificationJob(preset)
		results[index].Status = status
		results[index].Detail = detail
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		_, _ = a.RecordExecution(ExecutionHistoryItem{
			Kind:    "workflow",
			Title:   "Workflow (preset_verification)",
			Status:  historyStatus,
			Summary: fmt.Sprintf("verification | %s", preset.Name),
			Detail: map[bool]string{
				true:  "representative verification completed",
				false: "one or more representative verification steps failed",
			}[historyStatus == "ok"],
			Payload: marshalJSONString(map[string]any{
				"workflow":    "preset_verification",
				"preset_name": preset.Name,
				"preset":      preset,
				"steps":       steps,
			}),
		})

		if a.isBatchWorkflowCancelRequested() {
			for skipped := index + 1; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested."
				}
			}
			a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
			break
		}
	}

	okCount := 0
	errorCount := 0
	cancelledCount := 0
	for _, result := range results {
		switch result.Status {
		case "ok":
			okCount++
		case "error":
			errorCount++
		case "cancelled":
			cancelledCount++
		}
	}
	finalStatus := "completed"
	historyStatus := "ok"
	if cancelledCount > 0 {
		finalStatus = "cancelled"
		historyStatus = "cancelled"
	} else if errorCount > 0 && okCount == 0 {
		finalStatus = "failed"
		historyStatus = "error"
	} else if errorCount > 0 {
		finalStatus = "completed"
		historyStatus = "error"
	}
	a.updateBatchWorkflowState(workflowLabel, finalStatus, false, a.isBatchWorkflowCancelRequested(), results)

	steps := make([]WorkflowStep, 0, len(results))
	for _, result := range results {
		stepStatus := result.Status
		switch result.Status {
		case "error":
			stepStatus = "failed"
		}
		steps = append(steps, WorkflowStep{
			Name:   result.PresetName,
			Status: stepStatus,
			Detail: result.Detail,
		})
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_batch_verification)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("batch verification | %d presets", len(presets)),
		Detail:  fmt.Sprintf("ok=%d, error=%d, cancelled=%d", okCount, errorCount, cancelledCount),
		Payload: marshalJSONString(map[string]any{
			"workflow":           "preset_batch_verification",
			"preset_name":        strings.Join(mapPresetNames(presets), ", "),
			"preset":             map[string]any{"name": strings.Join(mapPresetNames(presets), ", ")},
			"steps":              steps,
			"batch_preset_names": mapPresetNames(presets),
			"batch_results":      results,
		}),
	})
}

func (a *App) runBatchPresetValidate(presets []ProjectPreset) {
	results := make([]BatchWorkflowResultItem, len(presets))
	for index, preset := range presets {
		results[index] = BatchWorkflowResultItem{
			PresetName: preset.Name,
			Status:     "queued",
			Detail:     "Waiting to start.",
		}
	}
	workflowLabel := "Batch Preset Validate"

	for index, preset := range presets {
		if a.isBatchWorkflowCancelRequested() {
			for skipped := index; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested before this preset started."
					a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
				}
			}
			break
		}

		results[index].Status = "running"
		results[index].Detail = "Running validation..."
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		status, detail, historyStatus, steps := a.runPresetValidateJob(preset)
		results[index].Status = status
		results[index].Detail = detail
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		_, _ = a.RecordExecution(ExecutionHistoryItem{
			Kind:    "workflow",
			Title:   "Workflow (preset_validate)",
			Status:  historyStatus,
			Summary: fmt.Sprintf("validate | %s", preset.Name),
			Detail:  detail,
			Payload: marshalJSONString(map[string]any{
				"workflow":    "preset_validate",
				"preset_name": preset.Name,
				"preset":      preset,
				"steps":       steps,
			}),
		})

		if a.isBatchWorkflowCancelRequested() {
			for skipped := index + 1; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested."
				}
			}
			a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
			break
		}
	}

	okCount := 0
	errorCount := 0
	cancelledCount := 0
	for _, result := range results {
		switch result.Status {
		case "ok":
			okCount++
		case "error":
			errorCount++
		case "cancelled":
			cancelledCount++
		}
	}
	finalStatus := "completed"
	historyStatus := "ok"
	if cancelledCount > 0 {
		finalStatus = "cancelled"
		historyStatus = "cancelled"
	} else if errorCount > 0 && okCount == 0 {
		finalStatus = "failed"
		historyStatus = "error"
	} else if errorCount > 0 {
		finalStatus = "completed"
		historyStatus = "error"
	}
	a.updateBatchWorkflowState(workflowLabel, finalStatus, false, a.isBatchWorkflowCancelRequested(), results)

	steps := make([]WorkflowStep, 0, len(results))
	for _, result := range results {
		stepStatus := result.Status
		if result.Status == "error" {
			stepStatus = "failed"
		}
		steps = append(steps, WorkflowStep{Name: result.PresetName, Status: stepStatus, Detail: result.Detail})
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_batch_validate)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("batch validate | %d presets", len(presets)),
		Detail:  fmt.Sprintf("ok=%d, error=%d, cancelled=%d", okCount, errorCount, cancelledCount),
		Payload: marshalJSONString(map[string]any{
			"workflow":           "preset_batch_validate",
			"preset_name":        strings.Join(mapPresetNames(presets), ", "),
			"preset":             map[string]any{"name": strings.Join(mapPresetNames(presets), ", ")},
			"steps":              steps,
			"batch_preset_names": mapPresetNames(presets),
			"batch_results":      results,
		}),
	})
}

func (a *App) runBatchPresetSmoke(presets []ProjectPreset) {
	results := make([]BatchWorkflowResultItem, len(presets))
	for index, preset := range presets {
		results[index] = BatchWorkflowResultItem{
			PresetName: preset.Name,
			Status:     "queued",
			Detail:     "Waiting to start.",
		}
	}
	workflowLabel := "Batch Preset Smoke"

	for index, preset := range presets {
		if a.isBatchWorkflowCancelRequested() {
			for skipped := index; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested before this preset started."
					a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
				}
			}
			break
		}

		results[index].Status = "running"
		results[index].Detail = "Running smoke..."
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		status, detail, historyStatus, steps := a.runPresetSmokeJob(preset)
		results[index].Status = status
		results[index].Detail = detail
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		_, _ = a.RecordExecution(ExecutionHistoryItem{
			Kind:    "workflow",
			Title:   "Workflow (preset_smoke)",
			Status:  historyStatus,
			Summary: fmt.Sprintf("smoke | %s", preset.Name),
			Detail:  detail,
			Payload: marshalJSONString(map[string]any{
				"workflow":    "preset_smoke",
				"preset_name": preset.Name,
				"preset":      preset,
				"steps":       steps,
			}),
		})

		if a.isBatchWorkflowCancelRequested() {
			for skipped := index + 1; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested."
				}
			}
			a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
			break
		}
	}

	okCount := 0
	errorCount := 0
	cancelledCount := 0
	for _, result := range results {
		switch result.Status {
		case "ok":
			okCount++
		case "error":
			errorCount++
		case "cancelled":
			cancelledCount++
		}
	}
	finalStatus := "completed"
	historyStatus := "ok"
	if cancelledCount > 0 {
		finalStatus = "cancelled"
		historyStatus = "cancelled"
	} else if errorCount > 0 && okCount == 0 {
		finalStatus = "failed"
		historyStatus = "error"
	} else if errorCount > 0 {
		finalStatus = "completed"
		historyStatus = "error"
	}
	a.updateBatchWorkflowState(workflowLabel, finalStatus, false, a.isBatchWorkflowCancelRequested(), results)

	steps := make([]WorkflowStep, 0, len(results))
	for _, result := range results {
		stepStatus := result.Status
		if result.Status == "error" {
			stepStatus = "failed"
		}
		steps = append(steps, WorkflowStep{Name: result.PresetName, Status: stepStatus, Detail: result.Detail})
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_batch_smoke)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("batch smoke | %d presets", len(presets)),
		Detail:  fmt.Sprintf("ok=%d, error=%d, cancelled=%d", okCount, errorCount, cancelledCount),
		Payload: marshalJSONString(map[string]any{
			"workflow":           "preset_batch_smoke",
			"preset_name":        strings.Join(mapPresetNames(presets), ", "),
			"preset":             map[string]any{"name": strings.Join(mapPresetNames(presets), ", ")},
			"steps":              steps,
			"batch_preset_names": mapPresetNames(presets),
			"batch_results":      results,
		}),
	})
}

func (a *App) runBatchPresetWatch(presets []ProjectPreset) {
	results := make([]BatchWorkflowResultItem, len(presets))
	for index, preset := range presets {
		results[index] = BatchWorkflowResultItem{
			PresetName: preset.Name,
			Status:     "queued",
			Detail:     "Waiting to start.",
		}
	}
	workflowLabel := "Batch Preset Watch"

	for index, preset := range presets {
		if a.isBatchWorkflowCancelRequested() {
			for skipped := index; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested before this preset started."
					a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
				}
			}
			break
		}

		results[index].Status = "running"
		results[index].Detail = "Starting watch..."
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		status, detail, historyStatus, steps := a.runPresetWatchJob(preset)
		results[index].Status = status
		results[index].Detail = detail
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		_, _ = a.RecordExecution(ExecutionHistoryItem{
			Kind:    "workflow",
			Title:   "Workflow (preset_watch)",
			Status:  historyStatus,
			Summary: fmt.Sprintf("watch | %s", preset.Name),
			Detail:  detail,
			Payload: marshalJSONString(map[string]any{
				"workflow":    "preset_watch",
				"preset_name": preset.Name,
				"preset":      preset,
				"steps":       steps,
			}),
		})

		if a.isBatchWorkflowCancelRequested() {
			for skipped := index + 1; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested."
				}
			}
			a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
			break
		}
	}

	okCount := 0
	errorCount := 0
	cancelledCount := 0
	for _, result := range results {
		switch result.Status {
		case "ok":
			okCount++
		case "error":
			errorCount++
		case "cancelled":
			cancelledCount++
		}
	}
	finalStatus := "completed"
	historyStatus := "ok"
	if cancelledCount > 0 {
		finalStatus = "cancelled"
		historyStatus = "cancelled"
	} else if errorCount > 0 && okCount == 0 {
		finalStatus = "failed"
		historyStatus = "error"
	} else if errorCount > 0 {
		finalStatus = "completed"
		historyStatus = "error"
	}
	a.updateBatchWorkflowState(workflowLabel, finalStatus, false, a.isBatchWorkflowCancelRequested(), results)

	steps := make([]WorkflowStep, 0, len(results))
	for _, result := range results {
		stepStatus := result.Status
		if result.Status == "error" {
			stepStatus = "failed"
		}
		steps = append(steps, WorkflowStep{Name: result.PresetName, Status: stepStatus, Detail: result.Detail})
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_batch_watch)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("batch watch | %d presets", len(presets)),
		Detail:  fmt.Sprintf("ok=%d, error=%d, cancelled=%d", okCount, errorCount, cancelledCount),
		Payload: marshalJSONString(map[string]any{
			"workflow":           "preset_batch_watch",
			"preset_name":        strings.Join(mapPresetNames(presets), ", "),
			"preset":             map[string]any{"name": strings.Join(mapPresetNames(presets), ", ")},
			"steps":              steps,
			"batch_preset_names": mapPresetNames(presets),
			"batch_results":      results,
		}),
	})
}

func (a *App) runBatchPresetRuntimeStackPrepare(presets []ProjectPreset) {
	results := make([]BatchWorkflowResultItem, len(presets))
	for index, preset := range presets {
		results[index] = BatchWorkflowResultItem{
			PresetName: preset.Name,
			Status:     "queued",
			Detail:     "Waiting to start.",
		}
	}
	workflowLabel := "Batch Preset Runtime + Stack Prepare"

	for index, preset := range presets {
		if a.isBatchWorkflowCancelRequested() {
			for skipped := index; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested before this preset started."
					a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
				}
			}
			break
		}

		results[index].Status = "running"
		results[index].Detail = "Preparing runtime profile and recommended stack..."
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		status, detail, historyStatus, steps := a.runPresetRuntimeStackPrepareJob(preset)
		results[index].Status = status
		results[index].Detail = detail
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		_, _ = a.RecordExecution(ExecutionHistoryItem{
			Kind:    "workflow",
			Title:   "Workflow (preset_runtime_stack_prepare)",
			Status:  historyStatus,
			Summary: fmt.Sprintf("runtime+stack prepare | %s", preset.Name),
			Detail:  detail,
			Payload: marshalJSONString(map[string]any{
				"workflow":    "preset_runtime_stack_prepare",
				"preset_name": preset.Name,
				"preset":      preset,
				"steps":       steps,
			}),
		})

		if a.isBatchWorkflowCancelRequested() {
			for skipped := index + 1; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested."
				}
			}
			a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
			break
		}
	}

	okCount := 0
	errorCount := 0
	cancelledCount := 0
	for _, result := range results {
		switch result.Status {
		case "ok":
			okCount++
		case "error":
			errorCount++
		case "cancelled":
			cancelledCount++
		}
	}
	finalStatus := "completed"
	historyStatus := "ok"
	if cancelledCount > 0 {
		finalStatus = "cancelled"
		historyStatus = "cancelled"
	} else if errorCount > 0 && okCount == 0 {
		finalStatus = "failed"
		historyStatus = "error"
	} else if errorCount > 0 {
		finalStatus = "completed"
		historyStatus = "error"
	}
	a.updateBatchWorkflowState(workflowLabel, finalStatus, false, a.isBatchWorkflowCancelRequested(), results)

	steps := make([]WorkflowStep, 0, len(results))
	for _, result := range results {
		stepStatus := result.Status
		if result.Status == "error" {
			stepStatus = "failed"
		}
		steps = append(steps, WorkflowStep{Name: result.PresetName, Status: stepStatus, Detail: result.Detail})
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_batch_runtime_stack_prepare)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("batch runtime+stack prepare | %d presets", len(presets)),
		Detail:  fmt.Sprintf("ok=%d, error=%d, cancelled=%d", okCount, errorCount, cancelledCount),
		Payload: marshalJSONString(map[string]any{
			"workflow":           "preset_batch_runtime_stack_prepare",
			"preset_name":        strings.Join(mapPresetNames(presets), ", "),
			"preset":             map[string]any{"name": strings.Join(mapPresetNames(presets), ", ")},
			"steps":              steps,
			"batch_preset_names": mapPresetNames(presets),
			"batch_results":      results,
		}),
	})
}

func (a *App) runBatchPresetStackIngestEval(presets []ProjectPreset) {
	results := make([]BatchWorkflowResultItem, len(presets))
	for index, preset := range presets {
		results[index] = BatchWorkflowResultItem{
			PresetName: preset.Name,
			Status:     "queued",
			Detail:     "Waiting to start.",
		}
	}
	workflowLabel := "Batch Preset Stack + Ingest + Eval"

	for index, preset := range presets {
		if a.isBatchWorkflowCancelRequested() {
			for skipped := index; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested before this preset started."
					a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
				}
			}
			break
		}

		results[index].Status = "running"
		results[index].Detail = "Running stack + ingest + eval..."
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		status, detail, historyStatus, steps := a.runPresetStackIngestEvalJob(preset)
		results[index].Status = status
		results[index].Detail = detail
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		_, _ = a.RecordExecution(ExecutionHistoryItem{
			Kind:    "workflow",
			Title:   "Workflow (preset_stack_ingest_eval)",
			Status:  historyStatus,
			Summary: fmt.Sprintf("stack+ingest+eval | %s", preset.Name),
			Detail:  detail,
			Payload: marshalJSONString(map[string]any{
				"workflow":    "preset_stack_ingest_eval",
				"preset_name": preset.Name,
				"preset":      preset,
				"steps":       steps,
			}),
		})

		if a.isBatchWorkflowCancelRequested() {
			for skipped := index + 1; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested."
				}
			}
			a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
			break
		}
	}

	okCount := 0
	errorCount := 0
	cancelledCount := 0
	for _, result := range results {
		switch result.Status {
		case "ok":
			okCount++
		case "error":
			errorCount++
		case "cancelled":
			cancelledCount++
		}
	}
	finalStatus := "completed"
	historyStatus := "ok"
	if cancelledCount > 0 {
		finalStatus = "cancelled"
		historyStatus = "cancelled"
	} else if errorCount > 0 && okCount == 0 {
		finalStatus = "failed"
		historyStatus = "error"
	} else if errorCount > 0 {
		finalStatus = "completed"
		historyStatus = "error"
	}
	a.updateBatchWorkflowState(workflowLabel, finalStatus, false, a.isBatchWorkflowCancelRequested(), results)

	steps := make([]WorkflowStep, 0, len(results))
	for _, result := range results {
		stepStatus := result.Status
		if result.Status == "error" {
			stepStatus = "failed"
		}
		steps = append(steps, WorkflowStep{Name: result.PresetName, Status: stepStatus, Detail: result.Detail})
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_batch_stack_ingest_eval)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("batch stack+ingest+eval | %d presets", len(presets)),
		Detail:  fmt.Sprintf("ok=%d, error=%d, cancelled=%d", okCount, errorCount, cancelledCount),
		Payload: marshalJSONString(map[string]any{
			"workflow":           "preset_batch_stack_ingest_eval",
			"preset_name":        strings.Join(mapPresetNames(presets), ", "),
			"preset":             map[string]any{"name": strings.Join(mapPresetNames(presets), ", ")},
			"steps":              steps,
			"batch_preset_names": mapPresetNames(presets),
			"batch_results":      results,
		}),
	})
}

func (a *App) runBatchPresetIngestEval(presets []ProjectPreset) {
	results := make([]BatchWorkflowResultItem, len(presets))
	for index, preset := range presets {
		results[index] = BatchWorkflowResultItem{
			PresetName: preset.Name,
			Status:     "queued",
			Detail:     "Waiting to start.",
		}
	}
	workflowLabel := "Batch Preset Ingest + Eval"

	for index, preset := range presets {
		if a.isBatchWorkflowCancelRequested() {
			for skipped := index; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested before this preset started."
					a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
				}
			}
			break
		}

		results[index].Status = "running"
		results[index].Detail = "Running ingest + eval..."
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		status, detail, historyStatus, steps := a.runPresetIngestEvalJob(preset)
		results[index].Status = status
		results[index].Detail = detail
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		_, _ = a.RecordExecution(ExecutionHistoryItem{
			Kind:    "workflow",
			Title:   "Workflow (preset_ingest_eval)",
			Status:  historyStatus,
			Summary: fmt.Sprintf("ingest+eval | %s", preset.Name),
			Detail:  detail,
			Payload: marshalJSONString(map[string]any{
				"workflow":    "preset_ingest_eval",
				"preset_name": preset.Name,
				"preset":      preset,
				"steps":       steps,
			}),
		})

		if a.isBatchWorkflowCancelRequested() {
			for skipped := index + 1; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested."
				}
			}
			a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
			break
		}
	}

	okCount := 0
	errorCount := 0
	cancelledCount := 0
	for _, result := range results {
		switch result.Status {
		case "ok":
			okCount++
		case "error":
			errorCount++
		case "cancelled":
			cancelledCount++
		}
	}
	finalStatus := "completed"
	historyStatus := "ok"
	if cancelledCount > 0 {
		finalStatus = "cancelled"
		historyStatus = "cancelled"
	} else if errorCount > 0 && okCount == 0 {
		finalStatus = "failed"
		historyStatus = "error"
	} else if errorCount > 0 {
		finalStatus = "completed"
		historyStatus = "error"
	}
	a.updateBatchWorkflowState(workflowLabel, finalStatus, false, a.isBatchWorkflowCancelRequested(), results)

	steps := make([]WorkflowStep, 0, len(results))
	for _, result := range results {
		stepStatus := result.Status
		switch result.Status {
		case "error":
			stepStatus = "failed"
		}
		steps = append(steps, WorkflowStep{
			Name:   result.PresetName,
			Status: stepStatus,
			Detail: result.Detail,
		})
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_batch_ingest_eval)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("batch ingest+eval | %d presets", len(presets)),
		Detail:  fmt.Sprintf("ok=%d, error=%d, cancelled=%d", okCount, errorCount, cancelledCount),
		Payload: marshalJSONString(map[string]any{
			"workflow":           "preset_batch_ingest_eval",
			"preset_name":        strings.Join(mapPresetNames(presets), ", "),
			"preset":             map[string]any{"name": strings.Join(mapPresetNames(presets), ", ")},
			"steps":              steps,
			"batch_preset_names": mapPresetNames(presets),
			"batch_results":      results,
		}),
	})
}

func (a *App) runBatchPresetEval(presets []ProjectPreset) {
	results := make([]BatchWorkflowResultItem, len(presets))
	for index, preset := range presets {
		results[index] = BatchWorkflowResultItem{
			PresetName: preset.Name,
			Status:     "queued",
			Detail:     "Waiting to start.",
		}
	}
	workflowLabel := "Batch Preset Eval"

	for index, preset := range presets {
		if a.isBatchWorkflowCancelRequested() {
			for skipped := index; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested before this preset started."
					a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
				}
			}
			break
		}

		results[index].Status = "running"
		results[index].Detail = "Running eval..."
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		status, detail, historyStatus, steps := a.runPresetEvalJob(preset)
		results[index].Status = status
		results[index].Detail = detail
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		_, _ = a.RecordExecution(ExecutionHistoryItem{
			Kind:    "workflow",
			Title:   "Workflow (preset_eval)",
			Status:  historyStatus,
			Summary: fmt.Sprintf("eval | %s", preset.Name),
			Detail:  detail,
			Payload: marshalJSONString(map[string]any{
				"workflow":    "preset_eval",
				"preset_name": preset.Name,
				"preset":      preset,
				"steps":       steps,
			}),
		})

		if a.isBatchWorkflowCancelRequested() {
			for skipped := index + 1; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested."
				}
			}
			a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
			break
		}
	}

	okCount := 0
	errorCount := 0
	cancelledCount := 0
	for _, result := range results {
		switch result.Status {
		case "ok":
			okCount++
		case "error":
			errorCount++
		case "cancelled":
			cancelledCount++
		}
	}
	finalStatus := "completed"
	historyStatus := "ok"
	if cancelledCount > 0 {
		finalStatus = "cancelled"
		historyStatus = "cancelled"
	} else if errorCount > 0 && okCount == 0 {
		finalStatus = "failed"
		historyStatus = "error"
	} else if errorCount > 0 {
		finalStatus = "completed"
		historyStatus = "error"
	}
	a.updateBatchWorkflowState(workflowLabel, finalStatus, false, a.isBatchWorkflowCancelRequested(), results)

	steps := make([]WorkflowStep, 0, len(results))
	for _, result := range results {
		stepStatus := result.Status
		if result.Status == "error" {
			stepStatus = "failed"
		}
		steps = append(steps, WorkflowStep{Name: result.PresetName, Status: stepStatus, Detail: result.Detail})
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_batch_eval)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("batch eval | %d presets", len(presets)),
		Detail:  fmt.Sprintf("ok=%d, error=%d, cancelled=%d", okCount, errorCount, cancelledCount),
		Payload: marshalJSONString(map[string]any{
			"workflow":           "preset_batch_eval",
			"preset_name":        strings.Join(mapPresetNames(presets), ", "),
			"preset":             map[string]any{"name": strings.Join(mapPresetNames(presets), ", ")},
			"steps":              steps,
			"batch_preset_names": mapPresetNames(presets),
			"batch_results":      results,
		}),
	})
}

func (a *App) runBatchPresetIngest(presets []ProjectPreset) {
	results := make([]BatchWorkflowResultItem, len(presets))
	for index, preset := range presets {
		results[index] = BatchWorkflowResultItem{
			PresetName: preset.Name,
			Status:     "queued",
			Detail:     "Waiting to start.",
		}
	}
	workflowLabel := "Batch Preset Ingest"

	for index, preset := range presets {
		if a.isBatchWorkflowCancelRequested() {
			for skipped := index; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested before this preset started."
					a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
				}
			}
			break
		}

		results[index].Status = "running"
		results[index].Detail = "Running ingest..."
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		status, detail, historyStatus, steps := a.runPresetIngestJob(preset)
		results[index].Status = status
		results[index].Detail = detail
		a.updateBatchWorkflowState(workflowLabel, statusForBatchCancellation(a.isBatchWorkflowCancelRequested()), true, a.isBatchWorkflowCancelRequested(), results)

		_, _ = a.RecordExecution(ExecutionHistoryItem{
			Kind:    "workflow",
			Title:   "Workflow (preset_ingest)",
			Status:  historyStatus,
			Summary: fmt.Sprintf("ingest | %s", preset.Name),
			Detail:  detail,
			Payload: marshalJSONString(map[string]any{
				"workflow":    "preset_ingest",
				"preset_name": preset.Name,
				"preset":      preset,
				"steps":       steps,
			}),
		})

		if a.isBatchWorkflowCancelRequested() {
			for skipped := index + 1; skipped < len(results); skipped++ {
				if results[skipped].Status == "queued" {
					results[skipped].Status = "cancelled"
					results[skipped].Detail = "Skipped because batch cancellation was requested."
				}
			}
			a.updateBatchWorkflowState(workflowLabel, "cancelling", true, true, results)
			break
		}
	}

	okCount := 0
	errorCount := 0
	cancelledCount := 0
	for _, result := range results {
		switch result.Status {
		case "ok":
			okCount++
		case "error":
			errorCount++
		case "cancelled":
			cancelledCount++
		}
	}
	finalStatus := "completed"
	historyStatus := "ok"
	if cancelledCount > 0 {
		finalStatus = "cancelled"
		historyStatus = "cancelled"
	} else if errorCount > 0 && okCount == 0 {
		finalStatus = "failed"
		historyStatus = "error"
	} else if errorCount > 0 {
		finalStatus = "completed"
		historyStatus = "error"
	}
	a.updateBatchWorkflowState(workflowLabel, finalStatus, false, a.isBatchWorkflowCancelRequested(), results)

	steps := make([]WorkflowStep, 0, len(results))
	for _, result := range results {
		stepStatus := result.Status
		if result.Status == "error" {
			stepStatus = "failed"
		}
		steps = append(steps, WorkflowStep{Name: result.PresetName, Status: stepStatus, Detail: result.Detail})
	}
	_, _ = a.RecordExecution(ExecutionHistoryItem{
		Kind:    "workflow",
		Title:   "Workflow (preset_batch_ingest)",
		Status:  historyStatus,
		Summary: fmt.Sprintf("batch ingest | %d presets", len(presets)),
		Detail:  fmt.Sprintf("ok=%d, error=%d, cancelled=%d", okCount, errorCount, cancelledCount),
		Payload: marshalJSONString(map[string]any{
			"workflow":           "preset_batch_ingest",
			"preset_name":        strings.Join(mapPresetNames(presets), ", "),
			"preset":             map[string]any{"name": strings.Join(mapPresetNames(presets), ", ")},
			"steps":              steps,
			"batch_preset_names": mapPresetNames(presets),
			"batch_results":      results,
		}),
	})
}

func (a *App) runPresetVerificationJob(preset ProjectPreset) (string, string, string, []WorkflowStep) {
	representativeChecks := []struct {
		kind     string
		name     string
		stepName string
	}{
		{kind: "chat", name: strings.TrimSpace(preset.ChatRequestName), stepName: "chat_verification"},
		{kind: "ingest", name: strings.TrimSpace(preset.IngestRequestName), stepName: "ingest_verification"},
		{kind: "rag", name: strings.TrimSpace(preset.RagRequestName), stepName: "rag_verification"},
		{kind: "eval", name: strings.TrimSpace(preset.EvalRequestName), stepName: "eval_verification"},
	}

	filteredChecks := make([]struct {
		kind     string
		name     string
		stepName string
	}, 0, len(representativeChecks))
	for _, item := range representativeChecks {
		if item.name != "" {
			filteredChecks = append(filteredChecks, item)
		}
	}
	if len(filteredChecks) == 0 {
		return "error", "Preset has no representative requests.", "error", []WorkflowStep{{
			Name:   "preset_verification",
			Status: "failed",
			Detail: "Preset has no representative requests.",
		}}
	}

	runtimeSteps, err := a.prepareRuntimeProfileForPresetGo(preset, "verification")
	if err != nil {
		return "error", fmt.Sprintf("Failed to apply preset runtime profile: %v", err), "error", []WorkflowStep{{
			Name:   "runtime_profile",
			Status: "failed",
			Detail: err.Error(),
		}}
	}
	steps := append([]WorkflowStep{}, runtimeSteps...)

	validation, err := a.ValidateProjectPreset(preset)
	if err != nil {
		steps = append(steps, WorkflowStep{Name: "preset_validation", Status: "failed", Detail: err.Error()})
		return "error", "Preset validation failed.", "error", steps
	}
	if !(validation.Valid && validation.Ready) {
		steps = append(steps, buildPresetValidationStepsGo(validation)...)
		detail := "Preset is not ready for verification."
		if !validation.Valid {
			detail = "Preset is invalid for verification."
		}
		for _, item := range filteredChecks {
			steps = append(steps, WorkflowStep{Name: item.stepName, Status: "skipped", Detail: "Skipped because preset validation failed."})
		}
		return "error", detail, "error", steps
	}
	steps = append(steps, WorkflowStep{Name: "preset_validation", Status: "ok", Detail: "preset validation passed"})

	smokeResult, err := a.runSmokeForPresetGo(preset)
	if err != nil {
		steps = append(steps, WorkflowStep{Name: "smoke", Status: "failed", Detail: err.Error()})
		return "error", "Smoke checks need attention.", "error", steps
	}
	if !smokeResult.Ok {
		steps = append(steps, WorkflowStep{Name: "smoke", Status: "failed", Detail: "Smoke checks need attention."})
		for _, check := range smokeResult.Checks {
			checkStatus := "ok"
			if !check.Ok {
				checkStatus = "failed"
			}
			steps = append(steps, WorkflowStep{Name: fallbackString(check.Name, "smoke"), Status: checkStatus, Detail: check.Detail})
		}
		for _, item := range filteredChecks {
			steps = append(steps, WorkflowStep{Name: item.stepName, Status: "skipped", Detail: "Skipped because smoke failed."})
		}
		return "error", "Smoke checks need attention.", "error", steps
	}
	smokeStatus := "ok"
	smokeDetail := "Smoke checks passed."
	if !preset.WorkflowRunSmoke {
		smokeStatus = "skipped"
		smokeDetail = "Smoke skipped by preset policy."
	}
	steps = append(steps, WorkflowStep{Name: "smoke", Status: smokeStatus, Detail: smokeDetail})

	requests, err := a.readSavedRequests()
	if err != nil {
		steps = append(steps, WorkflowStep{Name: "saved_requests", Status: "failed", Detail: err.Error()})
		return "error", "Failed to load representative requests.", "error", steps
	}

	allOK := true
	for _, item := range filteredChecks {
		requestIndex := slices.IndexFunc(requests, func(request SavedRequest) bool {
			return request.Kind == item.kind && request.Name == item.name
		})
		if requestIndex < 0 {
			steps = append(steps, WorkflowStep{Name: item.stepName, Status: "failed", Detail: fmt.Sprintf("saved request not found: %s", item.name)})
			allOK = false
			continue
		}
		stepStatus, detail := a.runRepresentativeSavedRequestGo(preset, item.kind, requests[requestIndex])
		if stepStatus != "ok" {
			allOK = false
		}
		steps = append(steps, WorkflowStep{Name: item.stepName, Status: stepStatus, Detail: detail})
	}

	if allOK {
		return "ok", "representative verification completed", "ok", steps
	}
	return "error", "one or more representative verification steps failed", "error", steps
}

func (a *App) runPresetValidateJob(preset ProjectPreset) (string, string, string, []WorkflowStep) {
	validation, err := a.ValidateProjectPreset(preset)
	if err != nil {
		return "error", err.Error(), "error", []WorkflowStep{{Name: "preset_validation", Status: "failed", Detail: err.Error()}}
	}
	steps := buildPresetValidationStepsGo(validation)
	if validation.Valid && validation.Ready {
		return "ok", "preset validation passed", "ok", steps
	}
	if validation.Valid {
		return "error", "preset validation still not ready", "error", steps
	}
	return "error", "preset validation failed", "error", steps
}

func (a *App) runPresetSmokeJob(preset ProjectPreset) (string, string, string, []WorkflowStep) {
	validation, err := a.ValidateProjectPreset(preset)
	if err != nil {
		return "error", err.Error(), "error", []WorkflowStep{{Name: "preset_validation", Status: "failed", Detail: err.Error()}}
	}
	steps := buildPresetValidationStepsGo(validation)
	if !(validation.Valid && validation.Ready) {
		detail := "Preset is not ready for smoke."
		if !validation.Valid {
			detail = "Preset is invalid for smoke."
		}
		steps = append(steps, WorkflowStep{Name: "smoke", Status: "skipped", Detail: "Skipped because preset validation failed."})
		return "error", detail, "error", steps
	}

	smokeResponse, err := a.runSmokeForPresetGo(preset)
	if err != nil {
		steps = append(steps, WorkflowStep{Name: "smoke", Status: "failed", Detail: err.Error()})
		return "error", err.Error(), "error", steps
	}
	smokeStatus := "ok"
	smokeDetail := "Smoke checks passed."
	if !preset.WorkflowRunSmoke {
		smokeStatus = "skipped"
		smokeDetail = "Smoke skipped by preset policy."
	} else if smokeResponse == nil || !smokeResponse.Ok {
		smokeStatus = "failed"
		smokeDetail = "Smoke checks need attention."
	}
	steps = append(steps, WorkflowStep{Name: "smoke", Status: smokeStatus, Detail: smokeDetail})
	if smokeResponse != nil {
		for _, check := range smokeResponse.Checks {
			checkStatus := "ok"
			if !check.Ok {
				checkStatus = "failed"
			}
			steps = append(steps, WorkflowStep{Name: fallbackString(check.Name, "smoke"), Status: checkStatus, Detail: check.Detail})
		}
	}
	if smokeStatus == "failed" {
		return "error", smokeDetail, "error", steps
	}
	return "ok", smokeDetail, "ok", steps
}

func (a *App) runPresetStackIngestEvalJob(preset ProjectPreset) (string, string, string, []WorkflowStep) {
	runtimeSteps, err := a.prepareRuntimeProfileForPresetGo(preset, "stack + ingest + eval")
	if err != nil {
		return "error", fmt.Sprintf("Failed to apply preset runtime profile: %v", err), "error", []WorkflowStep{{
			Name:   "runtime_profile",
			Status: "failed",
			Detail: err.Error(),
		}}
	}
	steps := append([]WorkflowStep{}, runtimeSteps...)

	validation, err := a.ValidateProjectPreset(preset)
	if err != nil {
		steps = append(steps, WorkflowStep{Name: "preset_validation", Status: "failed", Detail: err.Error()})
		return "error", "Preset validation failed.", "error", steps
	}
	if !(validation.Valid && validation.Ready) {
		steps = append(steps, buildPresetValidationStepsGo(validation)...)
		detail := "Preset is not ready for stack + ingest + eval."
		if !validation.Valid {
			detail = "Preset is invalid for stack + ingest + eval."
		}
		steps = append(steps,
			WorkflowStep{Name: "recommended_stack", Status: "skipped", Detail: "Skipped because preset validation failed."},
			WorkflowStep{Name: "ingest", Status: "skipped", Detail: "Skipped because preset validation failed."},
			WorkflowStep{Name: "eval", Status: "skipped", Detail: "Skipped because preset validation failed."},
		)
		return "error", detail, "error", steps
	}
	steps = append(steps, WorkflowStep{Name: "preset_validation", Status: "ok", Detail: "preset validation passed"})

	smokeResult, err := a.runSmokeForPresetGo(preset)
	if err != nil {
		steps = append(steps, WorkflowStep{Name: "smoke", Status: "failed", Detail: err.Error()})
		return "error", "Smoke checks need attention.", "error", steps
	}
	if !smokeResult.Ok {
		steps = append(steps, WorkflowStep{Name: "smoke", Status: "failed", Detail: "Smoke checks need attention."})
		for _, check := range smokeResult.Checks {
			checkStatus := "ok"
			if !check.Ok {
				checkStatus = "failed"
			}
			steps = append(steps, WorkflowStep{Name: fallbackString(check.Name, "smoke"), Status: checkStatus, Detail: check.Detail})
		}
		steps = append(steps,
			WorkflowStep{Name: "recommended_stack", Status: "skipped", Detail: "Skipped because smoke failed."},
			WorkflowStep{Name: "ingest", Status: "skipped", Detail: "Skipped because smoke failed."},
			WorkflowStep{Name: "eval", Status: "skipped", Detail: "Skipped because smoke failed."},
		)
		return "error", "Smoke checks need attention.", "error", steps
	}
	smokeStatus := "ok"
	smokeDetail := "Smoke checks passed."
	if !preset.WorkflowRunSmoke {
		smokeStatus = "skipped"
		smokeDetail = "Smoke skipped by preset policy."
	}
	steps = append(steps, WorkflowStep{Name: "smoke", Status: smokeStatus, Detail: smokeDetail})

	stackResponse, err := a.StartRecommendedStack()
	stackSteps := stackActionToWorkflowSteps(stackResponse)
	if err != nil {
		steps = append(steps, stackSteps...)
		steps = append(steps,
			WorkflowStep{Name: "ingest", Status: "skipped", Detail: "Skipped because stack startup failed."},
			WorkflowStep{Name: "eval", Status: "skipped", Detail: "Skipped because stack startup failed."},
		)
		return "error", fmt.Sprintf("stack startup failed: %v", err), "error", steps
	}
	steps = append(steps, stackSteps...)

	ingestResponse, err := a.Ingest(IngestRequest{
		Paths:     splitLines(preset.IngestPaths),
		Project:   preset.IngestProject,
		Recursive: true,
	})
	if err != nil {
		steps = append(steps,
			WorkflowStep{Name: "ingest", Status: "failed", Detail: err.Error()},
			WorkflowStep{Name: "eval", Status: "skipped", Detail: "Skipped because ingest failed."},
		)
		return "error", fmt.Sprintf("ingest failed: %v", err), "error", steps
	}
	ingestDetailParts := []string{}
	if value, ok := ingestResponse["status"]; ok {
		ingestDetailParts = append(ingestDetailParts, fmt.Sprintf("status=%v", value))
	}
	if value, ok := ingestResponse["documents_indexed"]; ok {
		ingestDetailParts = append(ingestDetailParts, fmt.Sprintf("documents_indexed=%v", value))
	}
	if value, ok := ingestResponse["chunks_indexed"]; ok {
		ingestDetailParts = append(ingestDetailParts, fmt.Sprintf("chunks_indexed=%v", value))
	}
	steps = append(steps, WorkflowStep{Name: "ingest", Status: "ok", Detail: strings.Join(ingestDetailParts, ", ")})

	evalResponse, err := a.Eval(EvalRequest{
		DatasetPath: preset.EvalDataset,
		Project:     preset.EvalProject,
		SourcePath:  preset.EvalSourcePath,
		TopK:        positiveIntOrDefault(preset.EvalTopK, 5),
		WithAnswer:  preset.EvalWithAnswer,
	})
	if err != nil {
		steps = append(steps, WorkflowStep{Name: "eval", Status: "failed", Detail: err.Error()})
		return "error", fmt.Sprintf("eval failed: %v", err), "error", steps
	}
	evalDetail := fmt.Sprintf(
		"source_hit_rate=%v, keyword_hit_rate=%v, total_cases=%d",
		evalResponse.SourceHitRate,
		derefFloat64(evalResponse.KeywordHitRate),
		evalResponse.TotalCases,
	)
	steps = append(steps, WorkflowStep{Name: "eval", Status: "ok", Detail: evalDetail})
	return "ok", fmt.Sprintf("completed | %s", evalDetail), "ok", steps
}

func (a *App) runPresetWatchJob(preset ProjectPreset) (string, string, string, []WorkflowStep) {
	steps, ok, detail := a.preparePresetWorkflowGo(preset, "watch", []string{"watch"})
	if !ok {
		return "error", detail, "error", steps
	}
	paths := splitLines(preset.WatchPaths)
	interval := preset.WatchInterval
	if interval <= 0 {
		interval = 2
	}
	_, err := a.StartWatch(WatchRequest{
		Paths:     paths,
		Project:   preset.WatchProject,
		Interval:  interval,
		Recursive: true,
	})
	if err != nil {
		steps = append(steps, WorkflowStep{Name: "watch", Status: "failed", Detail: err.Error()})
		return "error", err.Error(), "error", steps
	}
	detail = fmt.Sprintf("watching %d path(s) for project %s every %.2fs", len(paths), fallbackString(preset.WatchProject, "default"), interval)
	steps = append(steps, WorkflowStep{Name: "watch", Status: "ok", Detail: detail})
	return "ok", detail, "ok", steps
}

func (a *App) runPresetIngestJob(preset ProjectPreset) (string, string, string, []WorkflowStep) {
	steps, ok, detail := a.preparePresetWorkflowGo(preset, "ingest", []string{"ingest"})
	if !ok {
		return "error", detail, "error", steps
	}
	ingestResponse, err := a.Ingest(IngestRequest{
		Paths:     splitLines(preset.IngestPaths),
		Project:   preset.IngestProject,
		Recursive: true,
	})
	if err != nil {
		steps = append(steps, WorkflowStep{Name: "ingest", Status: "failed", Detail: err.Error()})
		return "error", err.Error(), "error", steps
	}
	detail = summarizeIngestResponseDetail(ingestResponse)
	steps = append(steps, WorkflowStep{Name: "ingest", Status: "ok", Detail: detail})
	return "ok", detail, "ok", steps
}

func (a *App) runPresetEvalJob(preset ProjectPreset) (string, string, string, []WorkflowStep) {
	steps, ok, detail := a.preparePresetWorkflowGo(preset, "eval", []string{"eval"})
	if !ok {
		return "error", detail, "error", steps
	}
	evalResponse, err := a.Eval(EvalRequest{
		DatasetPath: preset.EvalDataset,
		Project:     preset.EvalProject,
		SourcePath:  preset.EvalSourcePath,
		TopK:        positiveIntOrDefault(preset.EvalTopK, 5),
		WithAnswer:  preset.EvalWithAnswer,
	})
	if err != nil {
		steps = append(steps, WorkflowStep{Name: "eval", Status: "failed", Detail: err.Error()})
		return "error", err.Error(), "error", steps
	}
	detail = summarizeEvalResponseDetail(evalResponse)
	steps = append(steps, WorkflowStep{Name: "eval", Status: "ok", Detail: detail})
	return "ok", detail, "ok", steps
}

func (a *App) runPresetIngestEvalJob(preset ProjectPreset) (string, string, string, []WorkflowStep) {
	steps, ok, detail := a.preparePresetWorkflowGo(preset, "ingest + eval", []string{"ingest", "eval"})
	if !ok {
		return "error", detail, "error", steps
	}
	ingestResponse, err := a.Ingest(IngestRequest{
		Paths:     splitLines(preset.IngestPaths),
		Project:   preset.IngestProject,
		Recursive: true,
	})
	if err != nil {
		steps = append(steps,
			WorkflowStep{Name: "ingest", Status: "failed", Detail: err.Error()},
			WorkflowStep{Name: "eval", Status: "skipped", Detail: "Skipped because ingest failed."},
		)
		return "error", fmt.Sprintf("ingest failed: %v", err), "error", steps
	}
	ingestDetail := summarizeIngestResponseDetail(ingestResponse)
	steps = append(steps, WorkflowStep{Name: "ingest", Status: "ok", Detail: ingestDetail})
	evalResponse, err := a.Eval(EvalRequest{
		DatasetPath: preset.EvalDataset,
		Project:     preset.EvalProject,
		SourcePath:  preset.EvalSourcePath,
		TopK:        positiveIntOrDefault(preset.EvalTopK, 5),
		WithAnswer:  preset.EvalWithAnswer,
	})
	if err != nil {
		steps = append(steps, WorkflowStep{Name: "eval", Status: "failed", Detail: err.Error()})
		return "error", fmt.Sprintf("eval failed: %v", err), "error", steps
	}
	evalDetail := summarizeEvalResponseDetail(evalResponse)
	steps = append(steps, WorkflowStep{Name: "eval", Status: "ok", Detail: evalDetail})
	return "ok", fmt.Sprintf("completed | %s", evalDetail), "ok", steps
}

func (a *App) runPresetRecoveryActionJob(request PresetRecoveryActionRequest) (string, string, string, []WorkflowStep) {
	preset := request.Preset
	actionKind := strings.TrimSpace(request.ActionKind)
	serviceName := strings.TrimSpace(request.ServiceName)

	switch actionKind {
	case "apply-runtime-profile":
		runtimeSteps, err := a.prepareRuntimeProfileForPresetGo(preset, "recovery")
		if err != nil {
			return "error", err.Error(), "error", []WorkflowStep{{Name: "runtime_profile", Status: "failed", Detail: err.Error()}}
		}
		validation, err := a.ValidateProjectPreset(preset)
		steps := append([]WorkflowStep{}, runtimeSteps...)
		if err != nil {
			steps = append(steps, WorkflowStep{Name: "preset_validation", Status: "failed", Detail: err.Error()})
			return "error", err.Error(), "error", steps
		}
		steps = append(steps, buildPresetValidationStepsGo(validation)...)
		return "ok", latestWorkflowStepDetail(runtimeSteps, fmt.Sprintf("Applied runtime profile for preset: %s", fallbackString(preset.Name, "(unnamed preset)"))), "ok", steps
	case "start-service":
		detail, err := a.startValidationServiceByNameGo(serviceName)
		steps := []WorkflowStep{}
		if err != nil {
			steps = append(steps, WorkflowStep{Name: fallbackString(serviceName, "service"), Status: "failed", Detail: err.Error()})
			return "error", err.Error(), "error", steps
		}
		steps = append(steps, WorkflowStep{Name: fallbackString(serviceName, "service"), Status: "ok", Detail: detail})
		validation, validationErr := a.ValidateProjectPreset(preset)
		if validationErr != nil {
			steps = append(steps, WorkflowStep{Name: "preset_validation", Status: "failed", Detail: validationErr.Error()})
			return "error", validationErr.Error(), "error", steps
		}
		steps = append(steps, buildPresetValidationStepsGo(validation)...)
		return "ok", detail, "ok", steps
	case "start-recommended-stack":
		stackResponse, err := a.StartRecommendedStack()
		steps := stackActionToWorkflowSteps(stackResponse)
		validation, validationErr := a.ValidateProjectPreset(preset)
		if validationErr == nil {
			steps = append(steps, buildPresetValidationStepsGo(validation)...)
		} else {
			steps = append(steps, WorkflowStep{Name: "preset_validation", Status: "failed", Detail: validationErr.Error()})
		}
		if err != nil {
			return "error", fmt.Sprintf("recommended stack failed: %v", err), "error", steps
		}
		if validationErr != nil {
			return "error", validationErr.Error(), "error", steps
		}
		return "ok", fallbackString(stackResponse.Status, "recommended stack started"), "ok", steps
	case "run-smoke":
		smokeResponse, err := a.runSmokeForPresetGo(preset)
		if err != nil {
			return "error", err.Error(), "error", []WorkflowStep{{Name: "smoke", Status: "failed", Detail: err.Error()}}
		}
		smokeStatus := "ok"
		smokeDetail := "Smoke checks passed."
		if !preset.WorkflowRunSmoke {
			smokeStatus = "skipped"
			smokeDetail = "Smoke skipped by preset policy."
		} else if smokeResponse == nil || !smokeResponse.Ok {
			smokeStatus = "failed"
			smokeDetail = "Smoke checks need attention."
		}
		steps := []WorkflowStep{{Name: "smoke", Status: smokeStatus, Detail: smokeDetail}}
		if smokeResponse != nil {
			for _, check := range smokeResponse.Checks {
				checkStatus := "ok"
				if !check.Ok {
					checkStatus = "failed"
				}
				steps = append(steps, WorkflowStep{Name: fallbackString(check.Name, "smoke"), Status: checkStatus, Detail: check.Detail})
			}
		}
		if smokeStatus == "failed" {
			return "error", smokeDetail, "error", steps
		}
		return "ok", smokeDetail, "ok", steps
	case "validate":
		validation, err := a.ValidateProjectPreset(preset)
		if err != nil {
			return "error", err.Error(), "error", []WorkflowStep{{Name: "preset_validation", Status: "failed", Detail: err.Error()}}
		}
		steps := buildPresetValidationStepsGo(validation)
		if validation.Ready {
			return "ok", "preset validation passed", "ok", steps
		}
		if validation.Valid {
			return "running", "preset validation still not ready", "error", steps
		}
		return "error", "preset validation failed", "error", steps
	default:
		detail := fmt.Sprintf("unsupported recovery action: %s", actionKind)
		return "error", detail, "error", []WorkflowStep{{
			Name:   fallbackString(actionKind, "recovery"),
			Status: "failed",
			Detail: detail,
		}}
	}
}

func (a *App) runPresetRuntimeStackPrepareJob(preset ProjectPreset) (string, string, string, []WorkflowStep) {
	runtimeStatus, runtimeDetail, _, runtimeSteps := a.runPresetRecoveryActionJob(PresetRecoveryActionRequest{
		Preset:     preset,
		ActionKind: "apply-runtime-profile",
		StepName:   "runtime_stack_prepare",
	})
	steps := append([]WorkflowStep{}, runtimeSteps...)
	if runtimeStatus != "ok" {
		steps = append(steps, WorkflowStep{
			Name:   "recommended_stack",
			Status: "skipped",
			Detail: "Skipped because runtime profile preparation did not complete successfully.",
		})
		return "error", fallbackString(runtimeDetail, fmt.Sprintf("runtime preparation had issues for preset: %s", fallbackString(preset.Name, "(unnamed preset)"))), "error", steps
	}

	stackStatus, stackDetail, _, stackSteps := a.runPresetRecoveryActionJob(PresetRecoveryActionRequest{
		Preset:     preset,
		ActionKind: "start-recommended-stack",
		StepName:   "runtime_stack_prepare",
	})
	steps = append(steps, stackSteps...)
	if stackStatus != "ok" {
		return "error", fallbackString(stackDetail, fmt.Sprintf("runtime preparation had issues for preset: %s", fallbackString(preset.Name, "(unnamed preset)"))), "error", steps
	}
	return "ok", fmt.Sprintf("applied runtime profile and started stack for preset: %s", fallbackString(preset.Name, "(unnamed preset)")), "ok", steps
}

func (a *App) runRuntimeConfigActionJob(request RuntimeConfigActionRequest) (string, string, string, []WorkflowStep) {
	action := strings.TrimSpace(request.Action)
	switch action {
	case "reload_gateway_config":
		response, err := a.ReloadGatewayConfig()
		if err != nil {
			return "error", err.Error(), "error", []WorkflowStep{{Name: "reload_gateway_config", Status: "failed", Detail: err.Error()}}
		}
		detail := fmt.Sprintf("reloaded gateway config with %d configured model(s)", len(response.ConfiguredModels))
		return "ok", detail, "ok", []WorkflowStep{{Name: "reload_gateway_config", Status: "ok", Detail: detail}}
	case "apply_local_only":
		if _, err := a.DeleteLocalConfigFile(LocalConfigNameRequest{Name: "models.local.yaml"}); err != nil {
			return "error", err.Error(), "error", []WorkflowStep{{Name: "models.local.yaml", Status: "failed", Detail: err.Error()}}
		}
		if _, err := a.SaveLocalConfigFile(SaveLocalConfigRequest{Name: "rag.local.yaml", Content: ragLocalOnlyPreset}); err != nil {
			return "error", err.Error(), "error", []WorkflowStep{{Name: "rag.local.yaml", Status: "failed", Detail: err.Error()}}
		}
		response, err := a.ReloadGatewayConfig()
		steps := []WorkflowStep{
			{Name: "models.local.yaml", Status: "ok", Detail: "removed local models override"},
			{Name: "rag.local.yaml", Status: "ok", Detail: "applied local-only rag override"},
		}
		if err != nil {
			steps = append(steps, WorkflowStep{Name: "reload_gateway_config", Status: "failed", Detail: err.Error()})
			return "error", err.Error(), "error", steps
		}
		steps = append(steps, WorkflowStep{Name: "reload_gateway_config", Status: "ok", Detail: fmt.Sprintf("reloaded gateway config with %d configured model(s)", len(response.ConfiguredModels))})
		return "ok", "applied local-only runtime config", "ok", steps
	case "apply_external_rag":
		if _, err := a.SaveLocalConfigFile(SaveLocalConfigRequest{Name: "models.local.yaml", Content: modelsLocalExternalPreset}); err != nil {
			return "error", err.Error(), "error", []WorkflowStep{{Name: "models.local.yaml", Status: "failed", Detail: err.Error()}}
		}
		if _, err := a.SaveLocalConfigFile(SaveLocalConfigRequest{Name: "rag.local.yaml", Content: ragExternalPreset}); err != nil {
			return "error", err.Error(), "error", []WorkflowStep{{Name: "rag.local.yaml", Status: "failed", Detail: err.Error()}}
		}
		response, err := a.ReloadGatewayConfig()
		steps := []WorkflowStep{
			{Name: "models.local.yaml", Status: "ok", Detail: "applied external embedding models override"},
			{Name: "rag.local.yaml", Status: "ok", Detail: "applied external rag override"},
		}
		if err != nil {
			steps = append(steps, WorkflowStep{Name: "reload_gateway_config", Status: "failed", Detail: err.Error()})
			return "error", err.Error(), "error", steps
		}
		steps = append(steps, WorkflowStep{Name: "reload_gateway_config", Status: "ok", Detail: fmt.Sprintf("reloaded gateway config with %d configured model(s)", len(response.ConfiguredModels))})
		return "ok", "applied external embedding + qdrant runtime config", "ok", steps
	case "save_local_config":
		name := strings.TrimSpace(request.Name)
		if _, err := a.SaveLocalConfigFile(SaveLocalConfigRequest{Name: name, Content: request.Content}); err != nil {
			return "error", err.Error(), "error", []WorkflowStep{{Name: fallbackString(name, "local_config"), Status: "failed", Detail: err.Error()}}
		}
		detail := fmt.Sprintf("saved local config: %s", name)
		return "ok", detail, "ok", []WorkflowStep{{Name: fallbackString(name, "local_config"), Status: "ok", Detail: detail}}
	case "delete_local_config":
		name := strings.TrimSpace(request.Name)
		if _, err := a.DeleteLocalConfigFile(LocalConfigNameRequest{Name: name}); err != nil {
			return "error", err.Error(), "error", []WorkflowStep{{Name: fallbackString(name, "local_config"), Status: "failed", Detail: err.Error()}}
		}
		detail := fmt.Sprintf("deleted local config: %s", name)
		return "ok", detail, "ok", []WorkflowStep{{Name: fallbackString(name, "local_config"), Status: "ok", Detail: detail}}
	default:
		detail := fmt.Sprintf("unsupported runtime config action: %s", action)
		return "error", detail, "error", []WorkflowStep{{Name: fallbackString(action, "runtime_config"), Status: "failed", Detail: detail}}
	}
}

func (a *App) runRuntimeServiceActionJob(request RuntimeServiceActionRequest) (string, string, string, []WorkflowStep) {
	action := strings.TrimSpace(request.Action)
	switch action {
	case "start_fast":
		_, err := a.StartFast()
		return runtimeServiceActionResult("fast", "started", err)
	case "stop_fast":
		_, err := a.StopFast()
		return runtimeServiceActionResult("fast", "stopped", err)
	case "start_work":
		_, err := a.StartWork()
		return runtimeServiceActionResult("work", "started", err)
	case "stop_work":
		_, err := a.StopWork()
		return runtimeServiceActionResult("work", "stopped", err)
	case "start_code":
		_, err := a.StartCode()
		return runtimeServiceActionResult("code", "started", err)
	case "stop_code":
		_, err := a.StopCode()
		return runtimeServiceActionResult("code", "stopped", err)
	case "start_gateway":
		_, err := a.StartGateway()
		return runtimeServiceActionResult("gateway", "started", err)
	case "stop_gateway":
		_, err := a.StopGateway()
		return runtimeServiceActionResult("gateway", "stopped", err)
	case "start_embedding":
		_, err := a.StartEmbedding()
		return runtimeServiceActionResult("embedding", "started", err)
	case "stop_embedding":
		_, err := a.StopEmbedding()
		return runtimeServiceActionResult("embedding", "stopped", err)
	case "start_qdrant":
		_, err := a.StartQdrant()
		return runtimeServiceActionResult("qdrant", "started", err)
	case "stop_qdrant":
		_, err := a.StopQdrant()
		return runtimeServiceActionResult("qdrant", "stopped", err)
	case "start_watch":
		_, err := a.StartWatch(request.Watch)
		if err != nil {
			return "error", err.Error(), "error", []WorkflowStep{{Name: "watch", Status: "failed", Detail: err.Error()}}
		}
		projectName := fallbackString(strings.TrimSpace(request.Watch.Project), "default")
		detail := fmt.Sprintf("watching %d path(s) for project %s every %.2fs", len(request.Watch.Paths), projectName, request.Watch.Interval)
		return "ok", detail, "ok", []WorkflowStep{{Name: "watch", Status: "ok", Detail: detail}}
	case "stop_watch":
		_, err := a.StopWatch()
		return runtimeServiceActionResult("watch", "stopped", err)
	default:
		detail := fmt.Sprintf("unsupported runtime service action: %s", action)
		return "error", detail, "error", []WorkflowStep{{Name: fallbackString(action, "runtime_service"), Status: "failed", Detail: detail}}
	}
}

func (a *App) preparePresetWorkflowGo(preset ProjectPreset, workflowName string, skippedSteps []string) ([]WorkflowStep, bool, string) {
	runtimeSteps, err := a.prepareRuntimeProfileForPresetGo(preset, workflowName)
	if err != nil {
		return []WorkflowStep{{Name: "runtime_profile", Status: "failed", Detail: err.Error()}}, false, fmt.Sprintf("Failed to apply preset runtime profile: %v", err)
	}
	steps := append([]WorkflowStep{}, runtimeSteps...)
	validation, err := a.ValidateProjectPreset(preset)
	if err != nil {
		steps = append(steps, WorkflowStep{Name: "preset_validation", Status: "failed", Detail: err.Error()})
		return steps, false, "Preset validation failed."
	}
	if !(validation.Valid && validation.Ready) {
		steps = append(steps, buildPresetValidationStepsGo(validation)...)
		for _, name := range skippedSteps {
			steps = append(steps, WorkflowStep{Name: name, Status: "skipped", Detail: "Skipped because preset validation failed."})
		}
		detail := "Preset is not ready."
		if !validation.Valid {
			detail = "Preset is invalid."
		}
		return steps, false, detail
	}
	steps = append(steps, WorkflowStep{Name: "preset_validation", Status: "ok", Detail: "preset validation passed"})
	smokeResult, err := a.runSmokeForPresetGo(preset)
	if err != nil {
		steps = append(steps, WorkflowStep{Name: "smoke", Status: "failed", Detail: err.Error()})
		return steps, false, "Smoke checks need attention."
	}
	if !smokeResult.Ok {
		steps = append(steps, WorkflowStep{Name: "smoke", Status: "failed", Detail: "Smoke checks need attention."})
		for _, check := range smokeResult.Checks {
			checkStatus := "ok"
			if !check.Ok {
				checkStatus = "failed"
			}
			steps = append(steps, WorkflowStep{Name: fallbackString(check.Name, "smoke"), Status: checkStatus, Detail: check.Detail})
		}
		for _, name := range skippedSteps {
			steps = append(steps, WorkflowStep{Name: name, Status: "skipped", Detail: "Skipped because smoke failed."})
		}
		return steps, false, "Smoke checks need attention."
	}
	smokeStatus := "ok"
	smokeDetail := "Smoke checks passed."
	if !preset.WorkflowRunSmoke {
		smokeStatus = "skipped"
		smokeDetail = "Smoke skipped by preset policy."
	}
	steps = append(steps, WorkflowStep{Name: "smoke", Status: smokeStatus, Detail: smokeDetail})
	return steps, true, ""
}

func (a *App) runRepresentativeSavedRequestGo(preset ProjectPreset, kind string, request SavedRequest) (string, string) {
	switch kind {
	case "chat":
		mode := fallbackString(request.Mode, "auto")
		response, err := a.Chat(ChatRequest{
			Mode:        mode,
			Prompt:      request.Prompt,
			Temperature: 0.2,
			MaxTokens:   defaultChatMaxTokens(mode),
		})
		if err != nil {
			return "failed", err.Error()
		}
		answer := strings.TrimSpace(response.Answer)
		ok := true
		detail := fmt.Sprintf("chat:%s | %s", request.Name, trimDetail(answer))
		if preset.ChatExpectContains != "" && !strings.Contains(answer, preset.ChatExpectContains) {
			ok = false
			detail = fmt.Sprintf("%s | missing expected text: %s", detail, preset.ChatExpectContains)
		} else if preset.ChatExpectContains != "" {
			detail = fmt.Sprintf("%s | matched expected text", detail)
		}
		detail = fmt.Sprintf("%s | answer_chars=%d", detail, len(answer))
		if ok {
			return "ok", detail
		}
		return "failed", detail
	case "ingest":
		paths := splitLines(request.Paths)
		response, err := a.Ingest(IngestRequest{
			Paths:     paths,
			Project:   request.Project,
			Recursive: true,
		})
		if err != nil {
			return "failed", err.Error()
		}
		detailParts := []string{fmt.Sprintf("ingest:%s", request.Name)}
		if status, ok := response["status"]; ok {
			detailParts = append(detailParts, fmt.Sprintf("status=%v", status))
		}
		if value, ok := response["documents_indexed"]; ok {
			detailParts = append(detailParts, fmt.Sprintf("documents_indexed=%v", value))
		}
		if value, ok := response["chunks_indexed"]; ok {
			detailParts = append(detailParts, fmt.Sprintf("chunks_indexed=%v", value))
		}
		return "ok", strings.Join(detailParts, " | ")
	case "rag":
		if request.Answer {
			response, err := a.Query(QueryRequest{
				Query:      request.Query,
				Project:    request.Project,
				SourcePath: request.SourcePath,
				TopK:       positiveIntOrDefault(request.TopK, 5),
				Answer:     true,
			})
			if err != nil {
				return "failed", err.Error()
			}
			text := strings.TrimSpace(response.Answer)
			sourceCount := len(response.Sources)
			topSource := ""
			if sourceCount > 0 {
				topSource = response.Sources[0].SourcePath
			}
			ok := true
			detail := fmt.Sprintf("rag:%s | %s | source_count=%d", request.Name, trimDetail(text), sourceCount)
			if topSource != "" {
				detail = fmt.Sprintf("%s | top_source=%s", detail, topSource)
			}
			if preset.RagExpectContains != "" && !strings.Contains(text, preset.RagExpectContains) {
				ok = false
				detail = fmt.Sprintf("%s | missing expected text: %s", detail, preset.RagExpectContains)
			} else if preset.RagExpectContains != "" {
				detail = fmt.Sprintf("%s | matched expected text", detail)
			}
			detail = fmt.Sprintf("%s | answer_chars=%d", detail, len(text))
			if ok {
				return "ok", detail
			}
			return "failed", detail
		}
		response, err := a.Search(SearchRequest{
			Query:      request.Query,
			Project:    request.Project,
			SourcePath: request.SourcePath,
			TopK:       positiveIntOrDefault(request.TopK, 5),
		})
		if err != nil {
			return "failed", err.Error()
		}
		joined := make([]string, 0, len(response.Results))
		for _, item := range response.Results {
			joined = append(joined, item.ChunkText)
		}
		text := strings.Join(joined, "\n")
		sourceCount := len(response.Results)
		topSource := ""
		if sourceCount > 0 {
			topSource = response.Results[0].SourcePath
		}
		ok := true
		detail := fmt.Sprintf("rag:%s | %d results | source_count=%d", request.Name, sourceCount, sourceCount)
		if topSource != "" {
			detail = fmt.Sprintf("%s | top_source=%s", detail, topSource)
		}
		if preset.RagExpectContains != "" && !strings.Contains(text, preset.RagExpectContains) {
			ok = false
			detail = fmt.Sprintf("%s | missing expected text: %s", detail, preset.RagExpectContains)
		} else if preset.RagExpectContains != "" {
			detail = fmt.Sprintf("%s | matched expected text", detail)
		}
		detail = fmt.Sprintf("%s | answer_chars=%d", detail, len(text))
		if ok {
			return "ok", detail
		}
		return "failed", detail
	case "eval":
		response, err := a.Eval(EvalRequest{
			DatasetPath: request.DatasetPath,
			Project:     request.Project,
			SourcePath:  request.SourcePath,
			TopK:        positiveIntOrDefault(request.TopK, 5),
			WithAnswer:  request.WithAnswer,
		})
		if err != nil {
			return "failed", err.Error()
		}
		ok := true
		detail := fmt.Sprintf(
			"eval:%s | source_hit_rate=%v | keyword_hit_rate=%v | total_cases=%d",
			request.Name,
			response.SourceHitRate,
			derefFloat64(response.KeywordHitRate),
			response.TotalCases,
		)
		if preset.EvalMinSourceHitRate > 0 && response.SourceHitRate < preset.EvalMinSourceHitRate {
			ok = false
			detail = fmt.Sprintf("%s | source_hit_rate %v < %v", detail, response.SourceHitRate, preset.EvalMinSourceHitRate)
		} else if preset.EvalMinSourceHitRate > 0 {
			detail = fmt.Sprintf("%s | source_hit_rate >= %v", detail, preset.EvalMinSourceHitRate)
		}
		if ok {
			return "ok", detail
		}
		return "failed", detail
	default:
		return "failed", fmt.Sprintf("unsupported verification step: %s", kind)
	}
}

func (a *App) prepareRuntimeProfileForPresetGo(preset ProjectPreset, workflowName string) ([]WorkflowStep, error) {
	switch strings.TrimSpace(preset.RuntimeProfile) {
	case "", "current":
		return []WorkflowStep{{Name: "runtime_profile", Status: "skipped", Detail: "Using current runtime config."}}, nil
	case "local_only":
		if _, err := a.DeleteLocalConfigFile(LocalConfigNameRequest{Name: "models.local.yaml"}); err != nil {
			return nil, err
		}
		if _, err := a.SaveLocalConfigFile(SaveLocalConfigRequest{Name: "rag.local.yaml", Content: ragLocalOnlyPreset}); err != nil {
			return nil, err
		}
		if _, err := a.ReloadGatewayConfig(); err != nil {
			return nil, err
		}
		return []WorkflowStep{{Name: "runtime_profile", Status: "ok", Detail: fmt.Sprintf("Applied runtime profile for %s: Auto apply local-only runtime", workflowName)}}, nil
	case "external_rag":
		if _, err := a.SaveLocalConfigFile(SaveLocalConfigRequest{Name: "models.local.yaml", Content: modelsLocalExternalPreset}); err != nil {
			return nil, err
		}
		if _, err := a.SaveLocalConfigFile(SaveLocalConfigRequest{Name: "rag.local.yaml", Content: ragExternalPreset}); err != nil {
			return nil, err
		}
		if _, err := a.ReloadGatewayConfig(); err != nil {
			return nil, err
		}
		return []WorkflowStep{{Name: "runtime_profile", Status: "ok", Detail: fmt.Sprintf("Applied runtime profile for %s: Auto apply external embedding + qdrant runtime", workflowName)}}, nil
	default:
		return nil, fmt.Errorf("unsupported runtime profile: %s", preset.RuntimeProfile)
	}
}

func (a *App) runSmokeForPresetGo(preset ProjectPreset) (*SmokeResponse, error) {
	if !preset.WorkflowRunSmoke {
		return &SmokeResponse{Ok: true, Checks: []SmokeCheckItem{}}, nil
	}
	return a.Smoke(SmokeRequest{
		GatewayURL:    a.baseURL,
		SkipQdrant:    preset.SmokeSkipQdrant,
		SkipEmbedding: preset.SmokeSkipEmbedding,
		SkipReranker:  preset.SmokeSkipReranker,
	})
}

func buildPresetValidationStepsGo(validation *PresetValidationResponse) []WorkflowStep {
	if validation == nil {
		return []WorkflowStep{{Name: "preset_validation", Status: "failed", Detail: "preset validation did not return a result"}}
	}

	steps := make([]WorkflowStep, 0)
	for _, warning := range validation.Warnings {
		steps = append(steps, WorkflowStep{Name: "preset_validation", Status: "failed", Detail: warning})
	}
	for _, warning := range validation.ConfigWarnings {
		steps = append(steps, WorkflowStep{Name: "runtime_config", Status: "failed", Detail: warning})
	}
	for _, check := range validation.PathChecks {
		if check.Required && !check.Exists {
			steps = append(steps, WorkflowStep{
				Name:   check.Label,
				Status: "failed",
				Detail: fmt.Sprintf("%s: %s", check.Detail, fallbackString(check.ResolvedPath, check.Path)),
			})
		}
	}
	for _, check := range validation.ServiceChecks {
		if check.Required && check.Status != "running" {
			steps = append(steps, WorkflowStep{
				Name:   check.Name,
				Status: "failed",
				Detail: fallbackString(check.Detail, fmt.Sprintf("required service is %s", check.Status)),
			})
		}
	}
	if len(steps) == 0 {
		steps = append(steps, WorkflowStep{Name: "preset_validation", Status: "ok", Detail: "preset validation passed"})
	}
	return steps
}

func (a *App) startValidationServiceByNameGo(name string) (string, error) {
	switch strings.TrimSpace(name) {
	case "fast":
		_, err := a.StartFast()
		return "Started fast", err
	case "work":
		_, err := a.StartWork()
		return "Started work", err
	case "code":
		_, err := a.StartCode()
		return "Started code", err
	case "gateway":
		_, err := a.StartGateway()
		return "Started gateway", err
	case "embedding":
		_, err := a.StartEmbedding()
		return "Started embedding", err
	case "qdrant":
		_, err := a.StartQdrant()
		return "Started qdrant", err
	default:
		return "", fmt.Errorf("unsupported service start action: %s", name)
	}
}

func (a *App) updateBatchWorkflowState(workflowLabel string, status string, running bool, cancelRequested bool, results []BatchWorkflowResultItem) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.batchWorkflowState == nil {
		a.batchWorkflowState = &BatchWorkflowState{}
	}
	a.batchWorkflowState.WorkflowLabel = workflowLabel
	a.batchWorkflowState.Status = status
	a.batchWorkflowState.Running = running
	a.batchWorkflowState.CancelRequested = cancelRequested
	a.batchWorkflowState.Results = append([]BatchWorkflowResultItem(nil), results...)
	_ = a.writeBatchWorkflowState(a.batchWorkflowState)
}

func (a *App) isBatchWorkflowCancelRequested() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.batchWorkflowState != nil && a.batchWorkflowState.CancelRequested
}

func statusForBatchCancellation(cancelRequested bool) string {
	if cancelRequested {
		return "cancelling"
	}
	return "running"
}

func (a *App) startBatchWorkflowState(presetNames []string, workflowLabel string) ([]ProjectPreset, *BatchWorkflowState, error) {
	if len(presetNames) == 0 {
		return nil, nil, fmt.Errorf("at least one preset name is required")
	}

	presets, err := a.readProjectPresets()
	if err != nil {
		return nil, nil, err
	}

	selectedPresets := make([]ProjectPreset, 0, len(presetNames))
	for _, name := range presetNames {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		index := slices.IndexFunc(presets, func(p ProjectPreset) bool {
			return p.Name == trimmed
		})
		if index < 0 {
			return nil, nil, fmt.Errorf("preset not found: %s", trimmed)
		}
		selectedPresets = append(selectedPresets, presets[index])
	}
	if len(selectedPresets) == 0 {
		return nil, nil, fmt.Errorf("no matching presets were found")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.batchWorkflowState != nil && a.batchWorkflowState.Running {
		return nil, cloneBatchWorkflowState(a.batchWorkflowState), fmt.Errorf("batch workflow already running")
	}
	a.batchWorkflowState = &BatchWorkflowState{
		WorkflowLabel:   workflowLabel,
		Status:          "running",
		Running:         true,
		CancelRequested: false,
		Results:         make([]BatchWorkflowResultItem, len(selectedPresets)),
	}
	for index, preset := range selectedPresets {
		a.batchWorkflowState.Results[index] = BatchWorkflowResultItem{
			PresetName: preset.Name,
			Status:     "queued",
			Detail:     "Waiting to start.",
		}
	}
	_ = a.writeBatchWorkflowState(a.batchWorkflowState)
	_ = a.writeBatchPresetSelection(mapPresetNames(selectedPresets))
	return selectedPresets, cloneBatchWorkflowState(a.batchWorkflowState), nil
}

func mapPresetNames(presets []ProjectPreset) []string {
	names := make([]string, 0, len(presets))
	for _, preset := range presets {
		names = append(names, preset.Name)
	}
	return names
}

func stackActionToWorkflowSteps(response *StackActionResponse) []WorkflowStep {
	if response == nil {
		return []WorkflowStep{{Name: "recommended_stack", Status: "failed", Detail: "no stack response returned"}}
	}
	steps := make([]WorkflowStep, 0, len(response.Steps))
	for name, detail := range response.Steps {
		text := fmt.Sprint(detail)
		status := "ok"
		lower := strings.ToLower(name + " " + text + " " + response.Status)
		if strings.Contains(lower, "failed") || strings.Contains(lower, "error") {
			status = "failed"
		} else if strings.Contains(lower, "skipped") {
			status = "skipped"
		}
		steps = append(steps, WorkflowStep{Name: name, Status: status, Detail: text})
	}
	if len(steps) == 0 {
		steps = append(steps, WorkflowStep{Name: "recommended_stack", Status: "ok", Detail: response.Status})
	}
	return steps
}

func smokeResponseToWorkflowSteps(response *SmokeResponse, smokeErr error) []WorkflowStep {
	if smokeErr != nil {
		return []WorkflowStep{{Name: "smoke", Status: "failed", Detail: smokeErr.Error()}}
	}
	if response == nil {
		return []WorkflowStep{{Name: "smoke", Status: "failed", Detail: "no smoke response returned"}}
	}
	steps := make([]WorkflowStep, 0, len(response.Checks)+1)
	summaryStatus := "ok"
	summaryDetail := "Smoke checks passed."
	if !response.Ok {
		summaryStatus = "failed"
		summaryDetail = "Smoke checks need attention."
	}
	steps = append(steps, WorkflowStep{Name: "smoke", Status: summaryStatus, Detail: summaryDetail})
	for _, check := range response.Checks {
		checkStatus := "ok"
		if !check.Ok {
			checkStatus = "failed"
		}
		steps = append(steps, WorkflowStep{Name: fallbackString(check.Name, "smoke"), Status: checkStatus, Detail: check.Detail})
	}
	return steps
}

func runtimeServiceActionResult(serviceName string, verb string, err error) (string, string, string, []WorkflowStep) {
	if err != nil {
		return "error", err.Error(), "error", []WorkflowStep{{Name: serviceName, Status: "failed", Detail: err.Error()}}
	}
	detail := fmt.Sprintf("%s %s", serviceName, verb)
	return "ok", detail, "ok", []WorkflowStep{{Name: serviceName, Status: "ok", Detail: detail}}
}

func responseStatusOrEmpty(response *StackActionResponse) string {
	if response == nil {
		return ""
	}
	return strings.TrimSpace(response.Status)
}

func truncateString(value string, limit int) string {
	text := strings.TrimSpace(value)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit]
}

func summarizeIngestResponseDetail(response map[string]any) string {
	parts := []string{}
	if value, ok := response["status"]; ok {
		parts = append(parts, fmt.Sprintf("status=%v", value))
	}
	if value, ok := response["documents_indexed"]; ok {
		parts = append(parts, fmt.Sprintf("documents_indexed=%v", value))
	}
	if value, ok := response["chunks_indexed"]; ok {
		parts = append(parts, fmt.Sprintf("chunks_indexed=%v", value))
	}
	if len(parts) == 0 {
		return "ingest completed"
	}
	return strings.Join(parts, ", ")
}

func summarizeEvalResponseDetail(response *EvalResponse) string {
	if response == nil {
		return "eval completed"
	}
	return fmt.Sprintf(
		"source_hit_rate=%v, keyword_hit_rate=%v, total_cases=%d, average_latency_ms=%v, total_prompt_tokens=%v, total_completion_tokens=%v, total_tokens=%v",
		response.SourceHitRate,
		derefFloat64(response.KeywordHitRate),
		response.TotalCases,
		derefFloat64(response.AverageLatency),
		derefInt(response.TotalPrompt),
		derefInt(response.TotalComplete),
		derefInt(response.TotalTokens),
	)
}

func derefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func latestWorkflowStepDetail(steps []WorkflowStep, defaultDetail string) string {
	if len(steps) == 0 {
		return defaultDetail
	}
	for index := len(steps) - 1; index >= 0; index-- {
		if text := strings.TrimSpace(steps[index].Detail); text != "" {
			return text
		}
	}
	return defaultDetail
}

func marshalJSONString(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func positiveIntOrDefault(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func derefFloat64(value *float64) any {
	if value == nil {
		return "-"
	}
	return *value
}

func trimDetail(value string) string {
	compact := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if compact == "" {
		return "-"
	}
	if len(compact) <= 160 {
		return compact
	}
	return compact[:157] + "..."
}

func (a *App) getJSON(path string, out any) error {
	response, err := a.httpClient.Get(a.baseURL + path)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("gateway returned %s", response.Status)
	}

	return json.NewDecoder(response.Body).Decode(out)
}

func (a *App) postJSON(path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	response, err := a.httpClient.Post(a.baseURL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("gateway returned %s", response.Status)
	}

	return json.NewDecoder(response.Body).Decode(out)
}

func (a *App) streamGatewayResponse(path string, payload any, onEvent func(eventType string, data string) error) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	request, err := http.NewRequest(http.MethodPost, a.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")

	response, err := a.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		data, _ := io.ReadAll(response.Body)
		trimmed := strings.TrimSpace(string(data))
		if trimmed == "" {
			return fmt.Errorf("gateway returned %s", response.Status)
		}
		return fmt.Errorf("gateway returned %s: %s", response.Status, trimmed)
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	eventType := ""
	dataLines := []string{}

	dispatch := func() error {
		if len(dataLines) == 0 {
			eventType = ""
			return nil
		}
		data := strings.Join(dataLines, "\n")
		err := onEvent(eventType, data)
		eventType = ""
		dataLines = nil
		return err
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return dispatch()
}

func (a *App) emitChatStreamEvent(event ChatStreamEvent) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, "chat-stream", event)
}

type parsedStreamChunk struct {
	Thinking     string
	Answer       string
	FinishReason string
}

func parseStreamChunk(eventType string, data string) (*parsedStreamChunk, error) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, err
	}

	chunk := &parsedStreamChunk{
		Thinking:     extractReasoningFromChoiceContainer(raw),
		Answer:       extractContentFromChoiceContainer(raw),
		FinishReason: extractFinishReason(raw),
	}
	if eventType == "thinking" && chunk.Thinking == "" {
		chunk.Thinking = chunk.Answer
		chunk.Answer = ""
	}
	return chunk, nil
}

func parseStreamError(data string) error {
	var payload struct {
		Error string `json:"error"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return fmt.Errorf("gateway stream failed: %s", strings.TrimSpace(data))
	}
	message := strings.TrimSpace(payload.Error)
	if message == "" {
		message = "backend stream failed"
	}
	if model := strings.TrimSpace(payload.Model); model != "" {
		return fmt.Errorf("%s: %s", model, message)
	}
	return fmt.Errorf("%s", message)
}

func (a *App) captureGatewayStream(reader io.ReadCloser) {
	buffer := make([]byte, 4096)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			a.mu.Lock()
			a.appendGatewayLog(strings.TrimSpace(string(buffer[:n])))
			a.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (a *App) captureFastStream(reader io.ReadCloser) {
	a.captureModelStream(reader, a.appendFastLog)
}

func (a *App) captureWorkStream(reader io.ReadCloser) {
	a.captureModelStream(reader, a.appendWorkLog)
}

func (a *App) captureCodeStream(reader io.ReadCloser) {
	a.captureModelStream(reader, a.appendCodeLog)
}

func (a *App) captureEmbeddingStream(reader io.ReadCloser) {
	a.captureModelStream(reader, a.appendEmbeddingLog)
}

func (a *App) captureModelStream(reader io.ReadCloser, appendFn func(string)) {
	buffer := make([]byte, 4096)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			a.mu.Lock()
			appendFn(strings.TrimSpace(string(buffer[:n])))
			a.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (a *App) waitFastProcess(cmd *exec.Cmd) {
	a.waitModelProcess(cmd, &a.fastCmd, &a.fastRunning, a.appendFastLog, "fast")
}

func (a *App) waitWorkProcess(cmd *exec.Cmd) {
	a.waitModelProcess(cmd, &a.workCmd, &a.workRunning, a.appendWorkLog, "work")
}

func (a *App) waitCodeProcess(cmd *exec.Cmd) {
	a.waitModelProcess(cmd, &a.codeCmd, &a.codeRunning, a.appendCodeLog, "code")
}

func (a *App) captureWatchStream(reader io.ReadCloser) {
	buffer := make([]byte, 4096)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			a.mu.Lock()
			a.appendWatchLog(strings.TrimSpace(string(buffer[:n])))
			a.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (a *App) waitGatewayProcess(cmd *exec.Cmd) {
	err := cmd.Wait()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gatewayRunning = false
	a.gatewayCmd = nil
	if err != nil {
		a.appendGatewayLog("gateway exited: " + err.Error())
	} else {
		a.appendGatewayLog("gateway exited")
	}
}

func (a *App) waitEmbeddingProcess(cmd *exec.Cmd) {
	a.waitModelProcess(cmd, &a.embeddingCmd, &a.embeddingRunning, a.appendEmbeddingLog, "embedding")
}

func (a *App) waitWatchProcess(cmd *exec.Cmd) {
	err := cmd.Wait()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.watchRunning = false
	a.watchCmd = nil
	if err != nil {
		a.appendWatchLog("watch exited: " + err.Error())
	} else {
		a.appendWatchLog("watch exited")
	}
}

func (a *App) runWorkspaceCommand(command string) (string, error) {
	cmd := exec.Command("/bin/zsh", "-lc", command)
	cmd.Dir = a.workspaceRoot
	output, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func (a *App) getQdrantRuntimeState() (bool, string) {
	pidPath := filepath.Join(a.workspaceRoot, "data", "runtime", "pids", "qdrant.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "service is not running"
		}
		return false, err.Error()
	}

	pidText := strings.TrimSpace(string(data))
	if pidText == "" {
		return false, "service is not running"
	}

	var pid int
	if _, err := fmt.Sscanf(pidText, "%d", &pid); err != nil {
		return false, "invalid qdrant pid file"
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false, err.Error()
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return false, "stale qdrant pid file"
	}

	return true, fmt.Sprintf("service is running via local binary (pid=%d)", pid)
}

func (a *App) startModelProcess(scriptRelativePath string, label string, cmdRef **exec.Cmd, runningRef *bool, appendFn func(string), captureFn func(io.ReadCloser), waitFn func(*exec.Cmd)) (*RuntimeStatus, error) {
	a.modelLifecycleMu.Lock()
	defer a.modelLifecycleMu.Unlock()
	return a.startModelProcessLocked(scriptRelativePath, label, cmdRef, runningRef, appendFn, captureFn, waitFn)
}

func (a *App) startModelProcessLocked(scriptRelativePath string, label string, cmdRef **exec.Cmd, runningRef *bool, appendFn func(string), captureFn func(io.ReadCloser), waitFn func(*exec.Cmd)) (*RuntimeStatus, error) {
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return nil, fmt.Errorf("Desktop is shutting down")
	}
	if *runningRef {
		a.mu.Unlock()
		return a.GetRuntimeStatus(), nil
	}

	scriptPath := filepath.Join(a.workspaceRoot, scriptRelativePath)
	if _, err := os.Stat(scriptPath); err != nil {
		appendFn(fmt.Sprintf("missing %s", scriptRelativePath))
		a.mu.Unlock()
		return a.GetRuntimeStatus(), fmt.Errorf("missing %s runtime script at %s", label, scriptPath)
	}

	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = a.workspaceRoot
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		appendFn(fmt.Sprintf("failed to start %s: %s", label, err.Error()))
		a.mu.Unlock()
		return nil, err
	}

	*cmdRef = cmd
	*runningRef = true
	appendFn(label + " started")
	a.mu.Unlock()

	go captureFn(stdout)
	go captureFn(stderr)
	go waitFn(cmd)

	time.Sleep(300 * time.Millisecond)
	return a.GetRuntimeStatus(), nil
}

func (a *App) stopModelProcess(label string, cmdRef **exec.Cmd, runningRef *bool, appendFn func(string)) (*RuntimeStatus, error) {
	a.modelLifecycleMu.Lock()
	defer a.modelLifecycleMu.Unlock()
	return a.stopModelProcessLocked(label, cmdRef, runningRef, appendFn)
}

func (a *App) stopModelProcessLocked(label string, cmdRef **exec.Cmd, runningRef *bool, appendFn func(string)) (*RuntimeStatus, error) {
	a.mu.Lock()
	cmd := *cmdRef
	if cmd == nil || cmd.Process == nil || !*runningRef {
		*runningRef = false
		*cmdRef = nil
		a.mu.Unlock()
		return a.GetRuntimeStatus(), nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		stopped := *cmdRef != cmd || !*runningRef
		a.mu.Unlock()
		if stopped {
			return a.GetRuntimeStatus(), nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("%s did not exit; model selection was not changed", label)
}

func (a *App) waitModelProcess(cmd *exec.Cmd, cmdRef **exec.Cmd, runningRef *bool, appendFn func(string), label string) {
	err := cmd.Wait()
	a.mu.Lock()
	defer a.mu.Unlock()
	if *cmdRef != cmd {
		return // An older process must not clear a newer process handle．
	}
	*runningRef = false
	*cmdRef = nil
	if err != nil {
		appendFn(label + " exited: " + err.Error())
	} else {
		appendFn(label + " exited")
	}
}

func (a *App) appendGatewayLog(line string) {
	a.gatewayLogs = appendBounded(a.gatewayLogs, line)
}

func (a *App) appendFastLog(line string) {
	a.fastLogs = appendBounded(a.fastLogs, line)
}

func (a *App) appendWorkLog(line string) {
	a.workLogs = appendBounded(a.workLogs, line)
}

func (a *App) appendCodeLog(line string) {
	a.codeLogs = appendBounded(a.codeLogs, line)
}

func (a *App) appendEmbeddingLog(line string) {
	a.embeddingLogs = appendBounded(a.embeddingLogs, line)
}

func (a *App) appendQdrantLog(line string) {
	a.qdrantLogs = appendBounded(a.qdrantLogs, line)
}

func (a *App) appendWatchLog(line string) {
	a.watchLogs = appendBounded(a.watchLogs, line)
}

func (a *App) readLocalConfigFile(name string) LocalConfigFile {
	path := filepath.Join(a.workspaceRoot, "configs", name)
	content := ""
	if data, err := os.ReadFile(path); err == nil {
		content = string(data)
	}
	return LocalConfigFile{
		Name:    name,
		Path:    path,
		Exists:  fileExists(path),
		Content: content,
	}
}

func (a *App) readExampleConfigFile(name string) LocalConfigFile {
	exampleName := name + ".example"
	path := filepath.Join(a.workspaceRoot, "configs", exampleName)
	content := ""
	if data, err := os.ReadFile(path); err == nil {
		content = string(data)
	}
	return LocalConfigFile{
		Name:    exampleName,
		Path:    path,
		Exists:  fileExists(path),
		Content: content,
	}
}

func (a *App) presetFilePath() string {
	return filepath.Join(a.workspaceRoot, "data", "cache", "ui_presets.json")
}

func (a *App) savedRequestsFilePath() string {
	return filepath.Join(a.workspaceRoot, "data", "cache", "ui_saved_requests.json")
}

func (a *App) executionHistoryFilePath() string {
	return filepath.Join(a.workspaceRoot, "data", "cache", "ui_execution_history.json")
}

func (a *App) batchWorkflowStateFilePath() string {
	return filepath.Join(a.workspaceRoot, "data", "cache", "ui_batch_workflow_state.json")
}

func (a *App) batchPresetSelectionFilePath() string {
	return filepath.Join(a.workspaceRoot, "data", "cache", "ui_batch_preset_selection.json")
}

func (a *App) regressionWatchSettingsFilePath() string {
	return filepath.Join(a.workspaceRoot, "data", "cache", "ui_regression_watch_settings.json")
}

func (a *App) regressionWatchProfilesFilePath() string {
	return filepath.Join(a.workspaceRoot, "data", "cache", "ui_regression_watch_profiles.json")
}

func (a *App) runtimeConfigSummary() (RuntimeConfigSummary, []string, []string, []string) {
	modelsFile, ragFile, err := a.loadRuntimeConfigFiles()
	if err != nil {
		return RuntimeConfigSummary{}, []string{"gateway"}, nil, []string{err.Error()}
	}

	summary := RuntimeConfigSummary{
		EmbeddingProvider: fallbackString(modelsFile.Models[ragFile.Rag.EmbeddingAlias].Provider, ragFile.Rag.EmbeddingProvider),
		EmbeddingAlias:    ragFile.Rag.EmbeddingAlias,
		EmbeddingModel:    modelsFile.Models[ragFile.Rag.EmbeddingAlias].Model,
		RerankerProvider:  fallbackString(modelsFile.Models[ragFile.Rag.RerankerAlias].Provider, ragFile.Rag.RerankerProvider),
		RerankerAlias:     ragFile.Rag.RerankerAlias,
		RerankerModel:     modelsFile.Models[ragFile.Rag.RerankerAlias].Model,
		VectorDBProvider:  ragFile.VectorDB.Provider,
		VectorDBURL:       ragFile.VectorDB.URL,
		VectorDBStorePath: ragFile.VectorDB.StorePath,
	}

	requiredSet := map[string]struct{}{
		"gateway": {},
	}
	optionalSet := map[string]struct{}{
		"watch": {},
	}
	var warnings []string

	switch summary.EmbeddingProvider {
	case "", "local", "local_hash":
		optionalSet["embedding"] = struct{}{}
	case "llama_cpp", "openai_compatible":
		requiredSet["embedding"] = struct{}{}
	default:
		warnings = append(warnings, "unknown embedding provider: "+summary.EmbeddingProvider)
	}

	switch summary.RerankerProvider {
	case "", "local", "local_overlap":
	case "llama_cpp", "openai_compatible":
		requiredSet["reranker_endpoint"] = struct{}{}
	default:
		warnings = append(warnings, "unknown reranker provider: "+summary.RerankerProvider)
	}

	switch summary.VectorDBProvider {
	case "", "local_json":
		optionalSet["qdrant"] = struct{}{}
	case "qdrant":
		requiredSet["qdrant"] = struct{}{}
	default:
		warnings = append(warnings, "unknown vector db provider: "+summary.VectorDBProvider)
	}

	if summary.EmbeddingAlias != "" && summary.EmbeddingModel == "" {
		warnings = append(warnings, "embedding alias is configured but model entry is missing: "+summary.EmbeddingAlias)
	}
	if summary.RerankerAlias != "" && summary.RerankerModel == "" {
		warnings = append(warnings, "reranker alias is configured but model entry is missing: "+summary.RerankerAlias)
	}

	return summary, sortedKeys(requiredSet), sortedKeys(optionalSet), warnings
}

func (a *App) loadRuntimeConfigFiles() (runtimeModelsFile, runtimeRagFile, error) {
	modelsPath := filepath.Join(a.workspaceRoot, "configs", "models.yaml")
	modelsPayload, err := loadMergedYAML(modelsPath, filepath.Join(a.workspaceRoot, "configs", "models.local.yaml"))
	if err != nil {
		return runtimeModelsFile{}, runtimeRagFile{}, fmt.Errorf("failed to load models config: %w", err)
	}

	ragPath := filepath.Join(a.workspaceRoot, "configs", "rag.yaml")
	ragPayload, err := loadMergedYAML(ragPath, filepath.Join(a.workspaceRoot, "configs", "rag.local.yaml"))
	if err != nil {
		return runtimeModelsFile{}, runtimeRagFile{}, fmt.Errorf("failed to load rag config: %w", err)
	}

	var modelsFile runtimeModelsFile
	if err := yaml.Unmarshal(modelsPayload, &modelsFile); err != nil {
		return runtimeModelsFile{}, runtimeRagFile{}, fmt.Errorf("failed to parse models config: %w", err)
	}
	var ragFile runtimeRagFile
	if err := yaml.Unmarshal(ragPayload, &ragFile); err != nil {
		return runtimeModelsFile{}, runtimeRagFile{}, fmt.Errorf("failed to parse rag config: %w", err)
	}
	return modelsFile, ragFile, nil
}

func loadMergedYAML(basePath string, localPath string) ([]byte, error) {
	baseData, err := os.ReadFile(basePath)
	if err != nil {
		return nil, err
	}
	var basePayload map[string]any
	if err := yaml.Unmarshal(baseData, &basePayload); err != nil {
		return nil, err
	}

	if fileExists(localPath) {
		localData, err := os.ReadFile(localPath)
		if err != nil {
			return nil, err
		}
		var localPayload map[string]any
		if err := yaml.Unmarshal(localData, &localPayload); err != nil {
			return nil, err
		}
		basePayload = mergeMaps(basePayload, localPayload)
	}

	return yaml.Marshal(basePayload)
}

func mergeMaps(base map[string]any, override map[string]any) map[string]any {
	merged := map[string]any{}
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range override {
		if baseMap, ok := merged[key].(map[string]any); ok {
			if overrideMap, ok := value.(map[string]any); ok {
				merged[key] = mergeMaps(baseMap, overrideMap)
				continue
			}
		}
		merged[key] = value
	}
	return merged
}

func sortedKeys(items map[string]struct{}) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func fallbackString(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func defaultChatMaxTokens(mode string) int {
	switch strings.TrimSpace(mode) {
	case "fast":
		return 1024
	case "work":
		return 4096
	case "rag":
		return 2048
	case "code":
		return 3072
	default:
		return 2048
	}
}

func splitLines(raw string) []string {
	parts := strings.Split(raw, "\n")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

func (a *App) resolveWorkspacePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed)
	}
	return filepath.Join(a.workspaceRoot, trimmed)
}

func (a *App) readProjectPresets() ([]ProjectPreset, error) {
	path := a.presetFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []ProjectPreset{}, nil
		}
		return nil, err
	}
	var presets []ProjectPreset
	if len(data) == 0 {
		return []ProjectPreset{}, nil
	}
	if err := json.Unmarshal(data, &presets); err != nil {
		return nil, err
	}
	return presets, nil
}

func (a *App) writeProjectPresets(presets []ProjectPreset) error {
	path := a.presetFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(presets, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (a *App) readSavedRequests() ([]SavedRequest, error) {
	path := a.savedRequestsFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []SavedRequest{}, nil
		}
		return nil, err
	}
	var items []SavedRequest
	if len(data) == 0 {
		return []SavedRequest{}, nil
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (a *App) writeSavedRequests(items []SavedRequest) error {
	path := a.savedRequestsFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (a *App) readExecutionHistory() ([]ExecutionHistoryItem, error) {
	path := a.executionHistoryFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []ExecutionHistoryItem{}, nil
		}
		return nil, err
	}
	var items []ExecutionHistoryItem
	if len(data) == 0 {
		return []ExecutionHistoryItem{}, nil
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (a *App) writeExecutionHistory(items []ExecutionHistoryItem) error {
	path := a.executionHistoryFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (a *App) readBatchWorkflowState() (*BatchWorkflowState, error) {
	path := a.batchWorkflowStateFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var state BatchWorkflowState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return cloneBatchWorkflowState(&state), nil
}

func (a *App) writeBatchWorkflowState(state *BatchWorkflowState) error {
	path := a.batchWorkflowStateFilePath()
	if state == nil {
		return a.clearBatchWorkflowStateFile()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (a *App) clearBatchWorkflowStateFile() error {
	path := a.batchWorkflowStateFilePath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (a *App) readBatchPresetSelection() ([]string, error) {
	path := a.batchPresetSelectionFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return []string{}, nil
	}
	var items []string
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return normalizeBatchPresetSelection(items), nil
}

func (a *App) readRegressionWatchSettings() (RegressionWatchSettings, error) {
	path := a.regressionWatchSettingsFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return normalizeRegressionWatchSettings(RegressionWatchSettings{
				SourceHitDrop:  0,
				IncludePreset:  true,
				IncludeDataset: true,
			}), nil
		}
		return RegressionWatchSettings{}, err
	}
	if len(data) == 0 {
		return normalizeRegressionWatchSettings(RegressionWatchSettings{
			SourceHitDrop:  0,
			IncludePreset:  true,
			IncludeDataset: true,
		}), nil
	}
	var settings RegressionWatchSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return RegressionWatchSettings{}, err
	}
	return normalizeRegressionWatchSettings(settings), nil
}

func (a *App) writeRegressionWatchSettings(settings RegressionWatchSettings) error {
	path := a.regressionWatchSettingsFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(normalizeRegressionWatchSettings(settings), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (a *App) readRegressionWatchProfiles() (map[string]RegressionWatchProfile, error) {
	path := a.regressionWatchProfilesFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]RegressionWatchProfile{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]RegressionWatchProfile{}, nil
	}
	var profiles map[string]RegressionWatchProfile
	if err := json.Unmarshal(data, &profiles); err != nil {
		return nil, err
	}
	return normalizeRegressionWatchProfiles(profiles), nil
}

func (a *App) writeRegressionWatchProfiles(profiles map[string]RegressionWatchProfile) error {
	path := a.regressionWatchProfilesFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(normalizeRegressionWatchProfiles(profiles), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (a *App) writeBatchPresetSelection(presetNames []string) error {
	path := a.batchPresetSelectionFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(normalizeBatchPresetSelection(presetNames), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (a *App) clearBatchPresetSelectionFile() error {
	path := a.batchPresetSelectionFilePath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func appendBounded(lines []string, line string) []string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return lines
	}
	lines = append(lines, trimmed)
	if len(lines) > 80 {
		lines = lines[len(lines)-80:]
	}
	return lines
}

func detectWorkspaceRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	if root := findRuntimeRoot(wd); root != "" {
		return root
	}
	if executable, err := os.Executable(); err == nil {
		if root := findRuntimeRoot(filepath.Dir(executable)); root != "" {
			return root
		}
	}
	if filepath.Base(wd) == "desktop" {
		return filepath.Dir(wd)
	}
	return wd
}

func findRuntimeRoot(start string) string {
	directory, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		if fileExists(filepath.Join(directory, "configs", "models.yaml")) &&
			fileExists(filepath.Join(directory, "scripts", "start_gateway.sh")) {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return ""
		}
		directory = parent
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func cloneBatchWorkflowState(state *BatchWorkflowState) *BatchWorkflowState {
	if state == nil {
		return nil
	}
	cloned := &BatchWorkflowState{
		WorkflowLabel:   state.WorkflowLabel,
		Status:          state.Status,
		Running:         state.Running,
		CancelRequested: state.CancelRequested,
		Results:         make([]BatchWorkflowResultItem, len(state.Results)),
	}
	copy(cloned.Results, state.Results)
	return cloned
}

func normalizeBatchPresetSelection(presetNames []string) []string {
	items := make([]string, 0, len(presetNames))
	seen := map[string]struct{}{}
	for _, name := range presetNames {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		items = append(items, trimmed)
	}
	return items
}

func normalizeRegressionWatchSettings(settings RegressionWatchSettings) RegressionWatchSettings {
	sourceHitDrop := settings.SourceHitDrop
	if sourceHitDrop < 0 {
		sourceHitDrop = 0
	}
	return RegressionWatchSettings{
		SourceHitDrop:  sourceHitDrop,
		IncludePreset:  settings.IncludePreset,
		IncludeDataset: settings.IncludeDataset,
	}
}

func normalizeRegressionWatchProfiles(profiles map[string]RegressionWatchProfile) map[string]RegressionWatchProfile {
	if len(profiles) == 0 {
		return map[string]RegressionWatchProfile{}
	}
	normalized := make(map[string]RegressionWatchProfile, len(profiles))
	for key, profile := range profiles {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		label := strings.TrimSpace(profile.Label)
		if label == "" {
			label = trimmedKey
		}
		sourceHitDrop := profile.SourceHitDrop
		if sourceHitDrop < 0 {
			sourceHitDrop = 0
		}
		normalized[trimmedKey] = RegressionWatchProfile{
			Label:          label,
			SourceHitDrop:  sourceHitDrop,
			IncludePreset:  profile.IncludePreset,
			IncludeDataset: profile.IncludeDataset,
			Builtin:        false,
		}
	}
	return normalized
}

func cloneRegressionWatchProfiles(profiles map[string]RegressionWatchProfile) map[string]RegressionWatchProfile {
	if len(profiles) == 0 {
		return map[string]RegressionWatchProfile{}
	}
	cloned := make(map[string]RegressionWatchProfile, len(profiles))
	for key, profile := range profiles {
		cloned[key] = profile
	}
	return cloned
}

func batchPresetSelectionFromState(state *BatchWorkflowState) []string {
	if state == nil {
		return []string{}
	}
	items := make([]string, 0, len(state.Results))
	for _, result := range state.Results {
		items = append(items, result.PresetName)
	}
	return normalizeBatchPresetSelection(items)
}

func sanitizeFileName(value string) string {
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", "-",
		"*", "-",
		"?", "-",
		"\"", "-",
		"<", "-",
		">", "-",
		"|", "-",
		" ", "-",
	)
	return strings.Trim(replacer.Replace(value), "-.")
}

func extractChatAnswer(raw map[string]any) string {
	return extractContentFromChoiceContainer(raw)
}

func extractChatReasoning(raw map[string]any) string {
	return extractReasoningFromChoiceContainer(raw)
}

func extractChatSources(raw map[string]any) []SearchItem {
	var sources []SearchItem
	payload, ok := raw["sources"]
	if !ok {
		return []SearchItem{}
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return []SearchItem{}
	}
	if err := json.Unmarshal(encoded, &sources); err != nil {
		return []SearchItem{}
	}
	return sources
}

func extractWebSearchStatus(raw map[string]any) *WebSearchStatus {
	payload, ok := raw["web_search_status"]
	if !ok {
		return nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	var status WebSearchStatus
	if err := json.Unmarshal(encoded, &status); err != nil {
		return nil
	}
	return &status
}

func extractFinishReason(raw map[string]any) string {
	choices, ok := raw["choices"].([]any)
	if !ok || len(choices) == 0 {
		return ""
	}

	firstChoice, ok := choices[0].(map[string]any)
	if !ok {
		return ""
	}

	if reason, ok := firstChoice["finish_reason"].(string); ok {
		return strings.TrimSpace(reason)
	}

	return ""
}

func extractContentFromChoiceContainer(raw map[string]any) string {
	choices, ok := raw["choices"].([]any)
	if !ok || len(choices) == 0 {
		return ""
	}

	firstChoice, ok := choices[0].(map[string]any)
	if !ok {
		return ""
	}

	for _, key := range []string{"message", "delta"} {
		container, ok := firstChoice[key].(map[string]any)
		if !ok {
			continue
		}
		for _, field := range []string{"content", "text", "output_text"} {
			if text := stringifyContentValue(container[field]); text != "" {
				return text
			}
		}
	}

	if text := stringifyContentValue(firstChoice["text"]); text != "" {
		return text
	}

	return ""
}

func extractReasoningFromChoiceContainer(raw map[string]any) string {
	choices, ok := raw["choices"].([]any)
	if !ok || len(choices) == 0 {
		return ""
	}

	firstChoice, ok := choices[0].(map[string]any)
	if !ok {
		return ""
	}

	for _, key := range []string{"message", "delta"} {
		container, ok := firstChoice[key].(map[string]any)
		if !ok {
			continue
		}
		for _, reasoningKey := range []string{"reasoning_content", "reasoning", "thinking"} {
			if value, ok := container[reasoningKey].(string); ok {
				return value
			}
		}
	}

	return ""
}

func stringifyContentValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		var builder strings.Builder
		for _, item := range typed {
			switch contentPart := item.(type) {
			case string:
				builder.WriteString(contentPart)
			case map[string]any:
				for _, key := range []string{"text", "content", "output_text"} {
					if nested, ok := contentPart[key]; ok {
						builder.WriteString(stringifyContentValue(nested))
					}
				}
			}
		}
		return builder.String()
	case map[string]any:
		for _, key := range []string{"text", "content", "output_text"} {
			if nested, ok := typed[key]; ok {
				if text := stringifyContentValue(nested); text != "" {
					return text
				}
			}
		}
	}
	return ""
}
