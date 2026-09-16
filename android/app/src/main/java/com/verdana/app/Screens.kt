package com.verdana.app

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.foundation.Image
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import android.graphics.BitmapFactory
import kotlinx.coroutines.withContext
import java.util.concurrent.ConcurrentHashMap

// The name of the theme the launcher is currently drawing with, kept in one
// place so screens that don't get it handed down (the prompt dialog) can ask.
var currentThemeName: String = "light"
    private set
fun setCurrentThemeName(name: String) { currentThemeName = name }

// The launcher screens: login, the installed/discovery tabs, and the prompt
// dialog — the same surfaces the Gio desktop shows, from the same backend
// state.

private val iconCache = ConcurrentHashMap<String, ImageBitmap?>()

@Composable
fun LoadingScreen() {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        CircularProgressIndicator()
    }
}

@Composable
fun LoginScreen(activity: MainActivity, st: LauncherState) {
    val theme = themeByName(st.theme)
    var input by remember { mutableStateOf("") }

    Column(
        Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(20.dp),
        verticalArrangement = Arrangement.Center,
    ) {
        Text("Log in to Verdana", style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold, color = theme.fg)
        Spacer(Modifier.height(6.dp))
        Text(
            "Paste your nsec or a bunker:// URL",
            style = MaterialTheme.typography.bodyMedium,
            color = theme.subtle,
        )
        Spacer(Modifier.height(16.dp))
        OutlinedTextField(
            value = input,
            onValueChange = { input = it },
            modifier = Modifier.fillMaxWidth(),
            placeholder = { Text("nsec1... or bunker://...", color = theme.inputHint) },
            singleLine = true,
            colors = outlinedColors(theme),
        )
        Spacer(Modifier.height(12.dp))
        Button(onClick = { activity.login(input.trim()) }, modifier = Modifier.fillMaxWidth()) {
            Text("Log in")
        }
        if (st.loginErr.isNotBlank()) {
            Spacer(Modifier.height(12.dp))
            Text(st.loginErr, color = theme.danger, style = MaterialTheme.typography.bodySmall)
        }
    }
}

@Composable
fun LauncherScreen(activity: MainActivity, st: LauncherState) {
    val theme = themeByName(st.theme)
    var tab by remember { mutableStateOf(0) }
    var relaysEd by remember(st.relays.hashCode()) { mutableStateOf(st.relays.joinToString("\n")) }
    var showLogoutConfirm by remember { mutableStateOf(false) }

    Column(Modifier.fillMaxSize().padding(16.dp)) {

        // profile header
        Row(verticalAlignment = Alignment.CenterVertically) {
            Avatar(st.profilePicture, theme, 44)
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(
                    st.profileName.ifBlank { "…" },
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                    color = theme.fg,
                    maxLines = 1,
                )
                TextButton(onClick = { showLogoutConfirm = true }, contentPadding = PaddingValues(0.dp)) {
                    Text("Log out", fontSize = 12.sp, color = theme.muted)
                }
            }
            TextButton(onClick = { activity.toggleTheme() }) {
                Text(if (theme.name == "dark") "☀ Light" else "☾ Dark", color = theme.chipFg, fontSize = 13.sp)
            }
        }

        if (showLogoutConfirm) {
            LogoutConfirmDialog(
                theme = theme,
                onConfirm = {
                    showLogoutConfirm = false
                    activity.logout()
                },
                onDismiss = { showLogoutConfirm = false },
            )
        }

        Spacer(Modifier.height(8.dp))

        // tabs
        Row {
            TabChip("Installed", tab == 0, theme) { tab = 0 }
            Spacer(Modifier.width(8.dp))
            TabChip("Discovery", tab == 1, theme) { tab = 1 }
        }

        Spacer(Modifier.height(12.dp))

        if (tab == 0) {
            InstalledTab(activity, st, theme)
        } else {
            DiscoveryTab(activity, st, theme, relaysEd) { relaysEd = it }
        }
    }
}

// LogoutConfirmDialog asks before logging out: it closes every open napp
// window, so it deserves a second look — same flow as the desktop launcher.
@Composable
private fun LogoutConfirmDialog(theme: Theme, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    androidx.compose.ui.window.Dialog(onDismissRequest = onDismiss) {
        Column(
            Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(12.dp))
                .background(theme.card)
                .padding(20.dp),
        ) {
            Text("Log out?", fontWeight = FontWeight.Bold, style = MaterialTheme.typography.titleMedium, color = theme.fg)
            Spacer(Modifier.height(8.dp))
            Text(
                "This closes every open napp and forgets the key on this device.",
                color = theme.subtle,
                style = MaterialTheme.typography.bodyMedium,
            )
            Spacer(Modifier.height(16.dp))
            Row {
                Button(
                    onClick = onConfirm,
                    colors = ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.danger),
                ) { Text("Log out") }
                Spacer(Modifier.width(12.dp))
                Button(
                    onClick = onDismiss,
                    colors = ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.chipFg),
                ) { Text("Cancel") }
            }
        }
    }
}

