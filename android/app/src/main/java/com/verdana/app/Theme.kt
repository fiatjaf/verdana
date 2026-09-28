package com.verdana.app

// The launcher has one theme at a time and every napp tracks it. This is the
// same palette the Gio desktop uses (see theme.go there), expressed as Compose
// colors; the CSS tokens napps get are derived from the same values.

import androidx.compose.material3.ColorScheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.ui.graphics.Color

data class Theme(
    val name: String,
    val bg: Color,
    val fg: Color,
    val accent: Color,
    val accentText: Color,
    val card: Color,
    val chipBg: Color,
    val chipFg: Color,
    val border: Color,
    val codeBg: Color,
    val codeFg: Color,
    val subtle: Color,
    val muted: Color,
    val danger: Color,
    val imageBg: Color,
    val inputHint: Color,
    val devBg: Color,
    val devFg: Color,
)

fun cssHex(c: Color): String = String.format(
    "#%02x%02x%02x",
    (c.red * 255).toInt(),
    (c.green * 255).toInt(),
    (c.blue * 255).toInt(),
)

val lightTheme = Theme(
    name = "light",
    bg = Color(0xFFFFFFFF),
    fg = Color(0xFF000000),
    accent = Color(0xFF3F51B5),
    accentText = Color(0xFFFFFFFF),
    card = Color(0xFFF2F2F2),
    chipBg = Color(0xFFE8E8E8),
    chipFg = Color(0xFF333333),
    border = Color(0xFFCCCCCC),
    codeBg = Color(0xFFF0F0F0),
    codeFg = Color(0xFF333333),
    subtle = Color(0xFF666666),
    muted = Color(0xFF999999),
    danger = Color(0xFFCC2222),
    imageBg = Color(0xFFDDDDDD),
    inputHint = Color(0xFF999999),
    devBg = Color(0xFFFFE5B4),
    devFg = Color(0xFF704000),
)

val darkTheme = Theme(
    name = "dark",
    bg = Color(0xFF17181B),
    fg = Color(0xFFE8E8EA),
    accent = Color(0xFF5C6BC0),
    accentText = Color(0xFFFFFFFF),
    card = Color(0xFF23252B),
    chipBg = Color(0xFF2B2E35),
    chipFg = Color(0xFFD8D8DC),
    border = Color(0xFF3A3D45),
    codeBg = Color(0xFF21232A),
    codeFg = Color(0xFFCFD2D8),
    subtle = Color(0xFFA0A4AD),
    muted = Color(0xFF7D818A),
    danger = Color(0xFFFF6B6B),
    imageBg = Color(0xFF33363D),
    inputHint = Color(0xFF6D717A),
    devBg = Color(0xFF5A3B1A),
    devFg = Color(0xFFFFD79A),
)

fun themeByName(name: String): Theme = if (name == "dark") darkTheme else lightTheme

// varsJSON is the CSS custom properties (without the leading `--`) that every
// napp receives on :root — the same tokens behavior.md documents.
fun themeVarsJSON(t: Theme): String {
    fun q(k: String, v: String) = "\"$k\":\"$v\""
    return "{" + listOf(
        q("surface", cssHex(t.bg)),
        q("surface-alt", cssHex(t.card)),
        q("text", cssHex(t.fg)),
        q("text-muted", cssHex(t.subtle)),
        q("text-faint", cssHex(t.muted)),
        q("border", cssHex(t.border)),
        q("chip", cssHex(t.chipBg)),
        q("chip-text", cssHex(t.chipFg)),
        q("accent", cssHex(t.accent)),
        q("accent-text", cssHex(t.accentText)),
        q("danger", cssHex(t.danger)),
        q("dev", cssHex(t.devBg)),
        q("dev-text", cssHex(t.devFg)),
    ).joinToString(",") + "}"
}

// compose takes the theme one step further, for the launcher's own screens.
fun Theme.compose(): ColorScheme {
    val base = if (name == "dark") darkColorScheme() else lightColorScheme()
    return base.copy(
        primary = accent,
        onPrimary = accentText,
        background = bg,
        onBackground = fg,
        surface = bg,
        onSurface = fg,
        surfaceVariant = card,
        onSurfaceVariant = subtle,
        outline = border,
        error = danger,
    )
}
