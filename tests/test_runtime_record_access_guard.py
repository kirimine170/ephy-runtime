import json
from pathlib import Path
import subprocess

import httpx
import pytest

from packages.config_core.loader import VectorDBConfig
from packages.karte_core.protected import protected_path, has_record_marker
from packages.karte_core.source import KarteSourceAdapter
from packages.rag_core.schemas import IndexedChunk
from packages.rag_core.store import JsonChunkStore
from packages.rag_core.vector_store import QdrantVectorStore, LocalJsonVectorStore, ResilientVectorStore
from packages.rag_core.service import RagService
from packages.tool_core.mutation_tools import MutationToolExecutor
from packages.tool_core.path_guard import PathPolicyError, WorkspacePathGuard


def record_root(tmp_path, monkeypatch):
    root = tmp_path/'karte'
    content = root/'content'
    content.mkdir(parents=True)
    source = content/'renamed.md'
    source.write_text('---\ndoc_id: private-record\n---\nprivate fixture body')
    ledger = root/'.mdsys/ephy/records/v2/ledger.json'
    ledger.parent.mkdir(parents=True)
    ledger.write_text(json.dumps({'docs':{'private-record':{'path':'content/renamed.md'}}}))
    monkeypatch.setenv('KARTE_DATA_DIR',str(root))
    return root,source,ledger


def chunk(path, original=None, identity='record'):
    return IndexedChunk(chunk_id=identity,source_path=str(path),original_source_path=str(original) if original else None,
        doc_id='private-record' if identity=='record' else None,chunk_text='fixture body without metadata',hash='fixture',embedding=[1.0])


def test_legacy_reader_blocks_renamed_record_even_without_metadata(tmp_path,monkeypatch):
    root,source,_ = record_root(tmp_path,monkeypatch)
    public=root/'content/public.md'
    public.write_text('---\ndoc_id: public-note\n---\nordinary note')
    adapter=KarteSourceAdapter(root)
    assert [doc.doc_id for doc in adapter.scan().documents]==['public-note']
    with pytest.raises(ValueError,match='policy_reader_required'):
        adapter.read_document('content/renamed.md')
    source.unlink()
    assert protected_path(source,doc_id='private-record')


def test_existing_copy_and_cached_body_are_purged_but_source_is_preserved(tmp_path,monkeypatch):
    _,source,_ = record_root(tmp_path,monkeypatch)
    managed=tmp_path/'LW_data';managed.mkdir()
    monkeypatch.setenv('LW_DATA_ROOT',str(managed))
    copied=managed/'renamed-copy.md';copied.write_bytes(source.read_bytes())
    public=tmp_path/'normal.txt';public.write_text('ordinary')
    store=JsonChunkStore(tmp_path/'index.json')
    # Seed a pre-migration cache，bypassing the new write guard deliberately．
    store._path.write_text(json.dumps([chunk(copied,source).model_dump(),chunk(public,identity='public').model_dump()]))
    before=source.read_bytes()
    assert [item.chunk_id for item in store.load()]==['public']
    assert not copied.exists() and source.read_bytes()==before
    assert 'private-record' not in store._path.read_text()
    assert [item.chunk_id for item in store.load()]==['public']


def test_unknown_ownership_blocks_without_deleting_unrelated_cache(tmp_path,monkeypatch):
    _,_,ledger=record_root(tmp_path,monkeypatch)
    ledger.write_text('{invalid')
    source=tmp_path/'ordinary.txt';source.write_text('ordinary')
    store=JsonChunkStore(tmp_path/'index.json')
    store._path.write_text(json.dumps([chunk(source,identity='public').model_dump()]))
    before=store._path.read_bytes()
    with pytest.raises(ValueError,match='karte_ownership_unavailable'):
        store.load()
    assert source.exists() and store._path.read_bytes()==before