@Composable
private fun TabChip(label: String, active: Boolean, theme: Theme, onClick: () -> Unit) {
    Button(
        onClick = onClick,
        colors = ButtonDefaults.buttonColors(
            containerColor = if (active) theme.accent else theme.chipBg,
            contentColor = if (active) theme.accentText else theme.chipFg,
        ),
        contentPadding = PaddingValues(horizontal = 16.dp, vertical = 6.dp),
    ) {
        Text(label, fontSize = 13.sp)
    }
}

@Composable
private fun InstalledTab(activity: MainActivity, st: LauncherState, theme: Theme) {
    if (st.windows.isNotEmpty()) {
        Text(
            "Open: " + st.windows.joinToString(", ") { it.name },
            color = theme.muted,
            fontSize = 12.sp,
        )
        Spacer(Modifier.height(6.dp))
    }
    if (st.installed.isEmpty()) {
        Text("No napps installed yet. Find some in Discovery.", color = theme.muted)
        return
    }
    LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        items(st.installed, key = { it.id }) { napp ->
            NappCard(activity, napp, theme, "Open") { activity.launch(napp.id) }
        }
    }
}

@Composable
private fun DiscoveryTab(
    activity: MainActivity,
    st: LauncherState,
    theme: Theme,
    relaysEd: String,
    setRelaysEd: (String) -> Unit,
) {
    LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        item {
            Text("Relays (one per line)", color = theme.subtle, fontSize = 13.sp)
            Spacer(Modifier.height(6.dp))
            OutlinedTextField(
                value = relaysEd,
                onValueChange = setRelaysEd,
                modifier = Modifier.fillMaxWidth(),
                placeholder = { Text("relay.example.com", color = theme.inputHint) },
                colors = outlinedColors(theme),
                minLines = 2,
            )
            Spacer(Modifier.height(8.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Button(
                    onClick = {
                        activity.saveRelays(relaysEd)
                        activity.fetchNapps()
                    },
                    enabled = !st.fetching,
                ) {
                    Text(if (st.fetching) "Fetching…" else "Fetch napps")
                }
                if (st.fetchErr.isNotBlank()) {
                    Spacer(Modifier.width(12.dp))
                    Text(st.fetchErr, color = theme.danger, fontSize = 12.sp, modifier = Modifier.weight(1f))
                }
            }
            Spacer(Modifier.height(12.dp))
            if (st.discovery.isEmpty()) {
                Text(
                    if (st.fetching) "Searching relays…" else "No napps yet. Tap \"Fetch napps\".",
                    color = theme.muted,
                )
            }
        }
        items(st.discovery, key = { it.id }) { napp ->
            val installed = st.installed.any { it.id == napp.id }
            val busy = st.busy.contains(napp.id)
            NappCard(activity, napp, theme, when {
                busy -> "Working…"
                installed -> "Uninstall"
                else -> "Install"
            }) {
                if (installed) activity.uninstall(napp.id) else activity.install(napp.id)
            }
        }
    }
}

@Composable
private fun NappCard(
    activity: MainActivity,
    napp: Napp,
    theme: Theme,
    actionLabel: String,
    onAction: () -> Unit,
) {
    // icons load off the main thread, keyed by blob hash like the desktop
    val hash = napp.iconHash()
    var bitmap by remember(napp.id, hash) { mutableStateOf(iconCache[hash]) }

    LaunchedEffect(napp.id, hash) {
        if (bitmap == null && hash.isNotBlank()) {
            activity.loadIcon(napp) { bytes ->
                val img = bytes?.decodeBitmap()
                if (img != null) iconCache[hash] = img
                bitmap = img
            }
        }
    }

    Row(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(8.dp))
            .background(theme.card)
            .padding(12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        NappIcon(bitmap, theme, 40)
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(napp.name.ifBlank { napp.id }, fontWeight = FontWeight.Bold, color = theme.fg)
            if (napp.description.isNotBlank()) {
                Text(napp.description, color = theme.subtle, fontSize = 13.sp, maxLines = 2)
            }
        }
        Spacer(Modifier.width(8.dp))
        Button(
            onClick = onAction,
            enabled = actionLabel != "Working…",
            contentPadding = PaddingValues(horizontal = 12.dp, vertical = 6.dp),
        ) {
            Text(actionLabel, fontSize = 13.sp)
        }
    }
}

