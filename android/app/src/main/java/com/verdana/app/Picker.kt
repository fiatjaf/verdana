package com.verdana.app

import android.graphics.BitmapFactory
import android.os.SystemClock
import android.util.Base64
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay

// A picker is a napp window before it runs any napp: the backend opened it to
// ask which napp handles an action, and it shows the napps that could as a
// grid of icons, like the home tab, the windows already open first — and
// before them the action's default handler, when a napp configuration gave it
// one: a timed one is picked by itself once its countdown runs out, a
// countdown that grows while the user's finger moves around in it. Once the
// user picks, the backend either closes it or opens the napp picked in it,
// which turns this same window into that napp's (see NappActivity.become).

// pickerFilterOver is how many choices there have to be before the picker
// offers a box to filter them by name.
private const val pickerFilterOver = 4

// pickerMoveDp is how far a touch (or the mouse) has to travel in the window
// to put another pickerMoveDelayMs on a default handler's countdown: someone
// moving around is about to pick something else.
private const val pickerMoveDp = 48
private const val pickerMoveDelayMs = 1000L

// PickerWindow draws prompt p, or, with none, the window waiting: for the
// question to arrive, or for the backend to act on the answer (opening is the
// name of what was picked, then).
@Composable
fun PickerWindow(p: Prompt?, opening: String?, onPick: (Int) -> Unit, onCancel: () -> Unit) {
    val theme = themeByName(currentThemeName)
    if (p == null) {
        Box(Modifier.fillMaxSize().background(theme.bg), contentAlignment = Alignment.Center) {
            Text(if (opening != null) "Opening $opening…" else "…", color = theme.muted)
        }
        return
    }

    var filter by remember(p.id) { mutableStateOf("") }
    var unfolded by remember(p.id) { mutableStateOf(false) }
    val filtering = p.options.size > pickerFilterOver
    val q = filter.trim().lowercase()
    val visible = p.options.withIndex().filter { (_, opt) ->
        !filtering || q.isEmpty() ||
            opt.pickerName().lowercase().contains(q) ||
            opt.detail.lowercase().contains(q) ||
            opt.instance.startsWith(q)
    }
    val default = visible.filter { it.value.default }
    val open = visible.filter { !it.value.default && it.value.instance.isNotBlank() }
    val fresh = visible.filter { !it.value.default && it.value.instance.isBlank() }

    // a timed default: picked when deadline comes, which every pickerMoveDp
    // of movement pushes back
    val defaultIndex = p.options.indexOfFirst { it.default }
    var deadline by remember(p.id) {
        mutableLongStateOf(if (p.countdown > 0 && defaultIndex >= 0) SystemClock.uptimeMillis() + p.countdown else 0L)
    }
    var now by remember(p.id) { mutableLongStateOf(SystemClock.uptimeMillis()) }
    LaunchedEffect(p.id) {
        if (deadline == 0L) return@LaunchedEffect
        // a deadline gone to 0 meanwhile is the countdown stopped
        while (deadline != 0L) {
            now = SystemClock.uptimeMillis()
            if (now >= deadline) {
                deadline = 0L
                onPick(defaultIndex)
                break
            }
            delay(minOf(100L, deadline - now))
        }
    }
    val watch = if (deadline == 0L) Modifier else Modifier.pointerInput(p.id) {
        val step = pickerMoveDp.dp.toPx()
        var moved = 0f
        awaitPointerEventScope {
            while (true) {
                // seen before the grid gets it, and left for the grid
                val ev = awaitPointerEvent(PointerEventPass.Initial)
                for (c in ev.changes) moved += (c.position - c.previousPosition).getDistance()
                while (moved >= step && deadline != 0L) {
                    moved -= step
                    deadline += pickerMoveDelayMs
                }
            }
        }
    }

    LazyVerticalGrid(
        columns = GridCells.Adaptive(88.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
        contentPadding = PaddingValues(20.dp),
        modifier = Modifier.fillMaxSize().background(theme.bg).then(watch),
    ) {
        item(span = { GridItemSpan(maxLineSpan) }) {
            Text(p.title, fontWeight = FontWeight.Bold, fontSize = 18.sp, color = theme.fg)
        }
        if (p.code.isNotBlank()) {
            // what is being sent, folded: it is rarely what the user picks by
            item(span = { GridItemSpan(maxLineSpan) }) {
                Column {
                    Text(
                        (if (unfolded) "▾ " else "▸ ") + "what is being sent",
                        modifier = Modifier.clickable { unfolded = !unfolded }.padding(vertical = 4.dp),
                        color = theme.muted,
                        fontSize = 13.sp,
                    )
                    if (unfolded) {
                        Text(
                            p.code,
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(top = 4.dp)
                                .clip(RoundedCornerShape(6.dp))
                                .background(theme.codeBg)
                                .padding(8.dp),
                            color = theme.codeFg,
                            fontSize = 12.sp,
                            fontFamily = FontFamily.Monospace,
                            maxLines = 8,
                        )
                    }
                }
            }
        }
        if (filtering) {
            item(span = { GridItemSpan(maxLineSpan) }) {
                OutlinedTextField(
                    value = filter,
                    onValueChange = { filter = it },
                    // tapping in here is looking for something else: the
                    // default's countdown stops for good
                    modifier = Modifier.fillMaxWidth().padding(top = 8.dp)
                        .onFocusChanged { if (it.isFocused) deadline = 0L },
                    placeholder = { Text("filter by name", color = theme.inputHint) },
                    colors = outlinedColors(theme),
                    singleLine = true,
                    // done picks the first one left, the napp the user picks
                    // most for this action when it is still there
                    keyboardOptions = KeyboardOptions(imeAction = ImeAction.Go),
                    keyboardActions = KeyboardActions(onGo = { visible.firstOrNull()?.let { onPick(it.index) } }),
                )
            }
        }
        fun section(title: String, cells: List<IndexedValue<PromptOption>>) {
            if (cells.isEmpty()) return
            item(span = { GridItemSpan(maxLineSpan) }) {
                Text(
                    title.uppercase(),
                    modifier = Modifier.padding(top = 12.dp),
                    color = theme.muted,
                    fontSize = 12.sp,
                    fontWeight = FontWeight.Bold,
                )
            }
            items(cells, key = { it.index }) { (i, opt) -> PickerCell(opt, theme) { onPick(i) } }
        }
        if (deadline != 0L && default.isNotEmpty()) {
            val left = ((deadline - now + 999) / 1000).coerceAtLeast(1)
            section("Default · opening in $left second" + if (left == 1L) "" else "s", default)
        } else {
            section("Default", default)
        }
        section("Already open", open)
        section("Open in a new window", fresh)
        if (visible.isEmpty()) {
            item(span = { GridItemSpan(maxLineSpan) }) {
                Text("No napp matches “${filter.trim()}”", color = theme.muted, modifier = Modifier.padding(top = 16.dp))
            }
        }
        item(span = { GridItemSpan(maxLineSpan) }) {
            Box(Modifier.fillMaxWidth().padding(top = 12.dp), contentAlignment = Alignment.CenterEnd) {
                TextButton(onClick = onCancel) { Text("Cancel", color = theme.chipFg) }
            }
        }
    }
}

// PickerCell is one choice: its icon with its name under it, and the window
// for one already open.
@Composable
private fun PickerCell(opt: PromptOption, theme: Theme, onClick: () -> Unit) {
    val ink = when {
        opt.default -> theme.suggestFg
        opt.dev -> theme.devFg
        else -> theme.fg
    }
    val bg = when {
        opt.default -> theme.suggestBg
        opt.dev -> theme.devBg
        else -> Color.Transparent
    }
    Column(
        Modifier
            .clip(RoundedCornerShape(10.dp))
            .background(bg)
            .clickable(onClick = onClick)
            .padding(6.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        PickerIcon(opt, theme)
        Spacer(Modifier.height(6.dp))
        Text(
            opt.pickerName(),
            color = ink,
            fontSize = 12.sp,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
            textAlign = TextAlign.Center,
        )
        if (opt.instance.isNotBlank()) {
            // the heading already says these are windows
            Text(opt.instance, color = theme.muted, fontSize = 10.sp, maxLines = 1)
        }
    }
}

// PickerIcon is the napp's icon cropped to a rounded square, or its initial on
// a tile when it has none.
@Composable
private fun PickerIcon(opt: PromptOption, theme: Theme) {
    val img: ImageBitmap? = remember(opt.icon) {
        if (opt.icon.isBlank()) null else try {
            val bytes = Base64.decode(opt.icon, Base64.DEFAULT)
            BitmapFactory.decodeByteArray(bytes, 0, bytes.size)?.asImageBitmap()
        } catch (_: Exception) {
            null
        }
    }
    val shape = RoundedCornerShape(10.dp)
    if (img != null) {
        Image(
            bitmap = img,
            contentDescription = null,
            modifier = Modifier.size(56.dp).clip(shape),
            contentScale = ContentScale.Crop,
        )
        return
    }
    Box(Modifier.size(56.dp).clip(shape).background(theme.imageBg), contentAlignment = Alignment.Center) {
        Text(
            opt.pickerName().trim().take(1).uppercase().ifBlank { "?" },
            color = theme.muted,
            fontSize = 22.sp,
            fontWeight = FontWeight.Bold,
        )
    }
}

private fun PromptOption.pickerName(): String = name.ifBlank { label }
