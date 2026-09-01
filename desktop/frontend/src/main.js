import './style.css';
import './app.css';
import {
  buildDropResultMessage,
  classifyDroppedPaths,
} from './ingestDrop';
import {
  renderPresetBatchSelectionButtons,
  renderPresetBatchSelectionMeta,
  renderPresetPrimaryActionButtons,
  renderSinglePresetBatchActionButtons,
} from './presetBatchRender';
import {prepareWebSearchRequest} from './webSearchFlow';
import {
  buildKarteConversationRequest,
  formatLocalISOString,
  renderKarteConversationCard,
} from './karteConversation';
import {mountModelManager} from './modelManager';
import {
  assertBlindPreferencePair,
  PREFERENCE_GENERATION_BATCH_SIZE,
  preferenceEmptyMessage,
  preferenceGenerationLimit,
  preferenceSelectionForKey,
  renderBlindPreferencePair,
  renderPromptComparison,
} from './preferenceReview';
import {
  renderOverviewPresetRuntimeHintCard,
  renderPresetCatalogCard,
  renderSelectedPresetPreviewCard,
  renderSelectedPresetWorkflowCard,
  renderSelectedPresetWorkflowVerificationSection,
  renderSelectedPresetWorkflowEmptyCard,
} from './presetWorkflowRender';

import {
  GetLocalModelCatalog,
  SetDeveloperMode,
  ImportLocalModel,
  ApplyLocalModel,
  CancelBatchWorkflow,
  ClearBatchPresetSelection,
  ClearBatchWorkflowState,
  ClearExecutionHistory,
  CreatePreferenceSession,
  DeleteProjectPreset,
  DeleteSavedRequest,
  ExportResult,
  ExportPreferenceSession,
  GeneratePreferencePairs,
  GetBatchPresetSelection,
  GetRegressionWatchProfiles,
  GetRegressionWatchSettings,
  GetBatchWorkflowState,
  GetExecutionHistory,
  GetGatewayURL,
  GetKarteProposalStatus,
  GetIndexSource,
  ListExportedResults,
  ListPreferenceSessions,
  ReadExportedResult,
  GetLocalConfigFiles,
  GetProjectPresets,
  GetRuntimeStatus,
  GetSavedRequests,
  Health,
  LoadLocalConfigExample,
  Models,
  NextPreferencePair,
  OpenWebSource,
  PlanWebSearch,
  PlanKarteConversation,
  PreferenceStats,
  PublishKarteConversation,
  ApproveWebSearch,
  RecordExecution,
  ReloadGatewayConfig,
  RunChatAction,
  RunEmbeddingAction,
  RunEvalAction,
  RunIndexBrowseAction,
  RunIngestAction,
  RunPresetEval,
  RunPresetValidate,
  RunPresetIngest,
  RunPresetIngestEval,
  RunPresetRecoveryAction,
  RunPresetRuntimeStackPrepare,
  RunPresetSmoke,
  RunRagQueryAction,
  RunRagSearchAction,
  RunPresetStackIngestEval,
  RunPresetVerification,
  RunPresetWatch,
  RunRuntimeConfigAction,
  RunRuntimeSmoke,
  RunRuntimeServiceAction,
  RunRuntimeStackAction,
  RoutePlan,
  SaveProjectPreset,
  SaveRequest,
  SetBatchPresetSelection,
  SetRegressionWatchProfiles,
  SetRegressionWatchSettings,
  SetBatchWorkflowState,
  SetGatewayURL,
  StartBatchPresetEval,
  StartBatchPresetIngest,
  StartBatchPresetIngestEval,
  StartBatchPresetRuntimeStackPrepare,
  StartBatchPresetSmoke,
  StartBatchPresetStackIngestEval,
  StartBatchPresetValidate,
  StartBatchPresetWatch,
  StartBatchPresetVerification,
  ValidateProjectPreset,
  VotePreferencePair,
} from '../wailsjs/go/main/App';
import { EventsOn, OnFileDrop, OnFileDropOff } from '../wailsjs/runtime/runtime';

const app = document.querySelector('#app');
let currentExecutionHistory = [];
let currentPresets = [];
let latestChatExport = null;
let latestRouteExport = null;
let latestRagExport = null;
let latestEmbeddingExport = null;
let latestIngestExport = null;
let latestEvalExport = null;
let latestWorkflowExport = null;
let latestIndexSummaryExport = null;
let latestIndexExport = null;
let currentIndexBrowseResponse = null;
let currentIndexSourceResponse = null;
let latestRuntimeStatus = null;
let preferenceSessionId = '';
let preferencePair = null;
let preferenceSelection = null;
let preferenceSaving = false;
let preferencePrefetchPromise = null;
let preferenceLastVote = null;
let preferenceCorrectionVoteId = '';
let chatThreadEntries = [];
let chatConversationId = createKarteConversationId();
let chatOccurredAt = formatLocalISOString();
let latestChatSources = [];
let activeChatSourceIndex = 0;
let latestRouteInspectorState = null;
let latestChatSourceTitle = 'Sources';
let activeChatStreamRequestId = '';
let chatStreamListenerBound = false;
let chatSendInFlight = false;
let webSearchAvailable = false;
let karteAvailable = false;
let sidebarCollapsed = false;
let activePanelTab = 'chat';
const tabScrollPositions = new Map();
let chatSourceScope = 'all';
let activeIngestDropZone = '';
let latestPresetValidationToken = 0;
let latestValidatedPreset = null;
let latestValidationResponse = null;
let currentPresetValidationMap = new Map();
let expandedPresetNames = new Set();
let selectedBatchPresetNames = new Set();
let batchWorkflowState = null;
let presetCatalogFilter = 'all';
let presetCatalogSort = 'name';
let evalDatasetTrendFilter = 'all';
let evalDatasetTrendSort = 'dataset';
let regressionWatchSourceHitDrop = 0;
let regressionWatchIncludePreset = true;
let regressionWatchIncludeDataset = true;
let currentRegressionWatchProfiles = {};
let presetCompareLeftName = '';
let presetCompareRightName = '';
const CHAT_MODE_MAX_TOKENS = {
  auto: 2048,
  fast: 1024,
  work: 4096,
  rag: 2048,
  code: 3072,
};
const PRESET_CATALOG_FILTER_KEY = 'local-llm-workbench:preset-catalog-filter';
const PRESET_CATALOG_SORT_KEY = 'local-llm-workbench:preset-catalog-sort';
const EVAL_DATASET_TREND_FILTER_KEY = 'local-llm-workbench:eval-dataset-trend-filter';
const EVAL_DATASET_TREND_SORT_KEY = 'local-llm-workbench:eval-dataset-trend-sort';
const REGRESSION_WATCH_SOURCE_HIT_DROP_KEY = 'local-llm-workbench:regression-watch-source-hit-drop';
const REGRESSION_WATCH_INCLUDE_PRESET_KEY = 'local-llm-workbench:regression-watch-include-preset';
const REGRESSION_WATCH_INCLUDE_DATASET_KEY = 'local-llm-workbench:regression-watch-include-dataset';
const REGRESSION_WATCH_PROFILES_KEY = 'local-llm-workbench:regression-watch-profiles';
const BATCH_PRESET_SELECTION_KEY = 'local-llm-workbench:batch-preset-selection';
const SAVED_REQUEST_KIND_CONFIGS = [
  {
    kind: 'route',
    selectId: 'route-history-select',
    nameId: 'route-save-name',
    saveButtonId: 'save-route-request',
    loadButtonId: 'load-route-request',
    deleteButtonId: 'delete-route-request',
    placeholder: 'Select saved route',
    emptySaveMessage: 'Route save name is required.',
    emptyLoadMessage: 'Select a saved route request first.',
    emptyDeleteMessage: 'Select or enter a route name first.',
    onError: (message) => renderRuntimeMessage('route-output', message),
    buildPayload: () => ({
      mode: document.getElementById('route-mode').value,
      prompt: document.getElementById('route-prompt').value,
    }),
    applyToForm: (item) => {
      activateTab('router');
      document.getElementById('route-save-name').value = item.name || '';
      document.getElementById('route-mode').value = item.mode || 'auto';
      document.getElementById('route-prompt').value = item.prompt || '';
    },
  },
  {
    kind: 'chat',
    selectId: 'chat-history-select',
    nameId: 'chat-save-name',
    saveButtonId: 'save-chat-request',
    loadButtonId: 'load-chat-request',
    deleteButtonId: 'delete-chat-request',
    placeholder: 'Current chat',
    presetSelectId: 'preset-chat-request-name',
    emptySaveMessage: 'Chat save name is required.',
    emptyLoadMessage: 'Select a saved chat first.',
    emptyDeleteMessage: 'Select or enter a chat name first.',
    onError: (message) => renderRuntimeMessage('chat-output', message),
    buildPayload: () => ({
      mode: document.getElementById('chat-mode').value,
      prompt: document.getElementById('chat-prompt').value,
    }),
    applyToForm: (item) => {
      activateTab('chat');
      document.getElementById('chat-save-name').value = item.name || '';
      document.getElementById('chat-mode').value = item.mode || 'auto';
      document.getElementById('chat-prompt').value = item.prompt || '';
    },
  },
  {
    kind: 'rag',
    selectId: 'rag-history-select',
    nameId: 'rag-save-name',
    saveButtonId: 'save-rag-request',
    loadButtonId: 'load-rag-request',
    deleteButtonId: 'delete-rag-request',
    placeholder: 'Select saved RAG',
    presetSelectId: 'preset-rag-request-name',
    emptySaveMessage: 'RAG save name is required.',
    emptyLoadMessage: 'Select a saved RAG request first.',
    emptyDeleteMessage: 'Select or enter a RAG name first.',
    onError: (message) => renderRuntimeMessage('rag-output', message),
    buildPayload: () => ({
      query: document.getElementById('rag-query').value,
      project: document.getElementById('rag-project').value,
      source_path: document.getElementById('rag-source-path').value.trim(),
      tags: parseTagList(document.getElementById('rag-tags').value),
      top_k: getPositiveInt('rag-top-k', 5),
      answer: document.getElementById('rag-answer').checked,
    }),
    applyToForm: (item) => {
      activateTab('rag');
      document.getElementById('rag-save-name').value = item.name || '';
      document.getElementById('rag-query').value = item.query || '';
      document.getElementById('rag-project').value = item.project || '';
      document.getElementById('rag-source-path').value = item.source_path || '';
      document.getElementById('rag-tags').value = Array.isArray(item.tags) ? item.tags.join(', ') : (item.tags || '');
      document.getElementById('rag-top-k').value = String(item.top_k || 5);
      document.getElementById('rag-answer').checked = item.answer !== false;
    },
  },
  {
    kind: 'embedding',
    selectId: 'embedding-history-select',
    nameId: 'embedding-save-name',
    saveButtonId: 'save-embedding-request',
    loadButtonId: 'load-embedding-request',
    deleteButtonId: 'delete-embedding-request',
    placeholder: 'Select saved embedding',
    emptySaveMessage: 'Embedding save name is required.',
    emptyLoadMessage: 'Select a saved embedding request first.',
    emptyDeleteMessage: 'Select or enter an embedding name first.',
    onError: (message) => renderRuntimeMessage('embedding-output', message),
    buildPayload: () => ({
      model: document.getElementById('embedding-model').value.trim() || 'auto',
      input: document.getElementById('embedding-input').value,
    }),
    applyToForm: (item) => {
      activateTab('rag');
      document.getElementById('embedding-save-name').value = item.name || '';
      document.getElementById('embedding-model').value = item.model || 'auto';
      document.getElementById('embedding-input').value = item.input || '';
    },
  },
  {
    kind: 'index',
    selectId: 'index-history-select',
    nameId: 'index-save-name',
    saveButtonId: 'save-index-request',
    loadButtonId: 'load-index-request',
    deleteButtonId: 'delete-index-request',
    placeholder: 'Select saved index',
    emptySaveMessage: 'Index save name is required.',
    emptyLoadMessage: 'Select a saved index request first.',
    emptyDeleteMessage: 'Select or enter an index name first.',
    onError: (message) => renderRuntimeMessage('index-output', message),
    buildPayload: () => ({
      project: document.getElementById('index-project').value.trim(),
      source_query: document.getElementById('index-source-query').value.trim(),
      limit: getPositiveInt('index-limit', 20),
    }),
    applyToForm: (item) => {
      activateTab('rag');
      document.getElementById('index-save-name').value = item.name || '';
      document.getElementById('index-project').value = item.project || '';
      document.getElementById('index-source-query').value = item.source_query || '';
      document.getElementById('index-limit').value = String(item.limit || 20);
    },
  },
  {
    kind: 'ingest',
    selectId: 'ingest-history-select',
    nameId: 'ingest-save-name',
    saveButtonId: 'save-ingest-request',
    loadButtonId: 'load-ingest-request',
    deleteButtonId: 'delete-ingest-request',
    placeholder: 'Select saved ingest',
    presetSelectId: 'preset-ingest-request-name',
    emptySaveMessage: 'Ingest save name is required.',
    emptyLoadMessage: 'Select a saved ingest request first.',
    emptyDeleteMessage: 'Select or enter an ingest name first.',
    onError: (message) => setOutput('ingest-output', message),
    buildPayload: () => ({
      paths: document.getElementById('ingest-paths').value,
      project: document.getElementById('ingest-project').value,
      tags: parseTagList(document.getElementById('ingest-tags').value),
      recursive: true,
    }),
    applyToForm: (item) => {
      activateTab('rag');
      document.getElementById('ingest-save-name').value = item.name || '';
      document.getElementById('ingest-paths').value = item.paths || '';
      document.getElementById('ingest-project').value = item.project || '';
      document.getElementById('ingest-tags').value = Array.isArray(item.tags) ? item.tags.join(', ') : (item.tags || '');
    },
  },
  {
    kind: 'eval',
    selectId: 'eval-history-select',
    nameId: 'eval-save-name',
    saveButtonId: 'save-eval-request',
    loadButtonId: 'load-eval-request',
    deleteButtonId: 'delete-eval-request',
    placeholder: 'Select saved eval',
    presetSelectId: 'preset-eval-request-name',
    emptySaveMessage: 'Eval save name is required.',
    emptyLoadMessage: 'Select a saved eval request first.',
    emptyDeleteMessage: 'Select or enter an eval name first.',
    onError: (message) => renderRuntimeMessage('eval-output', message),
    buildPayload: () => ({
      dataset_path: document.getElementById('eval-dataset').value,
      project: document.getElementById('eval-project').value,
      source_path: document.getElementById('eval-source-path').value.trim(),
      top_k: getPositiveInt('eval-top-k', 5),
      with_answer: document.getElementById('eval-with-answer').checked,
    }),
    applyToForm: (item) => {
      activateTab('eval');
      document.getElementById('eval-save-name').value = item.name || '';
      document.getElementById('eval-dataset').value = item.dataset_path || 'configs/eval.sample.yaml';
      document.getElementById('eval-project').value = item.project || '';
      document.getElementById('eval-source-path').value = item.source_path || '';
      document.getElementById('eval-top-k').value = String(item.top_k || 5);
      document.getElementById('eval-with-answer').checked = item.with_answer === true;
    },
  },
];

app.innerHTML = `
  <div
    class="shell drop-ingest-zone drop-ingest-zone-shell wails-drop-target"
    data-drop-ingest-zone="workspace"
    data-drop-overlay="Drop files or folders anywhere to ingest into RAG"
  >
    <aside class="sidebar">
      <div class="brand">
        <div class="brand-mark">ER</div>
        <div>
          <div class="brand-title">Ephy Runtime</div>
          <div class="brand-subtitle">Chat-first workspace</div>
        </div>
        <button id="sidebar-toggle" class="sidebar-toggle" type="button" aria-label="Hide sidebar">Hide</button>
      </div>
      <nav class="nav">
        <div class="nav-section-label">Workspace</div>
        <button class="nav-btn active" data-tab="chat">New Chat</button>
        <button class="nav-btn" data-tab="chat-history">Chats</button>
        <button class="nav-btn" data-tab="overview">Projects</button>
        <button class="nav-btn" data-tab="rag">Library</button>
        <div class="nav-section-label">Advanced</div>
        <button class="nav-btn" data-tab="settings">Settings</button>
        <button class="nav-btn utility-hidden" data-tab="runtime">Runtime</button>
        <button class="nav-btn utility-hidden" data-tab="router">Routing</button>
        <button class="nav-btn utility-hidden" data-tab="eval">Evaluation</button>
      </nav>
      <div class="sidebar-foot">
        <div class="eyebrow">Gateway URL</div>
        <input id="gateway-url" class="text-input" />
        <button id="save-url" class="ghost-btn">Apply</button>
      </div>
    </aside>
    <header class="app-header">
      <div class="app-header-inner">
        <div class="chat-toolbar">
                <button id="chat-sidebar-toggle" class="gmail-icon-btn chat-toolbar-menu" type="button" aria-label="Toggle sidebar">
                  <span class="gmail-hamburger"></span>
                </button>
                <div class="chat-toolbar-brand">
                  <div class="gmail-brand-mark">ER</div>
                </div>
                <select id="chat-history-select" class="text-input chat-toolbar-select chat-toolbar-current" aria-label="Current Chat">
                  <option value="">Current Chat</option>
                </select>
                <select id="chat-project-select" class="text-input chat-toolbar-select" aria-label="Project">
                  <option value="">All Projects</option>
                </select>
                <select id="chat-mode" class="text-input chat-toolbar-select" aria-label="Mode">
                  <option value="auto">Auto</option>
                  <option value="fast" selected>Quick</option>
                  <option value="work">Deep Work</option>
                  <option value="rag">With Sources</option>
                  <option value="code">Code</option>
                </select>
                <select id="chat-source-scope-select" class="text-input chat-toolbar-select" aria-label="Source Scope">
                  <option value="all">All</option>
                  <option value="project">Current Project</option>
                  <option value="selected_docs">Selected Docs</option>
                </select>
                <select id="chat-top-k-select" class="text-input chat-toolbar-select chat-toolbar-topk" aria-label="Top K">
                  <option value="3">Top K 3</option>
                  <option value="5" selected>Top K 5</option>
                  <option value="8">Top K 8</option>
                  <option value="10">Top K 10</option>
                  <option value="20">Top K 20</option>
                </select>
                <label class="chat-web-toggle" title="Search the web through the local privacy gateway">
                  <input id="chat-web-search" type="checkbox" />
                  <span>Web</span>
                </label>
                <span id="chat-web-status" class="chat-web-status">Local only</span>
                <button id="start-conversation" class="ghost-btn" type="button">Ephyを起動</button>
                <span id="conversation-start-status" role="status" class="helper-text"></span>
                <div id="chat-source-scope" class="chat-toolbar-meta">scope=all | project=(default) | top_k=5</div>
                <div class="chat-header-actions">
                  <details id="chat-more-menu" class="chat-more-menu">
                    <summary class="ghost-btn">More</summary>
                    <div class="chat-more-panel">
                      <label class="field compact-field">
                        <span>Chat Name</span>
                        <input id="chat-save-name" class="text-input" placeholder="chat-idea-1" />
                      </label>
                      <div class="chat-more-actions">
                        <button id="chat-route-toggle" class="ghost-btn" type="button">Route</button>
                        <button id="export-chat" class="ghost-btn" type="button">Export Markdown</button>
                        <button id="chat-open-library" class="ghost-btn" type="button">Open Library</button>
                        <button id="chat-new-session" class="ghost-btn" type="button">New Chat</button>
                        <button id="save-chat-request" class="ghost-btn" type="button">Save Current Chat</button>
                        <button id="rename-chat-request" class="ghost-btn" type="button">Rename Chat</button>
                        <button id="load-chat-request" class="ghost-btn" type="button">Load Saved Chat</button>
                        <button id="delete-chat-request" class="ghost-btn" type="button">Delete Chat</button>
                      </div>
                    </div>
                  </details>
                </div>
        </div>
      </div>
    </header>
    <main class="content">
      <div class="content-chrome">
        <button id="sidebar-reveal" class="sidebar-reveal hidden" type="button" aria-label="Show sidebar">Menu</button>
      </div>
      <section class="tab" data-tab-panel="overview">
        <div class="hero">
          <div>
            <div class="eyebrow">Phase 1 + early Phase 2</div>
            <h1>Gateway, routing, ingest, search, and desktop control.</h1>
            <p>Use this desktop shell to inspect the gateway, run chat requests, ingest local docs, and test retrieval before adding heavier RAG components.</p>
          </div>
          <div class="status-card">
            <div class="status-line"><span>Gateway</span><strong id="gateway-status">Unknown</strong></div>
            <div class="status-line"><span>Configured models</span><strong id="model-count">0</strong></div>
            <button id="refresh-overview" class="primary-btn">Refresh Overview</button>
          </div>
        </div>
        <div class="grid two">
          <article class="panel">
            <div class="panel-head">
              <h2>Health</h2>
            </div>
            <pre id="health-output" class="output-block"></pre>
          </article>
          <article class="panel">
            <div class="panel-head">
              <h2>Models</h2>
            </div>
            <pre id="models-output" class="output-block"></pre>
          </article>
        </div>
        <article class="panel top-gap">
          <div class="panel-head">
            <h2>Preset Launcher</h2>
          </div>
          <div class="actions">
            <select id="overview-preset-select" class="text-input">
              <option value="">Select preset</option>
            </select>
            <button id="overview-load-preset" class="ghost-btn">Load Into Runtime</button>
            <button id="overview-validate-preset" class="ghost-btn">Validate Selected Preset</button>
            <button id="overview-run-preset-stack-ingest-eval" class="primary-btn">Start Stack + Ingest + Eval</button>
          </div>
          <div id="overview-preset-runtime-hint" class="runtime-result"></div>
        </article>
        <article class="panel top-gap">
          <div class="panel-head">
            <h2>Selected Preset Preview</h2>
          </div>
          <div id="selected-preset-preview" class="runtime-result"></div>
        </article>
        <article class="panel top-gap">
          <div class="panel-head">
            <h2>Selected Preset Workflow</h2>
          </div>
          <div id="selected-preset-workflow" class="runtime-result"></div>
        </article>
        <article class="panel top-gap">
          <div class="panel-head">
            <h2>Preset Validation</h2>
          </div>
          <div id="overview-preset-validation" class="runtime-result"></div>
        </article>
        <article class="panel top-gap">
          <div class="panel-head">
            <h2>Batch Preset Runner</h2>
          </div>
          <div class="actions">
            <button id="select-filtered-batch-presets" class="ghost-btn">Select Filtered Presets</button>
            <button id="clear-batch-presets" class="ghost-btn">Clear Selection</button>
            <button id="run-batch-preset-validate" class="ghost-btn">Run Validate For Selected</button>
            <button id="run-batch-preset-smoke" class="ghost-btn">Run Smoke For Selected</button>
            <button id="run-batch-preset-verification" class="ghost-btn">Run Verification For Selected</button>
            <button id="run-batch-preset-watch" class="ghost-btn">Run Watch For Selected</button>
            <button id="run-batch-preset-runtime-stack-prepare" class="ghost-btn">Run Runtime + Stack For Selected</button>
            <button id="run-batch-preset-ingest" class="ghost-btn">Run Ingest For Selected</button>
            <button id="run-batch-preset-eval" class="ghost-btn">Run Eval For Selected</button>
            <button id="run-batch-preset-ingest-eval" class="ghost-btn">Run Ingest + Eval For Selected</button>
            <button id="run-batch-preset-stack-ingest-eval" class="primary-btn">Run Stack + Ingest + Eval For Selected</button>
          </div>
          <div id="batch-preset-output" class="runtime-result"></div>
        </article>
        <article class="panel top-gap">
          <div class="panel-head">
            <h2>Preset Comparison</h2>
          </div>
          <div class="actions">
            <select id="preset-compare-left" class="text-input">
              <option value="">Compare left preset</option>
            </select>
            <select id="preset-compare-right" class="text-input">
              <option value="">Compare right preset</option>
            </select>
            <button id="use-selected-for-compare" class="ghost-btn">Use Selected Preset</button>
            <button id="swap-compare-presets" class="ghost-btn">Swap</button>
            <button id="export-preset-compare" class="ghost-btn">Export Comparison</button>
          </div>
          <div id="preset-compare-output" class="runtime-result"></div>
        </article>
        <article class="panel top-gap">
          <div class="panel-head">
            <h2>Preset Catalog</h2>
          </div>
          <div class="actions">
            <select id="preset-catalog-filter" class="text-input">
              <option value="all">All Presets</option>
              <option value="regressed">Regressed Only</option>
              <option value="runtime_mismatch">Runtime Mismatch</option>
              <option value="external_rag">External RAG Presets</option>
              <option value="local_only">Local Only Presets</option>
              <option value="current">Current Runtime Presets</option>
            </select>
            <select id="preset-catalog-sort" class="text-input">
              <option value="name">Sort: Name</option>
              <option value="recent">Sort: Latest Activity</option>
              <option value="regression">Sort: Regression First</option>
            </select>
          </div>
          <div id="preset-catalog" class="runtime-result"></div>
        </article>
        <article class="panel top-gap">
          <div class="panel-head">
            <h2>Workflow Summary</h2>
          </div>
          <div class="actions">
            <select id="eval-dataset-trend-filter" class="text-input">
              <option value="all">All Datasets</option>
              <option value="regressed">Regressed Only</option>
            </select>
            <select id="eval-dataset-trend-sort" class="text-input">
              <option value="dataset">Sort: Dataset</option>
              <option value="recent">Sort: Latest Activity</option>
              <option value="regression">Sort: Regression First</option>
            </select>
            <select id="regression-watch-profile-select" class="text-input"></select>
            <input id="regression-watch-profile-name" class="text-input" placeholder="watch profile name" />
            <button id="apply-regression-watch-profile" class="ghost-btn">Apply Watch Profile</button>
            <button id="save-regression-watch-profile" class="ghost-btn">Save Current Watch</button>
            <button id="delete-regression-watch-profile" class="ghost-btn">Delete Watch Profile</button>
            <input id="regression-watch-source-hit-drop" class="text-input" type="number" step="0.01" min="0" value="0" placeholder="source_hit_rate drop threshold" />
            <label class="check-field inline"><input id="regression-watch-include-preset" type="checkbox" checked /> Monitor Presets</label>
            <label class="check-field inline"><input id="regression-watch-include-dataset" type="checkbox" checked /> Monitor Datasets</label>
          </div>
          <div id="workflow-summary" class="runtime-result"></div>
        </article>
        <article class="panel top-gap">
          <div class="panel-head">
            <h2>Recent Activity</h2>
          </div>
          <div class="actions">
            <button id="refresh-history" class="ghost-btn">Refresh Activity</button>
            <button id="clear-history" class="ghost-btn">Clear Activity</button>
          </div>
          <div id="recent-activity" class="runtime-result"></div>
        </article>
        <article class="panel top-gap">
          <div class="panel-head">
            <h2>Exports</h2>
          </div>
          <div id="exported-results" class="runtime-result"></div>
        </article>
        <article class="panel top-gap">
          <div class="panel-head">
            <h2>Export Preview</h2>
          </div>
          <div id="export-preview" class="runtime-result"></div>
        </article>
      </section>

      <section class="tab" data-tab-panel="runtime">
        <div class="grid two unequal">
          <article class="panel">
            <div class="panel-head">
              <h2>Runtime Control</h2>
            </div>
            <div class="status-grid">
              <div class="status-box">
                <span class="eyebrow dark">Fast</span>
                <strong id="runtime-fast-status">stopped</strong>
                <span id="runtime-fast-pid">PID: -</span>
              </div>
              <div class="status-box">
                <span class="eyebrow dark">Work</span>
                <strong id="runtime-work-status">stopped</strong>
                <span id="runtime-work-pid">PID: -</span>
              </div>
              <div class="status-box">
                <span class="eyebrow dark">Code</span>
                <strong id="runtime-code-status">stopped</strong>
                <span id="runtime-code-pid">PID: -</span>
              </div>
              <div class="status-box">
                <span class="eyebrow dark">Gateway</span>
                <strong id="runtime-gateway-status">unknown</strong>
                <span id="runtime-gateway-pid">PID: -</span>
              </div>
              <div class="status-box">
                <span class="eyebrow dark">Watch</span>
                <strong id="runtime-watch-status">stopped</strong>
                <span id="runtime-watch-pid">PID: -</span>
              </div>
              <div class="status-box">
                <span class="eyebrow dark">Embedding</span>
                <strong id="runtime-embedding-status">stopped</strong>
                <span id="runtime-embedding-pid">PID: -</span>
              </div>
              <div class="status-box">
                <span class="eyebrow dark">Qdrant</span>
                <strong id="runtime-qdrant-status">unknown</strong>
                <span id="runtime-qdrant-detail">Local binary: -</span>
              </div>
              <div class="status-box">
                <span class="eyebrow dark">Workspace</span>
                <strong id="runtime-workspace">-</strong>
              </div>
            </div>
            <div class="actions">
              <button id="start-recommended-stack" class="primary-btn">Start Recommended Stack</button>
              <button id="stop-recommended-stack" class="ghost-btn">Stop Recommended Stack</button>
              <button id="start-core-stack" class="primary-btn">Start Core Stack</button>
              <button id="stop-core-stack" class="ghost-btn">Stop Core Stack</button>
            </div>
            <div class="actions">
              <button id="start-fast" class="primary-btn">Start Fast</button>
              <button id="stop-fast" class="ghost-btn">Stop Fast</button>
              <button id="start-work" class="primary-btn">Start Work</button>
              <button id="stop-work" class="ghost-btn">Stop Work</button>
            </div>
            <div class="actions">
              <button id="start-code" class="primary-btn">Start Code</button>
              <button id="stop-code" class="ghost-btn">Stop Code</button>
            </div>
            <div class="actions">
              <button id="start-gateway" class="primary-btn">Start Gateway</button>
              <button id="stop-gateway" class="ghost-btn">Stop Gateway</button>
            </div>
            <div class="actions">
              <button id="start-embedding" class="primary-btn">Start Embedding</button>
              <button id="stop-embedding" class="ghost-btn">Stop Embedding</button>
            </div>
            <div class="actions">
              <button id="start-qdrant" class="primary-btn">Start Qdrant</button>
              <button id="stop-qdrant" class="ghost-btn">Stop Qdrant</button>
              <button id="refresh-runtime" class="ghost-btn">Refresh Runtime</button>
            </div>
            <div class="field">
              <span>Local Override Config</span>
              <pre id="runtime-config-status" class="output-block compact"></pre>
            </div>
            <div class="field">
              <span>Runtime Config Summary</span>
              <div id="runtime-config-summary" class="runtime-summary"></div>
            </div>
            <div class="actions">
              <button id="apply-local-only-preset" class="ghost-btn">Preset: Local Only</button>
              <button id="apply-external-rag-preset" class="ghost-btn">Preset: External Embedding + Qdrant</button>
            </div>
            <div class="actions">
              <button id="apply-local-only-now" class="primary-btn">Apply Local Only Now</button>
              <button id="apply-local-only-stack-now" class="primary-btn">Apply Local Only + Start Stack</button>
              <button id="apply-external-rag-now" class="primary-btn">Apply External Preset Now</button>
              <button id="apply-external-rag-stack-now" class="primary-btn">Apply External + Start Stack</button>
            </div>
            <label class="field">
              <span>Preset Runtime Profile</span>
              <select id="preset-runtime-profile" class="text-input">
                <option value="current">Use Current Runtime Config</option>
                <option value="local_only">Auto Apply Local Only</option>
                <option value="external_rag">Auto Apply External Embedding + Qdrant</option>
              </select>
            </label>
            <div class="grid two">
              <label class="field">
                <span>Project Preset</span>
                <input id="preset-name" class="text-input" placeholder="lab-default" />
              </label>
              <label class="field">
                <span>Saved Presets</span>
                <select id="preset-select" class="text-input">
                  <option value="">Select preset</option>
                </select>
              </label>
            </div>
            <div class="actions">
              <button id="save-preset" class="primary-btn">Save Preset</button>
              <button id="load-preset" class="ghost-btn">Load Preset</button>
              <button id="delete-preset" class="ghost-btn">Delete Preset</button>
              <button id="reload-presets" class="ghost-btn">Reload Presets</button>
              <button id="validate-current-preset" class="ghost-btn">Validate Current Preset</button>
            </div>
            <div id="runtime-preset-validation" class="runtime-result"></div>
            <div class="grid two">
              <label class="field">
                <span>Representative Chat</span>
                <select id="preset-chat-request-name" class="text-input">
                  <option value="">Select saved chat</option>
                </select>
              </label>
              <label class="field">
                <span>Chat Must Contain</span>
                <input id="preset-chat-expect-contains" class="text-input" placeholder="expected phrase in chat answer" />
              </label>
            </div>
            <div class="grid two">
              <label class="field">
                <span>Representative Ingest</span>
                <select id="preset-ingest-request-name" class="text-input">
                  <option value="">Select saved ingest</option>
                </select>
              </label>
              <label class="field">
                <span>Representative RAG</span>
                <select id="preset-rag-request-name" class="text-input">
                  <option value="">Select saved RAG</option>
                </select>
              </label>
            </div>
            <div class="grid two">
              <label class="field">
                <span>RAG Must Contain</span>
                <input id="preset-rag-expect-contains" class="text-input" placeholder="expected phrase in rag answer/result" />
              </label>
              <label class="field">
                <span>Representative Eval</span>
                <select id="preset-eval-request-name" class="text-input">
                  <option value="">Select saved eval</option>
                </select>
              </label>
            </div>
            <div class="grid two">
              <label class="field">
                <span>Eval Min Source Hit Rate</span>
                <input id="preset-eval-min-source-hit-rate" class="text-input" type="number" value="0" min="0" max="1" step="0.05" />
              </label>
            </div>
            <div class="field">
              <span>Workflow Smoke Policy</span>
              <label class="check-field">
                <input id="preset-run-smoke-first" type="checkbox" />
                <span>Run smoke before preset workflows</span>
              </label>
              <div class="actions">
                <label class="check-field inline">
                  <input id="preset-smoke-skip-qdrant" type="checkbox" />
                  <span>Skip Qdrant</span>
                </label>
                <label class="check-field inline">
                  <input id="preset-smoke-skip-embedding" type="checkbox" />
                  <span>Skip Embedding</span>
                </label>
                <label class="check-field inline">
                  <input id="preset-smoke-skip-reranker" type="checkbox" />
                  <span>Skip Reranker</span>
                </label>
              </div>
            </div>
            <div class="actions">
              <button id="preset-start-watch" class="ghost-btn">Start Preset Watch</button>
              <button id="preset-run-ingest" class="ghost-btn">Run Preset Ingest</button>
              <button id="preset-run-eval" class="ghost-btn">Run Preset Eval</button>
              <button id="preset-run-verification" class="ghost-btn">Run Preset Verification</button>
              <button id="preset-run-ingest-eval" class="primary-btn">Run Preset Ingest + Eval</button>
              <button id="preset-run-stack-ingest-eval" class="primary-btn">Start Stack + Ingest + Eval</button>
            </div>
            <div class="grid two">
              <label class="field">
                <span>models.local.yaml</span>
                <textarea id="models-local-editor" class="text-area compact-area" placeholder="models.local.yaml content"></textarea>
                <div class="actions">
                  <button id="load-models-example" class="ghost-btn">Use Example</button>
                  <button id="delete-models-local" class="ghost-btn">Delete Local</button>
                </div>
              </label>
              <label class="field">
                <span>rag.local.yaml</span>
                <textarea id="rag-local-editor" class="text-area compact-area" placeholder="rag.local.yaml content"></textarea>
                <div class="actions">
                  <button id="load-rag-example" class="ghost-btn">Use Example</button>
                  <button id="delete-rag-local" class="ghost-btn">Delete Local</button>
                </div>
              </label>
            </div>
            <div class="actions">
              <button id="reload-local-config" class="ghost-btn">Reload Local Config</button>
              <button id="save-models-local" class="primary-btn">Save models.local</button>
              <button id="save-rag-local" class="primary-btn">Save rag.local</button>
              <button id="reload-gateway-config" class="ghost-btn">Reload Gateway Config</button>
            </div>
            <label class="field">
              <span>Watch Paths</span>
              <textarea id="watch-paths" class="text-area compact-area" placeholder="/absolute/path/to/docs"></textarea>
            </label>
            <div class="grid two">
              <label class="field">
                <span>Watch Project</span>
                <input id="watch-project" class="text-input" />
              </label>
              <label class="field">
                <span>Watch Tags</span>
                <input id="watch-tags" class="text-input" placeholder="research, notes" />
              </label>
              <label class="field">
                <span>Interval Seconds</span>
                <input id="watch-interval" class="text-input" type="number" value="2" min="0.5" step="0.5" />
              </label>
            </div>
            <div class="actions">
              <button id="start-watch" class="primary-btn">Start Watch</button>
              <button id="stop-watch" class="ghost-btn">Stop Watch</button>
            </div>
            <div class="actions">
              <button id="run-smoke" class="primary-btn">Run Smoke</button>
              <label class="check-field inline">
                <input id="smoke-skip-qdrant" type="checkbox" />
                <span>Skip Qdrant</span>
              </label>
              <label class="check-field inline">
                <input id="smoke-skip-embedding" type="checkbox" />
                <span>Skip Embedding</span>
              </label>
              <label class="check-field inline">
                <input id="smoke-skip-reranker" type="checkbox" />
                <span>Skip Reranker</span>
              </label>
            </div>
            <p class="helper-text">Gateway は workspace の .venv/bin/python を使って起動します。Qdrant は workspace の local binary で制御します。</p>
          </article>
          <article class="panel">
            <div class="panel-head">
              <h2>Runtime Logs</h2>
            </div>
            <div class="grid two">
              <div>
                <div class="mini-head">Fast</div>
                <pre id="runtime-fast-log" class="output-block compact"></pre>
              </div>
              <div>
                <div class="mini-head">Work</div>
                <pre id="runtime-work-log" class="output-block compact"></pre>
              </div>
            </div>
            <div class="grid two top-gap">
              <div>
                <div class="mini-head">Code</div>
                <pre id="runtime-code-log" class="output-block compact"></pre>
              </div>
              <div>
                <div class="mini-head">Gateway</div>
                <pre id="runtime-gateway-log" class="output-block compact"></pre>
              </div>
            </div>
            <div class="grid two top-gap">
              <div>
                <div class="mini-head">Embedding</div>
                <pre id="runtime-embedding-log" class="output-block compact"></pre>
              </div>
            </div>
            <div class="grid two top-gap">
              <div>
                <div class="mini-head">Qdrant</div>
                <pre id="runtime-qdrant-log" class="output-block compact"></pre>
              </div>
              <div>
                <div class="mini-head">Watch</div>
                <pre id="runtime-watch-log" class="output-block compact"></pre>
              </div>
            </div>
            <div class="mini-head top-gap">Smoke</div>
            <div id="runtime-smoke-output" class="runtime-result"></div>
            <div class="mini-head top-gap">Stack Action</div>
            <div class="actions">
              <button id="export-workflow" class="ghost-btn">Export Workflow Markdown</button>
            </div>
            <div id="runtime-stack-output" class="runtime-result"></div>
          </article>
        </div>
      </section>

      <section class="tab active" data-tab-panel="chat">
        <div class="chat-first-layout">
          <article
            id="chat-drop-zone"
            class="panel chat-main-panel drop-ingest-zone wails-drop-target"
            data-drop-ingest-zone="chat"
            data-drop-overlay="Drop files or folders to ingest into RAG"
          >
            <div class="chat-toolbar-shell">
              <details class="route-inspector route-inspector-popover">
                <summary>Route Inspector</summary>
                <div id="chat-route-output" class="runtime-result"></div>
              </details>
            </div>
            <div id="chat-output" class="conversation-thread"></div>
            <div class="chat-composer">
              <div id="chat-drop-status" class="chat-drop-status helper-text"></div>
              <label class="field composer-field">
                <span>Prompt</span>
                <textarea id="chat-prompt" class="text-area chat-input" placeholder="Ask the gateway something..."></textarea>
                <span class="helper-text">Send with Cmd+Enter on macOS or Ctrl+Enter on other platforms.</span>
              </label>
              <div class="actions composer-actions">
                <button id="send-chat" class="primary-btn">Send</button>
              </div>
            </div>
          </article>
          <aside class="chat-sources-pane">
            <article class="panel source-side-panel">
              <div class="panel-head">
                <h2>Sources</h2>
              </div>
              <div id="chat-sources-meta" class="helper-text">No source-backed answer yet.</div>
              <div id="chat-source-list" class="source-card-list"></div>
            </article>
            <article class="panel source-side-panel">
              <div class="panel-head">
                <h2>Document Preview</h2>
              </div>
              <div id="chat-source-preview" class="runtime-result"></div>
            </article>
          </aside>
        </div>
      </section>

      <section class="tab" data-tab-panel="router">
        <div class="grid two unequal">
          <article class="panel">
            <div class="panel-head">
              <h2>Route Planner</h2>
            </div>
            <label class="field">
              <span>Mode Hint</span>
              <select id="route-mode" class="text-input">
                <option value="auto">auto</option>
                <option value="fast">fast</option>
                <option value="work">work</option>
                <option value="code">code</option>
                <option value="rag">rag</option>
              </select>
            </label>
            <label class="field">
              <span>Prompt</span>
              <textarea id="route-prompt" class="text-area" placeholder="Preview how the router will classify this request..."></textarea>
            </label>
            <div class="grid two">
              <label class="field">
                <span>Route Save Name</span>
                <input id="route-save-name" class="text-input" placeholder="route-backend-check" />
              </label>
              <label class="field">
                <span>Saved Route</span>
                <select id="route-history-select" class="text-input">
                  <option value="">Select saved route</option>
                </select>
              </label>
            </div>
            <div class="actions">
              <button id="run-route-plan" class="primary-btn">Plan Route</button>
              <button id="save-route-request" class="ghost-btn">Save Route</button>
              <button id="load-route-request" class="ghost-btn">Load Route</button>
              <button id="delete-route-request" class="ghost-btn">Delete Route</button>
            </div>
            <p class="helper-text">Chat 実行前に、どの mode と backend model に振られるかを確認できます。</p>
          </article>
          <article class="panel">
            <div class="panel-head">
              <h2>Route Decision</h2>
            </div>
            <div class="actions">
              <button id="export-route" class="ghost-btn">Export Markdown</button>
            </div>
            <div id="route-output" class="runtime-result"></div>
          </article>
        </div>
      </section>

      <section class="tab" data-tab-panel="rag">
        <div class="grid two">
          <article
            id="ingest-drop-zone"
            class="panel drop-ingest-zone wails-drop-target"
            data-drop-ingest-zone="library"
            data-drop-overlay="Drop files or folders to import into RAG"
          >
            <div class="panel-head">
              <h2>Import Documents</h2>
            </div>
            <label class="field">
              <span>Paths</span>
              <textarea id="ingest-paths" class="text-area" placeholder="/absolute/path/to/docs"></textarea>
            </label>
            <label class="field">
              <span>Project</span>
              <input id="ingest-project" class="text-input" />
            </label>
            <label class="field">
              <span>Tags</span>
              <input id="ingest-tags" class="text-input" placeholder="research, npo, robotics" />
            </label>
            <div class="grid two">
              <label class="field">
                <span>Ingest Save Name</span>
                <input id="ingest-save-name" class="text-input" placeholder="ingest-lab-docs" />
              </label>
              <label class="field">
                <span>Saved Ingest</span>
                <select id="ingest-history-select" class="text-input">
                  <option value="">Select saved ingest</option>
                </select>
              </label>
            </div>
            <div class="actions">
              <button id="run-ingest" class="primary-btn">Run Ingest</button>
              <button id="save-ingest-request" class="ghost-btn">Save Ingest</button>
              <button id="load-ingest-request" class="ghost-btn">Load Ingest</button>
              <button id="delete-ingest-request" class="ghost-btn">Delete Ingest</button>
            </div>
            <div class="actions">
              <button id="export-ingest" class="ghost-btn">Export Markdown</button>
            </div>
            <pre id="ingest-output" class="output-block"></pre>
          </article>
          <article class="panel">
            <div class="panel-head">
              <h2>Search Sources</h2>
            </div>
            <label class="field">
              <span>Question</span>
              <textarea id="rag-query" class="text-area" placeholder="Search your ingested notes..."></textarea>
            </label>
            <div class="grid two">
              <label class="field">
                <span>Project</span>
                <input id="rag-project" class="text-input" />
              </label>
              <label class="field">
                <span>Top K</span>
                <input id="rag-top-k" class="text-input" type="number" value="5" min="1" />
              </label>
            </div>
            <label class="field">
              <span>Source Path Filter</span>
              <input id="rag-source-path" class="text-input" placeholder="/absolute/path/to/source.md" />
            </label>
            <label class="field">
              <span>Tag Filter</span>
              <input id="rag-tags" class="text-input" placeholder="research, meeting" />
            </label>
            <label class="check-field">
              <input id="rag-answer" type="checkbox" checked />
              <span>Generate answer with sources</span>
            </label>
            <div class="actions">
              <button id="run-search" class="ghost-btn">Search Only</button>
              <button id="run-query" class="primary-btn">Run Saved Mode</button>
              <button id="clear-rag-source-path" class="ghost-btn">Clear Source Filter</button>
            </div>
            <div class="grid two">
              <label class="field">
                <span>RAG Save Name</span>
                <input id="rag-save-name" class="text-input" placeholder="rag-roster-check" />
              </label>
              <label class="field">
                <span>Saved RAG</span>
                <select id="rag-history-select" class="text-input">
                  <option value="">Select saved RAG</option>
                </select>
              </label>
            </div>
            <div class="actions">
              <button id="save-rag-request" class="ghost-btn">Save RAG</button>
              <button id="load-rag-request" class="ghost-btn">Load RAG</button>
              <button id="delete-rag-request" class="ghost-btn">Delete RAG</button>
            </div>
            <div class="actions">
              <button id="export-rag" class="ghost-btn">Export Markdown</button>
            </div>
            <div id="rag-output" class="runtime-result"></div>
          </article>
        </div>
        <div class="grid two top-gap">
          <article class="panel">
            <div class="panel-head">
              <h2>Embedding Probe</h2>
            </div>
            <label class="field">
              <span>Model Alias</span>
              <input id="embedding-model" class="text-input" value="auto" />
            </label>
            <label class="field">
              <span>Input Text</span>
              <textarea id="embedding-input" class="text-area" placeholder="Run a direct embedding request through the gateway..."></textarea>
            </label>
            <div class="grid two">
              <label class="field">
                <span>Embedding Save Name</span>
                <input id="embedding-save-name" class="text-input" placeholder="embedding-roster-probe" />
              </label>
              <label class="field">
                <span>Saved Embedding</span>
                <select id="embedding-history-select" class="text-input">
                  <option value="">Select saved embedding</option>
                </select>
              </label>
            </div>
            <div class="actions">
              <button id="run-embedding" class="primary-btn">Run Embedding</button>
              <button id="save-embedding-request" class="ghost-btn">Save Embedding</button>
              <button id="load-embedding-request" class="ghost-btn">Load Embedding</button>
              <button id="delete-embedding-request" class="ghost-btn">Delete Embedding</button>
            </div>
            <p class="helper-text">Use this to confirm /v1/embeddings routing from the Wails UI. auto resolves to the configured embedding alias.</p>
          </article>
          <article class="panel">
            <div class="panel-head">
              <h2>Embedding Result</h2>
            </div>
            <div class="actions">
              <button id="export-embedding" class="ghost-btn">Export Markdown</button>
            </div>
            <div id="embedding-output" class="runtime-result"></div>
          </article>
        </div>
        <div class="grid two top-gap">
          <article class="panel">
            <div class="panel-head">
              <h2>Index Browser</h2>
            </div>
            <div class="grid two">
              <label class="field">
                <span>Project Filter</span>
                <input id="index-project" class="text-input" placeholder="lab" />
              </label>
              <label class="field">
                <span>Chunk Limit</span>
                <input id="index-limit" class="text-input" type="number" value="20" min="1" max="200" />
              </label>
            </div>
            <label class="field">
              <span>Source Path Contains</span>
              <input id="index-source-query" class="text-input" placeholder="notes.md" />
            </label>
            <div class="grid two">
              <label class="field">
                <span>Index Save Name</span>
                <input id="index-save-name" class="text-input" placeholder="index-lab-notes" />
              </label>
              <label class="field">
                <span>Saved Index</span>
                <select id="index-history-select" class="text-input">
                  <option value="">Select saved index</option>
                </select>
              </label>
            </div>
            <div class="actions">
              <button id="browse-index" class="primary-btn">Browse Index</button>
              <button id="save-index-request" class="ghost-btn">Save Index</button>
              <button id="load-index-request" class="ghost-btn">Load Index</button>
              <button id="delete-index-request" class="ghost-btn">Delete Index</button>
            </div>
            <p class="helper-text">Inspect ingested sources and chunk previews after running ingest, without leaving the Wails app.</p>
          </article>
          <article class="panel">
            <div class="panel-head">
              <h2>Index Contents</h2>
            </div>
            <div class="actions">
              <button id="export-index-summary" class="ghost-btn">Export Summary</button>
            </div>
            <div id="index-output" class="runtime-result"></div>
          </article>
        </div>
      </section>
      <section class="tab" data-tab-panel="eval">
        <div class="grid two unequal">
          <article class="panel">
            <div class="panel-head">
              <h2>Eval Runner</h2>
            </div>
            <label class="field">
              <span>Dataset Path</span>
              <input id="eval-dataset" class="text-input" value="configs/eval.sample.yaml" />
            </label>
            <label class="field">
              <span>Project</span>
              <input id="eval-project" class="text-input" />
            </label>
            <label class="field">
              <span>Source Path Filter</span>
              <input id="eval-source-path" class="text-input" placeholder="/absolute/path/to/source.md" />
            </label>
            <label class="field">
              <span>Top K</span>
              <input id="eval-top-k" class="text-input" type="number" value="5" min="1" />
            </label>
            <label class="check-field">
              <input id="eval-with-answer" type="checkbox" />
              <span>Run answer generation too</span>
            </label>
            <div class="grid two">
              <label class="field">
                <span>Eval Save Name</span>
                <input id="eval-save-name" class="text-input" placeholder="eval-npo-smoke" />
              </label>
              <label class="field">
                <span>Saved Eval</span>
                <select id="eval-history-select" class="text-input">
                  <option value="">Select saved eval</option>
                </select>
              </label>
            </div>
            <div class="actions">
              <button id="run-eval" class="primary-btn">Run Eval</button>
              <button id="save-eval-request" class="ghost-btn">Save Eval</button>
              <button id="load-eval-request" class="ghost-btn">Load Eval</button>
              <button id="delete-eval-request" class="ghost-btn">Delete Eval</button>
              <button id="clear-eval-source-path" class="ghost-btn">Clear Source Filter</button>
            </div>
            <p class="helper-text">Search-only eval works without an LLM backend. Enable answer generation only when the target model is running.</p>
          </article>
          <article class="panel">
            <div class="panel-head">
              <h2>Eval Report</h2>
            </div>
            <div class="actions">
              <button id="export-eval" class="ghost-btn">Export Markdown</button>
            </div>
            <div id="eval-output" class="runtime-result"></div>
          </article>
        </div>
        <article class="panel preference-panel">
          <div class="panel-head preference-panel-head">
            <div>
              <span class="eyebrow dark">Conversation quality</span>
              <h2>Preference A/B</h2>
            </div>
            <div id="preference-progress" class="preference-progress">Sessionを開始してください．</div>
          </div>
          <div class="preference-session-controls">
            <label class="field preference-dataset-field">
              <span>Dataset</span>
              <input id="preference-dataset" class="text-input" value="configs/eval.preference.v3.yaml" />
            </label>
            <label class="field">
              <span>Comparison</span>
              <select id="preference-comparison" class="text-input">
                <option value="base_vs_adapter">Base vs selected LoRA</option>
                <option value="prompt_v2_v3">Prompt v2 vs v3</option>
                <option value="prompt_v1_v2">Prompt v1 vs v2</option>
                <option value="same_prompt">Same prompt sampling</option>
              </select>
            </label>
            <label class="field">
              <span>Model role</span>
              <select id="preference-role" class="text-input">
                <option value="fast">Fast</option>
                <option value="work">Work</option>
                <option value="code">Code</option>
              </select>
            </label>
            <label class="field">
              <span>Pairs</span>
              <input id="preference-count" class="text-input" type="number" min="1" max="100" value="11" />
            </label>
            <label class="field">
              <span>LoRA scale</span>
              <input id="preference-adapter-scale" class="text-input" type="number" min="0.1" max="100" step="0.1" value="1" />
            </label>
            <label class="field">
              <span>Resume session</span>
              <select id="preference-session-select" class="text-input">
                <option value="">Select session</option>
              </select>
            </label>
          </div>
          <div class="actions">
            <button id="preference-start" class="primary-btn">Start A/B session</button>
            <button id="preference-resume" class="ghost-btn">Resume</button>
          </div>
          <p class="helper-text">Base／LoRA比較はModel Managerで選択中のLoRAを同一seed，v3 prompt，validation／holdoutだけで比較します．候補の正体はsession完了まで表示しません．1／2／0／Sで選択，Enterで保存，Zで直前の投票を訂正できます．</p>
          <div id="preference-status" class="runtime-result"></div>
          <div id="preference-review" class="preference-review">
            <div class="preference-empty">Sessionを開始または再開すると，ここに会話と2候補が表示されます．</div>
          </div>
          <div class="preference-vote-controls">
            <div class="preference-choice-row" role="group" aria-label="Preference selection">
              <button class="ghost-btn preference-choice" data-preference-choice="left">左 · 1</button>
              <button class="ghost-btn preference-choice" data-preference-choice="right">右 · 2</button>
              <button class="ghost-btn preference-choice" data-preference-choice="tie">同程度 · 0</button>
              <button class="ghost-btn preference-choice" data-preference-choice="skip">判断不能 · S</button>
            </div>
            <details class="preference-details">
              <summary>理由タグとメモを追加</summary>
              <div id="preference-reason-tags" class="preference-reason-tags">
                ${[
                  'direct', 'natural_japanese', 'friendly_polite', 'good_distance',
                  'contextual', 'concise', 'good_question', 'unnecessary_question',
                  'too_formal', 'too_casual', 'too_long', 'generic_preamble',
                  'excessive_agreement', 'persona_break', 'factual_problem', 'other',
                ].map((tag) => `<label><input type="checkbox" value="${tag}" />${tag}</label>`).join('')}
              </div>
              <label class="field">
                <span>Optional note</span>
                <textarea id="preference-note" class="text-area" rows="3" maxlength="2000"></textarea>
              </label>
              <label class="check-field">
                <input id="preference-sft-approval" type="checkbox" />
                <span>選んだ応答をSFT用として明示承認する</span>
              </label>
            </details>
            <div class="actions">
              <button id="preference-submit" class="primary-btn" disabled>Submit vote · Enter</button>
              <button id="preference-correct" class="ghost-btn" disabled>Correct previous · Z</button>
            </div>
          </div>
          <div class="preference-lower-grid">
            <section>
              <h3>Session stats</h3>
              <div id="preference-stats" class="runtime-result"></div>
            </section>
            <section>
              <h3>Training export</h3>
              <label class="field">
                <span>DPO JSONL path</span>
                <input id="preference-dpo-output" class="text-input" value="exports/ephy-preference.dpo.jsonl" />
              </label>
              <label class="field">
                <span>SFT JSONL path</span>
                <input id="preference-sft-output" class="text-input" value="exports/ephy-preference.sft.jsonl" />
              </label>
              <div class="actions">
                <button id="preference-export-dpo" class="ghost-btn">Export DPO</button>
                <button id="preference-export-sft" class="ghost-btn">Export SFT</button>
              </div>
              <p class="helper-text">出力先はEPHY_PREFERENCE_DATA_ROOT配下に限定されます．既存ファイルは上書きしません．</p>
            </section>
          </div>
        </article>
      </section>
      <section class="tab" data-tab-panel="settings">
        <div class="settings-hub">
          <article class="panel" id="developer-model-manager"></article>
          <article class="panel">
            <div class="panel-head">
              <h2>Settings</h2>
            </div>
            <p class="helper-text">Runtime, evaluation, logs, and routing tools are grouped here so Chat remains the default workspace.</p>
            <div class="settings-shortcut-grid">
              <button id="settings-open-runtime" class="settings-shortcut-card">
                <span class="eyebrow dark">Advanced</span>
                <strong>Runtime</strong>
                <span>Servers, models, stack control, and logs.</span>
              </button>
              <button id="settings-open-routing" class="settings-shortcut-card">
                <span class="eyebrow dark">Advanced</span>
                <strong>Routing</strong>
                <span>Inspect route decisions and backend model selection.</span>
              </button>
              <button id="settings-open-eval" class="settings-shortcut-card">
                <span class="eyebrow dark">Advanced</span>
                <strong>Evaluation</strong>
                <span>Run eval datasets, compare metrics, and inspect regressions.</span>
              </button>
              <button id="settings-open-dashboard" class="settings-shortcut-card">
                <span class="eyebrow dark">Status</span>
                <strong>Dashboard</strong>
                <span>Open recent activity, presets, and workflow summary.</span>
              </button>
            </div>
          </article>
        </div>
      </section>
    </main>
    <dialog id="web-search-consent" class="web-search-consent">
      <form method="dialog" class="web-search-consent-card">
        <div class="eyebrow dark">External Search Gate</div>
        <h2 id="web-search-consent-title">Review outbound search</h2>
        <p id="web-search-consent-message"></p>
        <div class="web-search-query-block">
          <span>Exact query sent outside this Mac</span>
          <code id="web-search-outbound-query"></code>
        </div>
        <div id="web-search-risk-list" class="web-search-risk-list"></div>
        <div class="actions web-search-consent-actions">
          <button id="web-search-confirm" class="primary-btn" value="search" type="submit">Search Web</button>
          <button class="ghost-btn" value="local" type="submit">Continue Locally</button>
          <button class="ghost-btn" value="cancel" type="submit">Cancel</button>
        </div>
      </form>
    </dialog>
  </div>
`;

mountModelManager(document.getElementById('developer-model-manager'), {
  GetLocalModelCatalog, SetDeveloperMode, ImportLocalModel, ApplyLocalModel,
});

document.getElementById('start-conversation').addEventListener('click', async () => {
  const button = document.getElementById('start-conversation');
  button.disabled = true;
  button.textContent = '起動中…';
  document.getElementById('conversation-start-status').textContent = '';
  try {
    const result = await RunRuntimeStackAction({action: 'start_conversation'});
    if (result.status !== 'ok') throw new Error(result.detail);
    document.getElementById('chat-mode').value = 'fast';
    button.textContent = '会話できます';
    await refreshRuntime();
  } catch (error) {
    button.textContent = '起動を再試行';
    button.title = String(error);
    document.getElementById('conversation-start-status').textContent = String(error);
    setOutput('runtime-config-status', String(error));
  } finally { button.disabled = false; }
});

function bindTabs() {
  document.querySelectorAll('.nav-btn').forEach((button) => {
    button.addEventListener('click', () => {
      const tab = button.dataset.tab;
      if (tab === 'chat') {
        startNewChat();
        return;
      }
      activateTab(tab);
      if (tab === 'chat-history') {
        document.getElementById('chat-history-select')?.focus();
      }
    });
  });
}

function getContentScroller() {
  return document.querySelector('.content');
}

function saveActivePanelScroll() {
  const content = getContentScroller();
  if (!content || !activePanelTab) {
    return;
  }
  tabScrollPositions.set(activePanelTab, content.scrollTop);
}

function restorePanelScroll(panelTab) {
  const content = getContentScroller();
  if (!content) {
    return;
  }
  const nextScrollTop = tabScrollPositions.get(panelTab) || 0;
  content.scrollTop = nextScrollTop;
}

function activateTab(tab) {
  saveActivePanelScroll();
  const visibleTab = resolveVisibleTab(tab);
  const panelTab = resolvePanelTab(tab);
  document.querySelectorAll('.nav-btn').forEach((item) => item.classList.toggle('active', item.dataset.tab === visibleTab));
  document.querySelectorAll('.tab').forEach((panel) => {
    panel.classList.toggle('active', panel.dataset.tabPanel === panelTab);
  });
  activePanelTab = panelTab;
  restorePanelScroll(panelTab);
}

function resolveVisibleTab(tab) {
  if (tab === 'runtime' || tab === 'router' || tab === 'eval') {
    return 'settings';
  }
  return tab;
}

function resolvePanelTab(tab) {
  if (tab === 'chat-history') {
    return 'chat';
  }
  return tab;
}

function setOutput(id, value) {
  document.getElementById(id).textContent = typeof value === 'string' ? value : JSON.stringify(value, null, 2);
}

function setChatDropStatus(message = '') {
  const status = document.getElementById('chat-drop-status');
  if (!status) {
    return;
  }
  status.textContent = message;
  status.classList.toggle('visible', Boolean(String(message || '').trim()));
}

function setChatWebStatus(message = 'Local only', tone = '') {
  const status = document.getElementById('chat-web-status');
  if (!status) {
    return;
  }
  status.textContent = message;
  status.dataset.tone = tone;
}

function showWebSearchConsent(plan) {
  const dialog = document.getElementById('web-search-consent');
  const title = document.getElementById('web-search-consent-title');
  const message = document.getElementById('web-search-consent-message');
  const query = document.getElementById('web-search-outbound-query');
  const risks = document.getElementById('web-search-risk-list');
  const confirm = document.getElementById('web-search-confirm');
  const blocked = plan.decision === 'block';
  title.textContent = blocked ? 'Web search blocked' : 'Review outbound search';
  message.textContent = blocked
    ? 'The outbound policy blocked this search. No query has been sent outside this Mac.'
    : 'Sensitive information may have been removed. Review the exact query before it is sent to SearXNG.';
  query.textContent = plan.outbound_query || '(no outbound query)';
  risks.innerHTML = (plan.risk_categories || []).map((category) => (
    `<span class="runtime-pill ${blocked ? 'required' : 'neutral'}">${escapeHtml(category)}</span>`
  )).join('') || '<span class="runtime-pill neutral">no detected risk</span>';
  confirm.hidden = blocked;

  return new Promise((resolve) => {
    const handleClose = () => {
      dialog.removeEventListener('close', handleClose);
      resolve(dialog.returnValue || 'cancel');
    };
    dialog.addEventListener('close', handleClose);
    dialog.showModal();
  });
}

async function prepareWebSearch(prompt) {
  const toggle = document.getElementById('chat-web-search');
  const result = await prepareWebSearchRequest({
    enabled: Boolean(toggle?.checked),
    prompt,
    planSearch: PlanWebSearch,
    approvePlan: ApproveWebSearch,
    reviewPlan: showWebSearchConsent,
  });
  if (result?.web_search) {
    setChatWebStatus('Web ready', 'active');
  } else if (result) {
    setChatWebStatus('Local only', '');
  }
  return result;
}

async function refreshWebSearchCapability() {
  const toggle = document.getElementById('chat-web-search');
  if (!toggle) {
    return;
  }
  try {
    const health = await Health();
    webSearchAvailable = Boolean(health?.web_search_enabled);
    karteAvailable = Boolean(health?.karte_enabled);
  } catch (_error) {
    webSearchAvailable = false;
    karteAvailable = false;
  }
  toggle.disabled = !webSearchAvailable;
  toggle.checked = webSearchAvailable && toggle.checked;
  toggle.closest('.chat-web-toggle')?.classList.toggle('disabled', !webSearchAvailable);
  toggle.closest('.chat-web-toggle')?.setAttribute(
    'title',
    webSearchAvailable
      ? 'Search through local SearXNG; the sanitized query is sent to the configured upstream engine.'
      : 'Web search is disabled. Run setup_searxng.sh and enable configs/web.local.yaml.',
  );
  setChatWebStatus(webSearchAvailable ? 'Local only' : 'Web unavailable', webSearchAvailable ? '' : 'warning');
}

async function refreshKarteCapability() {
  try {
    const health = await Health();
    karteAvailable = Boolean(health?.karte_enabled);
  } catch (_error) {
    karteAvailable = false;
  }
}

function setSidebarCollapsed(collapsed) {
  sidebarCollapsed = collapsed;
  const shell = document.querySelector('.shell');
  const toggle = document.getElementById('sidebar-toggle');
  const reveal = document.getElementById('sidebar-reveal');
  if (!shell || !toggle || !reveal) {
    return;
  }
  shell.classList.toggle('sidebar-collapsed', collapsed);
  toggle.textContent = collapsed ? 'Show' : 'Hide';
  toggle.setAttribute('aria-label', collapsed ? 'Show sidebar' : 'Hide sidebar');
  reveal.classList.toggle('hidden', !collapsed);
}

function ensureSelectHasOption(select, value, label) {
  if (!select || !value) {
    return;
  }
  if ([...select.options].some((option) => option.value === value)) {
    return;
  }
  const option = document.createElement('option');
  option.value = value;
  option.textContent = label;
  select.appendChild(option);
}

function closeChatToolbarMenus(exceptId = '') {
  ['chat-more-menu'].forEach((id) => {
    if (id !== exceptId) {
      document.getElementById(id)?.removeAttribute('open');
    }
  });
}

function setActiveIngestDropZone(zoneId = '') {
  activeIngestDropZone = zoneId;
  document.querySelectorAll('[data-drop-ingest-zone]').forEach((element) => {
    const active = zoneId && element.dataset.dropIngestZone === zoneId;
    element.classList.toggle('drop-ingest-zone-active', active);
  });
}

function isChatSubmitShortcut(event) {
  return event.key === 'Enter'
    && (event.metaKey || event.ctrlKey)
    && !event.shiftKey
    && !event.altKey
    && !event.isComposing;
}

function escapeHtml(value) {
  return String(value)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function renderTagList(items, emptyLabel, tone = 'neutral') {
  if (!items || items.length === 0) {
    return `<span class="runtime-pill ${tone}">${escapeHtml(emptyLabel)}</span>`;
  }
  return items.map((item) => `<span class="runtime-pill ${tone}">${escapeHtml(item)}</span>`).join('');
}

function renderRuntimeSummary(runtime) {
  const container = document.getElementById('runtime-config-summary');
  const summary = runtime.config_summary || {};
  const warnings = runtime.warnings || [];
  const requiredServices = runtime.required_services || [];
  const optionalServices = runtime.optional_services || [];

  container.innerHTML = `
    <div class="runtime-summary-grid">
      <div class="runtime-summary-card">
        <div class="runtime-summary-title">Providers</div>
        <div class="runtime-summary-line"><span>Embedding</span><strong>${escapeHtml(summary.embedding_provider || '-')}</strong></div>
        <div class="runtime-summary-line"><span>Reranker</span><strong>${escapeHtml(summary.reranker_provider || '-')}</strong></div>
        <div class="runtime-summary-line"><span>Vector DB</span><strong>${escapeHtml(summary.vector_db_provider || '-')}</strong></div>
      </div>
      <div class="runtime-summary-card">
        <div class="runtime-summary-title">Model Alias</div>
        <div class="runtime-summary-line"><span>Embedding</span><strong>${escapeHtml(summary.embedding_alias || '-')}</strong></div>
        <div class="runtime-summary-line"><span>Reranker</span><strong>${escapeHtml(summary.reranker_alias || '-')}</strong></div>
        <div class="runtime-summary-line"><span>Store</span><strong>${escapeHtml(summary.vector_db_store_path || '-')}</strong></div>
      </div>
    </div>
    <div class="runtime-summary-card">
      <div class="runtime-summary-title">Required Services</div>
      <div class="runtime-pill-row">${renderTagList(requiredServices, 'none', 'required')}</div>
    </div>
    <div class="runtime-summary-card">
      <div class="runtime-summary-title">Optional Services</div>
      <div class="runtime-pill-row">${renderTagList(optionalServices, 'none', 'optional')}</div>
    </div>
    <div class="runtime-summary-card">
      <div class="runtime-summary-title">Warnings</div>
      ${
        warnings.length === 0
          ? '<div class="runtime-summary-ok">No config warnings.</div>'
          : `<div class="runtime-warning-list">${warnings.map((warning) => `<div class="runtime-warning-item">${escapeHtml(warning)}</div>`).join('')}</div>`
      }
    </div>
  `;
}

function renderRuntimeMessage(id, message) {
  document.getElementById(id).innerHTML = `<div class="runtime-result-card"><div class="runtime-result-text">${escapeHtml(message)}</div></div>`;
}

function updateChatScopeSummary() {
  const project = document.getElementById('rag-project')?.value?.trim() || '(default)';
  const tags = parseTagList(document.getElementById('rag-tags')?.value || '');
  const sourcePath = document.getElementById('rag-source-path')?.value?.trim() || '';
  const topK = document.getElementById('rag-top-k')?.value || '5';
  const scopeSelect = document.getElementById('chat-source-scope-select');
  if (scopeSelect?.value) {
    chatSourceScope = scopeSelect.value;
  }
  const scopeLabelMap = {
    all: 'all',
    project: 'current project',
    selected_docs: 'selected docs',
  };
  const parts = [`scope=${scopeLabelMap[chatSourceScope] || 'all'}`, `project=${project}`, `top_k=${topK}`];
  if (tags.length > 0) {
    parts.push(`tags=${tags.join(', ')}`);
  }
  if (sourcePath) {
    parts.push(`source=${sourcePath}`);
  }
  const target = document.getElementById('chat-source-scope');
  if (target) {
    target.textContent = `Library scope: ${parts.join(' | ')}`;
  }
}

function syncChatContextBarFromRagState() {
  const project = document.getElementById('rag-project')?.value || '';
  const topK = String(document.getElementById('rag-top-k')?.value || '5');
  const sourcePath = document.getElementById('rag-source-path')?.value?.trim() || '';
  const projectSelect = document.getElementById('chat-project-select');
  const topKSelect = document.getElementById('chat-top-k-select');
  const scopeSelect = document.getElementById('chat-source-scope-select');
  if (projectSelect) {
    ensureSelectHasOption(projectSelect, project, project || 'All projects');
    projectSelect.value = project;
  }
  if (topKSelect) {
    ensureSelectHasOption(topKSelect, topK, topK);
    topKSelect.value = topK;
  }
  if (scopeSelect) {
    if (sourcePath) {
      chatSourceScope = 'selected_docs';
    } else if (project) {
      chatSourceScope = 'project';
    } else {
      chatSourceScope = 'all';
    }
    scopeSelect.value = chatSourceScope;
  }
}

function applyChatContextScope() {
  const projectSelect = document.getElementById('chat-project-select');
  const scopeSelect = document.getElementById('chat-source-scope-select');
  const topKSelect = document.getElementById('chat-top-k-select');
  const sourcePathInput = document.getElementById('rag-source-path');
  const projectInput = document.getElementById('rag-project');
  const topKInput = document.getElementById('rag-top-k');
  const selectedProject = projectSelect?.value || '';
  chatSourceScope = scopeSelect?.value || 'all';
  if (projectInput) {
    projectInput.value = chatSourceScope === 'all' ? '' : selectedProject;
  }
  if (sourcePathInput && chatSourceScope !== 'selected_docs') {
    sourcePathInput.value = '';
  }
  if (topKInput && topKSelect) {
    topKInput.value = topKSelect.value || '5';
  }
  updateChatScopeSummary();
}

function buildChatGroundingPayload() {
  const sourceScope = document.getElementById('chat-source-scope-select')?.value || 'all';
  const project = document.getElementById('rag-project')?.value?.trim() || '';
  const sourcePath = document.getElementById('rag-source-path')?.value?.trim() || '';
  const topK = getPositiveInt('rag-top-k', 5);
  const tags = parseTagList(document.getElementById('rag-tags')?.value || '');
  return {
    project,
    source_path: sourcePath,
    source_scope: sourceScope,
    top_k: topK,
    tags,
  };
}

function refreshChatContextProjectOptions(presets = []) {
  const select = document.getElementById('chat-project-select');
  if (!select) {
    return;
  }
  const currentValue = select.value;
  const projects = new Set(['']);
  presets.forEach((preset) => {
    [preset.watch_project, preset.ingest_project, preset.rag_project, preset.eval_project].forEach((value) => {
      const normalized = String(value || '').trim();
      if (normalized) {
        projects.add(normalized);
      }
    });
  });
  const ragProject = document.getElementById('rag-project')?.value?.trim() || '';
  if (ragProject) {
    projects.add(ragProject);
  }
  select.innerHTML = '<option value="">All projects</option>';
  [...projects]
    .filter((value) => value)
    .sort((left, right) => left.localeCompare(right))
    .forEach((project) => {
      const option = document.createElement('option');
      option.value = project;
      option.textContent = project;
      select.appendChild(option);
    });
  if ([...select.options].some((option) => option.value === currentValue)) {
    select.value = currentValue;
  } else if ([...select.options].some((option) => option.value === ragProject)) {
    select.value = ragProject;
  } else {
    select.value = '';
  }
  syncChatContextBarFromRagState();
}

async function renameCurrentChatFromInputs() {
  const selectedName = document.getElementById('chat-history-select').value.trim();
  const nextName = document.getElementById('chat-save-name').value.trim();
  if (!selectedName) {
    renderRuntimeMessage('chat-output', 'Select a saved chat to rename.');
    return;
  }
  if (!nextName) {
    renderRuntimeMessage('chat-output', 'Rename target is empty.');
    return;
  }
  const requests = await GetSavedRequests();
  const item = requests.find((request) => request.kind === 'chat' && request.name === selectedName);
  if (!item) {
    throw new Error(`Saved chat not found: ${selectedName}`);
  }
  await SaveRequest({
    name: nextName,
    kind: 'chat',
    mode: item.mode,
    prompt: item.prompt,
  });
  await DeleteSavedRequest({name: selectedName, kind: 'chat'});
  await refreshSavedRequests();
  document.getElementById('chat-history-select').value = nextName;
  document.getElementById('chat-save-name').value = nextName;
}

function createChatRequestId() {
  return `chat-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

function createKarteConversationId() {
  const randomPart = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
  return `conversation-${randomPart}`;
}

function getChatModeLabel(mode) {
  const option = [...(document.getElementById('chat-mode')?.options || [])].find((item) => item.value === mode);
  return option?.textContent || mode || 'Auto';
}

function getChatMaxTokens(mode) {
  return CHAT_MODE_MAX_TOKENS[mode] || CHAT_MODE_MAX_TOKENS.auto;
}

function isLengthLimitedFinishReason(reason) {
  return String(reason || '').trim().toLowerCase() === 'length';
}

function buildContinuationPrompt(entry) {
  return [
    'The previous assistant response was cut off because it reached the output token limit.',
    'Continue the assistant response from exactly where it stopped.',
    'Do not repeat the already generated text.',
    'Do not restart the answer.',
    'Output only the continuation.',
    '',
    'Original user prompt:',
    entry.prompt || '',
    '',
    'Assistant response so far:',
    entry.text || '',
  ].join('\n');
}

function setChatSendState(inFlight) {
  chatSendInFlight = inFlight;
  const sendButton = document.getElementById('send-chat');
  const promptInput = document.getElementById('chat-prompt');
  if (sendButton) {
    sendButton.disabled = inFlight;
    sendButton.textContent = inFlight ? 'Streaming...' : 'Send';
  }
  if (promptInput) {
    promptInput.dataset.streaming = inFlight ? 'true' : 'false';
  }
}

function startNewChat() {
  chatThreadEntries = [];
  chatConversationId = createKarteConversationId();
  chatOccurredAt = formatLocalISOString();
  latestChatSources = [];
  activeChatSourceIndex = 0;
  latestRouteInspectorState = null;
  activeChatStreamRequestId = '';
  chatSourceScope = 'all';
  document.getElementById('chat-prompt').value = '';
  document.getElementById('chat-save-name').value = '';
  document.getElementById('chat-history-select').value = '';
  document.getElementById('chat-mode').value = 'fast';
  document.getElementById('chat-web-search').checked = false;
  document.getElementById('rag-project').value = '';
  document.getElementById('rag-source-path').value = '';
  document.getElementById('rag-top-k').value = '5';
  activateTab('chat');
  renderChatThread();
  renderChatSourcesPane({sources: [], title: 'Sources'});
  renderRouteInspectorCard(null);
  document.getElementById('chat-more-menu')?.removeAttribute('open');
  syncChatContextBarFromRagState();
  updateChatScopeSummary();
  setChatSendState(false);
  setChatDropStatus('');
  setChatWebStatus(webSearchAvailable ? 'Local only' : 'Web unavailable', webSearchAvailable ? '' : 'warning');
  document.getElementById('chat-prompt')?.focus();
}

function renderChatThread() {
  const container = document.getElementById('chat-output');
  if (!container) {
    return;
  }
  if (chatThreadEntries.length === 0) {
    container.innerHTML = `
      <div class="empty-chat-state">
        <div class="eyebrow dark">Chat-first Workspace</div>
        <h3>Start a new conversation.</h3>
        <p>Use Quick, Deep Work, With Sources, or Code modes. Source-backed answers will appear in the right pane.</p>
      </div>
    `;
    return;
  }

  container.innerHTML = chatThreadEntries.map((entry) => `
    <article class="message-card message-${escapeHtml(entry.role)} ${entry.streaming ? 'message-streaming' : ''}">
      <div class="message-head">
        <span class="message-role">${escapeHtml(entry.label)}</span>
        ${entry.meta ? `<span class="message-meta">${escapeHtml(entry.meta)}</span>` : ''}
      </div>
      ${
        entry.thinking
          ? `
            <details class="message-thinking" ${entry.streaming ? 'open' : ''}>
              <summary>Thinking</summary>
              <div class="message-thinking-body">${escapeHtml(entry.thinking)}</div>
            </details>
          `
          : ''
      }
      <div class="message-body">${escapeHtml(entry.text || (entry.streaming ? '…' : ''))}</div>
      ${
        entry.role === 'assistant' && !entry.streaming
          ? `
            <div class="message-actions">
              ${entry.canContinue ? `
                <button
                  class="ghost-btn compact-btn"
                  type="button"
                  data-chat-action="continue"
                  data-request-id="${escapeHtml(entry.requestId || '')}"
                >Continue</button>
              ` : ''}
              ${entry.karteMemory ? '' : `
                <button
                  class="ghost-btn compact-btn"
                  type="button"
                  data-karte-action="plan"
                  data-request-id="${escapeHtml(entry.requestId || '')}"
                >Karteにまとめる</button>
              `}
            </div>
          `
          : ''
      }
      ${entry.role === 'assistant' ? renderKarteConversationCard(entry.karteMemory, entry.requestId || '') : ''}
    </article>
  `).join('');
  container.scrollTop = container.scrollHeight;
}

function appendChatThreadEntry(entry) {
  chatThreadEntries.push(entry);
  if (chatThreadEntries.length > 30) {
    chatThreadEntries = chatThreadEntries.slice(-30);
  }
  renderChatThread();
}

function updateChatThreadEntry(requestId, updater) {
  const index = chatThreadEntries.findIndex((entry) => entry.requestId === requestId);
  if (index < 0) {
    return;
  }
  const current = chatThreadEntries[index];
  chatThreadEntries[index] = updater({...current});
  renderChatThread();
}

function syncLatestChatExportFromEntry(entry) {
  if (!entry || entry.role !== 'assistant') {
    return;
  }
  latestChatExport = {
    kind: 'chat',
    title: `Chat Response (${entry.mode || 'auto'})`,
    content: `## Answer\n\n${entry.text || ''}`,
    fileStem: `chat-${entry.mode || 'auto'}`,
  };
}

function beginStreamingChat({requestId, prompt, modeLabel}) {
  activeChatStreamRequestId = requestId;
  appendChatThreadEntry({
    role: 'user',
    label: 'You',
    meta: modeLabel,
    text: prompt,
  });
  appendChatThreadEntry({
    requestId,
    role: 'assistant',
    label: 'Assistant',
    meta: `${modeLabel} · streaming`,
    text: '',
    thinking: '',
    streaming: true,
    canContinue: false,
    finishReason: '',
    mode: document.getElementById('chat-mode').value,
    prompt,
  });
  renderChatSourcesPane({sources: [], title: 'Sources'});
}

function beginStreamingContinuation({targetRequestId, requestId, mode, modeLabel}) {
  activeChatStreamRequestId = requestId;
  updateChatThreadEntry(targetRequestId, (entry) => {
    entry.requestId = requestId;
    entry.streaming = true;
    entry.meta = `${modeLabel} · continuing`;
    entry.canContinue = false;
    entry.finishReason = '';
    entry.mode = mode;
    return entry;
  });
}

function applyChatStreamDelta({requestId, channel, delta}) {
  updateChatThreadEntry(requestId, (entry) => {
    if (channel === 'thinking') {
      entry.thinking = `${entry.thinking || ''}${delta || ''}`;
    } else {
      entry.text = `${entry.text || ''}${delta || ''}`;
    }
    return entry;
  });
}

function finalizeStreamingChat({requestId, meta, answer, thinking, finishReason}) {
  updateChatThreadEntry(requestId, (entry) => {
    entry.streaming = false;
    entry.meta = meta || entry.meta;
    if (thinking && !(entry.thinking || '').trim()) {
      entry.thinking = thinking;
    }
    if (answer && !(entry.text || '').trim()) {
      entry.text = answer;
    }
    entry.finishReason = finishReason || entry.finishReason || '';
    entry.canContinue = isLengthLimitedFinishReason(entry.finishReason) && Boolean((entry.text || '').trim());
    syncLatestChatExportFromEntry(entry);
    return entry;
  });
}

function failStreamingChat({requestId, message}) {
  updateChatThreadEntry(requestId, (entry) => {
    entry.streaming = false;
    entry.meta = 'error';
    entry.canContinue = false;
    entry.finishReason = '';
    if (!(entry.text || '').trim()) {
      entry.text = message || 'Streaming request failed.';
    }
    return entry;
  });
}

function chatEntriesThrough(requestId) {
  const index = chatThreadEntries.findIndex((entry) => entry.requestId === requestId);
  return index < 0 ? [] : chatThreadEntries.slice(0, index + 1);
}

function defaultKarteProject() {
  return document.getElementById('chat-project-select')?.value?.trim()
    || document.getElementById('rag-project')?.value?.trim()
    || '';
}

function buildKarteRequest(requestId, overrides = {}) {
  const current = chatThreadEntries.find((entry) => entry.requestId === requestId)?.karteMemory || {};
  return buildKarteConversationRequest({
    conversationId: chatConversationId,
    occurredAt: chatOccurredAt,
    entries: chatEntriesThrough(requestId),
    project: overrides.project ?? current.project ?? defaultKarteProject(),
    kind: overrides.kind ?? current.kind ?? '',
    sensitivity: 'internal',
    tags: parseTagList(document.getElementById('rag-tags')?.value || ''),
    resolution: overrides.resolution ?? current.resolution ?? 'auto',
    intendedDocId: overrides.intendedDocId ?? current.intendedDocId ?? '',
  });
}

function setKarteMemory(requestId, updater) {
  updateChatThreadEntry(requestId, (entry) => {
    entry.karteMemory = typeof updater === 'function' ? updater(entry.karteMemory || {}) : updater;
    return entry;
  });
}

async function planKarteConversation(requestId, overrides = {}) {
  let request;
  try {
    request = buildKarteRequest(requestId, overrides);
  } catch (error) {
    setKarteMemory(requestId, (current) => ({...current, ...overrides, state: 'error', error: String(error)}));
    return null;
  }
  setKarteMemory(requestId, (current) => ({...current, ...overrides, state: 'planning', dismissed: false}));
  try {
    const plan = await PlanKarteConversation(request);
    setKarteMemory(requestId, (current) => ({
      ...current,
      ...overrides,
      state: plan.publishable ? 'ready' : 'consultation',
      plan,
      error: '',
      project: request.project || '',
      kind: request.kind || '',
      resolution: request.resolution || 'auto',
      intendedDocId: request.intended_doc_id || '',
    }));
    return plan;
  } catch (error) {
    setKarteMemory(requestId, (current) => ({...current, ...overrides, state: 'error', error: String(error)}));
    return null;
  }
}

async function autoPlanKarteConversation(requestId) {
  if (!karteAvailable) return null;
  return planKarteConversation(requestId);
}

function readKarteResolution(requestId) {
  const card = document.querySelector(`[data-karte-card="${requestId}"]`);
  if (!card) return {};
  return {
    project: card.querySelector('[data-karte-field="project"]')?.value?.trim() || '',
    kind: card.querySelector('[data-karte-field="kind"]')?.value || '',
    resolution: card.querySelector('[data-karte-field="resolution"]')?.value || 'auto',
    intendedDocId: card.querySelector('[data-karte-field="intended-doc-id"]')?.value || '',
  };
}

async function publishKarteConversation(requestId) {
  const overrides = readKarteResolution(requestId);
  let request;
  try {
    request = buildKarteRequest(requestId, overrides);
  } catch (error) {
    setKarteMemory(requestId, (current) => ({...current, state: 'error', error: String(error)}));
    return;
  }
  setKarteMemory(requestId, (current) => ({...current, ...overrides, state: 'planning'}));
  try {
    const latestPlan = await PlanKarteConversation(request);
    if (!latestPlan.publishable) {
      setKarteMemory(requestId, (current) => ({...current, ...overrides, state: 'consultation', plan: latestPlan}));
      return;
    }
    const published = await PublishKarteConversation(request);
    setKarteMemory(requestId, (current) => ({
      ...current,
      ...overrides,
      state: published.state,
      plan: published.plan,
      candidateId: published.candidate_id,
      error: '',
    }));
  } catch (error) {
    setKarteMemory(requestId, (current) => ({...current, ...overrides, state: 'error', error: String(error)}));
  }
}

async function refreshKarteProposal(requestId) {
  const entry = chatThreadEntries.find((item) => item.requestId === requestId);
  const candidateId = entry?.karteMemory?.candidateId || entry?.karteMemory?.plan?.candidate_id || '';
  if (!candidateId) return;
  try {
    const status = await GetKarteProposalStatus(candidateId);
    setKarteMemory(requestId, (current) => ({...current, state: status.state, receipt: status.receipt || null}));
  } catch (error) {
    setKarteMemory(requestId, (current) => ({...current, state: 'error', error: String(error)}));
  }
}

async function handleKarteConversationAction(button) {
  const requestId = button.dataset.requestId || '';
  if (!requestId) return;
  const action = button.dataset.karteAction;
  if (action === 'dismiss') {
    setKarteMemory(requestId, (current) => ({...current, dismissed: true}));
    return;
  }
  if (action === 'publish') {
    await publishKarteConversation(requestId);
    return;
  }
  if (action === 'refresh') {
    await refreshKarteProposal(requestId);
    return;
  }
  await planKarteConversation(requestId, readKarteResolution(requestId));
}

function bindChatStreamEvents() {
  if (chatStreamListenerBound) {
    return;
  }
  chatStreamListenerBound = true;
  EventsOn('chat-stream', (payload) => {
    if (!payload || !payload.request_id || payload.request_id !== activeChatStreamRequestId) {
      return;
    }
    if (payload.kind === 'delta') {
      applyChatStreamDelta({
        requestId: payload.request_id,
        channel: payload.channel,
        delta: payload.delta,
      });
      return;
    }
    if (payload.kind === 'sources') {
      activeChatSourceIndex = 0;
      renderChatSourcesPane({sources: payload.sources || [], title: 'Used Sources'});
      return;
    }
    if (payload.kind === 'web_search_status') {
      const status = payload.web_search_status || {};
      if (status.status === 'completed') {
        setChatWebStatus(`Web · ${status.source_count || 0} sources`, 'active');
      } else {
        setChatWebStatus('Web unavailable', 'warning');
        setChatDropStatus(status.detail || 'Web search unavailable. Continuing with local context.');
      }
      return;
    }
    if (payload.kind === 'done') {
      finalizeStreamingChat({
        requestId: payload.request_id,
        answer: payload.answer,
        thinking: payload.thinking,
        finishReason: payload.finish_reason,
      });
      return;
    }
    if (payload.kind === 'error') {
      failStreamingChat({requestId: payload.request_id, message: payload.error});
    }
  });
}

function renderChatSourcePreview(index = 0) {
  const container = document.getElementById('chat-source-preview');
  if (!container) {
    return;
  }
  if (!latestChatSources.length) {
    container.innerHTML = '<div class="runtime-result-card"><div class="runtime-result-text">Select a source-backed answer to preview chunks and metadata.</div></div>';
    return;
  }

  activeChatSourceIndex = Math.max(0, Math.min(index, latestChatSources.length - 1));
  const source = latestChatSources[activeChatSourceIndex];
  const isWeb = source.source_type === 'web';
  container.innerHTML = `
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">${escapeHtml(isWeb ? (source.title || source.source_id || 'Web Source') : (source.heading_path?.slice(-1)?.[0] || source.source_path || 'Source Preview'))}</span>
        <span class="runtime-pill ${isWeb ? 'optional' : 'neutral'}">${escapeHtml(isWeb ? 'external untrusted' : (source.score != null ? source.score.toFixed(3) : (source.project || '-')))}</span>
      </div>
      ${isWeb ? `
        <div class="runtime-result-meta">${escapeHtml(source.source_id || '-')} · ${escapeHtml(source.url || '-')}</div>
        <div class="runtime-result-text">${escapeHtml(source.snippet || '')}</div>
        ${source.injection_suspected ? '<div class="web-source-warning">Potential instruction-like content was isolated from the answer model.</div>' : ''}
        <button class="ghost-btn compact-btn open-web-source" type="button" data-web-source-url="${escapeHtml(source.url || '')}">Open in Browser</button>
      ` : `
        <div class="runtime-result-meta">${escapeHtml(source.source_path || '-')}</div>
        <div class="runtime-result-meta">${escapeHtml((source.heading_path || []).join(' > ') || '(root)')}</div>
        <div class="runtime-result-meta">${escapeHtml(source.project || '(default)')} | ${escapeHtml((source.tags || []).join(', ') || '(no tags)')}</div>
        <div class="runtime-result-text">${escapeHtml(source.chunk_text || '')}</div>
      `}
    </div>
  `;
}

function renderChatSourcesPane({sources = [], title = 'Sources'} = {}) {
  latestChatSourceTitle = title;
  latestChatSources = Array.isArray(sources) ? sources : [];
  const meta = document.getElementById('chat-sources-meta');
  const list = document.getElementById('chat-source-list');
  if (!meta || !list) {
    return;
  }
  if (latestChatSources.length === 0) {
    meta.textContent = 'No source-backed answer yet.';
    list.innerHTML = '';
    renderChatSourcePreview(0);
    return;
  }

  meta.textContent = `${title} · ${latestChatSources.length} source${latestChatSources.length === 1 ? '' : 's'}`;
  list.innerHTML = latestChatSources.map((source, index) => `
    <button class="source-card ${index === activeChatSourceIndex ? 'active' : ''}" data-source-index="${index}">
      <div class="source-card-top">
        <strong>${escapeHtml(source.source_type === 'web' ? (source.title || source.source_id || `Web ${index + 1}`) : (source.heading_path?.slice(-1)?.[0] || `Source ${index + 1}`))}</strong>
        <span>${escapeHtml(source.source_type === 'web' ? 'WEB' : (source.score != null ? source.score.toFixed(3) : '-'))}</span>
      </div>
      <div class="source-card-path">${escapeHtml(source.source_type === 'web' ? (source.url || '-') : (source.source_path || '-'))}</div>
      <div class="source-card-meta">${escapeHtml(source.source_type === 'web' ? 'external_untrusted' : (source.project || '(default)'))}</div>
      <div class="source-card-heading">${escapeHtml(source.source_type === 'web' ? (source.snippet || '').slice(0, 120) : ((source.heading_path || []).join(' > ') || '(root)'))}</div>
    </button>
  `).join('');
  renderChatSourcePreview(activeChatSourceIndex);
}

function renderRouteInspectorCard(state) {
  latestRouteInspectorState = state || null;
  const container = document.getElementById('chat-route-output');
  if (!container) {
    return;
  }
  if (!state) {
    container.innerHTML = '<div class="runtime-result-card"><div class="runtime-result-text">Send a message to inspect route selection, backend model, and latency.</div></div>';
    return;
  }

  const items = [
    ['selected mode', state.selectedMode || '-'],
    ['backend model', state.backendModel || '-'],
    ['rag enabled', state.ragEnabled ? 'yes' : 'no'],
    ['source count', state.sourceCount != null ? String(state.sourceCount) : '-'],
    ['latency', state.latencyMs != null ? `${state.latencyMs} ms` : '-'],
    ['route reason', state.routeReason || '-'],
  ];
  container.innerHTML = `
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">Route Inspector</span>
        <span class="runtime-pill neutral">${escapeHtml(state.selectedMode || '-')}</span>
      </div>
      <div class="runtime-result-list">
        ${items.map(([label, value]) => `
          <div class="runtime-result-item compact-item">
            <div class="runtime-result-meta">${escapeHtml(label)}</div>
            <div class="runtime-result-text">${escapeHtml(value)}</div>
          </div>
        `).join('')}
      </div>
    </div>
  `;
}

function renderWorkflowResultIntoTarget(targetId, title, status, steps, {trackExport = false, fileStemPrefix = 'workflow'} = {}) {
  const container = document.getElementById(targetId);
  const tone = status === 'completed' ? 'optional' : status === 'running' ? 'neutral' : 'required';
  const exportPayload = {
    kind: 'workflow',
    title,
    content: [
      `- status: ${status}`,
      '',
      '## Steps',
      ...steps.map((step) => [
        `### ${step.name || '-'}`,
        `- status: ${step.status || '-'}`,
        '',
        step.detail || '',
      ].join('\n')),
    ].join('\n\n'),
    fileStem: `${fileStemPrefix}-${title.toLowerCase().replaceAll(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') || 'result'}`,
  };
  if (trackExport) {
    latestWorkflowExport = exportPayload;
  }
  container.innerHTML = `
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">${escapeHtml(title)}</span>
        <span class="runtime-pill ${tone}">${escapeHtml(status)}</span>
      </div>
      <div class="runtime-result-list">
        ${steps.map((step) => `
          <div class="runtime-result-item">
            <div class="runtime-result-head">
              <span class="runtime-result-name">${escapeHtml(step.name || '-')}</span>
              <span class="runtime-pill ${
                step.status === 'ok' ? 'optional' : step.status === 'running' ? 'neutral' : step.status === 'skipped' ? 'neutral' : 'required'
              }">${escapeHtml(step.status || '-')}</span>
            </div>
            <div class="runtime-result-text">${escapeHtml(step.detail || '')}</div>
          </div>
        `).join('')}
      </div>
    </div>
  `;
}

function renderWorkflowResult(title, status, steps) {
  renderWorkflowResultIntoTarget('runtime-stack-output', title, status, steps, {trackExport: true, fileStemPrefix: 'workflow'});
}

function renderWorkflowFollowupActions({title = 'Next Action', message = '', historyId = '', requestKind = '', requestName = ''} = {}) {
  const container = document.getElementById('runtime-stack-output');
  if (!container) {
    return;
  }
  container.innerHTML += `
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">${escapeHtml(title)}</span>
        <span class="runtime-pill optional">ready</span>
      </div>
      <div class="runtime-result-text">${escapeHtml(message || 'Follow-up action is available.')}</div>
      <div class="actions">
        ${
          historyId
            ? `<button class="primary-btn retry-original-after-recovery-btn" data-history-id="${escapeHtml(historyId)}">Retry Original Now</button>`
            : ''
        }
        ${
          requestKind && requestName
            ? `<button class="ghost-btn load-followup-request-btn" data-request-kind="${escapeHtml(requestKind)}" data-request-name="${escapeHtml(requestName)}">Load Failed Request</button>`
            : ''
        }
        <button class="ghost-btn export-current-workflow-btn">Export Current Workflow</button>
      </div>
    </div>
  `;
}

function renderExecutionHistory(items) {
  const container = document.getElementById('recent-activity');
  currentExecutionHistory = items || [];
  renderPresetCatalog(currentPresets);
  renderSelectedPresetWorkflowByName(
    document.getElementById('overview-preset-select').value
      || document.getElementById('preset-select').value
      || document.getElementById('preset-name').value.trim(),
  );
  renderWorkflowSummary(items || []);
  if (!items || items.length === 0) {
    container.innerHTML = '<div class="runtime-result-card"><div class="runtime-result-text">No recent activity.</div></div>';
    return;
  }
  container.innerHTML = items.map((item) => `
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">${escapeHtml(item.title || item.kind || '-')}</span>
        <span class="runtime-pill ${item.status === 'ok' ? 'optional' : item.status === 'error' ? 'required' : 'neutral'}">${escapeHtml(item.status || '-')}</span>
      </div>
      <div class="runtime-result-text">${escapeHtml(item.summary || '')}</div>
      <div class="runtime-result-meta">${escapeHtml(item.kind || '-')} | ${escapeHtml(item.timestamp || '-')}</div>
      ${item.detail ? `<div class="runtime-result-text">${escapeHtml(item.detail)}</div>` : ''}
      <div class="actions">
        ${item.payload ? `<button class="ghost-btn rerun-history-btn" data-history-id="${escapeHtml(item.id)}">Rerun</button>` : ''}
        ${item.payload ? `<button class="ghost-btn reuse-history-btn" data-history-id="${escapeHtml(item.id)}">Reuse</button>` : ''}
        <button class="ghost-btn export-history-btn" data-history-id="${escapeHtml(item.id)}">Export</button>
      </div>
    </div>
  `).join('');
}

function renderWorkflowSummary(items) {
  const container = document.getElementById('workflow-summary');
  document.getElementById('eval-dataset-trend-filter').value = evalDatasetTrendFilter;
  document.getElementById('eval-dataset-trend-sort').value = evalDatasetTrendSort;
  renderRegressionWatchProfileOptions();
  document.getElementById('regression-watch-source-hit-drop').value = String(regressionWatchSourceHitDrop);
  document.getElementById('regression-watch-include-preset').checked = regressionWatchIncludePreset;
  document.getElementById('regression-watch-include-dataset').checked = regressionWatchIncludeDataset;
  const workflows = (items || []).filter((item) => item.kind === 'workflow');
  const workflowWithType = workflows.map((item) => {
    let workflowType = item.title || 'workflow';
    if (item.payload) {
      try {
        const payload = JSON.parse(item.payload);
        if (payload.workflow) {
          workflowType = payload.workflow;
        }
      } catch (error) {
        // Ignore malformed payloads and fall back to title.
      }
    }
    return {item, workflowType};
  });
  const lastSuccess = workflows.find((item) => item.status === 'ok');
  const lastFailure = workflows.find((item) => item.status === 'error');
  const successCount = workflows.filter((item) => item.status === 'ok').length;
  const failureCount = workflows.filter((item) => item.status === 'error').length;
  const successRate = workflows.length > 0 ? Math.round((successCount / workflows.length) * 100) : 0;
  const latestWorkflow = workflows[0];
  const workflowTypeStats = Array.from(workflowWithType.reduce((map, entry) => {
    const {item, workflowType} = entry;
    const current = map.get(workflowType) || {name: workflowType, runs: 0, success: 0, failure: 0};
    current.runs += 1;
    if (item.status === 'ok') {
      current.success += 1;
    } else if (item.status === 'error') {
      current.failure += 1;
    }
    map.set(workflowType, current);
    return map;
  }, new Map()).values()).map((entry) => ({
    ...entry,
    successRate: entry.runs > 0 ? Math.round((entry.success / entry.runs) * 100) : 0,
  })).sort((a, b) => b.runs - a.runs || a.name.localeCompare(b.name));
  const recoveryWorkflows = workflowWithType.filter((entry) => entry.workflowType === 'preset_recovery').map((entry) => entry.item);
  const verificationWorkflows = workflowWithType.filter((entry) => entry.workflowType === 'preset_verification').map((entry) => entry.item);
  const primaryWorkflows = workflowWithType
    .filter((entry) => entry.workflowType !== 'preset_recovery' && entry.workflowType !== 'preset_verification')
    .map((entry) => entry.item);
  const recoverySuccessCount = recoveryWorkflows.filter((item) => item.status === 'ok').length;
  const recoveryFailureCount = recoveryWorkflows.filter((item) => item.status === 'error').length;
  const recoverySuccessRate = recoveryWorkflows.length > 0 ? Math.round((recoverySuccessCount / recoveryWorkflows.length) * 100) : 0;
  const latestRecovery = recoveryWorkflows[0] || null;
  const verificationSuccessCount = verificationWorkflows.filter((item) => item.status === 'ok').length;
  const verificationFailureCount = verificationWorkflows.filter((item) => item.status === 'error').length;
  const verificationSuccessRate = verificationWorkflows.length > 0 ? Math.round((verificationSuccessCount / verificationWorkflows.length) * 100) : 0;
  const latestVerification = verificationWorkflows[0] || null;
  const evalDatasetTrends = summarizeEvalDatasetTrends(items);
  const filteredEvalDatasetTrends = evalDatasetTrends
    .filter((entry) => {
      if (evalDatasetTrendFilter === 'regressed') {
        return getEvalDatasetTrendRegressionRank(entry) === 0;
      }
      return true;
    })
    .sort((left, right) => {
      if (evalDatasetTrendSort === 'recent') {
        return (right.latest?.timestamp || '').localeCompare(left.latest?.timestamp || '') || left.datasetPath.localeCompare(right.datasetPath);
      }
      if (evalDatasetTrendSort === 'regression') {
        return getEvalDatasetTrendRegressionRank(left) - getEvalDatasetTrendRegressionRank(right)
          || (right.latest?.timestamp || '').localeCompare(left.latest?.timestamp || '')
          || left.datasetPath.localeCompare(right.datasetPath);
      }
      return left.datasetPath.localeCompare(right.datasetPath);
    });
  const verificationTrends = Array.from(workflowWithType
    .filter((entry) => entry.workflowType === 'preset_verification')
    .reduce((map, entry) => {
      let payload = null;
      try {
        payload = entry.item.payload ? JSON.parse(entry.item.payload) : null;
      } catch (error) {
        payload = null;
      }

      const presetName = payload?.preset?.name || payload?.preset_name || '(unknown preset)';
      const steps = Array.isArray(payload?.steps) ? payload.steps : [];
      const summary = summarizeVerificationRun({
        workflow: 'preset_verification',
        steps,
      });
      const ragStep = summary.representativeSteps.find((step) => step.name === 'rag_verification') || null;
      const evalStep = summary.representativeSteps.find((step) => step.name === 'eval_verification') || null;
      const current = map.get(presetName) || {presetName, runs: []};
      current.runs.push({
        status: entry.item.status || '-',
        timestamp: entry.item.timestamp || '-',
        okCount: summary.okCount,
        failedCount: summary.failedCount,
        skippedCount: summary.skippedCount,
        sourceHitRate: parseMetricFromDetail(evalStep?.detail, 'source_hit_rate'),
        keywordHitRate: parseMetricFromDetail(evalStep?.detail, 'keyword_hit_rate'),
        sourceCount: parseMetricFromDetail(ragStep?.detail, 'source_count'),
        topSource: parseMetricFromDetail(ragStep?.detail, 'top_source'),
      });
      map.set(presetName, current);
      return map;
    }, new Map()).values())
    .map((entry) => ({
      presetName: entry.presetName,
      runs: entry.runs.slice(0, 3),
      latest: entry.runs[0] || null,
      previous: entry.runs[1] || null,
      successRate: entry.runs.length > 0
        ? Math.round((entry.runs.filter((run) => run.status === 'ok').length / entry.runs.length) * 100)
        : 0,
    }))
    .sort((a, b) => (b.latest?.timestamp || '').localeCompare(a.latest?.timestamp || '') || a.presetName.localeCompare(b.presetName));
  const primarySuccessCount = primaryWorkflows.filter((item) => item.status === 'ok').length;
  const primaryFailureCount = primaryWorkflows.filter((item) => item.status === 'error').length;
  const primarySuccessRate = primaryWorkflows.length > 0 ? Math.round((primarySuccessCount / primaryWorkflows.length) * 100) : 0;

  if (workflows.length === 0) {
    container.innerHTML = '<div class="runtime-result-card"><div class="runtime-result-text">No workflow runs recorded yet.</div></div>';
    return;
  }

  const cards = [];
  const presetRegressionAlerts = regressionWatchIncludePreset ? getPresetRegressionAlerts(verificationTrends) : [];
  const datasetRegressionAlerts = regressionWatchIncludeDataset ? getDatasetRegressionAlerts(evalDatasetTrends) : [];
  const topRegressedPreset = presetRegressionAlerts[0] || null;
  const topRegressedDataset = datasetRegressionAlerts[0] || null;
  if (topRegressedPreset || topRegressedDataset) {
    cards.push(`
      <div class="runtime-result-card">
        <div class="runtime-result-head">
          <span class="runtime-result-title">Regression Watch</span>
          <span class="runtime-pill required">${escapeHtml(String(presetRegressionAlerts.length + datasetRegressionAlerts.length))} alerts</span>
        </div>
        <div class="runtime-result-meta">source_hit_rate_drop_threshold=${escapeHtml(String(regressionWatchSourceHitDrop))} | monitor_presets=${escapeHtml(regressionWatchIncludePreset ? 'true' : 'false')} | monitor_datasets=${escapeHtml(regressionWatchIncludeDataset ? 'true' : 'false')}</div>
        <div class="actions">
          <button class="ghost-btn export-regression-watch-btn">Export Regression Watch</button>
        </div>
        <div class="runtime-result-list">
          ${presetRegressionAlerts.map((entry) => `
            <div class="runtime-result-item">
              <div class="runtime-result-head">
                <span class="runtime-result-name">Preset: ${escapeHtml(entry.presetName)}</span>
                <span class="runtime-pill required">alert</span>
              </div>
              <div class="runtime-result-meta">status_delta=${escapeHtml(summarizeStatusDelta(entry.latest?.status, entry.previous?.status))}</div>
              <div class="runtime-result-meta">source_hit_rate_delta=${escapeHtml(formatMetricDelta(entry.latest?.sourceHitRate, entry.previous?.sourceHitRate))} | rag_source_count_delta=${escapeHtml(formatMetricDelta(entry.latest?.sourceCount, entry.previous?.sourceCount))}</div>
              <div class="actions">
                <button class="ghost-btn trend-focus-preset-btn" data-preset-name="${escapeHtml(entry.presetName)}">Focus Preset</button>
                <button class="ghost-btn trend-open-verification-btn" data-preset-name="${escapeHtml(entry.presetName)}">Open Latest Verification</button>
              </div>
            </div>
          `).join('')}
          ${datasetRegressionAlerts.map((entry) => `
            <div class="runtime-result-item">
              <div class="runtime-result-head">
                <span class="runtime-result-name">Dataset: ${escapeHtml(entry.datasetPath)}</span>
                <span class="runtime-pill required">alert</span>
              </div>
              <div class="runtime-result-meta">project=${escapeHtml(entry.latest?.project || '(default)')}</div>
              <div class="runtime-result-meta">source_hit_rate_delta=${escapeHtml(formatMetricDelta(entry.latest?.sourceHitRate, entry.previous?.sourceHitRate))} | keyword_hit_rate_delta=${escapeHtml(formatMetricDelta(entry.latest?.keywordHitRate, entry.previous?.keywordHitRate))}</div>
              <div class="runtime-result-meta">average_latency_ms_delta=${escapeHtml(formatMetricDelta(entry.latest?.averageLatencyMs, entry.previous?.averageLatencyMs))} | total_tokens_delta=${escapeHtml(formatMetricDelta(entry.latest?.totalTokens, entry.previous?.totalTokens))}</div>
              <div class="actions">
                <button class="ghost-btn trend-use-eval-dataset-btn" data-dataset-path="${escapeHtml(entry.datasetPath)}" data-project="${escapeHtml(entry.latest?.project || '')}">Use In Eval</button>
                <button class="ghost-btn trend-run-eval-dataset-btn" data-dataset-path="${escapeHtml(entry.datasetPath)}" data-project="${escapeHtml(entry.latest?.project || '')}">Run Eval</button>
              </div>
            </div>
          `).join('')}
        </div>
      </div>
    `);
  }
  cards.push(`
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">Workflow Health</span>
        <span class="runtime-pill ${successRate >= 70 ? 'optional' : successRate >= 40 ? 'neutral' : 'required'}">${escapeHtml(`${successRate}% success`)}</span>
      </div>
      <div class="runtime-summary-grid">
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">Runs</div>
          <div class="runtime-result-text">${escapeHtml(String(workflows.length))}</div>
        </div>
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">Successful</div>
          <div class="runtime-result-text">${escapeHtml(String(successCount))}</div>
        </div>
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">Failed</div>
          <div class="runtime-result-text">${escapeHtml(String(failureCount))}</div>
        </div>
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">Latest Type</div>
          <div class="runtime-result-text">${escapeHtml(latestWorkflow?.title || '-')}</div>
        </div>
      </div>
    </div>
  `);
  cards.push(`
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">Primary vs Recovery</span>
        <span class="runtime-pill neutral">${escapeHtml(`${primaryWorkflows.length} / ${recoveryWorkflows.length} / ${verificationWorkflows.length}`)}</span>
      </div>
      <div class="runtime-summary-grid">
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">Primary Flows</div>
          <div class="runtime-result-text">${escapeHtml(String(primaryWorkflows.length))}</div>
          <div class="runtime-result-meta">${escapeHtml(`${primarySuccessRate}% success | ok=${primarySuccessCount} | error=${primaryFailureCount}`)}</div>
        </div>
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">Recovery Flows</div>
          <div class="runtime-result-text">${escapeHtml(String(recoveryWorkflows.length))}</div>
          <div class="runtime-result-meta">${escapeHtml(`${recoverySuccessRate}% success | ok=${recoverySuccessCount} | error=${recoveryFailureCount}`)}</div>
        </div>
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">Verification Flows</div>
          <div class="runtime-result-text">${escapeHtml(String(verificationWorkflows.length))}</div>
          <div class="runtime-result-meta">${escapeHtml(`${verificationSuccessRate}% success | ok=${verificationSuccessCount} | error=${verificationFailureCount}`)}</div>
        </div>
      </div>
    </div>
  `);
  cards.push(`
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">By Workflow Type</span>
        <span class="runtime-pill neutral">${escapeHtml(String(workflowTypeStats.length))}</span>
      </div>
      <div class="runtime-result-list">
        ${workflowTypeStats.map((entry) => `
          <div class="runtime-result-item">
            <div class="runtime-result-head">
              <span class="runtime-result-name">${escapeHtml(entry.name)}</span>
              <span class="runtime-pill ${entry.successRate >= 70 ? 'optional' : entry.successRate >= 40 ? 'neutral' : 'required'}">${escapeHtml(`${entry.successRate}%`)}</span>
            </div>
            <div class="runtime-result-meta">runs=${escapeHtml(String(entry.runs))} | ok=${escapeHtml(String(entry.success))} | error=${escapeHtml(String(entry.failure))}</div>
          </div>
        `).join('')}
      </div>
    </div>
  `);
  if (verificationTrends.length > 0) {
    cards.push(`
      <div class="runtime-result-card">
        <div class="runtime-result-head">
          <span class="runtime-result-title">Verification Trends</span>
          <span class="runtime-pill neutral">${escapeHtml(String(verificationTrends.length))}</span>
        </div>
        <div class="runtime-result-list">
          ${verificationTrends.map((entry) => `
            <div class="runtime-result-item">
              <div class="runtime-result-head">
                <span class="runtime-result-name">${escapeHtml(entry.presetName)}</span>
                <span class="runtime-pill ${entry.successRate >= 70 ? 'optional' : entry.successRate >= 40 ? 'neutral' : 'required'}">${escapeHtml(`${entry.successRate}%`)}</span>
              </div>
              <div class="runtime-result-meta">recent=${escapeHtml(entry.runs.map((run) => run.status).join(' -> '))}</div>
              <div class="runtime-result-meta">latest=${escapeHtml(entry.latest?.timestamp || '-')} | ok=${escapeHtml(String(entry.latest?.okCount ?? '-'))} | failed=${escapeHtml(String(entry.latest?.failedCount ?? '-'))} | skipped=${escapeHtml(String(entry.latest?.skippedCount ?? '-'))}</div>
              <div class="runtime-result-meta">source_hit_rate=${escapeHtml(entry.latest?.sourceHitRate || '-')} | keyword_hit_rate=${escapeHtml(entry.latest?.keywordHitRate || '-')}</div>
              <div class="runtime-result-meta">rag_source_count=${escapeHtml(entry.latest?.sourceCount || '-')} | top_source=${escapeHtml(entry.latest?.topSource || '-')}</div>
              <div class="runtime-result-meta">status_delta=${escapeHtml(summarizeStatusDelta(entry.latest?.status, entry.previous?.status))} | <span class="runtime-pill ${classifyStatusDelta(entry.latest?.status, entry.previous?.status).pillClass}">${escapeHtml(classifyStatusDelta(entry.latest?.status, entry.previous?.status).label)}</span></div>
              <div class="runtime-result-meta">source_hit_rate_delta=${escapeHtml(formatMetricDelta(entry.latest?.sourceHitRate, entry.previous?.sourceHitRate))} | <span class="runtime-pill ${classifyMetricDelta(entry.latest?.sourceHitRate, entry.previous?.sourceHitRate).pillClass}">${escapeHtml(classifyMetricDelta(entry.latest?.sourceHitRate, entry.previous?.sourceHitRate).label)}</span> | keyword_hit_rate_delta=${escapeHtml(formatMetricDelta(entry.latest?.keywordHitRate, entry.previous?.keywordHitRate))} | <span class="runtime-pill ${classifyMetricDelta(entry.latest?.keywordHitRate, entry.previous?.keywordHitRate).pillClass}">${escapeHtml(classifyMetricDelta(entry.latest?.keywordHitRate, entry.previous?.keywordHitRate).label)}</span></div>
              <div class="runtime-result-meta">rag_source_count_delta=${escapeHtml(formatMetricDelta(entry.latest?.sourceCount, entry.previous?.sourceCount))} | <span class="runtime-pill ${classifyMetricDelta(entry.latest?.sourceCount, entry.previous?.sourceCount).pillClass}">${escapeHtml(classifyMetricDelta(entry.latest?.sourceCount, entry.previous?.sourceCount).label)}</span></div>
              <div class="actions">
                <button class="ghost-btn trend-focus-preset-btn" data-preset-name="${escapeHtml(entry.presetName)}">Focus Preset</button>
                <button class="ghost-btn trend-open-verification-btn" data-preset-name="${escapeHtml(entry.presetName)}">Open Latest Verification</button>
              </div>
            </div>
          `).join('')}
        </div>
      </div>
    `);
  }
  if (filteredEvalDatasetTrends.length > 0) {
    cards.push(`
      <div class="runtime-result-card">
        <div class="runtime-result-head">
          <span class="runtime-result-title">Eval Dataset Trends</span>
          <span class="runtime-pill neutral">${escapeHtml(String(filteredEvalDatasetTrends.length))}</span>
        </div>
        <div class="runtime-result-list">
          ${filteredEvalDatasetTrends.map((entry) => `
            <div class="runtime-result-item">
              <div class="runtime-result-head">
                <span class="runtime-result-name">${escapeHtml(entry.datasetPath)}</span>
                <span class="runtime-pill ${entry.successRate >= 70 ? 'optional' : entry.successRate >= 40 ? 'neutral' : 'required'}">${escapeHtml(`${entry.successRate}%`)}</span>
              </div>
              <div class="runtime-result-meta">recent=${escapeHtml(entry.runs.map((run) => run.status).join(' -> '))}</div>
              <div class="runtime-result-meta">latest=${escapeHtml(entry.latest?.timestamp || '-')} | project=${escapeHtml(entry.latest?.project || '(default)')} | total_cases=${escapeHtml(entry.latest?.totalCases || '-')}</div>
              <div class="runtime-result-meta">source_hit_rate=${escapeHtml(entry.latest?.sourceHitRate || '-')} | keyword_hit_rate=${escapeHtml(entry.latest?.keywordHitRate || '-')}</div>
              <div class="runtime-result-meta">average_latency_ms=${escapeHtml(entry.latest?.averageLatencyMs || '-')} | total_prompt_tokens=${escapeHtml(entry.latest?.totalPromptTokens || '-')} | total_completion_tokens=${escapeHtml(entry.latest?.totalCompletionTokens || '-')} | total_tokens=${escapeHtml(entry.latest?.totalTokens || '-')}</div>
              <div class="runtime-result-meta">source_hit_rate_delta=${escapeHtml(formatMetricDelta(entry.latest?.sourceHitRate, entry.previous?.sourceHitRate))} | <span class="runtime-pill ${classifyMetricDelta(entry.latest?.sourceHitRate, entry.previous?.sourceHitRate).pillClass}">${escapeHtml(classifyMetricDelta(entry.latest?.sourceHitRate, entry.previous?.sourceHitRate).label)}</span> | keyword_hit_rate_delta=${escapeHtml(formatMetricDelta(entry.latest?.keywordHitRate, entry.previous?.keywordHitRate))} | <span class="runtime-pill ${classifyMetricDelta(entry.latest?.keywordHitRate, entry.previous?.keywordHitRate).pillClass}">${escapeHtml(classifyMetricDelta(entry.latest?.keywordHitRate, entry.previous?.keywordHitRate).label)}</span></div>
              <div class="runtime-result-meta">average_latency_ms_delta=${escapeHtml(formatMetricDelta(entry.latest?.averageLatencyMs, entry.previous?.averageLatencyMs))} | <span class="runtime-pill ${classifyMetricDelta(entry.latest?.averageLatencyMs, entry.previous?.averageLatencyMs, false).pillClass}">${escapeHtml(classifyMetricDelta(entry.latest?.averageLatencyMs, entry.previous?.averageLatencyMs, false).label)}</span> | total_tokens_delta=${escapeHtml(formatMetricDelta(entry.latest?.totalTokens, entry.previous?.totalTokens))} | <span class="runtime-pill ${classifyMetricDelta(entry.latest?.totalTokens, entry.previous?.totalTokens).pillClass}">${escapeHtml(classifyMetricDelta(entry.latest?.totalTokens, entry.previous?.totalTokens).label)}</span></div>
              <div class="actions">
                <button class="ghost-btn trend-use-eval-dataset-btn" data-dataset-path="${escapeHtml(entry.datasetPath)}" data-project="${escapeHtml(entry.latest?.project || '')}">Use In Eval</button>
                <button class="ghost-btn trend-run-eval-dataset-btn" data-dataset-path="${escapeHtml(entry.datasetPath)}" data-project="${escapeHtml(entry.latest?.project || '')}">Run Eval</button>
              </div>
            </div>
          `).join('')}
        </div>
      </div>
    `);
  } else if (evalDatasetTrends.length > 0) {
    cards.push(`
      <div class="runtime-result-card">
        <div class="runtime-result-head">
          <span class="runtime-result-title">Eval Dataset Trends</span>
          <span class="runtime-pill neutral">0</span>
        </div>
        <div class="runtime-result-text">No eval datasets match the current trend filter.</div>
      </div>
    `);
  }
  if (latestRecovery) {
    let latestRecoveryPayload = null;
    try {
      latestRecoveryPayload = latestRecovery.payload ? JSON.parse(latestRecovery.payload) : null;
    } catch (error) {
      latestRecoveryPayload = null;
    }
    const retryOriginalButton = latestRecoveryPayload?.recovery_for_history_id
      ? `<button class="ghost-btn rerun-workflow-btn" data-history-id="${escapeHtml(latestRecoveryPayload.recovery_for_history_id)}">Retry Original</button>`
      : '';
    cards.push(`
      <div class="runtime-result-card">
        <div class="runtime-result-head">
          <span class="runtime-result-title">Latest Recovery</span>
          <span class="runtime-pill ${latestRecovery.status === 'ok' ? 'optional' : latestRecovery.status === 'error' ? 'required' : 'neutral'}">${escapeHtml(latestRecovery.status || '-')}</span>
        </div>
        <div class="runtime-result-text">${escapeHtml(latestRecovery.summary || latestRecovery.title || '-')}</div>
        <div class="runtime-result-meta">${escapeHtml(latestRecovery.timestamp || '-')}</div>
        ${latestRecovery.detail ? `<div class="runtime-result-text">${escapeHtml(latestRecovery.detail)}</div>` : ''}
        <div class="actions">
          <button class="ghost-btn rerun-workflow-btn" data-history-id="${escapeHtml(latestRecovery.id)}">Rerun Recovery</button>
          ${retryOriginalButton}
          <button class="ghost-btn export-history-btn" data-history-id="${escapeHtml(latestRecovery.id)}">Export</button>
        </div>
      </div>
    `);
  }
  if (latestVerification) {
    cards.push(`
      <div class="runtime-result-card">
        <div class="runtime-result-head">
          <span class="runtime-result-title">Latest Verification</span>
          <span class="runtime-pill ${latestVerification.status === 'ok' ? 'optional' : latestVerification.status === 'error' ? 'required' : 'neutral'}">${escapeHtml(latestVerification.status || '-')}</span>
        </div>
        <div class="runtime-result-text">${escapeHtml(latestVerification.summary || latestVerification.title || '-')}</div>
        <div class="runtime-result-meta">${escapeHtml(latestVerification.timestamp || '-')}</div>
        ${latestVerification.detail ? `<div class="runtime-result-text">${escapeHtml(latestVerification.detail)}</div>` : ''}
        <div class="actions">
          <button class="ghost-btn rerun-workflow-btn" data-history-id="${escapeHtml(latestVerification.id)}">Rerun Verification</button>
          <button class="ghost-btn export-history-btn" data-history-id="${escapeHtml(latestVerification.id)}">Export</button>
        </div>
      </div>
    `);
  }
  if (lastSuccess) {
    cards.push(`
      <div class="runtime-result-card">
        <div class="runtime-result-head">
          <span class="runtime-result-title">Last Successful Workflow</span>
          <span class="runtime-pill optional">ok</span>
        </div>
        <div class="runtime-result-text">${escapeHtml(lastSuccess.summary || lastSuccess.title || '-')}</div>
        <div class="runtime-result-meta">${escapeHtml(lastSuccess.timestamp || '-')}</div>
        ${lastSuccess.detail ? `<div class="runtime-result-text">${escapeHtml(lastSuccess.detail)}</div>` : ''}
        <div class="actions">
          <button class="ghost-btn rerun-workflow-btn" data-history-id="${escapeHtml(lastSuccess.id)}">Rerun</button>
          <button class="ghost-btn export-history-btn" data-history-id="${escapeHtml(lastSuccess.id)}">Export</button>
        </div>
      </div>
    `);
  }
  if (lastFailure) {
    cards.push(`
      <div class="runtime-result-card">
        <div class="runtime-result-head">
          <span class="runtime-result-title">Last Failed Workflow</span>
          <span class="runtime-pill required">error</span>
        </div>
        <div class="runtime-result-text">${escapeHtml(lastFailure.summary || lastFailure.title || '-')}</div>
        <div class="runtime-result-meta">${escapeHtml(lastFailure.timestamp || '-')}</div>
        ${lastFailure.detail ? `<div class="runtime-result-text">${escapeHtml(lastFailure.detail)}</div>` : ''}
        <div class="actions">
          <button class="ghost-btn rerun-workflow-btn" data-history-id="${escapeHtml(lastFailure.id)}">Retry</button>
          <button class="ghost-btn export-history-btn" data-history-id="${escapeHtml(lastFailure.id)}">Export</button>
        </div>
      </div>
    `);
  }

  cards.push(`
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">Workflow Notes</span>
        <span class="runtime-pill neutral">${escapeHtml(String(workflows.length))}</span>
      </div>
      <div class="runtime-result-text">Overview tracks preset workflow runs from Recent Activity so you can relaunch or export them later.</div>
    </div>
  `);

  container.innerHTML = cards.join('');
}

function summarizeSmokePolicy(preset) {
  if (!preset?.workflow_run_smoke) {
    return 'Smoke disabled';
  }
  const skips = [];
  if (preset.smoke_skip_qdrant) {
    skips.push('qdrant');
  }
  if (preset.smoke_skip_embedding) {
    skips.push('embedding');
  }
  if (preset.smoke_skip_reranker) {
    skips.push('reranker');
  }
  return skips.length === 0 ? 'Smoke enabled, no skips' : `Smoke enabled, skip: ${skips.join(', ')}`;
}

function normalizePresetRuntimeProfile(profile) {
  const normalized = String(profile || '').trim();
  if (normalized === 'local_only' || normalized === 'external_rag') {
    return normalized;
  }
  return 'current';
}

function describePresetRuntimeProfile(profile) {
  switch (normalizePresetRuntimeProfile(profile)) {
    case 'local_only':
      return 'Auto apply local-only runtime';
    case 'external_rag':
      return 'Auto apply external embedding + qdrant runtime';
    default:
      return 'Use current runtime config';
  }
}

function summarizePresetRuntimeProfile(profile) {
  const normalized = normalizePresetRuntimeProfile(profile);
  return {
    key: normalized,
    label: describePresetRuntimeProfile(normalized),
    pillClass: normalized === 'local_only' ? 'optional' : 'neutral',
    validationNote: normalized === 'current'
      ? 'Validation reflects the current runtime config.'
      : 'Workflow auto-applies this runtime profile before validation and execution.',
  };
}

function summarizeRuntimeProfileMatch(profile) {
  const runtimeProfile = summarizePresetRuntimeProfile(profile);
  const matches = runtimeConfigMatchesProfile(latestRuntimeStatus?.config_summary, runtimeProfile.key);
  return {
    ...runtimeProfile,
    matches,
    matchLabel: matches ? 'active' : 'mismatch',
    matchPillClass: matches ? 'optional' : 'required',
  };
}

function summarizePresetPaths(rawValue) {
  const paths = splitMultilinePaths(rawValue);
  if (paths.length === 0) {
    return '-';
  }
  if (paths.length === 1) {
    return paths[0];
  }
  return `${paths[0]} (+${paths.length - 1} more)`;
}

function canStartValidationService(name) {
  return ['fast', 'work', 'code', 'gateway', 'embedding', 'qdrant'].includes(String(name || ''));
}

function summarizeValidationState(response) {
  if (!response) {
    return {label: 'unknown', pillClass: 'neutral'};
  }
  if (!response.valid) {
    return {label: 'invalid', pillClass: 'required'};
  }
  if (response.ready) {
    return {label: 'ready', pillClass: 'optional'};
  }
  return {label: 'not ready', pillClass: 'neutral'};
}

function summarizePresetOperations(validation, historyEntries) {
  const state = summarizeValidationState(validation);
  const entries = Array.isArray(historyEntries) ? historyEntries : [];
  const latest = entries[0]?.item || null;
  const latestEntry = entries[0] || null;
  const successCount = entries.filter((entry) => entry.item.status === 'ok').length;
  const errorCount = entries.filter((entry) => entry.item.status === 'error').length;

  if (state.label === 'ready' && latest?.status === 'ok') {
    return {
      label: 'healthy',
      pillClass: 'optional',
      detail: `last_ok=${latest.timestamp || '-'} | ok=${successCount} | error=${errorCount}`,
    };
  }
  if (state.label === 'invalid') {
    return {
      label: 'stalled',
      pillClass: 'required',
      detail: `preset invalid | ok=${successCount} | error=${errorCount}`,
    };
  }
  if (state.label === 'not ready') {
    return {
      label: 'blocked',
      pillClass: 'required',
      detail: `waiting on runtime dependencies | ok=${successCount} | error=${errorCount}`,
    };
  }
  if (latest?.status === 'error') {
    return {
      label: 'degraded',
      pillClass: 'required',
      detail: latestEntry?.workflow === 'preset_verification'
        ? `verification_failed=${summarizeVerificationFailure(latestEntry)} | ok=${successCount} | error=${errorCount}`
        : `last_error=${latest.timestamp || '-'} | ok=${successCount} | error=${errorCount}`,
    };
  }
  if (entries.length === 0) {
    return {
      label: 'new',
      pillClass: 'neutral',
      detail: `no workflow runs yet | validation=${state.label}`,
    };
  }
  return {
    label: 'observing',
    pillClass: 'neutral',
    detail: `validation=${state.label} | ok=${successCount} | error=${errorCount}`,
  };
}

function summarizeWorkflowFailure(entry) {
  if (!entry?.item) {
    return '-';
  }
  const item = entry.item;
  const base = item.detail || item.summary || item.title || '-';
  const compact = String(base).replace(/\s+/g, ' ').trim();
  if (compact.length <= 140) {
    return compact;
  }
  return `${compact.slice(0, 137)}...`;
}

function summarizeVerificationFailure(entry) {
  if (!entry || entry.workflow !== 'preset_verification') {
    return '-';
  }
  const failedStep = (entry.steps || []).find((step) => step.status === 'failed' && step.name !== 'smoke' && step.name !== 'preset_validation');
  if (!failedStep) {
    return summarizeWorkflowFailure(entry);
  }
  const compact = String(failedStep.detail || '').replace(/\s+/g, ' ').trim();
  return compact.length <= 140 ? `${failedStep.name}: ${compact}` : `${failedStep.name}: ${compact.slice(0, 137)}...`;
}

function getRepresentativeVerificationSteps(entry) {
  if (!entry || entry.workflow !== 'preset_verification') {
    return [];
  }
  return (entry.steps || []).filter((step) => String(step.name || '').endsWith('_verification'));
}

function summarizeRepresentativeVerificationStep(step) {
  if (!step) {
    return {label: '-', detail: '-'};
  }

  const nameMap = {
    chat_verification: 'chat',
    ingest_verification: 'ingest',
    rag_verification: 'rag',
    eval_verification: 'eval',
  };
  const label = nameMap[String(step.name || '')] || String(step.name || '-');
  const detail = String(step.detail || '').replace(/\s+/g, ' ').trim() || '-';
  return {
    label,
    detail,
  };
}

function extractRepresentativeRequestFromVerificationStep(step) {
  const representative = summarizeRepresentativeVerificationStep(step);
  const detail = String(step?.detail || '').trim();
  const prefix = `${representative.label}:`;
  if (!detail.startsWith(prefix)) {
    return null;
  }

  const remainder = detail.slice(prefix.length).trim();
  const [requestName] = remainder.split('|');
  const normalizedName = String(requestName || '').trim();
  if (!normalizedName) {
    return null;
  }

  return {
    kind: representative.label,
    name: normalizedName,
  };
}

function summarizeVerificationRun(entry) {
  const representativeSteps = getRepresentativeVerificationSteps(entry);
  const okCount = representativeSteps.filter((step) => step.status === 'ok').length;
  const failedCount = representativeSteps.filter((step) => step.status === 'failed').length;
  const skippedCount = representativeSteps.filter((step) => step.status === 'skipped').length;
  const firstFailure = representativeSteps.find((step) => step.status === 'failed') || null;

  return {
    total: representativeSteps.length,
    okCount,
    failedCount,
    skippedCount,
    firstFailure,
    firstFailureSummary: firstFailure ? summarizeRepresentativeVerificationStep(firstFailure) : null,
    representativeSteps,
  };
}

function parseMetricFromDetail(detail, key) {
  const text = String(detail || '');
  const pattern = new RegExp(`${key}=([^,|]+)`);
  const match = text.match(pattern);
  return match ? String(match[1] || '').trim() : '';
}

function formatMetricDelta(latestValue, previousValue) {
  const latest = Number(latestValue);
  const previous = Number(previousValue);
  if (!Number.isFinite(latest) || !Number.isFinite(previous)) {
    return '-';
  }
  const delta = latest - previous;
  if (delta === 0) {
    return '0';
  }
  return `${delta > 0 ? '+' : ''}${delta}`;
}

function summarizeStatusDelta(latestStatus, previousStatus) {
  const latest = String(latestStatus || '-');
  const previous = String(previousStatus || '-');
  if (!previous || previous === '-') {
    return 'no previous run';
  }
  if (latest === previous) {
    return `unchanged (${latest})`;
  }
  return `${previous} -> ${latest}`;
}

function classifyStatusDelta(latestStatus, previousStatus) {
  const latest = String(latestStatus || '-');
  const previous = String(previousStatus || '-');
  if (!previous || previous === '-') {
    return {label: 'new', pillClass: 'neutral'};
  }
  if (latest === previous) {
    return {label: 'unchanged', pillClass: 'neutral'};
  }
  if (latest === 'ok' && previous !== 'ok') {
    return {label: 'improved', pillClass: 'optional'};
  }
  if (latest !== 'ok' && previous === 'ok') {
    return {label: 'regressed', pillClass: 'required'};
  }
  return {label: 'changed', pillClass: 'neutral'};
}

function classifyMetricDelta(latestValue, previousValue, higherIsBetter = true) {
  const latest = Number(latestValue);
  const previous = Number(previousValue);
  if (!Number.isFinite(latest) || !Number.isFinite(previous)) {
    return {label: 'n/a', pillClass: 'neutral'};
  }
  if (latest === previous) {
    return {label: 'unchanged', pillClass: 'neutral'};
  }
  if (higherIsBetter ? latest > previous : latest < previous) {
    return {label: 'improved', pillClass: 'optional'};
  }
  return {label: 'regressed', pillClass: 'required'};
}

function summarizePresetVerificationRegression(entries) {
  const verificationEntries = (Array.isArray(entries) ? entries : []).filter((entry) => entry.workflow === 'preset_verification');
  const latestVerification = verificationEntries[0] || null;
  const previousVerification = verificationEntries[1] || null;
  if (!latestVerification) {
    return null;
  }

  const latestSummary = summarizeVerificationRun(latestVerification);
  const previousSummary = summarizeVerificationRun(previousVerification);
  const latestRagStep = latestSummary.representativeSteps.find((step) => step.name === 'rag_verification') || null;
  const previousRagStep = previousSummary.representativeSteps.find((step) => step.name === 'rag_verification') || null;
  const latestEvalStep = latestSummary.representativeSteps.find((step) => step.name === 'eval_verification') || null;
  const previousEvalStep = previousSummary.representativeSteps.find((step) => step.name === 'eval_verification') || null;

  return {
    latest: latestVerification,
    previous: previousVerification,
    status: classifyStatusDelta(latestVerification.item?.status, previousVerification?.item?.status),
    statusText: summarizeStatusDelta(latestVerification.item?.status, previousVerification?.item?.status),
    sourceHitRateDelta: formatMetricDelta(
      parseMetricFromDetail(latestEvalStep?.detail, 'source_hit_rate'),
      parseMetricFromDetail(previousEvalStep?.detail, 'source_hit_rate'),
    ),
    sourceHitRateState: classifyMetricDelta(
      parseMetricFromDetail(latestEvalStep?.detail, 'source_hit_rate'),
      parseMetricFromDetail(previousEvalStep?.detail, 'source_hit_rate'),
    ),
    ragSourceCountDelta: formatMetricDelta(
      parseMetricFromDetail(latestRagStep?.detail, 'source_count'),
      parseMetricFromDetail(previousRagStep?.detail, 'source_count'),
    ),
    ragSourceCountState: classifyMetricDelta(
      parseMetricFromDetail(latestRagStep?.detail, 'source_count'),
      parseMetricFromDetail(previousRagStep?.detail, 'source_count'),
    ),
  };
}

function summarizeEvalDatasetTrends(items) {
  return Array.from((items || [])
    .filter((item) => item.kind === 'eval')
    .reduce((map, item) => {
      let payload = null;
      try {
        payload = item.payload ? JSON.parse(item.payload) : null;
      } catch (error) {
        payload = null;
      }

      const datasetPath = payload?.dataset_path || item.summary || '(unknown dataset)';
      const current = map.get(datasetPath) || {datasetPath, runs: []};
      current.runs.push({
        status: item.status || '-',
        timestamp: item.timestamp || '-',
        sourceHitRate: parseMetricFromDetail(item.detail, 'source_hit_rate'),
        keywordHitRate: parseMetricFromDetail(item.detail, 'keyword_hit_rate'),
        totalCases: parseMetricFromDetail(item.detail, 'total_cases'),
        averageLatencyMs: parseMetricFromDetail(item.detail, 'average_latency_ms'),
        totalPromptTokens: parseMetricFromDetail(item.detail, 'total_prompt_tokens'),
        totalCompletionTokens: parseMetricFromDetail(item.detail, 'total_completion_tokens'),
        totalTokens: parseMetricFromDetail(item.detail, 'total_tokens'),
        project: payload?.project || '',
      });
      map.set(datasetPath, current);
      return map;
    }, new Map()).values())
    .map((entry) => ({
      datasetPath: entry.datasetPath,
      runs: entry.runs.slice(0, 3),
      latest: entry.runs[0] || null,
      previous: entry.runs[1] || null,
      successRate: entry.runs.length > 0
        ? Math.round((entry.runs.filter((run) => run.status === 'ok').length / entry.runs.length) * 100)
        : 0,
    }))
    .sort((a, b) => (b.latest?.timestamp || '').localeCompare(a.latest?.timestamp || '') || a.datasetPath.localeCompare(b.datasetPath));
}

function getPresetCatalogRegressionRank(regression) {
  if (!regression) {
    return 3;
  }
  if (regression.status?.label === 'regressed' || regression.sourceHitRateState?.label === 'regressed' || regression.ragSourceCountState?.label === 'regressed') {
    return 0;
  }
  if (regression.status?.label === 'changed') {
    return 1;
  }
  if (regression.status?.label === 'improved' || regression.sourceHitRateState?.label === 'improved' || regression.ragSourceCountState?.label === 'improved') {
    return 2;
  }
  return 3;
}

function getEvalDatasetTrendRegressionRank(entry) {
  if (!entry) {
    return 3;
  }
  const sourceState = classifyMetricDelta(entry.latest?.sourceHitRate, entry.previous?.sourceHitRate);
  const keywordState = classifyMetricDelta(entry.latest?.keywordHitRate, entry.previous?.keywordHitRate);
  const latencyState = classifyMetricDelta(entry.latest?.averageLatencyMs, entry.previous?.averageLatencyMs, false);
  if (sourceState.label === 'regressed' || keywordState.label === 'regressed') {
    return 0;
  }
  if (latencyState.label === 'regressed') {
    return 1;
  }
  if (sourceState.label === 'improved' || keywordState.label === 'improved') {
    return 2;
  }
  return 3;
}

function getVisiblePresetCatalogEntries(presets) {
  const items = (presets || []).map((preset) => {
    const matching = getWorkflowEntriesForPreset(preset.name);
    const latestTimestamp = matching[0]?.item?.timestamp || '';
    return {
      preset,
      matching,
      latestTimestamp,
      regression: summarizePresetVerificationRegression(matching),
    };
  });

  const filteredPresets = items.filter((entry) => {
    if (presetCatalogFilter === 'regressed') {
      return getPresetCatalogRegressionRank(entry.regression) === 0;
    }
    if (presetCatalogFilter === 'runtime_mismatch') {
      return summarizeRuntimeProfileMatch(entry.preset.runtime_profile).matches === false;
    }
    if (presetCatalogFilter === 'external_rag') {
      return normalizePresetRuntimeProfile(entry.preset.runtime_profile) === 'external_rag';
    }
    if (presetCatalogFilter === 'local_only') {
      return normalizePresetRuntimeProfile(entry.preset.runtime_profile) === 'local_only';
    }
    if (presetCatalogFilter === 'current') {
      return normalizePresetRuntimeProfile(entry.preset.runtime_profile) === 'current';
    }
    return true;
  });

  return filteredPresets.slice().sort((left, right) => {
    if (presetCatalogSort === 'recent') {
      return (right.latestTimestamp || '').localeCompare(left.latestTimestamp || '') || left.preset.name.localeCompare(right.preset.name);
    }
    if (presetCatalogSort === 'regression') {
      return getPresetCatalogRegressionRank(left.regression) - getPresetCatalogRegressionRank(right.regression)
        || (right.latestTimestamp || '').localeCompare(left.latestTimestamp || '')
        || left.preset.name.localeCompare(right.preset.name);
    }
    return left.preset.name.localeCompare(right.preset.name);
  });
}

function getWorkflowEntriesForPreset(presetName) {
  const normalized = String(presetName || '').trim();
  if (!normalized) {
    return [];
  }
  return currentExecutionHistory
    .filter((item) => item.kind === 'workflow' && item.payload)
    .map((item) => {
      try {
        const payload = JSON.parse(item.payload);
        return {
          item,
          payload,
          presetName: payload.preset?.name || payload.preset_name || '',
          workflow: payload.workflow || item.title || 'workflow',
          steps: Array.isArray(payload.steps) ? payload.steps : [],
        };
      } catch (error) {
        return null;
      }
    })
    .filter((entry) => entry && entry.presetName === normalized);
}

function getLatestVerificationEntryForPreset(presetName) {
  return getWorkflowEntriesForPreset(presetName).find((entry) => entry.workflow === 'preset_verification') || null;
}

function extractPresetVerificationMetrics(entry) {
  if (!entry) {
    return null;
  }
  const summary = summarizeVerificationRun(entry);
  const ragStep = summary.representativeSteps.find((step) => step.name === 'rag_verification') || null;
  const evalStep = summary.representativeSteps.find((step) => step.name === 'eval_verification') || null;
  return {
    status: entry.item?.status || '-',
    timestamp: entry.item?.timestamp || '-',
    sourceHitRate: parseMetricFromDetail(evalStep?.detail, 'source_hit_rate'),
    keywordHitRate: parseMetricFromDetail(evalStep?.detail, 'keyword_hit_rate'),
    totalCases: parseMetricFromDetail(evalStep?.detail, 'total_cases'),
    ragSourceCount: parseMetricFromDetail(ragStep?.detail, 'source_count'),
    summary,
  };
}

function summarizeValidationIssues(validation) {
  if (!validation) {
    return ['validation not run'];
  }

  const issues = [];
  (validation.warnings || []).forEach((warning) => issues.push(String(warning)));
  (validation.config_warnings || []).forEach((warning) => issues.push(`config: ${warning}`));
  (validation.path_checks || [])
    .filter((check) => check.required ? !check.exists : false)
    .forEach((check) => {
      issues.push(`${check.label || 'path_check'}: ${check.detail || 'missing path'} (${check.resolved_path || check.path || '-'})`);
    });
  (validation.service_checks || [])
    .filter((check) => check.required && check.status !== 'running')
    .forEach((check) => {
      issues.push(`${check.name || 'service'}: ${check.detail || check.status || 'unavailable'}`);
    });

  return issues.length > 0 ? issues : ['none'];
}

function summarizeWorkflowSteps(steps, limit = 6) {
  const normalized = Array.isArray(steps) ? steps : [];
  if (normalized.length === 0) {
    return ['none'];
  }

  return normalized.slice(0, limit).map((step) => `${step.name || '-'} [${step.status || '-'}]: ${step.detail || '-'}`);
}

function buildRegressionWatchSettingsLines() {
  return [
    `- source_hit_rate_drop_threshold: ${regressionWatchSourceHitDrop}`,
    `- monitor_presets: ${regressionWatchIncludePreset ? 'true' : 'false'}`,
    `- monitor_datasets: ${regressionWatchIncludeDataset ? 'true' : 'false'}`,
  ];
}

function getPresetRegressionAlerts(verificationTrends) {
  return (verificationTrends || []).filter((entry) => {
    const statusState = classifyStatusDelta(entry.latest?.status, entry.previous?.status);
    const sourceState = isSourceHitRegressionAlert(entry.latest?.sourceHitRate, entry.previous?.sourceHitRate);
    const ragState = classifyMetricDelta(entry.latest?.sourceCount, entry.previous?.sourceCount);
    return statusState.label === 'regressed' || sourceState || ragState.label === 'regressed';
  });
}

function getDatasetRegressionAlerts(evalDatasetTrends) {
  return (evalDatasetTrends || []).filter((entry) => {
    const sourceRegression = isSourceHitRegressionAlert(entry.latest?.sourceHitRate, entry.previous?.sourceHitRate);
    const latencyState = classifyMetricDelta(entry.latest?.averageLatencyMs, entry.previous?.averageLatencyMs, false);
    return sourceRegression || latencyState.label === 'regressed';
  });
}

function buildRegressionWatchExportPayload({presetAlerts, datasetAlerts}) {
  const presets = presetAlerts || [];
  const datasets = datasetAlerts || [];
  return {
    kind: 'workflow',
    title: 'Regression Watch Summary',
    content: [
      '# Regression Watch',
      '',
      '## Settings',
      ...buildRegressionWatchSettingsLines(),
      '',
      '## Preset Alerts',
      ...(presets.length > 0
        ? presets.map((entry) => [
          `### ${entry.presetName}`,
          `- status_delta: ${summarizeStatusDelta(entry.latest?.status, entry.previous?.status)}`,
          `- source_hit_rate_delta: ${formatMetricDelta(entry.latest?.sourceHitRate, entry.previous?.sourceHitRate)}`,
          `- rag_source_count_delta: ${formatMetricDelta(entry.latest?.sourceCount, entry.previous?.sourceCount)}`,
          `- latest_timestamp: ${entry.latest?.timestamp || '-'}`,
        ].join('\n'))
        : ['- none']),
      '',
      '## Dataset Alerts',
      ...(datasets.length > 0
        ? datasets.map((entry) => [
          `### ${entry.datasetPath}`,
          `- project: ${entry.latest?.project || '(default)'}`,
          `- source_hit_rate_delta: ${formatMetricDelta(entry.latest?.sourceHitRate, entry.previous?.sourceHitRate)}`,
          `- keyword_hit_rate_delta: ${formatMetricDelta(entry.latest?.keywordHitRate, entry.previous?.keywordHitRate)}`,
          `- average_latency_ms_delta: ${formatMetricDelta(entry.latest?.averageLatencyMs, entry.previous?.averageLatencyMs)}`,
          `- total_tokens_delta: ${formatMetricDelta(entry.latest?.totalTokens, entry.previous?.totalTokens)}`,
          `- latest_timestamp: ${entry.latest?.timestamp || '-'}`,
        ].join('\n'))
        : ['- none']),
    ].join('\n'),
    fileStem: 'regression-watch-summary',
  };
}

function buildPresetComparisonExportPayload() {
  const leftName = String(presetCompareLeftName || '').trim();
  const rightName = String(presetCompareRightName || '').trim();
  if (!leftName || !rightName) {
    return null;
  }
  const leftMetrics = extractPresetVerificationMetrics(getLatestVerificationEntryForPreset(leftName));
  const rightMetrics = extractPresetVerificationMetrics(getLatestVerificationEntryForPreset(rightName));
  if (!leftMetrics || !rightMetrics) {
    return null;
  }
  return {
    kind: 'workflow',
    title: `Preset Comparison (${leftName} vs ${rightName})`,
    content: [
      `# Preset Comparison: ${leftName} vs ${rightName}`,
      '',
      '## Left',
      `- status: ${leftMetrics.status}`,
      `- timestamp: ${leftMetrics.timestamp}`,
      `- source_hit_rate: ${leftMetrics.sourceHitRate ?? '-'}`,
      `- keyword_hit_rate: ${leftMetrics.keywordHitRate ?? '-'}`,
      `- rag_source_count: ${leftMetrics.ragSourceCount ?? '-'}`,
      `- total_cases: ${leftMetrics.totalCases ?? '-'}`,
      '',
      '## Right',
      `- status: ${rightMetrics.status}`,
      `- timestamp: ${rightMetrics.timestamp}`,
      `- source_hit_rate: ${rightMetrics.sourceHitRate ?? '-'}`,
      `- keyword_hit_rate: ${rightMetrics.keywordHitRate ?? '-'}`,
      `- rag_source_count: ${rightMetrics.ragSourceCount ?? '-'}`,
      `- total_cases: ${rightMetrics.totalCases ?? '-'}`,
      '',
      '## Delta (right minus left)',
      `- status_delta: ${summarizeStatusDelta(rightMetrics.status, leftMetrics.status)}`,
      `- source_hit_rate_delta: ${formatMetricDelta(rightMetrics.sourceHitRate, leftMetrics.sourceHitRate)}`,
      `- keyword_hit_rate_delta: ${formatMetricDelta(rightMetrics.keywordHitRate, leftMetrics.keywordHitRate)}`,
      `- rag_source_count_delta: ${formatMetricDelta(rightMetrics.ragSourceCount, leftMetrics.ragSourceCount)}`,
      `- total_cases_delta: ${formatMetricDelta(rightMetrics.totalCases, leftMetrics.totalCases)}`,
    ].join('\n'),
    fileStem: `preset-compare-${leftName.toLowerCase().replaceAll(/[^a-z0-9]+/g, '-')}-vs-${rightName.toLowerCase().replaceAll(/[^a-z0-9]+/g, '-')}`,
  };
}

function buildPresetExportPayload(preset) {
  const validation = currentPresetValidationMap.get(preset.name);
  const state = summarizeValidationState(validation);
  const workflowEntries = getWorkflowEntriesForPreset(preset.name);
  const latestWorkflow = workflowEntries[0] || null;
  const latestWorkflowItem = latestWorkflow?.item || null;
  const verificationEntries = workflowEntries.filter((entry) => entry.workflow === 'preset_verification');
  const latestVerification = verificationEntries[0] || null;
  const previousVerification = verificationEntries[1] || null;
  const latestVerificationItem = latestVerification?.item || null;
  const latestVerificationSummary = summarizeVerificationRun(latestVerification);
  const previousVerificationSummary = summarizeVerificationRun(previousVerification);
  const latestRagVerificationStep = latestVerificationSummary.representativeSteps.find((step) => step.name === 'rag_verification') || null;
  const previousRagVerificationStep = previousVerificationSummary.representativeSteps.find((step) => step.name === 'rag_verification') || null;
  const latestEvalVerificationStep = latestVerificationSummary.representativeSteps.find((step) => step.name === 'eval_verification') || null;
  const previousEvalVerificationStep = previousVerificationSummary.representativeSteps.find((step) => step.name === 'eval_verification') || null;
  const ops = summarizePresetOperations(validation, workflowEntries);
  const validationIssues = summarizeValidationIssues(validation);
  const serviceChecks = (validation?.service_checks || []).map((check) => [
    `- ${check.name || 'service'}`,
    `status=${check.status || '-'}`,
    `required=${check.required ? 'true' : 'false'}`,
    `startable=${canStartValidationService(check.name) ? 'true' : 'false'}`,
    check.detail || '',
  ].filter(Boolean).join(' | '));
  const pathChecks = (validation?.path_checks || []).map((check) => [
    `- ${check.label || 'path_check'}`,
    `required=${check.required ? 'true' : 'false'}`,
    `exists=${check.exists ? 'true' : 'false'}`,
    check.resolved_path || check.path || '-',
    check.detail || '',
  ].filter(Boolean).join(' | '));
  const verificationStepLines = latestVerificationSummary.representativeSteps.map((step) => {
    const representative = summarizeRepresentativeVerificationStep(step);
    const request = extractRepresentativeRequestFromVerificationStep(step);
    return [
      `- ${representative.label}`,
      `status=${step.status || '-'}`,
      request?.name ? `request=${request.name}` : '',
      representative.detail,
    ].filter(Boolean).join(' | ');
  });

  const lines = [
    `# Preset Summary: ${preset.name || 'preset'}`,
    '',
    '## Scope',
    `- runtime_profile: ${describePresetRuntimeProfile(preset.runtime_profile)}`,
    `- watch_project: ${preset.watch_project || '(default)'}`,
    `- watch_paths: ${summarizePresetPaths(preset.watch_paths)}`,
    `- watch_interval: ${preset.watch_interval || 2}`,
    `- ingest_project: ${preset.ingest_project || '(default)'}`,
    `- ingest_paths: ${summarizePresetPaths(preset.ingest_paths)}`,
    `- rag_project: ${preset.rag_project || '(default)'}`,
    `- rag_source_path: ${preset.rag_source_path || '-'}`,
    `- rag_top_k: ${preset.rag_top_k || 5}`,
    `- eval_project: ${preset.eval_project || '(default)'}`,
    `- eval_source_path: ${preset.eval_source_path || '-'}`,
    `- eval_dataset: ${preset.eval_dataset || 'configs/eval.sample.yaml'}`,
    `- eval_top_k: ${preset.eval_top_k || 5}`,
    `- eval_with_answer: ${preset.eval_with_answer ? 'true' : 'false'}`,
    '',
    '## Operational Status',
    `- validation_state: ${state.label}`,
    `- operations_state: ${ops.label}`,
    `- operations_detail: ${ops.detail}`,
    `- workflow_runs: ${workflowEntries.length}`,
    '',
    '## Validation',
    `- ready: ${validation?.ready ? 'true' : 'false'}`,
    `- valid: ${validation?.valid ? 'true' : 'false'}`,
    `- config_warnings: ${(validation?.config_warnings || []).length}`,
    `- issues: ${validationIssues.join(' ; ')}`,
    '',
    '### Path Checks',
    ...(pathChecks.length > 0 ? pathChecks : ['- none']),
    '',
    '### Service Checks',
    ...(serviceChecks.length > 0 ? serviceChecks : ['- none']),
    '',
    '## Smoke Policy',
    `- ${summarizeSmokePolicy(preset)}`,
    '',
    '## Regression Watch Settings',
    ...buildRegressionWatchSettingsLines(),
    '',
    '## Latest Workflow',
    `- latest_type: ${latestWorkflow?.workflow || '-'}`,
    `- latest_status: ${latestWorkflowItem?.status || '-'}`,
    `- latest_timestamp: ${latestWorkflowItem?.timestamp || '-'}`,
    `- latest_summary: ${latestWorkflowItem?.summary || '-'}`,
    `- latest_detail: ${latestWorkflowItem?.detail || '-'}`,
    '',
    '### Latest Workflow Steps',
    ...summarizeWorkflowSteps(latestWorkflow?.steps),
    '',
    '## Latest Verification',
    `- verification_status: ${latestVerificationItem?.status || '-'}`,
    `- verification_timestamp: ${latestVerificationItem?.timestamp || '-'}`,
    `- verification_summary: ${latestVerificationItem?.summary || '-'}`,
    `- verification_detail: ${latestVerificationItem?.detail || '-'}`,
    `- representative_runs: ${latestVerificationSummary.total}`,
    `- ok: ${latestVerificationSummary.okCount}`,
    `- failed: ${latestVerificationSummary.failedCount}`,
    `- skipped: ${latestVerificationSummary.skippedCount}`,
    `- previous_verification_status: ${previousVerification?.item?.status || '-'}`,
    `- previous_verification_timestamp: ${previousVerification?.item?.timestamp || '-'}`,
    `- status_delta: ${summarizeStatusDelta(latestVerificationItem?.status, previousVerification?.item?.status)}`,
    `- status_delta_judgement: ${classifyStatusDelta(latestVerificationItem?.status, previousVerification?.item?.status).label}`,
    `- source_hit_rate_delta: ${formatMetricDelta(parseMetricFromDetail(latestEvalVerificationStep?.detail, 'source_hit_rate'), parseMetricFromDetail(previousEvalVerificationStep?.detail, 'source_hit_rate'))}`,
    `- source_hit_rate_delta_judgement: ${classifyMetricDelta(parseMetricFromDetail(latestEvalVerificationStep?.detail, 'source_hit_rate'), parseMetricFromDetail(previousEvalVerificationStep?.detail, 'source_hit_rate')).label}`,
    `- keyword_hit_rate_delta: ${formatMetricDelta(parseMetricFromDetail(latestEvalVerificationStep?.detail, 'keyword_hit_rate'), parseMetricFromDetail(previousEvalVerificationStep?.detail, 'keyword_hit_rate'))}`,
    `- keyword_hit_rate_delta_judgement: ${classifyMetricDelta(parseMetricFromDetail(latestEvalVerificationStep?.detail, 'keyword_hit_rate'), parseMetricFromDetail(previousEvalVerificationStep?.detail, 'keyword_hit_rate')).label}`,
    `- rag_source_count_delta: ${formatMetricDelta(parseMetricFromDetail(latestRagVerificationStep?.detail, 'source_count'), parseMetricFromDetail(previousRagVerificationStep?.detail, 'source_count'))}`,
    `- rag_source_count_delta_judgement: ${classifyMetricDelta(parseMetricFromDetail(latestRagVerificationStep?.detail, 'source_count'), parseMetricFromDetail(previousRagVerificationStep?.detail, 'source_count')).label}`,
    `- first_failure: ${
      latestVerificationSummary.firstFailureSummary
        ? `${latestVerificationSummary.firstFailureSummary.label}: ${latestVerificationSummary.firstFailureSummary.detail}`
        : '-'
    }`,
    '',
    '### Verification Representative Steps',
    ...(verificationStepLines.length > 0 ? verificationStepLines : ['- none']),
  ];

  return {
    kind: 'preset',
    title: `Preset Summary (${preset.name || 'preset'})`,
    content: lines.join('\n'),
    fileStem: `preset-${String(preset.name || 'preset').toLowerCase().replaceAll(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') || 'preset'}-summary`,
  };
}

function isPresetExpanded(name) {
  return expandedPresetNames.has(String(name || '').trim());
}

function renderPresetValidationCard(targetId, response) {
  const container = document.getElementById(targetId);
  if (!response) {
    container.innerHTML = '<div class="runtime-result-card"><div class="runtime-result-text">No preset validation yet.</div></div>';
    return;
  }

  const runtimeProfile = summarizePresetRuntimeProfile(latestValidatedPreset?.runtime_profile);
  const warnings = Array.isArray(response.warnings) ? response.warnings : [];
  const configWarnings = Array.isArray(response.config_warnings) ? response.config_warnings : [];
  const pathChecks = Array.isArray(response.path_checks) ? response.path_checks : [];
  const serviceChecks = Array.isArray(response.service_checks) ? response.service_checks : [];
  const missingStartableServices = serviceChecks.filter((check) => check.required && check.status !== 'running' && canStartValidationService(check.name));

  container.innerHTML = `
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">${escapeHtml(response.preset_name || 'Preset Validation')}</span>
        <span class="runtime-pill ${response.ready ? 'optional' : response.valid ? 'neutral' : 'required'}">${escapeHtml(response.ready ? 'ready' : response.valid ? 'valid' : 'invalid')}</span>
      </div>
      <div class="runtime-result-meta">runtime_profile=<span class="runtime-pill ${runtimeProfile.pillClass}">${escapeHtml(runtimeProfile.label)}</span></div>
      <div class="runtime-result-meta">${escapeHtml(runtimeProfile.validationNote)}</div>
      ${
        runtimeProfile.key !== 'current'
          ? `
            <div class="actions">
              <button class="ghost-btn validation-apply-runtime-profile-btn">Apply Runtime Profile</button>
            </div>
          `
          : ''
      }
      <div class="runtime-result-meta">required_services=${escapeHtml((response.required_services || []).join(', ') || '-')}</div>
      <div class="runtime-result-meta">optional_services=${escapeHtml((response.optional_services || []).join(', ') || '-')}</div>
      ${
        response.valid && !response.ready
          ? `
            <div class="actions">
              <button class="ghost-btn validation-start-services-btn">Start Required Services</button>
              ${missingStartableServices.map((check) => `
                <button class="ghost-btn validation-start-service-btn" data-service-name="${escapeHtml(check.name || '')}">Start ${escapeHtml(check.name || 'service')}</button>
              `).join('')}
            </div>
          `
          : ''
      }
      <div class="runtime-result-list">
        <div class="runtime-result-item">
          <div class="runtime-result-head">
            <span class="runtime-result-name">Definition</span>
            <span class="runtime-pill ${response.valid ? 'optional' : 'required'}">${escapeHtml(response.valid ? 'ok' : 'error')}</span>
          </div>
          <div class="runtime-result-text">${warnings.length === 0 ? 'No preset definition warnings.' : escapeHtml(warnings.join('\n'))}</div>
        </div>
        <div class="runtime-result-item">
          <div class="runtime-result-head">
            <span class="runtime-result-name">Runtime Config</span>
            <span class="runtime-pill ${configWarnings.length === 0 ? 'optional' : 'required'}">${escapeHtml(configWarnings.length === 0 ? 'ok' : 'warning')}</span>
          </div>
          <div class="runtime-result-text">${configWarnings.length === 0 ? 'No config warnings.' : escapeHtml(configWarnings.join('\n'))}</div>
        </div>
        <div class="runtime-result-item">
          <div class="runtime-result-head">
            <span class="runtime-result-name">Path Checks</span>
            <span class="runtime-pill neutral">${escapeHtml(String(pathChecks.length))}</span>
          </div>
          <div class="runtime-result-list">
            ${pathChecks.map((check) => `
              <div class="runtime-result-item">
                <div class="runtime-result-head">
                  <span class="runtime-result-name">${escapeHtml(check.label || '-')}</span>
                  <span class="runtime-pill ${check.exists ? 'optional' : check.required ? 'required' : 'neutral'}">${escapeHtml(check.exists ? 'exists' : check.required ? 'missing' : 'optional')}</span>
                </div>
                <div class="runtime-result-meta">${escapeHtml(check.path || '-')}</div>
                <div class="runtime-result-meta">${escapeHtml(check.resolved_path || '-')}</div>
                <div class="runtime-result-text">${escapeHtml(check.detail || '-')}</div>
              </div>
            `).join('') || '<div class="runtime-result-text">No path checks recorded.</div>'}
          </div>
        </div>
        <div class="runtime-result-item">
          <div class="runtime-result-head">
            <span class="runtime-result-name">Service Checks</span>
            <span class="runtime-pill neutral">${escapeHtml(String(serviceChecks.length))}</span>
          </div>
          <div class="runtime-result-list">
            ${serviceChecks.map((check) => `
              <div class="runtime-result-item">
                <div class="runtime-result-head">
                  <span class="runtime-result-name">${escapeHtml(check.name || '-')}</span>
                  <span class="runtime-pill ${check.status === 'running' ? 'optional' : check.required ? 'required' : 'neutral'}">${escapeHtml(check.status || 'unknown')}</span>
                </div>
                <div class="runtime-result-meta">${escapeHtml(check.required ? 'required' : 'optional')}</div>
                <div class="runtime-result-text">${escapeHtml(check.detail || '-')}</div>
              </div>
            `).join('') || '<div class="runtime-result-text">No service checks recorded.</div>'}
          </div>
        </div>
      </div>
    </div>
  `;
}

function renderPresetValidation(response) {
  latestValidationResponse = response || null;
  renderPresetValidationCard('overview-preset-validation', response);
  renderPresetValidationCard('runtime-preset-validation', response);
}

async function validateAndRenderPreset(preset) {
  if (!preset) {
    latestValidatedPreset = null;
    renderPresetValidation(null);
    return null;
  }

  const token = ++latestPresetValidationToken;
  try {
    const response = await ValidateProjectPreset(preset);
    if (token !== latestPresetValidationToken) {
      return response;
    }
    latestValidatedPreset = typeof structuredClone === 'function' ? structuredClone(preset) : JSON.parse(JSON.stringify(preset));
    renderPresetValidation(response);
    return response;
  } catch (error) {
    const fallbackResponse = {
      preset_name: preset.name || '(unnamed preset)',
      valid: false,
      ready: false,
      warnings: [String(error)],
      config_warnings: [],
      required_services: [],
      optional_services: [],
      path_checks: [],
      service_checks: [],
    };
    if (token !== latestPresetValidationToken) {
      return fallbackResponse;
    }
    latestValidatedPreset = typeof structuredClone === 'function' ? structuredClone(preset) : JSON.parse(JSON.stringify(preset));
    renderPresetValidation(fallbackResponse);
    return fallbackResponse;
  }
}

async function refreshSelectedPresetValidationByName(name) {
  const normalized = String(name || '').trim();
  if (!normalized) {
    renderPresetValidation(null);
    return;
  }
  const preset = currentPresets.find((item) => item.name === normalized);
  if (!preset) {
    renderPresetValidation({
      preset_name: normalized,
      valid: false,
      ready: false,
      warnings: [`Preset not found: ${normalized}`],
      config_warnings: [],
      required_services: [],
      optional_services: [],
      path_checks: [],
      service_checks: [],
    });
    return;
  }
  await validateAndRenderPreset(preset);
}

async function refreshPresetValidationSnapshots(presets) {
  if (!Array.isArray(presets) || presets.length === 0) {
    currentPresetValidationMap = new Map();
    return;
  }

  const results = await Promise.all(
    presets.map(async (preset) => {
      try {
        const response = await ValidateProjectPreset(preset);
        return [preset.name, response];
      } catch (error) {
        return [preset.name, {
          preset_name: preset.name || '(unnamed preset)',
          valid: false,
          ready: false,
          warnings: [String(error)],
          config_warnings: [],
          required_services: [],
          optional_services: [],
          path_checks: [],
          service_checks: [],
        }];
      }
    }),
  );

  currentPresetValidationMap = new Map(results);
}

function normalizeBatchWorkflowState(state) {
  if (!state) {
    return null;
  }
  return {
    workflowLabel: state.workflow_label || state.workflowLabel || '',
    status: state.status || '',
    running: state.running === true,
    cancelRequested: state.cancel_requested === true || state.cancelRequested === true,
    results: Array.isArray(state.results)
      ? state.results.map((result) => ({
        presetName: result.preset_name || result.presetName || '',
        status: result.status || '',
        detail: result.detail || '',
      }))
      : [],
  };
}

function normalizeBatchPresetNames(presetNames) {
  return (Array.isArray(presetNames) ? presetNames : [])
    .map((name) => String(name || '').trim())
    .filter(Boolean);
}

async function persistBatchPresetSelection() {
  const selection = Array.from(selectedBatchPresetNames).map((name) => String(name || '').trim()).filter(Boolean);
  try {
    if (selection.length === 0) {
      await ClearBatchPresetSelection();
      window.localStorage.removeItem(BATCH_PRESET_SELECTION_KEY);
      return;
    }
    await SetBatchPresetSelection(selection);
    window.localStorage.removeItem(BATCH_PRESET_SELECTION_KEY);
  } catch (error) {
    try {
      window.localStorage.setItem(
        BATCH_PRESET_SELECTION_KEY,
        JSON.stringify(selection),
      );
    } catch (storageError) {
      console.warn('failed to persist batch preset selection', storageError);
    }
  }
}

async function restoreBatchPresetSelection() {
  try {
    selectedBatchPresetNames = new Set(normalizeBatchPresetNames(await GetBatchPresetSelection()));
  } catch (error) {
    try {
      const payload = window.localStorage.getItem(BATCH_PRESET_SELECTION_KEY);
      if (!payload) {
        selectedBatchPresetNames = new Set();
        return;
      }
      selectedBatchPresetNames = new Set(normalizeBatchPresetNames(JSON.parse(payload)));
      void persistBatchPresetSelection();
    } catch (storageError) {
      selectedBatchPresetNames = new Set();
    }
  }
}

function syncBatchPresetSelectionFromState(state, {preferState = false} = {}) {
  const namesFromState = normalizeBatchPresetNames((state?.results || []).map((result) => result?.presetName));
  if (namesFromState.length === 0) {
    return;
  }
  if (preferState || selectedBatchPresetNames.size === 0 || state?.running) {
    selectedBatchPresetNames = new Set(namesFromState);
    void persistBatchPresetSelection();
  }
}

function summarizeBatchPresetNames(presetNames, limit = 4) {
  const names = normalizeBatchPresetNames(presetNames);
  if (names.length === 0) {
    return '-';
  }
  if (names.length <= limit) {
    return names.join(', ');
  }
  return `${names.slice(0, limit).join(', ')}, +${names.length - limit} more`;
}

function summarizeBatchResultCounts(results) {
  const counts = {
    ok: 0,
    error: 0,
    running: 0,
    queued: 0,
    cancelled: 0,
  };
  for (const result of Array.isArray(results) ? results : []) {
    const status = String(result?.status || '').trim();
    if (status && Object.hasOwn(counts, status)) {
      counts[status] += 1;
    }
  }
  return counts;
}

function renderSinglePresetBatchActionSection(buttonPrefix, presetName) {
  return renderSinglePresetBatchActionButtons({buttonPrefix, presetName, escapeHtml});
}

function renderPresetPrimaryActionSection(buttonPrefix, preset) {
  return renderPresetPrimaryActionButtons({
    buttonPrefix,
    presetName: preset?.name || '',
    runtimeProfile: normalizePresetRuntimeProfile(preset?.runtime_profile),
    escapeHtml,
  });
}

function renderPresetBatchSelectionButtonsSection(buttonPrefix, preset) {
  const presetName = String(preset?.name || '').trim();
  return renderPresetBatchSelectionButtons({
    buttonPrefix,
    presetName,
    isSelected: selectedBatchPresetNames.has(presetName),
    escapeHtml,
  });
}

function renderPresetBatchSelectionMetaSection(preset) {
  const presetName = String(preset?.name || '').trim();
  return renderPresetBatchSelectionMeta({
    isSelected: selectedBatchPresetNames.has(presetName),
    escapeHtml,
  });
}

function renderPresetWorkflowShortcutButtons(buttonPrefix, presetName) {
  const normalizedPrefix = String(buttonPrefix || '').trim();
  const escapedPresetName = escapeHtml(String(presetName || '').trim());
  return `
    <button class="ghost-btn ${normalizedPrefix}-run-smoke-btn" data-preset-name="${escapedPresetName}">Run Smoke</button>
    <button class="ghost-btn ${normalizedPrefix}-run-watch-btn" data-preset-name="${escapedPresetName}">Run Watch</button>
    <button class="ghost-btn ${normalizedPrefix}-run-ingest-btn" data-preset-name="${escapedPresetName}">Run Ingest</button>
    <button class="ghost-btn ${normalizedPrefix}-run-eval-btn" data-preset-name="${escapedPresetName}">Run Eval</button>
    <button class="ghost-btn ${normalizedPrefix}-run-ingest-eval-btn" data-preset-name="${escapedPresetName}">Run Ingest + Eval</button>
  `;
}

async function persistBatchWorkflowState() {
  if (!batchWorkflowState) {
    await ClearBatchWorkflowState();
    return;
  }
  await SetBatchWorkflowState({
    workflow_label: batchWorkflowState.workflowLabel || '',
    status: batchWorkflowState.status || '',
    running: batchWorkflowState.running === true,
    cancel_requested: batchWorkflowState.cancelRequested === true,
    results: (batchWorkflowState.results || []).map((result) => ({
      preset_name: result.presetName || '',
      status: result.status || '',
      detail: result.detail || '',
    })),
  });
}

async function restoreBatchWorkflowState() {
  try {
    batchWorkflowState = normalizeBatchWorkflowState(await GetBatchWorkflowState());
    syncBatchPresetSelectionFromState(batchWorkflowState);
    renderBatchPresetOutput();
    renderPresetCatalog(currentPresets);
  } catch (error) {
    batchWorkflowState = null;
    renderBatchPresetOutput();
  }
}

function renderBatchPresetOutput() {
  const container = document.getElementById('batch-preset-output');
  if (batchWorkflowState) {
    const selectedNames = normalizeBatchPresetNames(
      batchWorkflowState.results?.map((result) => result?.presetName),
    );
    const resultCounts = summarizeBatchResultCounts(batchWorkflowState.results);
    const tone = batchWorkflowState.status === 'completed'
      ? 'optional'
      : batchWorkflowState.status === 'running' || batchWorkflowState.status === 'cancelling'
        ? 'neutral'
        : batchWorkflowState.status === 'cancelled'
          ? 'neutral'
          : 'required';
    container.innerHTML = `
      <div class="runtime-result-card">
        <div class="runtime-result-head">
          <span class="runtime-result-title">${escapeHtml(batchWorkflowState.workflowLabel || 'Batch Preset Workflow')}</span>
          <span class="runtime-pill ${tone}">${escapeHtml(batchWorkflowState.status || 'idle')}</span>
        </div>
        <div class="runtime-result-meta">execution_mode=sequential | cancellation=${escapeHtml(batchWorkflowState.running ? 'cooperative' : 'idle')}</div>
        <div class="runtime-result-meta">selected_presets=${escapeHtml(String(selectedNames.length || selectedBatchPresetNames.size || 0))} | targets=${escapeHtml(summarizeBatchPresetNames(selectedNames.length > 0 ? selectedNames : Array.from(selectedBatchPresetNames)))}</div>
        <div class="runtime-result-meta">ok=${escapeHtml(String(resultCounts.ok))} | error=${escapeHtml(String(resultCounts.error))} | running=${escapeHtml(String(resultCounts.running))} | queued=${escapeHtml(String(resultCounts.queued))} | cancelled=${escapeHtml(String(resultCounts.cancelled))}</div>
        ${
          batchWorkflowState.running
            ? `
              <div class="actions">
                <button class="ghost-btn batch-cancel-btn" ${batchWorkflowState.cancelRequested ? 'disabled' : ''}>
                  ${batchWorkflowState.cancelRequested ? 'Cancellation Requested' : 'Cancel After Current Preset'}
                </button>
              </div>
            `
            : `
              <div class="actions">
                <button class="ghost-btn batch-clear-btn">Clear Batch Result</button>
              </div>
            `
        }
        <div class="runtime-result-list">
          ${(batchWorkflowState.results || []).map((result) => `
            <div class="runtime-result-item">
              <div class="runtime-result-head">
                <span class="runtime-result-name">${escapeHtml(result.presetName || '-')}</span>
                <span class="runtime-pill ${
                  result.status === 'ok' ? 'optional' : result.status === 'running' || result.status === 'queued' ? 'neutral' : result.status === 'cancelled' ? 'neutral' : 'required'
                }">${escapeHtml(result.status || '-')}</span>
              </div>
              <div class="runtime-result-text">${escapeHtml(result.detail || '-')}</div>
            </div>
          `).join('')}
        </div>
      </div>
    `;
    return;
  }

  const selectedPresets = currentPresets.filter((preset) => selectedBatchPresetNames.has(preset.name));
  if (selectedPresets.length === 0) {
    container.innerHTML = '<div class="runtime-result-card"><div class="runtime-result-text">No presets selected for batch execution. Batch workflows run sequentially, and cancellation only applies between presets.</div></div>';
    return;
  }

  container.innerHTML = `
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">Selected Batch Presets</span>
        <span class="runtime-pill neutral">${escapeHtml(String(selectedPresets.length))}</span>
      </div>
      <div class="runtime-result-meta">execution_mode=sequential | cancellation=between presets only</div>
      <div class="runtime-result-meta">targets=${escapeHtml(summarizeBatchPresetNames(selectedPresets.map((preset) => preset.name), 6))}</div>
      <div class="runtime-result-list">
        ${selectedPresets.map((preset) => {
          const runtimeProfile = summarizeRuntimeProfileMatch(preset.runtime_profile);
          const latestVerification = getLatestVerificationEntryForPreset(preset.name);
          const latestWorkflow = getWorkflowEntriesForPreset(preset.name)[0] || null;
          return `
            <div class="runtime-result-item">
              <div class="runtime-result-head">
                <span class="runtime-result-name">${escapeHtml(preset.name || '-')}</span>
                <span class="runtime-pill ${runtimeProfile.pillClass}">${escapeHtml(runtimeProfile.label)}</span>
              </div>
              <div class="runtime-result-meta">current_runtime=<span class="runtime-pill ${runtimeProfile.matchPillClass}">${escapeHtml(runtimeProfile.matchLabel)}</span></div>
              <div class="runtime-result-meta">latest_workflow=${escapeHtml(latestWorkflow?.workflow || '-')} | latest_status=${escapeHtml(latestWorkflow?.item?.status || '-')}</div>
              <div class="runtime-result-meta">latest_verification=${escapeHtml(latestVerification?.item?.status || '-')} | timestamp=${escapeHtml(latestVerification?.item?.timestamp || '-')}</div>
            </div>
          `;
        }).join('')}
      </div>
    </div>
  `;
}

function renderPresetComparison() {
  const container = document.getElementById('preset-compare-output');
  const leftName = String(presetCompareLeftName || '').trim();
  const rightName = String(presetCompareRightName || '').trim();

  if (!leftName || !rightName) {
    container.innerHTML = '<div class="runtime-result-card"><div class="runtime-result-text">Select two presets to compare their latest verification snapshots.</div></div>';
    return;
  }

  const leftPreset = currentPresets.find((item) => item.name === leftName) || null;
  const rightPreset = currentPresets.find((item) => item.name === rightName) || null;
  if (!leftPreset || !rightPreset) {
    container.innerHTML = '<div class="runtime-result-card"><div class="runtime-result-text">One or both comparison presets are no longer available.</div></div>';
    return;
  }

  const leftVerification = getLatestVerificationEntryForPreset(leftName);
  const rightVerification = getLatestVerificationEntryForPreset(rightName);
  const leftMetrics = extractPresetVerificationMetrics(leftVerification);
  const rightMetrics = extractPresetVerificationMetrics(rightVerification);
  if (!leftMetrics || !rightMetrics) {
    container.innerHTML = `
      <div class="runtime-result-card">
        <div class="runtime-result-head">
          <span class="runtime-result-title">Preset Comparison</span>
          <span class="runtime-pill neutral">waiting</span>
        </div>
        <div class="runtime-result-text">Both presets need at least one verification run before they can be compared.</div>
        <div class="runtime-result-meta">${escapeHtml(leftName)}=${escapeHtml(leftVerification?.item?.status || 'no verification')} | ${escapeHtml(rightName)}=${escapeHtml(rightVerification?.item?.status || 'no verification')}</div>
      </div>
    `;
    return;
  }

  const metrics = [
    {
      label: 'Verification Status',
      left: leftMetrics.status,
      right: rightMetrics.status,
      delta: summarizeStatusDelta(rightMetrics.status, leftMetrics.status),
      state: classifyStatusDelta(rightMetrics.status, leftMetrics.status),
    },
    {
      label: 'Source Hit Rate',
      left: leftMetrics.sourceHitRate,
      right: rightMetrics.sourceHitRate,
      delta: formatMetricDelta(rightMetrics.sourceHitRate, leftMetrics.sourceHitRate),
      state: classifyMetricDelta(rightMetrics.sourceHitRate, leftMetrics.sourceHitRate),
    },
    {
      label: 'Keyword Hit Rate',
      left: leftMetrics.keywordHitRate,
      right: rightMetrics.keywordHitRate,
      delta: formatMetricDelta(rightMetrics.keywordHitRate, leftMetrics.keywordHitRate),
      state: classifyMetricDelta(rightMetrics.keywordHitRate, leftMetrics.keywordHitRate),
    },
    {
      label: 'RAG Source Count',
      left: leftMetrics.ragSourceCount,
      right: rightMetrics.ragSourceCount,
      delta: formatMetricDelta(rightMetrics.ragSourceCount, leftMetrics.ragSourceCount),
      state: classifyMetricDelta(rightMetrics.ragSourceCount, leftMetrics.ragSourceCount),
    },
    {
      label: 'Eval Cases',
      left: leftMetrics.totalCases,
      right: rightMetrics.totalCases,
      delta: formatMetricDelta(rightMetrics.totalCases, leftMetrics.totalCases),
      state: classifyMetricDelta(rightMetrics.totalCases, leftMetrics.totalCases),
    },
  ];

  container.innerHTML = `
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">${escapeHtml(leftName)} vs ${escapeHtml(rightName)}</span>
        <span class="runtime-pill neutral">right minus left</span>
      </div>
      <div class="runtime-summary-grid">
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">${escapeHtml(leftName)}</div>
          <div class="runtime-result-text">status=${escapeHtml(leftMetrics.status)}</div>
          <div class="runtime-result-meta">timestamp=${escapeHtml(leftMetrics.timestamp)}</div>
          <div class="runtime-result-meta">runtime_profile=${escapeHtml(describePresetRuntimeProfile(leftPreset.runtime_profile))}</div>
        </div>
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">${escapeHtml(rightName)}</div>
          <div class="runtime-result-text">status=${escapeHtml(rightMetrics.status)}</div>
          <div class="runtime-result-meta">timestamp=${escapeHtml(rightMetrics.timestamp)}</div>
          <div class="runtime-result-meta">runtime_profile=${escapeHtml(describePresetRuntimeProfile(rightPreset.runtime_profile))}</div>
        </div>
      </div>
      <div class="runtime-result-list">
        ${metrics.map((metric) => `
          <div class="runtime-result-item">
            <div class="runtime-result-head">
              <span class="runtime-result-name">${escapeHtml(metric.label)}</span>
              <span class="runtime-pill ${metric.state?.pillClass || 'neutral'}">${escapeHtml(metric.state?.label || 'n/a')}</span>
            </div>
            <div class="runtime-result-meta">left=${escapeHtml(metric.left != null ? String(metric.left) : '-')} | right=${escapeHtml(metric.right != null ? String(metric.right) : '-')}</div>
            <div class="runtime-result-text">delta=${escapeHtml(metric.delta || '-')}</div>
          </div>
        `).join('')}
      </div>
    </div>
  `;
}

function renderOverviewPresetRuntimeHint(name) {
  const container = document.getElementById('overview-preset-runtime-hint');
  const normalized = String(name || '').trim();
  if (!normalized) {
    container.innerHTML = '<div class="runtime-result-card"><div class="runtime-result-text">Select a preset to see runtime profile guidance here.</div></div>';
    return;
  }

  const preset = currentPresets.find((item) => item.name === normalized);
  if (!preset) {
    container.innerHTML = `<div class="runtime-result-card"><div class="runtime-result-text">Preset not found: ${escapeHtml(normalized)}</div></div>`;
    return;
  }

  const runtimeProfile = summarizeRuntimeProfileMatch(preset.runtime_profile);
  const isCurrent = runtimeProfile.key === 'current';
  container.innerHTML = renderOverviewPresetRuntimeHintCard({
    presetName: preset.name || '',
    runtimeProfile,
    smokeEnabled: preset.workflow_run_smoke === true,
    isCurrent,
    escapeHtml,
  });
}

function renderSelectedPresetPreviewByName(name) {
  const container = document.getElementById('selected-preset-preview');
  const normalized = String(name || '').trim();
  if (!normalized) {
    container.innerHTML = '<div class="runtime-result-card"><div class="runtime-result-text">Select a preset from Overview or Runtime to preview its workflow scope here.</div></div>';
    return;
  }

  const preset = currentPresets.find((item) => item.name === normalized);
  if (!preset) {
    container.innerHTML = `<div class="runtime-result-card"><div class="runtime-result-text">Preset not found: ${escapeHtml(normalized)}</div></div>`;
    return;
  }

  const runtimeMatch = summarizeRuntimeProfileMatch(preset.runtime_profile);
  const loadRequestButtonsHtml = [
    preset.chat_request_name ? `<button class="ghost-btn preset-load-request-btn" data-request-kind="chat" data-request-name="${escapeHtml(preset.chat_request_name)}">Load Chat</button>` : '',
    preset.ingest_request_name ? `<button class="ghost-btn preset-load-request-btn" data-request-kind="ingest" data-request-name="${escapeHtml(preset.ingest_request_name)}">Load Ingest</button>` : '',
    preset.rag_request_name ? `<button class="ghost-btn preset-load-request-btn" data-request-kind="rag" data-request-name="${escapeHtml(preset.rag_request_name)}">Load RAG</button>` : '',
    preset.eval_request_name ? `<button class="ghost-btn preset-load-request-btn" data-request-kind="eval" data-request-name="${escapeHtml(preset.eval_request_name)}">Load Eval</button>` : '',
  ].join('');
  container.innerHTML = renderSelectedPresetPreviewCard({
    preset,
    watchPathsSummary: summarizePresetPaths(preset.watch_paths),
    ingestPathsSummary: summarizePresetPaths(preset.ingest_paths),
    runtimeProfileDescription: describePresetRuntimeProfile(preset.runtime_profile),
    runtimeMatch,
    smokePolicy: summarizeSmokePolicy(preset),
    batchSelectionMeta: renderPresetBatchSelectionMetaSection(preset),
    primaryActionsHtml: renderPresetPrimaryActionSection('preset', preset),
    batchSelectionButtonsHtml: renderPresetBatchSelectionButtonsSection('preset', preset),
    singlePresetBatchActionsHtml: renderSinglePresetBatchActionSection('preset', preset.name || ''),
    workflowShortcutButtonsHtml: renderPresetWorkflowShortcutButtons('preset', preset.name || ''),
    verificationButtonHtml: preset.chat_request_name || preset.ingest_request_name || preset.rag_request_name || preset.eval_request_name
      ? `<button class="primary-btn preset-run-verification-btn" data-preset-name="${escapeHtml(preset.name || '')}">Run Verification</button>`
      : '',
    loadRequestButtonsHtml,
    escapeHtml,
  });
}

function renderSelectedPresetWorkflowByName(name) {
  const container = document.getElementById('selected-preset-workflow');
  const normalized = String(name || '').trim();
  if (!normalized) {
    container.innerHTML = '<div class="runtime-result-card"><div class="runtime-result-text">Select a preset to inspect its latest workflow run here.</div></div>';
    return;
  }

  const preset = currentPresets.find((item) => item.name === normalized);
  if (!preset) {
    container.innerHTML = `<div class="runtime-result-card"><div class="runtime-result-text">Preset not found: ${escapeHtml(normalized)}</div></div>`;
    return;
  }

  const entries = getWorkflowEntriesForPreset(normalized);
  const latest = entries[0] || null;
  const latestItem = latest?.item || null;
  const latestFailure = entries.find((entry) => entry.item.status === 'error') || null;
  const latestVerification = entries.find((entry) => entry.workflow === 'preset_verification') || null;
  const issues = summarizeValidationIssues(currentPresetValidationMap.get(normalized));
  let latestPayload = null;
  if (latestItem?.payload) {
    try {
      latestPayload = JSON.parse(latestItem.payload);
    } catch (error) {
      latestPayload = null;
    }
  }
  const linkedOriginalItem = latestPayload?.recovery_for_history_id ? findHistoryItemById(latestPayload.recovery_for_history_id) : null;
  const latestVerificationSummary = summarizeVerificationRun(latestVerification);

  if (!latestItem) {
    container.innerHTML = renderSelectedPresetWorkflowEmptyCard({
      presetName: normalized,
      runtimeProfileDescription: describePresetRuntimeProfile(preset.runtime_profile),
      runtimeMatch: summarizeRuntimeProfileMatch(preset.runtime_profile),
      issuesText: issues.join(' | '),
      batchSelectionMeta: renderPresetBatchSelectionMetaSection(preset),
      actionsHtml: `
        ${renderPresetPrimaryActionSection('selected-preset', preset)}
        ${renderPresetBatchSelectionButtonsSection('selected-preset', preset)}
        ${renderSinglePresetBatchActionSection('selected-preset', preset.name || '')}
        ${renderPresetWorkflowShortcutButtons('selected-preset', preset.name || '')}
      `,
      escapeHtml,
    });
    return;
  }

  const representativeStepItemsHtml = latestVerificationSummary.representativeSteps.map((step) => {
    const representative = summarizeRepresentativeVerificationStep(step);
    const request = extractRepresentativeRequestFromVerificationStep(step);
    return `
      <div class="runtime-result-item">
        <div class="runtime-result-head">
          <span class="runtime-result-name">${escapeHtml(representative.label)}</span>
          <span class="runtime-pill ${
            step.status === 'ok' ? 'optional' : step.status === 'running' ? 'neutral' : step.status === 'skipped' ? 'neutral' : 'required'
          }">${escapeHtml(step.status || '-')}</span>
        </div>
        <div class="runtime-result-text">${escapeHtml(representative.detail)}</div>
        ${
          request
            ? `
              <div class="actions">
                <button
                  class="ghost-btn selected-preset-load-request-btn"
                  data-request-kind="${escapeHtml(request.kind)}"
                  data-request-name="${escapeHtml(request.name)}"
                >Load ${escapeHtml(request.kind)}</button>
              </div>
            `
            : ''
        }
      </div>
    `;
  }).join('');

  const verificationSectionHtml = renderSelectedPresetWorkflowVerificationSection({
    latestVerification,
    latestVerificationSummary,
    representativeStepItemsHtml,
    escapeHtml,
  });

  const actionsHtml = `
    ${renderPresetPrimaryActionSection('selected-preset', preset)}
    ${renderPresetBatchSelectionButtonsSection('selected-preset', preset)}
    <button class="ghost-btn selected-preset-rerun-btn" data-history-id="${escapeHtml(latestItem.id)}">Rerun Latest</button>
    <button class="ghost-btn selected-preset-export-btn" data-history-id="${escapeHtml(latestItem.id)}">Export Latest Workflow</button>
    ${renderSinglePresetBatchActionSection('selected-preset', preset.name || '')}
    ${renderPresetWorkflowShortcutButtons('selected-preset', preset.name || '')}
    ${
      linkedOriginalItem
        ? `<button class="ghost-btn selected-preset-retry-original-btn" data-history-id="${escapeHtml(linkedOriginalItem.id)}">Retry Original Workflow</button>`
        : ''
    }
    ${
      latestFailure && latestFailure.item.id !== latestItem.id
        ? `<button class="ghost-btn selected-preset-show-failure-btn" data-history-id="${escapeHtml(latestFailure.item.id)}">Show Last Failure</button>`
        : ''
    }
  `;

  const stepItemsHtml = latest.steps.map((step, index) => {
    const action = getRecoveryActionForWorkflowStep(step);
    return `
      <div class="runtime-result-item">
        <div class="runtime-result-head">
          <span class="runtime-result-name">Step ${index + 1}: ${escapeHtml(step.name || '-')}</span>
          <span class="runtime-pill ${
            step.status === 'ok' ? 'optional' : step.status === 'running' ? 'neutral' : step.status === 'skipped' ? 'neutral' : 'required'
          }">${escapeHtml(step.status || '-')}</span>
        </div>
        <div class="runtime-result-text">${escapeHtml(step.detail || '-')}</div>
        ${
          action
            ? `
              <div class="actions">
                <button
                  class="ghost-btn selected-preset-step-action-btn"
                  data-preset-name="${escapeHtml(normalized)}"
                  data-step-name="${escapeHtml(step.name || '')}"
                  data-action-kind="${escapeHtml(action.kind)}"
                  data-service-name="${escapeHtml(action.serviceName || '')}"
                >${escapeHtml(action.label)}</button>
              </div>
            `
            : ''
        }
      </div>
    `;
  }).join('');

  container.innerHTML = renderSelectedPresetWorkflowCard({
    presetName: normalized,
    latestItem,
    latestWorkflowName: latest.workflow || '-',
    runtimeProfileDescription: describePresetRuntimeProfile(preset.runtime_profile),
    runtimeMatch: summarizeRuntimeProfileMatch(preset.runtime_profile),
    totalRuns: entries.length,
    issuesText: issues.join(' | '),
    batchSelectionMeta: renderPresetBatchSelectionMetaSection(preset),
    latestItemDetailHtml: latestItem.detail ? `<div class="runtime-result-text">${escapeHtml(latestItem.detail)}</div>` : '',
    verificationSectionHtml,
    actionsHtml,
    stepItemsHtml,
    lastFailureHtml: latestFailure
      ? `<div class="runtime-result-meta">last_failure=${escapeHtml(summarizeWorkflowFailure(latestFailure))}</div>`
      : '',
    escapeHtml,
  });
}

function getRecoveryActionForWorkflowStep(step) {
  if (!step || step.status !== 'failed') {
    return null;
  }

  const name = String(step.name || '').trim();
  if (canStartValidationService(name)) {
    return {
      kind: 'start-service',
      label: `Start ${name}`,
      serviceName: name,
    };
  }

  if (name === 'recommended_stack') {
    return {
      kind: 'start-recommended-stack',
      label: 'Start Recommended Stack',
    };
  }

  if (name === 'smoke') {
    return {
      kind: 'run-smoke',
      label: 'Run Smoke',
    };
  }

  if (name === 'runtime_profile') {
    return {
      kind: 'apply-runtime-profile',
      label: 'Apply Runtime Profile',
    };
  }

  if (name === 'preset_validation' || name === 'runtime_config' || name.includes('path') || name.includes('dataset')) {
    return {
      kind: 'validate',
      label: 'Validate Preset',
    };
  }

  return null;
}

async function runSelectedPresetWorkflowRecoveryAction({presetName, actionKind, serviceName, stepName, sourceHistoryId = '', sourceWorkflow = ''}) {
  const normalized = String(presetName || '').trim();
  const preset = currentPresets.find((item) => item.name === normalized);
  if (!preset) {
    throw new Error(`Preset not found: ${normalized}`);
  }

  applyProjectPreset(preset);
  syncPresetSelections(preset.name);
  activateTab('runtime');
  setOutput('runtime-config-status', `Running recovery for ${stepName || actionKind} on preset: ${preset.name}`);

  const response = await RunPresetRecoveryAction({
    preset,
    action_kind: actionKind || '',
    service_name: serviceName || '',
    step_name: stepName || '',
    source_history_id: sourceHistoryId || '',
    source_workflow: sourceWorkflow || '',
  });
  const renderStatus = response?.status === 'ok'
    ? 'completed'
    : response?.status === 'running'
      ? 'running'
      : 'failed';
  const title = actionKind === 'apply-runtime-profile'
    ? `Recovery Runtime: ${presetLabel(preset)}`
    : actionKind === 'run-smoke'
      ? `Recovery Smoke: ${presetLabel(preset)}`
      : actionKind === 'validate'
        ? `Recovery Validation: ${presetLabel(preset)}`
        : `Recovery: ${presetLabel(preset)}`;
  renderWorkflowResult(title, renderStatus, response?.steps || []);
  await refreshExecutionHistory();
  await refreshOverview();
  await refreshRuntime();

  if (sourceHistoryId && (response?.status === 'ok' || response?.status === 'running')) {
    const followupTitle = actionKind === 'apply-runtime-profile'
      ? 'Runtime Profile Applied'
      : actionKind === 'run-smoke'
        ? 'Smoke Passed'
        : actionKind === 'validate'
          ? 'Validation Passed'
          : 'Recovery Completed';
    const followupMessage = actionKind === 'apply-runtime-profile'
      ? `Runtime profile is ready for ${preset.name}. Retry the original workflow when ready.`
      : actionKind === 'run-smoke'
        ? `Smoke checks passed for ${preset.name}. Retry the original workflow when ready.`
        : actionKind === 'validate'
          ? `Preset validation passed for ${preset.name}. Retry the original workflow when ready.`
          : `Recovery succeeded for ${preset.name}. Retry the original workflow when ready.`;
    renderWorkflowFollowupActions({
      title: followupTitle,
      message: followupMessage,
      historyId: sourceHistoryId,
    });
  }

  const statusMessage = actionKind === 'apply-runtime-profile'
    ? `Applied runtime profile for preset: ${preset.name}`
    : actionKind === 'start-recommended-stack'
      ? response?.status === 'ok'
        ? `Started recommended stack for preset: ${preset.name}`
        : `Recommended stack had issues for preset: ${preset.name}`
      : actionKind === 'validate'
        ? `Validated preset: ${preset.name}`
        : response?.detail || `Recovery finished for preset: ${preset.name}`;
  setOutput('runtime-config-status', statusMessage);
  return response;
}

function renderPresetCatalog(presets) {
  const container = document.getElementById('preset-catalog');
  document.getElementById('preset-catalog-filter').value = presetCatalogFilter;
  document.getElementById('preset-catalog-sort').value = presetCatalogSort;
  currentPresets = presets || [];
  if (!presets || presets.length === 0) {
    container.innerHTML = '<div class="runtime-result-card"><div class="runtime-result-text">No saved presets yet.</div></div>';
    renderOverviewPresetRuntimeHint('');
    renderSelectedPresetPreviewByName('');
    renderSelectedPresetWorkflowByName('');
    renderBatchPresetOutput();
    renderPresetComparison();
    return;
  }

  const sortedPresets = getVisiblePresetCatalogEntries(presets);

  if (sortedPresets.length === 0) {
    container.innerHTML = '<div class="runtime-result-card"><div class="runtime-result-text">No presets match the current catalog filter.</div></div>';
    renderOverviewPresetRuntimeHint('');
    renderSelectedPresetPreviewByName('');
    renderSelectedPresetWorkflowByName('');
    renderBatchPresetOutput();
    renderPresetComparison();
    return;
  }

  container.innerHTML = sortedPresets.map(({preset, matching, regression}) => {
    const validation = currentPresetValidationMap.get(preset.name);
    const state = summarizeValidationState(validation);
    const ops = summarizePresetOperations(validation, matching);
    const blockedServices = (validation?.service_checks || [])
      .filter((check) => check.required && check.status !== 'running' && !canStartValidationService(check.name))
      .map((check) => check.name);
    const runtimeProfile = summarizeRuntimeProfileMatch(preset.runtime_profile);
    const selectedInBatch = selectedBatchPresetNames.has(preset.name);
    const expanded = isPresetExpanded(preset.name);
    const validationMetaHtml = `
      <div class="runtime-result-meta">runtime_profile=<span class="runtime-pill ${runtimeProfile.pillClass}">${escapeHtml(runtimeProfile.label)}</span></div>
      <div class="runtime-result-meta">current_runtime=<span class="runtime-pill ${runtimeProfile.matchPillClass}">${escapeHtml(runtimeProfile.matchLabel)}</span></div>
      <div class="runtime-result-meta">validation=<span class="runtime-pill ${state.pillClass}">${escapeHtml(state.label)}</span></div>
      <div class="runtime-result-meta">ops=<span class="runtime-pill ${ops.pillClass}">${escapeHtml(ops.label)}</span> | ${escapeHtml(ops.detail)}</div>
      <div class="runtime-result-meta">config_warnings=${escapeHtml(String((validation?.config_warnings || []).length))} | blocked_services=${escapeHtml(blockedServices.join(', ') || '-')}</div>
      <div class="runtime-result-meta">${escapeHtml(runtimeProfile.validationNote)}</div>
    `;
    const latestHistoryHtml = (() => {
      if (matching.length === 0) {
        return '<div class="runtime-result-meta">No workflow history for this preset yet.</div>';
      }
      const latest = matching[0];
      const latestFailure = matching.find((entry) => entry.item.status === 'error') || null;
      const latestVerificationFailure = matching.find((entry) => entry.workflow === 'preset_verification' && entry.item.status === 'error') || null;
      const okCount = matching.filter((entry) => entry.item.status === 'ok').length;
      const errorCount = matching.filter((entry) => entry.item.status === 'error').length;
      return `
        <div class="runtime-result-meta">latest_workflow=${escapeHtml(latest.workflow)} | latest_status=${escapeHtml(latest.item.status || '-')}</div>
        <div class="runtime-result-meta">workflow_runs=${escapeHtml(String(matching.length))} | ok=${escapeHtml(String(okCount))} | error=${escapeHtml(String(errorCount))}</div>
        <div class="runtime-result-meta">last_failure=${escapeHtml(latestFailure ? summarizeWorkflowFailure(latestFailure) : '-')}</div>
        <div class="runtime-result-meta">verification_failure=${escapeHtml(latestVerificationFailure ? summarizeVerificationFailure(latestVerificationFailure) : '-')}</div>
        <div class="runtime-result-meta">verification_delta=${escapeHtml(regression?.statusText || '-')} | <span class="runtime-pill ${regression?.status?.pillClass || 'neutral'}">${escapeHtml(regression?.status?.label || 'n/a')}</span> | source_hit_rate_delta=${escapeHtml(regression?.sourceHitRateDelta || '-')} | <span class="runtime-pill ${regression?.sourceHitRateState?.pillClass || 'neutral'}">${escapeHtml(regression?.sourceHitRateState?.label || 'n/a')}</span></div>
      `;
    })();
    const expandedScopeHtml = expanded
      ? (() => {
        const issues = summarizeValidationIssues(validation);
        const latest = getWorkflowEntriesForPreset(preset.name)[0] || null;
        const latestVerificationFailure = getWorkflowEntriesForPreset(preset.name).find((entry) => entry.workflow === 'preset_verification' && entry.item.status === 'error') || null;
        return `
          <div class="runtime-result-item">
            <div class="runtime-result-head">
              <span class="runtime-result-name">Expanded Scope</span>
              <span class="runtime-pill neutral">details</span>
            </div>
            <div class="runtime-result-meta">runtime_profile=${escapeHtml(runtimeProfile.label)} | current_runtime=${escapeHtml(runtimeProfile.matchLabel)}</div>
            <div class="runtime-result-meta">validation_issues=${escapeHtml(issues.join(' | '))}</div>
            <div class="runtime-result-meta">latest_steps=${escapeHtml(summarizeWorkflowSteps(latest?.steps, 3).join(' | '))}</div>
            <div class="runtime-result-meta">verification_failure=${escapeHtml(latestVerificationFailure ? summarizeVerificationFailure(latestVerificationFailure) : '-')}</div>
            <div class="runtime-result-meta">watch_paths=${escapeHtml(summarizePresetPaths(preset.watch_paths))}</div>
            <div class="runtime-result-meta">watch_interval=${escapeHtml(String(preset.watch_interval || 2))}</div>
            <div class="runtime-result-meta">ingest_paths=${escapeHtml(summarizePresetPaths(preset.ingest_paths))}</div>
            <div class="runtime-result-meta">rag_project=${escapeHtml(preset.rag_project || '(default)')} | rag_top_k=${escapeHtml(String(preset.rag_top_k || 5))}</div>
            <div class="runtime-result-meta">eval_project=${escapeHtml(preset.eval_project || '(default)')} | eval_with_answer=${escapeHtml(preset.eval_with_answer ? 'true' : 'false')}</div>
          </div>
        `;
      })()
      : '';
    const runtimeActionsHtml = normalizePresetRuntimeProfile(preset.runtime_profile) === 'current'
      ? ''
      : `
        <button class="ghost-btn apply-preset-runtime-card-btn" data-preset-name="${escapeHtml(preset.name || '')}">Apply Runtime</button>
        <button class="primary-btn apply-preset-runtime-stack-card-btn" data-preset-name="${escapeHtml(preset.name || '')}">Apply Runtime + Stack</button>
      `;
    return renderPresetCatalogCard({
      preset,
      selectedInBatch,
      validationMetaHtml,
      smokePolicy: summarizeSmokePolicy(preset),
      latestHistoryHtml,
      expandedScopeHtml,
      singlePresetBatchActionsHtml: renderSinglePresetBatchActionSection('catalog-preset', preset.name || ''),
      workflowShortcutButtonsHtml: renderPresetWorkflowShortcutButtons('catalog-preset', preset.name || ''),
      runtimeActionsHtml,
      verificationDisabled: !(preset.chat_request_name || preset.ingest_request_name || preset.rag_request_name || preset.eval_request_name),
      retryDisabled: !findLatestWorkflowEntryForPreset(preset.name),
      expanded,
      escapeHtml,
    });
  }).join('');

  const selectedName = document.getElementById('overview-preset-select').value
    || document.getElementById('preset-select').value
    || document.getElementById('preset-name').value.trim();
  renderOverviewPresetRuntimeHint(selectedName);
  renderSelectedPresetPreviewByName(selectedName);
  renderSelectedPresetWorkflowByName(selectedName);
  renderBatchPresetOutput();
  renderPresetComparison();
  void refreshSelectedPresetValidationByName(selectedName);
}

function renderExportedResults(items) {
  const container = document.getElementById('exported-results');
  if (!items || items.length === 0) {
    container.innerHTML = '<div class="runtime-result-card"><div class="runtime-result-text">No exported markdown files yet.</div></div>';
    return;
  }
  container.innerHTML = items.map((item) => `
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">${escapeHtml(item.name || '-')}</span>
      </div>
      <div class="runtime-result-meta">${escapeHtml(item.mod_time || '-')}</div>
      <div class="runtime-result-text">${escapeHtml(item.path || '-')}</div>
      <div class="actions"><button class="ghost-btn preview-export-btn" data-export-path="${escapeHtml(item.path || '')}">Preview</button></div>
    </div>
  `).join('');
}

function renderExportPreview(file) {
  const container = document.getElementById('export-preview');
  if (!file) {
    container.innerHTML = '<div class="runtime-result-card"><div class="runtime-result-text">Select an exported markdown file to preview it here.</div></div>';
    return;
  }
  container.innerHTML = `
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">${escapeHtml(file.name || '-')}</span>
      </div>
      <div class="runtime-result-meta">${escapeHtml(file.path || '-')}</div>
      <div class="runtime-result-text">${escapeHtml(file.content || '')}</div>
    </div>
  `;
}

function renderRagResult(response, {answerMode, query = '', routeState = null, appendToChat = true}) {
  const container = document.getElementById('rag-output');
  const sources = response?.sources || response?.results || [];
  const answer = response?.answer;
  latestRagExport = {
    kind: 'rag',
    title: answerMode ? 'RAG Query Result' : 'RAG Search Result',
    content: [
      answerMode ? `## Answer\n\n${answer || 'No answer returned.'}` : `## Query\n\n${response?.query || ''}`,
      '## Sources',
      ...sources.map((source, index) => [
        `### Source ${index + 1}`,
        `- path: ${source.source_path || '-'}`,
        `- heading: ${(source.heading_path || []).join(' > ') || '(root)'}`,
        `- tags: ${(source.tags || []).join(', ') || '-'}`,
        `- score: ${source.score != null ? source.score : '-'}`,
        '',
        source.chunk_text || '',
      ].join('\n')),
    ].join('\n\n'),
    fileStem: answerMode ? 'rag-query-result' : 'rag-search-result',
  };
  container.innerHTML = `
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">${answerMode ? 'RAG Answer' : 'RAG Search'}</span>
        <span class="runtime-pill ${sources.length > 0 ? 'optional' : 'neutral'}">${escapeHtml(`${sources.length} sources`)}</span>
      </div>
      ${
        answerMode
          ? `<div class="runtime-result-text">${escapeHtml(answer || 'No answer returned.')}</div>`
          : `<div class="runtime-result-text">${escapeHtml(response?.query || '')}</div>`
      }
      <div class="runtime-result-list">
        ${sources.map((source, index) => `
          <div class="runtime-result-item">
            <div class="runtime-result-head">
              <span class="runtime-result-name">Source ${index + 1}</span>
              <span class="runtime-pill neutral">${escapeHtml(source.score != null ? source.score.toFixed(3) : source.project || '-')}</span>
            </div>
            <div class="runtime-result-meta">${escapeHtml(source.source_path || '-')}</div>
            <div class="runtime-result-meta">${escapeHtml((source.heading_path || []).join(' > ') || '(root)')}</div>
            <div class="runtime-result-meta">${escapeHtml((source.tags || []).join(', ') || '(no tags)')}</div>
            <div class="runtime-result-text">${escapeHtml(source.chunk_text || '')}</div>
          </div>
        `).join('')}
      </div>
    </div>
  `;
  if (appendToChat) {
    appendChatThreadEntry({
      role: 'user',
      label: 'You',
      meta: answerMode ? 'With Sources' : 'Search Sources',
      text: query || response?.query || '',
    });
    appendChatThreadEntry({
      role: 'assistant',
      label: answerMode ? 'With Sources' : 'Search Results',
      meta: `${sources.length} sources`,
      text: answerMode ? (answer || 'No answer returned.') : (sources[0]?.chunk_text || 'No matching chunks returned.'),
      thinking: response?.thinking || '',
    });
  }
  activeChatSourceIndex = 0;
  renderChatSourcesPane({sources, title: answerMode ? 'Used Sources' : 'Search Results'});
  renderRouteInspectorCard(routeState);
}

function renderEmbeddingResult(response, request) {
  const container = document.getElementById('embedding-output');
  const items = Array.isArray(response?.data) ? response.data : [];
  const first = items[0] || {};
  const embedding = Array.isArray(first.embedding) ? first.embedding : [];
  const preview = embedding.slice(0, 8).map((value) => {
    if (typeof value === 'number' && Number.isFinite(value)) {
      return value.toFixed(4);
    }
    return String(value);
  });
  latestEmbeddingExport = {
    kind: 'embedding',
    title: `Embedding Response (${response?.model || request.model || 'embedding'})`,
    content: [
      `- model: ${response?.model || request.model || 'embedding'}`,
      `- input: ${(request.input || '').slice(0, 500) || '-'}`,
      `- vector_length: ${embedding.length}`,
      `- preview: ${preview.join(', ') || '-'}`,
      '',
      '## Raw Response',
      '```json',
      JSON.stringify(response, null, 2),
      '```',
    ].join('\n'),
    fileStem: `embedding-${response?.model || request.model || 'embedding'}`,
  };
  container.innerHTML = `
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">Embedding Response</span>
        <span class="runtime-pill neutral">${escapeHtml(response?.model || request.model || 'embedding')}</span>
      </div>
      <div class="runtime-result-text">input=${escapeHtml(request.input.slice(0, 120) || '-')}</div>
      <div class="runtime-result-text">vector_length=${escapeHtml(String(embedding.length))}</div>
      <div class="runtime-result-text">preview=${escapeHtml(preview.join(', ') || '-')}</div>
      <pre class="output-block compact">${escapeHtml(JSON.stringify(response, null, 2))}</pre>
    </div>
  `;
}

function renderIndexBrowseResult(response) {
  currentIndexBrowseResponse = response;
  const container = document.getElementById('index-output');
  const projects = Array.isArray(response?.projects) ? response.projects : [];
  const sources = Array.isArray(response?.sources) ? response.sources : [];
  const chunks = Array.isArray(response?.chunks) ? response.chunks : [];
  const selectedSourcePath = currentIndexSourceResponse?.source_path;
  latestIndexSummaryExport = {
    kind: 'index',
    title: `Index Summary (${response?.project_filter || 'all-projects'})`,
    content: [
      `- total_chunks: ${response?.total_chunks ?? 0}`,
      `- filtered_chunks: ${response?.filtered_chunks ?? 0}`,
      `- project_filter: ${response?.project_filter || '(all)'}`,
      `- source_query: ${response?.source_query || '(none)'}`,
      '',
      '## Projects',
      ...(projects.length > 0
        ? projects.map((item) => `- ${item.project || '(default)'}: ${item.chunk_count || 0} chunks / ${item.source_count || 0} sources`)
        : ['No matching projects.']),
      '',
      '## Sources',
      ...(sources.length > 0
        ? sources.slice(0, 20).map((item) => [
          `### ${item.source_path || '-'}`,
          `- project: ${item.project || '(default)'}`,
          `- chunk_count: ${item.chunk_count || 0}`,
          `- sample_heading: ${item.sample_heading || '(root)'}`,
          '',
          item.sample_text || '',
        ].join('\n'))
        : ['No matching sources.']),
    ].join('\n\n'),
    fileStem: `index-summary-${(response?.project_filter || 'all').toLowerCase().replaceAll(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') || 'all'}`,
  };
  container.innerHTML = `
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">Index Summary</span>
        <span class="runtime-pill neutral">${escapeHtml(String(response?.filtered_chunks ?? 0))} filtered</span>
      </div>
      <div class="runtime-result-text">total_chunks=${escapeHtml(String(response?.total_chunks ?? 0))}</div>
      <div class="runtime-result-text">project_filter=${escapeHtml(response?.project_filter || '(all)')}</div>
      <div class="runtime-result-text">source_query=${escapeHtml(response?.source_query || '(none)')}</div>
      <div class="runtime-result-list">
        <div class="runtime-result-item">
          <div class="runtime-result-head">
            <span class="runtime-result-name">Projects</span>
            <span class="runtime-pill optional">${escapeHtml(String(projects.length))}</span>
          </div>
          <div class="runtime-result-text">${projects.map((item) => `${item.project}: ${item.chunk_count} chunks / ${item.source_count} sources`).join('\n') || 'No matching projects.'}</div>
        </div>
        <div class="runtime-result-item">
          <div class="runtime-result-head">
            <span class="runtime-result-name">Sources</span>
            <span class="runtime-pill optional">${escapeHtml(String(sources.length))}</span>
          </div>
          <div class="runtime-result-list">
            ${sources.slice(0, 10).map((item) => `
              <div class="runtime-result-item">
                <div class="runtime-result-head">
                  <span class="runtime-result-name">${escapeHtml(item.source_path || '-')}</span>
                  <span class="runtime-pill neutral">${escapeHtml(String(item.chunk_count || 0))} chunks</span>
                </div>
                <div class="runtime-result-meta">${escapeHtml(item.project || '(default)')}</div>
                <div class="runtime-result-meta">${escapeHtml(item.sample_heading || '(root)')}</div>
                <div class="runtime-result-text">${escapeHtml(item.sample_text || '')}</div>
                <div class="actions">
                  <button class="ghost-btn index-open-source-btn" data-source-path="${escapeHtml(item.source_path || '')}" data-project="${escapeHtml(item.project || '')}">Open Chunks</button>
                  <button class="ghost-btn index-export-source-btn" data-source-path="${escapeHtml(item.source_path || '')}" data-project="${escapeHtml(item.project || '')}">Export</button>
                  <button class="ghost-btn index-use-rag-source-btn" data-source-path="${escapeHtml(item.source_path || '')}" data-project="${escapeHtml(item.project || '')}">Use Source In RAG</button>
                  <button class="ghost-btn index-use-eval-source-btn" data-source-path="${escapeHtml(item.source_path || '')}" data-project="${escapeHtml(item.project || '')}">Use Source In Eval</button>
                  <button class="ghost-btn index-use-rag-btn" data-project="${escapeHtml(item.project || '')}">Use In RAG</button>
                  <button class="ghost-btn index-use-eval-btn" data-project="${escapeHtml(item.project || '')}">Use In Eval</button>
                  <button class="ghost-btn index-use-ingest-btn" data-source-path="${escapeHtml(item.source_path || '')}" data-project="${escapeHtml(item.project || '')}">Reingest Source</button>
                </div>
              </div>
            `).join('') || '<div class="runtime-result-text">No matching sources.</div>'}
          </div>
        </div>
        <div class="runtime-result-item">
          <div class="runtime-result-head">
            <span class="runtime-result-name">Selected Source</span>
            <span class="runtime-pill optional">${escapeHtml(selectedSourcePath || 'none')}</span>
          </div>
          ${
            currentIndexSourceResponse
              ? renderIndexSourceDetailCard(currentIndexSourceResponse)
              : '<div class="runtime-result-text">Select a source above to inspect exact chunk details.</div>'
          }
        </div>
        <div class="runtime-result-item">
          <div class="runtime-result-head">
            <span class="runtime-result-name">Chunk Preview</span>
            <span class="runtime-pill optional">${escapeHtml(String(chunks.length))}</span>
          </div>
          <div class="runtime-result-list">
            ${chunks.map((item, index) => `
              <div class="runtime-result-item">
                <div class="runtime-result-head">
                  <span class="runtime-result-name">Chunk ${index + 1}</span>
                  <span class="runtime-pill neutral">${escapeHtml(item.project || '(default)')}</span>
                </div>
                <div class="runtime-result-meta">${escapeHtml(item.source_path || '-')}</div>
                <div class="runtime-result-meta">${escapeHtml((item.heading_path || []).join(' > ') || '(root)')}</div>
                <div class="runtime-result-text">${escapeHtml(item.chunk_text || '')}</div>
              </div>
            `).join('') || '<div class="runtime-result-text">No matching chunks.</div>'}
          </div>
        </div>
      </div>
    </div>
  `;
}

function renderIndexSourceDetailCard(response) {
  const chunks = Array.isArray(response?.chunks) ? response.chunks : [];
  return `
    <div class="runtime-result-list">
      <div class="runtime-result-meta">${escapeHtml(response?.source_path || '-')}</div>
      <div class="runtime-result-meta">total_chunks=${escapeHtml(String(response?.total_chunks ?? 0))}</div>
      <div class="actions">
        <button class="ghost-btn index-export-current-source-btn">Export Selected Source</button>
        <button class="ghost-btn index-use-current-rag-source-btn" data-source-path="${escapeHtml(response?.source_path || '')}" data-project="${escapeHtml(response?.project_filter || '')}">Use Source In RAG</button>
        <button class="ghost-btn index-use-current-eval-source-btn" data-source-path="${escapeHtml(response?.source_path || '')}" data-project="${escapeHtml(response?.project_filter || '')}">Use Source In Eval</button>
        <button class="ghost-btn index-run-current-search-btn" data-source-path="${escapeHtml(response?.source_path || '')}" data-project="${escapeHtml(response?.project_filter || '')}">Run Search</button>
        <button class="ghost-btn index-run-current-query-btn" data-source-path="${escapeHtml(response?.source_path || '')}" data-project="${escapeHtml(response?.project_filter || '')}">Run Query</button>
        <button class="ghost-btn index-run-current-eval-btn" data-source-path="${escapeHtml(response?.source_path || '')}" data-project="${escapeHtml(response?.project_filter || '')}">Run Eval</button>
        <button class="ghost-btn index-use-current-rag-btn" data-project="${escapeHtml(response?.project_filter || '')}">Use In RAG</button>
        <button class="ghost-btn index-use-current-eval-btn" data-project="${escapeHtml(response?.project_filter || '')}">Use In Eval</button>
        <button class="ghost-btn index-use-current-ingest-btn" data-source-path="${escapeHtml(response?.source_path || '')}" data-project="${escapeHtml(response?.project_filter || '')}">Reingest Source</button>
      </div>
      ${chunks.map((item, index) => `
        <div class="runtime-result-item">
          <div class="runtime-result-head">
            <span class="runtime-result-name">Chunk ${index + 1}</span>
            <span class="runtime-pill neutral">${escapeHtml(item.project || '(default)')}</span>
          </div>
          <div class="runtime-result-meta">${escapeHtml((item.heading_path || []).join(' > ') || '(root)')}</div>
          <div class="runtime-result-text">${escapeHtml(item.chunk_text || '')}</div>
        </div>
      `).join('') || '<div class="runtime-result-text">No chunks found for this source.</div>'}
    </div>
  `;
}

function buildIndexSourceExportPayload(response) {
  if (!response) {
    return null;
  }
  const chunks = Array.isArray(response?.chunks) ? response.chunks : [];
  return {
    kind: 'index',
    title: `Indexed Source: ${response.source_path || 'source'}`,
    content: [
      `## Source\n\n${response.source_path || '-'}`,
      `## Total Chunks\n\n${response.total_chunks ?? 0}`,
      '## Chunks',
      ...chunks.map((item, index) => [
        `### Chunk ${index + 1}`,
        `- project: ${item.project || '(default)'}`,
        `- heading: ${(item.heading_path || []).join(' > ') || '(root)'}`,
        '',
        item.chunk_text || '',
      ].join('\n')),
    ].join('\n\n'),
    fileStem: `index-${String(response.source_path || 'source').toLowerCase().replaceAll(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') || 'source'}`,
  };
}

function applyIndexProjectToRag(project) {
  activateTab('rag');
  document.getElementById('rag-project').value = project || '';
  document.getElementById('rag-source-path').value = '';
  syncChatContextBarFromRagState();
  updateChatScopeSummary();
  renderRuntimeMessage('rag-output', `Loaded project into RAG form: ${project || '(default)'}`);
}

function applyIndexSourceToRag(sourcePath, project) {
  activateTab('rag');
  document.getElementById('rag-project').value = project || '';
  document.getElementById('rag-source-path').value = sourcePath || '';
  syncChatContextBarFromRagState();
  updateChatScopeSummary();
  renderRuntimeMessage('rag-output', `Loaded source filter into RAG form: ${sourcePath || '-'}`);
}

function applyIndexProjectToEval(project) {
  activateTab('eval');
  document.getElementById('eval-project').value = project || '';
  document.getElementById('eval-source-path').value = '';
  renderRuntimeMessage('eval-output', `Loaded project into Eval form: ${project || '(default)'}`);
}

function applyEvalDatasetToForm({datasetPath, project}) {
  activateTab('eval');
  document.getElementById('eval-dataset').value = datasetPath || 'configs/eval.sample.yaml';
  document.getElementById('eval-project').value = project || '';
  renderRuntimeMessage('eval-output', `Loaded eval dataset into form: ${datasetPath || 'configs/eval.sample.yaml'}`);
}

function applyIndexSourceToEval(sourcePath, project) {
  activateTab('eval');
  document.getElementById('eval-project').value = project || '';
  document.getElementById('eval-source-path').value = sourcePath || '';
  renderRuntimeMessage('eval-output', `Loaded source filter into Eval form: ${sourcePath || '-'}`);
}

async function runIndexSourceSearch(sourcePath, project) {
  applyIndexSourceToRag(sourcePath, project);
  await runRagRequest({answer: false});
}

async function runIndexSourceQuery(sourcePath, project) {
  applyIndexSourceToRag(sourcePath, project);
  await runRagRequest({answer: document.getElementById('rag-answer').checked});
}

async function runIndexSourceEval(sourcePath, project) {
  applyIndexSourceToEval(sourcePath, project);
  await runEvalFromForm();
}

function applyIndexSourceToIngest(sourcePath, project) {
  activateTab('rag');
  document.getElementById('ingest-paths').value = sourcePath || '';
  document.getElementById('ingest-project').value = project || '';
  setOutput('ingest-output', `Loaded source into Ingest form: ${sourcePath || '-'}`);
}

function renderChatResult(response, mode, prompt = '', routeState = null, {appendToChat = true} = {}) {
  latestChatExport = {
    kind: 'chat',
    title: `Chat Response (${mode || 'auto'})`,
    content: `## Answer\n\n${response?.answer || ''}`,
    fileStem: `chat-${mode || 'auto'}`,
  };
  if (appendToChat) {
    appendChatThreadEntry({
      role: 'user',
      label: 'You',
      meta: mode || 'auto',
      text: prompt,
    });
    appendChatThreadEntry({
      role: 'assistant',
      label: 'Assistant',
      meta: mode || 'auto',
      text: response?.answer || '',
      thinking: response?.thinking || '',
      requestId: response?.request_id || '',
      finishReason: response?.finish_reason || '',
      canContinue: isLengthLimitedFinishReason(response?.finish_reason) && Boolean(String(response?.answer || '').trim()),
      mode,
      prompt,
    });
  }
  renderChatSourcesPane({sources: response?.sources || [], title: response?.sources?.length ? 'Used Sources' : 'Sources'});
  renderRouteInspectorCard(routeState);
}

function renderRouteDecision(response) {
  const container = document.getElementById('route-output');
  const inspector = document.getElementById('chat-route-output');
  latestRouteExport = {
    kind: 'route',
    title: `Route Decision (${response?.mode || 'auto'})`,
    content: [
      `- mode: ${response?.mode || 'auto'}`,
      `- model_alias: ${response?.model_alias || '-'}`,
      `- provider: ${response?.provider || '-'}`,
      `- backend_model: ${response?.backend_model || '-'}`,
      `- max_context: ${response?.max_context ?? '-'}`,
      `- base_url: ${response?.base_url || '-'}`,
    ].join('\n'),
    fileStem: `route-${response?.mode || 'auto'}`,
  };
  container.innerHTML = `
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">Route Decision</span>
        <span class="runtime-pill optional">${escapeHtml(response?.mode || 'auto')}</span>
      </div>
      <div class="runtime-summary-grid">
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">Model Alias</div>
          <div class="runtime-result-text">${escapeHtml(response?.model_alias || '-')}</div>
        </div>
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">Provider</div>
          <div class="runtime-result-text">${escapeHtml(response?.provider || '-')}</div>
        </div>
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">Backend Model</div>
          <div class="runtime-result-text">${escapeHtml(response?.backend_model || '-')}</div>
        </div>
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">Max Context</div>
          <div class="runtime-result-text">${escapeHtml(String(response?.max_context ?? '-'))}</div>
        </div>
      </div>
      <div class="runtime-result-item">
        <div class="runtime-summary-title">Base URL</div>
        <div class="runtime-result-text">${escapeHtml(response?.base_url || '-')}</div>
      </div>
    </div>
  `;
  if (inspector && inspector !== container) {
    inspector.innerHTML = container.innerHTML;
  }
}

async function runRoutePlanFromForm() {
  const mode = document.getElementById('route-mode').value;
  const prompt = document.getElementById('route-prompt').value;
  if (!prompt.trim()) {
    renderRuntimeMessage('route-output', 'Prompt is empty.');
    return {ok: false, detail: 'Prompt is empty.'};
  }

  renderRuntimeMessage('route-output', 'Planning route...');
  try {
    const response = await RoutePlan({mode, prompt});
    renderRouteDecision(response);
    await recordExecution({
      kind: 'route',
      title: `Route Plan (${mode || 'auto'})`,
      status: 'ok',
      summary: prompt.slice(0, 120),
      detail: `model_alias=${response?.model_alias || '-'}, provider=${response?.provider || '-'}, backend_model=${response?.backend_model || '-'}`,
      payload: JSON.stringify({mode, prompt}),
    });
    return {
      ok: true,
      detail: `model_alias=${response?.model_alias || '-'}, provider=${response?.provider || '-'}, backend_model=${response?.backend_model || '-'}`,
    };
  } catch (error) {
    renderRuntimeMessage('route-output', String(error));
    await recordExecution({
      kind: 'route',
      title: `Route Plan (${mode || 'auto'})`,
      status: 'error',
      summary: prompt.slice(0, 120),
      detail: String(error),
      payload: JSON.stringify({mode, prompt}),
    });
    return {ok: false, detail: String(error)};
  }
}

function renderEvalResult(response) {
  const container = document.getElementById('eval-output');
  const results = response?.results || [];
  latestEvalExport = {
    kind: 'eval',
    title: `Eval Report (${response?.dataset_path || 'dataset'})`,
    content: [
      `- dataset: ${response?.dataset_path || '-'}`,
      `- total_cases: ${response?.total_cases ?? '-'}`,
      `- source_hit_rate: ${response?.source_hit_rate ?? '-'}`,
      `- keyword_hit_rate: ${response?.keyword_hit_rate ?? '-'}`,
      `- average_latency_ms: ${response?.average_latency_ms ?? '-'}`,
      `- total_prompt_tokens: ${response?.total_prompt_tokens ?? '-'}`,
      `- total_completion_tokens: ${response?.total_completion_tokens ?? '-'}`,
      `- total_tokens: ${response?.total_tokens ?? '-'}`,
      '',
      '## Cases',
      ...results.map((item) => [
        `### ${item.id || item.query || '-'}`,
        `- query: ${item.query || '-'}`,
        `- source_hit: ${item.source_hit}`,
        `- top_source: ${item.top_source || '-'}`,
        `- latency_ms: ${item.latency_ms ?? '-'}`,
        `- prompt_tokens: ${item.prompt_tokens ?? '-'}`,
        `- completion_tokens: ${item.completion_tokens ?? '-'}`,
        `- total_tokens: ${item.total_tokens ?? '-'}`,
        item.answer ? `- answer: ${item.answer}` : '',
      ].filter(Boolean).join('\n')),
    ].join('\n\n'),
    fileStem: 'eval-report',
  };
  container.innerHTML = `
    <div class="runtime-result-card">
      <div class="runtime-result-head">
        <span class="runtime-result-title">Eval Summary</span>
        <span class="runtime-pill optional">${escapeHtml(`${response?.total_cases ?? 0} cases`)}</span>
      </div>
      <div class="runtime-summary-grid">
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">Source Hit Rate</div>
          <div class="runtime-result-text">${escapeHtml(String(response?.source_hit_rate ?? '-'))}</div>
        </div>
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">Keyword Hit Rate</div>
          <div class="runtime-result-text">${escapeHtml(String(response?.keyword_hit_rate ?? '-'))}</div>
        </div>
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">Avg Latency</div>
          <div class="runtime-result-text">${escapeHtml(String(response?.average_latency_ms ?? '-'))}</div>
        </div>
        <div class="runtime-summary-card">
          <div class="runtime-summary-title">Total Tokens</div>
          <div class="runtime-result-text">${escapeHtml(String(response?.total_tokens ?? '-'))}</div>
        </div>
      </div>
      <div class="runtime-result-list">
        ${results.map((item) => `
          <div class="runtime-result-item">
            <div class="runtime-result-head">
              <span class="runtime-result-name">${escapeHtml(item.id || item.query || '-')}</span>
              <span class="runtime-pill ${item.source_hit ? 'optional' : 'required'}">${escapeHtml(item.source_hit ? 'source hit' : 'miss')}</span>
            </div>
            <div class="runtime-result-text">${escapeHtml(item.query || '')}</div>
            <div class="runtime-result-meta">${escapeHtml(item.top_source || '-')}</div>
            <div class="runtime-result-meta">latency_ms=${escapeHtml(String(item.latency_ms ?? '-'))} | total_tokens=${escapeHtml(String(item.total_tokens ?? '-'))}</div>
            ${item.answer ? `<div class="runtime-result-text">${escapeHtml(item.answer)}</div>` : ''}
          </div>
        `).join('')}
      </div>
    </div>
  `;
}

async function refreshExecutionHistory() {
  try {
    const items = await GetExecutionHistory();
    renderExecutionHistory(items);
  } catch (error) {
    renderExecutionHistory([{title: 'History Load Failed', status: 'error', summary: String(error), kind: 'history', timestamp: ''}]);
  }
}

function findLatestWorkflowEntryForPreset(presetName) {
  const normalized = String(presetName || '').trim();
  if (!normalized) {
    return null;
  }
  return currentExecutionHistory.find((item) => {
    if (item.kind !== 'workflow' || !item.payload) {
      return false;
    }
    try {
      const payload = JSON.parse(item.payload);
      return (payload.preset?.name || payload.preset_name || '') === normalized;
    } catch (error) {
      return false;
    }
  }) || null;
}

function findHistoryItemById(id) {
  const normalized = String(id || '').trim();
  if (!normalized) {
    return null;
  }
  return currentExecutionHistory.find((item) => item.id === normalized) || null;
}

async function recordExecution(entry) {
  try {
    await RecordExecution(entry);
    await refreshExecutionHistory();
  } catch (error) {
    console.error('failed to record execution history', error);
  }
}

async function exportLatestResult(targetId, payload) {
  if (!payload) {
    renderRuntimeMessage(targetId, 'Nothing to export yet.');
    return;
  }
  try {
    const response = await ExportResult(payload);
    renderRuntimeMessage(targetId, `Exported to ${response.path}`);
  } catch (error) {
    renderRuntimeMessage(targetId, String(error));
  }
}

function syncPresetSelections(name) {
  const normalized = String(name || '').trim();
  document.getElementById('preset-select').value = normalized;
  document.getElementById('overview-preset-select').value = normalized;
  renderOverviewPresetRuntimeHint(normalized);
  renderSelectedPresetPreviewByName(normalized);
  renderSelectedPresetWorkflowByName(normalized);
  void refreshSelectedPresetValidationByName(normalized);
}

async function recordWorkflowExecution({workflow, preset, status, summary, detail, steps, extraPayload = {}}) {
  const payload = {
    workflow,
    preset_name: preset?.name || '',
    preset,
    steps,
    ...extraPayload,
  };
  await recordExecution({
    kind: 'workflow',
    title: `Workflow (${workflow})`,
    status,
    summary,
    detail,
    payload: JSON.stringify(payload),
  });
}

function buildHistoryExportPayload(item) {
  const lines = [
    `- kind: ${item.kind || '-'}`,
    `- status: ${item.status || '-'}`,
    `- timestamp: ${item.timestamp || '-'}`,
    '',
    '## Regression Watch Settings',
    ...buildRegressionWatchSettingsLines(),
    '',
    '## Summary',
    item.summary || '-',
  ];

  if (item.detail) {
    lines.push('', '## Detail', item.detail);
  }

  if (item.payload) {
    try {
      const payload = JSON.parse(item.payload);
      if (item.kind === 'workflow' && Array.isArray(payload.steps)) {
        lines.push(
          '',
          '## Steps',
          ...payload.steps.map((step) => [
            `### ${step.name || '-'}`,
            `- status: ${step.status || '-'}`,
            '',
            step.detail || '',
          ].join('\n')),
        );
      } else {
        lines.push('', '## Payload', '```json', JSON.stringify(payload, null, 2), '```');
      }
    } catch (error) {
      lines.push('', '## Payload', item.payload);
    }
  }

  return {
    kind: item.kind || 'activity',
    title: item.title || `History Export (${item.kind || 'activity'})`,
    content: lines.join('\n\n'),
    fileStem: `history-${(item.kind || 'activity').toLowerCase()}-${(item.id || 'item').toLowerCase().replaceAll(/[^a-z0-9-]+/g, '-')}`,
  };
}

function applyRuntimeEditorsForProfile(profile) {
  const normalized = normalizePresetRuntimeProfile(profile);
  if (normalized === 'local_only') {
    document.getElementById('models-local-editor').value = '';
    document.getElementById('rag-local-editor').value = RAG_LOCAL_ONLY_PRESET;
    return true;
  }
  if (normalized === 'external_rag') {
    document.getElementById('models-local-editor').value = MODELS_LOCAL_EXTERNAL_PRESET;
    document.getElementById('rag-local-editor').value = RAG_EXTERNAL_PRESET;
    return true;
  }
  return false;
}

function applyRuntimeWorkflowPayload(payload) {
  const workflow = String(payload?.workflow || '').trim();
  if (!workflow) {
    return;
  }

  activateTab('runtime');

  if (workflow === 'runtime_smoke' && payload.request) {
    document.getElementById('gateway-url').value = payload.request.gateway_url || document.getElementById('gateway-url').value;
    document.getElementById('smoke-skip-qdrant').checked = payload.request.skip_qdrant === true;
    document.getElementById('smoke-skip-embedding').checked = payload.request.skip_embedding === true;
    document.getElementById('smoke-skip-reranker').checked = payload.request.skip_reranker === true;
    return;
  }

  if ((workflow === 'runtime_start_watch' || workflow === 'runtime_stop_watch') && payload.watch) {
    const watchPaths = Array.isArray(payload.watch.paths) ? payload.watch.paths : [];
    document.getElementById('watch-paths').value = watchPaths.join('\n');
    document.getElementById('watch-project').value = payload.watch.project || '';
    document.getElementById('watch-tags').value = Array.isArray(payload.watch.tags) ? payload.watch.tags.join(', ') : '';
    if (payload.watch.interval) {
      document.getElementById('watch-interval').value = String(payload.watch.interval);
    }
    return;
  }

  if (workflow === 'runtime_apply_local_only') {
    applyRuntimeEditorsForProfile('local_only');
    return;
  }

  if (workflow === 'runtime_apply_local_only_stack') {
    applyRuntimeEditorsForProfile('local_only');
    return;
  }

  if (workflow === 'runtime_apply_external_rag') {
    applyRuntimeEditorsForProfile('external_rag');
    return;
  }

  if (workflow === 'runtime_apply_external_rag_stack') {
    applyRuntimeEditorsForProfile('external_rag');
    return;
  }

  if (workflow.startsWith('preset_') && applyRuntimeEditorsForProfile(payload?.preset?.runtime_profile)) {
    return;
  }

  if (workflow === 'runtime_save_local_config' || workflow === 'runtime_delete_local_config') {
    if (payload.name === 'models.local.yaml') {
      document.getElementById('models-local-editor').value = workflow === 'runtime_delete_local_config' ? '' : (payload.content || '');
    }
    if (payload.name === 'rag.local.yaml') {
      document.getElementById('rag-local-editor').value = workflow === 'runtime_delete_local_config' ? '' : (payload.content || '');
    }
  }
}

function applyBatchPresetSelection(presetNames) {
  selectedBatchPresetNames = new Set(normalizeBatchPresetNames(presetNames));
  void persistBatchPresetSelection();
  if (!batchWorkflowState?.running) {
    batchWorkflowState = null;
    void persistBatchWorkflowState();
  }
  renderPresetCatalog(currentPresets);
  renderBatchPresetOutput();
}

function toggleBatchPresetSelectionByName(presetName, forceSelected = null) {
  const normalized = String(presetName || '').trim();
  if (!normalized) {
    return false;
  }
  const shouldSelect = forceSelected == null ? !selectedBatchPresetNames.has(normalized) : forceSelected === true;
  if (shouldSelect) {
    selectedBatchPresetNames.add(normalized);
  } else {
    selectedBatchPresetNames.delete(normalized);
  }
  void persistBatchPresetSelection();
  if (!batchWorkflowState?.running) {
    batchWorkflowState = null;
    void persistBatchWorkflowState();
  }
  renderPresetCatalog(currentPresets);
  renderBatchPresetOutput();
  renderSelectedPresetPreviewByName(normalized);
  return shouldSelect;
}

async function runSinglePresetBatchVerification(presetName) {
  const normalized = String(presetName || '').trim();
  if (!normalized) {
    throw new Error('Preset name is required.');
  }
  applyBatchPresetSelection([normalized]);
  const state = await StartBatchPresetVerification({preset_names: [normalized]});
  batchWorkflowState = normalizeBatchWorkflowState(state);
  syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
  renderBatchPresetOutput();
  renderPresetCatalog(currentPresets);
  renderSelectedPresetPreviewByName(normalized);
  setOutput('runtime-config-status', `Started Go-backed batch verification for preset: ${normalized}`);
}

async function runSinglePresetBatchValidate(presetName) {
  const normalized = String(presetName || '').trim();
  if (!normalized) {
    throw new Error('Preset name is required.');
  }
  applyBatchPresetSelection([normalized]);
  const state = await StartBatchPresetValidate({preset_names: [normalized]});
  batchWorkflowState = normalizeBatchWorkflowState(state);
  syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
  renderBatchPresetOutput();
  renderPresetCatalog(currentPresets);
  renderSelectedPresetPreviewByName(normalized);
  setOutput('runtime-config-status', `Started Go-backed batch validate for preset: ${normalized}`);
}

async function runSinglePresetBatchSmoke(presetName) {
  const normalized = String(presetName || '').trim();
  if (!normalized) {
    throw new Error('Preset name is required.');
  }
  applyBatchPresetSelection([normalized]);
  const state = await StartBatchPresetSmoke({preset_names: [normalized]});
  batchWorkflowState = normalizeBatchWorkflowState(state);
  syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
  renderBatchPresetOutput();
  renderPresetCatalog(currentPresets);
  renderSelectedPresetPreviewByName(normalized);
  setOutput('runtime-config-status', `Started Go-backed batch smoke for preset: ${normalized}`);
}

async function runSinglePresetBatchWatch(presetName) {
  const normalized = String(presetName || '').trim();
  if (!normalized) {
    throw new Error('Preset name is required.');
  }
  applyBatchPresetSelection([normalized]);
  const state = await StartBatchPresetWatch({preset_names: [normalized]});
  batchWorkflowState = normalizeBatchWorkflowState(state);
  syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
  renderBatchPresetOutput();
  renderPresetCatalog(currentPresets);
  renderSelectedPresetPreviewByName(normalized);
  setOutput('runtime-config-status', `Started Go-backed batch watch for preset: ${normalized}`);
}

async function runSinglePresetBatchRuntimeStackPrepare(presetName) {
  const normalized = String(presetName || '').trim();
  if (!normalized) {
    throw new Error('Preset name is required.');
  }
  applyBatchPresetSelection([normalized]);
  const state = await StartBatchPresetRuntimeStackPrepare({preset_names: [normalized]});
  batchWorkflowState = normalizeBatchWorkflowState(state);
  syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
  renderBatchPresetOutput();
  renderPresetCatalog(currentPresets);
  renderSelectedPresetPreviewByName(normalized);
  setOutput('runtime-config-status', `Started Go-backed batch runtime + stack prepare for preset: ${normalized}`);
}

async function runSinglePresetBatchEval(presetName) {
  const normalized = String(presetName || '').trim();
  if (!normalized) {
    throw new Error('Preset name is required.');
  }
  applyBatchPresetSelection([normalized]);
  const state = await StartBatchPresetEval({preset_names: [normalized]});
  batchWorkflowState = normalizeBatchWorkflowState(state);
  syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
  renderBatchPresetOutput();
  renderPresetCatalog(currentPresets);
  renderSelectedPresetPreviewByName(normalized);
  setOutput('runtime-config-status', `Started Go-backed batch eval for preset: ${normalized}`);
}

async function runSinglePresetBatchIngest(presetName) {
  const normalized = String(presetName || '').trim();
  if (!normalized) {
    throw new Error('Preset name is required.');
  }
  applyBatchPresetSelection([normalized]);
  const state = await StartBatchPresetIngest({preset_names: [normalized]});
  batchWorkflowState = normalizeBatchWorkflowState(state);
  syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
  renderBatchPresetOutput();
  renderPresetCatalog(currentPresets);
  renderSelectedPresetPreviewByName(normalized);
  setOutput('runtime-config-status', `Started Go-backed batch ingest for preset: ${normalized}`);
}

async function runSinglePresetBatchIngestEval(presetName) {
  const normalized = String(presetName || '').trim();
  if (!normalized) {
    throw new Error('Preset name is required.');
  }
  applyBatchPresetSelection([normalized]);
  const state = await StartBatchPresetIngestEval({preset_names: [normalized]});
  batchWorkflowState = normalizeBatchWorkflowState(state);
  syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
  renderBatchPresetOutput();
  renderPresetCatalog(currentPresets);
  renderSelectedPresetPreviewByName(normalized);
  setOutput('runtime-config-status', `Started Go-backed batch ingest + eval for preset: ${normalized}`);
}

async function runSinglePresetBatchStackWorkflow(presetName) {
  const normalized = String(presetName || '').trim();
  if (!normalized) {
    throw new Error('Preset name is required.');
  }
  applyBatchPresetSelection([normalized]);
  const state = await StartBatchPresetStackIngestEval({preset_names: [normalized]});
  batchWorkflowState = normalizeBatchWorkflowState(state);
  syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
  renderBatchPresetOutput();
  renderPresetCatalog(currentPresets);
  renderSelectedPresetPreviewByName(normalized);
  setOutput('runtime-config-status', `Started Go-backed batch stack + ingest + eval for preset: ${normalized}`);
}

function summarizeWorkflowReuseMessage(payload) {
  const workflow = String(payload?.workflow || '').trim();
  if (!workflow) {
    return 'Loaded workflow context from history.';
  }

  if (Array.isArray(payload.batch_preset_names) && payload.batch_preset_names.length > 0) {
    return `Loaded batch preset selection from history: ${payload.batch_preset_names.length} presets.`;
  }

  const presetName = String(payload?.preset?.name || payload?.preset_name || '').trim();
  if (workflow.startsWith('preset_') && presetName) {
    return `Loaded preset workflow from history: ${presetName} (${workflow}).`;
  }
  if (workflow.startsWith('runtime_')) {
    return `Loaded runtime workflow from history: ${workflow}.`;
  }
  return `Loaded workflow from history: ${workflow}.`;
}

function applyHistoryPayload(item) {
  if (!item?.payload) {
    return;
  }
  const payload = JSON.parse(item.payload);
  switch (item.kind) {
    case 'route':
      activateTab('router');
      document.getElementById('route-mode').value = payload.mode || 'auto';
      document.getElementById('route-prompt').value = payload.prompt || '';
      break;
    case 'chat':
      activateTab('chat');
      document.getElementById('chat-mode').value = payload.mode || 'auto';
      document.getElementById('chat-prompt').value = payload.prompt || '';
      break;
    case 'rag':
      activateTab('rag');
      document.getElementById('rag-query').value = payload.query || '';
      document.getElementById('rag-project').value = payload.project || '';
      document.getElementById('rag-source-path').value = payload.source_path || '';
      document.getElementById('rag-tags').value = Array.isArray(payload.tags) ? payload.tags.join(', ') : '';
      document.getElementById('rag-top-k').value = String(payload.top_k || 5);
      document.getElementById('rag-answer').checked = payload.answer !== false;
      break;
    case 'embedding':
      activateTab('rag');
      document.getElementById('embedding-model').value = payload.model || 'auto';
      document.getElementById('embedding-input').value = payload.input || '';
      break;
    case 'ingest':
      activateTab('rag');
      document.getElementById('ingest-paths').value = payload.paths || '';
      document.getElementById('ingest-project').value = payload.project || '';
      document.getElementById('ingest-tags').value = Array.isArray(payload.tags) ? payload.tags.join(', ') : '';
      break;
    case 'index':
      activateTab('rag');
      document.getElementById('index-project').value = payload.project || '';
      document.getElementById('index-source-query').value = payload.source_query || '';
      if (payload.limit) {
        document.getElementById('index-limit').value = String(payload.limit);
      }
      break;
    case 'eval':
      activateTab('eval');
      document.getElementById('eval-dataset').value = payload.dataset_path || 'configs/eval.sample.yaml';
      document.getElementById('eval-project').value = payload.project || '';
      document.getElementById('eval-source-path').value = payload.source_path || '';
      document.getElementById('eval-top-k').value = String(payload.top_k || 5);
      document.getElementById('eval-with-answer').checked = payload.with_answer === true;
      break;
    case 'workflow':
      applyRuntimeWorkflowPayload(payload);
      if (Array.isArray(payload.batch_preset_names) && payload.batch_preset_names.length > 0) {
        applyBatchPresetSelection(payload.batch_preset_names);
      }
      if (payload.preset) {
        applyProjectPreset(payload.preset);
        syncPresetSelections(payload.preset.name || payload.preset_name || '');
      } else if (payload.preset_name) {
        document.getElementById('preset-name').value = payload.preset_name;
        syncPresetSelections(payload.preset_name);
      }
      break;
    default:
      break;
  }
  syncChatContextBarFromRagState();
  updateChatScopeSummary();
}

async function reuseHistoryItem(item) {
  if (!item?.payload) {
    return;
  }
  applyHistoryPayload(item);

  if (item.kind === 'workflow') {
    const payload = JSON.parse(item.payload);
    setOutput('runtime-config-status', summarizeWorkflowReuseMessage(payload));
    return;
  }

  if (item.kind !== 'index') {
    return;
  }

  const payload = JSON.parse(item.payload);
  if (payload.source_path) {
    await openIndexSource({
      sourcePath: payload.source_path,
      project: payload.project || '',
      limit: payload.limit,
    });
  }
}

async function rerunWorkflowHistoryItem(item) {
  if (!item?.payload) {
    throw new Error('Workflow payload is missing.');
  }
  const payload = JSON.parse(item.payload);
  const workflow = payload.workflow;
  if (!workflow) {
    throw new Error('Workflow payload is incomplete.');
  }

  switch (workflow) {
    case 'preset_watch': {
      const preset = payload.preset;
      if (!preset?.name) {
        throw new Error('Workflow payload is incomplete.');
      }
      syncPresetSelections(preset.name);
      applyProjectPreset(preset);
      document.getElementById('preset-start-watch').click();
      return;
    }
    case 'preset_ingest': {
      const preset = payload.preset;
      if (!preset?.name) {
        throw new Error('Workflow payload is incomplete.');
      }
      syncPresetSelections(preset.name);
      applyProjectPreset(preset);
      document.getElementById('preset-run-ingest').click();
      return;
    }
    case 'preset_eval': {
      const preset = payload.preset;
      if (!preset?.name) {
        throw new Error('Workflow payload is incomplete.');
      }
      syncPresetSelections(preset.name);
      applyProjectPreset(preset);
      document.getElementById('preset-run-eval').click();
      return;
    }
    case 'preset_verification': {
      const preset = payload.preset;
      if (!preset?.name) {
        throw new Error('Workflow payload is incomplete.');
      }
      syncPresetSelections(preset.name);
      applyProjectPreset(preset);
      await runPresetVerificationWorkflow(preset);
      return;
    }
    case 'preset_validate': {
      const preset = payload.preset;
      if (!preset?.name) {
        throw new Error('Workflow payload is incomplete.');
      }
      syncPresetSelections(preset.name);
      applyProjectPreset(preset);
      await runGoPresetWorkflow({
        preset,
        runner: RunPresetValidate,
        title: `Preset Validation: ${presetLabel(preset)}`,
        successMessage: (item) => `Preset validation passed: ${item.name}`,
        failureMessage: (item) => `Preset validation found issues: ${item.name}`,
        tab: 'runtime',
      });
      return;
    }
    case 'preset_smoke': {
      const preset = payload.preset;
      if (!preset?.name) {
        throw new Error('Workflow payload is incomplete.');
      }
      syncPresetSelections(preset.name);
      applyProjectPreset(preset);
      await runGoPresetWorkflow({
        preset,
        runner: RunPresetSmoke,
        title: `Preset Smoke: ${presetLabel(preset)}`,
        successMessage: (item) => `Preset smoke passed: ${item.name}`,
        failureMessage: (item) => `Preset smoke found issues: ${item.name}`,
        tab: 'runtime',
      });
      return;
    }
    case 'preset_ingest_eval': {
      const preset = payload.preset;
      if (!preset?.name) {
        throw new Error('Workflow payload is incomplete.');
      }
      syncPresetSelections(preset.name);
      applyProjectPreset(preset);
      document.getElementById('preset-run-ingest-eval').click();
      return;
    }
    case 'preset_stack_ingest_eval': {
      const preset = payload.preset;
      if (!preset?.name) {
        throw new Error('Workflow payload is incomplete.');
      }
      syncPresetSelections(preset.name);
      applyProjectPreset(preset);
      await runPresetStackIngestEvalWorkflow(preset);
      return;
    }
    case 'preset_runtime_stack_prepare': {
      const preset = payload.preset;
      if (!preset?.name) {
        throw new Error('Workflow payload is incomplete.');
      }
      syncPresetSelections(preset.name);
      applyProjectPreset(preset);
      await runPresetRuntimePreparationAndStackWorkflow(preset);
      return;
    }
    case 'preset_recovery': {
      const preset = payload.preset;
      if (!preset?.name) {
        throw new Error('Workflow payload is incomplete.');
      }
      syncPresetSelections(preset.name);
      applyProjectPreset(preset);
      await runSelectedPresetWorkflowRecoveryAction({
        presetName: preset.name,
        actionKind: payload.recovery_action || '',
        serviceName: payload.recovery_service_name || '',
        stepName: payload.recovery_step_name || '',
        sourceHistoryId: payload.recovery_for_history_id || '',
        sourceWorkflow: payload.recovery_for_workflow || '',
      });
      return;
    }
    case 'preset_batch_verification': {
      const presetNames = Array.isArray(payload.batch_preset_names)
        ? payload.batch_preset_names.map((name) => String(name || '').trim()).filter(Boolean)
        : [];
      if (presetNames.length === 0) {
        throw new Error('Batch workflow payload is incomplete.');
      }
      applyBatchPresetSelection(presetNames);
      const state = await StartBatchPresetVerification({preset_names: presetNames});
      batchWorkflowState = normalizeBatchWorkflowState(state);
      renderBatchPresetOutput();
      setOutput('runtime-config-status', `Started Go-backed batch verification for ${presetNames.length} presets.`);
      return;
    }
    case 'preset_batch_validate': {
      const presetNames = Array.isArray(payload.batch_preset_names)
        ? payload.batch_preset_names.map((name) => String(name || '').trim()).filter(Boolean)
        : [];
      if (presetNames.length === 0) {
        throw new Error('Batch workflow payload is incomplete.');
      }
      applyBatchPresetSelection(presetNames);
      const state = await StartBatchPresetValidate({preset_names: presetNames});
      batchWorkflowState = normalizeBatchWorkflowState(state);
      renderBatchPresetOutput();
      setOutput('runtime-config-status', `Started Go-backed batch validate for ${presetNames.length} presets.`);
      return;
    }
    case 'preset_batch_smoke': {
      const presetNames = Array.isArray(payload.batch_preset_names)
        ? payload.batch_preset_names.map((name) => String(name || '').trim()).filter(Boolean)
        : [];
      if (presetNames.length === 0) {
        throw new Error('Batch workflow payload is incomplete.');
      }
      applyBatchPresetSelection(presetNames);
      const state = await StartBatchPresetSmoke({preset_names: presetNames});
      batchWorkflowState = normalizeBatchWorkflowState(state);
      renderBatchPresetOutput();
      setOutput('runtime-config-status', `Started Go-backed batch smoke for ${presetNames.length} presets.`);
      return;
    }
    case 'preset_batch_watch': {
      const presetNames = Array.isArray(payload.batch_preset_names)
        ? payload.batch_preset_names.map((name) => String(name || '').trim()).filter(Boolean)
        : [];
      if (presetNames.length === 0) {
        throw new Error('Batch workflow payload is incomplete.');
      }
      applyBatchPresetSelection(presetNames);
      const state = await StartBatchPresetWatch({preset_names: presetNames});
      batchWorkflowState = normalizeBatchWorkflowState(state);
      renderBatchPresetOutput();
      setOutput('runtime-config-status', `Started Go-backed batch watch for ${presetNames.length} presets.`);
      return;
    }
    case 'preset_batch_runtime_stack_prepare': {
      const presetNames = Array.isArray(payload.batch_preset_names)
        ? payload.batch_preset_names.map((name) => String(name || '').trim()).filter(Boolean)
        : [];
      if (presetNames.length === 0) {
        throw new Error('Batch workflow payload is incomplete.');
      }
      applyBatchPresetSelection(presetNames);
      const state = await StartBatchPresetRuntimeStackPrepare({preset_names: presetNames});
      batchWorkflowState = normalizeBatchWorkflowState(state);
      renderBatchPresetOutput();
      setOutput('runtime-config-status', `Started Go-backed batch runtime + stack prepare for ${presetNames.length} presets.`);
      return;
    }
    case 'preset_batch_ingest': {
      const presetNames = Array.isArray(payload.batch_preset_names)
        ? payload.batch_preset_names.map((name) => String(name || '').trim()).filter(Boolean)
        : [];
      if (presetNames.length === 0) {
        throw new Error('Batch workflow payload is incomplete.');
      }
      applyBatchPresetSelection(presetNames);
      const state = await StartBatchPresetIngest({preset_names: presetNames});
      batchWorkflowState = normalizeBatchWorkflowState(state);
      renderBatchPresetOutput();
      setOutput('runtime-config-status', `Started Go-backed batch ingest for ${presetNames.length} presets.`);
      return;
    }
    case 'preset_batch_eval': {
      const presetNames = Array.isArray(payload.batch_preset_names)
        ? payload.batch_preset_names.map((name) => String(name || '').trim()).filter(Boolean)
        : [];
      if (presetNames.length === 0) {
        throw new Error('Batch workflow payload is incomplete.');
      }
      applyBatchPresetSelection(presetNames);
      const state = await StartBatchPresetEval({preset_names: presetNames});
      batchWorkflowState = normalizeBatchWorkflowState(state);
      renderBatchPresetOutput();
      setOutput('runtime-config-status', `Started Go-backed batch eval for ${presetNames.length} presets.`);
      return;
    }
    case 'preset_batch_ingest_eval': {
      const presetNames = Array.isArray(payload.batch_preset_names)
        ? payload.batch_preset_names.map((name) => String(name || '').trim()).filter(Boolean)
        : [];
      if (presetNames.length === 0) {
        throw new Error('Batch workflow payload is incomplete.');
      }
      applyBatchPresetSelection(presetNames);
      const state = await StartBatchPresetIngestEval({preset_names: presetNames});
      batchWorkflowState = normalizeBatchWorkflowState(state);
      renderBatchPresetOutput();
      setOutput('runtime-config-status', `Started Go-backed batch ingest + eval for ${presetNames.length} presets.`);
      return;
    }
    case 'preset_batch_stack_ingest_eval': {
      const presetNames = Array.isArray(payload.batch_preset_names)
        ? payload.batch_preset_names.map((name) => String(name || '').trim()).filter(Boolean)
        : [];
      if (presetNames.length === 0) {
        throw new Error('Batch workflow payload is incomplete.');
      }
      applyBatchPresetSelection(presetNames);
      const state = await StartBatchPresetStackIngestEval({preset_names: presetNames});
      batchWorkflowState = normalizeBatchWorkflowState(state);
      renderBatchPresetOutput();
      setOutput('runtime-config-status', `Started Go-backed batch stack + ingest + eval for ${presetNames.length} presets.`);
      return;
    }
    case 'runtime_smoke':
      await runGoRuntimeWorkflow({
        runner: RunRuntimeSmoke,
        request: payload.request || {
          gateway_url: document.getElementById('gateway-url').value,
          skip_qdrant: document.getElementById('smoke-skip-qdrant').checked,
          skip_embedding: document.getElementById('smoke-skip-embedding').checked,
          skip_reranker: document.getElementById('smoke-skip-reranker').checked,
        },
        title: 'Runtime Smoke',
        targetId: 'runtime-smoke-output',
        successMessage: () => 'Runtime smoke passed.',
        failureMessage: () => 'Runtime smoke needs attention.',
        fileStemPrefix: 'runtime-smoke',
      });
      return;
    case 'runtime_start_recommended_stack':
    case 'runtime_stop_recommended_stack':
    case 'runtime_start_core_stack':
    case 'runtime_stop_core_stack':
      await runGoRuntimeWorkflow({
        runner: RunRuntimeStackAction,
        request: {action: payload.action || workflow.replace(/^runtime_/, '')},
        title: workflow === 'runtime_start_recommended_stack'
          ? 'Runtime Stack: Start Recommended'
          : workflow === 'runtime_stop_recommended_stack'
            ? 'Runtime Stack: Stop Recommended'
            : workflow === 'runtime_start_core_stack'
              ? 'Runtime Stack: Start Core'
              : 'Runtime Stack: Stop Core',
        targetId: 'runtime-stack-output',
        successMessage: () => 'Runtime stack action completed.',
        failureMessage: () => 'Runtime stack action had issues.',
        fileStemPrefix: 'runtime-stack',
      });
      return;
    case 'runtime_reload_gateway_config':
    case 'runtime_apply_local_only':
    case 'runtime_apply_external_rag':
    case 'runtime_save_local_config':
    case 'runtime_delete_local_config':
      await runGoRuntimeConfigWorkflow({
        action: payload.action || workflow.replace(/^runtime_/, ''),
        name: payload.name || '',
        content: payload.content || '',
        title: workflow === 'runtime_reload_gateway_config'
          ? 'Runtime Config: Reload Gateway'
          : workflow === 'runtime_apply_local_only'
            ? 'Runtime Config: Apply Local Only'
            : workflow === 'runtime_apply_external_rag'
              ? 'Runtime Config: Apply External RAG'
              : workflow === 'runtime_save_local_config'
                ? `Runtime Config: Save ${payload.name || 'local config'}`
                : `Runtime Config: Delete ${payload.name || 'local config'}`,
        successMessage: () => 'Runtime config action completed.',
        failureMessage: () => 'Runtime config action had issues.',
      });
      return;
    case 'runtime_apply_local_only_stack':
      await runApplyLocalOnlyStackWorkflow();
      return;
    case 'runtime_apply_external_rag_stack':
      await runApplyExternalRagStackWorkflow();
      return;
    case 'runtime_start_fast':
    case 'runtime_stop_fast':
    case 'runtime_start_work':
    case 'runtime_stop_work':
    case 'runtime_start_code':
    case 'runtime_stop_code':
    case 'runtime_start_gateway':
    case 'runtime_stop_gateway':
    case 'runtime_start_embedding':
    case 'runtime_stop_embedding':
    case 'runtime_start_qdrant':
    case 'runtime_stop_qdrant':
    case 'runtime_start_watch':
    case 'runtime_stop_watch':
      await runGoRuntimeServiceWorkflow({
        action: payload.action || workflow.replace(/^runtime_/, ''),
        watchRequest: payload.watch || null,
        title: `Runtime Service: ${String((payload.action || workflow.replace(/^runtime_/, '')).replaceAll('_', ' ')).replace(/\b\w/g, (char) => char.toUpperCase())}`,
        successMessage: () => 'Runtime service action completed.',
        failureMessage: () => 'Runtime service action had issues.',
      });
      return;
    default:
      throw new Error(`Unsupported workflow type: ${workflow}`);
  }
}

function renderWorkflowActionFailure(title, stepName, error) {
  renderWorkflowResult(title, 'failed', [
    {name: stepName, status: 'failed', detail: String(error)},
  ]);
  setOutput('runtime-config-status', String(error));
}

function rerunWorkflowHistoryItemWithHandling(item, {title, stepName}) {
  if (!item) {
    return;
  }
  rerunWorkflowHistoryItem(item).catch((error) => {
    renderWorkflowActionFailure(title, stepName, error);
  });
}

function rerunWorkflowHistoryById(historyId, {title, stepName}) {
  const item = currentExecutionHistory.find((historyItem) => historyItem.id === historyId);
  rerunWorkflowHistoryItemWithHandling(item, {title, stepName});
}

async function rerunHistoryItem(item) {
  if (!item?.payload) {
    throw new Error('History payload is missing.');
  }
  if (item.kind === 'workflow') {
    await rerunWorkflowHistoryItem(item);
    return;
  }

  const payload = JSON.parse(item.payload);
  switch (item.kind) {
    case 'route':
      applyHistoryPayload(item);
      await runRoutePlanFromForm();
      return;
    case 'chat':
      applyHistoryPayload(item);
      await runChatFromForm();
      return;
    case 'ingest':
      applyHistoryPayload(item);
      await runIngestFromForm();
      return;
    case 'rag':
      applyHistoryPayload(item);
      await runRagRequest({answer: payload.answer !== false});
      return;
    case 'eval':
      applyHistoryPayload(item);
      await runEvalFromForm();
      return;
    case 'embedding':
      document.getElementById('embedding-model').value = payload.model || 'auto';
      document.getElementById('embedding-input').value = payload.input || '';
      activateTab('rag');
      await runEmbeddingProbe();
      return;
    case 'index':
      activateTab('rag');
      if (payload.source_path) {
        await openIndexSource({
          sourcePath: payload.source_path,
          project: payload.project || '',
          limit: payload.limit,
        });
        return;
      }
      document.getElementById('index-project').value = payload.project || '';
      document.getElementById('index-source-query').value = payload.source_query || '';
      if (payload.limit) {
        document.getElementById('index-limit').value = String(payload.limit);
      }
      await runIndexBrowser();
      return;
    default:
      throw new Error(`Unsupported history kind: ${item.kind}`);
  }
}

const MODELS_LOCAL_EXTERNAL_PRESET = `models:
  embedding:
    provider: llama_cpp
    model: qwen3-embedding-0.6b
    base_url: http://localhost:8090/v1

  reranker:
    provider: openai_compatible
    model: qwen3-reranker-0.6b
    base_url: http://localhost:8100/v1
`;

const RAG_LOCAL_ONLY_PRESET = `rag:
  embedding_provider: local_hash
  embedding_model_alias: embedding
  reranker_provider: local_overlap
  reranker_model_alias: reranker

vector_db:
  provider: local_json
  collection: local_docs
  store_path: data/index/local_docs.json
`;

const RAG_EXTERNAL_PRESET = `rag:
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
`;

function collectProjectPreset() {
  return {
    name: document.getElementById('preset-name').value.trim(),
    runtime_profile: document.getElementById('preset-runtime-profile').value || 'current',
    watch_paths: document.getElementById('watch-paths').value,
    watch_project: document.getElementById('watch-project').value,
    watch_interval: Number(document.getElementById('watch-interval').value || 2),
    ingest_paths: document.getElementById('ingest-paths').value,
    ingest_project: document.getElementById('ingest-project').value,
    chat_request_name: document.getElementById('preset-chat-request-name').value,
    chat_expect_contains: document.getElementById('preset-chat-expect-contains').value.trim(),
    ingest_request_name: document.getElementById('preset-ingest-request-name').value,
    rag_project: document.getElementById('rag-project').value,
    rag_source_path: document.getElementById('rag-source-path').value.trim(),
    rag_top_k: getPositiveInt('rag-top-k', 5),
    rag_request_name: document.getElementById('preset-rag-request-name').value,
    rag_expect_contains: document.getElementById('preset-rag-expect-contains').value.trim(),
    eval_dataset: document.getElementById('eval-dataset').value,
    eval_project: document.getElementById('eval-project').value,
    eval_source_path: document.getElementById('eval-source-path').value.trim(),
    eval_top_k: getPositiveInt('eval-top-k', 5),
    eval_with_answer: document.getElementById('eval-with-answer').checked,
    eval_request_name: document.getElementById('preset-eval-request-name').value,
    eval_min_source_hit_rate: Number(document.getElementById('preset-eval-min-source-hit-rate').value || 0),
    workflow_run_smoke: document.getElementById('preset-run-smoke-first').checked,
    smoke_skip_qdrant: document.getElementById('preset-smoke-skip-qdrant').checked,
    smoke_skip_embedding: document.getElementById('preset-smoke-skip-embedding').checked,
    smoke_skip_reranker: document.getElementById('preset-smoke-skip-reranker').checked,
  };
}

function getPositiveInt(id, fallback) {
  const value = Number(document.getElementById(id).value);
  if (!Number.isFinite(value) || value < 1) {
    return fallback;
  }
  return Math.floor(value);
}

function readLocalSetting(key, fallback) {
  try {
    const value = window.localStorage.getItem(key);
    return value == null || value === '' ? fallback : value;
  } catch (error) {
    return fallback;
  }
}

function writeLocalSetting(key, value) {
  try {
    window.localStorage.setItem(key, String(value));
  } catch (error) {
    // Ignore persistence failures in embedded runtimes.
  }
}

function restorePresetCatalogControls() {
  presetCatalogFilter = readLocalSetting(PRESET_CATALOG_FILTER_KEY, presetCatalogFilter);
  presetCatalogSort = readLocalSetting(PRESET_CATALOG_SORT_KEY, presetCatalogSort);
  document.getElementById('preset-catalog-filter').value = presetCatalogFilter;
  document.getElementById('preset-catalog-sort').value = presetCatalogSort;
}

function restoreEvalDatasetTrendControls() {
  evalDatasetTrendFilter = readLocalSetting(EVAL_DATASET_TREND_FILTER_KEY, evalDatasetTrendFilter);
  evalDatasetTrendSort = readLocalSetting(EVAL_DATASET_TREND_SORT_KEY, evalDatasetTrendSort);
  document.getElementById('eval-dataset-trend-filter').value = evalDatasetTrendFilter;
  document.getElementById('eval-dataset-trend-sort').value = evalDatasetTrendSort;
}

function restoreRegressionWatchControls() {
  document.getElementById('regression-watch-source-hit-drop').value = String(regressionWatchSourceHitDrop);
  document.getElementById('regression-watch-include-preset').checked = regressionWatchIncludePreset;
  document.getElementById('regression-watch-include-dataset').checked = regressionWatchIncludeDataset;
}

function getBuiltinRegressionWatchProfiles() {
  return {
    balanced: {
      label: 'Balanced',
      sourceHitDrop: 0,
      includePreset: true,
      includeDataset: true,
      builtin: true,
    },
    strict: {
      label: 'Strict',
      sourceHitDrop: 0.05,
      includePreset: true,
      includeDataset: true,
      builtin: true,
    },
    preset_only: {
      label: 'Preset Only',
      sourceHitDrop: 0,
      includePreset: true,
      includeDataset: false,
      builtin: true,
    },
    dataset_only: {
      label: 'Dataset Only',
      sourceHitDrop: 0,
      includePreset: false,
      includeDataset: true,
      builtin: true,
    },
  };
}

function readLegacyRegressionWatchProfiles() {
  const builtin = getBuiltinRegressionWatchProfiles();
  try {
    const payload = window.localStorage.getItem(REGRESSION_WATCH_PROFILES_KEY);
    if (!payload) {
      return {};
    }
    const parsed = JSON.parse(payload);
    if (!parsed || typeof parsed !== 'object') {
      return {};
    }
    return Object.fromEntries(Object.entries(parsed).filter(([key]) => !builtin[key]));
  } catch (error) {
    return {};
  }
}

function writeLegacyRegressionWatchProfiles(profiles) {
  try {
    const builtinKeys = new Set(Object.keys(getBuiltinRegressionWatchProfiles()));
    const customOnly = Object.fromEntries(Object.entries(profiles || {}).filter(([key]) => !builtinKeys.has(key)));
    window.localStorage.setItem(REGRESSION_WATCH_PROFILES_KEY, JSON.stringify(customOnly));
  } catch (error) {
    // Ignore persistence failures in embedded runtimes.
  }
}

function readLegacyRegressionWatchSettings() {
  const hasStoredSourceHitDrop = window.localStorage.getItem(REGRESSION_WATCH_SOURCE_HIT_DROP_KEY) != null;
  const hasStoredIncludePreset = window.localStorage.getItem(REGRESSION_WATCH_INCLUDE_PRESET_KEY) != null;
  const hasStoredIncludeDataset = window.localStorage.getItem(REGRESSION_WATCH_INCLUDE_DATASET_KEY) != null;
  if (!hasStoredSourceHitDrop && !hasStoredIncludePreset && !hasStoredIncludeDataset) {
    return null;
  }
  let sourceHitDrop = Number(readLocalSetting(REGRESSION_WATCH_SOURCE_HIT_DROP_KEY, 0));
  if (!Number.isFinite(sourceHitDrop) || sourceHitDrop < 0) {
    sourceHitDrop = 0;
  }
  return {
    sourceHitDrop,
    includePreset: readLocalSetting(REGRESSION_WATCH_INCLUDE_PRESET_KEY, 'true') === 'true',
    includeDataset: readLocalSetting(REGRESSION_WATCH_INCLUDE_DATASET_KEY, 'true') === 'true',
  };
}

function getCurrentRegressionWatchSettings() {
  return {
    sourceHitDrop: regressionWatchSourceHitDrop,
    includePreset: regressionWatchIncludePreset,
    includeDataset: regressionWatchIncludeDataset,
  };
}

function applyRegressionWatchSettings(settings) {
  regressionWatchSourceHitDrop = Number(settings?.sourceHitDrop || 0);
  if (!Number.isFinite(regressionWatchSourceHitDrop) || regressionWatchSourceHitDrop < 0) {
    regressionWatchSourceHitDrop = 0;
  }
  regressionWatchIncludePreset = settings?.includePreset !== false;
  regressionWatchIncludeDataset = settings?.includeDataset !== false;
  writeLocalSetting(REGRESSION_WATCH_SOURCE_HIT_DROP_KEY, regressionWatchSourceHitDrop);
  writeLocalSetting(REGRESSION_WATCH_INCLUDE_PRESET_KEY, regressionWatchIncludePreset);
  writeLocalSetting(REGRESSION_WATCH_INCLUDE_DATASET_KEY, regressionWatchIncludeDataset);
  restoreRegressionWatchControls();
}

function normalizeRegressionWatchProfilePayload(profiles) {
  return Object.fromEntries(Object.entries(profiles || {}).map(([key, profile]) => [
    key,
    {
      label: profile.label || key,
      sourceHitDrop: Number(profile.sourceHitDrop || 0),
      includePreset: profile.includePreset !== false,
      includeDataset: profile.includeDataset !== false,
      builtin: profile.builtin === true,
    },
  ]));
}

function getMergedRegressionWatchProfiles() {
  return {
    ...getBuiltinRegressionWatchProfiles(),
    ...currentRegressionWatchProfiles,
  };
}

async function persistRegressionWatchSettings(settings = getCurrentRegressionWatchSettings()) {
  const normalized = {
    sourceHitDrop: Number(settings?.sourceHitDrop || 0),
    includePreset: settings?.includePreset !== false,
    includeDataset: settings?.includeDataset !== false,
  };
  if (!Number.isFinite(normalized.sourceHitDrop) || normalized.sourceHitDrop < 0) {
    normalized.sourceHitDrop = 0;
  }
  writeLocalSetting(REGRESSION_WATCH_SOURCE_HIT_DROP_KEY, normalized.sourceHitDrop);
  writeLocalSetting(REGRESSION_WATCH_INCLUDE_PRESET_KEY, normalized.includePreset);
  writeLocalSetting(REGRESSION_WATCH_INCLUDE_DATASET_KEY, normalized.includeDataset);
  try {
    await SetRegressionWatchSettings({
      source_hit_drop: normalized.sourceHitDrop,
      include_preset: normalized.includePreset,
      include_dataset: normalized.includeDataset,
    });
  } catch (error) {
    // Keep local fallback when backend persistence is unavailable.
  }
}

async function persistRegressionWatchProfiles(profiles = currentRegressionWatchProfiles) {
  const normalized = normalizeRegressionWatchProfilePayload(profiles);
  currentRegressionWatchProfiles = Object.fromEntries(Object.entries(normalized).filter(([, profile]) => !profile.builtin));
  writeLegacyRegressionWatchProfiles(currentRegressionWatchProfiles);
  try {
    await SetRegressionWatchProfiles(currentRegressionWatchProfiles);
  } catch (error) {
    // Keep local fallback when backend persistence is unavailable.
  }
}

async function restoreRegressionWatchState() {
  let backendSettings = null;
  let backendProfiles = null;
  try {
    backendSettings = await GetRegressionWatchSettings();
  } catch (error) {
    backendSettings = null;
  }
  try {
    backendProfiles = await GetRegressionWatchProfiles();
  } catch (error) {
    backendProfiles = null;
  }

  const legacySettings = readLegacyRegressionWatchSettings();
  const legacyProfiles = readLegacyRegressionWatchProfiles();
  const hasBackendProfiles = backendProfiles && Object.keys(backendProfiles).length > 0;
  const hasLegacyProfiles = Object.keys(legacyProfiles).length > 0;
  const backendLooksDefault = !backendSettings
    || (
      Number(backendSettings.source_hit_drop ?? backendSettings.sourceHitDrop ?? 0) === 0
      && (backendSettings.include_preset ?? backendSettings.includePreset ?? true) === true
      && (backendSettings.include_dataset ?? backendSettings.includeDataset ?? true) === true
    );

  if (legacySettings && backendLooksDefault) {
    applyRegressionWatchSettings(legacySettings);
    await persistRegressionWatchSettings(legacySettings);
  } else if (backendSettings) {
    applyRegressionWatchSettings({
      sourceHitDrop: backendSettings.source_hit_drop ?? backendSettings.sourceHitDrop ?? 0,
      includePreset: backendSettings.include_preset ?? backendSettings.includePreset ?? true,
      includeDataset: backendSettings.include_dataset ?? backendSettings.includeDataset ?? true,
    });
  } else if (legacySettings) {
    applyRegressionWatchSettings(legacySettings);
  } else {
    applyRegressionWatchSettings({sourceHitDrop: 0, includePreset: true, includeDataset: true});
  }

  if (hasLegacyProfiles && !hasBackendProfiles) {
    currentRegressionWatchProfiles = normalizeRegressionWatchProfilePayload(legacyProfiles);
    await persistRegressionWatchProfiles(currentRegressionWatchProfiles);
  } else if (backendProfiles) {
    currentRegressionWatchProfiles = normalizeRegressionWatchProfilePayload(backendProfiles);
  } else {
    currentRegressionWatchProfiles = normalizeRegressionWatchProfilePayload(legacyProfiles);
  }

  renderRegressionWatchProfileOptions();
}

function renderRegressionWatchProfileOptions() {
  const select = document.getElementById('regression-watch-profile-select');
  const profiles = getMergedRegressionWatchProfiles();
  const currentValue = select.value;
  select.innerHTML = '<option value="">Select watch profile</option>';
  Object.entries(profiles)
    .sort((left, right) => left[1].label.localeCompare(right[1].label))
    .forEach(([key, profile]) => {
      const option = document.createElement('option');
      option.value = key;
      option.textContent = profile.builtin ? `${profile.label} (Built-in)` : profile.label;
      select.appendChild(option);
    });
  if ([...select.options].some((option) => option.value === currentValue)) {
    select.value = currentValue;
  }
}

function isSourceHitRegressionAlert(latestValue, previousValue) {
  const latest = Number(latestValue);
  const previous = Number(previousValue);
  if (!Number.isFinite(latest) || !Number.isFinite(previous)) {
    return false;
  }
  const drop = previous - latest;
  return latest < previous && drop >= regressionWatchSourceHitDrop;
}

function applyProjectPreset(preset) {
  document.getElementById('preset-name').value = preset.name || '';
  document.getElementById('preset-runtime-profile').value = preset.runtime_profile || 'current';
  document.getElementById('watch-paths').value = preset.watch_paths || '';
  document.getElementById('watch-project').value = preset.watch_project || '';
  document.getElementById('watch-interval').value = String(preset.watch_interval || 2);
  document.getElementById('ingest-paths').value = preset.ingest_paths || '';
  document.getElementById('ingest-project').value = preset.ingest_project || '';
  document.getElementById('preset-chat-request-name').value = preset.chat_request_name || '';
  document.getElementById('preset-chat-expect-contains').value = preset.chat_expect_contains || '';
  document.getElementById('preset-ingest-request-name').value = preset.ingest_request_name || '';
  document.getElementById('rag-project').value = preset.rag_project || '';
  document.getElementById('rag-source-path').value = preset.rag_source_path || '';
  document.getElementById('rag-top-k').value = String(preset.rag_top_k || 5);
  document.getElementById('preset-rag-request-name').value = preset.rag_request_name || '';
  document.getElementById('preset-rag-expect-contains').value = preset.rag_expect_contains || '';
  document.getElementById('eval-dataset').value = preset.eval_dataset || 'configs/eval.sample.yaml';
  document.getElementById('eval-project').value = preset.eval_project || '';
  document.getElementById('eval-source-path').value = preset.eval_source_path || '';
  document.getElementById('eval-top-k').value = String(preset.eval_top_k || 5);
  document.getElementById('eval-with-answer').checked = preset.eval_with_answer === true;
  document.getElementById('preset-eval-request-name').value = preset.eval_request_name || '';
  document.getElementById('preset-eval-min-source-hit-rate').value = String(preset.eval_min_source_hit_rate || 0);
  document.getElementById('preset-run-smoke-first').checked = preset.workflow_run_smoke === true;
  document.getElementById('preset-smoke-skip-qdrant').checked = preset.smoke_skip_qdrant === true;
  document.getElementById('preset-smoke-skip-embedding').checked = preset.smoke_skip_embedding === true;
  document.getElementById('preset-smoke-skip-reranker').checked = preset.smoke_skip_reranker === true;
  renderSelectedPresetPreviewByName(preset.name || '');
  syncChatContextBarFromRagState();
  updateChatScopeSummary();
}

async function refreshPresets() {
  try {
    const presets = await GetProjectPresets();
    const select = document.getElementById('preset-select');
    const overviewSelect = document.getElementById('overview-preset-select');
    const compareLeftSelect = document.getElementById('preset-compare-left');
    const compareRightSelect = document.getElementById('preset-compare-right');
    const current = select.value;
    const overviewCurrent = overviewSelect.value;
    const compareLeftCurrent = compareLeftSelect.value || presetCompareLeftName;
    const compareRightCurrent = compareRightSelect.value || presetCompareRightName;
    select.innerHTML = '<option value="">Select preset</option>';
    overviewSelect.innerHTML = '<option value="">Select preset</option>';
    compareLeftSelect.innerHTML = '<option value="">Compare left preset</option>';
    compareRightSelect.innerHTML = '<option value="">Compare right preset</option>';
    presets.forEach((preset) => {
      const option = document.createElement('option');
      option.value = preset.name;
      option.textContent = preset.name;
      select.appendChild(option);
      overviewSelect.appendChild(option.cloneNode(true));
      compareLeftSelect.appendChild(option.cloneNode(true));
      compareRightSelect.appendChild(option.cloneNode(true));
    });
    if (presets.some((preset) => preset.name === current)) {
      select.value = current;
    }
    if (presets.some((preset) => preset.name === overviewCurrent)) {
      overviewSelect.value = overviewCurrent;
    }
    if (presets.some((preset) => preset.name === compareLeftCurrent)) {
      compareLeftSelect.value = compareLeftCurrent;
      presetCompareLeftName = compareLeftCurrent;
    } else {
      presetCompareLeftName = '';
    }
    if (presets.some((preset) => preset.name === compareRightCurrent)) {
      compareRightSelect.value = compareRightCurrent;
      presetCompareRightName = compareRightCurrent;
    } else {
      presetCompareRightName = '';
    }
    selectedBatchPresetNames = new Set(
      Array.from(selectedBatchPresetNames).filter((name) => presets.some((preset) => preset.name === name)),
    );
    void persistBatchPresetSelection();
    await refreshPresetValidationSnapshots(presets);
    renderPresetCatalog(presets);
    renderBatchPresetOutput();
    refreshChatContextProjectOptions(presets);
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    currentPresetValidationMap = new Map();
    renderPresetCatalog([]);
  }
}

async function refreshSavedRequests() {
  try {
    const requests = await GetSavedRequests();
    const configs = SAVED_REQUEST_KIND_CONFIGS.map((config) => {
      const select = document.getElementById(config.selectId);
      const presetSelect = config.presetSelectId ? document.getElementById(config.presetSelectId) : null;
      const currentValue = select.value;
      const presetCurrentValue = presetSelect ? presetSelect.value : '';
      select.innerHTML = `<option value="">${config.placeholder}</option>`;
      if (presetSelect) {
        presetSelect.innerHTML = `<option value="">${config.placeholder}</option>`;
      }
      return {
        ...config,
        select,
        presetSelect,
        currentValue,
        presetCurrentValue,
      };
    });

    configs.forEach((config) => {
      requests.filter((item) => item.kind === config.kind).forEach((item) => {
        const option = document.createElement('option');
        option.value = item.name;
        option.textContent = item.name;
        config.select.appendChild(option);
        if (config.presetSelect) {
          config.presetSelect.appendChild(option.cloneNode(true));
        }
      });
    });

    configs.forEach((config) => {
      config.select.value = [...config.select.options].some((option) => option.value === config.currentValue)
        ? config.currentValue
        : '';
      if (config.presetSelect) {
        config.presetSelect.value = [...config.presetSelect.options].some((option) => option.value === config.presetCurrentValue)
          ? config.presetCurrentValue
          : '';
      }
    });
    syncChatContextBarFromRagState();
  } catch (error) {
    setOutput('chat-output', String(error));
    setOutput('rag-output', String(error));
    setOutput('ingest-output', String(error));
    setOutput('eval-output', String(error));
  }
}

function splitMultilinePaths(rawValue) {
  return String(rawValue || '')
    .split('\n')
    .map((item) => item.trim())
    .filter(Boolean);
}

function parseTagList(rawValue) {
  return String(rawValue || '')
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean);
}

function applySavedRequestToForm(item) {
  if (!item) {
    return;
  }
  const config = SAVED_REQUEST_KIND_CONFIGS.find((entry) => entry.kind === item.kind);
  config?.applyToForm?.(item);
}

async function loadSavedRequestByName(kind, name) {
  const normalizedKind = String(kind || '').trim();
  const normalizedName = String(name || '').trim();
  if (!normalizedKind || !normalizedName) {
    throw new Error('Saved request kind and name are required.');
  }
  const requests = await GetSavedRequests();
  const item = requests.find((request) => request.kind === normalizedKind && request.name === normalizedName);
  if (!item) {
    throw new Error(`Saved ${normalizedKind} request not found: ${normalizedName}`);
  }
  applySavedRequestToForm(item);
  return item;
}

async function saveSavedRequestFromInputs({nameId, kind, buildPayload, onError, emptyMessage}) {
  const name = document.getElementById(nameId).value.trim();
  if (!name) {
    onError(emptyMessage);
    return null;
  }
  await SaveRequest({
    name,
    kind,
    ...buildPayload(),
  });
  await refreshSavedRequests();
  return name;
}

async function loadSavedRequestFromSelect({kind, selectId, onError, emptyMessage}) {
  const name = document.getElementById(selectId).value;
  if (!name) {
    onError(emptyMessage);
    return null;
  }
  return loadSavedRequestByName(kind, name);
}

async function deleteSavedRequestFromInputs({kind, selectId, nameId, onError, emptyMessage}) {
  const name = document.getElementById(selectId).value || document.getElementById(nameId).value.trim();
  if (!name) {
    onError(emptyMessage);
    return null;
  }
  await DeleteSavedRequest({name, kind});
  await refreshSavedRequests();
  return name;
}

function registerSavedRequestHandlers() {
  SAVED_REQUEST_KIND_CONFIGS.forEach((config) => {
    document.getElementById(config.saveButtonId).addEventListener('click', async () => {
      try {
        await saveSavedRequestFromInputs({
          nameId: config.nameId,
          kind: config.kind,
          buildPayload: config.buildPayload,
          onError: config.onError,
          emptyMessage: config.emptySaveMessage,
        });
      } catch (error) {
        config.onError(String(error));
      }
    });

    document.getElementById(config.loadButtonId).addEventListener('click', async () => {
      try {
        await loadSavedRequestFromSelect({
          kind: config.kind,
          selectId: config.selectId,
          onError: config.onError,
          emptyMessage: config.emptyLoadMessage,
        });
      } catch (error) {
        config.onError(String(error));
      }
    });

    document.getElementById(config.deleteButtonId).addEventListener('click', async () => {
      try {
        await deleteSavedRequestFromInputs({
          kind: config.kind,
          selectId: config.selectId,
          nameId: config.nameId,
          onError: config.onError,
          emptyMessage: config.emptyDeleteMessage,
        });
      } catch (error) {
        config.onError(String(error));
      }
    });
  });
}

async function resolveSelectedPreset() {
  const selectedName = document.getElementById('preset-select').value || document.getElementById('preset-name').value.trim();
  if (!selectedName) {
    throw new Error('Select or enter a preset name first.');
  }

  const presets = await GetProjectPresets();
  const preset = presets.find((item) => item.name === selectedName);
  if (!preset) {
    throw new Error(`Preset not found: ${selectedName}`);
  }
  return preset;
}

async function resolvePresetByName(name) {
  const selectedName = String(name || '').trim();
  if (!selectedName) {
    throw new Error('Select or enter a preset name first.');
  }

  const presets = await GetProjectPresets();
  const preset = presets.find((item) => item.name === selectedName);
  if (!preset) {
    throw new Error(`Preset not found: ${selectedName}`);
  }
  return preset;
}

function presetLabel(preset) {
  return preset?.name || 'selected preset';
}

function buildPresetValidationSteps(validation) {
  const steps = [];

  (validation?.warnings || []).forEach((warning) => {
    steps.push({name: 'preset_validation', status: 'failed', detail: warning});
  });
  (validation?.config_warnings || []).forEach((warning) => {
    steps.push({name: 'runtime_config', status: 'failed', detail: warning});
  });
  (validation?.path_checks || [])
    .filter((check) => check.required ? !check.exists : false)
    .forEach((check) => {
      steps.push({
        name: check.label || 'path_check',
        status: 'failed',
        detail: `${check.detail || 'missing path'}: ${check.resolved_path || check.path || '-'}`,
      });
    });
  (validation?.service_checks || [])
    .filter((check) => check.required && check.status !== 'running')
    .forEach((check) => {
      steps.push({
        name: check.name || 'service',
        status: 'failed',
        detail: check.detail || `required service is ${check.status || 'unavailable'}`,
      });
    });

  if (steps.length === 0 && validation) {
    steps.push({name: 'preset_validation', status: 'ok', detail: 'preset validation passed'});
  }

  return steps;
}

async function runIngestPaths(paths, {project = '', tags = [], sourceLabel = 'form'} = {}) {
  if (paths.length === 0) {
    setOutput('ingest-output', 'At least one path is required.');
    latestIngestExport = null;
    return {ok: false, detail: 'At least one path is required.'};
  }

  setOutput('ingest-output', 'Ingesting...');
  try {
    const response = await RunIngestAction({paths, project, tags, recursive: true});
    setOutput('ingest-output', response);
    await refreshExecutionHistory();
    const summary = [
      response?.indexed_documents != null ? `indexed_documents=${response.indexed_documents}` : '',
      response?.indexed_chunks != null ? `indexed_chunks=${response.indexed_chunks}` : '',
      response?.total_chunks != null ? `total_chunks=${response.total_chunks}` : '',
      response?.lw_data_root ? `lw_data_root=${response.lw_data_root}` : '',
    ].filter(Boolean).join(', ');
    latestIngestExport = {
      kind: 'ingest',
      title: `Ingest Result (${project || 'default'})`,
      content: [
        `- project: ${project || '(default)'}`,
        `- tags: ${tags.length > 0 ? tags.join(', ') : '(none)'}`,
        `- paths: ${paths.join(', ')}`,
        `- source: ${sourceLabel}`,
        `- provider: ${response?.provider || '-'}`,
        `- collection: ${response?.collection || '-'}`,
        `- lw_data_root: ${response?.lw_data_root || '-'}`,
        `- copied_files: ${Array.isArray(response?.copied_files) ? response.copied_files.length : '-'}`,
        `- indexed_documents: ${response?.indexed_documents ?? '-'}`,
        `- indexed_chunks: ${response?.indexed_chunks ?? '-'}`,
        `- total_chunks: ${response?.total_chunks ?? '-'}`,
        '',
        '## Raw Response',
        '```json',
        JSON.stringify(response, null, 2),
        '```',
      ].join('\n'),
      fileStem: `ingest-${(project || 'default').toLowerCase().replaceAll(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') || 'default'}`,
    };
    return {ok: true, detail: summary || `ingested ${paths.length} path(s)`};
  } catch (error) {
    setOutput('ingest-output', String(error));
    await refreshExecutionHistory();
    latestIngestExport = {
      kind: 'ingest',
      title: `Ingest Result (${project || 'default'})`,
      content: [
        `- project: ${project || '(default)'}`,
        `- tags: ${tags.length > 0 ? tags.join(', ') : '(none)'}`,
        `- paths: ${paths.join(', ')}`,
        `- source: ${sourceLabel}`,
        '',
        '## Error',
        String(error),
      ].join('\n'),
      fileStem: `ingest-${(project || 'default').toLowerCase().replaceAll(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') || 'default'}-error`,
    };
    return {ok: false, detail: String(error)};
  }
}

async function runIngestFromForm() {
  const rawPaths = document.getElementById('ingest-paths').value;
  const project = document.getElementById('ingest-project').value;
  const tags = parseTagList(document.getElementById('ingest-tags').value);
  const paths = splitMultilinePaths(rawPaths);
  return runIngestPaths(paths, {project, tags, sourceLabel: 'manual form'});
}

function findDropIngestZoneFromPoint(x, y) {
  const target = document.elementFromPoint(x, y);
  return target?.closest?.('[data-drop-ingest-zone]') || null;
}

async function handleDroppedIngestPaths(paths, zoneId = '') {
  const result = classifyDroppedPaths(paths);
  const project = document.getElementById('ingest-project').value.trim();
  const tags = parseTagList(document.getElementById('ingest-tags').value);
  document.getElementById('ingest-paths').value = result.accepted.join('\n');

  if (result.accepted.length === 0) {
    const message = 'No supported files or directories were dropped.';
    setOutput('ingest-output', message);
    setChatDropStatus(message);
    setOutput('runtime-config-status', message);
    latestIngestExport = null;
    return {ok: false, detail: message};
  }

  if (zoneId === 'library' || zoneId === 'workspace') {
    activateTab('rag');
  }

  const response = await runIngestPaths(result.accepted, {
    project,
    tags,
    sourceLabel: `drag-and-drop:${zoneId || 'unknown'}`,
  });

  if (response.ok) {
    const message = buildDropResultMessage({
      acceptedCount: result.accepted.length,
      skippedCount: result.skippedCount,
      project,
      originLabel: zoneId || 'drop zone',
    });
    setChatDropStatus(zoneId === 'chat' || zoneId === 'workspace' ? message : '');
    setOutput('runtime-config-status', message);
  } else {
    const message = String(response.detail || 'Drop ingest failed.');
    setChatDropStatus(message);
    setOutput('runtime-config-status', message);
  }
  return response;
}

function bindIngestDropTargets() {
  const zones = [...document.querySelectorAll('[data-drop-ingest-zone]')];
  zones.forEach((zone) => {
    zone.addEventListener('dragenter', (event) => {
      event.preventDefault();
      setActiveIngestDropZone(zone.dataset.dropIngestZone || '');
    });
    zone.addEventListener('dragover', (event) => {
      event.preventDefault();
      setActiveIngestDropZone(zone.dataset.dropIngestZone || '');
    });
    zone.addEventListener('dragleave', (event) => {
      if (!zone.contains(event.relatedTarget)) {
        setActiveIngestDropZone('');
      }
    });
    zone.addEventListener('drop', (event) => {
      event.preventDefault();
      setActiveIngestDropZone('');
    });
  });

  OnFileDrop(async (x, y, droppedPaths) => {
    const zone = findDropIngestZoneFromPoint(x, y);
    setActiveIngestDropZone('');
    if (!zone) {
      return;
    }
    await handleDroppedIngestPaths(droppedPaths, zone.dataset.dropIngestZone || '');
  }, true);

  window.addEventListener('beforeunload', () => {
    OnFileDropOff();
  }, {once: true});
}

async function runEmbeddingProbe() {
  const model = document.getElementById('embedding-model').value.trim() || 'auto';
  const input = document.getElementById('embedding-input').value.trim();
  if (!input) {
    renderRuntimeMessage('embedding-output', 'Embedding input is required.');
    return {ok: false, detail: 'Embedding input is required.'};
  }

  renderRuntimeMessage('embedding-output', 'Running embedding request...');
  try {
    const response = await RunEmbeddingAction({model, input});
    renderEmbeddingResult(response, {model, input});
    await refreshExecutionHistory();
    return {ok: true, detail: 'Embedding request completed.'};
  } catch (error) {
    renderRuntimeMessage('embedding-output', String(error));
    await refreshExecutionHistory();
    return {ok: false, detail: String(error)};
  }
}

async function runIndexBrowser() {
  const project = document.getElementById('index-project').value.trim();
  const sourceQuery = document.getElementById('index-source-query').value.trim();
  const limit = getPositiveInt('index-limit', 20);

  renderRuntimeMessage('index-output', 'Loading indexed chunks...');
  try {
    currentIndexSourceResponse = null;
    latestIndexExport = null;
    const response = await RunIndexBrowseAction({
      project,
      source_query: sourceQuery,
      limit,
    });
    renderIndexBrowseResult(response);
    await refreshExecutionHistory();
    return {ok: true, detail: `${response.filtered_chunks || 0} chunks matched`};
  } catch (error) {
    renderRuntimeMessage('index-output', String(error));
    await refreshExecutionHistory();
    return {ok: false, detail: String(error)};
  }
}

async function openIndexSource({sourcePath, project, limit: requestedLimit = null}) {
  const limit = Math.max(
    Number.isFinite(Number(requestedLimit)) && Number(requestedLimit) > 0
      ? Number(requestedLimit)
      : getPositiveInt('index-limit', 20),
    20,
  );
  renderRuntimeMessage('index-output', 'Loading source chunks...');
  const response = await GetIndexSource({
    source_path: sourcePath,
    project,
    limit,
  });
  document.getElementById('index-limit').value = String(limit);
  currentIndexSourceResponse = response;
  latestIndexExport = buildIndexSourceExportPayload(response);
  renderIndexBrowseResult(currentIndexBrowseResponse || {projects: [], sources: [], chunks: [], filtered_chunks: 0, total_chunks: 0});
  return response;
}

async function runEvalFromForm() {
  const datasetPath = document.getElementById('eval-dataset').value;
  const project = document.getElementById('eval-project').value;
  const sourcePath = document.getElementById('eval-source-path').value.trim();
  const topK = getPositiveInt('eval-top-k', 5);
  const withAnswer = document.getElementById('eval-with-answer').checked;
  if (!datasetPath.trim()) {
    renderRuntimeMessage('eval-output', 'Dataset path is empty.');
    return {ok: false, detail: 'Dataset path is empty.'};
  }

  renderRuntimeMessage('eval-output', 'Running eval...');
  try {
    const response = await RunEvalAction({dataset_path: datasetPath, project, source_path: sourcePath, top_k: topK, with_answer: withAnswer});
    renderEvalResult(response);
    await refreshExecutionHistory();
    return {
      ok: true,
      detail: `source_hit_rate=${response.source_hit_rate}, keyword_hit_rate=${response.keyword_hit_rate ?? '-'}, total_cases=${response.total_cases ?? '-'}, average_latency_ms=${response.average_latency_ms ?? '-'}, total_prompt_tokens=${response.total_prompt_tokens ?? '-'}, total_completion_tokens=${response.total_completion_tokens ?? '-'}, total_tokens=${response.total_tokens ?? '-'}`,
      sourceHitRate: response.source_hit_rate,
      keywordHitRate: response.keyword_hit_rate,
      totalCases: response.total_cases,
    };
  } catch (error) {
    renderRuntimeMessage('eval-output', String(error));
    await refreshExecutionHistory();
    return {ok: false, detail: String(error)};
  }
}

function selectedPreferenceReasonTags() {
  return [...document.querySelectorAll('#preference-reason-tags input:checked')]
    .map((input) => input.value);
}

function clearPreferenceVoteForm() {
  preferenceSelection = null;
  document.querySelectorAll('#preference-reason-tags input').forEach((input) => {
    input.checked = false;
  });
  document.getElementById('preference-note').value = '';
  document.getElementById('preference-sft-approval').checked = false;
  updatePreferenceSelectionUI();
}

function updatePreferenceSelectionUI() {
  document.querySelectorAll('[data-preference-choice], [data-preference-selection]').forEach((button) => {
    const value = button.dataset.preferenceChoice || button.dataset.preferenceSelection;
    button.classList.toggle('selected', value === preferenceSelection);
    button.disabled = preferenceSaving || !preferencePair;
  });
  const submit = document.getElementById('preference-submit');
  submit.disabled = preferenceSaving || !preferencePair || !preferenceSelection;
  submit.textContent = preferenceSaving ? 'Saving…' : preferenceCorrectionVoteId
    ? 'Save correction · Enter'
    : 'Submit vote · Enter';
  document.getElementById('preference-correct').disabled = preferenceSaving || !preferenceLastVote;
  const approved = document.getElementById('preference-sft-approval');
  approved.disabled = !['left', 'right'].includes(preferenceSelection || '');
  if (approved.disabled) {
    approved.checked = false;
  }
}

function choosePreference(selection) {
  if (!preferencePair || preferenceSaving || !['left', 'right', 'tie', 'skip'].includes(selection)) {
    return;
  }
  preferenceSelection = selection;
  updatePreferenceSelectionUI();
}

function renderPreferencePair(pair) {
  preferencePair = pair || null;
  const container = document.getElementById('preference-review');
  try {
    container.innerHTML = renderBlindPreferencePair(preferencePair, escapeHtml);
  } catch (error) {
    preferencePair = null;
    container.innerHTML = '<div class="preference-empty">Blind化されていない候補を拒否しました．</div>';
    renderRuntimeMessage('preference-status', String(error));
  }
  updatePreferenceSelectionUI();
}

function renderPreferenceStats(stats) {
  if (!stats) {
    document.getElementById('preference-stats').innerHTML = '';
    return;
  }
  const left = Number(stats.display_selections?.left || 0);
  const right = Number(stats.display_selections?.right || 0);
  const leftRate = Math.round(Number(stats.display_selection_rate?.left || 0) * 100);
  const rightRate = Math.round(Number(stats.display_selection_rate?.right || 0) * 100);
  const reasonTags = Object.entries(stats.reason_tags || {})
    .sort((a, b) => Number(b[1]) - Number(a[1]))
    .map(([tag, count]) => `${escapeHtml(tag)}=${escapeHtml(String(count))}`)
    .join(' · ') || 'まだありません．';
  const categories = Object.entries(stats.categories || {})
    .map(([category, counts]) => {
      const summary = Object.entries(counts || {}).map(([key, count]) => `${key}:${count}`).join('，');
      return `<li><span>${escapeHtml(category)}</span><strong>${escapeHtml(summary || '-')}</strong></li>`;
    }).join('');
  const comparison = renderPromptComparison(stats.comparison, escapeHtml);
  document.getElementById('preference-progress').textContent =
    `${stats.reviewed || 0} reviewed · ${stats.remaining || 0} remaining`;
  document.getElementById('preference-stats').innerHTML = `
    <div class="preference-stat-grid">
      <div><strong>${escapeHtml(String(stats.reviewed || 0))}</strong><span>reviewed</span></div>
      <div><strong>${escapeHtml(String(stats.remaining || 0))}</strong><span>remaining</span></div>
      <div><strong>${escapeHtml(String(stats.tie || 0))}</strong><span>tie</span></div>
      <div><strong>${escapeHtml(String(stats.skip || 0))}</strong><span>skip</span></div>
      <div><strong>${escapeHtml(String(stats.duplicate_generation || 0))}</strong><span>duplicate</span></div>
      <div><strong>${escapeHtml(`${leftRate}% / ${rightRate}%`)}</strong><span>left / right</span></div>
    </div>
    <div class="runtime-result-text"><strong>Reason tags：</strong>${reasonTags}</div>
    ${comparison}
    ${categories ? `<ul class="preference-category-stats">${categories}</ul>` : ''}
  `;
}

async function refreshPreferenceStats() {
  if (!preferenceSessionId) {
    return null;
  }
  const stats = await PreferenceStats(preferenceSessionId);
  renderPreferenceStats(stats);
  return stats;
}

async function refreshPreferenceSessions({quiet = false} = {}) {
  try {
    const response = await ListPreferenceSessions();
    const sessions = Array.isArray(response?.sessions) ? response.sessions : [];
    const select = document.getElementById('preference-session-select');
    select.innerHTML = [
      '<option value="">Select session</option>',
      ...sessions.map((session) => {
        const comparison = {
          prompt_v1_v2: 'prompt v1/v2',
          prompt_v2_v3: 'prompt v2/v3',
          base_vs_adapter: 'base/LoRA',
          same_prompt: 'same prompt',
        }[session.comparison_mode] || session.comparison_mode;
        const label = `${session.created_at || ''} · ${session.reviewed || 0}/${session.target_pairs || 0} · ${session.model_role || 'fast'} · ${comparison}`;
        return `<option value="${escapeHtml(session.session_id || '')}">${escapeHtml(label)}</option>`;
      }),
    ].join('');
    if (preferenceSessionId && sessions.some((session) => session.session_id === preferenceSessionId)) {
      select.value = preferenceSessionId;
    }
    return sessions;
  } catch (error) {
    if (!quiet) {
      renderRuntimeMessage('preference-status', String(error));
    }
    return [];
  }
}

async function prefetchPreferencePairs(limit = 1) {
  if (!preferenceSessionId) {
    return null;
  }
  if (preferencePrefetchPromise) {
    return preferencePrefetchPromise;
  }
  const generationLimit = preferenceGenerationLimit(limit);
  if (generationLimit === 0) {
    return null;
  }
  preferencePrefetchPromise = (async () => {
    try {
      await GeneratePreferencePairs(preferenceSessionId, {limit: generationLimit});
      return await refreshPreferenceStats();
    } catch (error) {
      renderRuntimeMessage('preference-status', String(error));
      return null;
    } finally {
      preferencePrefetchPromise = null;
    }
  })();
  return preferencePrefetchPromise;
}

async function loadNextPreferencePair({generateIfNeeded = true} = {}) {
  if (!preferenceSessionId) {
    return null;
  }
  const response = await NextPreferencePair(preferenceSessionId);
  if (response?.pair) {
    assertBlindPreferencePair(response.pair);
    preferenceCorrectionVoteId = '';
    clearPreferenceVoteForm();
    renderPreferencePair(response.pair);
    await refreshPreferenceStats();
    void prefetchPreferencePairs(1);
    return response.pair;
  }
  const stats = await refreshPreferenceStats();
  if (generateIfNeeded && Number(stats?.generated || 0) < Number(stats?.target_pairs || 0)) {
    await prefetchPreferencePairs(Number(stats.target_pairs) - Number(stats.generated || 0));
    return loadNextPreferencePair({generateIfNeeded: false});
  }
  renderPreferencePair(null);
  renderRuntimeMessage('preference-status', preferenceEmptyMessage(stats));
  return null;
}

async function startPreferenceSession() {
  if (preferenceSaving) {
    return;
  }
  const datasetPath = document.getElementById('preference-dataset').value.trim();
  const pairCount = Math.min(Math.max(getPositiveInt('preference-count', 20), 1), 100);
  if (!datasetPath) {
    renderRuntimeMessage('preference-status', 'Dataset path is empty.');
    return;
  }
  renderRuntimeMessage('preference-status', 'Sessionを作成し，最初の候補を生成しています…');
  try {
    const session = await CreatePreferenceSession({
      dataset_path: datasetPath,
      model_role: document.getElementById('preference-role').value || 'fast',
      pair_count: pairCount,
      prefetch: PREFERENCE_GENERATION_BATCH_SIZE,
      comparison_mode: document.getElementById('preference-comparison').value || 'base_vs_adapter',
      adapter_scale: Number(document.getElementById('preference-adapter-scale').value || 1),
      generation_parameters: {temperature: 0.8, top_p: 0.95, max_tokens: 512},
    });
    preferenceSessionId = session.session_id;
    preferenceLastVote = null;
    preferenceCorrectionVoteId = '';
    await GeneratePreferencePairs(preferenceSessionId, {limit: preferenceGenerationLimit(pairCount)});
    await refreshPreferenceSessions({quiet: true});
    const pair = await loadNextPreferencePair();
    if (pair) {
      renderRuntimeMessage('preference-status', '候補をblind表示しました．自然な方を選んでください．');
    }
    await refreshExecutionHistory();
  } catch (error) {
    renderPreferencePair(null);
    renderRuntimeMessage('preference-status', String(error));
  }
}

async function resumePreferenceSession() {
  const selected = document.getElementById('preference-session-select').value;
  if (!selected) {
    renderRuntimeMessage('preference-status', '再開するsessionを選択してください．');
    return;
  }
  preferenceSessionId = selected;
  preferenceLastVote = null;
  preferenceCorrectionVoteId = '';
  try {
    const pair = await loadNextPreferencePair();
    if (pair) {
      renderRuntimeMessage('preference-status', '未評価候補からsessionを再開しました．');
    }
  } catch (error) {
    renderRuntimeMessage('preference-status', String(error));
  }
}

async function submitPreferenceVote() {
  if (preferenceSaving || !preferencePair || !preferenceSelection) {
    return false;
  }
  preferenceSaving = true;
  updatePreferenceSelectionUI();
  const pair = preferencePair;
  try {
    const response = await VotePreferencePair(pair.pair_id, {
      selection: preferenceSelection,
      reason_tags: selectedPreferenceReasonTags(),
      note: document.getElementById('preference-note').value.trim(),
      approved_for_sft: ['left', 'right'].includes(preferenceSelection)
        && document.getElementById('preference-sft-approval').checked,
      supersedes_vote_id: preferenceCorrectionVoteId || '',
    });
    preferenceLastVote = {pair, voteId: response.vote_id};
    preferenceCorrectionVoteId = '';
    clearPreferenceVoteForm();
    await loadNextPreferencePair();
    await refreshExecutionHistory();
    return true;
  } catch (error) {
    renderRuntimeMessage('preference-status', String(error));
    return false;
  } finally {
    preferenceSaving = false;
    updatePreferenceSelectionUI();
  }
}

function correctPreviousPreferenceVote() {
  if (!preferenceLastVote || preferenceSaving) {
    return;
  }
  preferenceCorrectionVoteId = preferenceLastVote.voteId;
  clearPreferenceVoteForm();
  renderPreferencePair(preferenceLastVote.pair);
  renderRuntimeMessage('preference-status', '直前の候補を再表示しました．新しい選択を保存すると訂正履歴が追記されます．');
}

async function exportPreference(format) {
  if (!preferenceSessionId) {
    renderRuntimeMessage('preference-status', '先にsessionを開始または再開してください．');
    return;
  }
  const outputId = format === 'dpo' ? 'preference-dpo-output' : 'preference-sft-output';
  try {
    const response = await ExportPreferenceSession(preferenceSessionId, {
      format,
      output: document.getElementById(outputId).value.trim(),
    });
    renderRuntimeMessage('preference-status', `${format.toUpperCase()}を${response.output}へ${response.records}件exportしました．`);
    await refreshExecutionHistory();
  } catch (error) {
    renderRuntimeMessage('preference-status', String(error));
  }
}

async function runChatFromForm() {
  const mode = document.getElementById('chat-mode').value;
  const promptInput = document.getElementById('chat-prompt');
  const prompt = promptInput.value;
  if (!prompt.trim()) {
    failStreamingChat({requestId: activeChatStreamRequestId, message: 'Prompt is empty.'});
    return {ok: false, detail: 'Prompt is empty.'};
  }
  if (chatSendInFlight) {
    return {ok: false, detail: 'Chat request already in progress.'};
  }
  let webSearch;
  try {
    webSearch = await prepareWebSearch(prompt);
  } catch (error) {
    setChatWebStatus('Web planning failed', 'warning');
    setChatDropStatus(String(error));
    return {ok: false, detail: String(error)};
  }
  if (!webSearch) {
    return {ok: false, detail: 'Web search cancelled.'};
  }
  const requestId = createChatRequestId();
  beginStreamingChat({
    requestId,
    prompt,
    modeLabel: getChatModeLabel(mode),
  });
  setChatSendState(true);
  if (mode === 'rag' && !webSearch.web_search) {
    document.getElementById('rag-query').value = prompt;
    try {
      const result = await runRagRequest({answer: true, queryOverride: prompt, origin: 'chat', requestId});
      if (result?.ok) {
        promptInput.value = '';
        if (!isLengthLimitedFinishReason(result?.finishReason)) {
          await autoPlanKarteConversation(requestId);
        }
      }
      return result;
    } catch (error) {
      failStreamingChat({requestId, message: String(error)});
      await refreshExecutionHistory();
      return {ok: false, detail: String(error)};
    } finally {
      setChatSendState(false);
    }
  }
  try {
    const startedAt = performance.now();
    let routePlan = null;
    const grounding = buildChatGroundingPayload();
    try {
      routePlan = await RoutePlan({mode, prompt});
      renderRouteDecision(routePlan);
    } catch (routeError) {
      renderRouteInspectorCard({
        selectedMode: mode,
        backendModel: '-',
        ragEnabled: false,
        sourceCount: 0,
        latencyMs: null,
        routeReason: String(routeError),
      });
    }
    const response = await RunChatAction({
      mode,
      prompt,
      ...grounding,
      ...webSearch,
      temperature: 0.2,
      max_tokens: getChatMaxTokens(mode),
      request_id: requestId,
      stream: true,
    });
    renderChatResult(response, mode, prompt, {
      selectedMode: routePlan?.mode || mode,
      backendModel: routePlan?.backend_model || '-',
      ragEnabled: (response?.sources?.length || 0) > 0,
      sourceCount: response?.sources?.length || 0,
      latencyMs: Math.round(performance.now() - startedAt),
      routeReason: routePlan
        ? `mode=${routePlan.mode}, provider=${routePlan.provider}, grounding_sources=${response?.sources?.length || 0}`
        : 'direct chat request',
    }, {appendToChat: false});
    finalizeStreamingChat({
      requestId,
      meta: isLengthLimitedFinishReason(response?.finish_reason)
        ? `${getChatModeLabel(mode)} · output limit reached`
        : `${getChatModeLabel(mode)} · done`,
      answer: response?.answer || '',
      thinking: response?.thinking || '',
      finishReason: response?.finish_reason || '',
    });
    if (!isLengthLimitedFinishReason(response?.finish_reason)) {
      await autoPlanKarteConversation(requestId);
    }
    await refreshExecutionHistory();
    promptInput.value = '';
    return {
      ok: true,
      detail: String(response.answer || '').slice(0, 200) || 'chat completed',
      text: String(response.answer || ''),
      answerChars: String(response.answer || '').length,
    };
  } catch (error) {
    failStreamingChat({requestId, message: String(error)});
    await refreshExecutionHistory();
    return {ok: false, detail: String(error)};
  } finally {
    setChatSendState(false);
  }
}

async function continueChatGeneration(requestId) {
  const entry = chatThreadEntries.find((item) => item.requestId === requestId);
  if (!entry) {
    return {ok: false, detail: 'Continuation target not found.'};
  }
  if (chatSendInFlight) {
    return {ok: false, detail: 'Chat request already in progress.'};
  }

  const mode = entry.mode || document.getElementById('chat-mode').value || 'auto';
  const continuationPrompt = buildContinuationPrompt(entry);
  const continuationRequestId = createChatRequestId();
  const modeLabel = getChatModeLabel(mode);
  beginStreamingContinuation({
    targetRequestId: requestId,
    requestId: continuationRequestId,
    mode,
    modeLabel,
  });
  setChatSendState(true);

  if (mode === 'rag') {
    document.getElementById('rag-query').value = entry.prompt || '';
    try {
      const result = await runRagRequest({
        answer: true,
        queryOverride: continuationPrompt,
        origin: 'chat',
        requestId: continuationRequestId,
      });
      finalizeStreamingChat({
        requestId: continuationRequestId,
        meta: isLengthLimitedFinishReason(result?.finishReason)
          ? `${modeLabel} · output limit reached`
          : `${modeLabel} · continued`,
        finishReason: result?.finishReason || '',
      });
      if (result?.ok && !isLengthLimitedFinishReason(result?.finishReason)) {
        await autoPlanKarteConversation(continuationRequestId);
      }
      await refreshExecutionHistory();
      return result;
    } catch (error) {
      failStreamingChat({requestId: continuationRequestId, message: String(error)});
      await refreshExecutionHistory();
      return {ok: false, detail: String(error)};
    } finally {
      setChatSendState(false);
    }
  }

  try {
    const grounding = buildChatGroundingPayload();
    const response = await RunChatAction({
      mode,
      prompt: continuationPrompt,
      ...grounding,
      temperature: 0.2,
      max_tokens: getChatMaxTokens(mode),
      request_id: continuationRequestId,
      stream: true,
    });
    finalizeStreamingChat({
      requestId: continuationRequestId,
      meta: isLengthLimitedFinishReason(response?.finish_reason)
        ? `${modeLabel} · output limit reached`
        : `${modeLabel} · continued`,
      answer: response?.answer || '',
      thinking: response?.thinking || '',
      finishReason: response?.finish_reason || '',
    });
    if (!isLengthLimitedFinishReason(response?.finish_reason)) {
      await autoPlanKarteConversation(continuationRequestId);
    }
    await refreshExecutionHistory();
    return {
      ok: true,
      detail: String(response.answer || '').slice(0, 200) || 'chat continuation completed',
      text: String(response.answer || ''),
      answerChars: String(response.answer || '').length,
      finishReason: response?.finish_reason || '',
    };
  } catch (error) {
    failStreamingChat({requestId: continuationRequestId, message: String(error)});
    await refreshExecutionHistory();
    return {ok: false, detail: String(error)};
  } finally {
    setChatSendState(false);
  }
}

async function runPresetVerificationWorkflow(preset) {
  applyProjectPreset(preset);
  const response = await RunPresetVerification(preset);
  const ok = response?.status === 'ok';
  renderWorkflowResult(`Preset Verification: ${presetLabel(preset)}`, ok ? 'completed' : 'failed', response?.steps || []);
  await refreshExecutionHistory();
  await refreshOverview();
  await refreshRuntime();
  setOutput('runtime-config-status', ok ? `Preset verification completed: ${preset.name}` : `Preset verification found issues: ${preset.name}`);
}

async function runRagRequest({answer, queryOverride = '', origin = 'library', requestId = ''}) {
  const query = queryOverride || document.getElementById('rag-query').value;
  const project = document.getElementById('rag-project').value;
  const sourcePath = document.getElementById('rag-source-path').value.trim();
  const tags = parseTagList(document.getElementById('rag-tags').value);
  const topK = getPositiveInt('rag-top-k', 5);
  if (!query.trim()) {
    renderRuntimeMessage('rag-output', 'Query is empty.');
    return {ok: false, detail: 'Query is empty.', text: ''};
  }

  renderRuntimeMessage('rag-output', answer ? 'Querying...' : 'Searching...');
  const startedAt = performance.now();
  if (answer) {
    const response = await RunRagQueryAction({
      query,
      project,
      source_path: sourcePath,
      tags,
      top_k: topK,
      answer: true,
      request_id: requestId,
      stream: origin === 'chat',
    });
    const sourceCount = response.sources?.length || 0;
    const topSourcePath = response.sources?.[0]?.source_path || '';
    renderRagResult(response, {
      answerMode: true,
      query,
      routeState: {
        selectedMode: 'rag',
        backendModel: 'RAG Query',
        ragEnabled: true,
        sourceCount,
        latencyMs: Math.round(performance.now() - startedAt),
        routeReason: origin === 'chat' ? 'Chat mode "With Sources" delegates to /v1/rag/query.' : 'Library source-backed query.',
      },
      appendToChat: origin !== 'chat',
    });
    if (origin === 'chat') {
      finalizeStreamingChat({
        requestId,
        meta: isLengthLimitedFinishReason(response?.finish_reason)
          ? `With Sources · output limit reached`
          : `With Sources · ${sourceCount} sources`,
        answer: response?.answer || '',
        thinking: response?.thinking || '',
        finishReason: response?.finish_reason || '',
      });
    }
    await refreshExecutionHistory();
    return {
      ok: true,
      detail: String(response.answer || '').slice(0, 200) || 'rag query completed',
      text: String(response.answer || ''),
      sourceCount,
      topSourcePath,
      answerChars: String(response.answer || '').length,
      finishReason: response?.finish_reason || '',
    };
  }

  const response = await RunRagSearchAction({query, project, source_path: sourcePath, tags, top_k: topK});
  const sourceCount = response.results?.length || 0;
  const topSourcePath = response.results?.[0]?.source_path || '';
  renderRagResult(response, {
    answerMode: false,
    query,
    routeState: {
      selectedMode: 'rag',
      backendModel: 'RAG Search',
      ragEnabled: true,
      sourceCount,
      latencyMs: Math.round(performance.now() - startedAt),
      routeReason: 'Library search against indexed sources.',
    },
  });
  await refreshExecutionHistory();
  const joined = (response.results || []).map((item) => item.chunk_text || '').join('\n');
  return {
    ok: true,
    detail: `${response.results?.length || 0} results`,
    text: joined,
    sourceCount,
    topSourcePath,
    answerChars: joined.length,
  };
}

function runtimeConfigMatchesProfile(configSummary, profile) {
  const normalized = normalizePresetRuntimeProfile(profile);
  if (normalized === 'current') {
    return true;
  }

  const summary = configSummary || {};
  if (normalized === 'local_only') {
    return summary.embedding_provider === 'local_hash' && summary.vector_db_provider === 'local_json';
  }
  if (normalized === 'external_rag') {
    return summary.embedding_provider === 'openai_compatible' && summary.vector_db_provider === 'qdrant';
  }
  return true;
}

async function refreshOverview() {
  try {
    const [health, models, exports] = await Promise.all([Health(), Models(), ListExportedResults()]);
    setOutput('health-output', health);
    setOutput('models-output', models);
    renderExportedResults(exports);
    if (!exports || exports.length === 0) {
      renderExportPreview(null);
    }
    document.getElementById('gateway-status').textContent = health.status;
    document.getElementById('model-count').textContent = String(models.data.length);
  } catch (error) {
    setOutput('health-output', String(error));
    setOutput('models-output', String(error));
    renderExportedResults([{name: 'Export Load Failed', path: String(error), mod_time: ''}]);
    renderExportPreview({name: 'Preview Error', path: '-', content: String(error)});
    document.getElementById('gateway-status').textContent = 'unreachable';
  }
}

async function refreshRuntime() {
  try {
    const runtime = await GetRuntimeStatus();
    latestRuntimeStatus = runtime;
    document.getElementById('runtime-fast-status').textContent = runtime.fast_running ? 'running' : 'stopped';
    document.getElementById('runtime-fast-pid').textContent = runtime.fast_pid ? `PID: ${runtime.fast_pid}` : 'PID: -';
    document.getElementById('runtime-work-status').textContent = runtime.work_running ? 'running' : 'stopped';
    document.getElementById('runtime-work-pid').textContent = runtime.work_pid ? `PID: ${runtime.work_pid}` : 'PID: -';
    document.getElementById('runtime-code-status').textContent = runtime.code_running ? 'running' : 'stopped';
    document.getElementById('runtime-code-pid').textContent = runtime.code_pid ? `PID: ${runtime.code_pid}` : 'PID: -';
    document.getElementById('runtime-gateway-status').textContent = runtime.gateway_running ? 'running' : 'stopped';
    document.getElementById('runtime-gateway-pid').textContent = runtime.gateway_pid ? `PID: ${runtime.gateway_pid}` : 'PID: -';
    document.getElementById('runtime-watch-status').textContent = runtime.watch_running ? 'running' : 'stopped';
    document.getElementById('runtime-watch-pid').textContent = runtime.watch_pid ? `PID: ${runtime.watch_pid}` : 'PID: -';
    document.getElementById('runtime-embedding-status').textContent = runtime.embedding_running ? 'running' : 'stopped';
    document.getElementById('runtime-embedding-pid').textContent = runtime.embedding_pid ? `PID: ${runtime.embedding_pid}` : 'PID: -';
    document.getElementById('runtime-qdrant-status').textContent = runtime.qdrant_running ? 'running' : 'stopped';
    document.getElementById('runtime-qdrant-detail').textContent = runtime.qdrant_detail || 'Local binary: -';
    document.getElementById('runtime-workspace').textContent = runtime.workspace_root || '-';
    setOutput('runtime-fast-log', runtime.fast_logs || []);
    setOutput('runtime-work-log', runtime.work_logs || []);
    setOutput('runtime-code-log', runtime.code_logs || []);
    setOutput('runtime-gateway-log', runtime.gateway_logs || []);
    setOutput('runtime-embedding-log', runtime.embedding_logs || []);
    setOutput('runtime-qdrant-log', runtime.qdrant_logs || []);
    setOutput('runtime-watch-log', runtime.watch_logs || []);
    setOutput('runtime-config-status', {
      models_local_override: runtime.models_local_override,
      models_local_path: runtime.models_local_path,
      rag_local_override: runtime.rag_local_override,
      rag_local_path: runtime.rag_local_path,
    });
    renderRuntimeSummary(runtime);
    const selectedName = document.getElementById('overview-preset-select').value
      || document.getElementById('preset-select').value
      || document.getElementById('preset-name').value.trim();
    renderOverviewPresetRuntimeHint(selectedName);
    renderSelectedPresetPreviewByName(selectedName);
    if (currentPresets.length > 0) {
      renderPresetCatalog(currentPresets);
    }
  } catch (error) {
    latestRuntimeStatus = null;
    document.getElementById('runtime-watch-status').textContent = 'unknown';
    document.getElementById('runtime-watch-pid').textContent = 'PID: -';
    document.getElementById('runtime-fast-status').textContent = 'unknown';
    document.getElementById('runtime-fast-pid').textContent = 'PID: -';
    document.getElementById('runtime-work-status').textContent = 'unknown';
    document.getElementById('runtime-work-pid').textContent = 'PID: -';
    document.getElementById('runtime-code-status').textContent = 'unknown';
    document.getElementById('runtime-code-pid').textContent = 'PID: -';
    document.getElementById('runtime-embedding-status').textContent = 'unknown';
    document.getElementById('runtime-embedding-pid').textContent = 'PID: -';
    document.getElementById('runtime-qdrant-status').textContent = 'unknown';
    document.getElementById('runtime-qdrant-detail').textContent = 'Local binary: -';
    setOutput('runtime-fast-log', String(error));
    setOutput('runtime-work-log', String(error));
    setOutput('runtime-code-log', String(error));
    setOutput('runtime-gateway-log', String(error));
    setOutput('runtime-embedding-log', String(error));
    setOutput('runtime-qdrant-log', String(error));
    setOutput('runtime-watch-log', String(error));
    setOutput('runtime-config-status', String(error));
    document.getElementById('runtime-config-summary').textContent = String(error);
    const selectedName = document.getElementById('overview-preset-select').value
      || document.getElementById('preset-select').value
      || document.getElementById('preset-name').value.trim();
    renderOverviewPresetRuntimeHint(selectedName);
    renderSelectedPresetPreviewByName(selectedName);
    if (currentPresets.length > 0) {
      renderPresetCatalog(currentPresets);
    }
  }
}

async function refreshLocalConfigEditors() {
  try {
    const files = await GetLocalConfigFiles();
    const byName = Object.fromEntries(files.map((file) => [file.name, file]));
    document.getElementById('models-local-editor').value = byName['models.local.yaml']?.content || '';
    document.getElementById('rag-local-editor').value = byName['rag.local.yaml']?.content || '';
  } catch (error) {
    setOutput('runtime-config-status', String(error));
  }
}

async function bootstrapGatewayURL() {
  const current = await GetGatewayURL();
  document.getElementById('gateway-url').value = current;
}

document.getElementById('save-url').addEventListener('click', async () => {
  const nextURL = document.getElementById('gateway-url').value;
  await SetGatewayURL(nextURL);
  await refreshOverview();
});

document.getElementById('refresh-overview').addEventListener('click', refreshOverview);
document.getElementById('refresh-runtime').addEventListener('click', refreshRuntime);

document.getElementById('start-gateway').addEventListener('click', async () => {
  try {
    await runGoRuntimeServiceWorkflow({
      action: 'start_gateway',
      title: 'Runtime Service: Start Gateway',
      successMessage: () => 'Started gateway.',
      failureMessage: () => 'Starting gateway had issues.',
    });
  } catch (error) {
    setOutput('runtime-gateway-log', String(error));
  }
});

document.getElementById('start-fast').addEventListener('click', async () => {
  try {
    await runGoRuntimeServiceWorkflow({
      action: 'start_fast',
      title: 'Runtime Service: Start Fast',
      successMessage: () => 'Started fast.',
      failureMessage: () => 'Starting fast had issues.',
    });
  } catch (error) {
    setOutput('runtime-fast-log', String(error));
  }
});

document.getElementById('stop-fast').addEventListener('click', async () => {
  try {
    await runGoRuntimeServiceWorkflow({
      action: 'stop_fast',
      title: 'Runtime Service: Stop Fast',
      successMessage: () => 'Stopped fast.',
      failureMessage: () => 'Stopping fast had issues.',
    });
  } catch (error) {
    setOutput('runtime-fast-log', String(error));
  }
});

document.getElementById('start-work').addEventListener('click', async () => {
  try {
    await runGoRuntimeServiceWorkflow({
      action: 'start_work',
      title: 'Runtime Service: Start Work',
      successMessage: () => 'Started work.',
      failureMessage: () => 'Starting work had issues.',
    });
  } catch (error) {
    setOutput('runtime-work-log', String(error));
  }
});

document.getElementById('stop-work').addEventListener('click', async () => {
  try {
    await runGoRuntimeServiceWorkflow({
      action: 'stop_work',
      title: 'Runtime Service: Stop Work',
      successMessage: () => 'Stopped work.',
      failureMessage: () => 'Stopping work had issues.',
    });
  } catch (error) {
    setOutput('runtime-work-log', String(error));
  }
});

document.getElementById('start-code').addEventListener('click', async () => {
  try {
    await runGoRuntimeServiceWorkflow({
      action: 'start_code',
      title: 'Runtime Service: Start Code',
      successMessage: () => 'Started code.',
      failureMessage: () => 'Starting code had issues.',
    });
  } catch (error) {
    setOutput('runtime-code-log', String(error));
  }
});

document.getElementById('stop-code').addEventListener('click', async () => {
  try {
    await runGoRuntimeServiceWorkflow({
      action: 'stop_code',
      title: 'Runtime Service: Stop Code',
      successMessage: () => 'Stopped code.',
      failureMessage: () => 'Stopping code had issues.',
    });
  } catch (error) {
    setOutput('runtime-code-log', String(error));
  }
});

document.getElementById('stop-gateway').addEventListener('click', async () => {
  try {
    await runGoRuntimeServiceWorkflow({
      action: 'stop_gateway',
      title: 'Runtime Service: Stop Gateway',
      successMessage: () => 'Stopped gateway.',
      failureMessage: () => 'Stopping gateway had issues.',
    });
  } catch (error) {
    setOutput('runtime-gateway-log', String(error));
  }
});

document.getElementById('start-qdrant').addEventListener('click', async () => {
  try {
    await runGoRuntimeServiceWorkflow({
      action: 'start_qdrant',
      title: 'Runtime Service: Start Qdrant',
      successMessage: () => 'Started qdrant.',
      failureMessage: () => 'Starting qdrant had issues.',
    });
  } catch (error) {
    setOutput('runtime-qdrant-log', String(error));
  }
});

document.getElementById('start-embedding').addEventListener('click', async () => {
  try {
    await runGoRuntimeServiceWorkflow({
      action: 'start_embedding',
      title: 'Runtime Service: Start Embedding',
      successMessage: () => 'Started embedding.',
      failureMessage: () => 'Starting embedding had issues.',
    });
  } catch (error) {
    setOutput('runtime-embedding-log', String(error));
  }
});

document.getElementById('stop-embedding').addEventListener('click', async () => {
  try {
    await runGoRuntimeServiceWorkflow({
      action: 'stop_embedding',
      title: 'Runtime Service: Stop Embedding',
      successMessage: () => 'Stopped embedding.',
      failureMessage: () => 'Stopping embedding had issues.',
    });
  } catch (error) {
    setOutput('runtime-embedding-log', String(error));
  }
});

document.getElementById('stop-qdrant').addEventListener('click', async () => {
  try {
    await runGoRuntimeServiceWorkflow({
      action: 'stop_qdrant',
      title: 'Runtime Service: Stop Qdrant',
      successMessage: () => 'Stopped qdrant.',
      failureMessage: () => 'Stopping qdrant had issues.',
    });
  } catch (error) {
    setOutput('runtime-qdrant-log', String(error));
  }
});

document.getElementById('start-watch').addEventListener('click', async () => {
  try {
    const rawPaths = document.getElementById('watch-paths').value;
    const project = document.getElementById('watch-project').value;
    const tags = parseTagList(document.getElementById('watch-tags').value);
    const interval = Number(document.getElementById('watch-interval').value || 2);
    const paths = splitMultilinePaths(rawPaths);
    if (paths.length === 0) {
      setOutput('runtime-watch-log', 'At least one watch path is required.');
      return;
    }
    await runGoRuntimeServiceWorkflow({
      action: 'start_watch',
      watchRequest: {paths, project, tags, interval, recursive: true},
      title: 'Runtime Service: Start Watch',
      successMessage: () => `Started watch for ${paths.length} path(s).`,
      failureMessage: () => 'Starting watch had issues.',
    });
  } catch (error) {
    setOutput('runtime-watch-log', String(error));
  }
});

document.getElementById('stop-watch').addEventListener('click', async () => {
  try {
    await runGoRuntimeServiceWorkflow({
      action: 'stop_watch',
      title: 'Runtime Service: Stop Watch',
      successMessage: () => 'Stopped watch.',
      failureMessage: () => 'Stopping watch had issues.',
    });
  } catch (error) {
    setOutput('runtime-watch-log', String(error));
  }
});

document.getElementById('run-smoke').addEventListener('click', async () => {
  try {
    await runGoRuntimeWorkflow({
      runner: RunRuntimeSmoke,
      request: {
        gateway_url: document.getElementById('gateway-url').value,
        skip_qdrant: document.getElementById('smoke-skip-qdrant').checked,
        skip_embedding: document.getElementById('smoke-skip-embedding').checked,
        skip_reranker: document.getElementById('smoke-skip-reranker').checked,
      },
      title: 'Runtime Smoke',
      targetId: 'runtime-smoke-output',
      successMessage: () => 'Runtime smoke passed.',
      failureMessage: () => 'Runtime smoke needs attention.',
      fileStemPrefix: 'runtime-smoke',
    });
  } catch (error) {
    renderRuntimeMessage('runtime-smoke-output', String(error));
  }
});

document.getElementById('start-recommended-stack').addEventListener('click', async () => {
  try {
    await runGoRuntimeWorkflow({
      runner: RunRuntimeStackAction,
      request: {action: 'start_recommended_stack'},
      title: 'Runtime Stack: Start Recommended',
      targetId: 'runtime-stack-output',
      successMessage: () => 'Started recommended stack.',
      failureMessage: () => 'Recommended stack had issues.',
      fileStemPrefix: 'runtime-stack',
    });
  } catch (error) {
    renderRuntimeMessage('runtime-stack-output', String(error));
  }
});

document.getElementById('stop-recommended-stack').addEventListener('click', async () => {
  try {
    await runGoRuntimeWorkflow({
      runner: RunRuntimeStackAction,
      request: {action: 'stop_recommended_stack'},
      title: 'Runtime Stack: Stop Recommended',
      targetId: 'runtime-stack-output',
      successMessage: () => 'Stopped recommended stack.',
      failureMessage: () => 'Stopping recommended stack had issues.',
      fileStemPrefix: 'runtime-stack',
    });
  } catch (error) {
    renderRuntimeMessage('runtime-stack-output', String(error));
  }
});

document.getElementById('start-core-stack').addEventListener('click', async () => {
  try {
    await runGoRuntimeWorkflow({
      runner: RunRuntimeStackAction,
      request: {action: 'start_core_stack'},
      title: 'Runtime Stack: Start Core',
      targetId: 'runtime-stack-output',
      successMessage: () => 'Started core stack.',
      failureMessage: () => 'Core stack had issues.',
      fileStemPrefix: 'runtime-stack',
    });
  } catch (error) {
    renderRuntimeMessage('runtime-stack-output', String(error));
  }
});

document.getElementById('stop-core-stack').addEventListener('click', async () => {
  try {
    await runGoRuntimeWorkflow({
      runner: RunRuntimeStackAction,
      request: {action: 'stop_core_stack'},
      title: 'Runtime Stack: Stop Core',
      targetId: 'runtime-stack-output',
      successMessage: () => 'Stopped core stack.',
      failureMessage: () => 'Stopping core stack had issues.',
      fileStemPrefix: 'runtime-stack',
    });
  } catch (error) {
    renderRuntimeMessage('runtime-stack-output', String(error));
  }
});

document.getElementById('reload-local-config').addEventListener('click', refreshLocalConfigEditors);

document.getElementById('load-models-example').addEventListener('click', async () => {
  try {
    const file = await LoadLocalConfigExample({name: 'models.local.yaml'});
    document.getElementById('models-local-editor').value = file.content || '';
  } catch (error) {
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('load-rag-example').addEventListener('click', async () => {
  try {
    const file = await LoadLocalConfigExample({name: 'rag.local.yaml'});
    document.getElementById('rag-local-editor').value = file.content || '';
  } catch (error) {
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('save-models-local').addEventListener('click', async () => {
  try {
    await runGoRuntimeConfigWorkflow({
      action: 'save_local_config',
      name: 'models.local.yaml',
      content: document.getElementById('models-local-editor').value,
      title: 'Runtime Config: Save models.local',
      successMessage: () => 'Saved models.local.yaml.',
      failureMessage: () => 'Saving models.local.yaml had issues.',
    });
  } catch (error) {
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('delete-models-local').addEventListener('click', async () => {
  try {
    await runGoRuntimeConfigWorkflow({
      action: 'delete_local_config',
      name: 'models.local.yaml',
      title: 'Runtime Config: Delete models.local',
      successMessage: () => 'Deleted models.local.yaml.',
      failureMessage: () => 'Deleting models.local.yaml had issues.',
    });
  } catch (error) {
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('save-rag-local').addEventListener('click', async () => {
  try {
    await runGoRuntimeConfigWorkflow({
      action: 'save_local_config',
      name: 'rag.local.yaml',
      content: document.getElementById('rag-local-editor').value,
      title: 'Runtime Config: Save rag.local',
      successMessage: () => 'Saved rag.local.yaml.',
      failureMessage: () => 'Saving rag.local.yaml had issues.',
    });
  } catch (error) {
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('delete-rag-local').addEventListener('click', async () => {
  try {
    await runGoRuntimeConfigWorkflow({
      action: 'delete_local_config',
      name: 'rag.local.yaml',
      title: 'Runtime Config: Delete rag.local',
      successMessage: () => 'Deleted rag.local.yaml.',
      failureMessage: () => 'Deleting rag.local.yaml had issues.',
    });
  } catch (error) {
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('reload-gateway-config').addEventListener('click', async () => {
  try {
    await runGoRuntimeConfigWorkflow({
      action: 'reload_gateway_config',
      title: 'Runtime Config: Reload Gateway',
      successMessage: () => 'Reloaded gateway config.',
      failureMessage: () => 'Reloading gateway config had issues.',
    });
  } catch (error) {
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('apply-local-only-preset').addEventListener('click', () => {
  document.getElementById('models-local-editor').value = '';
  document.getElementById('rag-local-editor').value = RAG_LOCAL_ONLY_PRESET;
  setOutput('runtime-config-status', 'Loaded local-only preset into editors. Save rag.local.yaml, then reload gateway config.');
});

document.getElementById('apply-external-rag-preset').addEventListener('click', () => {
  document.getElementById('models-local-editor').value = MODELS_LOCAL_EXTERNAL_PRESET;
  document.getElementById('rag-local-editor').value = RAG_EXTERNAL_PRESET;
  setOutput('runtime-config-status', 'Loaded external embedding + qdrant preset into editors. Save both local files, then reload gateway config.');
});

document.getElementById('apply-local-only-now').addEventListener('click', async () => {
  setOutput('runtime-config-status', 'Applying local-only preset...');
  try {
    document.getElementById('models-local-editor').value = '';
    document.getElementById('rag-local-editor').value = RAG_LOCAL_ONLY_PRESET;
    await runGoRuntimeConfigWorkflow({
      action: 'apply_local_only',
      title: 'Runtime Config: Apply Local Only',
      successMessage: () => 'Applied local-only preset.',
      failureMessage: () => 'Applying local-only preset had issues.',
    });
  } catch (error) {
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('apply-local-only-stack-now').addEventListener('click', async () => {
  try {
    await runApplyLocalOnlyStackWorkflow();
  } catch (error) {
    renderRuntimeMessage('runtime-stack-output', String(error));
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('apply-external-rag-now').addEventListener('click', async () => {
  setOutput('runtime-config-status', 'Applying external embedding + qdrant preset...');
  try {
    document.getElementById('models-local-editor').value = MODELS_LOCAL_EXTERNAL_PRESET;
    document.getElementById('rag-local-editor').value = RAG_EXTERNAL_PRESET;
    await runGoRuntimeConfigWorkflow({
      action: 'apply_external_rag',
      title: 'Runtime Config: Apply External RAG',
      successMessage: () => 'Applied external embedding + qdrant preset.',
      failureMessage: () => 'Applying external embedding + qdrant preset had issues.',
    });
  } catch (error) {
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('apply-external-rag-stack-now').addEventListener('click', async () => {
  try {
    await runApplyExternalRagStackWorkflow();
  } catch (error) {
    renderRuntimeMessage('runtime-stack-output', String(error));
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('send-chat').addEventListener('click', async () => {
  await runChatFromForm();
});

document.getElementById('chat-output').addEventListener('click', async (event) => {
  const karteButton = event.target.closest('[data-karte-action]');
  if (karteButton) {
    await handleKarteConversationAction(karteButton);
    return;
  }
  const button = event.target.closest('[data-chat-action="continue"]');
  if (!button) {
    return;
  }
  const requestId = button.dataset.requestId || '';
  if (!requestId) {
    return;
  }
  await continueChatGeneration(requestId);
});

document.getElementById('chat-sidebar-toggle').addEventListener('click', () => {
  setSidebarCollapsed(!sidebarCollapsed);
});

document.getElementById('sidebar-toggle').addEventListener('click', () => {
  setSidebarCollapsed(!sidebarCollapsed);
});

document.getElementById('sidebar-reveal').addEventListener('click', () => {
  setSidebarCollapsed(false);
});

document.getElementById('chat-prompt').addEventListener('keydown', async (event) => {
  if (!isChatSubmitShortcut(event)) {
    return;
  }
  event.preventDefault();
  await runChatFromForm();
});

document.getElementById('chat-open-library').addEventListener('click', () => {
  closeChatToolbarMenus();
  activateTab('rag');
  updateChatScopeSummary();
});

document.getElementById('chat-history-select').addEventListener('change', async (event) => {
  const name = event.target.value;
  if (!name) {
    return;
  }
  try {
    await loadSavedRequestByName('chat', name);
    document.getElementById('chat-save-name').value = name;
    document.getElementById('chat-more-menu')?.removeAttribute('open');
  } catch (error) {
    renderRuntimeMessage('chat-output', String(error));
  }
});

document.getElementById('chat-project-select').addEventListener('change', () => {
  const scopeSelect = document.getElementById('chat-source-scope-select');
  if (scopeSelect && scopeSelect.value === 'all') {
    scopeSelect.value = 'project';
  }
  applyChatContextScope();
});

document.getElementById('chat-source-scope-select').addEventListener('change', () => {
  applyChatContextScope();
});

document.getElementById('chat-top-k-select').addEventListener('change', () => {
  applyChatContextScope();
});

document.getElementById('chat-web-search').addEventListener('change', (event) => {
  setChatWebStatus(event.target.checked ? 'Web on' : 'Local only', event.target.checked ? 'active' : '');
});

document.getElementById('chat-route-toggle').addEventListener('click', () => {
  closeChatToolbarMenus();
  const inspector = document.querySelector('.route-inspector-popover');
  if (!inspector) {
    return;
  }
  inspector.open = !inspector.open;
});

document.getElementById('export-chat').addEventListener('click', async () => {
  closeChatToolbarMenus();
  await exportLatestResult('chat-output', latestChatExport);
});

document.getElementById('chat-new-session').addEventListener('click', () => {
  closeChatToolbarMenus();
  startNewChat();
});

document.getElementById('rename-chat-request').addEventListener('click', async () => {
  try {
    await renameCurrentChatFromInputs();
    closeChatToolbarMenus();
  } catch (error) {
    renderRuntimeMessage('chat-output', String(error));
  }
});

['save-chat-request', 'load-chat-request', 'delete-chat-request'].forEach((id) => {
  document.getElementById(id).addEventListener('click', () => {
    window.setTimeout(() => {
      closeChatToolbarMenus();
    }, 0);
  });
});

document.getElementById('export-route').addEventListener('click', async () => {
  await exportLatestResult('route-output', latestRouteExport);
});

document.getElementById('export-embedding').addEventListener('click', async () => {
  await exportLatestResult('embedding-output', latestEmbeddingExport);
});

document.getElementById('export-ingest').addEventListener('click', async () => {
  await exportLatestResult('ingest-output', latestIngestExport);
});

document.getElementById('export-index-summary').addEventListener('click', async () => {
  await exportLatestResult('index-output', latestIndexSummaryExport);
});

document.getElementById('run-route-plan').addEventListener('click', async () => {
  await runRoutePlanFromForm();
});

document.getElementById('run-ingest').addEventListener('click', async () => {
  await runIngestFromForm();
});

document.getElementById('run-search').addEventListener('click', async () => {
  try {
    await runRagRequest({answer: false});
  } catch (error) {
    const project = document.getElementById('rag-project').value;
    const query = document.getElementById('rag-query').value;
    const sourcePath = document.getElementById('rag-source-path').value.trim();
    const topK = getPositiveInt('rag-top-k', 5);
    renderRuntimeMessage('rag-output', String(error));
    await recordExecution({
      kind: 'rag',
      title: `RAG Search (${project || 'default'})`,
      status: 'error',
      summary: query.slice(0, 120),
      detail: String(error),
      payload: JSON.stringify({query, project, source_path: sourcePath, top_k: topK, answer: false}),
    });
  }
});

document.getElementById('run-query').addEventListener('click', async () => {
  try {
    await runRagRequest({answer: document.getElementById('rag-answer').checked});
  } catch (error) {
    const project = document.getElementById('rag-project').value;
    const query = document.getElementById('rag-query').value;
    const sourcePath = document.getElementById('rag-source-path').value.trim();
    const topK = getPositiveInt('rag-top-k', 5);
    const answer = document.getElementById('rag-answer').checked;
    renderRuntimeMessage('rag-output', String(error));
    await recordExecution({
      kind: 'rag',
      title: `RAG Query (${project || 'default'})`,
      status: 'error',
      summary: query.slice(0, 120),
      detail: String(error),
      payload: JSON.stringify({query, project, source_path: sourcePath, top_k: topK, answer}),
    });
  }
});

document.getElementById('clear-rag-source-path').addEventListener('click', () => {
  document.getElementById('rag-source-path').value = '';
  renderRuntimeMessage('rag-output', 'Cleared source filter.');
  syncChatContextBarFromRagState();
  updateChatScopeSummary();
});

document.getElementById('chat-source-list').addEventListener('click', (event) => {
  const card = event.target.closest('.source-card');
  if (!card) {
    return;
  }
  const index = Number(card.dataset.sourceIndex || 0);
  if (!Number.isFinite(index)) {
    return;
  }
  renderChatSourcePreview(index);
  renderChatSourcesPane({sources: latestChatSources, title: latestChatSourceTitle});
});

document.getElementById('chat-source-preview').addEventListener('click', async (event) => {
  const button = event.target.closest('.open-web-source');
  if (!button) {
    return;
  }
  try {
    await OpenWebSource(button.dataset.webSourceUrl || '');
  } catch (error) {
    setChatDropStatus(String(error));
  }
});

['rag-project', 'rag-tags', 'rag-source-path', 'rag-top-k'].forEach((id) => {
  document.getElementById(id).addEventListener('input', () => {
    syncChatContextBarFromRagState();
    updateChatScopeSummary();
  });
});

document.getElementById('settings-open-runtime').addEventListener('click', () => activateTab('runtime'));
document.getElementById('settings-open-routing').addEventListener('click', () => activateTab('router'));
document.getElementById('settings-open-eval').addEventListener('click', () => activateTab('eval'));
document.getElementById('settings-open-dashboard').addEventListener('click', () => activateTab('overview'));

document.getElementById('run-embedding').addEventListener('click', async () => {
  await runEmbeddingProbe();
});

document.getElementById('browse-index').addEventListener('click', async () => {
  await runIndexBrowser();
});

document.getElementById('index-output').addEventListener('click', async (event) => {
  const openButton = event.target.closest('.index-open-source-btn');
  const exportButton = event.target.closest('.index-export-source-btn');
  const exportCurrentButton = event.target.closest('.index-export-current-source-btn');
  const useRagButton = event.target.closest('.index-use-rag-btn, .index-use-current-rag-btn');
  const useRagSourceButton = event.target.closest('.index-use-rag-source-btn, .index-use-current-rag-source-btn');
  const useEvalButton = event.target.closest('.index-use-eval-btn, .index-use-current-eval-btn');
  const useEvalSourceButton = event.target.closest('.index-use-eval-source-btn, .index-use-current-eval-source-btn');
  const runSearchButton = event.target.closest('.index-run-current-search-btn');
  const runQueryButton = event.target.closest('.index-run-current-query-btn');
  const runEvalButton = event.target.closest('.index-run-current-eval-btn');
  const useIngestButton = event.target.closest('.index-use-ingest-btn, .index-use-current-ingest-btn');

  if (openButton) {
    try {
      const response = await openIndexSource({
        sourcePath: openButton.dataset.sourcePath || '',
        project: openButton.dataset.project || '',
      });
      await recordExecution({
        kind: 'index',
        title: 'Index Source Detail',
        status: 'ok',
        summary: response.source_path || '',
        detail: `${response.total_chunks || 0} chunks`,
        payload: JSON.stringify({source_path: response.source_path, project: response.project_filter, limit: response.total_chunks}),
      });
    } catch (error) {
      renderRuntimeMessage('index-output', String(error));
      await recordExecution({
        kind: 'index',
        title: 'Index Source Detail',
        status: 'error',
        summary: openButton.dataset.sourcePath || '',
        detail: String(error),
        payload: JSON.stringify({source_path: openButton.dataset.sourcePath || '', project: openButton.dataset.project || ''}),
      });
    }
    return;
  }

  if (exportButton) {
    try {
      const response = await openIndexSource({
        sourcePath: exportButton.dataset.sourcePath || '',
        project: exportButton.dataset.project || '',
      });
      await exportLatestResult('index-output', buildIndexSourceExportPayload(response));
    } catch (error) {
      renderRuntimeMessage('index-output', String(error));
    }
    return;
  }

  if (exportCurrentButton) {
    await exportLatestResult('index-output', latestIndexExport);
    return;
  }

  if (useRagButton) {
    applyIndexProjectToRag(useRagButton.dataset.project || '');
    return;
  }

  if (useRagSourceButton) {
    applyIndexSourceToRag(useRagSourceButton.dataset.sourcePath || '', useRagSourceButton.dataset.project || '');
    return;
  }

  if (useEvalButton) {
    applyIndexProjectToEval(useEvalButton.dataset.project || '');
    return;
  }

  if (useEvalSourceButton) {
    applyIndexSourceToEval(useEvalSourceButton.dataset.sourcePath || '', useEvalSourceButton.dataset.project || '');
    return;
  }

  if (runSearchButton) {
    try {
      await runIndexSourceSearch(runSearchButton.dataset.sourcePath || '', runSearchButton.dataset.project || '');
    } catch (error) {
      renderRuntimeMessage('rag-output', String(error));
    }
    return;
  }

  if (runQueryButton) {
    try {
      await runIndexSourceQuery(runQueryButton.dataset.sourcePath || '', runQueryButton.dataset.project || '');
    } catch (error) {
      renderRuntimeMessage('rag-output', String(error));
    }
    return;
  }

  if (runEvalButton) {
    try {
      await runIndexSourceEval(runEvalButton.dataset.sourcePath || '', runEvalButton.dataset.project || '');
    } catch (error) {
      renderRuntimeMessage('eval-output', String(error));
    }
    return;
  }

  if (useIngestButton) {
    applyIndexSourceToIngest(useIngestButton.dataset.sourcePath || '', useIngestButton.dataset.project || '');
  }
});

document.getElementById('export-rag').addEventListener('click', async () => {
  await exportLatestResult('rag-output', latestRagExport);
});

document.getElementById('run-eval').addEventListener('click', async () => {
  await runEvalFromForm();
});

document.getElementById('clear-eval-source-path').addEventListener('click', () => {
  document.getElementById('eval-source-path').value = '';
  renderRuntimeMessage('eval-output', 'Cleared source filter.');
});

document.getElementById('export-eval').addEventListener('click', async () => {
  await exportLatestResult('eval-output', latestEvalExport);
});

document.getElementById('export-workflow').addEventListener('click', async () => {
  await exportLatestResult('runtime-stack-output', latestWorkflowExport);
});

document.getElementById('preference-start').addEventListener('click', startPreferenceSession);
document.getElementById('preference-resume').addEventListener('click', resumePreferenceSession);
document.getElementById('preference-comparison').addEventListener('change', (event) => {
  document.getElementById('preference-count').value =
    event.target.value === 'base_vs_adapter' ? '11' : '30';
});
document.getElementById('preference-submit').addEventListener('click', submitPreferenceVote);
document.getElementById('preference-correct').addEventListener('click', correctPreviousPreferenceVote);
document.getElementById('preference-export-dpo').addEventListener('click', () => exportPreference('dpo'));
document.getElementById('preference-export-sft').addEventListener('click', () => exportPreference('sft'));
document.querySelector('.preference-choice-row').addEventListener('click', (event) => {
  const button = event.target.closest('[data-preference-choice]');
  if (button) {
    choosePreference(button.dataset.preferenceChoice);
  }
});
document.getElementById('preference-review').addEventListener('click', (event) => {
  const button = event.target.closest('[data-preference-selection]');
  if (button) {
    choosePreference(button.dataset.preferenceSelection);
  }
});
document.addEventListener('keydown', (event) => {
  const evalPanel = document.querySelector('[data-tab-panel="eval"]');
  const target = event.target;
  if (
    !evalPanel?.classList.contains('active')
    || target?.matches?.('input, textarea, select, [contenteditable="true"]')
  ) {
    return;
  }
  if (event.key === 'Enter') {
    if (preferenceSelection && !preferenceSaving) {
      event.preventDefault();
      void submitPreferenceVote();
    }
    return;
  }
  if (String(event.key).toLowerCase() === 'z') {
    event.preventDefault();
    correctPreviousPreferenceVote();
    return;
  }
  const selection = preferenceSelectionForKey(event.key);
  if (selection) {
    event.preventDefault();
    choosePreference(selection);
  }
});

registerSavedRequestHandlers();
bindTabs();
bindValidationAction('overview-preset-validation');
bindValidationAction('runtime-preset-validation');

async function initializeWorkbench() {
  setSidebarCollapsed(false);
  activateTab('chat');
  bindChatStreamEvents();
  bindIngestDropTargets();
  renderChatThread();
  renderChatSourcesPane({sources: [], title: 'Sources'});
  renderRouteInspectorCard(null);
  setChatDropStatus('');
  syncChatContextBarFromRagState();
  updateChatScopeSummary();
  restorePresetCatalogControls();
  restoreEvalDatasetTrendControls();
  await restoreRegressionWatchState();
  await restoreBatchPresetSelection();
  bootstrapGatewayURL();
  refreshOverview();
  refreshRuntime();
  refreshLocalConfigEditors();
  refreshWebSearchCapability();
  refreshPresets();
  refreshSavedRequests();
  refreshExecutionHistory();
  refreshPreferenceSessions({quiet: true});
  restoreBatchWorkflowState();
  window.setInterval(refreshRuntime, 4000);
  window.setInterval(refreshKarteCapability, 4000);
  window.setInterval(restoreBatchWorkflowState, 2000);
}

void initializeWorkbench();

document.getElementById('selected-preset-preview').addEventListener('click', async (event) => {
  const openRuntimeButton = event.target.closest('.preset-open-runtime-btn');
  const applyRuntimeButton = event.target.closest('.preset-apply-runtime-profile-btn');
  const applyRuntimeStackButton = event.target.closest('.preset-apply-runtime-stack-btn');
  const validateButton = event.target.closest('.preset-validate-btn');
  const runWorkflowButton = event.target.closest('.preset-run-workflow-btn');
  const runSmokeButton = event.target.closest('.preset-run-smoke-btn');
  const runWatchButton = event.target.closest('.preset-run-watch-btn');
  const runIngestButton = event.target.closest('.preset-run-ingest-btn');
  const runEvalButton = event.target.closest('.preset-run-eval-btn');
  const runIngestEvalButton = event.target.closest('.preset-run-ingest-eval-btn');
  const toggleBatchButton = event.target.closest('.preset-toggle-batch-btn');
  const batchOnlyButton = event.target.closest('.preset-batch-only-btn');
  const runBatchValidateButton = event.target.closest('.preset-run-batch-validate-btn');
  const runBatchSmokeButton = event.target.closest('.preset-run-batch-smoke-btn');
  const runBatchVerificationButton = event.target.closest('.preset-run-batch-verification-btn');
  const runBatchWatchButton = event.target.closest('.preset-run-batch-watch-btn');
  const runBatchRuntimeStackPrepareButton = event.target.closest('.preset-run-batch-runtime-stack-prepare-btn');
  const runBatchIngestButton = event.target.closest('.preset-run-batch-ingest-btn');
  const runBatchEvalButton = event.target.closest('.preset-run-batch-eval-btn');
  const runBatchIngestEvalButton = event.target.closest('.preset-run-batch-ingest-eval-btn');
  const runBatchStackButton = event.target.closest('.preset-run-batch-stack-btn');
  const verificationButton = event.target.closest('.preset-run-verification-btn');
  const button = event.target.closest('.preset-load-request-btn');
  if (openRuntimeButton) {
    activateTab('runtime');
    setOutput('runtime-config-status', 'Opened runtime controls for the selected preset.');
    return;
  }
  if (applyRuntimeButton) {
    try {
      const preset = await resolvePresetByName(applyRuntimeButton.dataset.presetName || '');
      syncPresetSelections(preset.name);
      await runSelectedPresetWorkflowRecoveryAction({
        presetName: preset.name,
        actionKind: 'apply-runtime-profile',
        stepName: 'preview',
      });
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (applyRuntimeStackButton) {
    try {
      const preset = await resolvePresetByName(applyRuntimeStackButton.dataset.presetName || '');
      syncPresetSelections(preset.name);
      await runPresetRuntimePreparationAndStack({
        preset,
        workflowName: 'preview',
        successMessage: (item) => `Applied runtime profile and started stack for preset: ${item.name}`,
        failureMessage: (item) => `Applied runtime profile but stack had issues for preset: ${item.name}`,
      });
    } catch (error) {
      setOutput('runtime-config-status', String(error));
      renderRuntimeMessage('runtime-stack-output', String(error));
    }
    return;
  }
  if (validateButton) {
    try {
      const preset = await resolvePresetByName(validateButton.dataset.presetName || '');
      syncPresetSelections(preset.name);
      await validateAndRenderPreset(preset);
      setOutput('runtime-config-status', `Validated preset from preview: ${preset.name}`);
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runWorkflowButton) {
    try {
      const preset = await resolvePresetByName(runWorkflowButton.dataset.presetName || '');
      syncPresetSelections(preset.name);
      await runPresetStackIngestEvalWorkflow(preset);
    } catch (error) {
      setOutput('runtime-config-status', String(error));
      renderWorkflowResult('Preset Workflow', 'failed', [
        {name: 'recommended_stack', status: 'failed', detail: String(error)},
        {name: 'ingest', status: 'skipped', detail: 'Workflow aborted.'},
        {name: 'eval', status: 'skipped', detail: 'Workflow aborted.'},
      ]);
    }
    return;
  }
  if (runSmokeButton) {
    try {
      await runPresetShortcutWorkflowByName(runSmokeButton.dataset.presetName || '', 'smoke');
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runWatchButton) {
    try {
      await runPresetShortcutWorkflowByName(runWatchButton.dataset.presetName || '', 'watch');
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runIngestButton) {
    try {
      await runPresetShortcutWorkflowByName(runIngestButton.dataset.presetName || '', 'ingest');
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runEvalButton) {
    try {
      await runPresetShortcutWorkflowByName(runEvalButton.dataset.presetName || '', 'eval');
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runIngestEvalButton) {
    try {
      await runPresetShortcutWorkflowByName(runIngestEvalButton.dataset.presetName || '', 'ingest-eval');
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (toggleBatchButton) {
    const presetName = toggleBatchButton.dataset.presetName || '';
    const selected = toggleBatchPresetSelectionByName(presetName);
    setOutput('runtime-config-status', `${selected ? 'Added' : 'Removed'} preset ${presetName} ${selected ? 'to' : 'from'} batch selection.`);
    return;
  }
  if (batchOnlyButton) {
    const presetName = batchOnlyButton.dataset.presetName || '';
    applyBatchPresetSelection([presetName]);
    renderSelectedPresetPreviewByName(presetName);
    setOutput('runtime-config-status', `Set batch selection to preset: ${presetName}`);
    return;
  }
  if (runBatchValidateButton) {
    try {
      const presetName = runBatchValidateButton.dataset.presetName || '';
      await runSinglePresetBatchValidate(presetName);
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runBatchSmokeButton) {
    try {
      const presetName = runBatchSmokeButton.dataset.presetName || '';
      await runSinglePresetBatchSmoke(presetName);
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runBatchVerificationButton) {
    try {
      const presetName = runBatchVerificationButton.dataset.presetName || '';
      await runSinglePresetBatchVerification(presetName);
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runBatchWatchButton) {
    try {
      const presetName = runBatchWatchButton.dataset.presetName || '';
      await runSinglePresetBatchWatch(presetName);
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runBatchRuntimeStackPrepareButton) {
    try {
      const presetName = runBatchRuntimeStackPrepareButton.dataset.presetName || '';
      await runSinglePresetBatchRuntimeStackPrepare(presetName);
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runBatchIngestButton) {
    try {
      const presetName = runBatchIngestButton.dataset.presetName || '';
      await runSinglePresetBatchIngest(presetName);
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runBatchEvalButton) {
    try {
      const presetName = runBatchEvalButton.dataset.presetName || '';
      await runSinglePresetBatchEval(presetName);
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runBatchIngestEvalButton) {
    try {
      const presetName = runBatchIngestEvalButton.dataset.presetName || '';
      await runSinglePresetBatchIngestEval(presetName);
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runBatchStackButton) {
    try {
      const presetName = runBatchStackButton.dataset.presetName || '';
      await runSinglePresetBatchStackWorkflow(presetName);
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (verificationButton) {
    try {
      const preset = await resolvePresetByName(verificationButton.dataset.presetName || '');
      await runPresetVerificationWorkflow(preset);
    } catch (error) {
      setOutput('runtime-config-status', String(error));
      renderWorkflowResult('Preset Verification', 'failed', [
        {name: 'verification', status: 'failed', detail: String(error)},
      ]);
    }
    return;
  }
  if (!button) {
    return;
  }
  try {
    const item = await loadSavedRequestByName(button.dataset.requestKind || '', button.dataset.requestName || '');
    setOutput('runtime-config-status', `Loaded representative ${item.kind} request: ${item.name}`);
  } catch (error) {
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('refresh-history').addEventListener('click', refreshExecutionHistory);

document.getElementById('clear-history').addEventListener('click', async () => {
  try {
    await ClearExecutionHistory();
    await refreshExecutionHistory();
  } catch (error) {
    renderExecutionHistory([{title: 'History Clear Failed', status: 'error', summary: String(error), kind: 'history', timestamp: ''}]);
  }
});

document.getElementById('recent-activity').addEventListener('click', (event) => {
  const rerunButton = event.target.closest('.rerun-history-btn');
  const button = event.target.closest('.reuse-history-btn');
  const exportButton = event.target.closest('.export-history-btn');
  if (rerunButton) {
    const item = currentExecutionHistory.find((historyItem) => historyItem.id === rerunButton.dataset.historyId);
    if (!item) {
      return;
    }
    rerunHistoryItem(item).catch((error) => {
      const message = String(error);
      if (item.kind === 'workflow') {
        renderWorkflowResult('History Rerun', 'failed', [
          {name: 'rerun', status: 'failed', detail: message},
        ]);
        setOutput('runtime-config-status', message);
        return;
      }
      renderRuntimeMessage('export-preview', message);
    });
    return;
  }
  if (button) {
    const item = currentExecutionHistory.find((historyItem) => historyItem.id === button.dataset.historyId);
    if (!item) {
      return;
    }
    reuseHistoryItem(item).catch((error) => {
      console.error('failed to reuse history payload', error);
      renderRuntimeMessage('export-preview', String(error));
    });
    return;
  }
  if (exportButton) {
    const item = currentExecutionHistory.find((historyItem) => historyItem.id === exportButton.dataset.historyId);
    if (!item) {
      return;
    }
    exportLatestResult('export-preview', buildHistoryExportPayload(item))
      .then(refreshOverview)
      .catch((error) => {
        renderExportPreview({name: 'Export Error', path: '-', content: String(error)});
      });
  }
});

document.getElementById('workflow-summary').addEventListener('click', (event) => {
  const rerunButton = event.target.closest('.rerun-workflow-btn');
  const exportButton = event.target.closest('.export-history-btn');
  const exportRegressionButton = event.target.closest('.export-regression-watch-btn');
  const focusPresetButton = event.target.closest('.trend-focus-preset-btn');
  const openVerificationButton = event.target.closest('.trend-open-verification-btn');
  const useEvalDatasetButton = event.target.closest('.trend-use-eval-dataset-btn');
  const runEvalDatasetButton = event.target.closest('.trend-run-eval-dataset-btn');
  if (exportRegressionButton) {
    const workflowItems = currentExecutionHistory || [];
    const evalDatasetTrends = summarizeEvalDatasetTrends(workflowItems);
    const verificationTrends = Array.from((workflowItems || [])
      .filter((item) => item.kind === 'workflow' && item.payload)
      .map((item) => {
        try {
          const payload = JSON.parse(item.payload);
          return {item, payload};
        } catch (error) {
          return null;
        }
      })
      .filter((entry) => entry && entry.payload.workflow === 'preset_verification')
      .reduce((map, entry) => {
        const presetName = entry.payload?.preset?.name || entry.payload?.preset_name || '(unknown preset)';
        const steps = Array.isArray(entry.payload?.steps) ? entry.payload.steps : [];
        const summary = summarizeVerificationRun({workflow: 'preset_verification', steps});
        const ragStep = summary.representativeSteps.find((step) => step.name === 'rag_verification') || null;
        const evalStep = summary.representativeSteps.find((step) => step.name === 'eval_verification') || null;
        const current = map.get(presetName) || {presetName, runs: []};
        current.runs.push({
          status: entry.item.status || '-',
          timestamp: entry.item.timestamp || '-',
          sourceHitRate: parseMetricFromDetail(evalStep?.detail, 'source_hit_rate'),
          keywordHitRate: parseMetricFromDetail(evalStep?.detail, 'keyword_hit_rate'),
          sourceCount: parseMetricFromDetail(ragStep?.detail, 'source_count'),
        });
        map.set(presetName, current);
        return map;
      }, new Map()).values())
      .map((entry) => ({
        presetName: entry.presetName,
        runs: entry.runs.slice(0, 3),
        latest: entry.runs[0] || null,
        previous: entry.runs[1] || null,
      }));
    const payload = buildRegressionWatchExportPayload({
      presetAlerts: regressionWatchIncludePreset ? getPresetRegressionAlerts(verificationTrends) : [],
      datasetAlerts: regressionWatchIncludeDataset ? getDatasetRegressionAlerts(evalDatasetTrends) : [],
    });
    exportLatestResult('workflow-summary', payload)
      .then(refreshOverview)
      .catch((error) => {
        renderRuntimeMessage('workflow-summary', String(error));
      });
    return;
  }
  if (focusPresetButton) {
    const presetName = String(focusPresetButton.dataset.presetName || '').trim();
    if (!presetName) {
      return;
    }
    syncPresetSelections(presetName);
    renderSelectedPresetPreviewByName(presetName);
    renderSelectedPresetWorkflowByName(presetName);
    void refreshSelectedPresetValidationByName(presetName);
    setOutput('runtime-config-status', `Focused preset from verification trends: ${presetName}`);
    return;
  }
  if (openVerificationButton) {
    const presetName = String(openVerificationButton.dataset.presetName || '').trim();
    if (!presetName) {
      return;
    }
    const verificationItem = currentExecutionHistory.find((item) => {
      if (item.kind !== 'workflow' || !item.payload) {
        return false;
      }
      try {
        const payload = JSON.parse(item.payload);
        return (payload.workflow === 'preset_verification')
          && ((payload.preset?.name || payload.preset_name || '') === presetName);
      } catch (error) {
        return false;
      }
    });
    if (!verificationItem) {
      setOutput('runtime-config-status', `No verification workflow found for preset: ${presetName}`);
      return;
    }
    syncPresetSelections(presetName);
    renderSelectedPresetPreviewByName(presetName);
    renderSelectedPresetWorkflowByName(presetName);
    void refreshSelectedPresetValidationByName(presetName);
    exportLatestResult('export-preview', buildHistoryExportPayload(verificationItem))
      .then(refreshOverview)
      .catch((error) => {
        renderExportPreview({name: 'Export Error', path: '-', content: String(error)});
      });
    setOutput('runtime-config-status', `Opened latest verification for preset: ${presetName}`);
    return;
  }
  if (useEvalDatasetButton) {
    applyEvalDatasetToForm({
      datasetPath: useEvalDatasetButton.dataset.datasetPath || 'configs/eval.sample.yaml',
      project: useEvalDatasetButton.dataset.project || '',
    });
    return;
  }
  if (runEvalDatasetButton) {
    applyEvalDatasetToForm({
      datasetPath: runEvalDatasetButton.dataset.datasetPath || 'configs/eval.sample.yaml',
      project: runEvalDatasetButton.dataset.project || '',
    });
    runEvalFromForm().catch((error) => {
      renderRuntimeMessage('eval-output', String(error));
    });
    return;
  }
  if (rerunButton) {
    rerunWorkflowHistoryById(rerunButton.dataset.historyId, {
      title: 'Workflow Rerun',
      stepName: 'rerun',
    });
    return;
  }
  if (exportButton) {
    const item = currentExecutionHistory.find((historyItem) => historyItem.id === exportButton.dataset.historyId);
    if (!item) {
      return;
    }
    exportLatestResult('export-preview', buildHistoryExportPayload(item))
      .then(refreshOverview)
      .catch((error) => {
        renderExportPreview({name: 'Export Error', path: '-', content: String(error)});
      });
  }
});

document.getElementById('selected-preset-workflow').addEventListener('click', (event) => {
  const openRuntimeButton = event.target.closest('.selected-preset-open-runtime-btn');
  const applyRuntimeButton = event.target.closest('.selected-preset-apply-runtime-btn');
  const applyRuntimeStackButton = event.target.closest('.selected-preset-apply-runtime-stack-btn');
  const validateButton = event.target.closest('.selected-preset-validate-btn');
  const runWorkflowButton = event.target.closest('.selected-preset-run-workflow-btn');
  const runSmokeButton = event.target.closest('.selected-preset-run-smoke-btn');
  const runWatchButton = event.target.closest('.selected-preset-run-watch-btn');
  const runIngestButton = event.target.closest('.selected-preset-run-ingest-btn');
  const runEvalButton = event.target.closest('.selected-preset-run-eval-btn');
  const runIngestEvalButton = event.target.closest('.selected-preset-run-ingest-eval-btn');
  const toggleBatchButton = event.target.closest('.selected-preset-toggle-batch-btn');
  const batchOnlyButton = event.target.closest('.selected-preset-batch-only-btn');
  const runBatchValidateButton = event.target.closest('.selected-preset-run-batch-validate-btn');
  const runBatchSmokeButton = event.target.closest('.selected-preset-run-batch-smoke-btn');
  const runBatchVerificationButton = event.target.closest('.selected-preset-run-batch-verification-btn');
  const runBatchWatchButton = event.target.closest('.selected-preset-run-batch-watch-btn');
  const runBatchRuntimeStackPrepareButton = event.target.closest('.selected-preset-run-batch-runtime-stack-prepare-btn');
  const runBatchIngestButton = event.target.closest('.selected-preset-run-batch-ingest-btn');
  const runBatchEvalButton = event.target.closest('.selected-preset-run-batch-eval-btn');
  const runBatchIngestEvalButton = event.target.closest('.selected-preset-run-batch-ingest-eval-btn');
  const runBatchStackButton = event.target.closest('.selected-preset-run-batch-stack-btn');
  const rerunButton = event.target.closest('.selected-preset-rerun-btn');
  const rerunVerificationButton = event.target.closest('.selected-preset-rerun-verification-btn');
  const loadRequestButton = event.target.closest('.selected-preset-load-request-btn');
  const exportButton = event.target.closest('.selected-preset-export-btn');
  const failureButton = event.target.closest('.selected-preset-show-failure-btn');
  const retryOriginalButton = event.target.closest('.selected-preset-retry-original-btn');
  const stepActionButton = event.target.closest('.selected-preset-step-action-btn');

  if (openRuntimeButton) {
    activateTab('runtime');
    setOutput('runtime-config-status', 'Opened runtime controls for the selected preset.');
    return;
  }

  if (applyRuntimeButton) {
    resolvePresetByName(applyRuntimeButton.dataset.presetName || '')
      .then(async (preset) => {
        syncPresetSelections(preset.name);
        await runSelectedPresetWorkflowRecoveryAction({
          presetName: preset.name,
          actionKind: 'apply-runtime-profile',
          stepName: 'selected workflow',
        });
      })
      .catch((error) => {
        setOutput('runtime-config-status', String(error));
      });
    return;
  }

  if (applyRuntimeStackButton) {
    resolvePresetByName(applyRuntimeStackButton.dataset.presetName || '')
      .then(async (preset) => {
        syncPresetSelections(preset.name);
        await runPresetRuntimePreparationAndStack({
          preset,
          workflowName: 'selected workflow',
          successMessage: (item) => `Applied runtime profile and started stack for preset: ${item.name}`,
          failureMessage: (item) => `Applied runtime profile but stack had issues for preset: ${item.name}`,
        });
      })
      .catch((error) => {
        setOutput('runtime-config-status', String(error));
        renderRuntimeMessage('runtime-stack-output', String(error));
      });
    return;
  }

  if (validateButton) {
    resolvePresetByName(validateButton.dataset.presetName || '')
      .then(async (preset) => {
        syncPresetSelections(preset.name);
        await validateAndRenderPreset(preset);
        setOutput('runtime-config-status', `Validated preset from workflow: ${preset.name}`);
      })
      .catch((error) => {
        setOutput('runtime-config-status', String(error));
      });
    return;
  }

  if (runWorkflowButton) {
    resolvePresetByName(runWorkflowButton.dataset.presetName || '')
      .then(async (preset) => {
        syncPresetSelections(preset.name);
        await runPresetStackIngestEvalWorkflow(preset);
      })
      .catch((error) => {
        renderWorkflowResult('Selected Preset Workflow', 'failed', [
          {name: 'workflow', status: 'failed', detail: String(error)},
        ]);
        setOutput('runtime-config-status', String(error));
      });
    return;
  }

  if (runSmokeButton) {
    runPresetShortcutWorkflowByName(runSmokeButton.dataset.presetName || '', 'smoke', 'Selected Preset Workflow')
      .catch((error) => {
        renderWorkflowResult('Selected Preset Workflow', 'failed', [
          {name: 'smoke', status: 'failed', detail: String(error)},
        ]);
        setOutput('runtime-config-status', String(error));
      });
    return;
  }

  if (runWatchButton) {
    runPresetShortcutWorkflowByName(runWatchButton.dataset.presetName || '', 'watch', 'Selected Preset Workflow')
      .catch((error) => {
        renderWorkflowResult('Selected Preset Workflow', 'failed', [
          {name: 'watch', status: 'failed', detail: String(error)},
        ]);
        setOutput('runtime-config-status', String(error));
      });
    return;
  }

  if (runIngestButton) {
    runPresetShortcutWorkflowByName(runIngestButton.dataset.presetName || '', 'ingest', 'Selected Preset Workflow')
      .catch((error) => {
        renderWorkflowResult('Selected Preset Workflow', 'failed', [
          {name: 'ingest', status: 'failed', detail: String(error)},
        ]);
        setOutput('runtime-config-status', String(error));
      });
    return;
  }

  if (runEvalButton) {
    runPresetShortcutWorkflowByName(runEvalButton.dataset.presetName || '', 'eval', 'Selected Preset Workflow')
      .catch((error) => {
        renderWorkflowResult('Selected Preset Workflow', 'failed', [
          {name: 'eval', status: 'failed', detail: String(error)},
        ]);
        setOutput('runtime-config-status', String(error));
      });
    return;
  }

  if (runIngestEvalButton) {
    runPresetShortcutWorkflowByName(runIngestEvalButton.dataset.presetName || '', 'ingest-eval', 'Selected Preset Workflow')
      .catch((error) => {
        renderWorkflowResult('Selected Preset Workflow', 'failed', [
          {name: 'ingest', status: 'failed', detail: String(error)},
          {name: 'eval', status: 'skipped', detail: 'Workflow aborted.'},
        ]);
        setOutput('runtime-config-status', String(error));
      });
    return;
  }

  if (toggleBatchButton) {
    const presetName = toggleBatchButton.dataset.presetName || '';
    const selected = toggleBatchPresetSelectionByName(presetName);
    setOutput('runtime-config-status', `${selected ? 'Added' : 'Removed'} preset ${presetName} ${selected ? 'to' : 'from'} batch selection.`);
    return;
  }

  if (batchOnlyButton) {
    const presetName = batchOnlyButton.dataset.presetName || '';
    applyBatchPresetSelection([presetName]);
    renderSelectedPresetWorkflowByName(presetName);
    setOutput('runtime-config-status', `Set batch selection to preset: ${presetName}`);
    return;
  }

  if (runBatchValidateButton) {
    runSinglePresetBatchValidate(runBatchValidateButton.dataset.presetName || '')
      .catch((error) => {
        renderWorkflowResult('Selected Preset Batch Validate', 'failed', [
          {name: 'batch_validate', status: 'failed', detail: String(error)},
        ]);
        setOutput('runtime-config-status', String(error));
      });
    return;
  }
  if (runBatchSmokeButton) {
    runSinglePresetBatchSmoke(runBatchSmokeButton.dataset.presetName || '')
      .catch((error) => {
        renderWorkflowResult('Selected Preset Batch Smoke', 'failed', [
          {name: 'batch_smoke', status: 'failed', detail: String(error)},
        ]);
        setOutput('runtime-config-status', String(error));
      });
    return;
  }

  if (runBatchVerificationButton) {
    runSinglePresetBatchVerification(runBatchVerificationButton.dataset.presetName || '')
      .catch((error) => {
        renderWorkflowResult('Selected Preset Batch Verification', 'failed', [
          {name: 'batch_verification', status: 'failed', detail: String(error)},
        ]);
        setOutput('runtime-config-status', String(error));
    });
    return;
  }
  if (runBatchWatchButton) {
    runSinglePresetBatchWatch(runBatchWatchButton.dataset.presetName || '')
      .catch((error) => {
        renderWorkflowResult('Selected Preset Batch Watch', 'failed', [
          {name: 'batch_watch', status: 'failed', detail: String(error)},
        ]);
        setOutput('runtime-config-status', String(error));
      });
    return;
  }
  if (runBatchRuntimeStackPrepareButton) {
    runSinglePresetBatchRuntimeStackPrepare(runBatchRuntimeStackPrepareButton.dataset.presetName || '')
      .catch((error) => {
        renderWorkflowResult('Selected Preset Batch Runtime + Stack', 'failed', [
          {name: 'batch_runtime_stack_prepare', status: 'failed', detail: String(error)},
        ]);
        setOutput('runtime-config-status', String(error));
      });
    return;
  }
  if (runBatchIngestButton) {
    runSinglePresetBatchIngest(runBatchIngestButton.dataset.presetName || '')
      .catch((error) => {
        renderWorkflowResult('Selected Preset Batch Ingest', 'failed', [
          {name: 'batch_ingest', status: 'failed', detail: String(error)},
        ]);
        setOutput('runtime-config-status', String(error));
      });
    return;
  }
  if (runBatchEvalButton) {
    runSinglePresetBatchEval(runBatchEvalButton.dataset.presetName || '')
      .catch((error) => {
        renderWorkflowResult('Selected Preset Batch Eval', 'failed', [
          {name: 'batch_eval', status: 'failed', detail: String(error)},
        ]);
        setOutput('runtime-config-status', String(error));
      });
    return;
  }

  if (runBatchIngestEvalButton) {
    runSinglePresetBatchIngestEval(runBatchIngestEvalButton.dataset.presetName || '')
      .catch((error) => {
        renderWorkflowResult('Selected Preset Batch Ingest + Eval', 'failed', [
          {name: 'batch_ingest_eval', status: 'failed', detail: String(error)},
        ]);
        setOutput('runtime-config-status', String(error));
      });
    return;
  }

  if (runBatchStackButton) {
    runSinglePresetBatchStackWorkflow(runBatchStackButton.dataset.presetName || '')
      .catch((error) => {
        renderWorkflowResult('Selected Preset Batch Stack Workflow', 'failed', [
          {name: 'batch_stack_ingest_eval', status: 'failed', detail: String(error)},
        ]);
        setOutput('runtime-config-status', String(error));
      });
    return;
  }

  if (rerunButton) {
    rerunWorkflowHistoryById(rerunButton.dataset.historyId, {
      title: 'Selected Preset Workflow Rerun',
      stepName: 'rerun',
    });
    return;
  }

  if (rerunVerificationButton) {
    rerunWorkflowHistoryById(rerunVerificationButton.dataset.historyId, {
      title: 'Selected Preset Verification Rerun',
      stepName: 'verification',
    });
    return;
  }

  if (loadRequestButton) {
    loadSavedRequestByName(loadRequestButton.dataset.requestKind || '', loadRequestButton.dataset.requestName || '')
      .then((item) => {
        setOutput('runtime-config-status', `Loaded ${item.kind} request from selected preset workflow: ${item.name}`);
      })
      .catch((error) => {
        renderWorkflowResult('Selected Preset Request Load', 'failed', [
          {name: loadRequestButton.dataset.requestKind || 'request', status: 'failed', detail: String(error)},
        ]);
        setOutput('runtime-config-status', String(error));
      });
    return;
  }

  if (stepActionButton) {
    const latestItem = findLatestWorkflowEntryForPreset(stepActionButton.dataset.presetName || '');
    let latestPayload = null;
    try {
      latestPayload = latestItem?.payload ? JSON.parse(latestItem.payload) : null;
    } catch (error) {
      latestPayload = null;
    }
    runSelectedPresetWorkflowRecoveryAction({
      presetName: stepActionButton.dataset.presetName || '',
      stepName: stepActionButton.dataset.stepName || '',
      actionKind: stepActionButton.dataset.actionKind || '',
      serviceName: stepActionButton.dataset.serviceName || '',
      sourceHistoryId: latestPayload?.recovery_for_history_id || latestItem?.id || '',
      sourceWorkflow: latestPayload?.recovery_for_workflow || latestPayload?.workflow || '',
    }).catch((error) => {
      renderWorkflowResult('Selected Preset Recovery', 'failed', [
        {name: stepActionButton.dataset.stepName || 'recovery', status: 'failed', detail: String(error)},
      ]);
      setOutput('runtime-config-status', String(error));
    });
    return;
  }

  if (retryOriginalButton) {
    rerunWorkflowHistoryById(retryOriginalButton.dataset.historyId, {
      title: 'Original Workflow Retry',
      stepName: 'retry_original',
    });
    return;
  }

  if (exportButton) {
    const item = currentExecutionHistory.find((historyItem) => historyItem.id === exportButton.dataset.historyId);
    if (!item) {
      return;
    }
    exportLatestResult('export-preview', buildHistoryExportPayload(item))
      .then(refreshOverview)
      .catch((error) => {
        renderExportPreview({name: 'Export Error', path: '-', content: String(error)});
      });
    return;
  }

  if (failureButton) {
    const item = currentExecutionHistory.find((historyItem) => historyItem.id === failureButton.dataset.historyId);
    if (!item) {
      return;
    }
    const detail = item.payload ? buildHistoryExportPayload(item).content : (item.detail || item.summary || '-');
    renderExportPreview({
      name: `Failure Detail: ${item.title || 'workflow'}`,
      path: item.timestamp || '-',
      content: detail,
    });
  }
});

document.getElementById('runtime-stack-output').addEventListener('click', (event) => {
  const retryButton = event.target.closest('.retry-original-after-recovery-btn');
  const loadRequestButton = event.target.closest('.load-followup-request-btn');
  const exportButton = event.target.closest('.export-current-workflow-btn');

  if (retryButton) {
    rerunWorkflowHistoryById(retryButton.dataset.historyId, {
      title: 'Original Workflow Retry',
      stepName: 'retry_original',
    });
    return;
  }

  if (loadRequestButton) {
    loadSavedRequestByName(loadRequestButton.dataset.requestKind || '', loadRequestButton.dataset.requestName || '')
      .then((item) => {
        setOutput('runtime-config-status', `Loaded follow-up ${item.kind} request: ${item.name}`);
      })
      .catch((error) => {
        renderRuntimeMessage('runtime-stack-output', String(error));
      });
    return;
  }

  if (exportButton) {
    exportLatestResult('runtime-stack-output', latestWorkflowExport).catch((error) => {
      renderRuntimeMessage('runtime-stack-output', String(error));
    });
  }
});

function bindValidationAction(containerId) {
  document.getElementById(containerId).addEventListener('click', async (event) => {
    const button = event.target.closest('.validation-start-services-btn');
    const serviceButton = event.target.closest('.validation-start-service-btn');
    const runtimeProfileButton = event.target.closest('.validation-apply-runtime-profile-btn');
    if (runtimeProfileButton) {
      if (!latestValidatedPreset) {
        setOutput('runtime-config-status', 'No validated preset is available yet.');
        return;
      }

      try {
        await runSelectedPresetWorkflowRecoveryAction({
          presetName: latestValidatedPreset.name || '',
          actionKind: 'apply-runtime-profile',
          stepName: 'validation',
        });
      } catch (error) {
        setOutput('runtime-config-status', String(error));
        renderWorkflowResult(`Validation Runtime: ${presetLabel(latestValidatedPreset)}`, 'failed', [
          {name: 'runtime_profile', status: 'failed', detail: String(error)},
        ]);
      }
      return;
    }
    if (serviceButton) {
      if (!latestValidatedPreset) {
        setOutput('runtime-config-status', 'No validated preset is available yet.');
        return;
      }

      const serviceName = serviceButton.dataset.serviceName || '';
      try {
        await runSelectedPresetWorkflowRecoveryAction({
          presetName: latestValidatedPreset.name || '',
          actionKind: 'start-service',
          serviceName,
          stepName: serviceName,
        });
      } catch (error) {
        setOutput('runtime-config-status', String(error));
        renderWorkflowResult(`Validation Recovery: ${presetLabel(latestValidatedPreset)}`, 'failed', [
          {name: serviceName || 'service', status: 'failed', detail: String(error)},
        ]);
      }
      return;
    }
    if (!button) {
      return;
    }
    if (!latestValidatedPreset) {
      setOutput('runtime-config-status', 'No validated preset is available yet.');
      return;
    }

    try {
      await runSelectedPresetWorkflowRecoveryAction({
        presetName: latestValidatedPreset.name || '',
        actionKind: 'start-recommended-stack',
        stepName: 'validation',
      });
    } catch (error) {
      setOutput('runtime-config-status', String(error));
      renderWorkflowResult('Validation Recovery', 'failed', [
        {name: 'recommended_stack', status: 'failed', detail: String(error)},
      ]);
    }
  });
}

document.getElementById('preset-catalog').addEventListener('click', async (event) => {
  const toggleButton = event.target.closest('.toggle-preset-card-btn');
  const showWorkflowButton = event.target.closest('.show-preset-workflow-card-btn');
  const loadButton = event.target.closest('.load-preset-card-btn');
  const runBatchValidateButton = event.target.closest('.catalog-preset-run-batch-validate-btn');
  const runBatchSmokeButton = event.target.closest('.catalog-preset-run-batch-smoke-btn');
  const runBatchVerificationButton = event.target.closest('.catalog-preset-run-batch-verification-btn');
  const runBatchWatchButton = event.target.closest('.catalog-preset-run-batch-watch-btn');
  const runBatchRuntimeStackPrepareButton = event.target.closest('.catalog-preset-run-batch-runtime-stack-prepare-btn');
  const runBatchIngestButton = event.target.closest('.catalog-preset-run-batch-ingest-btn');
  const runBatchEvalButton = event.target.closest('.catalog-preset-run-batch-eval-btn');
  const runBatchIngestEvalButton = event.target.closest('.catalog-preset-run-batch-ingest-eval-btn');
  const runBatchStackButton = event.target.closest('.catalog-preset-run-batch-stack-btn');
  const runWatchButton = event.target.closest('.catalog-preset-run-watch-btn');
  const runIngestButton = event.target.closest('.catalog-preset-run-ingest-btn');
  const runEvalButton = event.target.closest('.catalog-preset-run-eval-btn');
  const runIngestEvalButton = event.target.closest('.catalog-preset-run-ingest-eval-btn');
  const applyRuntimeButton = event.target.closest('.apply-preset-runtime-card-btn');
  const applyRuntimeStackButton = event.target.closest('.apply-preset-runtime-stack-card-btn');
  const validateButton = event.target.closest('.validate-preset-card-btn');
  const verifyButton = event.target.closest('.verify-preset-card-btn');
  const exportButton = event.target.closest('.export-preset-card-btn');
  const retryButton = event.target.closest('.retry-preset-card-btn');
  const runButton = event.target.closest('.run-preset-card-btn');
  const runSmokeButton = event.target.closest('.catalog-preset-run-smoke-btn');
  if (toggleButton) {
    const presetName = String(toggleButton.dataset.presetName || '').trim();
    if (!presetName) {
      return;
    }
    if (expandedPresetNames.has(presetName)) {
      expandedPresetNames.delete(presetName);
    } else {
      expandedPresetNames.add(presetName);
    }
    renderPresetCatalog(currentPresets);
    return;
  }
  if (showWorkflowButton) {
    const presetName = String(showWorkflowButton.dataset.presetName || '').trim();
    if (!presetName) {
      return;
    }
    syncPresetSelections(presetName);
    renderSelectedPresetWorkflowByName(presetName);
    return;
  }
  if (loadButton) {
    const preset = currentPresets.find((item) => item.name === loadButton.dataset.presetName);
    if (!preset) {
      return;
    }
    applyProjectPreset(preset);
    syncPresetSelections(preset.name);
    activateTab('runtime');
    setOutput('runtime-config-status', `Loaded preset from catalog: ${preset.name}`);
    return;
  }
  if (runBatchValidateButton) {
    try {
      await runSinglePresetBatchValidate(runBatchValidateButton.dataset.presetName || '');
    } catch (error) {
      renderWorkflowResult('Catalog Preset Batch Validate', 'failed', [
        {name: 'batch_validate', status: 'failed', detail: String(error)},
      ]);
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runBatchSmokeButton) {
    try {
      await runSinglePresetBatchSmoke(runBatchSmokeButton.dataset.presetName || '');
    } catch (error) {
      renderWorkflowResult('Catalog Preset Batch Smoke', 'failed', [
        {name: 'batch_smoke', status: 'failed', detail: String(error)},
      ]);
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runBatchVerificationButton) {
    try {
      await runSinglePresetBatchVerification(runBatchVerificationButton.dataset.presetName || '');
    } catch (error) {
      renderWorkflowResult('Catalog Preset Batch Verification', 'failed', [
        {name: 'batch_verification', status: 'failed', detail: String(error)},
      ]);
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runBatchWatchButton) {
    try {
      await runSinglePresetBatchWatch(runBatchWatchButton.dataset.presetName || '');
    } catch (error) {
      renderWorkflowResult('Catalog Preset Batch Watch', 'failed', [
        {name: 'batch_watch', status: 'failed', detail: String(error)},
      ]);
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runBatchRuntimeStackPrepareButton) {
    try {
      await runSinglePresetBatchRuntimeStackPrepare(runBatchRuntimeStackPrepareButton.dataset.presetName || '');
    } catch (error) {
      renderWorkflowResult('Catalog Preset Batch Runtime + Stack', 'failed', [
        {name: 'batch_runtime_stack_prepare', status: 'failed', detail: String(error)},
      ]);
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runBatchIngestButton) {
    try {
      await runSinglePresetBatchIngest(runBatchIngestButton.dataset.presetName || '');
    } catch (error) {
      renderWorkflowResult('Catalog Preset Batch Ingest', 'failed', [
        {name: 'batch_ingest', status: 'failed', detail: String(error)},
      ]);
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runBatchEvalButton) {
    try {
      await runSinglePresetBatchEval(runBatchEvalButton.dataset.presetName || '');
    } catch (error) {
      renderWorkflowResult('Catalog Preset Batch Eval', 'failed', [
        {name: 'batch_eval', status: 'failed', detail: String(error)},
      ]);
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runBatchIngestEvalButton) {
    try {
      await runSinglePresetBatchIngestEval(runBatchIngestEvalButton.dataset.presetName || '');
    } catch (error) {
      renderWorkflowResult('Catalog Preset Batch Ingest + Eval', 'failed', [
        {name: 'batch_ingest_eval', status: 'failed', detail: String(error)},
      ]);
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runBatchStackButton) {
    try {
      await runSinglePresetBatchStackWorkflow(runBatchStackButton.dataset.presetName || '');
    } catch (error) {
      renderWorkflowResult('Catalog Preset Batch Stack Workflow', 'failed', [
        {name: 'batch_stack_ingest_eval', status: 'failed', detail: String(error)},
      ]);
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runSmokeButton) {
    try {
      await runPresetShortcutWorkflowByName(runSmokeButton.dataset.presetName || '', 'smoke', 'Catalog Preset Workflow');
    } catch (error) {
      renderWorkflowResult('Catalog Preset Workflow', 'failed', [
        {name: 'smoke', status: 'failed', detail: String(error)},
      ]);
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runWatchButton) {
    try {
      await runPresetShortcutWorkflowByName(runWatchButton.dataset.presetName || '', 'watch', 'Catalog Preset Workflow');
    } catch (error) {
      renderWorkflowResult('Catalog Preset Workflow', 'failed', [
        {name: 'watch', status: 'failed', detail: String(error)},
      ]);
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runIngestButton) {
    try {
      await runPresetShortcutWorkflowByName(runIngestButton.dataset.presetName || '', 'ingest', 'Catalog Preset Workflow');
    } catch (error) {
      renderWorkflowResult('Catalog Preset Workflow', 'failed', [
        {name: 'ingest', status: 'failed', detail: String(error)},
      ]);
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runEvalButton) {
    try {
      await runPresetShortcutWorkflowByName(runEvalButton.dataset.presetName || '', 'eval', 'Catalog Preset Workflow');
    } catch (error) {
      renderWorkflowResult('Catalog Preset Workflow', 'failed', [
        {name: 'eval', status: 'failed', detail: String(error)},
      ]);
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (runIngestEvalButton) {
    try {
      await runPresetShortcutWorkflowByName(runIngestEvalButton.dataset.presetName || '', 'ingest-eval', 'Catalog Preset Workflow');
    } catch (error) {
      renderWorkflowResult('Catalog Preset Workflow', 'failed', [
        {name: 'ingest', status: 'failed', detail: String(error)},
        {name: 'eval', status: 'skipped', detail: 'Workflow aborted.'},
      ]);
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (applyRuntimeButton) {
    const preset = currentPresets.find((item) => item.name === applyRuntimeButton.dataset.presetName);
    if (!preset) {
      return;
    }
    try {
      syncPresetSelections(preset.name);
      await runSelectedPresetWorkflowRecoveryAction({
        presetName: preset.name,
        actionKind: 'apply-runtime-profile',
        stepName: 'catalog',
      });
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (applyRuntimeStackButton) {
    const preset = currentPresets.find((item) => item.name === applyRuntimeStackButton.dataset.presetName);
    if (!preset) {
      return;
    }
    try {
      syncPresetSelections(preset.name);
      await runPresetRuntimePreparationAndStack({
        preset,
        workflowName: 'catalog',
        successMessage: (item) => `Applied runtime profile and started stack from catalog: ${item.name}`,
        failureMessage: (item) => `Applied runtime profile but stack had issues from catalog: ${item.name}`,
      });
    } catch (error) {
      setOutput('runtime-config-status', String(error));
      renderRuntimeMessage('runtime-stack-output', String(error));
    }
    return;
  }
  if (validateButton) {
    const preset = currentPresets.find((item) => item.name === validateButton.dataset.presetName);
    if (!preset) {
      return;
    }
    try {
      syncPresetSelections(preset.name);
      await validateAndRenderPreset(preset);
      setOutput('runtime-config-status', `Validated preset from catalog: ${preset.name}`);
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (verifyButton) {
    const preset = currentPresets.find((item) => item.name === verifyButton.dataset.presetName);
    if (!preset) {
      return;
    }
    try {
      syncPresetSelections(preset.name);
      await runPresetVerificationWorkflow(preset);
    } catch (error) {
      renderWorkflowResult('Preset Verification', 'failed', [
        {name: 'verification', status: 'failed', detail: String(error)},
      ]);
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (exportButton) {
    const preset = currentPresets.find((item) => item.name === exportButton.dataset.presetName);
    if (!preset) {
      return;
    }
    try {
      await exportLatestResult('export-preview', buildPresetExportPayload(preset));
      await refreshOverview();
      setOutput('runtime-config-status', `Exported preset summary: ${preset.name}`);
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }
  if (retryButton) {
    const presetName = retryButton.dataset.presetName || '';
    const item = findLatestWorkflowEntryForPreset(presetName);
    if (!item) {
      setOutput('runtime-config-status', `No workflow history to retry for preset: ${presetName}`);
      return;
    }
    rerunWorkflowHistoryItemWithHandling(item, {
      title: 'Preset Workflow Retry',
      stepName: 'retry',
    });
    return;
  }
  if (runButton) {
    const preset = currentPresets.find((item) => item.name === runButton.dataset.presetName);
    if (!preset) {
      return;
    }
    try {
      await runPresetStackIngestEvalWorkflow(preset);
    } catch (error) {
      renderWorkflowResult('Preset Workflow', 'failed', [
        {name: 'workflow', status: 'failed', detail: String(error)},
      ]);
      setOutput('runtime-config-status', String(error));
    }
  }
});

document.getElementById('preset-catalog').addEventListener('change', (event) => {
  const checkbox = event.target.closest('.batch-preset-checkbox');
  if (!checkbox) {
    return;
  }
  const presetName = String(checkbox.dataset.presetName || '').trim();
  if (!presetName) {
    return;
  }
  if (checkbox.checked) {
    selectedBatchPresetNames.add(presetName);
  } else {
    selectedBatchPresetNames.delete(presetName);
  }
  void persistBatchPresetSelection();
  if (!batchWorkflowState?.running) {
    batchWorkflowState = null;
    void persistBatchWorkflowState();
  }
  renderBatchPresetOutput();
});

document.getElementById('batch-preset-output').addEventListener('click', (event) => {
  const clearButton = event.target.closest('.batch-clear-btn');
  const cancelButton = event.target.closest('.batch-cancel-btn');
  if (clearButton && batchWorkflowState && !batchWorkflowState.running) {
    batchWorkflowState = null;
    void persistBatchWorkflowState();
    renderBatchPresetOutput();
    setOutput('runtime-config-status', 'Cleared batch workflow result.');
    return;
  }
  if (!cancelButton || !batchWorkflowState?.running) {
    return;
  }
  batchWorkflowState = {
    ...batchWorkflowState,
    status: 'cancelling',
    cancelRequested: true,
  };
  renderBatchPresetOutput();
  CancelBatchWorkflow()
    .then((state) => {
      batchWorkflowState = normalizeBatchWorkflowState(state) || batchWorkflowState;
      renderBatchPresetOutput();
    })
    .catch(() => {
      void persistBatchWorkflowState();
    });
  setOutput('runtime-config-status', `Cancellation requested for ${batchWorkflowState.workflowLabel}. The current preset will finish first.`);
});

document.getElementById('exported-results').addEventListener('click', async (event) => {
  const button = event.target.closest('.preview-export-btn');
  if (!button) {
    return;
  }
  const path = button.dataset.exportPath;
  if (!path) {
    return;
  }
  try {
    const file = await ReadExportedResult({path});
    renderExportPreview(file);
  } catch (error) {
    renderExportPreview({name: 'Preview Error', path, content: String(error)});
  }
});

document.getElementById('save-preset').addEventListener('click', async () => {
  try {
    const preset = collectProjectPreset();
    if (!preset.name) {
      setOutput('runtime-config-status', 'Preset name is required.');
      return;
    }
    await SaveProjectPreset(preset);
    await refreshPresets();
  } catch (error) {
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('load-preset').addEventListener('click', async () => {
  try {
    const preset = await resolveSelectedPreset();
    applyProjectPreset(preset);
    syncPresetSelections(preset.name);
    setOutput('runtime-config-status', `Loaded preset: ${preset.name}`);
  } catch (error) {
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('delete-preset').addEventListener('click', async () => {
  try {
    const name = document.getElementById('preset-select').value || document.getElementById('preset-name').value.trim();
    if (!name) {
      setOutput('runtime-config-status', 'Select or enter a preset name first.');
      return;
    }
    await DeleteProjectPreset({name});
    await refreshPresets();
  } catch (error) {
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('reload-presets').addEventListener('click', refreshPresets);

document.getElementById('select-filtered-batch-presets').addEventListener('click', () => {
  getVisiblePresetCatalogEntries(currentPresets).forEach(({preset}) => {
    selectedBatchPresetNames.add(preset.name);
  });
  void persistBatchPresetSelection();
  if (!batchWorkflowState?.running) {
    batchWorkflowState = null;
    void persistBatchWorkflowState();
  }
  renderPresetCatalog(currentPresets);
  setOutput('runtime-config-status', `Selected ${selectedBatchPresetNames.size} presets for batch execution.`);
});

document.getElementById('clear-batch-presets').addEventListener('click', () => {
  selectedBatchPresetNames = new Set();
  void ClearBatchPresetSelection();
  window.localStorage.removeItem(BATCH_PRESET_SELECTION_KEY);
  if (!batchWorkflowState?.running) {
    batchWorkflowState = null;
    void persistBatchWorkflowState();
  }
  renderPresetCatalog(currentPresets);
  setOutput('runtime-config-status', 'Cleared batch preset selection.');
});

document.getElementById('run-batch-preset-verification').addEventListener('click', async () => {
  const presets = currentPresets.filter((preset) => selectedBatchPresetNames.has(preset.name));
  if (presets.length === 0) {
    renderBatchPresetOutput();
    setOutput('runtime-config-status', 'No presets selected for batch verification.');
    return;
  }
  try {
    const state = await StartBatchPresetVerification({
      preset_names: presets.map((preset) => preset.name),
    });
    batchWorkflowState = normalizeBatchWorkflowState(state);
    syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
    renderBatchPresetOutput();
    renderPresetCatalog(currentPresets);
    setOutput('runtime-config-status', `Started Go-backed batch verification for ${presets.length} presets.`);
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    renderBatchPresetOutput();
  }
});

document.getElementById('run-batch-preset-validate').addEventListener('click', async () => {
  const presets = currentPresets.filter((preset) => selectedBatchPresetNames.has(preset.name));
  if (presets.length === 0) {
    renderBatchPresetOutput();
    setOutput('runtime-config-status', 'No presets selected for batch validate.');
    return;
  }
  try {
    const state = await StartBatchPresetValidate({
      preset_names: presets.map((preset) => preset.name),
    });
    batchWorkflowState = normalizeBatchWorkflowState(state);
    syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
    renderBatchPresetOutput();
    renderPresetCatalog(currentPresets);
    setOutput('runtime-config-status', `Started Go-backed batch validate for ${presets.length} presets.`);
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    renderBatchPresetOutput();
  }
});

document.getElementById('run-batch-preset-smoke').addEventListener('click', async () => {
  const presets = currentPresets.filter((preset) => selectedBatchPresetNames.has(preset.name));
  if (presets.length === 0) {
    renderBatchPresetOutput();
    setOutput('runtime-config-status', 'No presets selected for batch smoke.');
    return;
  }
  try {
    const state = await StartBatchPresetSmoke({
      preset_names: presets.map((preset) => preset.name),
    });
    batchWorkflowState = normalizeBatchWorkflowState(state);
    syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
    renderBatchPresetOutput();
    renderPresetCatalog(currentPresets);
    setOutput('runtime-config-status', `Started Go-backed batch smoke for ${presets.length} presets.`);
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    renderBatchPresetOutput();
  }
});

document.getElementById('run-batch-preset-watch').addEventListener('click', async () => {
  const presets = currentPresets.filter((preset) => selectedBatchPresetNames.has(preset.name));
  if (presets.length === 0) {
    renderBatchPresetOutput();
    setOutput('runtime-config-status', 'No presets selected for batch watch.');
    return;
  }
  try {
    const state = await StartBatchPresetWatch({
      preset_names: presets.map((preset) => preset.name),
    });
    batchWorkflowState = normalizeBatchWorkflowState(state);
    syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
    renderBatchPresetOutput();
    renderPresetCatalog(currentPresets);
    setOutput('runtime-config-status', `Started Go-backed batch watch for ${presets.length} presets.`);
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    renderBatchPresetOutput();
  }
});

document.getElementById('run-batch-preset-runtime-stack-prepare').addEventListener('click', async () => {
  const presets = currentPresets.filter((preset) => selectedBatchPresetNames.has(preset.name));
  if (presets.length === 0) {
    renderBatchPresetOutput();
    setOutput('runtime-config-status', 'No presets selected for batch runtime + stack prepare.');
    return;
  }
  try {
    const state = await StartBatchPresetRuntimeStackPrepare({
      preset_names: presets.map((preset) => preset.name),
    });
    batchWorkflowState = normalizeBatchWorkflowState(state);
    syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
    renderBatchPresetOutput();
    renderPresetCatalog(currentPresets);
    setOutput('runtime-config-status', `Started Go-backed batch runtime + stack prepare for ${presets.length} presets.`);
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    renderBatchPresetOutput();
  }
});

document.getElementById('run-batch-preset-ingest').addEventListener('click', async () => {
  const presets = currentPresets.filter((preset) => selectedBatchPresetNames.has(preset.name));
  if (presets.length === 0) {
    renderBatchPresetOutput();
    setOutput('runtime-config-status', 'No presets selected for batch ingest.');
    return;
  }
  try {
    const state = await StartBatchPresetIngest({
      preset_names: presets.map((preset) => preset.name),
    });
    batchWorkflowState = normalizeBatchWorkflowState(state);
    syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
    renderBatchPresetOutput();
    renderPresetCatalog(currentPresets);
    setOutput('runtime-config-status', `Started Go-backed batch ingest for ${presets.length} presets.`);
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    renderBatchPresetOutput();
  }
});

document.getElementById('run-batch-preset-eval').addEventListener('click', async () => {
  const presets = currentPresets.filter((preset) => selectedBatchPresetNames.has(preset.name));
  if (presets.length === 0) {
    renderBatchPresetOutput();
    setOutput('runtime-config-status', 'No presets selected for batch eval.');
    return;
  }
  try {
    const state = await StartBatchPresetEval({
      preset_names: presets.map((preset) => preset.name),
    });
    batchWorkflowState = normalizeBatchWorkflowState(state);
    syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
    renderBatchPresetOutput();
    renderPresetCatalog(currentPresets);
    setOutput('runtime-config-status', `Started Go-backed batch eval for ${presets.length} presets.`);
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    renderBatchPresetOutput();
  }
});

document.getElementById('run-batch-preset-ingest-eval').addEventListener('click', async () => {
  const presets = currentPresets.filter((preset) => selectedBatchPresetNames.has(preset.name));
  if (presets.length === 0) {
    renderBatchPresetOutput();
    setOutput('runtime-config-status', 'No presets selected for batch ingest + eval.');
    return;
  }
  try {
    const state = await StartBatchPresetIngestEval({
      preset_names: presets.map((preset) => preset.name),
    });
    batchWorkflowState = normalizeBatchWorkflowState(state);
    syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
    renderBatchPresetOutput();
    renderPresetCatalog(currentPresets);
    setOutput('runtime-config-status', `Started Go-backed batch ingest + eval for ${presets.length} presets.`);
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    renderBatchPresetOutput();
  }
});

document.getElementById('run-batch-preset-stack-ingest-eval').addEventListener('click', async () => {
  const presets = currentPresets.filter((preset) => selectedBatchPresetNames.has(preset.name));
  if (presets.length === 0) {
    renderBatchPresetOutput();
    setOutput('runtime-config-status', 'No presets selected for batch stack + ingest + eval.');
    return;
  }
  try {
    const state = await StartBatchPresetStackIngestEval({
      preset_names: presets.map((preset) => preset.name),
    });
    batchWorkflowState = normalizeBatchWorkflowState(state);
    syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
    renderBatchPresetOutput();
    renderPresetCatalog(currentPresets);
    setOutput('runtime-config-status', `Started Go-backed batch stack + ingest + eval for ${presets.length} presets.`);
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    renderBatchPresetOutput();
  }
});

document.getElementById('preset-compare-left').addEventListener('change', (event) => {
  presetCompareLeftName = event.target.value || '';
  renderPresetComparison();
});

document.getElementById('preset-compare-right').addEventListener('change', (event) => {
  presetCompareRightName = event.target.value || '';
  renderPresetComparison();
});

document.getElementById('use-selected-for-compare').addEventListener('click', () => {
  const selectedName = document.getElementById('overview-preset-select').value
    || document.getElementById('preset-select').value
    || document.getElementById('preset-name').value.trim();
  if (!selectedName) {
    setOutput('runtime-config-status', 'Select a preset first.');
    return;
  }
  if (!presetCompareLeftName || presetCompareLeftName === selectedName) {
    presetCompareLeftName = selectedName;
    document.getElementById('preset-compare-left').value = selectedName;
  } else {
    presetCompareRightName = selectedName;
    document.getElementById('preset-compare-right').value = selectedName;
  }
  renderPresetComparison();
});

document.getElementById('swap-compare-presets').addEventListener('click', () => {
  const nextLeft = presetCompareRightName;
  const nextRight = presetCompareLeftName;
  presetCompareLeftName = nextLeft;
  presetCompareRightName = nextRight;
  document.getElementById('preset-compare-left').value = nextLeft;
  document.getElementById('preset-compare-right').value = nextRight;
  renderPresetComparison();
});

document.getElementById('export-preset-compare').addEventListener('click', async () => {
  const payload = buildPresetComparisonExportPayload();
  if (!payload) {
    setOutput('runtime-config-status', 'Comparison export requires two presets with verification history.');
    return;
  }
  await exportLatestResult('preset-compare-output', payload);
  await refreshOverview();
  setOutput('runtime-config-status', `Exported preset comparison: ${presetCompareLeftName} vs ${presetCompareRightName}`);
});

document.getElementById('preset-catalog-filter').addEventListener('change', (event) => {
  presetCatalogFilter = event.target.value || 'all';
  writeLocalSetting(PRESET_CATALOG_FILTER_KEY, presetCatalogFilter);
  renderPresetCatalog(currentPresets);
});

document.getElementById('preset-catalog-sort').addEventListener('change', (event) => {
  presetCatalogSort = event.target.value || 'name';
  writeLocalSetting(PRESET_CATALOG_SORT_KEY, presetCatalogSort);
  renderPresetCatalog(currentPresets);
});

document.getElementById('eval-dataset-trend-filter').addEventListener('change', (event) => {
  evalDatasetTrendFilter = event.target.value || 'all';
  writeLocalSetting(EVAL_DATASET_TREND_FILTER_KEY, evalDatasetTrendFilter);
  renderWorkflowSummary(currentExecutionHistory);
});

document.getElementById('eval-dataset-trend-sort').addEventListener('change', (event) => {
  evalDatasetTrendSort = event.target.value || 'dataset';
  writeLocalSetting(EVAL_DATASET_TREND_SORT_KEY, evalDatasetTrendSort);
  renderWorkflowSummary(currentExecutionHistory);
});

document.getElementById('regression-watch-source-hit-drop').addEventListener('change', (event) => {
  const value = Number(event.target.value || 0);
  regressionWatchSourceHitDrop = Number.isFinite(value) && value >= 0 ? value : 0;
  void persistRegressionWatchSettings();
  renderWorkflowSummary(currentExecutionHistory);
});

document.getElementById('regression-watch-include-preset').addEventListener('change', (event) => {
  regressionWatchIncludePreset = event.target.checked === true;
  void persistRegressionWatchSettings();
  renderWorkflowSummary(currentExecutionHistory);
});

document.getElementById('regression-watch-include-dataset').addEventListener('change', (event) => {
  regressionWatchIncludeDataset = event.target.checked === true;
  void persistRegressionWatchSettings();
  renderWorkflowSummary(currentExecutionHistory);
});

document.getElementById('apply-regression-watch-profile').addEventListener('click', async () => {
  const profileKey = document.getElementById('regression-watch-profile-select').value;
  if (!profileKey) {
    setOutput('runtime-config-status', 'Select a watch profile first.');
    return;
  }
  const profiles = getMergedRegressionWatchProfiles();
  const profile = profiles[profileKey];
  if (!profile) {
    setOutput('runtime-config-status', `Watch profile not found: ${profileKey}`);
    return;
  }
  applyRegressionWatchSettings(profile);
  await persistRegressionWatchSettings(profile);
  renderWorkflowSummary(currentExecutionHistory);
  setOutput('runtime-config-status', `Applied watch profile: ${profile.label}`);
});

document.getElementById('save-regression-watch-profile').addEventListener('click', async () => {
  const rawName = document.getElementById('regression-watch-profile-name').value.trim();
  if (!rawName) {
    setOutput('runtime-config-status', 'Watch profile name is required.');
    return;
  }
  const key = rawName.toLowerCase().replaceAll(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'watch_profile';
  currentRegressionWatchProfiles[key] = {
    label: rawName,
    ...getCurrentRegressionWatchSettings(),
    builtin: false,
  };
  await persistRegressionWatchProfiles(currentRegressionWatchProfiles);
  renderRegressionWatchProfileOptions();
  document.getElementById('regression-watch-profile-select').value = key;
  setOutput('runtime-config-status', `Saved watch profile: ${rawName}`);
});

document.getElementById('delete-regression-watch-profile').addEventListener('click', async () => {
  const profileKey = document.getElementById('regression-watch-profile-select').value;
  if (!profileKey) {
    setOutput('runtime-config-status', 'Select a watch profile first.');
    return;
  }
  const builtin = getBuiltinRegressionWatchProfiles();
  if (builtin[profileKey]) {
    setOutput('runtime-config-status', `Built-in watch profile cannot be deleted: ${builtin[profileKey].label}`);
    return;
  }
  const profiles = {...currentRegressionWatchProfiles};
  const profile = profiles[profileKey];
  if (!profile) {
    setOutput('runtime-config-status', `Watch profile not found: ${profileKey}`);
    return;
  }
  delete profiles[profileKey];
  currentRegressionWatchProfiles = profiles;
  await persistRegressionWatchProfiles(currentRegressionWatchProfiles);
  renderRegressionWatchProfileOptions();
  document.getElementById('regression-watch-profile-select').value = '';
  setOutput('runtime-config-status', `Deleted watch profile: ${profile.label}`);
});

document.getElementById('preset-select').addEventListener('change', (event) => {
  syncPresetSelections(event.target.value);
});

document.getElementById('overview-preset-select').addEventListener('change', (event) => {
  syncPresetSelections(event.target.value);
});

document.getElementById('validate-current-preset').addEventListener('click', async () => {
  try {
    await validateAndRenderPreset(collectProjectPreset());
    setOutput('runtime-config-status', 'Validated current preset form.');
  } catch (error) {
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('overview-validate-preset').addEventListener('click', async () => {
  try {
    const preset = await resolvePresetByName(document.getElementById('overview-preset-select').value);
    await validateAndRenderPreset(preset);
  } catch (error) {
    renderPresetValidation({
      preset_name: 'overview selection',
      valid: false,
      ready: false,
      warnings: [String(error)],
      config_warnings: [],
      required_services: [],
      optional_services: [],
      path_checks: [],
      service_checks: [],
    });
  }
});

document.getElementById('preset-start-watch').addEventListener('click', async () => {
  try {
    const preset = await resolveSelectedPreset();
    await runGoPresetWorkflow({
      preset,
      runner: RunPresetWatch,
      title: `Preset Workflow: ${presetLabel(preset)}`,
      successMessage: (item) => `Started watch from preset: ${item.name}`,
      failureMessage: (item) => `Preset watch found issues: ${item.name}`,
      tab: 'runtime',
    });
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    renderWorkflowResult('Preset Workflow', 'failed', [
      {name: 'watch', status: 'failed', detail: String(error)},
    ]);
  }
});

document.getElementById('preset-run-ingest').addEventListener('click', async () => {
  try {
    const preset = await resolveSelectedPreset();
    await runGoPresetWorkflow({
      preset,
      runner: RunPresetIngest,
      title: `Preset Workflow: ${presetLabel(preset)}`,
      successMessage: (item) => `Ingest started from preset: ${item.name}`,
      failureMessage: (item) => `Preset ingest found issues: ${item.name}`,
      tab: 'rag',
    });
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    renderWorkflowResult('Preset Workflow', 'failed', [
      {name: 'ingest', status: 'failed', detail: String(error)},
    ]);
  }
});

document.getElementById('preset-run-eval').addEventListener('click', async () => {
  try {
    const preset = await resolveSelectedPreset();
    await runGoPresetWorkflow({
      preset,
      runner: RunPresetEval,
      title: `Preset Workflow: ${presetLabel(preset)}`,
      successMessage: (item) => `Eval completed from preset: ${item.name}`,
      failureMessage: (item) => `Preset eval found issues: ${item.name}`,
      tab: 'eval',
    });
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    renderWorkflowResult('Preset Workflow', 'failed', [
      {name: 'eval', status: 'failed', detail: String(error)},
    ]);
  }
});

document.getElementById('preset-run-verification').addEventListener('click', async () => {
  try {
    const preset = await resolveSelectedPreset();
    await runPresetVerificationWorkflow(preset);
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    renderWorkflowResult('Preset Verification', 'failed', [
      {name: 'verification', status: 'failed', detail: String(error)},
    ]);
    await recordWorkflowExecution({
      workflow: 'preset_verification',
      preset: {name: document.getElementById('preset-name').value.trim()},
      status: 'error',
      summary: `verification | ${document.getElementById('preset-name').value.trim() || 'unknown preset'}`,
      detail: String(error),
      steps: [{name: 'verification', status: 'failed', detail: String(error)}],
    });
  }
});

document.getElementById('preset-run-ingest-eval').addEventListener('click', async () => {
  try {
    const preset = await resolveSelectedPreset();
    await runGoPresetWorkflow({
      preset,
      runner: RunPresetIngestEval,
      title: `Preset Workflow: ${presetLabel(preset)}`,
      successMessage: (item) => `Preset ingest + eval completed: ${item.name}`,
      failureMessage: (item) => `Preset ingest + eval found issues: ${item.name}`,
      tab: 'eval',
    });
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    renderWorkflowResult('Preset Workflow', 'failed', [
      {name: 'ingest', status: 'failed', detail: String(error)},
      {name: 'eval', status: 'skipped', detail: 'Workflow aborted.'},
    ]);
  }
});

async function runPresetStackIngestEvalWorkflow(preset) {
  applyProjectPreset(preset);
  activateTab('eval');
  const response = await RunPresetStackIngestEval(preset);
  const ok = response?.status === 'ok';
  renderWorkflowResult(`Preset Workflow: ${presetLabel(preset)}`, ok ? 'completed' : 'failed', response?.steps || []);
  await refreshExecutionHistory();
  await refreshOverview();
  await refreshRuntime();
  setOutput('runtime-config-status', ok ? `Preset stack + ingest + eval completed: ${preset.name}` : `Preset stack + ingest + eval found issues: ${preset.name}`);
}

async function runGoPresetWorkflow({preset, runner, title, successMessage, failureMessage, tab}) {
  applyProjectPreset(preset);
  if (tab) {
    activateTab(tab);
  }
  const response = await runner(preset);
  const ok = response?.status === 'ok';
  renderWorkflowResult(title, ok ? 'completed' : 'failed', response?.steps || []);
  await refreshExecutionHistory();
  await refreshOverview();
  await refreshRuntime();
  setOutput('runtime-config-status', ok ? successMessage(preset) : failureMessage(preset));
}

async function runPresetShortcutWorkflowByName(presetName, workflowKind, renderTitlePrefix = 'Preset Workflow') {
  const preset = await resolvePresetByName(presetName || '');
  syncPresetSelections(preset.name);

  switch (workflowKind) {
    case 'smoke':
      await runGoPresetWorkflow({
        preset,
        runner: RunPresetSmoke,
        title: `${renderTitlePrefix}: ${presetLabel(preset)}`,
        successMessage: (item) => `Preset smoke passed: ${item.name}`,
        failureMessage: (item) => `Preset smoke found issues: ${item.name}`,
        tab: 'runtime',
      });
      return;
    case 'watch':
      await runGoPresetWorkflow({
        preset,
        runner: RunPresetWatch,
        title: `${renderTitlePrefix}: ${presetLabel(preset)}`,
        successMessage: (item) => `Started watch from preset: ${item.name}`,
        failureMessage: (item) => `Preset watch found issues: ${item.name}`,
        tab: 'runtime',
      });
      return;
    case 'ingest':
      await runGoPresetWorkflow({
        preset,
        runner: RunPresetIngest,
        title: `${renderTitlePrefix}: ${presetLabel(preset)}`,
        successMessage: (item) => `Ingest started from preset: ${item.name}`,
        failureMessage: (item) => `Preset ingest found issues: ${item.name}`,
        tab: 'rag',
      });
      return;
    case 'eval':
      await runGoPresetWorkflow({
        preset,
        runner: RunPresetEval,
        title: `${renderTitlePrefix}: ${presetLabel(preset)}`,
        successMessage: (item) => `Eval completed from preset: ${item.name}`,
        failureMessage: (item) => `Preset eval found issues: ${item.name}`,
        tab: 'eval',
      });
      return;
    case 'ingest-eval':
      await runGoPresetWorkflow({
        preset,
        runner: RunPresetIngestEval,
        title: `${renderTitlePrefix}: ${presetLabel(preset)}`,
        successMessage: (item) => `Preset ingest + eval completed: ${item.name}`,
        failureMessage: (item) => `Preset ingest + eval found issues: ${item.name}`,
        tab: 'eval',
      });
      return;
    default:
      throw new Error(`Unsupported preset workflow shortcut: ${workflowKind}`);
  }
}

async function runPresetRuntimePreparationAndStack({preset, workflowName, successMessage, failureMessage}) {
  const response = await runPresetRuntimePreparationAndStackWorkflow(preset, workflowName);
  if (response?.status !== 'ok') {
    setOutput('runtime-config-status', failureMessage(preset));
    return;
  }
  setOutput('runtime-config-status', successMessage(preset));
}

async function runPresetRuntimePreparationAndStackWorkflow(preset, workflowName = 'runtime') {
  applyProjectPreset(preset);
  activateTab('runtime');
  const response = await RunPresetRuntimeStackPrepare(preset);
  const ok = response?.status === 'ok';
  renderWorkflowResult(`Preset Workflow: ${presetLabel(preset)}`, ok ? 'completed' : 'failed', response?.steps || []);
  await refreshExecutionHistory();
  await refreshOverview();
  await refreshRuntime();
  setOutput('runtime-config-status', ok
    ? `Applied runtime profile and started stack for preset: ${preset.name}`
    : `Applied runtime profile but stack had issues for preset: ${preset.name}`);
  return response;
}

async function runGoRuntimeWorkflow({runner, request, title, targetId, successMessage, failureMessage, fileStemPrefix = 'runtime'}) {
  const response = await runner(request);
  const ok = response?.status === 'ok';
  const renderStatus = ok ? 'completed' : response?.status === 'running' ? 'running' : 'failed';
  renderWorkflowResultIntoTarget(targetId, title, renderStatus, response?.steps || [], {
    trackExport: targetId === 'runtime-stack-output',
    fileStemPrefix,
  });
  await refreshExecutionHistory();
  await refreshOverview();
  await refreshRuntime();
  setOutput('runtime-config-status', ok ? successMessage(response) : failureMessage(response));
  return response;
}

async function runGoRuntimeConfigWorkflow({action, name = '', content = '', title, successMessage, failureMessage}) {
  const response = await RunRuntimeConfigAction({action, name, content});
  const ok = response?.status === 'ok';
  const renderStatus = ok ? 'completed' : response?.status === 'running' ? 'running' : 'failed';
  renderWorkflowResult(title, renderStatus, response?.steps || []);
  await refreshExecutionHistory();
  await refreshOverview();
  await refreshRuntime();
  await refreshLocalConfigEditors();
  setOutput('runtime-config-status', ok ? successMessage(response) : failureMessage(response));
  return response;
}

async function runGoRuntimeServiceWorkflow({action, watchRequest = null, title, successMessage, failureMessage}) {
  const response = await RunRuntimeServiceAction({
    action,
    watch: watchRequest || {paths: [], project: '', interval: 0, recursive: false},
  });
  const ok = response?.status === 'ok';
  const renderStatus = ok ? 'completed' : response?.status === 'running' ? 'running' : 'failed';
  renderWorkflowResult(title, renderStatus, response?.steps || []);
  await refreshExecutionHistory();
  await refreshOverview();
  await refreshRuntime();
  setOutput('runtime-config-status', ok ? successMessage(response) : failureMessage(response));
}

async function runRuntimeConfigAndRecommendedStackWorkflow({
  workflow,
  configAction,
  statusMessage,
  summary,
  successDetail,
  configFailureDetail,
  stackFailureDetail,
  modelsLocalContent,
  ragLocalContent,
}) {
  setOutput('runtime-config-status', statusMessage);
  document.getElementById('models-local-editor').value = modelsLocalContent;
  document.getElementById('rag-local-editor').value = ragLocalContent;

  let configResponse = null;
  let stackResponse = null;

  try {
    configResponse = await runGoRuntimeConfigWorkflow({
      action: configAction,
      title: configAction === 'apply_local_only'
        ? 'Runtime Config: Apply Local Only'
        : 'Runtime Config: Apply External RAG',
      successMessage: () => successDetail,
      failureMessage: () => configFailureDetail,
    });
    if (configResponse?.status !== 'ok') {
      throw new Error(configResponse?.detail || configFailureDetail);
    }

    stackResponse = await runGoRuntimeWorkflow({
      runner: RunRuntimeStackAction,
      request: {action: 'start_recommended_stack'},
      title: 'Runtime Stack: Start Recommended',
      targetId: 'runtime-stack-output',
      successMessage: () => successDetail,
      failureMessage: () => stackFailureDetail,
      fileStemPrefix: 'runtime-stack',
    });
    if (stackResponse?.status !== 'ok') {
      throw new Error(stackResponse?.detail || stackFailureDetail);
    }

    await recordWorkflowExecution({
      workflow,
      status: 'ok',
      summary,
      detail: successDetail,
      steps: [
        ...(configResponse?.steps || []),
        ...(stackResponse?.steps || []),
      ],
      extraPayload: {
        config_action: configAction,
        stack_action: 'start_recommended_stack',
      },
    });
  } catch (error) {
    await recordWorkflowExecution({
      workflow,
      status: 'error',
      summary,
      detail: String(error),
      steps: [
        ...(configResponse?.steps || []),
        ...(stackResponse?.steps || []),
        {name: 'combined_workflow', status: 'failed', detail: String(error)},
      ],
      extraPayload: {
        config_action: configAction,
        stack_action: 'start_recommended_stack',
      },
    });
    throw error;
  }
}

async function runApplyLocalOnlyStackWorkflow() {
  await runRuntimeConfigAndRecommendedStackWorkflow({
    workflow: 'runtime_apply_local_only_stack',
    configAction: 'apply_local_only',
    statusMessage: 'Applying local-only preset and starting recommended stack...',
    summary: 'runtime local-only apply + start stack',
    successDetail: 'applied local-only preset and started recommended stack',
    configFailureDetail: 'Applying local-only preset had issues.',
    stackFailureDetail: 'Applied local-only preset but recommended stack had issues.',
    modelsLocalContent: '',
    ragLocalContent: RAG_LOCAL_ONLY_PRESET,
  });
}

async function runApplyExternalRagStackWorkflow() {
  await runRuntimeConfigAndRecommendedStackWorkflow({
    workflow: 'runtime_apply_external_rag_stack',
    configAction: 'apply_external_rag',
    statusMessage: 'Applying external embedding + qdrant preset and starting recommended stack...',
    summary: 'runtime external-rag apply + start stack',
    successDetail: 'applied external embedding + qdrant preset and started recommended stack',
    configFailureDetail: 'Applying external embedding + qdrant preset had issues.',
    stackFailureDetail: 'Applied external preset but recommended stack had issues.',
    modelsLocalContent: MODELS_LOCAL_EXTERNAL_PRESET,
    ragLocalContent: RAG_EXTERNAL_PRESET,
  });
}

function renderBatchWorkflowSummary({workflowLabel, status, results, running = false, cancelRequested = false}) {
  batchWorkflowState = {
    workflowLabel,
    status,
    results,
    running,
    cancelRequested,
  };
  syncBatchPresetSelectionFromState(batchWorkflowState, {preferState: true});
  renderBatchPresetOutput();
  renderPresetCatalog(currentPresets);
  void persistBatchWorkflowState();
}

document.getElementById('preset-run-stack-ingest-eval').addEventListener('click', async () => {
  try {
    const preset = await resolveSelectedPreset();
    await runPresetStackIngestEvalWorkflow(preset);
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    renderWorkflowResult('Preset Workflow', 'failed', [
      {name: 'recommended_stack', status: 'failed', detail: String(error)},
      {name: 'ingest', status: 'skipped', detail: 'Workflow aborted.'},
      {name: 'eval', status: 'skipped', detail: 'Workflow aborted.'},
    ]);
  }
});

document.getElementById('overview-load-preset').addEventListener('click', async () => {
  try {
    const preset = await resolvePresetByName(document.getElementById('overview-preset-select').value);
    applyProjectPreset(preset);
    syncPresetSelections(preset.name);
    activateTab('runtime');
    setOutput('runtime-config-status', `Loaded preset from Overview: ${preset.name}`);
  } catch (error) {
    setOutput('runtime-config-status', String(error));
  }
});

document.getElementById('overview-run-preset-stack-ingest-eval').addEventListener('click', async () => {
  try {
    const preset = await resolvePresetByName(document.getElementById('overview-preset-select').value);
    await runPresetStackIngestEvalWorkflow(preset);
  } catch (error) {
    setOutput('runtime-config-status', String(error));
    renderWorkflowResult('Preset Workflow', 'failed', [
      {name: 'recommended_stack', status: 'failed', detail: String(error)},
      {name: 'ingest', status: 'skipped', detail: 'Workflow aborted.'},
      {name: 'eval', status: 'skipped', detail: 'Workflow aborted.'},
    ]);
  }
});

document.getElementById('overview-preset-runtime-hint').addEventListener('click', async (event) => {
  const openRuntimeButton = event.target.closest('.overview-open-runtime-btn');
  const applyProfileButton = event.target.closest('.overview-apply-runtime-profile-btn');
  const applyStackButton = event.target.closest('.overview-apply-runtime-stack-btn');

  if (openRuntimeButton) {
    activateTab('runtime');
    setOutput('runtime-config-status', 'Opened runtime controls for the selected preset.');
    return;
  }

  if (applyProfileButton) {
    const preset = currentPresets.find((item) => item.name === (applyProfileButton.dataset.presetName || ''));
    if (!preset) {
      return;
    }
    try {
      syncPresetSelections(preset.name);
      await runSelectedPresetWorkflowRecoveryAction({
        presetName: preset.name,
        actionKind: 'apply-runtime-profile',
        stepName: 'launcher',
      });
    } catch (error) {
      setOutput('runtime-config-status', String(error));
    }
    return;
  }

  if (applyStackButton) {
    const preset = currentPresets.find((item) => item.name === (applyStackButton.dataset.presetName || ''));
    if (!preset) {
      return;
    }
    try {
      syncPresetSelections(preset.name);
      await runPresetRuntimePreparationAndStack({
        preset,
        workflowName: 'launcher',
        successMessage: (item) => `Applied runtime profile and started stack for preset: ${item.name}`,
        failureMessage: (item) => `Applied runtime profile but stack had issues for preset: ${item.name}`,
      });
    } catch (error) {
      setOutput('runtime-config-status', String(error));
      renderRuntimeMessage('runtime-stack-output', String(error));
    }
  }
});
