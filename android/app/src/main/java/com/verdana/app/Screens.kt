package com.verdana.app

import android.app.Activity
import android.content.Intent
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
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
        Spacer(Modifier.height(8.dp))
        Text("— or —", style = MaterialTheme.typography.bodyMedium, color = theme.subtle)
        Spacer(Modifier.height(16.dp))
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
    var tab by remember { mutableStateOf(if (st.installed.isNotEmpty()) 0 else 1) }
    var discoveryFilter by remember { mutableStateOf("") }
    var discoveryKind by remember { mutableStateOf(DiscoveryKind.All) }
    var installedFilter by remember { mutableStateOf("") }
    // ephemeral detail tabs: vanish the moment any other tab is picked
    var detailNapp by remember { mutableStateOf<Napp?>(null) }
    var detailProfile by remember { mutableStateOf<String?>(null) }

    val requestedArchetype = VerdanaHost.discoveryArchetype
    androidx.compose.runtime.LaunchedEffect(requestedArchetype) {
        if (requestedArchetype.isNotBlank()) {
            tab = 1
            discoveryFilter = "archetype:$requestedArchetype"
            discoveryKind = DiscoveryKind.Napplets
            detailNapp = null
            detailProfile = null
            VerdanaHost.consumeDiscoveryArchetype()
        }
    }

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

    // keep the detail napp fresh (install/uninstall/update reflect immediately)
    val freshDetail = detailNapp?.let { d ->
        st.installed.firstOrNull { it.id == d.id }
            ?: st.discovery.firstOrNull { it.id == d.id }
            ?: d
    }

    Column(Modifier.fillMaxSize().padding(16.dp)) {
        // tabs, with the ephemeral one at the end
        Row {
            TabChip("Installed", tab == 0 && detailNapp == null && detailProfile == null, theme) { pickTab(0) }
            Spacer(Modifier.width(8.dp))
            TabChip("Discovery", tab == 1 && detailNapp == null && detailProfile == null, theme) { pickTab(1) }
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
            tab == 0 -> InstalledTab(activity, st, theme, installedFilter, { installedFilter = it },
                onDetail = { openNapp(it) }, onAuthor = { openProfile(it) })
            else -> DiscoveryTab(activity, st, theme, discoveryFilter, { discoveryFilter = it },
                discoveryKind, { discoveryKind = it },
                onDetail = { openNapp(it) }, onAuthor = { openProfile(it) })
        }
    }
}

// ProfileScreen holds everything about the current user that used to live
// on top of the launcher: picture, name, pubkey, theme switch and logout.
@Composable
fun ProfileScreen(activity: MainActivity, st: LauncherState, onBack: () -> Unit) {
    val theme = themeByName(st.theme)
    var showLogoutConfirm by remember { mutableStateOf(false) }

    Column(
        Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(16.dp),
    ) {
        TextButton(onClick = onBack, contentPadding = PaddingValues(0.dp)) {
            Text("← Back", color = theme.muted, fontSize = 13.sp)
        }
        Spacer(Modifier.height(8.dp))
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
                val next = when (st.themeMode) {
                    "system" -> "Light"
                    "light" -> "Dark"
                    else -> "System"
                }
                Text(next, color = theme.chipFg, fontSize = 13.sp)
            }
        }
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("Relays & servers", color = theme.fg, fontWeight = FontWeight.Bold, modifier = Modifier.weight(1f))
            TextButton(onClick = { VerdanaHost.openLauncherSettings() }) {
                Text("Settings", color = theme.chipFg, fontSize = 13.sp)
            }
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

// DiscoveryKind is which apps the discovery tab lists.
enum class DiscoveryKind(val label: String) {
    All("All"),
    Napps("Napps"),
    Napplets("Napplets");

