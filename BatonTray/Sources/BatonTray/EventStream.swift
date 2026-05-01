import Foundation

struct DaemonEvent: Decodable {
    let type: String
    let time: Date?
    let connected: Bool?
    let host: String?
    let message: String?
    let port: Int?
    let process: String?
    let action: String?
    let label: String?
    let pinned: Bool?
    let filename: String?
    let remotePath: String?
    let size: Int64?
    let durationMs: Double?
    let error: String?
    let upload: Double?
    let download: Double?
    let ports: [PortInfoDTO]?
    let portTraffic: [String: PortTrafficDTO]?
    let preset: String?
    let inbox: String?
    let autoReconnect: Bool?

    enum CodingKeys: String, CodingKey {
        case type, time, connected, host, message, port, process, action, label, pinned
        case filename
        case remotePath = "remote_path"
        case size
        case durationMs = "duration_ms"
        case error, upload, download, ports
        case portTraffic = "port_traffic"
        case preset, inbox
        case autoReconnect = "auto_reconnect"
    }
}

struct PortInfoDTO: Decodable {
    let port: Int
    let process: String?
    let label: String?
    let pinned: Bool?
    let stale: Bool?

    enum CodingKeys: String, CodingKey {
        case port = "Port"
        case process = "Process"
        case label = "Label"
        case pinned = "Pinned"
        case stale = "Stale"
    }
}

struct PortTrafficDTO: Decodable {
    let port: Int
    let upload: Double
    let download: Double

    enum CodingKeys: String, CodingKey {
        case port = "Port"
        case upload = "Upload"
        case download = "Download"
    }
}

struct DaemonCommand: Encodable {
    let cmd: String
    var path: String?
    var port: Int?
}

class EventStreamParser {
    private let decoder: JSONDecoder

    init() {
        decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
    }

    func parse(line: String) -> DaemonEvent? {
        guard let data = line.data(using: .utf8) else { return nil }
        return try? decoder.decode(DaemonEvent.self, from: data)
    }
}
