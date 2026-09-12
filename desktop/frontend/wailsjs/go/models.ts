export namespace main {

	export class ASRMetadata {
	    provider?: string;
	    model_revision?: string;
	    revision_count: number;
	    character_count: number;
	    first_audio_ms?: number;
	    first_partial_ms?: number;
	    first_stable_ms?: number;
	    final_ms?: number;
	    finalization_ms?: number;

	    static createFrom(source: any = {}) {
	        return new ASRMetadata(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.model_revision = source["model_revision"];
	        this.revision_count = source["revision_count"];
	        this.character_count = source["character_count"];
	        this.first_audio_ms = source["first_audio_ms"];
	        this.first_partial_ms = source["first_partial_ms"];
	        this.first_stable_ms = source["first_stable_ms"];
	        this.final_ms = source["final_ms"];
	        this.finalization_ms = source["finalization_ms"];
	    }
	}
	export class ASRSessionRequest {
	    operation_id: string;
	    session_id: string;
	    turn_id: string;
	    segment_id: string;
	    sample_rate: number;

	    static createFrom(source: any = {}) {
	        return new ASRSessionRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.operation_id = source["operation_id"];
	        this.session_id = source["session_id"];
	        this.turn_id = source["turn_id"];
	        this.segment_id = source["segment_id"];
	        this.sample_rate = source["sample_rate"];
	    }
	}
	export class ApplyLocalModelRequest {
	    role: string;
	    model_id: string;
	    adapter_id: string;

	    static createFrom(source: any = {}) {
	        return new ApplyLocalModelRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.model_id = source["model_id"];
	        this.adapter_id = source["adapter_id"];
	    }
	}
	export class BatchPresetWorkflowRequest {
	    preset_names: string[];

	    static createFrom(source: any = {}) {
	        return new BatchPresetWorkflowRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.preset_names = source["preset_names"];
	    }
	}
	export class BatchWorkflowResultItem {
	    preset_name: string;
	    status: string;
	    detail: string;

	    static createFrom(source: any = {}) {
	        return new BatchWorkflowResultItem(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.preset_name = source["preset_name"];
	        this.status = source["status"];
	        this.detail = source["detail"];
	    }
	}
	export class BatchWorkflowState {
	    workflow_label: string;
	    status: string;
	    running: boolean;
	    cancel_requested: boolean;
	    results: BatchWorkflowResultItem[];

	    static createFrom(source: any = {}) {
	        return new BatchWorkflowState(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workflow_label = source["workflow_label"];
	        this.status = source["status"];
	        this.running = source["running"];
	        this.cancel_requested = source["cancel_requested"];
	        this.results = this.convertValues(source["results"], BatchWorkflowResultItem);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class BlindInteractionComparison {
	    comparison_id: string;
	    source_operation_id: string;
	    candidate_a: string;
	    candidate_b: string;

	    static createFrom(source: any = {}) {
	        return new BlindInteractionComparison(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.comparison_id = source["comparison_id"];
	        this.source_operation_id = source["source_operation_id"];
	        this.candidate_a = source["candidate_a"];
	        this.candidate_b = source["candidate_b"];
	    }
	}
	export class ChatRequest {
	    messages?: GatewayMessage[];
	    session_id?: string;
	    session_mode?: string;
	    model_id?: string;
	    provider_id?: string;
	    configuration_id?: string;
	    mode: string;
	    prompt: string;
	    project?: string;
	    source_path?: string;
	    source_scope?: string;
	    top_k?: number;
	    tags?: string[];
	    temperature: number;
	    max_tokens: number;
	    request_id?: string;
	    stream?: boolean;
	    web_search?: boolean;
	    web_search_plan_id?: string;

	    static createFrom(source: any = {}) {
	        return new ChatRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.messages = this.convertValues(source["messages"], GatewayMessage);
	        this.session_id = source["session_id"];
	        this.session_mode = source["session_mode"];
	        this.model_id = source["model_id"];
	        this.provider_id = source["provider_id"];
	        this.configuration_id = source["configuration_id"];
	        this.mode = source["mode"];
	        this.prompt = source["prompt"];
	        this.project = source["project"];
	        this.source_path = source["source_path"];
	        this.source_scope = source["source_scope"];
	        this.top_k = source["top_k"];
	        this.tags = source["tags"];
	        this.temperature = source["temperature"];
	        this.max_tokens = source["max_tokens"];
	        this.request_id = source["request_id"];
	        this.stream = source["stream"];
	        this.web_search = source["web_search"];
	        this.web_search_plan_id = source["web_search_plan_id"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ChatResponse {
	    generation?: GenerationMetadata;
	    answer: string;
	    thinking?: string;
	    sources?: SearchItem[];
	    finish_reason?: string;
	    raw: any;
	    web_search_status?: WebSearchStatus;
	    karte_context_status?: KarteContextStatus;

	    static createFrom(source: any = {}) {
	        return new ChatResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.generation = this.convertValues(source["generation"], GenerationMetadata);
	        this.answer = source["answer"];
	        this.thinking = source["thinking"];
	        this.sources = this.convertValues(source["sources"], SearchItem);
	        this.finish_reason = source["finish_reason"];
	        this.raw = source["raw"];
	        this.web_search_status = this.convertValues(source["web_search_status"], WebSearchStatus);
	        this.karte_context_status = this.convertValues(source["karte_context_status"], KarteContextStatus);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class EmbeddingRequest {
	    model: string;
	    input: string;

	    static createFrom(source: any = {}) {
	        return new EmbeddingRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.input = source["input"];
	    }
	}
	export class EvalCaseItem {
	    id: string;
	    query: string;
	    matched_sources: string[];
	    source_hit: boolean;
	    keyword_hit?: boolean;
	    answer: string;
	    top_source: string;
	    latency_ms?: number;
	    prompt_tokens?: number;
	    completion_tokens?: number;
	    total_tokens?: number;

	    static createFrom(source: any = {}) {
	        return new EvalCaseItem(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.query = source["query"];
	        this.matched_sources = source["matched_sources"];
	        this.source_hit = source["source_hit"];
	        this.keyword_hit = source["keyword_hit"];
	        this.answer = source["answer"];
	        this.top_source = source["top_source"];
	        this.latency_ms = source["latency_ms"];
	        this.prompt_tokens = source["prompt_tokens"];
	        this.completion_tokens = source["completion_tokens"];
	        this.total_tokens = source["total_tokens"];
	    }
	}
	export class EvalRequest {
	    dataset_path: string;
	    project?: string;
	    source_path?: string;
	    top_k: number;
	    with_answer: boolean;

	    static createFrom(source: any = {}) {
	        return new EvalRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dataset_path = source["dataset_path"];
	        this.project = source["project"];
	        this.source_path = source["source_path"];
	        this.top_k = source["top_k"];
	        this.with_answer = source["with_answer"];
	    }
	}
	export class EvalResponse {
	    dataset_path: string;
	    total_cases: number;
	    source_hit_rate: number;
	    keyword_hit_rate?: number;
	    average_latency_ms?: number;
	    total_prompt_tokens?: number;
	    total_completion_tokens?: number;
	    total_tokens?: number;
	    results: EvalCaseItem[];

	    static createFrom(source: any = {}) {
	        return new EvalResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dataset_path = source["dataset_path"];
	        this.total_cases = source["total_cases"];
	        this.source_hit_rate = source["source_hit_rate"];
	        this.keyword_hit_rate = source["keyword_hit_rate"];
	        this.average_latency_ms = source["average_latency_ms"];
	        this.total_prompt_tokens = source["total_prompt_tokens"];
	        this.total_completion_tokens = source["total_completion_tokens"];
	        this.total_tokens = source["total_tokens"];
	        this.results = this.convertValues(source["results"], EvalCaseItem);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ExecutionHistoryItem {
	    id: string;
	    timestamp: string;
	    kind: string;
	    title: string;
	    status: string;
	    summary: string;
	    detail?: string;
	    payload?: string;

	    static createFrom(source: any = {}) {
	        return new ExecutionHistoryItem(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.timestamp = source["timestamp"];
	        this.kind = source["kind"];
	        this.title = source["title"];
	        this.status = source["status"];
	        this.summary = source["summary"];
	        this.detail = source["detail"];
	        this.payload = source["payload"];
	    }
	}
	export class ExportResultRequest {
	    kind: string;
	    title: string;
	    content: string;
	    file_stem?: string;

	    static createFrom(source: any = {}) {
	        return new ExportResultRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.title = source["title"];
	        this.content = source["content"];
	        this.file_stem = source["file_stem"];
	    }
	}
	export class ExportResultResponse {
	    path: string;

	    static createFrom(source: any = {}) {
	        return new ExportResultResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	    }
	}
	export class ExportedFileContent {
	    name: string;
	    path: string;
	    content: string;

	    static createFrom(source: any = {}) {
	        return new ExportedFileContent(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.content = source["content"];
	    }
	}
	export class ExportedFileItem {
	    name: string;
	    path: string;
	    mod_time: string;

	    static createFrom(source: any = {}) {
	        return new ExportedFileItem(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.mod_time = source["mod_time"];
	    }
	}
	export class ExportedFileRequest {
	    path: string;

	    static createFrom(source: any = {}) {
	        return new ExportedFileRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	    }
	}
	export class FillerAudio {
	    kind: string;
	    audio_base64: string;
	    duration_ms: number;

	    static createFrom(source: any = {}) {
	        return new FillerAudio(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.audio_base64 = source["audio_base64"];
	        this.duration_ms = source["duration_ms"];
	    }
	}
	export class FillerSetup {
	    enabled: boolean;
	    status: string;
	    samples: FillerTiming[];
	    assets: FillerAudio[];

	    static createFrom(source: any = {}) {
	        return new FillerSetup(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.status = source["status"];
	        this.samples = this.convertValues(source["samples"], FillerTiming);
	        this.assets = this.convertValues(source["assets"], FillerAudio);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FillerTiming {
	    llm_request_ms: number;
	    llm_first_ms: number;
	    tts_request_ms: number;
	    tts_chunk_ms: number;
	    answer_ready_ms: number;
	    llm_ttft_ms: number;
	    tts_latency_ms: number;

	    static createFrom(source: any = {}) {
	        return new FillerTiming(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.llm_request_ms = source["llm_request_ms"];
	        this.llm_first_ms = source["llm_first_ms"];
	        this.tts_request_ms = source["tts_request_ms"];
	        this.tts_chunk_ms = source["tts_chunk_ms"];
	        this.answer_ready_ms = source["answer_ready_ms"];
	        this.llm_ttft_ms = source["llm_ttft_ms"];
	        this.tts_latency_ms = source["tts_latency_ms"];
	    }
	}
	export class FillerTrace {
	    kind: string;
	    latency_ms: number;

	    static createFrom(source: any = {}) {
	        return new FillerTrace(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.latency_ms = source["latency_ms"];
	    }
	}
	export class GatewayMessage {
	    role: string;
	    content: string;

	    static createFrom(source: any = {}) {
	        return new GatewayMessage(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	    }
	}
	export class GenerationLimits {
	    segment_tokens: number;
	    max_segments: number;
	    max_total_tokens: number;
	    soft_target_percent: number;

	    static createFrom(source: any = {}) {
	        return new GenerationLimits(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.segment_tokens = source["segment_tokens"];
	        this.max_segments = source["max_segments"];
	        this.max_total_tokens = source["max_total_tokens"];
	        this.soft_target_percent = source["soft_target_percent"];
	    }
	}
	export class GenerationMetadata {
	    schema_version: number;
	    finish_reason: string;
	    provider_finish_reason: string;
	    output_budget: number;
	    total_token_budget: number;
	    max_segments: number;
	    soft_target_percent: number;
	    completion_tokens: number;
	    completion_tokens_observed: boolean;
	    reasoning_tokens?: number;
	    reasoning_token_source: string;
	    segment_count: number;
	    continuation_count: number;
	    first_raw_delta_at?: string;
	    first_visible_content_at?: string;
	    terminal_sse_at?: string;
	    terminal_sse: boolean;
	    done_received: boolean;
	    complete: boolean;

	    static createFrom(source: any = {}) {
	        return new GenerationMetadata(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.schema_version = source["schema_version"];
	        this.finish_reason = source["finish_reason"];
	        this.provider_finish_reason = source["provider_finish_reason"];
	        this.output_budget = source["output_budget"];
	        this.total_token_budget = source["total_token_budget"];
	        this.max_segments = source["max_segments"];
	        this.soft_target_percent = source["soft_target_percent"];
	        this.completion_tokens = source["completion_tokens"];
	        this.completion_tokens_observed = source["completion_tokens_observed"];
	        this.reasoning_tokens = source["reasoning_tokens"];
	        this.reasoning_token_source = source["reasoning_token_source"];
	        this.segment_count = source["segment_count"];
	        this.continuation_count = source["continuation_count"];
	        this.first_raw_delta_at = source["first_raw_delta_at"];
	        this.first_visible_content_at = source["first_visible_content_at"];
	        this.terminal_sse_at = source["terminal_sse_at"];
	        this.terminal_sse = source["terminal_sse"];
	        this.done_received = source["done_received"];
	        this.complete = source["complete"];
	    }
	}
	export class HealthResponse {
	    status: string;
	    service: string;
	    configured_models: string[];
	    web_search_enabled: boolean;
	    karte_enabled: boolean;
	    karte_context_enabled: boolean;

	    static createFrom(source: any = {}) {
	        return new HealthResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.service = source["service"];
	        this.configured_models = source["configured_models"];
	        this.web_search_enabled = source["web_search_enabled"];
	        this.karte_enabled = source["karte_enabled"];
	        this.karte_context_enabled = source["karte_context_enabled"];
	    }
	}
	export class ImportLocalModelRequest {
	    id: string;
	    path: string;
	    base_model_id: string;
	    profile_id: string;
	    context_size: number;

	    static createFrom(source: any = {}) {
	        return new ImportLocalModelRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.path = source["path"];
	        this.base_model_id = source["base_model_id"];
	        this.profile_id = source["profile_id"];
	        this.context_size = source["context_size"];
	    }
	}
	export class IndexBrowseRequest {
	    project?: string;
	    source_query?: string;
	    limit: number;

	    static createFrom(source: any = {}) {
	        return new IndexBrowseRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.project = source["project"];
	        this.source_query = source["source_query"];
	        this.limit = source["limit"];
	    }
	}
	export class IndexSourceRequest {
	    project?: string;
	    source_path: string;
	    limit: number;

	    static createFrom(source: any = {}) {
	        return new IndexSourceRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.project = source["project"];
	        this.source_path = source["source_path"];
	        this.limit = source["limit"];
	    }
	}
	export class IngestRequest {
	    paths: string[];
	    project?: string;
	    recursive: boolean;
	    tags?: string[];

	    static createFrom(source: any = {}) {
	        return new IngestRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.paths = source["paths"];
	        this.project = source["project"];
	        this.recursive = source["recursive"];
	        this.tags = source["tags"];
	    }
	}
	export class InteractionCandidateIdentity {
	    operation_id: string;
	    provider_id: string;
	    model_id: string;
	    configuration_id: string;
	    latency_ms: number;
	    generation?: GenerationMetadata;

	    static createFrom(source: any = {}) {
	        return new InteractionCandidateIdentity(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.operation_id = source["operation_id"];
	        this.provider_id = source["provider_id"];
	        this.model_id = source["model_id"];
	        this.configuration_id = source["configuration_id"];
	        this.latency_ms = source["latency_ms"];
	        this.generation = this.convertValues(source["generation"], GenerationMetadata);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class InteractionComparisonRequest {
	    request_id: string;
	    source_operation_id: string;
	    mode_a: string;
	    mode_b: string;
	    temperature_a: number;
	    temperature_b: number;

	    static createFrom(source: any = {}) {
	        return new InteractionComparisonRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.request_id = source["request_id"];
	        this.source_operation_id = source["source_operation_id"];
	        this.mode_a = source["mode_a"];
	        this.mode_b = source["mode_b"];
	        this.temperature_a = source["temperature_a"];
	        this.temperature_b = source["temperature_b"];
	    }
	}
	export class InteractionEvaluationRecord {
	    schema_version: number;
	    id: string;
	    timestamp: string;
	    source_operation_id: string;
	    trace_id: string;
	    session_id: string;
	    turn_id: string;
	    generation_revision?: number;
	    comparison_id?: string;
	    candidates?: InteractionCandidateIdentity[];
	    choice: string;
	    correction?: string;
	    failure_tags: string[];

	    static createFrom(source: any = {}) {
	        return new InteractionEvaluationRecord(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.schema_version = source["schema_version"];
	        this.id = source["id"];
	        this.timestamp = source["timestamp"];
	        this.source_operation_id = source["source_operation_id"];
	        this.trace_id = source["trace_id"];
	        this.session_id = source["session_id"];
	        this.turn_id = source["turn_id"];
	        this.generation_revision = source["generation_revision"];
	        this.comparison_id = source["comparison_id"];
	        this.candidates = this.convertValues(source["candidates"], InteractionCandidateIdentity);
	        this.choice = source["choice"];
	        this.correction = source["correction"];
	        this.failure_tags = source["failure_tags"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class InteractionEvaluationRequest {
	    comparison_id: string;
	    choice: string;
	    correction: string;
	    failure_tags: string[];

	    static createFrom(source: any = {}) {
	        return new InteractionEvaluationRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.comparison_id = source["comparison_id"];
	        this.choice = source["choice"];
	        this.correction = source["correction"];
	        this.failure_tags = source["failure_tags"];
	    }
	}
	export class InteractionReplayRequest {
	    source_operation_id: string;
	    transcript?: string;

	    static createFrom(source: any = {}) {
	        return new InteractionReplayRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source_operation_id = source["source_operation_id"];
	        this.transcript = source["transcript"];
	    }
	}
	export class InteractionSnapshot {
	    trace_id: string;
	    session_id: string;
	    turn_id: string;
	    operation_id: string;
	    state: string;
	    transcript?: string;
	    response_plan?: ResponsePlan;
	    error_code?: string;
	    generation?: GenerationMetadata;
	    generation_revision: number;
	    last_audio_sequence: number;

	    static createFrom(source: any = {}) {
	        return new InteractionSnapshot(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.trace_id = source["trace_id"];
	        this.session_id = source["session_id"];
	        this.turn_id = source["turn_id"];
	        this.operation_id = source["operation_id"];
	        this.state = source["state"];
	        this.transcript = source["transcript"];
	        this.response_plan = this.convertValues(source["response_plan"], ResponsePlan);
	        this.error_code = source["error_code"];
	        this.generation = this.convertValues(source["generation"], GenerationMetadata);
	        this.generation_revision = source["generation_revision"];
	        this.last_audio_sequence = source["last_audio_sequence"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class InteractionTraceEvent {
	    schema_version: number;
	    event_id: string;
	    trace_id: string;
	    session_id: string;
	    turn_id: string;
	    operation_id: string;
	    name: string;
	    source: string;
	    timestamp: string;
	    monotonic_ms: number;
	    status: string;
	    error_code?: string;
	    provider_id: string;
	    model_id: string;
	    configuration_id: string;
	    generation?: GenerationMetadata;
	    generation_revision?: number;
	    asr?: ASRMetadata;

	    static createFrom(source: any = {}) {
	        return new InteractionTraceEvent(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.schema_version = source["schema_version"];
	        this.event_id = source["event_id"];
	        this.trace_id = source["trace_id"];
	        this.session_id = source["session_id"];
	        this.turn_id = source["turn_id"];
	        this.operation_id = source["operation_id"];
	        this.name = source["name"];
	        this.source = source["source"];
	        this.timestamp = source["timestamp"];
	        this.monotonic_ms = source["monotonic_ms"];
	        this.status = source["status"];
	        this.error_code = source["error_code"];
	        this.provider_id = source["provider_id"];
	        this.model_id = source["model_id"];
	        this.configuration_id = source["configuration_id"];
	        this.generation = this.convertValues(source["generation"], GenerationMetadata);
	        this.generation_revision = source["generation_revision"];
	        this.asr = this.convertValues(source["asr"], ASRMetadata);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class KarteContextStatus {
	    status: string;
	    source_count: number;
	    searched_count: number;
	    read_count: number;
	    read_failed_count: number;

	    static createFrom(source: any = {}) {
	        return new KarteContextStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.source_count = source["source_count"];
	        this.searched_count = source["searched_count"];
	        this.read_count = source["read_count"];
	        this.read_failed_count = source["read_failed_count"];
	    }
	}
	export class KarteConversationContextStatus {
	    status: string;
	    searched_count: number;
	    read_count: number;
	    read_failed_count: number;

	    static createFrom(source: any = {}) {
	        return new KarteConversationContextStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.searched_count = source["searched_count"];
	        this.read_count = source["read_count"];
	        this.read_failed_count = source["read_failed_count"];
	    }
	}
	export class KarteConversationMessage {
	    role: string;
	    content: string;

	    static createFrom(source: any = {}) {
	        return new KarteConversationMessage(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	    }
	}
	export class KarteConversationPlanResponse {
	    candidate_id: string;
	    recommendation: string;
	    publishable: boolean;
	    needs_project: boolean;
	    reasons: string[];
	    summary_title: string;
	    summary_markdown: string;
	    similar_documents: KarteSimilarDocument[];
	    context_status?: KarteConversationContextStatus;
	    plan_sha256: string;
	    proposal: Record<string, any>;

	    static createFrom(source: any = {}) {
	        return new KarteConversationPlanResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.candidate_id = source["candidate_id"];
	        this.recommendation = source["recommendation"];
	        this.publishable = source["publishable"];
	        this.needs_project = source["needs_project"];
	        this.reasons = source["reasons"];
	        this.summary_title = source["summary_title"];
	        this.summary_markdown = source["summary_markdown"];
	        this.similar_documents = this.convertValues(source["similar_documents"], KarteSimilarDocument);
	        this.context_status = this.convertValues(source["context_status"], KarteConversationContextStatus);
	        this.plan_sha256 = source["plan_sha256"];
	        this.proposal = source["proposal"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class KarteConversationPublishResponse {
	    candidate_id: string;
	    state: string;
	    path: string;
	    plan: KarteConversationPlanResponse;

	    static createFrom(source: any = {}) {
	        return new KarteConversationPublishResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.candidate_id = source["candidate_id"];
	        this.state = source["state"];
	        this.path = source["path"];
	        this.plan = this.convertValues(source["plan"], KarteConversationPlanResponse);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class KarteConversationRequest {
	    conversation_id: string;
	    messages: KarteConversationMessage[];
	    occurred_at: string;
	    project?: string;
	    kind?: string;
	    sensitivity: string;
	    tags: string[];
	    resolution: string;
	    intended_doc_id?: string;
	    reviewed_plan_sha256?: string;

	    static createFrom(source: any = {}) {
	        return new KarteConversationRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.conversation_id = source["conversation_id"];
	        this.messages = this.convertValues(source["messages"], KarteConversationMessage);
	        this.occurred_at = source["occurred_at"];
	        this.project = source["project"];
	        this.kind = source["kind"];
	        this.sensitivity = source["sensitivity"];
	        this.tags = source["tags"];
	        this.resolution = source["resolution"];
	        this.intended_doc_id = source["intended_doc_id"];
	        this.reviewed_plan_sha256 = source["reviewed_plan_sha256"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class KarteConversationStatusResponse {
	    candidate_id: string;
	    state: string;
	    receipt?: Record<string, any>;

	    static createFrom(source: any = {}) {
	        return new KarteConversationStatusResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.candidate_id = source["candidate_id"];
	        this.state = source["state"];
	        this.receipt = source["receipt"];
	    }
	}
	export class KarteSimilarDocument {
	    doc_id: string;
	    title: string;
	    relative_path: string;
	    project?: string;
	    kind?: string;
	    similarity: number;

	    static createFrom(source: any = {}) {
	        return new KarteSimilarDocument(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.doc_id = source["doc_id"];
	        this.title = source["title"];
	        this.relative_path = source["relative_path"];
	        this.project = source["project"];
	        this.kind = source["kind"];
	        this.similarity = source["similarity"];
	    }
	}
	export class LocalAdapterArtifact {
	    id: string;
	    base_model_id: string;
	    base_sha256: string;
	    available: boolean;
	    experimental: boolean;

	    static createFrom(source: any = {}) {
	        return new LocalAdapterArtifact(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.base_model_id = source["base_model_id"];
	        this.base_sha256 = source["base_sha256"];
	        this.available = source["available"];
	        this.experimental = source["experimental"];
	    }
	}
	export class LocalConfigFile {
	    name: string;
	    path: string;
	    exists: boolean;
	    content: string;

	    static createFrom(source: any = {}) {
	        return new LocalConfigFile(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.exists = source["exists"];
	        this.content = source["content"];
	    }
	}
	export class LocalConfigNameRequest {
	    name: string;
	    kind?: string;

	    static createFrom(source: any = {}) {
	        return new LocalConfigNameRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.kind = source["kind"];
	    }
	}
	export class LocalModelArtifact {
	    id: string;
	    path: string;
	    sha256: string;
	    size_bytes: number;
	    backend_model: string;
	    quantization: string;
	    context_size: number;
	    profile_id?: string;
	    family?: string;
	    parameter_count_billions?: number;
	    capabilities?: string[];
	    enabled_capabilities?: string[];
	    thinking_mode?: string;
	    native_context_size?: number;
	    maximum_context_size?: number;
	    startup_timeout_seconds?: number;
	    resource_class?: string;
	    resource_fit?: boolean;
	    resource_warning?: string;
	    available: boolean;

	    static createFrom(source: any = {}) {
	        return new LocalModelArtifact(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.path = source["path"];
	        this.sha256 = source["sha256"];
	        this.size_bytes = source["size_bytes"];
	        this.backend_model = source["backend_model"];
	        this.quantization = source["quantization"];
	        this.context_size = source["context_size"];
	        this.profile_id = source["profile_id"];
	        this.family = source["family"];
	        this.parameter_count_billions = source["parameter_count_billions"];
	        this.capabilities = source["capabilities"];
	        this.enabled_capabilities = source["enabled_capabilities"];
	        this.thinking_mode = source["thinking_mode"];
	        this.native_context_size = source["native_context_size"];
	        this.maximum_context_size = source["maximum_context_size"];
	        this.startup_timeout_seconds = source["startup_timeout_seconds"];
	        this.resource_class = source["resource_class"];
	        this.resource_fit = source["resource_fit"];
	        this.resource_warning = source["resource_warning"];
	        this.available = source["available"];
	    }
	}
	export class LocalModelCatalog {
	    models: LocalModelArtifact[];
	    adapters: LocalAdapterArtifact[];
	    profiles: Record<string, RuntimeModelProfile>;
	    selections: Record<string, LocalModelSelection>;
	    revision: string;
	    developer_mode: boolean;
	    karte_auto_submit: boolean;

	    static createFrom(source: any = {}) {
	        return new LocalModelCatalog(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.models = this.convertValues(source["models"], LocalModelArtifact);
	        this.adapters = this.convertValues(source["adapters"], LocalAdapterArtifact);
	        this.profiles = this.convertValues(source["profiles"], RuntimeModelProfile, true);
	        this.selections = this.convertValues(source["selections"], LocalModelSelection, true);
	        this.revision = source["revision"];
	        this.developer_mode = source["developer_mode"];
	        this.karte_auto_submit = source["karte_auto_submit"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class LocalModelSelection {
	    model_id: string;
	    adapter_id?: string;

	    static createFrom(source: any = {}) {
	        return new LocalModelSelection(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model_id = source["model_id"];
	        this.adapter_id = source["adapter_id"];
	    }
	}
	export class ModelItem {
	    id: string;
	    object: string;
	    owned_by: string;
	    backend_model: string;

	    static createFrom(source: any = {}) {
	        return new ModelItem(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.object = source["object"];
	        this.owned_by = source["owned_by"];
	        this.backend_model = source["backend_model"];
	    }
	}
	export class ModelListResponse {
	    object: string;
	    data: ModelItem[];

	    static createFrom(source: any = {}) {
	        return new ModelListResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.object = source["object"];
	        this.data = this.convertValues(source["data"], ModelItem);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PreferenceExportRequest {
	    format: string;
	    output: string;

	    static createFrom(source: any = {}) {
	        return new PreferenceExportRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.format = source["format"];
	        this.output = source["output"];
	    }
	}
	export class PreferenceGenerateRequest {
	    limit?: number;

	    static createFrom(source: any = {}) {
	        return new PreferenceGenerateRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.limit = source["limit"];
	    }
	}
	export class PreferenceGenerationParameters {
	    temperature: number;
	    top_p: number;
	    seed?: number;
	    max_tokens: number;

	    static createFrom(source: any = {}) {
	        return new PreferenceGenerationParameters(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.temperature = source["temperature"];
	        this.top_p = source["top_p"];
	        this.seed = source["seed"];
	        this.max_tokens = source["max_tokens"];
	    }
	}
	export class PreferenceSessionRequest {
	    dataset_path: string;
	    model_role: string;
	    pair_count: number;
	    prefetch: number;
	    comparison_mode: string;
	    adapter_scale?: number;
	    generation_parameters: PreferenceGenerationParameters;

	    static createFrom(source: any = {}) {
	        return new PreferenceSessionRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dataset_path = source["dataset_path"];
	        this.model_role = source["model_role"];
	        this.pair_count = source["pair_count"];
	        this.prefetch = source["prefetch"];
	        this.comparison_mode = source["comparison_mode"];
	        this.adapter_scale = source["adapter_scale"];
	        this.generation_parameters = this.convertValues(source["generation_parameters"], PreferenceGenerationParameters);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PreferenceVoteRequest {
	    selection: string;
	    reason_tags?: string[];
	    note?: string;
	    approved_for_sft: boolean;
	    supersedes_vote_id?: string;

	    static createFrom(source: any = {}) {
	        return new PreferenceVoteRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.selection = source["selection"];
	        this.reason_tags = source["reason_tags"];
	        this.note = source["note"];
	        this.approved_for_sft = source["approved_for_sft"];
	        this.supersedes_vote_id = source["supersedes_vote_id"];
	    }
	}
	export class PresetPathCheck {
	    label: string;
	    path: string;
	    resolved_path: string;
	    kind: string;
	    required: boolean;
	    exists: boolean;
	    detail: string;

	    static createFrom(source: any = {}) {
	        return new PresetPathCheck(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.path = source["path"];
	        this.resolved_path = source["resolved_path"];
	        this.kind = source["kind"];
	        this.required = source["required"];
	        this.exists = source["exists"];
	        this.detail = source["detail"];
	    }
	}
	export class PresetRecoveryActionRequest {
	    preset: ProjectPreset;
	    action_kind: string;
	    service_name: string;
	    step_name: string;
	    source_history_id: string;
	    source_workflow: string;

	    static createFrom(source: any = {}) {
	        return new PresetRecoveryActionRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.preset = this.convertValues(source["preset"], ProjectPreset);
	        this.action_kind = source["action_kind"];
	        this.service_name = source["service_name"];
	        this.step_name = source["step_name"];
	        this.source_history_id = source["source_history_id"];
	        this.source_workflow = source["source_workflow"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PresetServiceCheck {
	    name: string;
	    required: boolean;
	    status: string;
	    detail: string;

	    static createFrom(source: any = {}) {
	        return new PresetServiceCheck(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.required = source["required"];
	        this.status = source["status"];
	        this.detail = source["detail"];
	    }
	}
	export class PresetValidationResponse {
	    preset_name: string;
	    valid: boolean;
	    ready: boolean;
	    warnings: string[];
	    config_warnings: string[];
	    required_services: string[];
	    optional_services: string[];
	    path_checks: PresetPathCheck[];
	    service_checks: PresetServiceCheck[];

	    static createFrom(source: any = {}) {
	        return new PresetValidationResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.preset_name = source["preset_name"];
	        this.valid = source["valid"];
	        this.ready = source["ready"];
	        this.warnings = source["warnings"];
	        this.config_warnings = source["config_warnings"];
	        this.required_services = source["required_services"];
	        this.optional_services = source["optional_services"];
	        this.path_checks = this.convertValues(source["path_checks"], PresetPathCheck);
	        this.service_checks = this.convertValues(source["service_checks"], PresetServiceCheck);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProjectPreset {
	    name: string;
	    runtime_profile: string;
	    watch_paths: string;
	    watch_project: string;
	    watch_interval: number;
	    ingest_paths: string;
	    ingest_project: string;
	    chat_request_name: string;
	    chat_expect_contains: string;
	    ingest_request_name: string;
	    rag_project: string;
	    rag_source_path: string;
	    rag_top_k: number;
	    rag_request_name: string;
	    rag_expect_contains: string;
	    eval_dataset: string;
	    eval_project: string;
	    eval_source_path: string;
	    eval_top_k: number;
	    eval_with_answer: boolean;
	    eval_request_name: string;
	    eval_min_source_hit_rate: number;
	    workflow_run_smoke: boolean;
	    smoke_skip_qdrant: boolean;
	    smoke_skip_embedding: boolean;
	    smoke_skip_reranker: boolean;

	    static createFrom(source: any = {}) {
	        return new ProjectPreset(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.runtime_profile = source["runtime_profile"];
	        this.watch_paths = source["watch_paths"];
	        this.watch_project = source["watch_project"];
	        this.watch_interval = source["watch_interval"];
	        this.ingest_paths = source["ingest_paths"];
	        this.ingest_project = source["ingest_project"];
	        this.chat_request_name = source["chat_request_name"];
	        this.chat_expect_contains = source["chat_expect_contains"];
	        this.ingest_request_name = source["ingest_request_name"];
	        this.rag_project = source["rag_project"];
	        this.rag_source_path = source["rag_source_path"];
	        this.rag_top_k = source["rag_top_k"];
	        this.rag_request_name = source["rag_request_name"];
	        this.rag_expect_contains = source["rag_expect_contains"];
	        this.eval_dataset = source["eval_dataset"];
	        this.eval_project = source["eval_project"];
	        this.eval_source_path = source["eval_source_path"];
	        this.eval_top_k = source["eval_top_k"];
	        this.eval_with_answer = source["eval_with_answer"];
	        this.eval_request_name = source["eval_request_name"];
	        this.eval_min_source_hit_rate = source["eval_min_source_hit_rate"];
	        this.workflow_run_smoke = source["workflow_run_smoke"];
	        this.smoke_skip_qdrant = source["smoke_skip_qdrant"];
	        this.smoke_skip_embedding = source["smoke_skip_embedding"];
	        this.smoke_skip_reranker = source["smoke_skip_reranker"];
	    }
	}
	export class QueryRequest {
	    query: string;
	    project?: string;
	    source_path?: string;
	    tags?: string[];
	    top_k: number;
	    answer: boolean;
	    request_id?: string;
	    stream?: boolean;

	    static createFrom(source: any = {}) {
	        return new QueryRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.query = source["query"];
	        this.project = source["project"];
	        this.source_path = source["source_path"];
	        this.tags = source["tags"];
	        this.top_k = source["top_k"];
	        this.answer = source["answer"];
	        this.request_id = source["request_id"];
	        this.stream = source["stream"];
	    }
	}
	export class QueryResponse {
	    answer: string;
	    thinking?: string;
	    sources: SearchItem[];
	    finish_reason?: string;

	    static createFrom(source: any = {}) {
	        return new QueryResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.answer = source["answer"];
	        this.thinking = source["thinking"];
	        this.sources = this.convertValues(source["sources"], SearchItem);
	        this.finish_reason = source["finish_reason"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RegressionWatchSettings {
	    source_hit_drop: number;
	    include_preset: boolean;
	    include_dataset: boolean;

	    static createFrom(source: any = {}) {
	        return new RegressionWatchSettings(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source_hit_drop = source["source_hit_drop"];
	        this.include_preset = source["include_preset"];
	        this.include_dataset = source["include_dataset"];
	    }
	}
	export class ReloadConfigResponse {
	    status: string;
	    configured_models: string[];

	    static createFrom(source: any = {}) {
	        return new ReloadConfigResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.configured_models = source["configured_models"];
	    }
	}
	export class ResponsePlan {
	    text: string;
	    dialogue_act: string;
	    affect: string;
	    voice_hint: VoiceHint;
	    interruptible: boolean;

	    static createFrom(source: any = {}) {
	        return new ResponsePlan(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.dialogue_act = source["dialogue_act"];
	        this.affect = source["affect"];
	        this.voice_hint = this.convertValues(source["voice_hint"], VoiceHint);
	        this.interruptible = source["interruptible"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RoutePlanRequest {
	    mode: string;
	    prompt: string;

	    static createFrom(source: any = {}) {
	        return new RoutePlanRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.prompt = source["prompt"];
	    }
	}
	export class RoutePlanResponse {
	    mode: string;
	    model_alias: string;
	    provider: string;
	    backend_model: string;
	    base_url: string;
	    max_context: number;

	    static createFrom(source: any = {}) {
	        return new RoutePlanResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.model_alias = source["model_alias"];
	        this.provider = source["provider"];
	        this.backend_model = source["backend_model"];
	        this.base_url = source["base_url"];
	        this.max_context = source["max_context"];
	    }
	}
	export class RuntimeConfigActionRequest {
	    action: string;
	    name: string;
	    content: string;

	    static createFrom(source: any = {}) {
	        return new RuntimeConfigActionRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.action = source["action"];
	        this.name = source["name"];
	        this.content = source["content"];
	    }
	}
	export class RuntimeModelProfile {
	    family: string;
	    parameter_count_billions: number;
	    capabilities: string[];
	    enabled_capabilities: string[];
	    thinking_mode: string;
	    native_context_size: number;
	    maximum_context_size: number;
	    default_context_size: number;
	    startup_timeout_seconds: number;
	    resource_class: string;
	    estimated_minimum_memory_bytes: number;

	    static createFrom(source: any = {}) {
	        return new RuntimeModelProfile(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.family = source["family"];
	        this.parameter_count_billions = source["parameter_count_billions"];
	        this.capabilities = source["capabilities"];
	        this.enabled_capabilities = source["enabled_capabilities"];
	        this.thinking_mode = source["thinking_mode"];
	        this.native_context_size = source["native_context_size"];
	        this.maximum_context_size = source["maximum_context_size"];
	        this.default_context_size = source["default_context_size"];
	        this.startup_timeout_seconds = source["startup_timeout_seconds"];
	        this.resource_class = source["resource_class"];
	        this.estimated_minimum_memory_bytes = source["estimated_minimum_memory_bytes"];
	    }
	}
	export class RuntimeServiceActionRequest {
	    action: string;
	    watch: WatchRequest;

	    static createFrom(source: any = {}) {
	        return new RuntimeServiceActionRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.action = source["action"];
	        this.watch = this.convertValues(source["watch"], WatchRequest);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RuntimeStackActionRequest {
	    action: string;

	    static createFrom(source: any = {}) {
	        return new RuntimeStackActionRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.action = source["action"];
	    }
	}
	export class RuntimeStatus {
	    workspace_root: string;
	    fast_running: boolean;
	    fast_pid: number;
	    fast_logs: string[];
	    work_running: boolean;
	    work_pid: number;
	    work_logs: string[];
	    code_running: boolean;
	    code_pid: number;
	    code_logs: string[];
	    gateway_running: boolean;
	    gateway_pid: number;
	    gateway_logs: string[];
	    embedding_running: boolean;
	    embedding_pid: number;
	    embedding_logs: string[];
	    qdrant_running: boolean;
	    qdrant_detail: string;
	    qdrant_logs: string[];
	    watch_running: boolean;
	    watch_pid: number;
	    watch_logs: string[];
	    models_local_override: boolean;
	    rag_local_override: boolean;
	    models_local_path: string;
	    rag_local_path: string;
	    config_summary: any;
	    required_services: string[];
	    optional_services: string[];
	    warnings: string[];

	    static createFrom(source: any = {}) {
	        return new RuntimeStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workspace_root = source["workspace_root"];
	        this.fast_running = source["fast_running"];
	        this.fast_pid = source["fast_pid"];
	        this.fast_logs = source["fast_logs"];
	        this.work_running = source["work_running"];
	        this.work_pid = source["work_pid"];
	        this.work_logs = source["work_logs"];
	        this.code_running = source["code_running"];
	        this.code_pid = source["code_pid"];
	        this.code_logs = source["code_logs"];
	        this.gateway_running = source["gateway_running"];
	        this.gateway_pid = source["gateway_pid"];
	        this.gateway_logs = source["gateway_logs"];
	        this.embedding_running = source["embedding_running"];
	        this.embedding_pid = source["embedding_pid"];
	        this.embedding_logs = source["embedding_logs"];
	        this.qdrant_running = source["qdrant_running"];
	        this.qdrant_detail = source["qdrant_detail"];
	        this.qdrant_logs = source["qdrant_logs"];
	        this.watch_running = source["watch_running"];
	        this.watch_pid = source["watch_pid"];
	        this.watch_logs = source["watch_logs"];
	        this.models_local_override = source["models_local_override"];
	        this.rag_local_override = source["rag_local_override"];
	        this.models_local_path = source["models_local_path"];
	        this.rag_local_path = source["rag_local_path"];
	        this.config_summary = source["config_summary"];
	        this.required_services = source["required_services"];
	        this.optional_services = source["optional_services"];
	        this.warnings = source["warnings"];
	    }
	}
	export class SaveLocalConfigRequest {
	    name: string;
	    content: string;

	    static createFrom(source: any = {}) {
	        return new SaveLocalConfigRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.content = source["content"];
	    }
	}
	export class SavedRequest {
	    name: string;
	    kind: string;
	    model?: string;
	    input?: string;
	    mode?: string;
	    prompt?: string;
	    query?: string;
	    project?: string;
	    source_query?: string;
	    source_path?: string;
	    limit?: number;
	    top_k?: number;
	    answer?: boolean;
	    paths?: string;
	    recursive?: boolean;
	    dataset_path?: string;
	    with_answer?: boolean;

	    static createFrom(source: any = {}) {
	        return new SavedRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.model = source["model"];
	        this.input = source["input"];
	        this.mode = source["mode"];
	        this.prompt = source["prompt"];
	        this.query = source["query"];
	        this.project = source["project"];
	        this.source_query = source["source_query"];
	        this.source_path = source["source_path"];
	        this.limit = source["limit"];
	        this.top_k = source["top_k"];
	        this.answer = source["answer"];
	        this.paths = source["paths"];
	        this.recursive = source["recursive"];
	        this.dataset_path = source["dataset_path"];
	        this.with_answer = source["with_answer"];
	    }
	}
	export class SearchItem {
	    chunk_id: string;
	    doc_id?: string;
	    source_path: string;
	    relative_path?: string;
	    heading_path: string[];
	    project: string;
	    kind?: string;
	    tags: string[];
	    sensitivity?: string;
	    chunk_text: string;
	    score: number;
	    source_type?: string;
	    source_id?: string;
	    title?: string;
	    url?: string;
	    snippet?: string;
	    trust_level?: string;
	    injection_suspected?: boolean;

	    static createFrom(source: any = {}) {
	        return new SearchItem(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.chunk_id = source["chunk_id"];
	        this.doc_id = source["doc_id"];
	        this.source_path = source["source_path"];
	        this.relative_path = source["relative_path"];
	        this.heading_path = source["heading_path"];
	        this.project = source["project"];
	        this.kind = source["kind"];
	        this.tags = source["tags"];
	        this.sensitivity = source["sensitivity"];
	        this.chunk_text = source["chunk_text"];
	        this.score = source["score"];
	        this.source_type = source["source_type"];
	        this.source_id = source["source_id"];
	        this.title = source["title"];
	        this.url = source["url"];
	        this.snippet = source["snippet"];
	        this.trust_level = source["trust_level"];
	        this.injection_suspected = source["injection_suspected"];
	    }
	}
	export class SearchRequest {
	    query: string;
	    project?: string;
	    source_path?: string;
	    tags?: string[];
	    top_k: number;

	    static createFrom(source: any = {}) {
	        return new SearchRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.query = source["query"];
	        this.project = source["project"];
	        this.source_path = source["source_path"];
	        this.tags = source["tags"];
	        this.top_k = source["top_k"];
	    }
	}
	export class SearchResponse {
	    query: string;
	    results: SearchItem[];

	    static createFrom(source: any = {}) {
	        return new SearchResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.query = source["query"];
	        this.results = this.convertValues(source["results"], SearchItem);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SmokeCheckItem {
	    name: string;
	    ok: boolean;
	    detail: string;

	    static createFrom(source: any = {}) {
	        return new SmokeCheckItem(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.ok = source["ok"];
	        this.detail = source["detail"];
	    }
	}
	export class SmokeRequest {
	    gateway_url: string;
	    skip_qdrant: boolean;
	    skip_embedding: boolean;
	    skip_reranker: boolean;

	    static createFrom(source: any = {}) {
	        return new SmokeRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.gateway_url = source["gateway_url"];
	        this.skip_qdrant = source["skip_qdrant"];
	        this.skip_embedding = source["skip_embedding"];
	        this.skip_reranker = source["skip_reranker"];
	    }
	}
	export class SmokeResponse {
	    ok: boolean;
	    checks: SmokeCheckItem[];

	    static createFrom(source: any = {}) {
	        return new SmokeResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.checks = this.convertValues(source["checks"], SmokeCheckItem);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SpeechOptions {
	    voice_profile_id: string;
	    style?: SpeechStyle;

	    static createFrom(source: any = {}) {
	        return new SpeechOptions(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.voice_profile_id = source["voice_profile_id"];
	        this.style = this.convertValues(source["style"], SpeechStyle);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SpeechStyle {
	    affect: string;
	    intensity: number;
	    pace: number;
	    pitch_hint: number;
	    volume: number;
	    pause_style: string;
	    interruptible: boolean;

	    static createFrom(source: any = {}) {
	        return new SpeechStyle(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.affect = source["affect"];
	        this.intensity = source["intensity"];
	        this.pace = source["pace"];
	        this.pitch_hint = source["pitch_hint"];
	        this.volume = source["volume"];
	        this.pause_style = source["pause_style"];
	        this.interruptible = source["interruptible"];
	    }
	}
	export class StackActionResponse {
	    status: string;
	    steps: Record<string, any>;

	    static createFrom(source: any = {}) {
	        return new StackActionResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.steps = source["steps"];
	    }
	}
	export class TraceValidation {
	    valid: boolean;
	    missing: string[];
	    latencies_ms: Record<string, number>;

	    static createFrom(source: any = {}) {
	        return new TraceValidation(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.valid = source["valid"];
	        this.missing = source["missing"];
	        this.latencies_ms = source["latencies_ms"];
	    }
	}
	export class VoiceCapabilities {
	    controls: Record<string, VoiceControl>;
	    streaming: boolean;
	    streaming_mode: string;
	    interruptible: boolean;

	    static createFrom(source: any = {}) {
	        return new VoiceCapabilities(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.controls = this.convertValues(source["controls"], VoiceControl, true);
	        this.streaming = source["streaming"];
	        this.streaming_mode = source["streaming_mode"];
	        this.interruptible = source["interruptible"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class VoiceControl {
	    type: string;
	    min: number;
	    max: number;
	    step?: number;
	    values?: string[];

	    static createFrom(source: any = {}) {
	        return new VoiceControl(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.min = source["min"];
	        this.max = source["max"];
	        this.step = source["step"];
	        this.values = source["values"];
	    }
	}
	export class VoiceHint {
	    pace: number;
	    volume: number;

	    static createFrom(source: any = {}) {
	        return new VoiceHint(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pace = source["pace"];
	        this.volume = source["volume"];
	    }
	}
	export class VoiceProfile {
	    voice_profile_id: string;
	    display_name: string;
	    provider: string;
	    model_revision: string;
	    language: string;
	    clone_prompt_digest: string;
	    reference_group_digest: string;
	    synthesis_config_digest?: string;
	    provenance_id: string;
	    default_style: SpeechStyle;
	    capabilities: VoiceCapabilities;
	    available: boolean;
	    error_code?: string;

	    static createFrom(source: any = {}) {
	        return new VoiceProfile(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.voice_profile_id = source["voice_profile_id"];
	        this.display_name = source["display_name"];
	        this.provider = source["provider"];
	        this.model_revision = source["model_revision"];
	        this.language = source["language"];
	        this.clone_prompt_digest = source["clone_prompt_digest"];
	        this.reference_group_digest = source["reference_group_digest"];
	        this.synthesis_config_digest = source["synthesis_config_digest"];
	        this.provenance_id = source["provenance_id"];
	        this.default_style = this.convertValues(source["default_style"], SpeechStyle);
	        this.capabilities = this.convertValues(source["capabilities"], VoiceCapabilities);
	        this.available = source["available"];
	        this.error_code = source["error_code"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class VoiceProfileCatalog {
	    default_profile_id: string;
	    profiles: VoiceProfile[];
	    error_code?: string;

	    static createFrom(source: any = {}) {
	        return new VoiceProfileCatalog(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.default_profile_id = source["default_profile_id"];
	        this.profiles = this.convertValues(source["profiles"], VoiceProfile);
	        this.error_code = source["error_code"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class VoiceReadiness {
	    state: string;
	    can_start: boolean;
	    error_code?: string;

	    static createFrom(source: any = {}) {
	        return new VoiceReadiness(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.can_start = source["can_start"];
	        this.error_code = source["error_code"];
	    }
	}
	export class VoiceSessionSnapshot {
	    id: string;
	    conversation_id: string;
	    epoch: number;
	    state: string;

	    static createFrom(source: any = {}) {
	        return new VoiceSessionSnapshot(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.conversation_id = source["conversation_id"];
	        this.epoch = source["epoch"];
	        this.state = source["state"];
	    }
	}
	export class VoiceTurnRequest {
	    voice_session_id?: string;
	    voice_session_epoch?: number;
	    session_id: string;
	    input_kind?: string;
	    chat: ChatRequest;
	    generation_limits: GenerationLimits;
	    speech: SpeechOptions;

	    static createFrom(source: any = {}) {
	        return new VoiceTurnRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.voice_session_id = source["voice_session_id"];
	        this.voice_session_epoch = source["voice_session_epoch"];
	        this.session_id = source["session_id"];
	        this.input_kind = source["input_kind"];
	        this.chat = this.convertValues(source["chat"], ChatRequest);
	        this.generation_limits = this.convertValues(source["generation_limits"], GenerationLimits);
	        this.speech = this.convertValues(source["speech"], SpeechOptions);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class WatchRequest {
	    paths: string[];
	    project?: string;
	    tags?: string[];
	    interval: number;
	    recursive: boolean;

	    static createFrom(source: any = {}) {
	        return new WatchRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.paths = source["paths"];
	        this.project = source["project"];
	        this.tags = source["tags"];
	        this.interval = source["interval"];
	        this.recursive = source["recursive"];
	    }
	}
	export class WebSearchPlanResponse {
	    plan_id: string;
	    decision: string;
	    outbound_query: string;
	    risk_categories: string[];
	    expires_at: string;

	    static createFrom(source: any = {}) {
	        return new WebSearchPlanResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.plan_id = source["plan_id"];
	        this.decision = source["decision"];
	        this.outbound_query = source["outbound_query"];
	        this.risk_categories = source["risk_categories"];
	        this.expires_at = source["expires_at"];
	    }
	}
	export class WebSearchStatus {
	    status: string;
	    detail?: string;
	    source_count: number;

	    static createFrom(source: any = {}) {
	        return new WebSearchStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.detail = source["detail"];
	        this.source_count = source["source_count"];
	    }
	}
	export class WorkflowRunResponse {
	    workflow: string;
	    preset_name: string;
	    status: string;
	    detail: string;
	    steps: WorkflowStep[];

	    static createFrom(source: any = {}) {
	        return new WorkflowRunResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workflow = source["workflow"];
	        this.preset_name = source["preset_name"];
	        this.status = source["status"];
	        this.detail = source["detail"];
	        this.steps = this.convertValues(source["steps"], WorkflowStep);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class WorkflowStep {
	    name: string;
	    status: string;
	    detail: string;

	    static createFrom(source: any = {}) {
	        return new WorkflowStep(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.status = source["status"];
	        this.detail = source["detail"];
	    }
	}
}
