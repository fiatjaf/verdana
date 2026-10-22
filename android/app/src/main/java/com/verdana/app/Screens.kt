package com.verdana.app

import android.app.Activity
import android.content.Intent
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import sh.calvin.reorderable.ReorderableItem
import sh.calvin.reorderable.rememberReorderableLazyGridState
import androidx.compose.foundation.lazy.grid.items as gridItems
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.LayoutCoordinates
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.foundation.Image
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import android.graphics.BitmapFactory
import kotlinx.coroutines.withContext
import mobile.Mobile
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

// AMBER_PKG is Amber's own package: the fallback when a signer app answers
// the login without naming itself.
private const val AMBER_PKG = "com.greenart7c3.nostrsigner"

// AmberLogin is the NIP-55 login button: it asks the signer app for the
// key's pubkey (one launch when Amber is the only signer installed, a
// picker otherwise) and turns the answer into the same "amber:" login the
// backend resumes sessions with. Reports on the same error line the typed
// login reports through.
@Composable
private fun AmberLogin(activity: MainActivity, theme: Theme, onErr: (String) -> Unit) {
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { r ->
        val d = r.data
        when {
            r.resultCode != Activity.RESULT_OK || d == null -> onErr("The signer app said no to the login.")
            d.getStringExtra("result").isNullOrBlank() -> onErr("The signer app gave no key back.")
            else -> {
                val pk = d.getStringExtra("result")!!
                val pkg = (d.getStringExtra("package") ?: "").ifBlank { AMBER_PKG }
                Mobile.login("amber:$pkg:$pk")
            }
        }
    }
    val signers = remember { Amber.signerApps(activity) }
    if (signers.size <= 1) {
        Button(
            onClick = { tryLaunch(launcher, null, onErr) },
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text("Log in with Amber")
        }
    } else {
        var showPicker by remember { mutableStateOf(false) }
        if (showPicker) {
            SignerPickerDialog(activity, theme, { showPicker = false }) { pkg ->
                showPicker = false
                tryLaunch(launcher, pkg, onErr)
            }
        }
        Button(
            onClick = { showPicker = true },
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text("Log in with a signer app")
        }
    }
}

// tryLaunch is the launcher invocation with the failure path built in: no
// installed signer, or one that refuses to open, reads as an error and not
// as a frozen screen.
private fun tryLaunch(
    launcher: androidx.activity.result.ActivityResultLauncher<Intent>,
    pkg: String?,
    onErr: (String) -> Unit,
) {
    try {
        launcher.launch(Amber.loginIntent(pkg))
    } catch (_: Exception) {
        onErr("Could not open the signer app.")
    }
}

// SignerPickerDialog is the chooser for when more than one NIP-55 signer
// app is installed: name and package of each, one tap to pick.
@Composable
private fun SignerPickerDialog(
    activity: MainActivity,
    theme: Theme,
    onDismissRequest: () -> Unit,
    onPick: (String) -> Unit,
) {
    androidx.compose.ui.window.Dialog(onDismissRequest = onDismissRequest) {
        Column(
            Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(12.dp))
                .background(theme.card)
                .padding(20.dp),
        ) {
            Text("Which signer app?", fontWeight = FontWeight.Bold, style = MaterialTheme.typography.titleMedium, color = theme.fg)
            Spacer(Modifier.height(10.dp))
            Amber.signerApps(activity).forEach { app ->
                val pkg = app.activityInfo.packageName
                TextButton(
                    onClick = { onPick(pkg) },
                    contentPadding = PaddingValues(0.dp),
                ) {
                    Column {
                        Text(app.loadLabel(activity.packageManager).toString(), color = theme.fg)
                        Text(pkg, fontSize = 11.sp, color = theme.muted)
                    }
                }
            }
            Spacer(Modifier.height(6.dp))
            TextButton(onClick = onDismissRequest) {
                Text("Cancel", color = theme.chipFg)
            }
        }
    }
}

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
        Spacer(Modifier.height(26.dp))
        OutlinedButton(onClick = { activity.loginWithGoogle() }, modifier = Modifier.fillMaxWidth()) {
            Text("Log in with Google", color = theme.fg)
        }
        if (st.loginStatus.isNotBlank()) {
            Spacer(Modifier.height(8.dp))
            Text(st.loginStatus, color = theme.subtle, style = MaterialTheme.typography.bodySmall)
        }
        Spacer(Modifier.height(12.dp))
        if (Amber.installed(activity)) {
            var amberErr by remember { mutableStateOf("") }
            AmberLogin(activity, theme) { amberErr = it }
            if (amberErr.isNotBlank()) {
                Spacer(Modifier.height(8.dp))
                Text(amberErr, color = theme.danger, style = MaterialTheme.typography.bodySmall)
            }
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
    // tabs: 0 home, 1 windows, 2 installed, 3 discovery. Home is where the
    // launcher opens as long as anything is pinned there.
    var tab by remember {
        mutableStateOf(
            when {
                st.home.isNotEmpty() -> 0
                st.installed.isNotEmpty() -> 2
                else -> 3
            }
        )
    }
    // the bundle editor, over the Windows tab while it is open: from
    // "Create bundle…" there, an Edit there, or holding a bundle on Home
    var bundleEdit by remember { mutableStateOf<BundleEdit?>(null) }
    var relaysEd by remember(st.relays.hashCode()) { mutableStateOf(st.relays.joinToString("\n")) }
    var discoveryFilter by remember { mutableStateOf("") }
    var installedFilter by remember { mutableStateOf("") }
    // ephemeral detail tabs: vanish the moment any other tab is picked
    var detailNapp by remember { mutableStateOf<Napp?>(null) }
    var detailProfile by remember { mutableStateOf<String?>(null) }

    fun pickTab(t: Int) {
        tab = t
        detailNapp = null
        detailProfile = null
    }

    fun openNapp(n: Napp) {
        detailNapp = n
        detailProfile = null
    }

    fun openProfile(pubkeyHex: String) {
        if (pubkeyHex.isBlank()) return
        detailProfile = pubkeyHex
        detailNapp = null
    }

    // a pasted code or a nostr: link that came out as a user or a napp
    // opens its page here
    val opened = activity.openedPage
    LaunchedEffect(opened) {
        if (opened == null) return@LaunchedEffect
        activity.openedPage = null
        when {
            opened.kind == "profile" -> openProfile(opened.pubkey)
            opened.kind == "napp" && opened.napp != null -> openNapp(opened.napp)
        }
    }
    var showOpenCode by remember { mutableStateOf(false) }

    // keep the detail napp fresh (install/uninstall/update reflect immediately)
    val freshDetail = detailNapp?.let { d ->
        st.installed.firstOrNull { it.id == d.id }
            ?: st.previews.firstOrNull { it.id == d.id }
            ?: st.discovery.firstOrNull { it.id == d.id }
            ?: d
    }

    Box(Modifier.fillMaxSize()) {
        Column(Modifier.fillMaxSize().padding(16.dp)) {
            // tabs, with the ephemeral one at the end
            Row(Modifier.horizontalScroll(rememberScrollState())) {
                TabChip("Home", tab == 0 && detailNapp == null && detailProfile == null, theme) { pickTab(0) }
                Spacer(Modifier.width(8.dp))
                TabChip("Windows", tab == 1 && detailNapp == null && detailProfile == null, theme) { pickTab(1) }
                Spacer(Modifier.width(8.dp))
                TabChip("Installed", tab == 2 && detailNapp == null && detailProfile == null, theme) { pickTab(2) }
                Spacer(Modifier.width(8.dp))
                TabChip("Discovery", tab == 3 && detailNapp == null && detailProfile == null, theme) { pickTab(3) }
                if (freshDetail != null) {
                    Spacer(Modifier.width(8.dp))
                    TabChip(freshDetail.name.ifBlank { freshDetail.id }.take(18), true, theme) { }
                } else if (detailProfile != null) {
                    Spacer(Modifier.width(8.dp))
                    TabChip("Profile", true, theme) { }
                }
            }

            Spacer(Modifier.height(12.dp))

            when {
                freshDetail != null -> NappDetailScreen(activity, st, theme, freshDetail,
                    onBack = { detailNapp = null },
                    onAuthor = { openProfile(it) },
                    onOpenNapp = { openNapp(it) })
                detailProfile != null -> AuthorProfileDetailScreen(activity, st, theme, detailProfile!!,
                    onBack = { detailProfile = null },
                    onOpenNapp = { openNapp(it) })
                tab == 0 -> HomeTab(activity, st, theme, onDetail = { openNapp(it) }, onEditBundle = {
                    bundleEdit = BundleEdit(it.name, activity.bundleDraft(it.name))
                    pickTab(1)
                })
                tab == 1 -> WindowsTab(activity, st, theme, bundleEdit, { bundleEdit = it })
                tab == 2 -> InstalledTab(activity, st, theme, installedFilter, { installedFilter = it },
                    onDetail = { openNapp(it) }, onAuthor = { openProfile(it) })
                else -> DiscoveryTab(activity, st, theme, relaysEd, { relaysEd = it }, discoveryFilter, { discoveryFilter = it },
                    onDetail = { openNapp(it) }, onAuthor = { openProfile(it) })
            }
        }
        // "+": open a pasted npub, nprofile, nevent, naddr or note
        FloatingActionButton(
            onClick = { showOpenCode = true },
            containerColor = theme.suggestBg,
            contentColor = theme.suggestFg,
            modifier = Modifier.align(Alignment.BottomEnd).padding(16.dp),
        ) { Text("+", fontSize = 24.sp) }
    }
    if (showOpenCode) {
        OpenCodeDialog(activity, theme, onDismiss = { showOpenCode = false })
    }
}

