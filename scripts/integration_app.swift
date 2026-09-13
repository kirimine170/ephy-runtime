import AppKit
import Foundation

let args = CommandLine.arguments
guard args.count == 3 else { exit(64) }
switch args[1] {
case "open":
    let configuration = NSWorkspace.OpenConfiguration()
    configuration.createsNewApplicationInstance = true
    let keys: Set<String> = [
        "PATH", "HOME", "TMPDIR", "LANG", "PYTHONPATH", "KARTE_DATA_DIR", "KARTE_LOG_DIR",
        "EPHY_RUNTIME_ROOT", "EPHY_LOG_DIR", "EPHY_PREFERENCE_DATA_ROOT",
        "EPHY_ASR_PROVIDER", "EPHY_ASR_CONFIG",
        "EPHY_TTS_BEARER_TOKEN", "EPHY_TTS_ENDPOINT", "EPHY_RECORDING_HOME",
        "EPHY_KARTE_CONFIG_ROOT", "EPHY_KARTE_CONTROL"
        , "EPHY_RESIDENT", "EPHY_RESIDENT_ENABLED", "EPHY_RESIDENT_NAMESPACE",
        "EPHY_RESIDENT_STATE_ROOT", "EPHY_RESIDENT_INSTANCE_ID", "EPHY_GATEWAY_URL",
        "EPHY_RESIDENT_VOICE_PROFILE_ID"
    ]
    configuration.environment = ProcessInfo.processInfo.environment.filter { keys.contains($0.key) }
    NSWorkspace.shared.openApplication(at: URL(fileURLWithPath: args[2]), configuration: configuration) { app, error in
        guard error == nil, let app = app else { exit(1) }
        print("{\"pid\":\(app.processIdentifier)}")
        exit(0)
    }
    DispatchQueue.global().asyncAfter(deadline: .now() + 20) { exit(2) }
    RunLoop.main.run()
case "quit", "activate":
    guard let pid = Int32(args[2]), let app = NSRunningApplication(processIdentifier: pid) else { exit(0) }
    if args[1] == "quit" {
        exit(app.terminate() ? 0 : 1)
    }
    exit(app.activate(options: [.activateIgnoringOtherApps]) ? 0 : 1)
default:
    exit(64)
}
