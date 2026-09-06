import AVFoundation
import Foundation
import Speech
import Darwin

// This helper never opens a microphone or stores audio．stdin is a mono PCM16 WAV
// or，with --stream，bounded NDJSON frames carrying transient PCM16 chunks．
func fail(_ code: String) -> Never {
    FileHandle.standardError.write(Data((code + "\n").utf8))
    exit(1)
}

let arguments = Array(CommandLine.arguments.dropFirst())
var localeIdentifier = "ja-JP"
var checkOnly = false
var streaming = false
var argumentIndex = 0
while argumentIndex < arguments.count {
    switch arguments[argumentIndex] {
    case "--check":
        checkOnly = true
    case "--stream":
        streaming = true
    case "--locale":
        argumentIndex += 1
        guard argumentIndex < arguments.count else { fail("invalid_voice_config") }
        localeIdentifier = arguments[argumentIndex]
    default:
        fail("invalid_voice_config")
    }
    argumentIndex += 1
}
guard localeIdentifier.range(of: "^[a-z]{2,3}[-_][A-Z]{2}$", options: .regularExpression) != nil else {
    fail("invalid_voice_config")
}
if checkOnly && streaming { fail("invalid_voice_config") }
guard let recognizer = SFSpeechRecognizer(locale: Locale(identifier: localeIdentifier)) else {
    fail("asr_unavailable")
}

func authorizationError(_ status: SFSpeechRecognizerAuthorizationStatus) -> String? {
    switch status {
    case .denied: return "asr_permission_denied"
    case .restricted: return "asr_permission_restricted"
    default: return nil
    }
}

// No requestAuthorization call is reachable from --check．Not-determined is allowed
// so the first explicit recording can request authorization during transcription．
if let code = authorizationError(SFSpeechRecognizer.authorizationStatus()) { fail(code) }
guard recognizer.supportsOnDeviceRecognition else { fail("asr_on_device_unavailable") }
guard recognizer.isAvailable else { fail("asr_unavailable") }
if checkOnly {
    print(SFSpeechRecognizer.authorizationStatus() == .notDetermined ? "permission_required" : "ready")
    exit(0)
}
if streaming { runStreamingRecognition(recognizer, localeIdentifier) }

let maximumBytes = 8 * 1024 * 1024
var input = Data()
while true {
    let piece = FileHandle.standardInput.readData(ofLength: min(65536, maximumBytes + 1 - input.count))
    if piece.isEmpty { break }
    input.append(piece)
    if input.count > maximumBytes { fail("invalid_audio") }
}
let bytes = [UInt8](input)
func u16(_ offset: Int) -> Int { Int(bytes[offset]) | Int(bytes[offset + 1]) << 8 }
func u32(_ offset: Int) -> Int {
    Int(bytes[offset]) | Int(bytes[offset + 1]) << 8 | Int(bytes[offset + 2]) << 16 | Int(bytes[offset + 3]) << 24
}
func chunkName(_ offset: Int) -> String { String(bytes: bytes[offset..<offset + 4], encoding: .ascii) ?? "" }
guard bytes.count >= 44, chunkName(0) == "RIFF", chunkName(8) == "WAVE", u32(4) + 8 == bytes.count else {
    fail("invalid_audio")
}
var sampleRate = 0
var audioRange: Range<Int>?
var foundFormat = false
var offset = 12
while offset < bytes.count {
    guard offset + 8 <= bytes.count else { fail("invalid_audio") }
    let size = u32(offset + 4)
    let start = offset + 8
    guard size <= bytes.count - start else { fail("invalid_audio") }
    switch chunkName(offset) {
    case "fmt ":
        guard !foundFormat, size >= 16 else { fail("invalid_audio") }
        sampleRate = u32(start + 4)
        guard u16(start) == 1, u16(start + 2) == 1, u16(start + 14) == 16,
              u16(start + 12) == 2, (8000...48000).contains(sampleRate),
              u32(start + 8) == sampleRate * 2 else { fail("invalid_audio") }
        foundFormat = true
    case "data":
        guard audioRange == nil, size > 0, size % 2 == 0 else { fail("invalid_audio") }
        audioRange = start..<start + size
    default: break
    }
    offset = start + size + size % 2
    guard offset <= bytes.count else { fail("invalid_audio") }
}
guard foundFormat, let audioRange, audioRange.count <= sampleRate * 2 * 60,
      let format = AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: Double(sampleRate), channels: 1, interleaved: false),
      let buffer = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: AVAudioFrameCount(audioRange.count / 2)),
      let samples = buffer.floatChannelData?[0] else { fail("invalid_audio") }
