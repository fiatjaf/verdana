package com.verdana.app

import android.annotation.SuppressLint
import android.app.ActivityManager
import android.content.Intent
import android.os.Bundle
import android.util.Log
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.systemBarsPadding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView
import androidx.webkit.JavaScriptReplyProxy
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewCompat.WebMessageListener
import mobile.Mobile
import org.json.JSONObject

// SettingsActivity is a napp's settings window (or the launcher's own): the
// launcher's settings page in a WebView, its own task and card in Recents
// like a napp window. No napp code runs in it; the page renders the napp's
// NAP-CONFIG schema and the launcher's relays and Blossom servers, and talks
// to the backend over the same wire protocol, under its window id.
class SettingsActivity : ComponentActivity() {

    lateinit var windowId: String
        private set

    private var web: SettingsWebView? = null
    private var ownsWindow = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        windowId = intent?.data?.lastPathSegment.orEmpty()
        val spec = intent?.getStringExtra(EXTRA_SPEC)?.let {
            try {
                JSONObject(it)
            } catch (e: Exception) {
                Log.e("Verdana", "unreadable settings spec", e)
                null
            }
        }
        // a settings window lives as long as the backend that opened it: one
        // Android brought back from Recents after the process died is gone
        if (windowId.isBlank() || spec == null || !Mobile.isSettingsWindow(windowId)) {
            finishAndRemoveTask()
            return
        }

        val view = SettingsWebView(this, windowId, spec.optString("theme"), spec.optString("themeVars")) {
            finishWindow()
        }
        if (!VerdanaHost.claimSettings(this)) {
            view.destroy()
            finish()
            return
        }
        ownsWindow = true
        web = view

        val name = spec.optString("name").ifBlank { "Verdana" }
        @Suppress("DEPRECATION")
        setTaskDescription(ActivityManager.TaskDescription("$name — Settings"))

        setContent {
            val theme = themeByName(VerdanaHost.state.theme)
            MaterialTheme(colorScheme = theme.compose()) {
                Surface(color = theme.bg, modifier = Modifier.fillMaxSize()) {
                    BackHandler { finishWindow() }
                    AndroidView(
                        factory = { view.view },
                        modifier = Modifier.fillMaxSize().systemBarsPadding(),
                    )
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
    }

    fun deliver(msgJSON: String) {
        runOnUiThread { web?.applyMessage(msgJSON) }
    }

    fun finishWindow() {
        runOnUiThread { finishAndRemoveTask() }
    }

    override fun onDestroy() {
        super.onDestroy()
        web?.destroy()
        web = null
        if (!ownsWindow) return
        ownsWindow = false
        VerdanaHost.unregisterSettings(windowId, this)
        if (isChangingConfigurations) return
        try {
            Mobile.windowClosed(windowId)
        } catch (e: Exception) {
            Log.e("Verdana", "windowClosed failed for $windowId", e)
        }
    }

    companion object {
        const val EXTRA_SPEC = "com.verdana.app.SETTINGS_SPEC"
    }
}

// SettingsWebView serves the settings page at its own https origin and
// carries wire messages over the __verdanaHost channel, the way NappWebView
// does for a napplet's host page.
class SettingsWebView(
    activity: ComponentActivity,
    private val windowId: String,
    theme: String,
    themeVars: String,
    private val onClose: () -> Unit,
) {
    val view: WebView = WebView(activity)
    private val origin = NappOrigins.originFor(windowId)

    private var proxy: JavaScriptReplyProxy? = null
    private val outbox = ArrayDeque<String>()

    init {
        @SuppressLint("SetJavaScriptEnabled")
        val settings = view.settings
        settings.javaScriptEnabled = true
        settings.domStorageEnabled = false
        settings.allowFileAccess = false
        settings.allowContentAccess = false
        settings.javaScriptCanOpenWindowsAutomatically = false
        view.setBackgroundColor(android.graphics.Color.TRANSPARENT)

        WebViewCompat.addWebMessageListener(
            view,
            "__verdanaHost",
            setOf(origin),
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
                    while (true) {
                        val pending = synchronized(outbox) { outbox.removeFirstOrNull() } ?: break
                        sendToPage(pending)
                    }
                    val data = message.data
                    if (!data.isNullOrBlank()) Mobile.handleMessage(windowId, data)
                }
            },
        )

        view.webViewClient = object : WebViewClient() {
            override fun shouldInterceptRequest(v: WebView, request: WebResourceRequest): WebResourceResponse? {
                val path = request.url.path ?: "/"
                if (path != "/" && path.isNotEmpty()) return null
                return WebResourceResponse(
                    "text/html",
                    "utf-8",
                    200,
                    "OK",
                    mapOf(
                        "Content-Security-Policy" to
                            "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; " +
                            "img-src data:; frame-src 'none'; base-uri 'none'; form-action 'none'",
                        "Cache-Control" to "no-store",
                    ),
                    Mobile.settingsHTML().toByteArray(Charsets.UTF_8).inputStream(),
                )
            }

            // the page never navigates anywhere
            override fun shouldOverrideUrlLoading(v: WebView, request: WebResourceRequest): Boolean =
                request.isForMainFrame
        }

        WebViewCompat.addDocumentStartJavaScript(
            view,
            themeInitScript(theme, themeVars) + "\n" + Mobile.settingsJS(),
            setOf(origin),
        )
        view.loadUrl("$origin/")
    }

    fun applyMessage(msgJSON: String) {
        val m = JSONObject(msgJSON)
        when (m.optString("t")) {
            "resp" -> sendToPage(msgJSON)
            "eval" -> view.post { view.evaluateJavascript(m.optString("code"), null) }
            "theme" -> {
                val name = m.optString("method", "light")
                val vars = m.optString("params", "{}").ifBlank { "{}" }
                val code = themeInitScript(name, vars) +
                    "if (window.__bridge_theme_change) window.__bridge_theme_change(" +
                    jsString(name) + "," + jsString(vars) + ");"
                view.post { view.evaluateJavascript(code, null) }
            }
            "close" -> onClose()
        }
    }

    private fun sendToPage(msgJSON: String) {
        val p = proxy ?: run {
            synchronized(outbox) { outbox.addLast(msgJSON) }
            return
        }
        view.post {
            try {
                p.postMessage(msgJSON)
            } catch (_: IllegalStateException) {
            }
        }
    }

    fun destroy() {
        view.destroy()
    }
}