// OpenCodeDialog takes a pasted nostr code: a user opens their page, a napp
// its page, any other event goes to a napp that views its kind (see
// MainActivity.openCode). It stays up with the error when that fails.
@Composable
private fun OpenCodeDialog(activity: MainActivity, theme: Theme, onDismiss: () -> Unit) {
    var input by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var err by remember { mutableStateOf("") }
    fun submit() {
        if (input.isBlank() || busy) return
        busy = true
        err = ""
        activity.openCode(input.trim()) { e ->
            busy = false
            if (e == null) onDismiss() else err = e
        }
    }
    androidx.compose.ui.window.Dialog(onDismissRequest = onDismiss) {
        Column(
            Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(12.dp))
                .background(theme.card)
                .padding(20.dp),
        ) {
            Text("Open", fontWeight = FontWeight.Bold, style = MaterialTheme.typography.titleMedium, color = theme.fg)
            Spacer(Modifier.height(8.dp))
            OutlinedTextField(
                value = input,
                onValueChange = { input = it; err = "" },
                placeholder = { Text("npub, nprofile, nevent, naddr…") },
                singleLine = true,
                enabled = !busy,
                colors = outlinedColors(theme),
                keyboardOptions = androidx.compose.foundation.text.KeyboardOptions(
                    imeAction = androidx.compose.ui.text.input.ImeAction.Go,
                ),
                keyboardActions = androidx.compose.foundation.text.KeyboardActions(onGo = { submit() }),
                modifier = Modifier.fillMaxWidth(),
            )
            if (err.isNotBlank()) {
                Spacer(Modifier.height(8.dp))
                Text(err, color = theme.danger, style = MaterialTheme.typography.bodySmall)
            }
            Spacer(Modifier.height(16.dp))
            Row {
                Button(
                    onClick = { submit() },
                    enabled = !busy && input.isNotBlank(),
                    colors = ButtonDefaults.buttonColors(containerColor = theme.suggestBg, contentColor = theme.suggestFg),
                ) { Text(if (busy) "Opening…" else "Open") }
                Spacer(Modifier.width(12.dp))
                Button(
                    onClick = onDismiss,
                    colors = ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.chipFg),
                ) { Text("Cancel") }
            }
        }
    }
}