buffer.frameLength = AVAudioFrameCount(audioRange.count / 2)
for frame in 0..<Int(buffer.frameLength) {
    let value = UInt16(u16(audioRange.lowerBound + frame * 2))
    samples[frame] = Float(Int16(bitPattern: value)) / 32768.0
}
input.removeAll(keepingCapacity: false)

let request = SFSpeechAudioBufferRecognitionRequest()
request.requiresOnDeviceRecognition = true
request.shouldReportPartialResults = false
request.taskHint = .dictation

var finished = false
var transcript: String?
var failure: String?
var speechTask: SFSpeechRecognitionTask?
func beginRecognition() {
    guard recognizer.supportsOnDeviceRecognition else {
        failure = "asr_on_device_unavailable"
        finished = true
        return
    }
    speechTask = recognizer.recognitionTask(with: request) { result, error in
        DispatchQueue.main.async {
            guard !finished else { return }
            if let result, result.isFinal {
                let text = result.bestTranscription.formattedString.trimmingCharacters(in: .whitespacesAndNewlines)
                if text.isEmpty { failure = "asr_empty_transcript" } else { transcript = text }
                finished = true
            } else if error != nil {
                // Apple's localized error can contain details about input or filesystem paths．
                failure = "asr_failed"
                finished = true
            }
        }
    }
    request.append(buffer)
    request.endAudio()
}
if SFSpeechRecognizer.authorizationStatus() == .authorized {
    beginRecognition()
} else {
    SFSpeechRecognizer.requestAuthorization { status in
        DispatchQueue.main.async {
            guard !finished else { return }
            if status == .authorized {
                beginRecognition()
            } else {
                failure = authorizationError(status) ?? "asr_permission_denied"
                finished = true
            }
        }
    }
}

let deadline = Date(timeIntervalSinceNow: 40)
while !finished && Date() < deadline {
    RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.05))
}
if !finished {
    speechTask?.cancel()
    fail("asr_timeout")
}
if let failure { fail(failure) }
guard let transcript else { fail("asr_empty_transcript") }
// Only the final transcript is written to stdout．No partials，audio，or diagnostics．
print(transcript)

// Protocol is bounded NDJSON．Audio is mono signed PCM16 little endian，base64
// encoded per frame．All Speech work and output revisions are serialized on main．
struct StreamingInput: Decodable {
    let type: String
    let operation_id: String?
    let session_id: String?
    let turn_id: String?
    let segment_id: String?
    let sample_rate: Int?
    let sequence: Int?
    let pcm_base64: String?
}

struct StreamingOutput: Encodable {
    let operation_id: String
    let session_id: String
    let turn_id: String
    let segment_id: String
    let revision: Int
    let phase: String
    let transcript: String
    let stable_prefix: String
    let provider: String
    let model_revision: String
    let monotonic_ms: Int64
    let error_code: String
}

func appleSpeechRevision(_ locale: String) -> String {
    let os = ProcessInfo.processInfo.operatingSystemVersion
    var size = 0
    guard sysctlbyname("kern.osversion", nil, &size, nil, 0) == 0, size > 1, size < 128 else { fail("asr_unavailable") }
    var bytes = [CChar](repeating: 0, count: size)
    guard sysctlbyname("kern.osversion", &bytes, &size, nil, 0) == 0 else { fail("asr_unavailable") }
    let build = String(cString: bytes)
    guard build.range(of: "^[A-Za-z0-9._-]+$", options: .regularExpression) != nil else { fail("asr_unavailable") }
    // Apple does not expose the installed acoustic/language model revision．
    return "apple-opaque:macos-\(os.majorVersion).\(os.minorVersion).\(os.patchVersion)-build-\(build):\(locale)"
}

