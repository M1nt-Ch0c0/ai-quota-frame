import SwiftUI
import AppKit

enum GuideTheme {
    static let blue = Color(red: 0.18, green: 0.39, blue: 0.67)
    static let ink = Color(red: 0.12, green: 0.20, blue: 0.30)
    static let muted = Color(red: 0.34, green: 0.39, blue: 0.45)
    static let line = Color(red: 0.80, green: 0.84, blue: 0.89)
}
enum GuideSection: String, CaseIterable, Identifiable {
    case map = "架构地图", hardware = "硬件与驱动", api = "应用接口", develop = "开发实践", ideas = "创意工坊"
    var id: Self { self }
}
struct GuideSource: Identifiable {
    let title: String; let url: URL
    var id: String { url.absoluteString }
    static func host(_ title: String, _ path: String) -> Self { Self(title: title, url: URL(string: "https://github.com/M1nt-Ch0c0/photopainter-host/blob/b4709c0/" + path)!) }
    static func app(_ title: String, _ path: String) -> Self { Self(title: title, url: URL(string: "https://github.com/M1nt-Ch0c0/photoframe/blob/e910153/" + path)!) }
    static func pc(_ title: String, _ path: String) -> Self { Self(title: title, url: URL(string: "https://github.com/M1nt-Ch0c0/ai-quota-frame/blob/93bbe07/" + path)!) }
}
struct GuideSources: View {
    let sources: [GuideSource]
    var body: some View {
        ViewThatFits(in: .horizontal) { HStack(spacing: 18) { links }; VStack(alignment: .leading, spacing: 10) { links } }
            .font(.system(size: 12)).tint(GuideTheme.blue)
    }
    @ViewBuilder private var links: some View {
        ForEach(sources) { s in Link(destination: s.url) { Label(s.title, systemImage: "arrow.up.right.square") } }
    }
}
struct GuideHeading: View {
    let title: String; let detail: String
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title).font(.system(size: 22, weight: .semibold)).foregroundStyle(GuideTheme.ink)
            Text(detail).font(.system(size: 14)).foregroundStyle(GuideTheme.muted).lineSpacing(5).fixedSize(horizontal: false, vertical: true)
        }
    }
}
struct GuideParagraph: View {
    let title: String; let text: String
    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            Text(title).font(.system(size: 14, weight: .semibold)).foregroundStyle(GuideTheme.ink)
            Text(text).font(.system(size: 14)).foregroundStyle(GuideTheme.muted).lineSpacing(6).fixedSize(horizontal: false, vertical: true).textSelection(.enabled)
        }.frame(maxWidth: .infinity, alignment: .leading)
    }
}
struct GuideNote: View {
    let text: String
    var body: some View {
        Label { Text(text).font(.system(size: 13)).lineSpacing(5).fixedSize(horizontal: false, vertical: true) } icon: { Image(systemName: "info.circle").foregroundStyle(GuideTheme.blue) }
            .foregroundStyle(GuideTheme.muted).padding(15).frame(maxWidth: .infinity, alignment: .leading)
            .background(GuideTheme.blue.opacity(0.06), in: RoundedRectangle(cornerRadius: 10))
    }
}
struct GuideFact: Identifiable {
    let name: String; let value: String
    var id: String { name }
    init(_ name: String, _ value: String) { self.name = name; self.value = value }
}
struct GuideFacts: View {
    let rows: [GuideFact]
    var body: some View {
        Grid(alignment: .leading, horizontalSpacing: 22, verticalSpacing: 12) {
            ForEach(rows) { row in GridRow {
                Text(row.name).fontWeight(.medium).frame(minWidth: 95, alignment: .leading)
                Text(row.value).foregroundStyle(GuideTheme.muted).frame(maxWidth: .infinity, alignment: .leading).textSelection(.enabled)
            } }
        }.font(.system(size: 13)).lineSpacing(4)
    }
}
struct GuideCode: View {
    let title: String; let code: String
    @State private var copied = false
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text(title).font(.system(size: 13, weight: .semibold)); Spacer()
                Button(copied ? "已复制" : "复制代码", systemImage: copied ? "checkmark" : "doc.on.doc") {
                    NSPasteboard.general.clearContents(); NSPasteboard.general.setString(code, forType: .string); copied = true
                    Task { try? await Task.sleep(for: .seconds(2)); copied = false }
                }.controlSize(.small)
            }
            ScrollView([.horizontal, .vertical]) {
                Text(code).font(.system(size: 12, design: .monospaced)).lineSpacing(5).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading).padding(1)
            }.frame(height: min(390, CGFloat(code.components(separatedBy: "\n").count) * 20 + 6))
        }.padding(18).background(.white, in: RoundedRectangle(cornerRadius: 12)).overlay(RoundedRectangle(cornerRadius: 12).stroke(GuideTheme.line, lineWidth: 1))
    }
}
struct GuideChoice: View {
    let title: String; let subtitle: String; let selected: Bool; let action: () -> Void
    var body: some View {
        Button(action: action) {
            HStack(spacing: 10) {
                VStack(alignment: .leading, spacing: 5) {
                    Text(title).font(.system(size: 14, weight: selected ? .semibold : .medium))
                    if !subtitle.isEmpty { Text(subtitle).font(.system(size: 12)).foregroundStyle(GuideTheme.muted) }
                }.frame(maxWidth: .infinity, alignment: .leading)
                if selected { Image(systemName: "chevron.right").font(.system(size: 11, weight: .semibold)) }
            }.padding(12).frame(minHeight: 44).foregroundStyle(selected ? GuideTheme.blue : GuideTheme.ink)
                .background(selected ? GuideTheme.blue.opacity(0.10) : .clear, in: RoundedRectangle(cornerRadius: 8))
        }.buttonStyle(.plain).accessibilityValue(selected ? "已选择" : "")
    }
}