// ProfileScreen holds everything about the current user that used to live
// on top of the launcher: picture, name, pubkey, theme switch, relay auth and
// logout. Leaving it is the system back gesture.
@Composable
fun ProfileScreen(activity: MainActivity, st: LauncherState) {
    val theme = themeByName(st.theme)
    var showLogoutConfirm by remember { mutableStateOf(false) }

    Column(
        Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(16.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Avatar(st.profilePicture, theme, 64)
            Spacer(Modifier.width(16.dp))
            Column(Modifier.weight(1f)) {
                Text(
                    st.profileName.ifBlank { "…" },
                    style = MaterialTheme.typography.titleLarge,
                    fontWeight = FontWeight.Bold,
                    color = theme.fg,
                    maxLines = 2,
                )
                if (st.pubkey.isNotBlank()) {
                    Spacer(Modifier.height(4.dp))
                    Text(
                        st.pubkey,
                        color = theme.muted,
                        fontSize = 11.sp,
                        maxLines = 2,
                    )
                }
            }
        }
        Spacer(Modifier.height(24.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("Theme", color = theme.fg, fontWeight = FontWeight.Bold, modifier = Modifier.weight(1f))
            TextButton(onClick = { activity.toggleTheme() }) {
                Text(if (theme.name == "dark") "Light" else "Dark", color = theme.chipFg, fontSize = 13.sp)
            }
        }
        Spacer(Modifier.height(8.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text("Authenticate when reading", color = theme.fg, fontWeight = FontWeight.Bold)
                Text(
                    "Tell relays that ask for it who you are. Publishing always does.",
                    color = theme.muted,
                    fontSize = 12.sp,
                )
            }
            Spacer(Modifier.width(12.dp))
            Switch(checked = st.readAuth, onCheckedChange = { activity.setReadAuth(it) })
        }
        Spacer(Modifier.height(8.dp))
        TextButton(
            onClick = { showLogoutConfirm = true },
            contentPadding = PaddingValues(0.dp),
        ) {
            Text("Log out", fontSize = 14.sp, color = theme.danger)
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
}

// appMatches: the filter the two tabs share — a case-insensitive substring
// match on name, description, author pubkey and author name (the name the
// backend resolves into every napp it sends over).
private fun Napp.matchesQuery(q: String): Boolean =
    name.contains(q, ignoreCase = true) ||
        description.contains(q, ignoreCase = true) ||
        author.contains(q, ignoreCase = true) ||
        authorName.contains(q, ignoreCase = true)

private fun List<Napp>.matching(filter: String): List<Napp> =
    filter.trim().let { q ->
        if (q.isEmpty()) this else filter { n -> n.matchesQuery(q) }
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
private fun InstalledTab(activity: MainActivity, st: LauncherState, theme: Theme, filter: String, setFilter: (String) -> Unit,
    onDetail: (Napp) -> Unit, onAuthor: (String) -> Unit) {
    Column(Modifier.fillMaxSize()) {
        // the filter box, matching on name, description, author pubkey and
        // author name (cards stay keyed by id either way)
        OutlinedTextField(
            value = filter,
            onValueChange = setFilter,
            modifier = Modifier.fillMaxWidth(),
            placeholder = { Text("filter by name, author or description", color = theme.inputHint) },
            colors = outlinedColors(theme),
            singleLine = true,
        )
        Spacer(Modifier.height(8.dp))
        val visible = st.installed.matching(filter)
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
        if (visible.isEmpty()) {
            Text("Nothing matches the filter.", color = theme.muted)
            return
        }
        LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.weight(1f)) {
            items(visible, key = { it.id }) { napp ->
                val busy = st.busy.contains(napp.id)
                val pinned = st.pinned.any { it.id == napp.id }
                NappCard(
                    activity, napp, theme,
                    onDetail = { onDetail(napp) },
                    onAuthor = { onAuthor(napp.author) },
                    onLaunch = { activity.launch(napp.id) },
                    showOpen = true,
                    primaryLabel = if (busy) "Working…" else "Uninstall",
                    onPrimary = { activity.uninstall(napp.id) },
                    secondaryLabel = if (napp.updateAvailable) "Update" else null,
                    onSecondary = { activity.update(napp.id) },
                    pinned = pinned,
                    onPin = { if (pinned) activity.unpin(napp.id) else activity.pin(napp.id) },
                )
            }
        }
        Spacer(Modifier.height(8.dp))
        val updates = st.installed.count { it.updateAvailable }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(
                onClick = { activity.checkForUpdates() },
                enabled = !st.updateCheckRunning,
                modifier = Modifier.weight(1f),
                colors = ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.chipFg),
            ) {
                Text(if (st.updateCheckRunning) "Checking…" else "Check for updates")
            }
            // only when a check found something
            if (updates > 0) {
                Button(onClick = { activity.updateAll() }, modifier = Modifier.weight(1f)) {
                    Text("Update all ($updates)")
                }
            }
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
    filter: String,
    setFilter: (String) -> Unit,
    onDetail: (Napp) -> Unit,
    onAuthor: (String) -> Unit,
) {
    // the filter matches on name, description, author pubkey and author
    // name (see matchesQuery); applied to the snapshot's list, cards stay
    // keyed by id either way
    val visible = st.discovery.matching(filter)
    LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        item {
            // the filter comes first
            OutlinedTextField(
                value = filter,
                onValueChange = setFilter,
                modifier = Modifier.fillMaxWidth(),
                placeholder = { Text("filter by name, author or description", color = theme.inputHint) },
                colors = outlinedColors(theme),
                singleLine = true,
            )
            Spacer(Modifier.height(10.dp))
            Column {
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
            }
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
            if (visible.isEmpty()) {
                Text(
                    when {
                        st.fetching -> "Searching relays…"
                        st.discovery.isNotEmpty() -> "Nothing matches the filter."                        else -> "No napps yet. Tap \"Fetch napps\"."
                    },
                    color = theme.muted,
                )
            }
        }
        items(visible, key = { it.id }) { napp ->
            val b = cardButtons(st, napp)
            NappCard(
                activity, napp, theme,
                onDetail = { onDetail(napp) },
                onAuthor = { onAuthor(napp.author) },
                onLaunch = { onCardOpen(activity, st, napp) },
                showOpen = b.open,
                openLabel = b.openLabel,
                primaryLabel = b.primary,
                onPrimary = { onCardPrimary(activity, st, napp) },
                secondaryLabel = b.secondary,
                onSecondary = { onCardSecondary(activity, st, napp) },
            )
        }
    }
}

// CardButtons is what a discovery or profile card offers for a napp: Open
// for an installed napp and Preview for any other (downloaded already or
// not), the primary button (install/uninstall) and the second one (update,
// when it is on disk and has one).
private data class CardButtons(val open: Boolean, val openLabel: String, val primary: String, val secondary: String?)

private fun cardButtons(st: LauncherState, napp: Napp): CardButtons {
    val installed = st.installed.firstOrNull { it.id == napp.id }
    val local = installed ?: st.previews.firstOrNull { it.id == napp.id }
    val openLabel = if (installed != null) "Open" else "Preview"
    if (st.busy.contains(napp.id)) return CardButtons(installed != null, openLabel, "Working…", null)
    return CardButtons(
        open = true,
        openLabel = openLabel,
        primary = if (installed != null) "Uninstall" else "Install",
        secondary = if (local?.updateAvailable == true) "Update" else null,
    )
}

// onCardOpen opens an installed napp, previews any other (the backend just
// opens one that is already downloaded)
private fun onCardOpen(activity: MainActivity, st: LauncherState, napp: Napp) {
    if (st.installed.any { it.id == napp.id }) activity.launch(napp.id)
    else if (!st.busy.contains(napp.id)) activity.preview(napp.id)
}

private fun onCardPrimary(activity: MainActivity, st: LauncherState, napp: Napp) {
    if (st.busy.contains(napp.id)) return
    if (st.installed.any { it.id == napp.id }) activity.uninstall(napp.id) else activity.install(napp.id)
}

private fun onCardSecondary(activity: MainActivity, st: LauncherState, napp: Napp) {
    if (cardButtons(st, napp).secondary == "Update") activity.update(napp.id)
}

@Composable
@OptIn(ExperimentalLayoutApi::class)
private fun NappCard(
    activity: MainActivity,
    napp: Napp,
    theme: Theme,
    primaryLabel: String,
    onPrimary: () -> Unit,
    secondaryLabel: String? = null,
    onSecondary: (() -> Unit)? = null,
    // onDetail makes the whole card tappable to open the napp page; onAuthor
    // is the only tap that leads to a profile instead. onLaunch + showOpen
    // draw the Open button in its own color.
    onDetail: (() -> Unit)? = null,
    onAuthor: (() -> Unit)? = null,
    onLaunch: (() -> Unit)? = null,
    showOpen: Boolean = false,
    // openLabel is "Preview" on napps that aren't installed: the same button
    // opens them without installing them
    openLabel: String = "Open",
    // onPin draws the Pin/Unpin toggle for the home tab
    pinned: Boolean = false,
    onPin: (() -> Unit)? = null,
) {
    val bitmap = rememberNappIcon(activity, napp)

    Column(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(8.dp))
            .background(theme.card)
            .clickable(enabled = onDetail != null) { onDetail?.invoke() }
            .padding(12.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            NappIcon(bitmap, theme, 40)
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(napp.name.ifBlank { napp.id }, fontWeight = FontWeight.Bold, color = theme.fg)
                if (napp.description.isNotBlank()) {
                    Text(napp.description, color = theme.subtle, fontSize = 13.sp, maxLines = 2)
                }
                if (napp.actions.any { it.isNotBlank() }) {
                    Row(
                        Modifier
                            .padding(top = 5.dp)
                            .horizontalScroll(rememberScrollState()),
                        horizontalArrangement = Arrangement.spacedBy(4.dp),
                    ) {
                        napp.actions.filter { it.isNotBlank() }.forEach { action ->
                            Surface(
                                color = theme.chipBg,
                                shape = RoundedCornerShape(5.dp),
                            ) {
                                Text(
                                    action,
                                    modifier = Modifier.padding(horizontal = 6.dp, vertical = 3.dp),
                                    color = theme.chipFg,
                                    fontSize = 11.sp,
                                )
                            }
                        }
                    }
                }
                if (napp.author.isNotBlank()) {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        modifier = Modifier
                            .padding(top = 6.dp)
                            .clickable(enabled = onAuthor != null) { onAuthor?.invoke() },
                    ) {
                        Text(
                            napp.authorName.ifBlank { napp.author.take(16) + "…" }.take(40),
                            color = theme.muted,
                            fontSize = 11.sp,
                            maxLines = 1,
                        )
                    }
                }
            }
        }
        FlowRow(
            modifier = Modifier
                .fillMaxWidth()
                .padding(top = 8.dp),
            horizontalArrangement = Arrangement.spacedBy(6.dp, Alignment.End),
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            if (onPin != null) {
                Button(
                    onClick = onPin,
                    contentPadding = PaddingValues(horizontal = 12.dp, vertical = 6.dp),
                    colors = ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.chipFg),
                ) {
                    Text(if (pinned) "Unpin" else "Pin", fontSize = 13.sp)
                }
            }
            if (showOpen && onLaunch != null) {
                Button(
                    onClick = onLaunch,
                    enabled = primaryLabel != "Working…",
                    contentPadding = PaddingValues(horizontal = 12.dp, vertical = 6.dp),
                    colors = ButtonDefaults.buttonColors(containerColor = theme.suggestBg, contentColor = theme.suggestFg),
                ) {
                    Text(openLabel, fontSize = 13.sp)
                }
            }
            if (secondaryLabel != null && onSecondary != null) {
                Button(
                    onClick = onSecondary,
                    enabled = primaryLabel != "Working…",
                    contentPadding = PaddingValues(horizontal = 12.dp, vertical = 6.dp),
                    colors = ButtonDefaults.buttonColors(containerColor = theme.accent, contentColor = theme.accentText),
                ) {
                    Text(secondaryLabel, fontSize = 13.sp)
                }
            }
            Button(
                onClick = onPrimary,
                enabled = primaryLabel != "Working…",
                contentPadding = PaddingValues(horizontal = 12.dp, vertical = 6.dp),
            ) {
                Text(primaryLabel, fontSize = 13.sp)
            }
        }
    }
}

