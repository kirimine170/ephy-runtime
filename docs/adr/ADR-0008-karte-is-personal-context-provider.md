# Karte is the Personal Context provider

## Status

Accepted．実装中．

## Context

ADR-0007で，EphyはKarte canonical Markdownをread-onlyでindexし，reviewed outboxだけへproposalを書く境界を採用した．この構成はwrite safetyを確保したが，Personal Contextの検索・読取・privacy判断をEphyが所有してしまう．Karteを人間とEphyが共同操作するcontext systemにするには，read pathもKarteの責務に戻す必要がある．

## Decision

KarteをPersonal Context providerとし，EphyはKarte Context Protocolのversioned clientになる．Ephyはconversation，task／goal，execution，working memory，permission UXを所有し，Karteはdurable document identity，search／read，provenance，sensitivity，reviewed mutationを所有する．

V1 transportはatomic filesystem request／response spoolである．既存direct filesystem adapterとEphy `rag_core` indexはmigration fallbackとして維持するが，正式なPersonal Context sourceには`source_type=karte_context`を使う．Skillsはbehavior policy，MCPは将来のthin facadeであり，transportやcanonical storeにはしない．

## Consequences

- GatewayはKarte Context clientを注入し，Karte unavailableを通常会話の停止理由にしない．
- Personal Contextはgeneric local RAGやWeb sourceとsource type，trust，identityを分離する．
- promptへ渡すKarte contextはuntrusted dataとして隔離する．
- `doc_id`でreadし，pathを恒久identityにしない．
- Restricted dataはKarte policy Gateが完了するまで利用しない．
- Context contractはKarteを先に更新してからephy-runtimeへ同期する．

## Critical path

`Karte T-021 → Karte T-106 → Ephy T-116 → Ephy T-117 → Ephy T-110 → Ephy T-118`．

## Traceability

- Ephy parent：https://github.com/kirimine170/ephy-runtime/issues/43
- Client：https://github.com/kirimine170/ephy-runtime/issues/39
- Grounding：https://github.com/kirimine170/ephy-runtime/issues/40
- UI：https://github.com/kirimine170/ephy-runtime/issues/41
- E2E：https://github.com/kirimine170/ephy-runtime/issues/42
- Karte parent：https://github.com/kirimine170/Karte/issues/288

## Date

2026-09-01．
