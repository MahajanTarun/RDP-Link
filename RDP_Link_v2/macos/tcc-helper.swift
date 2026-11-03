// Small helper for the RDP Link setup assistant. It reports and requests the
// macOS privacy permissions the host/guest needs, and opens the right Settings
// pane. Compiled at packaging time and bundled with the app.
import Foundation
import CoreGraphics
import ApplicationServices
import AVFoundation
import AppKit

func statusJSON() -> String {
    let screen = CGPreflightScreenCaptureAccess()
    let ax = AXIsProcessTrusted()
    let cam = AVCaptureDevice.authorizationStatus(for: .video)
    let mic = AVCaptureDevice.authorizationStatus(for: .audio)
    func name(_ s: AVAuthorizationStatus) -> String {
        switch s {
        case .authorized: return "granted"
        case .denied: return "denied"
        case .restricted: return "restricted"
        case .notDetermined: return "unknown"
        @unknown default: return "unknown"
        }
    }
    return "{\"screen\":\(screen),\"accessibility\":\(ax),\"camera\":\"\(name(cam))\",\"microphone\":\"\(name(mic))\"}"
}

func openPane(_ anchor: String) {
    let url = "x-apple.systempreferences:com.apple.preference.security?\(anchor)"
    if let u = URL(string: url) { NSWorkspace.shared.open(u) }
}

let cmd = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "status"
switch cmd {
case "status":
    print(statusJSON())
case "request-screen":
    _ = CGRequestScreenCaptureAccess()
    print(statusJSON())
case "request-accessibility":
    let opts = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true]
    _ = AXIsProcessTrustedWithOptions(opts as CFDictionary)
    print(statusJSON())
case "request-camera":
    AVCaptureDevice.requestAccess(for: .video) { _ in print(statusJSON()); exit(0) }
    RunLoop.current.run()
case "request-microphone":
    AVCaptureDevice.requestAccess(for: .audio) { _ in print(statusJSON()); exit(0) }
    RunLoop.current.run()
case "open-screen":
    openPane("Privacy_ScreenCapture")
case "open-accessibility":
    openPane("Privacy_Accessibility")
case "open-camera":
    openPane("Privacy_Camera")
case "open-microphone":
    openPane("Privacy_Microphone")
default:
    print("unknown command")
    exit(2)
}