// rememberNappIcon loads a napp's icon off the main thread, keyed by blob
// hash like the desktop.
@Composable
private fun rememberNappIcon(activity: MainActivity, napp: Napp): ImageBitmap? {
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
    return bitmap
}

// HomeTab is the pinned napps and bundles as a home-screen grid: the icon
// with its name below, a tap opens it (a bundle opens all of its windows), a
// long press picks it up to drag it somewhere else, and holding it still for
// a while opens the napp's page (or the bundle's editor) right there.
@Composable
private fun HomeTab(
    activity: MainActivity,
    st: LauncherState,
    theme: Theme,
    onDetail: (Napp) -> Unit,
    onEditBundle: (BundleInfo) -> Unit,
) {
    if (st.home.isEmpty()) {
        Text("Nothing pinned yet. Pin napps from the Installed tab to have them here.", color = theme.muted)
        return
    }
    // the grid is drawn from a local copy that the drag reorders as it
    // goes; the backend only hears about the final position, on drop. A
    // fresh snapshot with a different set or order replaces the copy.
    var order by remember(st.home.map { it.key }) { mutableStateOf(st.home) }
    val gridState = rememberLazyGridState()
    val reorderState = rememberReorderableLazyGridState(gridState) { from, to ->
        order = order.toMutableList().apply { add(to.index, removeAt(from.index)) }
    }
    LazyVerticalGrid(
        columns = GridCells.Adaptive(84.dp),
        state = gridState,
        verticalArrangement = Arrangement.spacedBy(16.dp),
        modifier = Modifier.fillMaxSize(),
    ) {
        gridItems(order, key = { it.key }) { item ->
            // where a long press picked it up, to tell a move from a drop
            // back in place
            var from by remember { mutableStateOf(-1) }
            ReorderableItem(reorderState, key = item.key) { dragging ->
                Column(
                    Modifier
                        .clip(RoundedCornerShape(8.dp))
                        .background(if (dragging) theme.card else Color.Transparent)
                        .clickable {
                            if (item.bundle != null) activity.runBundle(item.bundle.name)
                            else if (item.napp != null) activity.launch(item.napp.id)
                        }
                        .onHold {
                            if (item.bundle != null) onEditBundle(item.bundle)
                            else item.napp?.let(onDetail)
                        }
                        .longPressDraggableHandle(
                            onDragStarted = { from = order.indexOfFirst { it.key == item.key } },
                            onDragStopped = {
                                val to = order.indexOfFirst { it.key == item.key }
                                if (to >= 0 && to != from) activity.movePin(item.key, to)
                            },
                        )
                        .padding(6.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    if (item.bundle != null) BundleIcon(activity, st, item.bundle, theme, 56)
                    else if (item.napp != null) NappIcon(rememberNappIcon(activity, item.napp), theme, 56)
                    Spacer(Modifier.height(6.dp))
                    Text(
                        item.bundle?.name ?: item.napp?.name?.ifBlank { item.napp.id } ?: item.key,
                        color = theme.fg,
                        fontSize = 12.sp,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                        textAlign = TextAlign.Center,
                    )
                }
            }
        }
    }
}

// holdMillis is how long a home tile has to be held still to open its page,
// well past the long press that picks it up for dragging.
private const val holdMillis = 2000L

// onHold fires once the pointer has stayed down on this element for
// holdMillis without wandering off. It only watches, never consumes, so the
// tap and the long-press drag on the same element work as before. Movement
// is measured on screen, not in the element: a tile being dragged follows
// the finger, so the finger hardly moves relative to it.
private fun Modifier.onHold(onHold: () -> Unit): Modifier = composed {
    val coords = remember { mutableStateOf<LayoutCoordinates?>(null) }
    val current by rememberUpdatedState(onHold)
    this
        .onGloballyPositioned { coords.value = it }
        .pointerInput(Unit) {
            val tolerance = 12.dp.toPx()
            fun onScreen(p: Offset) = coords.value?.takeIf { it.isAttached }?.localToRoot(p) ?: p
            awaitEachGesture {
                val down = awaitFirstDown(requireUnconsumed = false, pass = PointerEventPass.Initial)
                val start = onScreen(down.position)
                val interrupted = withTimeoutOrNull(holdMillis) {
                    while (true) {
                        val change = awaitPointerEvent(PointerEventPass.Initial).changes
                            .firstOrNull { it.id == down.id } ?: break
                        if (!change.pressed) break
                        if ((onScreen(change.position) - start).getDistance() > tolerance) break
                    }
                }
                if (interrupted == null) current()
            }
        }
}

// BundleIcon is a bundle on the home tab: its napps' icons, up to four, two
// by two on a tile, so it reads as a folder of the napps it opens.
@Composable
private fun BundleIcon(activity: MainActivity, st: LauncherState, bundle: BundleInfo, theme: Theme, size: Int) {
    val napps = bundle.entries
        .mapNotNull { e -> st.installed.firstOrNull { it.id == e.nappId } }
        .take(4)
    val pad = size / 10
    val gap = size / 14
    val cell = (size - 2 * pad - gap) / 2
    Box(
        Modifier
            .size(size.dp)
            .clip(RoundedCornerShape(10.dp))
            .background(theme.chipBg)
            .padding(pad.dp),
    ) {
        napps.forEachIndexed { i, napp ->
            Box(Modifier.offset(x = ((i % 2) * (cell + gap)).dp, y = ((i / 2) * (cell + gap)).dp)) {
                NappIcon(rememberNappIcon(activity, napp), theme, cell)
            }
        }
    }
}

// ─── the Windows tab ─────────────────────────────────────────────

// BundleEdit is the bundle editor's content: oldName is the stored bundle
// being edited, "" for a new one.
data class BundleEdit(val oldName: String, val draft: List<BundleDraftEntry>)

// WindowsTab is every napp window of this run, open or closed (a closed one
// can be reopened, back on what it was showing), with checkboxes to make a
// bundle of some of them, and the bundles made so far below.
@Composable
private fun WindowsTab(
    activity: MainActivity,
    st: LauncherState,
    theme: Theme,
    edit: BundleEdit?,
    setEdit: (BundleEdit?) -> Unit,
) {
    var checked by remember { mutableStateOf(setOf<String>()) }
    var bundleErr by remember { mutableStateOf("") }

    if (edit != null) {
        BundleEditor(activity, theme, edit,
            onCancel = { setEdit(null); bundleErr = "" },
            onSaved = { setEdit(null); checked = setOf() })
        return
    }

    // a bundle outlives the windows it was made from, so this is empty only
    // when there are neither
    if (st.managedWindows.isEmpty() && st.bundles.isEmpty()) {
        Text("No windows opened yet.", color = theme.muted)
        return
    }

    // checks of windows that are gone (their napp uninstalled) don't count
    val picked = st.managedWindows.map { it.instance }.filter { it in checked }

    LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.fillMaxSize()) {
        if (bundleErr.isNotBlank()) {
            item { Text(bundleErr, color = theme.danger, fontSize = 13.sp) }
        }
        if (picked.isNotEmpty()) {
            item {
                Button(onClick = {
                    val draft = activity.bundleDraftFromWindows(picked)
                    if (draft.isNotEmpty()) {
                        bundleErr = ""
                        setEdit(BundleEdit("", draft))
                    }
                }) {
                    Text("Create bundle of ${picked.size}…", fontSize = 13.sp)
                }
            }
        }
        items(st.managedWindows, key = { "w:" + it.instance }) { w ->
            WindowRow(activity, w, theme,
                checked = w.instance in checked,
                onCheck = { on -> checked = if (on) checked + w.instance else checked - w.instance },
                onOpen = { if (w.open) activity.bringWindow(w.instance) else activity.reopenWindow(w.instance) },
                onClose = { activity.closeWindow(w.instance) })
        }
        if (st.bundles.isNotEmpty()) {
            item {
                Text("Bundles", color = theme.subtle, fontWeight = FontWeight.Bold, fontSize = 14.sp,
                    modifier = Modifier.padding(top = 8.dp))
            }
            items(st.bundles, key = { "b:" + it.name }) { b ->
                BundleRow(st, b, theme,
                    inAppMenu = b.name in activity.appMenuBundles,
                    onRun = { activity.runBundle(b.name) },
                    onPin = { if (b.pinned) activity.unpinBundle(b.name) else activity.pinBundle(b.name) },
                    onHomeScreen = if (activity.canPinToHomeScreen()) ({ activity.pinToHomeScreen(b) }) else null,
                    onAppMenu = { activity.toggleAppMenu(b) },
                    onEdit = {
                        bundleErr = ""
                        setEdit(BundleEdit(b.name, activity.bundleDraft(b.name)))
                    },
                    onDelete = { activity.deleteBundle(b.name) })
            }
        }
    }
}