def test_qdrant_query_purges_blocked_point_before_returning_payload(tmp_path,monkeypatch):
    _,source,_=record_root(tmp_path,monkeypatch)
    public=tmp_path/'ordinary.txt';public.write_text('ordinary')
    deleted=[]
    def respond(request):
        if request.url.path.endswith('/points/query'):
            return httpx.Response(200,json={'result':{'points':[
                {'id':'private-point','score':1,'payload':chunk(source).model_dump()},
                {'id':'public-point','score':.5,'payload':chunk(public,identity='public').model_dump()}]}})
        assert request.url.path.endswith('/points/delete')
        deleted.extend(json.loads(request.content)['points'])
        return httpx.Response(200,json={'result':{}})
    store=QdrantVectorStore(VectorDBConfig(provider='qdrant',collection='fixture'),httpx.Client(transport=httpx.MockTransport(respond)))
    result=store.search([1.0],None,None,[],5)
    assert [item.chunk_id for item in result]==['public']
    assert deleted==['private-point']


def test_model_file_tools_cannot_read_control_or_recording_paths(tmp_path,monkeypatch):
    root,source,_=record_root(tmp_path,monkeypatch)
    private=tmp_path/'delivery';private.mkdir();(private/'producer.json').write_text('fixture credential')
    monkeypatch.setenv('EPHY_RECORDING_HOME',str(private))
    guard=WorkspacePathGuard(str(tmp_path),(str(tmp_path),))
    for path in [source,root,private/'producer.json']:
        with pytest.raises(PathPolicyError,match='Karte'):
            guard.resolve(str(path))


@pytest.mark.parametrize('state', [{'docs':None}, {'docs':{'bad':{'path':'../../elsewhere'}}}])
def test_invalid_ledger_shape_does_not_delete_unrelated_data(tmp_path,monkeypatch,state):
    _,_,ledger=record_root(tmp_path,monkeypatch)
    ledger.write_text(json.dumps(state))
    source=tmp_path/'ordinary.md';source.write_text('ordinary')
    store=JsonChunkStore(tmp_path/'index.json')
    store._path.write_text(json.dumps([chunk(source,identity='public').model_dump()]))
    before=store._path.read_bytes()
    with pytest.raises(ValueError,match='karte_ownership_unavailable'):
        store.load()
    assert source.exists() and store._path.read_bytes()==before


def test_ingest_does_not_copy_new_v2_records_or_private_queue(tmp_path,monkeypatch):
    root,source,_=record_root(tmp_path,monkeypatch)
    queue=tmp_path/'queue';queue.mkdir();body=queue/'turn.json';body.write_text('private queue fixture')
    monkeypatch.setenv('EPHY_RECORDING_HOME',str(queue))
    service=object.__new__(RagService)
    assert not service._is_supported(source)
    assert not service._is_supported(body)
    assert service._iter_files(root,recursive=True)==[]


def test_qdrant_scroll_and_inactive_fallback_are_both_sanitized(tmp_path,monkeypatch):
    _,source,_=record_root(tmp_path,monkeypatch)
    public=tmp_path/'ordinary.txt';public.write_text('ordinary')
    deleted=[]
    def respond(request):
        if request.method=='GET':
            return httpx.Response(200,json={'result':{}})
        if request.url.path.endswith('/points/scroll'):
            return httpx.Response(200,json={'result':{'points':[
                {'id':'private-point','payload':chunk(source).model_dump()},
                {'id':'public-point','payload':chunk(public,identity='public').model_dump()}], 'next_page_offset':None}})
        assert request.url.path.endswith('/points/delete')
        deleted.extend(json.loads(request.content)['points'])
        return httpx.Response(200,json={'result':{}})
    primary=QdrantVectorStore(VectorDBConfig(provider='qdrant',collection='fixture'),httpx.Client(transport=httpx.MockTransport(respond)))
    cache=JsonChunkStore(tmp_path/'index.json')
    cache._path.write_text(json.dumps([chunk(source).model_dump()]))
    store=ResilientVectorStore(primary,LocalJsonVectorStore(cache))
    store.sanitize_protected()
    assert deleted==['private-point'] and cache.load()==[]
    assert [item.chunk_id for item in store.load_chunks()]==['public']


