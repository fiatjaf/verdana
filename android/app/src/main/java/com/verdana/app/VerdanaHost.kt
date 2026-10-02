package com.verdana.app

import android.app.ActivityManager
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.util.Log
import android.widget.Toast
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import mobile.Mobile
import mobile.UI
import java.util.concurrent.ConcurrentHashMap
import org.json.JSONObject

// VerdanaHost is the process-wide implementation of the backend's mobile.UI
// interface: the one place the Go side talks to. It deliberately outlives
// every activity — a napp's window is its own activity now, and the backend
// has to be able to reach a window that is being brought back or torn down
// independently of the launcher.
//
// VerdanaApplication brings it up before anything else runs; from there on
// the launcher (MainActivity) and the napp windows (NappActivity) are just
// two kinds of screen reading the same state.
object VerdanaHost : UI {

    // the launcher snapshot and the pending question, republished as compose
    // state so both kinds of screen redraw from the backend
    var state by mutableStateOf(emptyState())
        private set

    var prompt by mutableStateOf<Prompt?>(null)
        private set

    var discoveryArchetype by mutableStateOf("")
        private set

    // the napp windows that are alive right now, by instance id
    private val windows = ConcurrentHashMap<String, NappActivity>()

    // messages that got here before their window's activity finished starting
    private val outbox = ConcurrentHashMap<String, ArrayDeque<String>>()

    private var started = false
    private var appContext: Context? = null
    private val notificationPermissionLock = Any()

    private fun emptyState() = LauncherState(
        phase = "loading", loginErr = "", profileName = "", profilePicture = "",
        pubkey = "", fetchErr = "", fetching = false, theme = "light",
        relays = emptyList(), installed = emptyList(), discovery = emptyList(),
        busy = emptyList(), windows = emptyList(),
    )

    // start brings the backend up, once. Everything it needs is here or in
    // the app context, so it does not matter which activity the user got the
    // process started from.
    fun start(context: Context) {
        if (started) return
        started = true
        appContext = context.applicationContext

        try {
            Mobile.start(context.filesDir.absolutePath, this)
        } catch (e: Exception) {
            Log.e("Verdana", "backend failed to start", e)
        }

        // napps follow the system dark mode from day one
        val mode = context.resources.configuration.uiMode and
            android.content.res.Configuration.UI_MODE_NIGHT_MASK
        val initial = if (mode == android.content.res.Configuration.UI_MODE_NIGHT_YES) {
            "dark"
        } else {
            "light"
        }
        setCurrentThemeName(initial)
        Mobile.setTheme(initial, themeVarsJSON(themeByName(initial)))
    }

    // ─── windows ─────────────────────────────────────────────────────

    // windowUri is what makes every napp its own document: Android keys the
    // task off the intent's data, so a different instance is a different task
    // and the same instance comes back to the task already showing it.
    fun windowUri(instance: String): Uri = Uri.parse("$WINDOW_SCHEME://napp/$instance")