// WindowRow is one window: tap to bring it up (or reopen it, if closed), the
// checkbox puts it into the next bundle, and the pills are where it has been.
@Composable
@OptIn(ExperimentalLayoutApi::class)
private fun WindowRow(
    activity: MainActivity,
    w: WindowInfo,
    theme: Theme,
    checked: Boolean,
    onCheck: (Boolean) -> Unit,
    onOpen: () -> Unit,
    onClose: () -> Unit,
) {
    Row(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(8.dp))
            .background(theme.card)
            .clickable(onClick = onOpen)
            .padding(start = 4.dp, end = 8.dp, top = 6.dp, bottom = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Checkbox(checked = checked, onCheckedChange = onCheck)
        Column(Modifier.weight(1f)) {
            Text(
                w.name.ifBlank { w.nappId },
                color = if (w.open) theme.fg else theme.muted,
                fontWeight = FontWeight.Bold,
                fontSize = 14.sp,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                w.instance + (if (w.open) "" else " · closed"),
                color = theme.muted,
                fontSize = 11.sp,
                maxLines = 1,
            )
            if (w.history.isNotEmpty()) {
                FlowRow(
                    Modifier.padding(top = 4.dp),
                    horizontalArrangement = Arrangement.spacedBy(4.dp),
                    verticalArrangement = Arrangement.spacedBy(4.dp),
                ) {
                    // the same action twice in a row says nothing a single one doesn't
                    w.history.filterIndexed { i, a -> i == 0 || w.history[i - 1] != a }
                        .forEach { ActionPill(activity, it, theme) }
                }
            }
        }
        if (w.open) {
            TextButton(onClick = onClose) { Text("Close", color = theme.muted, fontSize = 13.sp) }
        } else {
            TextButton(onClick = onOpen) { Text("Reopen", color = theme.accent, fontSize = 13.sp) }
        }
    }
}