final class StreamingRecognition {
    let recognizer: SFSpeechRecognizer
    let modelRevision: String
    var start: StreamingInput?
    var request: SFSpeechAudioBufferRecognitionRequest?
    var task: SFSpeechRecognitionTask?
    var pendingAudio: [Data] = []
    var totalBytes = 0
    var sequence = 0
    var revision = 0
    var lastTranscript: String?
    var endRequested = false
    var finished = false
    var failed = false
    var startedNS = DispatchTime.now().uptimeNanoseconds
    var finishDeadlineNS: UInt64?

    init(_ recognizer: SFSpeechRecognizer, _ locale: String) {
        self.recognizer = recognizer
        modelRevision = appleSpeechRevision(locale)
    }

    func emit(_ phase: String, _ text: String = "", _ code: String = "") {
        guard !finished, let start, let operation = start.operation_id, let session = start.session_id,
              let turn = start.turn_id, let segment = start.segment_id else { return }
        revision += 1
        let output = StreamingOutput(operation_id: operation, session_id: session, turn_id: turn,
            segment_id: segment, revision: revision, phase: phase, transcript: text,
            stable_prefix: phase == "final" ? text : "", provider: "macos-speech",
            model_revision: modelRevision,
            monotonic_ms: Int64((DispatchTime.now().uptimeNanoseconds - startedNS) / 1_000_000), error_code: code)
        guard let json = try? JSONEncoder().encode(output), json.count < 96 * 1024 else { fail("asr_stream_invalid") }
        FileHandle.standardOutput.write(json + Data([10]))
    }

    func stop(_ code: String) {
        guard !finished else { return }
        emit(code == "asr_timeout" ? "timeout" : "failure", "", code)
        failed = true
        finished = true
        pendingAudio.removeAll(keepingCapacity: false)
        request?.endAudio()
        task?.cancel()
    }

