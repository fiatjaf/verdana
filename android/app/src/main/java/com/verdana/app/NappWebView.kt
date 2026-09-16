package com.verdana.app

import android.annotation.SuppressLint
import android.graphics.Bitmap
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.webkit.JavaScriptReplyProxy
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewCompat.WebMessageListener
import mobile.Mobile
import java.io.File
import java.io.IOException

// A NappWebView is one napp window on Android: a WebView that serves the
// napp's files from a per-instance https origin, injects the same bridge.js
// the desktop does, and speaks the backend's wire protocol over a web message
// channel (window.__verdanaHost).
//
// The wire messages themselves (resp/eval/action/theme/close) are interpreted
// in applyMessage, mirroring the desktop's child process.

object NappOrigins {
    private const val DOMAIN = "napp.verdana"

    // originFor gives every instance its own https origin, the way the
    // desktop gives every napp its own process: same-origin files, isolated
    // local storage, and a web message listener scoped to exactly it.
    fun hostFor(instance: String): String {
        val label = instance.filter { it.isLetterOrDigit() || it == '-' }.lowercase()
        return "$label.$DOMAIN"
    }

    fun originFor(instance: String): String = "https://" + hostFor(instance)
}

class NappWebView(
    private val activity: MainActivity,
    val instance: String,
    val name: String,
    private val spec: WindowSpec,
) {
    val view: WebView = WebView(activity)

    // replyProxy is the channel back into the page; it exists from the first
    // main-frame message on. Messages sent before it shows are queued.
    private var proxy: JavaScriptReplyProxy? = null
    private val outbox = ArrayDeque<String>()

    init {
        @SuppressLint("SetJavaScriptEnabled")
        val settings = view.settings
        settings.javaScriptEnabled = true
        settings.domStorageEnabled = true
        settings.allowFileAccess = false
        settings.allowContentAccess = false
        settings.javaScriptCanOpenWindowsAutomatically = false
        settings.mediaPlaybackRequiresUserGesture = true

        view.setBackgroundColor(android.graphics.Color.TRANSPARENT)

        WebViewCompat.addWebMessageListener(
            view,
            "__verdanaHost",
            setOf(NappOrigins.originFor(instance)),
            object : WebMessageListener {
                override fun onPostMessage(
                    view: WebView,
                    message: androidx.webkit.WebMessageCompat,
                    sourceOrigin: android.net.Uri,
                    isMainFrame: Boolean,
                    replyProxy: JavaScriptReplyProxy,
                ) {
                    if (!isMainFrame) return
                    proxy = replyProxy
                    // the page is listening now: anything queued can go
                    while (true) {
                        val pending = synchronized(outbox) { outbox.removeFirstOrNull() } ?: break
                        sendToPage(pending)
                    }
                    val data = message.data
                    if (!data.isNullOrBlank()) {
                        Mobile.handleMessage(instance, data)
                    }
                }
            },
        )

        view.webViewClient = object : WebViewClient() {
            override fun shouldInterceptRequest(
                v: WebView,
                request: WebResourceRequest,
            ): WebResourceResponse? = serve(request.url.path ?: "/")

            override fun shouldOverrideUrlLoading(v: WebView, request: WebResourceRequest): Boolean {
                // a napp never navigates away from itself on its own; external
                // links go through napp.link() (and a prompt) instead
                val url = request.url
                if (url.toString().startsWith(NappOrigins.originFor(instance))) return false
                return true
            }

            override fun onPageStarted(v: WebView, url: String, favicon: Bitmap?) {
                // nothing: document-start injection already ran
            }
        }

        // everything the page needs before any of its scripts run, on every
        // navigation: identity (window.name), domains, theme, and the bridge
        val origin = NappOrigins.originFor(instance)
        val init = "window.name = ${jsString(instance)};" +
            "window.__nappDomains = ${jsStringList(spec.requires)};" +
            themeInitScript(spec.theme, spec.themeVars)
        WebViewCompat.addDocumentStartJavaScript(
            view,
            "$init\n${Mobile.bridgeJS()}",
            setOf(origin),
        )

        view.loadUrl(origin + "/")
    }

    // ─── backend → page ──────────────────────────────────────────────

    // applyMessage interprets one wire message from the backend, exactly like
    // the desktop child's reader loop does.
    fun applyMessage(msgJSON: String) {
        val m = org.json.JSONObject(msgJSON)
        when (m.optString("t")) {
            "resp" -> sendToPage(msgJSON)
            "eval" -> view.post { view.evaluateJavascript(m.optString("code"), null) }
            "action" -> {
                val idx = if (m.has("idx") && !m.isNull("idx")) m.getLong("idx").toString() else "null"
                val payload = if (m.optString("params").isBlank()) "null" else m.optString("params")
                val code = "window.__bridge_dispatch_action(" +
                    m.optLong("id", -1) + "," + jsString(m.optString("method")) + "," +
                    jsString(payload) + "," + idx + ")"
                view.post { view.evaluateJavascript(code, null) }
            }
            "theme" -> {
                val name = m.optString("method", "light")
                val vars = m.optString("params", "{}").ifBlank { "{}" }
                val init = themeInitScript(name, vars)
                val code = init +
                    "if (window.__bridge_theme_change) window.__bridge_theme_change(" +
                    jsString(name) + "," + jsString(vars) + ");"
                // both now and on every future (re)load
                WebViewCompat.addDocumentStartJavaScript(
                    view,
                    init,
                    setOf(NappOrigins.originFor(instance)),
                )
                view.post { view.evaluateJavascript(code, null) }
            }
            "close" -> activity.closeTab(instance)
        }
    }

    private fun sendToPage(msgJSON: String) {
        val p = proxy ?: run {
            synchronized(outbox) { outbox.addLast(msgJSON) }
            return
        }
        // The port carries strings; the page's rpc resolver keys off `id`.
        view.post {
            try {
                p.postMessage(msgJSON)
            } catch (_: IllegalStateException) {
                // the page went away mid-send; nothing to do
            }
        }
    }

    fun destroy() {
        view.destroy()
    }

    // nappId is which napp this window is running, for the window switcher.
    fun nappId(): String = spec.nappId

    // ─── serving the napp's files ────────────────────────────────────

    // serve maps a request path onto the napp's unpacked directory, with the
    // desktop's SPA fallback: extensionless paths get index.html.
    private fun serve(path: String): WebResourceResponse? {
        val root = File(spec.dir)
        val clean = path.trimStart('/')
        var f = File(root, clean)
        if (f.isDirectory || clean.isBlank()) f = File(root, "index.html")
        if (!f.exists() && !clean.contains('.')) f = File(root, "index.html")
        if (!f.exists() || !f.canonicalFile.startsWith(root.canonicalFile)) return null

        return try {
            val stream = f.inputStream()
            WebResourceResponse(mimeFor(f.name), null, stream)
        } catch (_: IOException) {
            null
        }
    }

    private fun mimeFor(name: String): String = when {
        name.endsWith(".html") -> "text/html"
        name.endsWith(".js") -> "text/javascript"
        name.endsWith(".mjs") -> "text/javascript"
        name.endsWith(".css") -> "text/css"
        name.endsWith(".json") -> "application/json"
        name.endsWith(".svg") -> "image/svg+xml"
        name.endsWith(".png") -> "image/png"
        name.endsWith(".jpg") || name.endsWith(".jpeg") -> "image/jpeg"
        name.endsWith(".gif") -> "image/gif"
        name.endsWith(".webp") -> "image/webp"
        name.endsWith(".ico") -> "image/x-icon"
        name.endsWith(".woff") -> "font/woff"
        name.endsWith(".woff2") -> "font/woff2"
        name.endsWith(".ttf") -> "font/ttf"
        name.endsWith(".otf") -> "font/otf"
        name.endsWith(".wasm") -> "application/wasm"
        name.endsWith(".mp3") -> "audio/mpeg"
        name.endsWith(".mp4") -> "video/mp4"
        name.endsWith(".txt") -> "text/plain"
        else -> "application/octet-stream"
    }

    private fun jsString(s: String): String =
        org.json.JSONObject.quote(s ?: "")

    private fun jsStringList(items: List<String>): String =
        org.json.JSONArray(items ?: emptyList<String>()).toString()

    private fun themeInitScript(name: String, varsJSON: String): String {
        val n = name.ifBlank { "light" }
        val vars = varsJSON.ifBlank { "{}" }
        return "window.__nappTheme = {name:" + jsString(n) + ",vars:" + vars + "};"
    }
}