// ActionPill is one action as a pill: its name, then whatever of the payload
// is worth showing (see describeAction), and a × at the end when onRemove is
// given.
@Composable
private fun ActionPill(activity: MainActivity, a: BundleAction, theme: Theme, onRemove: (() -> Unit)? = null) {
    val d = remember(a) { describeAction(a) }
    Surface(color = theme.chipBg, shape = RoundedCornerShape(8.dp)) {
        Row(
            Modifier.padding(start = 6.dp, end = if (onRemove == null) 6.dp else 2.dp, top = 3.dp, bottom = 3.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            val withDetail = d.text.isNotBlank() || d.pubkey.isNotBlank()
            Text(if (withDetail) d.type + ":" else d.type, color = theme.chipFg, fontSize = 11.sp,
                fontWeight = FontWeight.Bold, maxLines = 1)
            if (d.text.isNotBlank()) {
                Text(d.text, color = theme.chipFg, fontSize = 11.sp, maxLines = 1)
            }
            if (d.pubkey.isNotBlank()) {
                val p = rememberPillProfile(activity, d.pubkey)
                if (d.avatar) Avatar(p?.picture ?: "", theme, 14)
                val name = p?.shortName?.ifBlank { null }?.let { cut(it, 20) } ?: (d.pubkey.take(8) + "…")
                Text(name, color = theme.chipFg, fontSize = 11.sp, maxLines = 1)
            }
            if (onRemove != null) {
                Text(
                    "×",
                    color = theme.muted,
                    fontSize = 14.sp,
                    fontWeight = FontWeight.Bold,
                    modifier = Modifier
                        .clip(RoundedCornerShape(4.dp))
                        .clickable(onClick = onRemove)
                        .padding(horizontal = 4.dp),
                )
            }
        }
    }
}

// PillDesc is what an action pill says: the action name, then a readable
// summary of the payload for the common actions (see NAPP.md) or its JSON,
// cut short, for anything else. With pubkey the pill ends with that user's
// name (and picture, with avatar).
private data class PillDesc(
    val type: String,
    val text: String = "",
    val pubkey: String = "",
    val avatar: Boolean = false,
)

private fun describeAction(a: BundleAction): PillDesc {
    if (a.payload.isBlank()) return PillDesc(a.type)
    val v = try { org.json.JSONTokener(a.payload).nextValue() } catch (_: Exception) { null }
    val hex = Regex("^[0-9a-f]{64}$")
    when {
        a.type == "profile" -> if (v is String && hex.matches(v)) {
            return PillDesc(a.type, pubkey = v, avatar = true)
        }
        a.type == "view" || a.type.startsWith("view:") -> {
            // a resolved event (maybe as JSON in a string), or, only for a
            // plain view, a nevent/naddr
            val ev = when {
                v is org.json.JSONObject -> v
                v is String && v.trimStart().startsWith("{") ->
                    try { org.json.JSONObject(v) } catch (_: Exception) { null }
                else -> null
            }
            if (ev != null && ev.has("kind")) {
                val pk = ev.optString("pubkey")
                // the kind is said in the text already
                return if (hex.matches(pk)) PillDesc("view", "kind ${ev.optInt("kind")} from", pubkey = pk)
                else PillDesc("view", "kind ${ev.optInt("kind")}")
            }
            if (v is String) return PillDesc(a.type, cut(v.removePrefix("nostr:"), 24))
        }
        a.type == "wiki-term" || a.type == "hashtag" -> {
            val field = if (a.type == "wiki-term") "term" else "value"
            val t = when (v) {
                is String -> v
                is org.json.JSONObject -> v.optString(field, "").ifBlank { null }
                else -> null
            }
            if (t != null) return PillDesc(a.type, cut(t, 32))
        }
        a.type == "user-search" -> if (v is String) return PillDesc(a.type, cut(v, 32))
        a.type == "relay" -> {
            val urls = when (v) {
                is String -> listOf(v)
                is org.json.JSONArray -> (0 until v.length()).map { v.optString(it) }
                else -> listOf()
            }
            if (urls.isNotEmpty()) {
                return PillDesc(a.type, cut(urls.joinToString(", ") {
                    it.removePrefix("wss://").removePrefix("ws://")
                }, 40))
            }
        }
    }
    return PillDesc(a.type, cut(a.payload, 40))
}

private fun cut(s: String, max: Int): String {
    val t = s.trim().split(Regex("\\s+")).joinToString(" ")
    return if (t.length > max) t.take(max) + "…" else t
}

// pillProfiles keeps the profiles pills have loaded, so a list redrawn (or
// scrolled back to) doesn't load them all over again.
private val pillProfiles = ConcurrentHashMap<String, ProfileDetail>()

@Composable
private fun rememberPillProfile(activity: MainActivity, pubkey: String): ProfileDetail? {
    var p by remember(pubkey) { mutableStateOf(pillProfiles[pubkey]) }
    LaunchedEffect(pubkey) {
        if (p == null) {
            activity.loadProfile(pubkey) { loaded ->
                if (loaded != null) {
                    pillProfiles[pubkey] = loaded
                    p = loaded
                }
            }
        }
    }
    return p
}

// BundleRow is one stored bundle: tap to open all of its windows; its napps,
// whether it is on Home and in the Verdana icon's menu, and the buttons that
// change that, put it on the phone's home screen, edit and delete. A
// shortcut on the phone's home screen is the user's to remove from there,
// so there is no button for that.
@Composable
@OptIn(ExperimentalLayoutApi::class)
private fun BundleRow(
    st: LauncherState,
    b: BundleInfo,
    theme: Theme,
    inAppMenu: Boolean,
    onRun: () -> Unit,
    onPin: () -> Unit,
    onHomeScreen: (() -> Unit)?,
    onAppMenu: () -> Unit,
    onEdit: () -> Unit,
    onDelete: () -> Unit,
) {
    var confirmDelete by remember { mutableStateOf(false) }
    val names = b.entries.map { e ->
        st.installed.firstOrNull { it.id == e.nappId }?.name?.ifBlank { null } ?: e.nappId
    }
    Column(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(8.dp))
            .background(theme.card)
            .clickable(onClick = onRun)
            .padding(12.dp),
    ) {
        Text(b.name, color = theme.fg, fontWeight = FontWeight.Bold)
        Text(names.joinToString(" + "), color = theme.muted, fontSize = 12.sp, maxLines = 2,
            overflow = TextOverflow.Ellipsis)
        Text(
            (if (b.pinned) "pinned to Home" else "not pinned") + (if (inAppMenu) " · in the Verdana icon menu" else ""),
            color = theme.subtle, fontSize = 11.sp,
        )
        FlowRow(
            modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
            horizontalArrangement = Arrangement.spacedBy(6.dp, Alignment.End),
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            val small = PaddingValues(horizontal = 12.dp, vertical = 6.dp)
            val chip = ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.chipFg)
            Button(onClick = onPin, contentPadding = small, colors = chip) {
                Text(if (b.pinned) "Unpin" else "Pin", fontSize = 13.sp)
            }
            if (onHomeScreen != null) {
                Button(onClick = onHomeScreen, contentPadding = small, colors = chip) {
                    Text("Add to home screen", fontSize = 13.sp)
                }
            }
            Button(onClick = onAppMenu, contentPadding = small, colors = chip) {
                Text(if (inAppMenu) "Remove from icon menu" else "Add to icon menu", fontSize = 13.sp)
            }
            Button(onClick = onEdit, contentPadding = small, colors = chip) {
                Text("Edit", fontSize = 13.sp)
            }
            Button(onClick = { confirmDelete = true }, contentPadding = small,
                colors = ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.danger)) {
                Text("Delete", fontSize = 13.sp)
            }
        }
    }
    if (confirmDelete) {
        AlertDialog(
            onDismissRequest = { confirmDelete = false },
            title = { Text("Delete ${b.name}?") },
            text = { Text("The bundle is forgotten and taken off Home and the Verdana icon menu. Its napps stay installed.") },
            confirmButton = {
                TextButton(onClick = { confirmDelete = false; onDelete() }) {
                    Text("Delete", color = theme.danger)
                }
            },
            dismissButton = { TextButton(onClick = { confirmDelete = false }) { Text("Cancel") } },
        )
    }
}

// BundleEditor is the bundle's name and, per napp, the actions it gets as
// pills that can be removed, as on the desktop.
@Composable
@OptIn(ExperimentalLayoutApi::class)
private fun BundleEditor(
    activity: MainActivity,
    theme: Theme,
    edit: BundleEdit,
    onCancel: () -> Unit,
    onSaved: () -> Unit,
) {
    var name by remember(edit) { mutableStateOf(edit.oldName) }
    val actions = remember(edit) { edit.draft.map { draftActions(it.lines) }.toMutableStateList() }
    var err by remember(edit) { mutableStateOf("") }
    var saving by remember(edit) { mutableStateOf(false) }

    androidx.activity.compose.BackHandler(onBack = onCancel)

    Column(Modifier.fillMaxSize()) {
        Text(if (edit.oldName.isBlank()) "New bundle" else "Edit bundle",
            color = theme.fg, fontWeight = FontWeight.Bold, fontSize = 18.sp)
        Spacer(Modifier.height(8.dp))
        OutlinedTextField(
            value = name,
            onValueChange = { name = it },
            modifier = Modifier.fillMaxWidth(),
            placeholder = { Text("name of the bundle", color = theme.inputHint) },
            colors = outlinedColors(theme),
            singleLine = true,
        )
        if (err.isNotBlank()) {
            Text(err, color = theme.danger, fontSize = 13.sp, modifier = Modifier.padding(top = 8.dp))
        }
        Spacer(Modifier.height(12.dp))
        LazyColumn(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            items(edit.draft.size) { i ->
                Column {
                    Text("Actions of ${edit.draft[i].label}",
                        color = theme.subtle, fontSize = 13.sp, fontWeight = FontWeight.Bold)
                    Spacer(Modifier.height(4.dp))
                    if (actions[i].isEmpty()) {
                        Text("No actions: the napp just opens.", color = theme.muted, fontSize = 12.sp)
                    } else {
                        FlowRow(
                            horizontalArrangement = Arrangement.spacedBy(4.dp),
                            verticalArrangement = Arrangement.spacedBy(4.dp),
                        ) {
                            actions[i].forEachIndexed { j, a ->
                                ActionPill(activity, a, theme, onRemove = {
                                    actions[i] = actions[i].filterIndexed { k, _ -> k != j }
                                })
                            }
                        }
                    }
                }
            }
        }
        Spacer(Modifier.height(12.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(
                enabled = !saving,
                onClick = {
                    if (name.isBlank()) {
                        err = "give the bundle a name"
                        return@Button
                    }
                    saving = true
                    val draft = edit.draft.mapIndexed { i, e -> e.copy(lines = draftLines(actions[i])) }
                    activity.saveBundle(edit.oldName, name.trim(), draft) { e ->
                        saving = false
                        if (e == null) onSaved() else err = e
                    }
                },
            ) { Text(if (saving) "Saving…" else "Save bundle") }
            Button(
                onClick = onCancel,
                colors = ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.chipFg),
            ) { Text("Cancel") }
        }
    }
}

