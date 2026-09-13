package main

import (
	"errors"
	"os"
)

// Model servers are read-only shared resources in the isolated resident build．
// Service lifecycle belongs to its launcher，never the normal stack manager．
func residentServiceGuard() error {
	if os.Getenv("EPHY_RESIDENT") == "1" {
		return errors.New("常駐検証版のサービスは専用launcherで管理します．共有モデルの切替・起動・停止は通常版で行ってください．")
	}
	return nil
}
