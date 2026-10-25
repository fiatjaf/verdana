package com.verdana.app

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

// SettingsScreen is the launcher's settings, from the gear in the header:
// authenticating on reads, what napps get when they ask to sign, to publish
// or to open a link, the default handlers napp configurations set (which can only be
// taken away here), and every configuration the launcher ever loaded, each
// one line that unfolds into what it sets, with a button to apply it.
// Leaving it is the system back gesture.
@Composable
fun SettingsScreen(activity: MainActivity, st: LauncherState) {
    val theme = themeByName(st.theme)
    var unfolded by remember { mutableStateOf(setOf<String>()) }

    Column(
        Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(16.dp),
    ) {
        Text("Settings", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold, color = theme.fg)

        Heading("Relays", theme)
        ChoiceRow(
            "Authenticate when reading",
            "Tell relays that ask for it who you are. Publishing always does.",
            listOf("yes" to "Yes", "no" to "No"),
            if (st.readAuth) "yes" else "no",
            theme,
        ) { activity.setReadAuth(it == "yes") }

        Heading("Permissions", theme)
        val policies = listOf("ask" to "Ask", "allow" to "Allow", "deny" to "Deny")
        ChoiceRow("Napps signing", "Unless you answered for that napp already.", policies, st.signPolicy, theme) {
            activity.setNappPolicy("sign", it)
        }
        Spacer(Modifier.height(10.dp))
        ChoiceRow("Napps publishing", "Unless you answered for that napp already.", policies, st.publishPolicy, theme) {
            activity.setNappPolicy("publish", it)
        }
        Spacer(Modifier.height(10.dp))
        ChoiceRow(
            "Open links without asking",
            "In your browser, unless you answered for that napp already. Non-image shows images over the napp instead.",
            listOf("yes" to "Yes", "non-image" to "Non-image", "no" to "No"),
            st.openLinks,
            theme,
        ) { activity.setOpenLinks(it) }

        Heading("Default handlers", theme)
        if (st.defaultHandlers.isEmpty()) {
            Note("None: every action with more than one napp to take it asks which one. Napp configurations set them.", theme)
        }
        st.defaultHandlers.forEach { h ->
            Row(Modifier.padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(
                        "${h.action} → ${h.targetName.ifBlank { h.target }}",
                        color = theme.fg,
                        fontSize = 14.sp,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    Text(h.mode + if (h.singleton) ", singleton" else "", color = theme.muted, fontSize = 12.sp)
                }
                Spacer(Modifier.width(8.dp))
                Choice("Delete", false, theme) { activity.removeDefaultHandler(h.action) }
            }
        }

        Heading("Napp configurations", theme)
        Note(
            if (st.configurations.isEmpty()) "None loaded yet: open the code of a napp configuration (kind 35133) to see it here."
            else "Every one the launcher has loaded. Tap one to see what it sets.",
            theme,
        )
        st.configurations.forEach { c ->
            val open = c.key in unfolded
            Column(Modifier.padding(vertical = 4.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Column(
                        Modifier
                            .weight(1f)
                            .clickable { unfolded = if (open) unfolded - c.key else unfolded + c.key },
                    ) {
                        Text(
                            (if (open) "▾ " else "▸ ") + c.name,
                            color = theme.fg,
                            fontWeight = FontWeight.Bold,
                            fontSize = 14.sp,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        val by = c.authorName.ifBlank { c.author.take(12) + "…" }
                        val date = SimpleDateFormat("yyyy-MM-dd", Locale.ROOT).format(Date(c.createdAt * 1000))
                        Text(
                            "${c.summary} · by $by · $date" + if (c.applied) " · applied" else "",
                            color = theme.muted,
                            fontSize = 12.sp,
                            maxLines = 2,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                    Spacer(Modifier.width(8.dp))
                    Choice(if (c.applied) "Apply again" else "Apply", false, theme) { activity.applyConfiguration(c.key) }
                }
                if (open) {
                    c.lines.ifEmpty { listOf("nothing") }.forEach { line ->
                        Text("· $line", color = theme.fg, fontSize = 13.sp, modifier = Modifier.padding(start = 16.dp, top = 3.dp))
                    }
                }
            }
        }
    }
}

@Composable
private fun Heading(text: String, theme: Theme) {
    Text(
        text,
        color = theme.subtle,
        fontWeight = FontWeight.Bold,
        fontSize = 14.sp,
        modifier = Modifier.padding(top = 20.dp, bottom = 6.dp),
    )
}

@Composable
private fun Note(text: String, theme: Theme) {
    Text(text, color = theme.muted, fontSize = 12.sp, modifier = Modifier.padding(bottom = 6.dp))
}

// ChoiceRow is a setting and its choices, the one in force highlighted.
@Composable
private fun ChoiceRow(
    label: String,
    detail: String,
    choices: List<Pair<String, String>>,
    current: String,
    theme: Theme,
    onPick: (String) -> Unit,
) {
    Column {
        Text(label, color = theme.fg, fontWeight = FontWeight.Bold)
        Text(detail, color = theme.muted, fontSize = 12.sp)
        Spacer(Modifier.height(4.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            choices.forEach { (value, text) -> Choice(text, current == value, theme) { onPick(value) } }
        }
    }
}

// Choice is a small chip, in the accent colour when it is the one in force.
@Composable
private fun Choice(text: String, selected: Boolean, theme: Theme, onClick: () -> Unit) {
    Text(
        text,
        modifier = Modifier
            .clip(RoundedCornerShape(6.dp))
            .background(if (selected) theme.accent else theme.chipBg)
            .clickable(onClick = onClick)
            .padding(horizontal = 10.dp, vertical = 6.dp),
        color = if (selected) theme.accentText else theme.chipFg,
        fontSize = 13.sp,
    )
}