// draftActions reads a draft entry's lines (one {"type", "payload"} object
// each, see backend/bundle_draft.go) into actions, skipping what doesn't read.
private fun draftActions(lines: String): List<BundleAction> =
    lines.lines().map { it.trim() }.filter { it.isNotEmpty() }.mapNotNull { line ->
        try {
            parseActions(org.json.JSONArray().put(org.json.JSONObject(line))).firstOrNull()
        } catch (_: Exception) { null }
    }

// draftLines is the actions back as the lines the backend reads.
private fun draftLines(actions: List<BundleAction>): String = actions.joinToString("\n") { a ->
    "{\"type\":" + org.json.JSONObject.quote(a.type) +
        (if (a.payload.isBlank()) "" else ",\"payload\":" + a.payload) + "}"
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
fun Avatar(url: String, theme: Theme, size: Int) {
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
internal fun outlinedColors(theme: Theme) = OutlinedTextFieldDefaults.colors(
    focusedBorderColor = theme.accent,
    unfocusedBorderColor = theme.border,
    cursorColor = theme.accent,
    focusedTextColor = theme.fg,
    unfocusedTextColor = theme.fg,
)

// ─── the launcher chrome ────────────────────────────────────────────
//
// A fixed header: the open-napps count box on the left, "Verdana" in the
// middle, the logged-user picture on the right (tap it for the profile
// screen); tapping the count box lists the open napp windows — each one its
// own task now — to bring to the front or close.

@Composable
fun AppHeader(
    st: LauncherState,
    theme: Theme,
    onActivate: (String) -> Unit,
    onClose: (String) -> Unit,
    onProfile: () -> Unit,
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
            // the open-napps count box, left: tap to list and switch
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

            Spacer(Modifier.width(8.dp))

            // the app name in the middle
            Row(Modifier.weight(1f), verticalAlignment = Alignment.CenterVertically) {
                Text("Verdana", fontWeight = FontWeight.Bold, fontSize = 16.sp, color = theme.fg)
            }

            Spacer(Modifier.width(8.dp))

            // the logged-user picture, right: tap for the profile screen
            Box(Modifier.clickable { showList = false; onProfile() }) {
                Avatar(st.profilePicture, theme, 32)
            }
        }

        if (showList) {
            WindowSwitcher(
                st = st,
                theme = theme,
                onPick = { instance ->
                    showList = false
                    onActivate(instance)
                },
                onClose = onClose,
            )
        }
    }
}

@Composable
private fun WindowSwitcher(
    st: LauncherState,
    theme: Theme,
    onPick: (String) -> Unit,
    onClose: (String) -> Unit,
) {
    Column(Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 4.dp)) {
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

// PromptWindow is the prompt as a full-screen overlay over whatever asked
// for it: the launcher when the launcher asked, the napp's own window when
// a napp did. It is a blocking question — back never dismisses it. A picker
// never comes here: it is a window of its own (see PickerWindow).
@Composable
fun PromptWindow(p: Prompt, onAnswer: (Boolean, String, List<Int>) -> Unit) {
    val theme = themeByName(currentThemeName)
    // the items come with a box each, ticked to start with: the answer says
    // which ones the user unticked
    val ticked = remember(p.id) { mutableStateListOf(*Array(p.items.size) { true }) }
    val answer = { ok: Boolean, scope: String ->
        onAnswer(ok, scope, ticked.indices.filter { !ticked[it] })
    }
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
        if (p.items.isNotEmpty()) {
            Spacer(Modifier.height(8.dp))
            Column(
                Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(6.dp))
                    .background(theme.codeBg)
                    .padding(8.dp),
                verticalArrangement = Arrangement.spacedBy(2.dp),
            ) {
                p.items.forEachIndexed { i, item ->
                    Row(
                        Modifier
                            .fillMaxWidth()
                            .clickable { ticked[i] = !ticked[i] },
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Checkbox(
                            checked = ticked[i],
                            onCheckedChange = { ticked[i] = it },
                            modifier = Modifier.size(32.dp),
                        )
                        Spacer(Modifier.width(4.dp))
                        Text(item, color = theme.codeFg, fontSize = 12.sp, fontFamily = FontFamily.Monospace)
                    }
                }
            }
        }
        if (p.warning.isNotBlank()) {
            Spacer(Modifier.height(10.dp))
            Text(
                p.warning,
                modifier = Modifier
                    .fillMaxWidth()
                    .border(1.dp, theme.danger, RoundedCornerShape(6.dp))
                    .padding(8.dp),
                color = theme.danger,
                fontWeight = FontWeight.Bold,
                style = MaterialTheme.typography.bodyMedium,
            )
        }
        // with a summary the payload starts folded under it
        var unfolded by remember(p.id) { mutableStateOf(false) }
        if (p.code.isNotBlank() && p.summary.isNotBlank()) {
            Spacer(Modifier.height(10.dp))
            Text(
                (if (unfolded) "▾ " else "▸ ") + p.summary,
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable { unfolded = !unfolded }
                    .padding(vertical = 4.dp),
                color = theme.muted,
                fontSize = 12.sp,
                fontFamily = FontFamily.Monospace,
            )
        }
        if (p.code.isNotBlank() && (p.summary.isBlank() || unfolded)) {
            Spacer(Modifier.height(if (p.summary.isBlank()) 10.dp else 4.dp))
            Text(
                p.code,
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(6.dp))
                    .background(theme.codeBg)
                    .padding(8.dp),
                color = theme.codeFg,
                fontSize = 12.sp,
                fontFamily = if (p.summary.isBlank()) null else FontFamily.Monospace,
                maxLines = if (p.summary.isBlank()) 8 else Int.MAX_VALUE,
            )
        }
        Spacer(Modifier.weight(1f))
        Spacer(Modifier.height(16.dp))
        if (p.remember) {
            PromptScopes(theme) { ok, scope -> answer(ok, scope) }
        } else {
            Row {
                Button(onClick = { answer(true, "once") }) { Text("Allow") }
                Spacer(Modifier.width(8.dp))
                Button(
                    onClick = { answer(false, "once") },
                    colors = ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.chipFg),
                ) { Text("Deny") }
            }
        }
    }
}

// one of the six answers a permission prompt gets
private data class PromptChoice(val label: String, val ok: Boolean, val scope: String, val loud: Boolean = false)

// PromptScopes is the grid those six answers are drawn in: allow and deny,
// each of them for this prompt only, for this session or always. Verdana
// files the wider answers away, so the next time the napp asks there is no
// prompt to answer.
@Composable
private fun PromptScopes(theme: Theme, onAnswer: (Boolean, String) -> Unit) {
    val rows = listOf(
        listOf(
            PromptChoice("Allow", true, "once", loud = true),
            PromptChoice("Deny", false, "once"),
        ),
        listOf(
            PromptChoice("Allow this session", true, "session"),
            PromptChoice("Deny this session", false, "session"),
        ),
        listOf(
            PromptChoice("Always allow", true, "always"),
            PromptChoice("Always deny", false, "always"),
        ),
    )
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        for (row in rows) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                for (choice in row) {
                    Button(
                        onClick = { onAnswer(choice.ok, choice.scope) },
                        modifier = Modifier.weight(1f),
                        colors = if (choice.loud) ButtonDefaults.buttonColors()
                        else ButtonDefaults.buttonColors(
                            containerColor = theme.chipBg,
                            contentColor = theme.chipFg,
                        ),
                    ) {
                        Text(choice.label, fontSize = 13.sp, maxLines = 1)
                    }
                }
            }
        }
    }
}