// iconHash mirrors backend.Napp.IconHash: the icon's blob sha256.
private fun Napp.iconHash(): String = paths.firstOrNull { p ->
    icon.isNotBlank() && p.path.trimStart('/') == icon.trimStart('/')
}?.sha256 ?: ""

private fun ByteArray.decodeBitmap(): ImageBitmap? =
    BitmapFactory.decodeByteArray(this, 0, size)?.asImageBitmap()

@Composable
private fun NappIcon(img: ImageBitmap?, theme: Theme, size: Int) {
    val shape = RoundedCornerShape(6.dp)
    if (img != null) {
        Image(
            bitmap = img,
            contentDescription = null,
            modifier = Modifier.size(size.dp).clip(shape),
            contentScale = ContentScale.Crop,
        )
    } else {
        Box(Modifier.size(size.dp).clip(shape).background(theme.imageBg))
    }
}

@Composable
private fun Avatar(url: String, theme: Theme, size: Int) {
    val shape = RoundedCornerShape(6.dp)
    var img by remember(url) { mutableStateOf<ImageBitmap?>(null) }

    LaunchedEffect(url) {
        if (url.isNotBlank() && img == null) {
            img = withContext(kotlinx.coroutines.Dispatchers.IO) { fetchImageBitmap(url) }
        }
    }
    if (img != null) {
        Image(
            bitmap = img!!,
            contentDescription = null,
            modifier = Modifier.size(size.dp).clip(shape),
            contentScale = ContentScale.Crop,
        )
    } else {
        Box(Modifier.size(size.dp).clip(shape).background(theme.imageBg))
    }
}

// fetchImageBitmap pulls a profile picture off the web once. Call it from a
// background dispatcher only.
private fun fetchImageBitmap(url: String): ImageBitmap? = try {
    val conn = java.net.URL(url).openConnection() as java.net.HttpURLConnection
    conn.connectTimeout = 8000
    conn.readTimeout = 8000
    conn.instanceFollowRedirects = true
    if (conn.responseCode == 200) {
        val bytes = conn.inputStream.use { it.readBytes() }
        BitmapFactory.decodeByteArray(bytes, 0, bytes.size)?.asImageBitmap()
    } else null
} catch (_: Exception) {
    null
}

@Composable
private fun outlinedColors(theme: Theme) = OutlinedTextFieldDefaults.colors(
    focusedBorderColor = theme.accent,
    unfocusedBorderColor = theme.border,
    cursorColor = theme.accent,
    focusedTextColor = theme.fg,
    unfocusedTextColor = theme.fg,
)

// ─── the browser-like chrome ─────────────────────────────────────────
//
// A fixed header carries the app name, the id of the window on screen and a
// box with the number of open napps; tapping the box lists the open windows
// (and the pending prompt, which is just another window) to jump between.

@Composable
fun AppHeader(
    st: LauncherState,
    subtitle: String,
    promptActive: Boolean,
    theme: Theme,
    onHome: () -> Unit,
    onShowPrompt: () -> Unit,
    onActivate: (String) -> Unit,
    onClose: (String) -> Unit,
) {
    var showList by remember { mutableStateOf(false) }

    Column(Modifier.fillMaxWidth().background(theme.card)) {
        Row(
            Modifier
                .statusBarsPadding()
                .fillMaxWidth()
                .padding(horizontal = 12.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            // the app name doubles as "home": tap it to go back to the launcher
            Column(Modifier.weight(1f).clickable { showList = false; onHome() }) {
                Text("Verdana", fontWeight = FontWeight.Bold, fontSize = 16.sp, color = theme.fg)
                if (subtitle.isNotBlank()) {
                    Text(subtitle, fontSize = 11.sp, color = theme.muted, maxLines = 1)
                }
            }

            // the open-napps count box: tap to list and switch
            Box(
                Modifier
                    .clip(RoundedCornerShape(14.dp))
                    .background(if (showList) theme.accent else theme.chipBg)
                    .clickable { showList = !showList }
                    .padding(horizontal = 12.dp, vertical = 5.dp),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    st.windows.size.toString(),
                    color = if (showList) theme.accentText else theme.chipFg,
                    fontWeight = FontWeight.Bold,
                    fontSize = 14.sp,
                )
            }
        }

        if (showList) {
            WindowSwitcher(
                st = st,
                promptActive = promptActive,
                theme = theme,
                onPick = { instance ->
                    showList = false
                    onActivate(instance)
                },
                onPickPrompt = {
                    showList = false
                    onShowPrompt()
                },
                onClose = onClose,
            )
        }
    }
}

