# Identity Core

`identity_core`は，EphyのIdentity Manifestを読み込み，構造を検証し，更新前後のimmutable fieldを比較する．特定model，LoRA，prompt又はMemory storageには依存しない．

実際のManifestはrepositoryへ保存せず，`EPHY_PRIVATE_ROOT`と`EPHY_INSTANCE_ID`から解決したprivate instance directoryから読み込む．
