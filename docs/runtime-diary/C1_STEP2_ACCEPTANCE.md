# C1 Step 2の実機受入

人の発話・聴感による本手順は未実施である．新buildの継続会話UI表示までは確認したが，Macのロックにより初期化完了・声の選択状態は未確認である．合成テスト，Goのplayback ACK，WAV生成だけを聴感の合格にしない．対応するsource SHA／binary hashと検証状況は[STATUS](STATUS.md)を参照する．実会話の自動記録はOFFのままにする．

## 起動と版の確認

1．Macのロックを解除し，ヘッドホンを接続する．表示中のEphy Runtimeを通常終了する．
2．`recovery/ephy-runtime/c1-step2-20260912/launch-c1-step2-acceptance.command`を実行する．launcherがclean sourceのmanifest，署名後binary hash，署名，既存ASR helperのhashを照合する．single-instanceで旧appへ転送されないことを確認する．
3．launcherが今回のcompletion guidance修正を含むRuntime mainのsourceと既存Gatewayの所有processを照合し，Gatewayだけを再起動する．同じsourceで準備済みのprocessなら再起動を省く．前後healthとsource SHAを`gateway-restart-provenance.json`へ記録する．port 8000の所有者やsourceが異なる場合は停止し，別processを強制終了しない．モデルの選択，persona，voice，thinkingの設定は変更しない．

受入bundleは元Runtime checkoutの`desktop/build/c1-step2/ephy-runtime.app`にあり，既存の設定・helper・素材を参照する．再起動後の声の初期選択を受入前のprofile／styleと照合し，異なる場合は既存の選択へ戻す．profileが利用不能ならその状態を記録し，smokeで使った標準声へ暗黙に切り替えて合格にしない．

アプリのsourceとGatewayのsourceは別々に確認する．ASR helperは既存署名済みhelperを使用する．launchしたappのsourceを，元のmainや古いStep 1 bundleのSHAと取り違えない．

## 発話と聴感

「会話を開始」を一度だけ押し，次を同じsessionで試す．各確認は実際にマイクへ話す．

| 場面 | 操作と確認 |
|---|---|
| 通常の連続会話 | 「日本の四季を順に説明して」→回答を聞く→「はい」→次の回答まで進む．開始を押し直さない |
| 本文の途中 | 説明中に「いや，冬の話だけ聞きたい」と話す．音声がすぐ止まること，「いや」を含む冒頭が認識されること，冬に絞った次の回答が出ることを確認する |
| LLM待機・フィラー・TTS待機 | それぞれの表示／音声中に同じ割込みを行う．固定ACKやフィラーが発話へ重ならず，次のASRへ引き継がれることを確認する |
| 繰り返しと再開 | 次の回答にも2回割り込む．pause→resume→短い発話→終了を試し，停止後のマイクや旧音声が残らないことを確認する |
| Step 1回帰 | 300 ms／600 msの途中休止，「えっと」からの続き，短い「うん」「はい」を試す．partialを確定表示・保存しないこと，finalが二重にならないことを確認する |
| Work | 既存Workを選び，thinkingを有効のまま四季→本文割込み→冬の次回答を一周する．Fastのnon-thinking結果をWorkの受入にしない |

フィラーがそのturnで鳴らなければ，フィラー中の試験は「場面未発生」と記録する．任意のフィラーを承認済みと扱って追加・変更しない．2秒の引継ぎ上限を超えた場合はpause理由を確認し，再発話する．文字にできていない冒頭を復元済みと扱わない．

## 計測と記録

Developer表示のmetadata traceをturnごとに取得する．userの発話本文・生成本文・reasoning・WAVを受入logへ貼り付けず，source SHA，binary hash，model／voice ID，時刻，状態，次の区間を記録する．

- `local_stop`：発話検出から端末のmute／stopまで．Web Audio制御の値であり，耳で確認した停止時間そのものではない．
- `input_handoff_asr_ready`／`input_handoff_drained`：検出から次ASR準備完了／保持PCM排出完了まで．
- `speech_first_partial`，`speech_to_endpoint`，`asr_finalization`：ASRとendpointを分ける．引継ぎPCMの到着後のbackend clockと，検出側のclockを混同しない．
- `llm_ttft`，generationのfirst raw delta／first visible content，`tts_ttfc`，`first_audio`：thinking待ち・表示待ち・合成待ち・再生開始を分ける．取得できないreasoning token数を0にしない．
- 旧回答のgeneration completionと，unitごとの自然終了／中断／unknownを分ける．WAVの部分終了をSpeechUnit全体の完了にしない．

利用者の聴感・冒頭認識・冬への回答接続の確認とmetadataを照合できた項目だけを実機受入済みにする．speaker環境のecho耐性は本headphone受入から推定しない．
