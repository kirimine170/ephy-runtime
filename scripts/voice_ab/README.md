# C0.3.2 Voice Provider Blind A／B

このtoolは，許諾済みの同一参照WAV，同一発話本文，同一speech unit境界から，24件のprivate listening packageを作る．既存のDeveloper A／Bと同様に，評価中は候補A／Bだけを示し，provider identityと機械計測をprivate keyへ分離する．生成音声，参照音声，provider対応表，回答はGitへ追加しない．

## 評価条件

- corpusは短い相槌，通常会話，質問，感情表現，固有名詞，難読語，長い説明，文末の間，フィラー候補，連続turnを含む24件である．
- 両providerへneutral default，pace 1.0，volume 1.0を指定する．Irodori固有のaffect優位を持ち込まない．
- 最初の同一caseだけをcoldとし，残り23件を同じprocessのwarm条件とする．生成順と人間へ提示するrandomized順は分離する．
- A側へ割り当てるproviderは12件ずつに均等化する．公開manifestと音声filenameにはprovider identityを入れない．
- Qwen用のユーザー確認済み参照transcriptがある場合は，参照WAVと全文から作ったICL clone promptを評価専用に使う．正確なtranscriptがない場合だけ，公式のspeaker-embedding-only modeへfallbackする．

## 計測と境界

workerはwarm first-audio p50／p95，real-time factor，発話全体の完了時間，peak RSS，cancelからdisposable inference process停止まで，stale chunk件数，speech unitの欠落・重複・順序違反を記録する．長文検査はtransport上のspeech unit整合であり，音響をASRで再転写した語彙一致ではない．連続turnの声質変動にはlocal WavLM speaker embeddingの相対cosineを使うが，人間の聴感scoreや普遍的な合格閾値として扱わない．

評価UIは可愛さ，Ephyらしさ，参照声との一致，自然さ，感情の適切さ，日本語発音，長時間聞いた際の疲れにくさ，A／B総合選好を保存する．全回答が揃うまでunblindしない．Codex自身の聴感を回答へ加えない．

## 安全条件

このtoolはservice configをread-only inputとしてhash確認し，default profileを変更しない．LoRA，Speaker Inversion，merge，release，deployを実行しない．評価完了後の3分類と，その先の変更は別の明示承認を必要とする．
