# Agent Workspace UI

Ephy Desktopの日常画面は，local Agent Workspaceとして責務を3領域へ固定します．

- 左：新規会話，会話履歴，project，library，settingsへのnavigation．
- 中央：会話，実行状態，Karteへの提案review，入力composer．
- 右：source scope，Karte Personal Context，根拠一覧，文書preview．

1200px以下では右領域を`Context` drawerへ，900px以下では左navigationもviewport内でscrollできるdrawerへ切り替えます．drawerを開くと最初の操作対象へfocusし，`Escape`で閉じた後は起点buttonへfocusを戻します．`⌘K`／`Ctrl+K`はcomposer，`⌘B`／`Ctrl+B`はnavigation，`⌘.`／`Ctrl+.`はContextを操作します．送信中のthreadとpanelは`aria-busy`とし，現在の応答開始／完了／失敗だけを専用`chat-stream-announcement` status regionで通知します．履歴のerror cardは再描画時に再通知しない非live表示です．空の入力はcomposerのvalidationとして扱い，空の会話とsource previewには次の操作を示すempty stateを設定します．

通常modeでは会話，project，library，Karte Personal Contextだけを表示します．Runtime，Routing，Evaluation，Top K，Gateway URL，model，LoRA，詳細diagnosticは削除せず，SettingsのDeveloper Modeを有効にした場合だけ表示します．Developer Modeを無効にした状態でadvanced panelへ移動しようとした場合はSettingsへ戻します．

frontendは次のfeature moduleへ分けます．

- `workspaceChrome.js`：viewport境界，pane state，keyboard shortcut．
- `chatSources.js`：Karte／local／web source listとpreview．
- `karteConversation.js`：会話からKarteへ送るproposal review．
- `developerMode.js`と`modelManager.js`：通常modeと実験機能の境界．
- `chatRouting.js`，`webSearchFlow.js`，`ingestDrop.js`：各実行経路．

`npm test`はviewport，keyboard，empty，untrusted source，Developer Mode gateを回帰確認し，`npm run build`でWails向けbundleを検証します．実ブラウザでは3ペイン，800px drawer，focus復帰，keyboard shortcutを確認します．native appではDeveloper Mode off／onとWails bridgeを確認します．
