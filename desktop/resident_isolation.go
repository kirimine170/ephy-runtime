package main

import (
	"errors"
	"os"
	"path/filepath"
)

// The dedicated artifact must never fall back to the normal app when opened
// directly from Finder without its isolated launch configuration．
var residentBuildMode string

func validateResidentLaunch(buildMode string) error {
	if buildMode != "isolated" {
		return nil
	}
	if os.Getenv("EPHY_RESIDENT") != "1" || os.Getenv("EPHY_RESIDENT_ENABLED") != "1" ||
		os.Getenv("EPHY_RESIDENT_NAMESPACE") != "resident-feedback" ||
		!filepath.IsAbs(os.Getenv("EPHY_RESIDENT_STATE_ROOT")) ||
		!filepath.IsAbs(os.Getenv("EPHY_RUNTIME_ROOT")) ||
		os.Getenv("EPHY_GATEWAY_URL") != "http://127.0.0.1:18900" {
		return errors.New("専用アプリは scripts/resident_app.py start から起動してください．通常版の設定へは接続しません．")
	}
	return nil
}

// Model servers are read-only shared resources in the isolated resident build．
// Service lifecycle belongs to its launcher，never the normal stack manager．
func residentServiceGuard() error {
	if os.Getenv("EPHY_RESIDENT") == "1" && !(residentBuildMode != "isolated" &&
		os.Getenv("EPHY_RESIDENT_MANAGED_STACK") == "1" &&
		os.Getenv("EPHY_GATEWAY_URL") == "http://127.0.0.1:8000") {
		return errors.New("常駐検証版のサービスは専用launcherで管理します．共有モデルの切替・起動・停止は通常版で行ってください．")
	}
	return nil
}