    private fun launchWindow(instance: String, specJSON: String) {
        val ctx = appContext ?: return
        val intent = Intent(ctx, NappActivity::class.java).apply {
            data = windowUri(instance)
            putExtra(NappActivity.EXTRA_SPEC, specJSON)
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_NEW_DOCUMENT)
        }
        try {
            ctx.startActivity(intent)
        } catch (e: Exception) {
            Log.e("Verdana", "could not open a window for $instance", e)
            Toast.makeText(ctx, "Could not open the napp window", Toast.LENGTH_SHORT).show()
        }
    }

    // claim makes a window's activity the one the backend talks to, and
    // says whether it got that role. A false means another activity is
    // already running this instance: the caller is a duplicate task and
    // should go away instead of becoming a second window for one napp.
    internal fun claim(window: NappActivity): Boolean {
        synchronized(windows) {
            val existing = windows[window.instance]
            if (existing != null && existing != window) return false
            windows[window.instance] = window
        }
        val queued = outbox.remove(window.instance)
        if (queued != null) queued.forEach { window.deliver(it) }
        return true
    }

    internal fun unregister(instance: String, window: NappActivity) {
        windows.remove(instance, window)
        outbox.remove(instance)
    }

    // surface brings an open napp's task back to the front. The system does
    // the switching; all this does is find the task that window is in.
    fun surface(instance: String) {
        val ctx = appContext ?: return
        val am = ctx.getSystemService(Context.ACTIVITY_SERVICE) as? ActivityManager ?: return
        // the live window's own task, so this never picks up a duplicate
        // task still on its way out (they share the same intent data)
        val owned = windows[instance]?.taskId
        val task = am.appTasks.firstOrNull {
            it.taskInfo.id == owned || it.taskInfo.baseIntent?.data == windowUri(instance)
        } ?: return
        task.moveToFront()
    }

    // answer files a prompt's answer and, when the answer routes somewhere,
    // puts that window in front of the user.
    fun answer(p: Prompt, ok: Boolean, index: Int, scope: String) {
        prompt = null
        Mobile.answerPrompt(p.id, ok, index.toLong(), scope)
        if (!ok || index < 0 || index >= p.options.size) return
        val opt = p.options[index]
        if (opt.instance.isNotBlank()) {
            surface(opt.instance)
        } else if (opt.nappId.isNotBlank()) {
            windows.values.firstOrNull { it.nappId == opt.nappId }?.let { surface(it.instance) }
        }
    }

    // promptFor is the question this window has to cover, or null: a prompt
    // fired by a napp belongs over that napp's own screen, one fired by the
    // launcher belongs wherever the launcher happens to be.
    fun promptFor(instance: String): Prompt? {
        val p = prompt ?: return null
        return if (p.instance.isBlank() || p.instance == instance) p else null
    }

    fun toggleTheme() {
        val next = if (state.theme == "dark") "light" else "dark"
        Mobile.setTheme(next, themeVarsJSON(themeByName(next)))
        setCurrentThemeName(next)
    }

    fun consumeDiscoveryArchetype() {
        discoveryArchetype = ""
    }

    // ─── mobile.UI ───────────────────────────────────────────────────
    // Every one of these may be called from any thread.

    override fun openWindow(instance: String, specJSON: String) {
        val ctx = appContext ?: return
        ctx.startActivityOnMain {
            launchWindow(instance, specJSON)
        }
    }

    override fun sendToWindow(instance: String, msgJSON: String) {
        val window = windows[instance]
        if (window != null) {
            window.deliver(msgJSON)
        } else {
            // the window is on its way up: hold the message until it is up
            synchronized(outbox) {
                outbox.computeIfAbsent(instance) { ArrayDeque() }.addLast(msgJSON)
            }
        }
    }

    override fun focusWindow(instance: String) {
        appContext?.startActivityOnMain { surface(instance) }
    }

    override fun closeWindow(instance: String) {
        // the window is on its way out either way: anything still queued
        // for it is never going to be delivered
        outbox.remove(instance)
        windows[instance]?.finishWindow()
    }

    override fun stateChanged() {
        val snapshot = Mobile.state()
        android.os.Handler(android.os.Looper.getMainLooper()).post {
            state = parseState(snapshot)
        }
    }

    override fun openDiscovery(archetype: String) {
        val ctx = appContext ?: return
        ctx.startActivityOnMain {
            discoveryArchetype = archetype
            ctx.startActivity(
                Intent(ctx, MainActivity::class.java).addFlags(
                    Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_REORDER_TO_FRONT
                )
            )
        }
    }

    override fun promptsChanged() {
        val current = Mobile.currentPrompt()
        appContext?.startActivityOnMain {
            val p = parsePrompt(current)
            prompt = p
            // A question a napp asked belongs over that napp's window, and a
            // question nobody is looking at cannot be answered: bring its
            // window to the front, the way the old tab switcher used to
            // force-switch to the prompt.
            if (p != null && p.instance.isNotBlank()) {
                val owner = windows[p.instance]
                if (owner != null && !owner.onScreen()) surface(p.instance)
            }
        }
    }

    override fun copyText(text: String) {
        val ctx = appContext ?: return
        ctx.startActivityOnMain {
            val cm = ctx.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
            cm.setPrimaryClip(ClipData.newPlainText("text", text))
            Toast.makeText(ctx, "Copied", Toast.LENGTH_SHORT).show()
        }
    }

    override fun saveFile(name: String, data: ByteArray): String {
        val ctx = appContext ?: throw java.io.IOException("verdana is not started")
        // MediaStore-downloaded folder, no storage permission needed
        val values = android.content.ContentValues().apply {
            put(android.provider.MediaStore.Downloads.DISPLAY_NAME, name)
            put(android.provider.MediaStore.Downloads.MIME_TYPE, mimeGuess(name))
        }
        val resolver = ctx.contentResolver
        val uri = resolver.insert(android.provider.MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
            ?: throw java.io.IOException("could not create download entry")
        resolver.openOutputStream(uri)?.use { it.write(data) }
            ?: throw java.io.IOException("could not open download stream")
        return name
    }

    override fun saveFileTarget(): String = "your Downloads folder"

    override fun openLink(url: String) {
        val ctx = appContext ?: return
        ctx.startActivityOnMain {
            try {
                ctx.startActivity(
                    Intent(Intent.ACTION_VIEW, Uri.parse(url))
                        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
                )
            } catch (e: Exception) {
                Toast.makeText(ctx, "No browser to open $url", Toast.LENGTH_SHORT).show()
            }
        }
    }

    // amberRequest is the Go side's one entry into the phone's signer app:
    // launch the request off the main thread and answer how it went. The
    // signer's answer itself arrives later, through the launcher callback,
    // which calls Mobile.answerAmber.
    override fun amberRequest(
        id: String,
        op: String,
        payload: String,
        pubkey: String,
        counterpart: String,
        pkg: String,
    ): Boolean {
        val done = java.util.concurrent.Semaphore(0)
        var went = false
        appContext?.startActivityOnMain {
            went = Amber.request(op, payload, pubkey, counterpart, pkg, id)
            done.release()
        }
        // startActivityOnMain posts when off the main thread; the Go caller
        // needs the answer of "did it launch", which is the only thing that
        // can be known this early, so wait for just that
        done.tryAcquire(java.util.concurrent.TimeUnit.SECONDS.toNanos(5), java.util.concurrent.TimeUnit.NANOSECONDS)
        return went
    }

    override fun systemNotification(requestJSON: String) {
        val ctx = appContext ?: throw java.io.IOException("verdana is not started")
        val request = JSONObject(requestJSON)
        if (android.os.Build.VERSION.SDK_INT >= 33 &&
            androidx.core.content.ContextCompat.checkSelfPermission(
                ctx, android.Manifest.permission.POST_NOTIFICATIONS,
            ) != android.content.pm.PackageManager.PERMISSION_GRANTED
        ) {
            throw java.io.IOException("notification permission denied")
        }
        val requestedChannel = request.optString("Channel").ifBlank { "napplets" }
        val channelId = "napplet." + requestedChannel.replace(Regex("[^A-Za-z0-9._-]"), "_")
        val manager = ctx.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        manager.createNotificationChannel(
            NotificationChannel(channelId, "Napplet notifications", NotificationManager.IMPORTANCE_DEFAULT),
        )
        val id = request.getString("ID")
        val title = request.optString("NappName").ifBlank { "Napplet" } + ": " +
            request.getString("Title")
        val notification = NotificationCompat.Builder(ctx, channelId)
            .setSmallIcon(com.verdana.app.R.mipmap.ic_launcher)
            .setContentTitle(title)
            .setContentText(request.optString("Body"))
            .setStyle(NotificationCompat.BigTextStyle().bigText(request.optString("Body")))
            .setAutoCancel(true)
            .build()
        NotificationManagerCompat.from(ctx).notify(id, id.hashCode(), notification)
    }

    override fun requestNotificationPermission(): Boolean {
        val ctx = appContext ?: return false
        if (android.os.Build.VERSION.SDK_INT < 33 ||
            androidx.core.content.ContextCompat.checkSelfPermission(
                ctx, android.Manifest.permission.POST_NOTIFICATIONS,
            ) == android.content.pm.PackageManager.PERMISSION_GRANTED
        ) return true

        synchronized(notificationPermissionLock) {
            val owner = windows.values.firstOrNull { it.onScreen() } ?: return false
            val done = java.util.concurrent.Semaphore(0)
            var granted = false
            owner.requestNotificationPermission {
                granted = it
                done.release()
            }
            if (!done.tryAcquire(60, java.util.concurrent.TimeUnit.SECONDS)) return false
            return granted
        }
    }

    override fun dismissSystemNotification(id: String) {
        appContext?.let { NotificationManagerCompat.from(it).cancel(id, id.hashCode()) }
    }

    // playMedia hands a napplet's shell-owned media session to whatever
    // player app the phone has. Like amberRequest, it waits on the main
    // thread only long enough to know whether some app took it.
    override fun playMedia(url: String, mime: String, title: String): Boolean {
        val ctx = appContext ?: return false
        val done = java.util.concurrent.Semaphore(0)
        var went = false
        ctx.startActivityOnMain {
            try {
                val type = mime.ifBlank { "video/*" }
                ctx.startActivity(
                    Intent(Intent.ACTION_VIEW)
                        .setDataAndType(Uri.parse(url), type)
                        .putExtra(Intent.EXTRA_TITLE, title)
                        .putExtra("title", title)
                        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
                )
                went = true
            } catch (e: android.content.ActivityNotFoundException) {
                Toast.makeText(ctx, "No app to play this media", Toast.LENGTH_SHORT).show()
            }
            done.release()
        }
        done.tryAcquire(java.util.concurrent.TimeUnit.SECONDS.toNanos(5), java.util.concurrent.TimeUnit.NANOSECONDS)
        return went
    }

    private fun mimeGuess(name: String): String = when {
        name.endsWith(".html") -> "text/html"
        name.endsWith(".png") -> "image/png"
        name.endsWith(".jpg") || name.endsWith(".jpeg") -> "image/jpeg"
        name.endsWith(".json") -> "application/json"
        name.endsWith(".pdf") -> "application/pdf"
        name.endsWith(".txt") -> "text/plain"
        else -> "application/octet-stream"
    }
}

// WINDOW_SCHEME is the intent scheme that makes every napp its own document:
// Android keys the task off the intent's data, so verdana://napp/7 and
// verdana://napp/8 are two windows and verdana://napp/7 twice is one window
// brought back to the front.
const val WINDOW_SCHEME = "verdana"

// startActivityOnMain runs a block on the main looper, right away when the
// caller is already on it: the backend calls in from its own goroutines.
internal fun <T> Context.startActivityOnMain(block: () -> T) {
    if (android.os.Looper.myLooper() == android.os.Looper.getMainLooper()) {
        block()
    } else {
        android.os.Handler(android.os.Looper.getMainLooper()).post { block() }
    }
}
