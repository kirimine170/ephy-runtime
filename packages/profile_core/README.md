# Profile Core

`profile_core`は，Ephy Profileを読み込み，会話modeに依存しない中核Profileから`ConversationPolicy`を解決する．Profile変更はIdentity変更ではなく，`instance_id`を変更しない．

prompt文字列は正本ではない．`prompt_core`が，この構造化Policyをmodel向けのprompt fragmentへ変換する．
