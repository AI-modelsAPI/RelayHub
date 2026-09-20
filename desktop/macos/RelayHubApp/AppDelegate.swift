import Cocoa
import WebKit

@main
class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem!
    private var window: NSWindow?
    private var webView: WKWebView?

    func applicationDidFinishLaunching(_ aNotification: Notification) {
        // Create Menu Bar Item
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        if let button = statusItem.button {
            button.title = "⚡ RelayHub"
        }
        setupMenu()

        // Create and show Main Desktop Window immediately
        setupWindow()
        showWindow()
    }

    private func setupMenu() {
        let menu = NSMenu()
        menu.addItem(NSMenuItem(title: "显示控制面板 (Show Dashboard)", action: #selector(showWindow), keyEquivalent: "d"))
        menu.addItem(NSMenuItem(title: "在浏览器中打开 (Open in Browser)", action: #selector(openExternalBrowser), keyEquivalent: "b"))
        menu.addItem(NSMenuItem.separator())
        menu.addItem(NSMenuItem(title: "刷新界面 (Reload)", action: #selector(reloadWebView), keyEquivalent: "r"))
        menu.addItem(NSMenuItem.separator())
        menu.addItem(NSMenuItem(title: "退出 RelayHub", action: #selector(quitApp), keyEquivalent: "q"))
        statusItem.menu = menu
    }

    private func setupWindow() {
        let rect = NSRect(x: 100, y: 100, width: 1000, height: 720)
        let styleMask: NSWindow.StyleMask = [.titled, .closable, .miniaturizable, .resizable]
        
        let win = NSWindow(contentRect: rect, styleMask: styleMask, backing: .buffered, defer: false)
        win.title = "RelayHub - 统一出口与自动签到聚合器"
        win.center()
        win.isReleasedWhenClosed = false

        let webConf = WKWebViewConfiguration()
        let wv = WKWebView(frame: rect, configuration: webConf)
        win.contentView = wv
        self.webView = wv
        self.window = win

        reloadWebView()
    }

    @objc private func showWindow() {
        if let win = window {
            NSApp.activate(ignoringOtherApps: true)
            win.makeKeyAndOrderFront(nil)
        }
    }

    @objc private func reloadWebView() {
        if let url = URL(string: "http://127.0.0.1:8790") {
            webView?.load(URLRequest(url: url))
        }
    }

    @objc private func openExternalBrowser() {
        if let url = URL(string: "http://127.0.0.1:8790") {
            NSWorkspace.shared.open(url)
        }
    }

    @objc private func quitApp() {
        NSApplication.shared.terminate(nil)
    }
}
