export namespace main {

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
	export class ChatRequest {
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
	export class SearchItem {
	    chunk_id: string;
	    source_path: string;
	    heading_path: string[];
	    project: string;
	    tags: string[];
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
	        this.source_path = source["source_path"];
	        this.heading_path = source["heading_path"];
	        this.project = source["project"];
	        this.tags = source["tags"];
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
	export class ChatResponse {
	    answer: string;
	    thinking?: string;
	    sources?: SearchItem[];
	    finish_reason?: string;
	    raw: any;
	    web_search_status?: WebSearchStatus;

	    static createFrom(source: any = {}) {
	        return new ChatResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.answer = source["answer"];
	        this.thinking = source["thinking"];
	        this.sources = this.convertValues(source["sources"], SearchItem);
	        this.finish_reason = source["finish_reason"];
	        this.raw = source["raw"];
	        this.web_search_status = this.convertValues(source["web_search_status"], WebSearchStatus);
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
	export class HealthResponse {
	    status: string;
	    service: string;
	    configured_models: string[];
	    web_search_enabled: boolean;

	    static createFrom(source: any = {}) {
	        return new HealthResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.service = source["service"];
	        this.configured_models = source["configured_models"];
	        this.web_search_enabled = source["web_search_enabled"];
	    }
	}
	export class ImportLocalModelRequest {
	    id: string;
	    path: string;
	    base_model_id: string;

	    static createFrom(source: any = {}) {
	        return new ImportLocalModelRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.path = source["path"];
	        this.base_model_id = source["base_model_id"];
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
	        this.available = source["available"];
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
	export class LocalModelCatalog {
	    models: LocalModelArtifact[];
	    adapters: LocalAdapterArtifact[];
	    selections: Record<string, LocalModelSelection>;
	    revision: string;
	    developer_mode: boolean;

	    static createFrom(source: any = {}) {
	        return new LocalModelCatalog(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.models = this.convertValues(source["models"], LocalModelArtifact);
	        this.adapters = this.convertValues(source["adapters"], LocalAdapterArtifact);
	        this.selections = this.convertValues(source["selections"], LocalModelSelection, true);
	        this.revision = source["revision"];
	        this.developer_mode = source["developer_mode"];
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

}

