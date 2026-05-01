import Foundation

class DaemonManager {
    private var process: Process?
    private var stdinPipe: Pipe?
    private let parser = EventStreamParser()
    private let encoder = JSONEncoder()
    var onEvent: ((DaemonEvent) -> Void)?
    var onExit: ((Int32) -> Void)?

    var isRunning: Bool { process?.isRunning ?? false }

    func start(host: String?, preset: String?) {
        let binary = findBatonBinary()

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: binary)

        var args = ["daemon"]
        if let host = host { args.append(host) }
        if let preset = preset {
            args.append("--preset")
            args.append(preset)
        }
        proc.arguments = args

        let stdout = Pipe()
        let stdin = Pipe()
        proc.standardOutput = stdout
        proc.standardInput = stdin
        proc.standardError = FileHandle.nullDevice

        self.stdinPipe = stdin
        self.process = proc

        proc.terminationHandler = { [weak self] p in
            DispatchQueue.main.async {
                self?.onExit?(p.terminationStatus)
            }
        }

        stdout.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard !data.isEmpty else {
                handle.readabilityHandler = nil
                return
            }
            guard let text = String(data: data, encoding: .utf8) else { return }
            for line in text.components(separatedBy: "\n") where !line.isEmpty {
                if let event = self?.parser.parse(line: line) {
                    DispatchQueue.main.async {
                        self?.onEvent?(event)
                    }
                }
            }
        }

        do {
            try proc.run()
        } catch {
            DispatchQueue.main.async {
                self.onExit?(-1)
            }
        }
    }

    func sendCommand(_ cmd: DaemonCommand) {
        guard let pipe = stdinPipe else { return }
        guard let data = try? encoder.encode(cmd) else { return }
        pipe.fileHandleForWriting.write(data)
        pipe.fileHandleForWriting.write("\n".data(using: .utf8)!)
    }

    func sendFile(path: String) {
        sendCommand(DaemonCommand(cmd: "send", path: path))
    }

    func forwardPort(_ port: Int) {
        sendCommand(DaemonCommand(cmd: "forward", port: port))
    }

    func toggleReconnect() {
        sendCommand(DaemonCommand(cmd: "reconnect_toggle"))
    }

    func requestStatus() {
        sendCommand(DaemonCommand(cmd: "status"))
    }

    func stop() {
        sendCommand(DaemonCommand(cmd: "quit"))
        DispatchQueue.global().asyncAfter(deadline: .now() + 2) { [weak self] in
            if self?.process?.isRunning == true {
                self?.process?.terminate()
            }
        }
    }

    private func findBatonBinary() -> String {
        // Look next to this app bundle first
        if let bundlePath = Bundle.main.executablePath {
            let dir = (bundlePath as NSString).deletingLastPathComponent
            let candidate = (dir as NSString).appendingPathComponent("baton")
            if FileManager.default.isExecutableFile(atPath: candidate) {
                return candidate
            }
            // Check Resources
            if let resourcePath = Bundle.main.resourcePath {
                let resCandidate = (resourcePath as NSString).appendingPathComponent("baton")
                if FileManager.default.isExecutableFile(atPath: resCandidate) {
                    return resCandidate
                }
            }
        }

        // Check common install locations
        for path in ["/usr/local/bin/baton", "\(NSHomeDirectory())/go/bin/baton"] {
            if FileManager.default.isExecutableFile(atPath: path) {
                return path
            }
        }

        // Fall back to PATH lookup
        return "baton"
    }
}
