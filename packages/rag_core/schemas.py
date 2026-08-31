from pydantic import BaseModel, ConfigDict, Field


class IngestRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    paths: list[str] = Field(default_factory=list)
    project: str | None = None
    recursive: bool = True
    tags: list[str] = Field(default_factory=list)


class SearchRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    query: str
    project: str | None = None
    source_path: str | None = None
    tags: list[str] = Field(default_factory=list)
    top_k: int = 5


class RAGQueryRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    query: str
    project: str | None = None
    source_path: str | None = None
    tags: list[str] = Field(default_factory=list)
    top_k: int = 5
    answer: bool = True
    stream: bool = False


class IndexBrowseRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    project: str | None = None
    source_query: str | None = None
    limit: int = 20


class IndexSourceRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    source_path: str
    project: str | None = None
    limit: int = 100


class IndexedChunk(BaseModel):
    model_config = ConfigDict(extra="forbid")

    chunk_id: str
    source_path: str
    original_source_path: str | None = None
    doc_id: str | None = None
    title: str | None = None
    relative_path: str | None = None
    updated_at: str | None = None
    source_sha256: str | None = None
    heading_path: list[str] = Field(default_factory=list)
    project: str | None = None
    tags: list[str] = Field(default_factory=list)
    chunk_text: str
    hash: str
    embedding: list[float] | None = None


class SearchResult(BaseModel):
    model_config = ConfigDict(extra="forbid")

    chunk_id: str
    source_path: str
    original_source_path: str | None = None
    doc_id: str | None = None
    title: str | None = None
    relative_path: str | None = None
    updated_at: str | None = None
    source_sha256: str | None = None
    heading_path: list[str] = Field(default_factory=list)
    project: str | None = None
    tags: list[str] = Field(default_factory=list)
    chunk_text: str
    score: float


class IndexProjectSummary(BaseModel):
    model_config = ConfigDict(extra="forbid")

    project: str
    chunk_count: int
    source_count: int


class IndexSourceSummary(BaseModel):
    model_config = ConfigDict(extra="forbid")

    source_path: str
    original_source_path: str | None = None
    doc_id: str | None = None
    title: str | None = None
    relative_path: str | None = None
    updated_at: str | None = None
    source_sha256: str | None = None
    project: str | None = None
    chunk_count: int
    sample_heading: str | None = None
    sample_text: str | None = None
