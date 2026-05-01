import Foundation

struct PortEntry: Identifiable {
    var id: Int { port }
    let port: Int
    var process: String
    var label: String
    var pinned: Bool
    var stale: Bool
    var upload: Double = 0
    var download: Double = 0
}

struct TransferEntry {
    let filename: String
    let remotePath: String
    let size: Int64
    let time: Date
    let error: String?
}

class BatonState {
    var isConnected = false
    var host = ""
    var preset = ""
    var inbox = ""
    var connectedSince: Date?
    var autoReconnect = true
    var uploadRate: Double = 0
    var downloadRate: Double = 0
    var localForwards: [PortEntry] = []
    var reverseTunnels: [PortEntry] = []
    var recentTransfers: [TransferEntry] = []
    var lastMessage: String?

    func apply(event: DaemonEvent) {
        switch event.type {
        case "ready":
            isConnected = event.connected ?? true
            host = event.host ?? host
            preset = event.preset ?? preset
            inbox = event.inbox ?? inbox
            autoReconnect = event.autoReconnect ?? true
            connectedSince = Date()

        case "conn_event":
            if let connected = event.connected {
                let wasConnected = isConnected
                isConnected = connected
                if connected && !wasConnected {
                    connectedSince = Date()
                }
            }
            lastMessage = event.message
            if let ar = event.autoReconnect {
                autoReconnect = ar
            }

        case "conn_status":
            if let connected = event.connected {
                isConnected = connected
            }
            host = event.host ?? host
            preset = event.preset ?? preset
            if let ar = event.autoReconnect {
                autoReconnect = ar
            }
            if let ports = event.ports {
                localForwards = ports.map {
                    PortEntry(
                        port: $0.port,
                        process: $0.process ?? "",
                        label: $0.label ?? "",
                        pinned: $0.pinned ?? false,
                        stale: $0.stale ?? false
                    )
                }.sorted { $0.port < $1.port }
            }

        case "port_event":
            guard let port = event.port, let action = event.action else { break }
            if action == "forwarded" {
                if !localForwards.contains(where: { $0.port == port }) {
                    localForwards.append(PortEntry(
                        port: port,
                        process: event.process ?? "",
                        label: event.label ?? "",
                        pinned: event.pinned ?? false,
                        stale: false
                    ))
                    localForwards.sort { $0.port < $1.port }
                }
            } else if action == "removed" {
                localForwards.removeAll { $0.port == port }
            }

        case "transfer_done":
            let entry = TransferEntry(
                filename: event.filename ?? "unknown",
                remotePath: event.remotePath ?? "",
                size: event.size ?? 0,
                time: event.time ?? Date(),
                error: event.error
            )
            recentTransfers.insert(entry, at: 0)
            if recentTransfers.count > 5 {
                recentTransfers = Array(recentTransfers.prefix(5))
            }

        case "throughput":
            uploadRate = event.upload ?? 0
            downloadRate = event.download ?? 0

        case "port_traffic":
            if let traffic = event.portTraffic {
                for (key, info) in traffic {
                    guard let port = Int(key) else { continue }
                    if let idx = localForwards.firstIndex(where: { $0.port == port }) {
                        localForwards[idx].upload = info.upload
                        localForwards[idx].download = info.download
                    }
                }
            }

        default:
            break
        }
    }
}