@Composable
fun NappDetailScreen(
    activity: MainActivity,
    st: LauncherState,
    theme: Theme,
    napp: Napp,
    onBack: () -> Unit,
    onAuthor: (String) -> Unit,
    onOpenNapp: (Napp) -> Unit,
) {
    val installed = st.installed.any { it.id == napp.id }
    val previewed = st.previews.any { it.id == napp.id }
    val pinned = st.pinned.any { it.id == napp.id }
    val busy = st.busy.contains(napp.id)
    // every time this page opens, look for a newer version of this napp only
    LaunchedEffect(napp.id) { activity.checkForUpdate(napp.id) }
    Column(
        Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState()),
    ) {
        TextButton(onClick = onBack, contentPadding = PaddingValues(0.dp)) {
            Text("← Back", color = theme.muted, fontSize = 13.sp)
        }
        Spacer(Modifier.height(8.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
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
            NappIcon(bitmap, theme, 56)
            Spacer(Modifier.width(12.dp))
            Text(napp.name.ifBlank { napp.id }, fontWeight = FontWeight.Bold,
                style = MaterialTheme.typography.titleLarge, color = theme.fg, modifier = Modifier.weight(1f))
        }
        if (napp.description.isNotBlank()) {
            Spacer(Modifier.height(8.dp))
            Text(napp.description, color = theme.subtle, style = MaterialTheme.typography.bodyMedium)
        }
        if (napp.author.isNotBlank()) {
            Spacer(Modifier.height(10.dp))
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier.clickable { onAuthor(napp.author) },
            ) {
                Text(napp.authorName.ifBlank { napp.author.take(16) + "…" },
                    color = theme.muted, fontSize = 13.sp)
            }
        }
        Spacer(Modifier.height(12.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
            if (installed || !busy) {
                // installed: open it; anything else: preview it (opening
                // it without installing it, downloading it first if needed)
                Button(
                    onClick = { if (installed) activity.launch(napp.id) else activity.preview(napp.id, checkUpdate = false) },
                    colors = ButtonDefaults.buttonColors(containerColor = theme.suggestBg, contentColor = theme.suggestFg),
                ) { Text(if (installed) "Open" else "Preview") }
                Spacer(Modifier.width(8.dp))
            }
            if (installed || previewed) {
                Button(
                    onClick = { if (pinned) activity.unpin(napp.id) else activity.pin(napp.id) },
                    colors = ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.chipFg),
                ) { Text(if (pinned) "Unpin" else "Pin") }
                Spacer(Modifier.width(8.dp))
            }
            if (previewed && !installed && !busy) {
                // a previewed napp has no Uninstall: this removes it
                Button(
                    onClick = { activity.uninstall(napp.id) },
                    colors = ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.chipFg),
                ) { Text("Remove") }
                Spacer(Modifier.width(8.dp))
            }
            Button(
                onClick = { if (installed) activity.uninstall(napp.id) else activity.install(napp.id) },
                enabled = !busy,
                colors = if (installed) ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.chipFg)
                else ButtonDefaults.buttonColors(),
            ) { Text(if (busy) "Working…" else if (installed) "Uninstall" else "Install") }
            if ((installed || previewed) && napp.updateAvailable) {
                Spacer(Modifier.width(8.dp))
                Button(onClick = { activity.update(napp.id) }) { Text("Update") }
            }
        }
        Spacer(Modifier.height(12.dp))
        DetailRow("ID", napp.id, theme)
        if (napp.d.isNotBlank()) DetailRow("d", napp.d, theme)
        DetailRow("Author", napp.author, theme)
        if (napp.actions.any { it.isNotBlank() }) DetailRow("Actions", napp.actions.filter { it.isNotBlank() }.joinToString(", "), theme)
        if (napp.requires.isNotEmpty()) DetailRow("Requires", napp.requires.joinToString(", "), theme)
        if (napp.servers.isNotEmpty()) DetailRow("Servers", napp.servers.joinToString(", "), theme)
        if (napp.icon.isNotBlank()) DetailRow("Icon", napp.icon, theme)
        if (napp.paths.isNotEmpty()) DetailRow("Files", napp.paths.joinToString(", ") { it.path }, theme)
    }
}

@Composable
private fun DetailRow(label: String, value: String, theme: Theme) {
    if (value.isBlank()) return
    Spacer(Modifier.height(4.dp))
    Row {
        Text("$label: ", color = theme.subtle, fontSize = 13.sp)
        Text(value, color = theme.fg, fontSize = 13.sp, modifier = Modifier.weight(1f))
    }
}

@Composable
fun AuthorProfileDetailScreen(
    activity: MainActivity,
    st: LauncherState,
    theme: Theme,
    pubkeyHex: String,
    onBack: () -> Unit,
    onOpenNapp: (Napp) -> Unit,
) {
    var profile by remember(pubkeyHex) { mutableStateOf<ProfileDetail?>(null) }
    var napps by remember(pubkeyHex) { mutableStateOf<List<Napp>?>(null) }
    LaunchedEffect(pubkeyHex) {
        activity.loadProfile(pubkeyHex) { profile = it }
        activity.loadAuthorNapps(pubkeyHex) { napps = it }
    }
    Column(Modifier.fillMaxSize()) {
        TextButton(onClick = onBack, contentPadding = PaddingValues(0.dp)) {
            Text("← Back", color = theme.muted, fontSize = 13.sp)
        }
        Spacer(Modifier.height(8.dp))
        // the avatar and name fire view:0, so the user picks the napp that
        // shows this profile
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.clickable { activity.viewProfile(pubkeyHex) },
        ) {
            Avatar(profile?.picture ?: "", theme, 56)
            Spacer(Modifier.width(12.dp))
            Text(
                (profile?.shortName?.ifBlank { null } ?: pubkeyHex),
                fontWeight = FontWeight.Bold,
                style = MaterialTheme.typography.titleLarge,
                color = theme.fg,
                modifier = Modifier.weight(1f),
                maxLines = 2,
            )
        }
        Spacer(Modifier.height(8.dp))
        Column(Modifier.verticalScroll(rememberScrollState()).weight(1f, fill = false)) {
            if (!profile?.name.isNullOrBlank()) DetailRow("Name", profile!!.name, theme)
            if (!profile?.about.isNullOrBlank()) DetailRow("About", profile!!.about, theme)
            if (!profile?.nip05.isNullOrBlank()) DetailRow("NIP-05", profile!!.nip05, theme)
            if (!profile?.website.isNullOrBlank()) DetailRow("Website", profile!!.website, theme)
            if (!profile?.npub.isNullOrBlank()) DetailRow("npub", profile!!.npub, theme)
            DetailRow("Pubkey", pubkeyHex, theme)
            Spacer(Modifier.height(12.dp))
            Text("Published napps", color = theme.subtle, fontSize = 13.sp, fontStyle = androidx.compose.ui.text.font.FontStyle.Italic)
            Spacer(Modifier.height(6.dp))
            when {
                napps == null -> Text("Fetching napps…", color = theme.muted, fontSize = 13.sp)
                napps!!.isEmpty() -> Text("No napps found on their relays.", color = theme.muted, fontSize = 13.sp)
                else -> napps!!.forEach { n ->
                    val b = cardButtons(st, n)
                    NappCard(
                        activity, n, theme,
                        onDetail = { onOpenNapp(n) },
                        onAuthor = null,
                        onLaunch = { onCardOpen(activity, st, n) },
                        showOpen = b.open,
                        openLabel = b.openLabel,
                        primaryLabel = b.primary,
                        onPrimary = { onCardPrimary(activity, st, n) },
                        secondaryLabel = b.secondary,
                        onSecondary = { onCardSecondary(activity, st, n) },
                    )
                    Spacer(Modifier.height(8.dp))
                }
            }
        }
    }
}