@Composable
private fun WindowSwitcher(
    st: LauncherState,
    promptActive: Boolean,
    theme: Theme,
    onPick: (String) -> Unit,
    onPickPrompt: () -> Unit,
    onClose: (String) -> Unit,
) {
    Column(Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 4.dp)) {
        if (promptActive) {
            SwitcherRow(
                label = "Prompt",
                detail = "waiting for your answer",
                active = true,
                theme = theme,
                onClick = onPickPrompt,
                onClose = null,
            )
        }
        st.windows.forEach { w ->
            SwitcherRow(
                label = w.name.ifBlank { w.nappId },
                detail = w.instance + if (w.action.isNotBlank()) " · ${w.action}" else "",
                active = false,
                theme = theme,
                onClick = { onPick(w.instance) },
                onClose = { onClose(w.instance) },
            )
        }
    }
}

@Composable
private fun SwitcherRow(
    label: String,
    detail: String,
    active: Boolean,
    theme: Theme,
    onClick: () -> Unit,
    onClose: (() -> Unit)?,
) {
    Row(
        Modifier
            .fillMaxWidth()
            .padding(vertical = 2.dp)
            .clip(RoundedCornerShape(8.dp))
            .background(if (active) theme.accent else theme.chipBg)
            .clickable(onClick = onClick)
            .padding(horizontal = 12.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                label,
                color = if (active) theme.accentText else theme.fg,
                fontWeight = FontWeight.Bold,
                fontSize = 14.sp,
                maxLines = 1,
            )
            if (detail.isNotBlank()) {
                Text(
                    detail,
                    color = if (active) theme.accentText else theme.muted,
                    fontSize = 11.sp,
                    maxLines = 1,
                )
            }
        }
        if (onClose != null) {
            TextButton(onClick = onClose) {
                Text("✕", color = if (active) theme.accentText else theme.muted)
            }
        }
    }
}

// PromptWindow is the prompt as one of the browser-like windows: a full
// screen under the same header, force-switched to whenever an action is
// invoked, and reachable again from the window switcher.
@Composable
fun PromptWindow(p: Prompt, onAnswer: (Boolean, Int) -> Unit) {
    val theme = themeByName(currentThemeName)
    Column(
        Modifier
            .fillMaxSize()
            .background(theme.bg)
            .verticalScroll(rememberScrollState())
            .padding(20.dp),
    ) {
        Text(p.title, fontWeight = FontWeight.Bold, style = MaterialTheme.typography.titleMedium, color = theme.fg)
        if (p.detail.isNotBlank()) {
            Spacer(Modifier.height(8.dp))
            Text(p.detail, color = theme.subtle, style = MaterialTheme.typography.bodyMedium)
        }
        if (p.code.isNotBlank()) {
            Spacer(Modifier.height(10.dp))
            Text(
                p.code,
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(6.dp))
                    .background(theme.codeBg)
                    .padding(8.dp),
                color = theme.codeFg,
                fontSize = 12.sp,
                maxLines = 8,
            )
        }
        Spacer(Modifier.weight(1f))
        Spacer(Modifier.height(16.dp))
        if (p.options.isNotEmpty()) {
            p.options.forEachIndexed { i, opt ->
                Button(
                    onClick = { onAnswer(true, i) },
                    modifier = Modifier.fillMaxWidth().padding(bottom = 6.dp),
                ) {
                    if (opt.detail.isNotBlank()) {
                        Column {
                            Text(opt.label, maxLines = 1)
                            Text(opt.detail, fontSize = 11.sp, color = theme.subtle, maxLines = 1)
                        }
                    } else {
                        Text(opt.label, maxLines = 1)
                    }
                }
            }
            TextButton(onClick = { onAnswer(false, 0) }) {
                Text("Cancel", color = theme.chipFg)
            }
        } else {
            Row {
                Button(onClick = { onAnswer(true, 0) }) { Text("Allow") }
                Spacer(Modifier.width(8.dp))
                Button(
                    onClick = { onAnswer(false, 0) },
                    colors = ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.chipFg),
                ) { Text("Deny") }
            }
        }
    }
}