    fun matches(n: Napp): Boolean = when (this) {
        All -> true
        Napps -> !n.isNapplet
        Napplets -> n.isNapplet
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
                NappCard(
                    activity, napp, theme,
                    showActions = false,
                    onDetail = { onDetail(napp) },
                    onAuthor = { onAuthor(napp.author) },
                    onLaunch = { activity.launch(napp.id) },
                    showOpen = true,
                    onSettings = if (napp.isNapplet) ({ VerdanaHost.openNappSettings(napp.id) }) else null,
                    primaryLabel = if (busy) "Working…" else "Uninstall",
                    onPrimary = { activity.uninstall(napp.id) },
                    secondaryLabel = if (napp.updateAvailable) "Update" else null,
                    onSecondary = { activity.update(napp.id) },
                )
            }
        }
        Spacer(Modifier.height(8.dp))
        Button(
            onClick = { activity.checkForUpdates() },
            enabled = !st.updateCheckRunning,
            modifier = Modifier.fillMaxWidth(),
            colors = ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.chipFg),
        ) {
            Text(if (st.updateCheckRunning) "Checking…" else "Check for updates")
        }
    }
}

@Composable
private fun DiscoveryTab(
    activity: MainActivity,
    st: LauncherState,
    theme: Theme,
    filter: String,
    setFilter: (String) -> Unit,
    kind: DiscoveryKind,
    setKind: (DiscoveryKind) -> Unit,
    onDetail: (Napp) -> Unit,
    onAuthor: (String) -> Unit,
) {
    // the filter matches on name, description, author pubkey and author
    // name (see matchesQuery); applied to the snapshot's list, cards stay
    // keyed by id either way. A napp address (naddr, nostr: link) is looked
    // up on relays instead, and only what it names is listed.
    val isArchetypeFilter = filter.trim().startsWith("archetype:")
    LaunchedEffect(filter) { activity.lookupAddress(if (isArchetypeFilter) "" else filter) }
    val lookup = st.lookup?.takeIf { !isArchetypeFilter && it.query == filter.trim() }
    // The kind tabs narrow the list to napps or napplets, except for an
    // address, which names one app whatever its kind.
    val requestedArchetype = filter.trim().removePrefix("archetype:").takeIf {
        filter.trim().startsWith("archetype:") && it.isNotBlank()
    }
    val visible = if (lookup != null) st.discovery.filter { it.id == lookup.nappId }
    else st.discovery
        .matching(if (requestedArchetype == null) filter else "")
        .filter { kind.matches(it) && (requestedArchetype == null || requestedArchetype in it.archetypes) }
    LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        item {
            // the filter comes first
            OutlinedTextField(
                value = filter,
                onValueChange = setFilter,
                modifier = Modifier.fillMaxWidth(),
                placeholder = { Text("filter by name, author or description, or paste an naddr", color = theme.inputHint) },
                colors = outlinedColors(theme),
                singleLine = true,
            )
            Spacer(Modifier.height(10.dp))
            // the kind tabs, and at the other end the refresh button that
            // asks the relays again
            Row(verticalAlignment = Alignment.CenterVertically) {
                DiscoveryKind.entries.forEach { k ->
                    val count = st.discovery.count { k.matches(it) }
                    TabChip(if (count > 0) "${k.label} ($count)" else k.label, kind == k, theme) { setKind(k) }
                    Spacer(Modifier.width(6.dp))
                }
                Spacer(Modifier.weight(1f))
                Button(
                    onClick = { activity.fetchNapps() },
                    enabled = !st.fetching,
                    contentPadding = PaddingValues(horizontal = 16.dp, vertical = 6.dp),
                ) {
                    Text(if (st.fetching) "Refreshing…" else "Refresh", fontSize = 13.sp)
                }
            }
            if (st.fetchErr.isNotBlank()) {
                Spacer(Modifier.height(6.dp))
                Text(st.fetchErr, color = theme.danger, fontSize = 12.sp)
            }
            Spacer(Modifier.height(12.dp))
            if (visible.isEmpty()) {
                Text(
                    when {
                        lookup?.pending == true -> "Looking up that address…"
                        lookup != null && lookup.err.isNotBlank() -> "Couldn't open that address: ${lookup.err}."
                        st.fetching -> "Searching relays…"
                        st.discovery.isNotEmpty() && filter.isBlank() -> "No ${kind.label.lowercase()} found on these relays."
                        st.discovery.isNotEmpty() -> "Nothing matches the filter."
                        else -> "No napps yet. Pick some good relays in Settings and tap \"Refresh\"."
                    },
                    color = theme.muted,
                )
            }
        }
        items(visible, key = { it.id }) { napp ->
            val installed = st.installed.any { it.id == napp.id }
            val busy = st.busy.contains(napp.id)
            NappCard(
                activity, napp, theme,
                showActions = false,
                onDetail = { onDetail(napp) },
                onAuthor = { onAuthor(napp.author) },
                onLaunch = { if (installed) activity.launch(napp.id) else activity.tryNapplet(napp.id) },
                showOpen = installed || napp.isNapplet,
                openLabel = if (installed) "Open" else "Try",
                primaryLabel = when {
                    busy -> "Working…"
                    installed -> "Uninstall"
                    else -> "Install"
                },
                onPrimary = { if (installed) activity.uninstall(napp.id) else activity.install(napp.id) },
                secondaryLabel = if (installed && napp.updateAvailable) "Update" else null,
                onSecondary = { activity.install(napp.id) },
            )
        }
    }
}

