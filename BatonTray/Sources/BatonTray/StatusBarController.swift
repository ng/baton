import AppKit

class StatusBarController: NSObject, NSMenuDelegate {
    private let statusItem: NSStatusItem
    private let daemon = DaemonManager()
    private let state = BatonState()
    private var dropView: DropTargetView?
    private var monitorTimer: Timer?
    private var monitorOnly = false

    override init() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        super.init()

        if let button = statusItem.button {
            button.image = NSImage(systemSymbolName: "bolt.fill", accessibilityDescription: "baton")

            let drop = DropTargetView(frame: button.bounds)
            drop.autoresizingMask = [.width, .height]
            drop.onDrop = { [weak self] urls in
                self?.handleFileDrop(urls)
            }
            button.addSubview(drop)
            self.dropView = drop
        }

        let menu = NSMenu()
        menu.delegate = self
        statusItem.menu = menu

        daemon.onEvent = { [weak self] event in
            self?.handleEvent(event)
        }
        daemon.onExit = { [weak self] status in
            self?.handleDaemonExit(status)
        }

        // Defer connection until after run loop starts
        DispatchQueue.main.async { [weak self] in
            self?.startConnection()
        }
    }

    private func startConnection() {
        if isExistingConnection() {
            monitorOnly = true
            startMonitorPolling()
        } else {
            let host = resolveHost()
            let preset = resolvePreset()
            if let host = host {
                daemon.start(host: host, preset: preset)
            }
        }
    }

    private func isExistingConnection() -> Bool {
        guard FileManager.default.fileExists(atPath: "/tmp/baton.sock") else { return false }
        guard let host = resolveHost(), !host.isEmpty else { return false }

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = ["-S", "/tmp/baton.sock", "-O", "check", host]
        proc.standardOutput = FileHandle.nullDevice
        proc.standardError = FileHandle.nullDevice
        do {
            try proc.run()
            proc.waitUntilExit()
            return proc.terminationStatus == 0
        } catch {
            return false
        }
    }

    private func startMonitorPolling() {
        pollStateFiles()
        monitorTimer = Timer.scheduledTimer(withTimeInterval: 5, repeats: true) { [weak self] _ in
            self?.pollStateFiles()
        }
    }

    private func pollStateFiles() {
        let hostFile = "/tmp/baton.sock.host"
        let portsFile = "/tmp/baton.sock.ports"

        if let hostData = try? String(contentsOfFile: hostFile, encoding: .utf8) {
            state.host = hostData.trimmingCharacters(in: .whitespacesAndNewlines)
        }

        DispatchQueue.global().async { [weak self] in
            guard let self = self else { return }
            let host = self.state.host
            guard !host.isEmpty else { return }

            let proc = Process()
            proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
            proc.arguments = ["-S", "/tmp/baton.sock", "-O", "check", host]
            proc.standardOutput = FileHandle.nullDevice
            proc.standardError = FileHandle.nullDevice
            do {
                try proc.run()
                proc.waitUntilExit()
                let alive = proc.terminationStatus == 0

                DispatchQueue.main.async {
                    self.state.isConnected = alive
                    self.updateIcon()
                }
            } catch {
                DispatchQueue.main.async {
                    self.state.isConnected = false
                    self.updateIcon()
                }
            }
        }

        if let portsData = try? String(contentsOfFile: portsFile, encoding: .utf8) {
            let ports = portsData.components(separatedBy: "\n")
                .compactMap { Int($0.trimmingCharacters(in: .whitespacesAndNewlines)) }
            state.localForwards = ports.sorted().map {
                PortEntry(port: $0, process: "", label: "", pinned: false, stale: false)
            }
        }
    }

    private func handleEvent(_ event: DaemonEvent) {
        state.apply(event: event)
        updateIcon()

        if event.type == "transfer_done" {
            let name = event.filename ?? "file"
            if let error = event.error {
                sendNotification(title: "Upload failed", body: "\(name): \(error)")
            } else {
                let remote = event.remotePath ?? ""
                sendNotification(title: "Uploaded \(name)", body: remote)
                if let path = event.remotePath {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(path, forType: .string)
                }
            }
        }
    }

    private func handleDaemonExit(_ status: Int32) {
        state.isConnected = false
        updateIcon()
    }

    private func handleFileDrop(_ urls: [URL]) {
        for url in urls {
            if monitorOnly {
                DispatchQueue.global().async { [weak self] in
                    let proc = Process()
                    proc.executableURL = URL(fileURLWithPath: "/usr/bin/env")
                    proc.arguments = ["baton", "send", url.path]
                    let pipe = Pipe()
                    proc.standardOutput = pipe
                    proc.standardError = FileHandle.nullDevice
                    do {
                        try proc.run()
                        proc.waitUntilExit()
                        let output = String(data: pipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)?
                            .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                        DispatchQueue.main.async {
                            if proc.terminationStatus == 0 {
                                self?.sendNotification(title: "Uploaded \(url.lastPathComponent)", body: output)
                                NSPasteboard.general.clearContents()
                                NSPasteboard.general.setString(output, forType: .string)
                            } else {
                                self?.sendNotification(title: "Upload failed", body: url.lastPathComponent)
                            }
                        }
                    } catch {
                        DispatchQueue.main.async {
                            self?.sendNotification(title: "Upload failed", body: url.lastPathComponent)
                        }
                    }
                }
            } else {
                daemon.sendFile(path: url.path)
            }
        }
    }

    private func updateIcon() {
        let symbolName = state.isConnected ? "link" : "bolt.fill"
        statusItem.button?.image = NSImage(
            systemSymbolName: symbolName,
            accessibilityDescription: "baton"
        )
    }

    // MARK: - NSMenuDelegate

    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()
        buildMenu(menu)
    }

    private func buildMenu(_ menu: NSMenu) {
        if state.isConnected {
            var statusText = "● Connected: \(state.host)"
            if let since = state.connectedSince {
                statusText += " (\(formatDuration(since)))"
            }
            addDisabledItem(menu, statusText)

            if state.uploadRate > 0 || state.downloadRate > 0 {
                let traffic = "  ↑ \(formatBytes(state.uploadRate))/s  ↓ \(formatBytes(state.downloadRate))/s"
                addDisabledItem(menu, traffic)
            }
        } else {
            addDisabledItem(menu, "○ Disconnected")
        }
        menu.addItem(.separator())

        if !state.localForwards.isEmpty {
            addDisabledItem(menu, "Forwarded Ports")
            for p in state.localForwards {
                var line = "  :\(p.port)"
                let desc = !p.label.isEmpty ? p.label : (!p.process.isEmpty ? p.process : "")
                if !desc.isEmpty { line += "  \(desc)" }
                let total = p.upload + p.download
                if total > 0 { line += "  \(formatBytes(total))" }
                if p.pinned { line += " 📌" }
                if p.stale { line += " ⊘" }
                addDisabledItem(menu, line)
            }
        }

        let forwardItem = NSMenuItem(title: "Forward Port...", action: #selector(forwardPort), keyEquivalent: "")
        forwardItem.target = self
        forwardItem.isEnabled = state.isConnected
        menu.addItem(forwardItem)
        menu.addItem(.separator())

        if !state.reverseTunnels.isEmpty {
            addDisabledItem(menu, "Reverse Tunnels")
            for p in state.reverseTunnels {
                var line = "  :\(p.port)"
                let desc = !p.label.isEmpty ? p.label : (!p.process.isEmpty ? p.process : "")
                if !desc.isEmpty { line += "  \(desc)" }
                addDisabledItem(menu, line)
            }
            menu.addItem(.separator())
        }

        let sendItem = NSMenuItem(title: "Send File...", action: #selector(sendFile), keyEquivalent: "")
        sendItem.target = self
        sendItem.isEnabled = state.isConnected
        menu.addItem(sendItem)

        addDisabledItem(menu, "Drop files on icon to upload")
        menu.addItem(.separator())

        if !state.recentTransfers.isEmpty {
            addDisabledItem(menu, "Recent Transfers")
            for t in state.recentTransfers.prefix(3) {
                let line = t.error != nil
                    ? "  ✕ \(t.filename): \(t.error!)"
                    : "  \(t.filename) → \(t.remotePath)"
                addDisabledItem(menu, line)
            }
            menu.addItem(.separator())
        }

        let reconnItem = NSMenuItem(
            title: "Auto-reconnect",
            action: #selector(toggleReconnect),
            keyEquivalent: ""
        )
        reconnItem.target = self
        reconnItem.state = state.autoReconnect ? .on : .off
        menu.addItem(reconnItem)

        if state.isConnected {
            let disconnItem = NSMenuItem(title: "Disconnect", action: #selector(disconnect), keyEquivalent: "")
            disconnItem.target = self
            menu.addItem(disconnItem)
        } else if !monitorOnly {
            let connItem = NSMenuItem(title: "Connect...", action: #selector(reconnect), keyEquivalent: "")
            connItem.target = self
            menu.addItem(connItem)
        }

        menu.addItem(.separator())
        let quitItem = NSMenuItem(title: "Quit", action: #selector(quit), keyEquivalent: "q")
        quitItem.target = self
        menu.addItem(quitItem)
    }

    // MARK: - Actions

    @objc private func sendFile() {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = true
        panel.canChooseDirectories = false
        if panel.runModal() == .OK {
            handleFileDrop(panel.urls)
        }
    }

    @objc private func forwardPort() {
        let alert = NSAlert()
        alert.messageText = "Forward Port"
        alert.informativeText = "Enter the remote port number to forward:"
        let input = NSTextField(frame: NSRect(x: 0, y: 0, width: 120, height: 24))
        input.placeholderString = "8080"
        alert.accessoryView = input
        alert.addButton(withTitle: "Forward")
        alert.addButton(withTitle: "Cancel")

        if alert.runModal() == .alertFirstButtonReturn {
            if let port = Int(input.stringValue), port > 0 {
                daemon.forwardPort(port)
            }
        }
    }

    @objc private func toggleReconnect() {
        daemon.toggleReconnect()
        state.autoReconnect.toggle()
    }

    @objc private func disconnect() {
        daemon.stop()
    }

    @objc private func reconnect() {
        let host = resolveHost()
        let preset = resolvePreset()
        if let host = host {
            daemon.start(host: host, preset: preset)
        }
    }

    @objc private func quit() {
        if daemon.isRunning {
            daemon.stop()
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
            NSApp.terminate(nil)
        }
    }

    // MARK: - Helpers

    private func addDisabledItem(_ menu: NSMenu, _ title: String) {
        let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        item.isEnabled = false
        menu.addItem(item)
    }

    private func resolveHost() -> String? {
        if !state.host.isEmpty { return state.host }
        if let data = try? String(contentsOfFile: "/tmp/baton.sock.host", encoding: .utf8) {
            return data.trimmingCharacters(in: .whitespacesAndNewlines)
        }
        return nil
    }

    private func resolvePreset() -> String? {
        return state.preset.isEmpty ? nil : state.preset
    }

    private func sendNotification(title: String, body: String) {
        let notification = NSUserNotification()
        notification.title = title
        notification.informativeText = body
        notification.soundName = NSUserNotificationDefaultSoundName
        NSUserNotificationCenter.default.deliver(notification)
    }

    private func formatDuration(_ since: Date) -> String {
        let interval = Date().timeIntervalSince(since)
        let hours = Int(interval) / 3600
        let minutes = (Int(interval) % 3600) / 60
        if hours > 0 {
            return "\(hours)h \(minutes)m"
        }
        return "\(minutes)m"
    }

    private func formatBytes(_ bytes: Double) -> String {
        if bytes < 1024 { return "\(Int(bytes)) B" }
        if bytes < 1024 * 1024 { return String(format: "%.1f KB", bytes / 1024) }
        return String(format: "%.1f MB", bytes / (1024 * 1024))
    }
}
