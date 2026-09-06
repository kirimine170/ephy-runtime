import AVFoundation
import Foundation
import Speech

// This helper never opens a microphone or stores audio．stdin is one mono PCM16 WAV．
func fail(_ code: String) -> Never {
    FileHandle.standardError.write(Data((code + "\n").utf8))
    exit(1)
}

let arguments = Array(CommandLine.arguments.dropFirst())
var localeIdentifier = "ja-JP"
var checkOnly = false
var argumentIndex = 0
while argumentIndex < arguments.count {
    switch arguments[argumentIndex] {
    case "--check":
        checkOnly = true
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