@Composable
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
    onSettings: (() -> Unit)? = null,
    showOpen: Boolean = false,
    openLabel: String = "Open",
    showActions: Boolean = true,
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
            .clickable(enabled = onDetail != null) { onDetail?.invoke() }
            .padding(12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        NappIcon(bitmap, theme, 40)
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(napp.name.ifBlank { napp.id }, fontWeight = FontWeight.Bold, color = theme.fg)
                if (napp.isNapplet) {
                    Spacer(Modifier.width(6.dp))
                    NappletBadge(theme)
                }
            }
            if (napp.description.isNotBlank()) {
                Text(napp.description, color = theme.subtle, fontSize = 13.sp, maxLines = 2)
            }
            if (showActions && napp.actions.any { it.isNotBlank() }) {
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
        Spacer(Modifier.width(8.dp))
        if (showOpen && onLaunch != null) {
            Button(
                onClick = onLaunch,
                enabled = primaryLabel != "Working…",
                contentPadding = PaddingValues(horizontal = 12.dp, vertical = 6.dp),
                colors = ButtonDefaults.buttonColors(containerColor = theme.suggestBg, contentColor = theme.suggestFg),
            ) {
                Text(openLabel, fontSize = 13.sp)
            }
            Spacer(Modifier.width(6.dp))
        }
        if (onSettings != null) {
            Button(
                onClick = onSettings,
                enabled = primaryLabel != "Working…",
                contentPadding = PaddingValues(horizontal = 12.dp, vertical = 6.dp),
                colors = ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.chipFg),
            ) {
                Text("Settings", fontSize = 13.sp)
            }
            Spacer(Modifier.width(6.dp))
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
            Spacer(Modifier.width(6.dp))
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

// iconHash mirrors backend.Napp.IconHash: the icon's blob sha256.
private fun Napp.iconHash(): String = if (isNapplet) iconSha else paths.firstOrNull { p ->
    icon.isNotBlank() && p.path.trimStart('/') == icon.trimStart('/')
}?.sha256 ?: ""

// NappletBadge marks a sandboxed kind:35129 napplet in the lists.
@Composable
private fun NappletBadge(theme: Theme) {
    Text(
        "napplet",
        color = theme.chipFg,
        fontSize = 11.sp,
        modifier = Modifier
            .clip(RoundedCornerShape(5.dp))
            .background(theme.chipBg)
            .padding(horizontal = 4.dp, vertical = 1.dp),
    )
}

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
private fun outlinedColors(theme: Theme) = OutlinedTextFieldDefaults.colors(
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
// a napp did. It is a blocking question — back never dismisses it.
@Composable
fun PromptWindow(p: Prompt, onAnswer: (Boolean, Int, String) -> Unit) {
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
                // the napp the user keeps picking for this action gets its own
                // color: it is why the list is in this order
                val ink = if (opt.suggested) theme.suggestFg else theme.accentText
                Button(
                    onClick = { onAnswer(true, i, "once") },
                    modifier = Modifier.fillMaxWidth().padding(bottom = 6.dp),
                    colors = ButtonDefaults.buttonColors(
                        containerColor = if (opt.suggested) theme.suggestBg else MaterialTheme.colorScheme.primary,
                        contentColor = ink,
                    ),
                ) {
                    if (opt.detail.isNotBlank()) {
                        Column {
                            Text(opt.label, maxLines = 1)
                            Text(
                                opt.detail,
                                fontSize = 11.sp,
                                color = if (opt.suggested) ink else theme.subtle,
                                maxLines = 1,
                            )
                        }
                    } else {
                        Text(opt.label, maxLines = 1)
                    }
                }
            }
            TextButton(onClick = { onAnswer(false, 0, "once") }) {
                Text("Cancel", color = theme.chipFg)
            }
        } else if (p.remember) {
            PromptScopes(theme) { ok, scope -> onAnswer(ok, 0, scope) }
        } else {
            Row {
                Button(onClick = { onAnswer(true, 0, "once") }) { Text("Allow") }
                Spacer(Modifier.width(8.dp))
                Button(
                    onClick = { onAnswer(false, 0, "once") },
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
    val busy = st.busy.contains(napp.id)
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
            if (installed || napp.isNapplet) {
                Button(
                    onClick = { if (installed) activity.launch(napp.id) else activity.tryNapplet(napp.id) },
                    colors = ButtonDefaults.buttonColors(containerColor = theme.suggestBg, contentColor = theme.suggestFg),
                ) { Text(if (installed) "Open" else "Try") }
                Spacer(Modifier.width(8.dp))
            }
            Button(
                onClick = { if (installed) activity.uninstall(napp.id) else activity.install(napp.id) },
                enabled = !busy,
                colors = if (installed) ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.chipFg)
                else ButtonDefaults.buttonColors(),
            ) { Text(if (busy) "Working…" else if (installed) "Uninstall" else "Install") }
            if (installed && napp.updateAvailable) {
                Spacer(Modifier.width(8.dp))
                Button(onClick = { activity.update(napp.id) }) { Text("Update") }
            }
        }
        // its settings window: what it declared (NAP-CONFIG) and what the
        // user let it do; a line of its own, the row above is full on a phone
        if (installed) {
            Spacer(Modifier.height(8.dp))
            Button(
                onClick = { VerdanaHost.openNappSettings(napp.id) },
                colors = ButtonDefaults.buttonColors(containerColor = theme.chipBg, contentColor = theme.chipFg),
            ) { Text("Settings") }
        }
        Spacer(Modifier.height(12.dp))
        DetailRow("ID", napp.id, theme)
        // the naddr is how a napp is shared; a tap copies it
        val address = remember(napp.id) { Mobile.nappAddress(napp.id) }
        if (address.isNotBlank()) {
            Spacer(Modifier.height(4.dp))
            Row(Modifier.clickable { VerdanaHost.copyText(address) }) {
                Text("Address: ", color = theme.subtle, fontSize = 13.sp)
                Text(address, color = theme.fg, fontSize = 13.sp, modifier = Modifier.weight(1f))
            }
        }
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
        Row(verticalAlignment = Alignment.CenterVertically) {
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
                    val installed = st.installed.any { it.id == n.id }
                    val busy = st.busy.contains(n.id)
                    NappCard(
                        activity, n, theme,
                        onDetail = { onOpenNapp(n) },
                        onAuthor = null,
                        onLaunch = { if (installed) activity.launch(n.id) else activity.tryNapplet(n.id) },
                        showOpen = installed || n.isNapplet,
                        openLabel = if (installed) "Open" else "Try",
                        primaryLabel = if (busy) "Working…" else if (installed) "Uninstall" else "Install",
                        onPrimary = { if (installed) activity.uninstall(n.id) else activity.install(n.id) },
                        secondaryLabel = if (installed && n.updateAvailable) "Update" else null,
                        onSecondary = { activity.install(n.id) },
                    )
                    Spacer(Modifier.height(8.dp))
                }
            }
        }
    }
}