def test_setup_does_not_claim_success_if_primary_is_unavailable(tmp_path):
    def unavailable(request):
        raise httpx.ConnectError('offline',request=request)
    primary=QdrantVectorStore(VectorDBConfig(provider='qdrant',collection='fixture'),httpx.Client(transport=httpx.MockTransport(unavailable)))
    store=ResilientVectorStore(primary,LocalJsonVectorStore(JsonChunkStore(tmp_path/'index.json')))
    with pytest.raises(httpx.ConnectError):
        store.sanitize_protected()


def test_process_sandbox_denies_record_and_credential_roots_even_inside_workspace(tmp_path,monkeypatch):
    root,_,_=record_root(tmp_path,monkeypatch)
    queue=tmp_path/'private-spool';monkeypatch.setenv('EPHY_RECORDING_HOME',str(queue))
    profile=MutationToolExecutor._sandbox_profile(tmp_path)
    for path in (root,queue):
        assert f'(deny file-read* file-write* (subpath "{path}"))' in profile
        assert profile.index(f'(subpath "{path}")') > profile.index(f'(allow file-read* (subpath "{tmp_path}")')


def test_schema_documentation_is_not_mistaken_for_a_private_record():
    assert not has_record_marker('Document the "runtime_record": field．\n```yaml\nruntime_record:\n```')
    assert has_record_marker('---\nruntime_record:\n  schema_version: "2.0"\n---\nbody')
    assert has_record_marker('{"runtime_record":{"schema_version":"2.0"},"text":"body"}')


@pytest.mark.skipif(not Path('/usr/bin/sandbox-exec').is_file(), reason='macOS sandbox is required')
def test_native_process_cannot_read_recording_credential_inside_workspace(tmp_path,monkeypatch):
    private=tmp_path/'private-spool';private.mkdir()
    credential=private/'producer.json';credential.write_text('synthetic-private-value')
    monkeypatch.setenv('EPHY_RECORDING_HOME',str(private))
    profile=MutationToolExecutor._sandbox_profile(tmp_path)
    ready=subprocess.run(['/usr/bin/sandbox-exec','-p',profile,'/bin/echo','ready'],capture_output=True,text=True)
    assert ready.returncode==0, ready.stderr
    result=subprocess.run(['/usr/bin/sandbox-exec','-p',profile,'/bin/cat',str(credential)],capture_output=True,text=True)
    assert result.returncode!=0 and 'synthetic-private-value' not in result.stdout


def test_recording_prepare_requires_both_current_client_and_cache_guard(tmp_path):
    from types import SimpleNamespace
    from fastapi.testclient import TestClient
    from apps.gateway.main import app
    calls=[]
    class Store:
        failed=False
        def sanitize_protected(self):
            calls.append('checked')
            if self.failed:
                raise RuntimeError('unavailable')
    with TestClient(app) as client:
        original_client=app.state.karte_context_client
        original_store=app.state.rag_service._vector_store
        store=Store()
        try:
            app.state.karte_context_client=SimpleNamespace(data_root=tmp_path)
            app.state.rag_service._vector_store=store
            result=client.post('/v1/karte/records/prepare')
            assert result.status_code==200 and result.json()=={'safe':True,'safety_version':1,'data_root':str(tmp_path)}
            store.failed=True
            assert client.post('/v1/karte/records/prepare').status_code==503
            app.state.karte_context_client=None
            assert client.post('/v1/karte/records/prepare').status_code==503
            assert calls==['checked','checked']
        finally:
            app.state.karte_context_client=original_client
            app.state.rag_service._vector_store=original_store