    func append(_ data: Data) {
        guard let sampleRate = start?.sample_rate,
              let format = AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: Double(sampleRate), channels: 1, interleaved: false),
              let buffer = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: AVAudioFrameCount(data.count / 2)),
              let samples = buffer.floatChannelData?[0] else { stop("invalid_audio"); return }
        buffer.frameLength = AVAudioFrameCount(data.count / 2)
        data.withUnsafeBytes { (bytes: UnsafeRawBufferPointer) in
            for index in 0..<Int(buffer.frameLength) {
                let value = UInt16(bytes[index * 2]) | UInt16(bytes[index * 2 + 1]) << 8
                samples[index] = Float(Int16(bitPattern: value)) / 32768.0
            }
        }
        request?.append(buffer)
    }

    func begin() {
        guard !finished, task == nil, recognizer.supportsOnDeviceRecognition, recognizer.isAvailable else { stop("asr_on_device_unavailable"); return }
        let request = SFSpeechAudioBufferRecognitionRequest()
        request.requiresOnDeviceRecognition = true
        request.shouldReportPartialResults = true
        request.taskHint = .dictation
        self.request = request
        task = recognizer.recognitionTask(with: request) { [weak self] result, error in
            DispatchQueue.main.async {
                guard let self, !self.finished else { return }
                if error != nil { self.stop("asr_failed"); return }
                if let result {
                    let text = result.bestTranscription.formattedString.trimmingCharacters(in: .whitespacesAndNewlines)
                    guard text.utf8.count <= 16 * 1024 else { self.stop("asr_stream_invalid"); return }
                    if result.isFinal {
                        guard !text.isEmpty else { self.stop("asr_empty_transcript"); return }
                        self.emit("final", text)
                        self.finished = true
                        self.pendingAudio.removeAll(keepingCapacity: false)
                        self.request?.endAudio()
                        return
                    }
                    if text != self.lastTranscript {
                        guard self.revision < 2046 else { self.stop("asr_stream_invalid"); return }
                        // Apple partial hypotheses provide no guaranteed stable prefix．
                        self.emit("partial", text)
                        self.lastTranscript = text
                    }
                }
            }
        }
        for pcm in pendingAudio { append(pcm) }
        pendingAudio.removeAll(keepingCapacity: false)
        if endRequested { request.endAudio() }
    }

    func consume(_ frame: Data) {
        guard !finished else { return }
        guard frame.count <= 96 * 1024, String(data: frame, encoding: .utf8) != nil,
              let input = try? JSONDecoder().decode(StreamingInput.self, from: frame) else { stop("asr_stream_invalid"); return }
        if input.type == "start" {
            guard start == nil, let rate = input.sample_rate, (8000...48000).contains(rate),
                  [input.operation_id, input.session_id, input.turn_id, input.segment_id].allSatisfy({
                    $0?.range(of: "^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,127}$", options: .regularExpression) != nil
                  }) else { stop("asr_stream_invalid"); return }
            start = input
            startedNS = DispatchTime.now().uptimeNanoseconds
            if SFSpeechRecognizer.authorizationStatus() == .authorized { begin() }
            else {
                SFSpeechRecognizer.requestAuthorization { [weak self] status in
                    DispatchQueue.main.async {
                        guard let self, !self.finished else { return }
                        if status == .authorized { self.begin() }
                        else { self.stop(authorizationError(status) ?? "asr_permission_denied") }
                    }
                }
            }
            return
        }
        guard let start else { stop("asr_stream_invalid"); return }
        if input.type == "audio" {
            guard !endRequested, input.sequence == sequence + 1, let encoded = input.pcm_base64,
                  encoded.utf8.count <= 88 * 1024, let pcm = Data(base64Encoded: encoded),
                  !pcm.isEmpty, pcm.count <= 64 * 1024, pcm.count % 2 == 0,
                  totalBytes + pcm.count <= min(8 * 1024 * 1024, (start.sample_rate ?? 0) * 2 * 60) else { stop("invalid_audio"); return }
            sequence += 1
            totalBytes += pcm.count
            if task == nil { pendingAudio.append(pcm) } else { append(pcm) }
        } else if input.type == "finish" {
            guard !endRequested else { stop("asr_stream_invalid"); return }
            endRequested = true
            finishDeadlineNS = DispatchTime.now().uptimeNanoseconds + 15_000_000_000
            if task != nil { request?.endAudio() }
        } else { stop("asr_stream_invalid") }
    }
}

func runStreamingRecognition(_ recognizer: SFSpeechRecognizer, _ locale: String) -> Never {
    let session = StreamingRecognition(recognizer, locale)
    DispatchQueue.global(qos: .userInitiated).async {
        var pending = Data()
        var inputBytes = [UInt8](repeating: 0, count: 16 * 1024)
        while true {
            // POSIX read returns available pipe bytes immediately，including a
            // small PCM frame．Do not wait to fill a Foundation read buffer．
            let count = Darwin.read(STDIN_FILENO, &inputBytes, inputBytes.count)
            if count < 0 {
                if errno == EINTR { continue }
                DispatchQueue.main.sync { session.stop("asr_stream_eof") }
                return
            }
            if count == 0 {
                DispatchQueue.main.sync { if !session.finished && (!session.endRequested || !pending.isEmpty) { session.stop("asr_stream_eof") } }
                return
            }
            pending.append(contentsOf: inputBytes[..<count])
            while let boundary = pending.firstIndex(of: 10) {
                let frame = Data(pending[..<boundary])
                pending.removeSubrange(...boundary)
                DispatchQueue.main.sync { session.consume(frame) }
            }
            if pending.count > 96 * 1024 {
                DispatchQueue.main.sync { session.stop("asr_stream_invalid") }
                return
            }
        }
    }
    let maximum = DispatchTime.now().uptimeNanoseconds + 115_000_000_000
    while !session.finished {
        let now = DispatchTime.now().uptimeNanoseconds
        if now >= maximum || (session.finishDeadlineNS != nil && now >= session.finishDeadlineNS!) { session.stop("asr_timeout"); break }
        RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.01))
    }
    exit(session.failed ? 1 : 0)
}
